// Package archive fetches messages, threads, and channel metadata from a
// Discord guild and writes them as date/channel-partitioned JSONL files.
package archive

import (
	"errors"
	"fmt"
	"time"
)

// DefaultLocation is the IANA timezone used to partition archives by date.
const DefaultLocation = "Asia/Tokyo"

// Config holds everything needed to run one archive pass.
type Config struct {
	Token               string
	GuildID             string
	OutputDir           string
	IncludeThreads      bool
	IncludePrivate      bool
	DownloadAttachments bool
}

func (c Config) Validate() error {
	if c.Token == "" {
		return errors.New("missing Discord bot token: set DISCORD_BOT_TOKEN or pass -token")
	}
	if c.GuildID == "" {
		return errors.New("missing Discord guild ID: set DISCORD_GUILD_ID or pass -guild")
	}
	return nil
}

// Run archives the guild configured in config. When date is non-empty
// (YYYY-MM-DD, interpreted in DefaultLocation), only that day is archived and
// the existing date partition is atomically replaced.
func Run(config Config, date string) error {
	if err := config.Validate(); err != nil {
		return err
	}

	partitionLocation, err := time.LoadLocation(DefaultLocation)
	if err != nil {
		return fmt.Errorf("load %s location: %w", DefaultLocation, err)
	}

	filter, err := parseDateFilter(date, partitionLocation)
	if err != nil {
		return err
	}

	client, err := newSessionClient(config.Token)
	if err != nil {
		return err
	}
	defer client.Close()

	output, err := newArchiveOutput(config.OutputDir, config.GuildID, filter)
	if err != nil {
		return err
	}
	defer output.Cleanup()

	return runArchive(client, output, config, filter, partitionLocation)
}

// runArchive drives one pass: fetch via client, write via output. Failures on
// individual channels and threads are collected so one broken channel does not
// abort the rest, but any failure makes the whole pass return an error.
func runArchive(client discordClient, output archiveWriter, config Config, filter *dateFilter, partitionLocation *time.Location) error {
	a := &archiver{
		client:              client,
		guildID:             config.GuildID,
		includePrivate:      config.IncludePrivate,
		downloadAttachments: config.DownloadAttachments,
		partitionLocation:   partitionLocation,
		dateFilter:          filter,
		output:              output,
		attachments:         newAttachmentPool(),
		seenChannels:        make(map[string]struct{}),
		seenThreadMetadata:  make(map[string]struct{}),
	}

	channels, err := client.GuildChannels(config.GuildID)
	if err != nil {
		return fmt.Errorf("list guild channels: %w", err)
	}
	for _, channel := range channels {
		if err := output.WriteChannelMetadata(channelRecord{GuildID: config.GuildID, Channel: channel}); err != nil {
			return err
		}
	}

	for _, channel := range channels {
		if !canContainMessages(channel.Type) {
			continue
		}
		if err := a.archiveChannel(channel); err != nil {
			a.fail(fmt.Errorf("archive channel %s (%s): %w", channel.Name, channel.ID, err))
		}
	}

	if config.IncludeThreads {
		a.archiveThreads(channels)
	}

	failures := append(a.failures, a.attachments.wait()...)
	if err := output.Close(); err != nil {
		// Staged files may be truncated; do not publish them.
		failures = append(failures, err)
		return errors.Join(failures...)
	}
	// Commit even after per-channel failures so everything that was fetched
	// is kept; the joined error still marks the pass as failed.
	if err := output.Commit(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
