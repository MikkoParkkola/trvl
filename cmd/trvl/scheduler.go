package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Schedule constants for the daily deal-radar run. Shared across every
// platform installer so the launchd plist, systemd timer, and cron line all
// agree on 08:00 local.
const (
	scheduleHour   = 8
	scheduleMinute = 0
)

// installScheduler / uninstallScheduler are implemented per-platform in
// scheduler_darwin.go, scheduler_linux.go, and scheduler_other.go. The build
// system selects exactly one via GOOS build tags.

// resolveBinaryPath returns the absolute path of the running trvl binary so the
// scheduled job invokes the same binary the user installed. Cross-platform.
func resolveBinaryPath() string {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs
		}
		return exe
	}
	return "trvl"
}

// manualCronLine returns a crontab line the user can install by hand on any
// Unix-like system that has cron. Used by the graceful-fallback installer.
func manualCronLine(binaryPath string) string {
	return fmt.Sprintf("%d %d * * * %s digest", scheduleMinute, scheduleHour, binaryPath)
}
