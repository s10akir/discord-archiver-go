package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/s10akir/discord-archiver-go/internal/archive"
	"github.com/s10akir/discord-archiver-go/internal/cli"
	"github.com/s10akir/discord-archiver-go/internal/dotenv"
	"github.com/s10akir/discord-archiver-go/internal/scheduler"
	"github.com/s10akir/discord-archiver-go/internal/viewer"
)

func main() {
	if err := dotenv.Load(".env"); err != nil {
		log.Fatal(err)
	}

	config, err := cli.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	if err := run(config); err != nil {
		log.Fatal(err)
	}
}

func run(config cli.Config) error {
	switch config.Mode {
	case cli.ModeDaemon:
		if err := config.Archive.Validate(); err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return scheduler.Run(ctx, config.Schedule, func(date string) error {
			return archive.Run(config.Archive, date)
		})
	case cli.ModeDump:
		return archive.Run(config.Archive, config.Date)
	case cli.ModeView:
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return viewer.Run(ctx, config.Archive.OutputDir, config.ViewAddr)
	default:
		return fmt.Errorf("unknown command mode %q", config.Mode)
	}
}
