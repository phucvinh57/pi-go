package main

import (
	"context"
	"os"
	"os/signal"

	"pi-go/internal/cli"
	"pi-go/internal/commands"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := cli.New(commands.All)

	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
