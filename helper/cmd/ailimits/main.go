// Command ailimits shows your Claude Code usage limits on a Garmin watch.
// It reads the local Claude Code login (read-only) and sends only the usage
// percentages to the AI Limits relay. Your Claude token is sent only to Anthropic, never to the relay.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(newCLI().run(ctx, os.Args[1:]))
}
