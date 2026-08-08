//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// launchdLabel is the LaunchAgent label; aliased to the shared scheduler label
// so the committed plist and the installer agree.
const launchdLabel = schedulerLabel

// launchdPlistPath is the per-user LaunchAgents path for the deal-radar agent.
func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

// renderLaunchdPlist produces the LaunchAgent plist scheduling `trvl digest`
// daily at 08:00 via StartCalendarInterval. Deterministic given the binary
// path, so tests can assert the rendered command line and schedule.
func renderLaunchdPlist(binaryPath string) string {
	logDir := "${HOME}/Library/Logs"
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>digest</string>
	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>%d</integer>
		<key>Minute</key>
		<integer>%d</integer>
	</dict>
	<key>StandardOutPath</key>
	<string>%s/%s.out.log</string>
	<key>StandardErrorPath</key>
	<string>%s/%s.err.log</string>
	<key>RunAtLoad</key>
	<false/>
</dict>
</plist>
`, launchdLabel, binaryPath, scheduleHour, scheduleMinute, logDir, launchdLabel, logDir, launchdLabel)
}

func installScheduler() error {
	dst, err := launchdPlistPath()
	if err != nil {
		return err
	}
	plist := renderLaunchdPlist(resolveBinaryPath())
	// 0755/0644 kept for the LaunchAgents plist (trvl#532 medium triage). Two
	// reasons, and the second is why this is not merely risk-aversion: the file
	// carries a binary path and a schedule, no credentials and no journey data,
	// so there is nothing here worth hiding from another account. And 0644 is
	// the platform convention for ~/Library/LaunchAgents; tightening it risks
	// launchd declining to load the job, which would silently stop the
	// scheduler rather than fail loudly.
	// #nosec G301 -- 0755 is the macOS LaunchAgents directory convention; the
	// directory contains no credentials or journey data.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// #nosec G306 -- launchd plists are conventionally 0644 and contain only a
	// binary path and schedule, never credentials.
	if err := os.WriteFile(dst, []byte(plist), 0o644); err != nil {
		return err
	}
	// Load via launchctl bootstrap (modern) into the user's GUI domain.
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	// #nosec G204 -- fixed launchctl executable and locally generated argv.
	if out, err := exec.Command("launchctl", "bootstrap", uid, dst).CombinedOutput(); err != nil {
		// bootstrap fails if already loaded; fall back to legacy load.
		// #nosec G204 -- fixed launchctl executable and resolved plist path argv.
		if out2, err2 := exec.Command("launchctl", "load", dst).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl bootstrap/load failed: %s / %s", out, out2)
		}
	}
	fmt.Printf("deal-radar LaunchAgent installed at %s (daily %02d:%02d)\n", dst, scheduleHour, scheduleMinute)
	return nil
}

func uninstallScheduler() error {
	dst, err := launchdPlistPath()
	if err != nil {
		return err
	}
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	// bootout is the modern unload; ignore errors if not loaded.
	// #nosec G204 -- fixed launchctl executable and locally generated argv.
	_ = exec.Command("launchctl", "bootout", uid, dst).Run()
	// #nosec G204 -- fixed launchctl executable and resolved plist path argv.
	_ = exec.Command("launchctl", "unload", dst).Run()
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("deal-radar LaunchAgent uninstalled (%s)\n", dst)
	return nil
}
