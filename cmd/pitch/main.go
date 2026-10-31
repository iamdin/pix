package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/iamdin/pitch/internal/pitch"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(pitch.Main(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
