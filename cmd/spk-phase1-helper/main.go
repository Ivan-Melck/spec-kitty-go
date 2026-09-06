package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(helpercmd.RunIO(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
