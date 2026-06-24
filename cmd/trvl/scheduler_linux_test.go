//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestSystemdServiceRendersDigestCommand(t *testing.T) {
	svc := renderSystemdService("/opt/trvl/bin/trvl")
	if !strings.Contains(svc, "ExecStart=/opt/trvl/bin/trvl digest") {
		t.Errorf("service missing ExecStart digest line:\n%s", svc)
	}
	if !strings.Contains(svc, "Type=oneshot") {
		t.Errorf("service should be oneshot:\n%s", svc)
	}
}

func TestSystemdTimerRenders0800Daily(t *testing.T) {
	tmr := renderSystemdTimer()
	if !strings.Contains(tmr, "OnCalendar=*-*-* 08:00:00") {
		t.Errorf("timer missing 08:00 daily schedule:\n%s", tmr)
	}
	if !strings.Contains(tmr, "WantedBy=timers.target") {
		t.Errorf("timer missing install target:\n%s", tmr)
	}
}

func TestCronFallbackReturnsCleanErrorNotCrash(t *testing.T) {
	// When systemd is unavailable the installer must return a clear error
	// naming the manual crontab line — never panic, never silent no-op.
	err := installCronFallback("/opt/trvl/bin/trvl")
	if err == nil {
		t.Fatal("cron fallback should return an error guiding manual setup")
	}
	if !strings.Contains(err.Error(), "0 8 * * * /opt/trvl/bin/trvl digest") {
		t.Errorf("fallback error missing manual cron line: %v", err)
	}
}
