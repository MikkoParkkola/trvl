//go:build darwin

package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestDigestInstall_RendersPlistCommandLineAndSchedule(t *testing.T) {
	plist := renderLaunchdPlist("/opt/trvl/bin/trvl")
	// Command line: the installed agent must invoke `<binary> digest`.
	for _, want := range []string{"<string>/opt/trvl/bin/trvl</string>", "<string>digest</string>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}
	// Schedule: 08:00 via StartCalendarInterval.
	if m, _ := regexp.MatchString(`<key>Hour</key>\s*<integer>8</integer>`, plist); !m {
		t.Errorf("plist missing Hour=8:\n%s", plist)
	}
	if m, _ := regexp.MatchString(`<key>Minute</key>\s*<integer>0</integer>`, plist); !m {
		t.Errorf("plist missing Minute=0:\n%s", plist)
	}
	if !strings.Contains(plist, launchdLabel) {
		t.Errorf("plist missing label %q", launchdLabel)
	}
}

func TestDigestInstall_PlistPathUnderLaunchAgents(t *testing.T) {
	p, err := launchdPlistPath()
	if err != nil {
		t.Fatalf("launchdPlistPath: %v", err)
	}
	if !strings.Contains(p, "Library/LaunchAgents") {
		t.Errorf("plist path %q not under Library/LaunchAgents", p)
	}
	if !strings.HasSuffix(p, launchdLabel+".plist") {
		t.Errorf("plist path %q does not end with %s.plist", p, launchdLabel)
	}
}

func TestDigestDaemon_RenderedPlistHasNoLaunchctl(t *testing.T) {
	// The rendered plist artifact must not embed launchctl invocations; the
	// installer issues launchctl separately. Asserts AC.5 without invoking
	// launchctl.
	plist := renderLaunchdPlist(resolveBinaryPath())
	if strings.Contains(plist, "launchctl") {
		t.Error("rendered plist should not embed launchctl invocations")
	}
}
