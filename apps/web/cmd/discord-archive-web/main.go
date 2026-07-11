package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/s10akir/discord-archiver-go/apps/web/internal/cli"
	"github.com/s10akir/discord-archiver-go/apps/web/internal/dotenv"
	"github.com/s10akir/discord-archiver-go/apps/web/internal/web"
)

func main() {
	if err := dotenv.Load(".env"); err != nil {
		log.Fatal(err)
	}
	config, err := cli.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := web.Run(ctx, config.ArchiveDir, config.DatabaseURL, config.Addr); err != nil {
		log.Fatal(err)
	}
}
