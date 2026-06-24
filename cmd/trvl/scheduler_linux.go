//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// systemdUnitName is the user-level systemd unit base name (service + timer
// share it). Aliased to the shared scheduler label.
const systemdUnitName = schedulerLabel

// systemdUserDir is the per-user systemd unit directory.
func systemdUserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// renderSystemdService renders the oneshot service that runs `trvl digest`.
func renderSystemdService(binaryPath string) string {
	return fmt.Sprintf(`[Unit]
Description=trvl daily deal-radar digest

[Service]
Type=oneshot
ExecStart=%s digest
`, binaryPath)
}

// renderSystemdTimer renders the timer firing daily at 08:00 local.
func renderSystemdTimer() string {
	return fmt.Sprintf(`[Unit]
Description=trvl daily deal-radar digest timer

[Timer]
OnCalendar=*-*-* %02d:%02d:00
Persistent=true

[Install]
WantedBy=timers.target
`, scheduleHour, scheduleMinute)
}

// hasSystemd reports whether a usable systemctl --user is available.
func hasSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// `systemctl --user` only works with a running user manager (a session
	// bus). Probe cheaply; if it errors we fall back to cron.
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func installScheduler() error {
	binary := resolveBinaryPath()
	if hasSystemd() {
		return installSystemd(binary)
	}
	return installCronFallback(binary)
}

func installSystemd(binary string) error {
	dir, err := systemdUserDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	svc := filepath.Join(dir, systemdUnitName+".service")
	tmr := filepath.Join(dir, systemdUnitName+".timer")
	if err := os.WriteFile(svc, []byte(renderSystemdService(binary)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(tmr, []byte(renderSystemdTimer()), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", systemdUnitName+".timer").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user enable failed: %s", out)
	}
	fmt.Printf("deal-radar systemd timer installed (%s, daily %02d:%02d)\n", tmr, scheduleHour, scheduleMinute)
	return nil
}

// installCronFallback prints the crontab line for systems without systemd.
// It does not edit the user's crontab automatically (that is a destructive,
// hard-to-undo edit); instead it gives the exact line to add.
func installCronFallback(binary string) error {
	return fmt.Errorf("systemd --user is not available on this system; add this crontab line manually with 'crontab -e': %s", manualCronLine(binary))
}

func uninstallScheduler() error {
	if hasSystemd() {
		dir, err := systemdUserDir()
		if err != nil {
			return err
		}
		_ = exec.Command("systemctl", "--user", "disable", "--now", systemdUnitName+".timer").Run()
		svc := filepath.Join(dir, systemdUnitName+".service")
		tmr := filepath.Join(dir, systemdUnitName+".timer")
		var firstErr error
		for _, f := range []string{tmr, svc} {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		if firstErr != nil {
			return firstErr
		}
		fmt.Println("deal-radar systemd timer uninstalled")
		return nil
	}
	fmt.Printf("no systemd timer to remove; if you added a crontab line, remove it manually (crontab -e):\n\n    %s\n", manualCronLine(resolveBinaryPath()))
	return nil
}
