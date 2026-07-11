package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/s10akir/discord-archiver-go/apps/importer/internal/cli"
	"github.com/s10akir/discord-archiver-go/apps/importer/internal/importer"
)

func main() {
	config, err := cli.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := importer.New(config).Run(ctx); err != nil {
		log.Fatal(err)
	}
}
