//go:build !darwin && !linux

package main

import (
	"strings"
	"testing"
)

func TestUnsupportedPlatformInstallReturnsCleanError(t *testing.T) {
	// installScheduler on an unsupported platform must return a non-nil error
	// (not panic, not silently succeed) and name the manual scheduling path.
	err := installScheduler()
	if err == nil {
		t.Fatal("install on unsupported platform should return a guiding error")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should explain it is unimplemented here: %v", err)
	}
}

func TestUnsupportedPlatformUninstallReturnsCleanError(t *testing.T) {
	if err := uninstallScheduler(); err == nil {
		t.Fatal("uninstall on unsupported platform should return a guiding error")
	}
}

func TestManualScheduleHintNonEmpty(t *testing.T) {
	if strings.TrimSpace(manualScheduleHint()) == "" {
		t.Error("manualScheduleHint should give the user a manual schedule command")
	}
}
