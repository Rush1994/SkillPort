package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Rush1994/SkillPort/internal/cli"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.BuildInfo{
		Version: version, Commit: commit, BuildDate: buildDate,
	}))
}
