// Package cli parses web command-line arguments and environment variables.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

const defaultAddr = ":8080"

type Config struct {
	ArchiveDir string
	Addr       string
}

func Parse(args []string, getenv func(string) string) (Config, error) {
	addr := strings.TrimSpace(getenv("DISCORD_ARCHIVE_WEB_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	config := Config{ArchiveDir: "archive", Addr: addr}
	flags := flag.NewFlagSet("discord-archive-web", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.ArchiveDir, "out-dir", config.ArchiveDir, "Archive directory to browse.")
	flags.StringVar(&config.Addr, "addr", config.Addr, "Address to serve the web app on.")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return config, nil
}
