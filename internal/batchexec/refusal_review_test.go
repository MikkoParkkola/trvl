package batchexec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Review fixes for the Google cooldown (MIK-8036).

func TestGoogle429WithUnreadableBodyStillStops(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// Promise a longer body than is sent, then hang up: the read fails.
		_, _ = buf.WriteString("HTTP/1.1 429 Too Many Requests\r\nContent-Length: 100\r\n\r\nshort")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer ts.Close()

	c := NewTestClient(ts.URL)
	c.refusalDir = t.TempDir()
	_, _, _ = c.PostForm(context.Background(), FlightsURL, "f.req=one")
	if hits.Load() != 1 {
		t.Fatalf("a 429 must not be retried even when its body is unreadable; upstream calls = %d", hits.Load())
	}
	if _, _, err := c.PostForm(context.Background(), FlightsURL, "f.req=two"); !errors.Is(err, ErrRefused) {
		t.Fatalf("the 429 must arm the cooldown; second call err = %v", err)
	}
}

func TestRefusalWaitHugeHintIsCappedNotWrapped(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	// 18446744074 * time.Second overflows int64 and used to wrap to ~0.29s.
	if got := googleRefusalWait(http.StatusTooManyRequests, "18446744074", nil, now); got != googleRefusalCap {
		t.Fatalf("huge Retry-After = %s, want the cap %s", got, googleRefusalCap)
	}
	if got := capSeconds(18446744074); got != googleRefusalCap {
		t.Fatalf("capSeconds(huge) = %s, want the cap", got)
	}
	if got := capSeconds(30); got != 30*time.Second {
		t.Fatalf("capSeconds(30) = %s", got)
	}
}

func TestRestoredDeadlineIsBoundedByTheCap(t *testing.T) {
	dir := t.TempDir()
	far := time.Now().Add(5 * time.Hour)
	if err := os.WriteFile(filepath.Join(dir, googleRefusalFilename), []byte(far.Format(time.RFC3339Nano)), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewTestClient("http://127.0.0.1:1")
	c.refusalDir = dir
	if _, _, err := c.PostForm(context.Background(), FlightsURL, "f.req=x"); !errors.Is(err, ErrRefused) {
		t.Fatalf("a stored future deadline must still refuse, got %v", err)
	}
	limit := time.Now().Add(googleRefusalCap + time.Second)
	if c.refuseUntil.After(limit) {
		t.Fatalf("restored deadline %s is beyond the %s cap", time.Until(c.refuseUntil).Round(time.Second), googleRefusalCap)
	}
	if disk := readRefusalDeadline(t, dir); disk.After(limit) {
		t.Fatalf("stored deadline must be rewritten within the cap, still %s ahead", time.Until(disk).Round(time.Second))
	}
}

func TestBeginGoogleHonoursCancellation(t *testing.T) {
	dir := t.TempDir()
	c := NewTestClient("http://127.0.0.1:1")
	c.refusalDir = dir
	release, refused, err := c.beginGoogle(context.Background())
	if err != nil || refused {
		t.Fatalf("first admission: refused=%v err=%v", refused, err)
	}
	defer release()

	check := func(name string, client *Client) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, _, err := client.beginGoogle(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: waiting admission must end with the context, got %v", name, err)
		}
		if waited := time.Since(start); waited > 2*time.Second {
			t.Fatalf("%s: admission ignored the deadline, waited %s", name, waited)
		}
	}
	// Same client: waits on the in-process gate.
	check("same client", c)
	// Another client sharing the directory: waits on the file lock.
	other := NewTestClient("http://127.0.0.1:1")
	other.refusalDir = dir
	check("other client", other)
}
