package archive

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

type archiver struct {
	client              discordClient
	guildID             string
	includePrivate      bool
	downloadAttachments bool
	partitionLocation   *time.Location
	dateFilter          *dateFilter
	output              archiveWriter
	attachments         *attachmentPool
	seenChannels        map[string]struct{}
	seenThreadMetadata  map[string]struct{}
	failures            []error
}

// fail records a per-channel/per-thread failure and lets the pass continue;
// runArchive joins the collected failures into the final error.
func (a *archiver) fail(err error) {
	log.Print(err)
	a.failures = append(a.failures, err)
}

func canContainMessages(channelType discordgo.ChannelType) bool {
	switch channelType {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildVoice,
		discordgo.ChannelTypeGuildNewsThread,
		discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread:
		return true
	default:
		return false
	}
}

func canContainThreads(channelType discordgo.ChannelType) bool {
	switch channelType {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildForum,
		discordgo.ChannelTypeGuildMedia:
		return true
	default:
		return false
	}
}

func (a *archiver) archiveChannel(channel *discordgo.Channel) error {
	if channel == nil {
		return nil
	}
	if _, ok := a.seenChannels[channel.ID]; ok {
		return nil
	}
	a.seenChannels[channel.ID] = struct{}{}

	log.Printf("archiving channel %s (%s)", channel.Name, channel.ID)

	var beforeID string
	for {
		messages, err := a.client.ChannelMessages(channel.ID, 100, beforeID)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}

		stopChannel := false
		for _, message := range messages {
			messageTime, err := messageTimestamp(message)
			if err != nil {
				return err
			}

			include, olderThanFilter := a.shouldArchiveMessage(messageTime)
			if olderThanFilter {
				stopChannel = true
				break
			}
			if !include {
				continue
			}

			record := archiveRecord{
				GuildID:     a.guildID,
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
				ChannelType: channel.Type,
				ParentID:    channel.ParentID,
				Message:     message,
			}
			date := partitionDate(messageTime, a.partitionLocation)
			if err := a.output.WriteMessage(date, channel.ID, record); err != nil {
				return err
			}
			a.queueAttachments(date, channel.ID, message)
		}

		if stopChannel {
			return nil
		}
		beforeID = messages[len(messages)-1].ID
	}
}

func (a *archiver) shouldArchiveMessage(messageTime time.Time) (include bool, olderThanFilter bool) {
	if a.dateFilter == nil {
		return true, false
	}
	if messageTime.Before(a.dateFilter.Start) {
		return false, true
	}
	if !messageTime.Before(a.dateFilter.End) {
		return false, false
	}
	return true, false
}

func messageTimestamp(message *discordgo.Message) (time.Time, error) {
	if message == nil {
		return time.Time{}, errors.New("nil message")
	}
	if !message.Timestamp.IsZero() {
		return message.Timestamp, nil
	}

	timestamp, err := discordgo.SnowflakeTimestamp(message.ID)
	if err != nil {
		return time.Time{}, fmt.Errorf("derive message timestamp from snowflake %q: %w", message.ID, err)
	}
	return timestamp, nil
}

func (a *archiver) archiveThreads(parentChannels []*discordgo.Channel) {
	activeThreads, err := a.client.GuildThreadsActive(a.guildID)
	if err != nil {
		a.fail(fmt.Errorf("list active threads: %w", err))
	} else {
		for _, thread := range activeThreads.Threads {
			a.archiveThread("active", thread)
		}
	}

	for _, parent := range parentChannels {
		if !canContainThreads(parent.Type) {
			continue
		}
		if err := a.archiveArchivedThreads("public_archived", parent.ID, a.client.ThreadsArchived); err != nil {
			a.fail(fmt.Errorf("archive public archived threads for %s (%s): %w", parent.Name, parent.ID, err))
		}
		if a.includePrivate {
			if err := a.archiveArchivedThreads("private_archived", parent.ID, a.client.ThreadsPrivateArchived); err != nil {
				a.fail(fmt.Errorf("archive private archived threads for %s (%s): %w", parent.Name, parent.ID, err))
			}
		}
	}
}

func (a *archiver) archiveThread(source string, thread *discordgo.Channel) {
	if thread == nil {
		return
	}
	if err := a.writeThreadMetadata(source, thread); err != nil {
		a.fail(fmt.Errorf("write %s thread metadata %s (%s): %w", source, thread.Name, thread.ID, err))
	}
	if err := a.archiveChannel(thread); err != nil {
		a.fail(fmt.Errorf("archive %s thread %s (%s): %w", source, thread.Name, thread.ID, err))
	}
}

func (a *archiver) writeThreadMetadata(source string, thread *discordgo.Channel) error {
	if thread == nil {
		return nil
	}
	if _, ok := a.seenThreadMetadata[thread.ID]; ok {
		return nil
	}
	a.seenThreadMetadata[thread.ID] = struct{}{}
	return a.output.WriteThreadMetadata(threadRecord{GuildID: a.guildID, Source: source, Thread: thread})
}

func (a *archiver) archiveArchivedThreads(source, parentChannelID string, fetch func(channelID string, before *time.Time, limit int) (*discordgo.ThreadsList, error)) error {
	var before *time.Time
	for {
		threads, err := fetch(parentChannelID, before, 100)
		if err != nil {
			return err
		}
		if len(threads.Threads) == 0 {
			return nil
		}

		for _, thread := range threads.Threads {
			a.archiveThread(source, thread)
		}
		if !threads.HasMore {
			return nil
		}

		before = oldestArchiveTimestamp(threads.Threads)
		if before == nil {
			return nil
		}
	}
}

func oldestArchiveTimestamp(threads []*discordgo.Channel) *time.Time {
	var oldest *time.Time
	for _, thread := range threads {
		if thread == nil || thread.ThreadMetadata == nil || thread.ThreadMetadata.ArchiveTimestamp.IsZero() {
			continue
		}

		timestamp := thread.ThreadMetadata.ArchiveTimestamp
		if oldest == nil || timestamp.Before(*oldest) {
			oldest = &timestamp
		}
	}
	return oldest
}
