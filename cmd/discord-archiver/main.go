package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type archiveRecord struct {
	GuildID     string                `json:"guild_id"`
	ChannelID   string                `json:"channel_id"`
	ChannelName string                `json:"channel_name,omitempty"`
	ChannelType discordgo.ChannelType `json:"channel_type"`
	ParentID    string                `json:"parent_id,omitempty"`
	Message     *discordgo.Message    `json:"message"`
}

type archiver struct {
	session        *discordgo.Session
	guildID        string
	includeThreads bool
	includePrivate bool
	encoder        *json.Encoder
	seenChannels   map[string]struct{}
}

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}

	var (
		token          = flag.String("token", os.Getenv("DISCORD_BOT_TOKEN"), "Discord bot token. Defaults to DISCORD_BOT_TOKEN.")
		guildID        = flag.String("guild", os.Getenv("DISCORD_GUILD_ID"), "Discord guild/server ID. Defaults to DISCORD_GUILD_ID.")
		outputPath     = flag.String("out", "discord-archive.jsonl", "Output JSONL file path.")
		includeThreads = flag.Bool("threads", true, "Include active and archived threads.")
		includePrivate = flag.Bool("private-threads", false, "Include private archived threads visible to the bot.")
	)
	flag.Parse()

	if err := run(*token, *guildID, *outputPath, *includeThreads, *includePrivate); err != nil {
		log.Fatal(err)
	}
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid .env line: %q", line)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("invalid .env line with empty key: %q", line)
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")

		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from .env: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return nil
}

func run(token, guildID, outputPath string, includeThreads, includePrivate bool) error {
	if token == "" {
		return errors.New("missing Discord bot token: set DISCORD_BOT_TOKEN or pass -token")
	}
	if guildID == "" {
		return errors.New("missing Discord guild ID: set DISCORD_GUILD_ID or pass -guild")
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return fmt.Errorf("create discord session: %w", err)
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	a := &archiver{
		session:        session,
		guildID:        guildID,
		includeThreads: includeThreads,
		includePrivate: includePrivate,
		encoder:        json.NewEncoder(out),
		seenChannels:   make(map[string]struct{}),
	}

	channels, err := session.GuildChannels(guildID)
	if err != nil {
		return fmt.Errorf("list guild channels: %w", err)
	}

	for _, channel := range channels {
		if !canContainMessages(channel.Type) {
			continue
		}
		if err := a.archiveChannel(channel); err != nil {
			log.Printf("archive channel %s (%s): %v", channel.Name, channel.ID, err)
		}
	}

	if includeThreads {
		if err := a.archiveThreads(channels); err != nil {
			return err
		}
	}

	return nil
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
		messages, err := a.session.ChannelMessages(channel.ID, 100, beforeID, "", "")
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}

		for _, message := range messages {
			record := archiveRecord{
				GuildID:     a.guildID,
				ChannelID:   channel.ID,
				ChannelName: channel.Name,
				ChannelType: channel.Type,
				ParentID:    channel.ParentID,
				Message:     message,
			}
			if err := a.encoder.Encode(record); err != nil {
				return err
			}
		}

		beforeID = messages[len(messages)-1].ID
	}
}

func (a *archiver) archiveThreads(parentChannels []*discordgo.Channel) error {
	activeThreads, err := a.session.GuildThreadsActive(a.guildID)
	if err != nil {
		return fmt.Errorf("list active threads: %w", err)
	}
	for _, thread := range activeThreads.Threads {
		if err := a.archiveChannel(thread); err != nil {
			log.Printf("archive active thread %s (%s): %v", thread.Name, thread.ID, err)
		}
	}

	for _, parent := range parentChannels {
		if !canContainThreads(parent.Type) {
			continue
		}
		if err := a.archivePublicArchivedThreads(parent.ID); err != nil {
			log.Printf("archive public archived threads for %s (%s): %v", parent.Name, parent.ID, err)
		}
		if a.includePrivate {
			if err := a.archivePrivateArchivedThreads(parent.ID); err != nil {
				log.Printf("archive private archived threads for %s (%s): %v", parent.Name, parent.ID, err)
			}
		}
	}

	return nil
}

func (a *archiver) archivePublicArchivedThreads(parentChannelID string) error {
	var before *time.Time
	for {
		threads, err := a.session.ThreadsArchived(parentChannelID, before, 100)
		if err != nil {
			return err
		}
		if len(threads.Threads) == 0 {
			return nil
		}

		for _, thread := range threads.Threads {
			if err := a.archiveChannel(thread); err != nil {
				log.Printf("archive public archived thread %s (%s): %v", thread.Name, thread.ID, err)
			}
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

func (a *archiver) archivePrivateArchivedThreads(parentChannelID string) error {
	var before *time.Time
	for {
		threads, err := a.session.ThreadsPrivateArchived(parentChannelID, before, 100)
		if err != nil {
			return err
		}
		if len(threads.Threads) == 0 {
			return nil
		}

		for _, thread := range threads.Threads {
			if err := a.archiveChannel(thread); err != nil {
				log.Printf("archive private archived thread %s (%s): %v", thread.Name, thread.ID, err)
			}
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
