//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

// installScheduler on unsupported platforms (Windows and others) fails
// gracefully: it names the platform and prints the equivalent manual schedule
// the user can set up themselves. It never crashes and never silently no-ops.
func installScheduler() error {
	return fmt.Errorf("automatic scheduler install is not implemented on %s; %s", runtime.GOOS, manualScheduleHint())
}

func uninstallScheduler() error {
	return fmt.Errorf("automatic scheduler uninstall is not implemented on %s; remove the scheduled job you created manually (%s)", runtime.GOOS, manualScheduleHint())
}

// manualScheduleHint returns platform-appropriate guidance for scheduling
// `trvl digest` at 08:00 by hand.
func manualScheduleHint() string {
	binary := resolveBinaryPath()
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("schedule it with Task Scheduler, e.g.:\n\n    schtasks /Create /SC DAILY /ST %02d:%02d /TN trvl-dealradar /TR \"%s digest\"\n", scheduleHour, scheduleMinute, binary)
	}
	return fmt.Sprintf("add this crontab line manually (crontab -e):\n\n    %s\n", manualCronLine(binary))
}
