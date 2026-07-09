// Package archive fetches messages, threads, and channel metadata from a
// Discord guild and writes them as date/channel-partitioned JSONL files.
package archive

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

// DefaultLocation is the IANA timezone used to partition archives by date.
const DefaultLocation = "Asia/Tokyo"

// Config holds everything needed to run one archive pass.
type Config struct {
	Token          string
	GuildID        string
	OutputDir      string
	IncludeThreads bool
	IncludePrivate bool
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

	session, err := discordgo.New("Bot " + config.Token)
	if err != nil {
		return fmt.Errorf("create discord session: %w", err)
	}

	output, err := newArchiveOutput(config.OutputDir, config.GuildID, filter)
	if err != nil {
		return err
	}
	defer output.Cleanup()

	a := &archiver{
		session:            session,
		guildID:            config.GuildID,
		includeThreads:     config.IncludeThreads,
		includePrivate:     config.IncludePrivate,
		partitionLocation:  partitionLocation,
		dateFilter:         filter,
		output:             output,
		seenChannels:       make(map[string]struct{}),
		seenThreadMetadata: make(map[string]struct{}),
	}

	channels, err := session.GuildChannels(config.GuildID)
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
			log.Printf("archive channel %s (%s): %v", channel.Name, channel.ID, err)
		}
	}

	if config.IncludeThreads {
		if err := a.archiveThreads(channels); err != nil {
			return err
		}
	}

	if err := output.Close(); err != nil {
		return err
	}
	if err := output.Commit(); err != nil {
		return err
	}

	return nil
}
