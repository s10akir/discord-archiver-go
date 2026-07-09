// Package cli parses command-line arguments and environment variables into a
// runnable configuration.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/s10akir/discord-archiver-go/internal/archive"
	"github.com/s10akir/discord-archiver-go/internal/scheduler"
)

const defaultScheduleTime = "03:00"
const defaultViewAddr = ":8080"

type Mode string

const (
	ModeDaemon Mode = "daemon"
	ModeDump   Mode = "dump"
	ModeView   Mode = "view"
)

type Config struct {
	Mode     Mode
	Archive  archive.Config
	Date     string
	Schedule scheduler.Config
	ViewAddr string
}

// Parse builds a Config from args (without the program name) and getenv.
// Flags take precedence over environment variables.
func Parse(args []string, getenv func(string) string) (Config, error) {
	mode := ModeDaemon
	if len(args) > 0 {
		switch args[0] {
		case "daemon":
			args = args[1:]
		case "dump":
			mode = ModeDump
			args = args[1:]
		case "view":
			mode = ModeView
			args = args[1:]
		}
	}

	config := Config{
		Mode: mode,
		Archive: archive.Config{
			DownloadAttachments: envBoolDefault(getenv("DISCORD_ARCHIVER_DOWNLOAD_ATTACHMENTS"), true),
		},
		Schedule: scheduler.Config{
			Time:       valueOrDefault(getenv("DISCORD_ARCHIVER_SCHEDULE_TIME"), defaultScheduleTime),
			Timezone:   valueOrDefault(getenv("TZ"), archive.DefaultLocation),
			RunOnStart: envBoolDefault(getenv("DISCORD_ARCHIVER_RUN_ON_START"), true),
		},
		ViewAddr: valueOrDefault(getenv("DISCORD_ARCHIVER_VIEW_ADDR"), defaultViewAddr),
	}

	flags := flag.NewFlagSet("discord-archiver", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	if mode == ModeView {
		flags.StringVar(&config.Archive.OutputDir, "out-dir", "archive", "Archive directory to browse.")
		flags.StringVar(&config.ViewAddr, "addr", config.ViewAddr, "Address to serve the viewer on.")
		if err := flags.Parse(args); err != nil {
			return Config{}, err
		}
		if flags.NArg() > 0 {
			return Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		return config, nil
	}

	excludePrivate := addArchiveFlags(flags, &config.Archive)

	switch mode {
	case ModeDaemon:
		var noRunOnStart bool
		flags.StringVar(&config.Schedule.Time, "schedule-time", config.Schedule.Time, "Daily archive time in HH:MM.")
		flags.StringVar(&config.Schedule.Timezone, "timezone", config.Schedule.Timezone, "Schedule timezone IANA name.")
		flags.BoolVar(&config.Schedule.RunOnStart, "run-on-start", config.Schedule.RunOnStart, "Run yesterday archive immediately on daemon start.")
		flags.BoolVar(&noRunOnStart, "no-run-on-start", false, "Skip the immediate archive on daemon start.")
		if err := flags.Parse(args); err != nil {
			return Config{}, err
		}
		if flags.NArg() > 0 {
			return Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		if noRunOnStart {
			config.Schedule.RunOnStart = false
		}
		resolveArchiveConfig(&config.Archive, *excludePrivate, getenv)
		if _, err := scheduler.ParseClock(config.Schedule.Time); err != nil {
			return Config{}, err
		}
		if _, err := time.LoadLocation(config.Schedule.Timezone); err != nil {
			return Config{}, fmt.Errorf("load schedule timezone %q: %w", config.Schedule.Timezone, err)
		}
		return config, nil
	case ModeDump:
		var all bool
		flags.BoolVar(&all, "all", false, "Archive all visible history.")
		flags.StringVar(&config.Date, "date", "", "JST date to refresh in YYYY-MM-DD format.")
		if err := flags.Parse(args); err != nil {
			return Config{}, err
		}
		if all == (strings.TrimSpace(config.Date) != "") {
			return Config{}, errors.New("dump requires exactly one of -all or -date")
		}
		if flags.NArg() > 0 {
			return Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		resolveArchiveConfig(&config.Archive, *excludePrivate, getenv)
		return config, nil
	default:
		return Config{}, fmt.Errorf("unknown command mode %q", mode)
	}
}

func addArchiveFlags(flags *flag.FlagSet, config *archive.Config) (excludePrivate *bool) {
	flags.StringVar(&config.Token, "token", "", "Discord bot token. Defaults to DISCORD_BOT_TOKEN.")
	flags.StringVar(&config.GuildID, "guild", "", "Discord guild/server ID. Defaults to DISCORD_GUILD_ID.")
	flags.StringVar(&config.OutputDir, "out-dir", "archive", "Output archive directory path.")
	flags.BoolVar(&config.IncludeThreads, "threads", true, "Include active and archived threads.")
	flags.BoolVar(&config.DownloadAttachments, "attachments", config.DownloadAttachments, "Download message attachments alongside their JSON records.")
	return flags.Bool("no-private-threads", false, "Exclude private archived threads visible to the bot.")
}

func resolveArchiveConfig(config *archive.Config, excludePrivate bool, getenv func(string) string) {
	config.Token = valueOrDefault(config.Token, getenv("DISCORD_BOT_TOKEN"))
	config.GuildID = valueOrDefault(config.GuildID, getenv("DISCORD_GUILD_ID"))
	config.IncludePrivate = !excludePrivate
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func envBoolDefault(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return fallback
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
