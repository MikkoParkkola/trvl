// Package main is the entrypoint for the trvl CLI.
package main

import (
	"context"
	"os"

	"github.com/MikkoParkkola/trvl/internal/selfupdate"
	"github.com/MikkoParkkola/trvl/internal/telemetry"
)

func main() {
	// Fire-and-forget daily update check. Returns immediately; the
	// goroutine writes a one-line stderr notice on the NEXT invocation
	// once the cache is warm. Skipped automatically for dev builds and
	// CI environments. Bounded to 6 s so trvl's actual exit isn't
	// noticeably delayed even if GitHub is slow.
	ctx, cancel := context.WithCancel(context.Background())
	selfupdate.CheckInBackground(ctx, Version, os.Stderr)
	defer cancel()

	// Fire-and-forget anonymous active-user heartbeat (at most once / 24h).
	// Uses a non-cancellable context so a fast exit doesn't abort it mid-send;
	// the 3s client timeout bounds it. Opt out with TRVL_NO_TELEMETRY /
	// NO_TELEMETRY / DO_NOT_TRACK; auto-skipped for CI and dev builds. See
	// telemetry.HeartbeatInBackground (MIK-6568).
	telemetry.HeartbeatInBackground(context.Background(), Version)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
