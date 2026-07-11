package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/s10akir/discord-archiver-go/apps/importer/internal/importer"
)

func Parse(args []string, getenv func(string) string) (importer.Config, error) {
	config := importer.Config{ArchiveDir: "archive", WebURL: strings.TrimSpace(getenv("DISCORD_ARCHIVE_WEB_URL")), Interval: 30 * time.Second, HTTPTimeout: 5 * time.Minute}
	flags := flag.NewFlagSet("discord-archive-importer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.ArchiveDir, "archive-dir", config.ArchiveDir, "Archive directory to scan.")
	flags.StringVar(&config.StateFile, "state-file", config.StateFile, "Path used to persist synchronized content hashes (defaults inside archive-dir).")
	flags.StringVar(&config.WebURL, "web-url", config.WebURL, "Web application base URL.")
	flags.DurationVar(&config.Interval, "interval", config.Interval, "Archive scan interval.")
	flags.DurationVar(&config.HTTPTimeout, "http-timeout", config.HTTPTimeout, "Import request timeout.")
	if err := flags.Parse(args); err != nil {
		return importer.Config{}, err
	}
	if flags.NArg() != 0 {
		return importer.Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if config.WebURL == "" {
		return importer.Config{}, fmt.Errorf("web URL is required")
	}
	if config.Interval <= 0 || config.HTTPTimeout <= 0 {
		return importer.Config{}, fmt.Errorf("durations must be positive")
	}
	if config.StateFile == "" {
		config.StateFile = filepath.Join(config.ArchiveDir, ".discord-archive-importer-state.json")
	}
	return config, nil
}
