package watch

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TRVL.ORPHAN.1 -- a meaningful pile of interrupted-write temp files must be
// reported, not left to accumulate silently.
//
// atomicjson writes temp-then-rename and cleans up on its error paths, but
// nothing survives a SIGKILL, so a process killed mid-write leaves a full copy
// of the target behind. One machine accumulated 7 files and 149MB in ~/.trvl
// before anyone noticed (trvl#513). `trvl tempfiles --delete` could always
// reclaim them; the gap was that nobody knew to look.
//
// Reports only. Automatic deletion is deliberately not done here -- see
// reportOrphanedTemps and atomicjson.Clean for why a PID-based liveness check
// is not safe to act on across hosts or reboots.
func TestSchedulerReportsOrphanedTemps(t *testing.T) {
	dir := t.TempDir()

	// The name shape atomicjson leaves behind: <target>.tmp-<pid>-<nonce>.
	// A PID that cannot be running, so the file reads as reclaimable.
	orphan := filepath.Join(dir, "price-history.json.tmp-999999-f3a91c")
	if err := os.WriteFile(orphan, make([]byte, 70<<20), 0o600); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatalf("age orphan: %v", err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	s := NewScheduler(dir, time.Hour, NoopChecker{})
	s.reportOrphanedTemps()

	out := buf.String()
	if !strings.Contains(out, "temp files behind") {
		t.Errorf("70MB of orphaned temp files went unreported; a user who does not know they exist "+
			"never runs the reclaim command. Log was: %s", out)
	}
	if !strings.Contains(out, "trvl tempfiles") {
		t.Errorf("the warning does not name the command that reclaims them: %s", out)
	}
}

// TRVL.ORPHAN.2 -- a single small leftover, or a large but RECENT one that may
// still be an in-flight write, must NOT warn. A warning that fires on the
// normal case stops being read.
func TestSchedulerStaysQuietOnSmallOrRecentTemps(t *testing.T) {
	dir := t.TempDir()

	// Small and old: below the size threshold.
	small := filepath.Join(dir, "date-grids.json.tmp-999999-7b2e04")
	if err := os.WriteFile(small, make([]byte, 1024), 0o600); err != nil {
		t.Fatalf("seed small: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(small, old, old); err != nil {
		t.Fatalf("age small: %v", err)
	}

	// Large but recent: may still be a write in progress.
	fresh := filepath.Join(dir, "price-history.json.tmp-999998-c81d5a")
	if err := os.WriteFile(fresh, make([]byte, 70<<20), 0o600); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	s := NewScheduler(dir, time.Hour, NoopChecker{})
	s.reportOrphanedTemps()

	if strings.Contains(buf.String(), "temp files behind") {
		t.Errorf("warned about a 1KB leftover and an in-flight write; a warning that fires on the "+
			"normal case stops being read. Log was: %s", buf.String())
	}
}
