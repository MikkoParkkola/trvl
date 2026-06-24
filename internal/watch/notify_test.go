package watch

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestNotify_Error(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch: Watch{Origin: "HEL", Destination: "BCN", Type: "flight"},
		Error: fmt.Errorf("network error"),
	})
	out := buf.String()
	if !strings.Contains(out, "ERR") || !strings.Contains(out, "network error") {
		t.Errorf("expected error output, got %q", out)
	}
}

func TestNotify_NoPrice(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch:    Watch{Origin: "HEL", Destination: "BCN", Type: "flight"},
		NewPrice: 0,
	})
	out := buf.String()
	if !strings.Contains(out, "no price data") {
		t.Errorf("expected 'no price data', got %q", out)
	}
}

func TestNotify_BelowGoal(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch:     Watch{Origin: "HEL", Destination: "BCN", Type: "flight", BelowPrice: 200, DepartDate: "2026-07-01"},
		NewPrice:  150,
		Currency:  "EUR",
		BelowGoal: true,
	})
	out := buf.String()
	if !strings.Contains(out, "DEAL") || !strings.Contains(out, "150") {
		t.Errorf("expected deal alert, got %q", out)
	}
	if !strings.Contains(out, "Book:") {
		t.Errorf("expected booking URL, got %q", out)
	}
}

func TestNotify_PriceDrop(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch:     Watch{Origin: "HEL", Destination: "BCN", Type: "flight", BelowPrice: 200},
		NewPrice:  180,
		PrevPrice: 210,
		PriceDrop: -30,
		Currency:  "EUR",
	})
	out := buf.String()
	if !strings.Contains(out, "-30") {
		t.Errorf("expected price drop indicator, got %q", out)
	}
}

func TestNotify_PriceIncrease(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch:     Watch{Origin: "HEL", Destination: "BCN", Type: "flight", BelowPrice: 200},
		NewPrice:  250,
		PrevPrice: 210,
		PriceDrop: 40,
		Currency:  "EUR",
	})
	out := buf.String()
	if !strings.Contains(out, "+40") {
		t.Errorf("expected price increase indicator, got %q", out)
	}
}

func TestNotify_Unchanged(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch:     Watch{Origin: "HEL", Destination: "BCN", Type: "flight"},
		NewPrice:  200,
		PrevPrice: 200,
		PriceDrop: 0,
		Currency:  "EUR",
	})
	out := buf.String()
	if !strings.Contains(out, "unchanged") {
		t.Errorf("expected 'unchanged', got %q", out)
	}
}

func TestNotify_LowestPrice(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	n.Notify(CheckResult{
		Watch:    Watch{Origin: "HEL", Destination: "BCN", Type: "flight", LowestPrice: 150},
		NewPrice: 200,
		Currency: "EUR",
	})
	out := buf.String()
	if !strings.Contains(out, "lowest: 150") {
		t.Errorf("expected lowest price, got %q", out)
	}
}

func TestNotifyAll(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false}
	results := []CheckResult{
		{Watch: Watch{Origin: "HEL", Destination: "BCN", Type: "flight"}, NewPrice: 200, Currency: "EUR"},
		{Watch: Watch{Origin: "HEL", Destination: "NRT", Type: "flight"}, NewPrice: 500, Currency: "EUR"},
	}
	n.NotifyAll(results)
	out := buf.String()
	if !strings.Contains(out, "BCN") || !strings.Contains(out, "NRT") {
		t.Errorf("expected both routes in output, got %q", out)
	}
}

func TestBuildBookingURL_Flight(t *testing.T) {
	url := buildBookingURL(Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01"})
	if !strings.Contains(url, "google.com/travel/flights") || !strings.Contains(url, "BCN") {
		t.Errorf("unexpected flight URL: %q", url)
	}
}

func TestBuildBookingURL_Hotel(t *testing.T) {
	url := buildBookingURL(Watch{Type: "hotel", Destination: "Barcelona", DepartDate: "2026-07-01", ReturnDate: "2026-07-08"})
	if !strings.Contains(url, "google.com/travel/hotels") || !strings.Contains(url, "Barcelona") {
		t.Errorf("unexpected hotel URL: %q", url)
	}
}

func TestBuildBookingURL_Unknown(t *testing.T) {
	url := buildBookingURL(Watch{Type: "bus"})
	if url != "" {
		t.Errorf("expected empty URL for unknown type, got %q", url)
	}
}

func TestColorHelpers_NoColor(t *testing.T) {
	n := &Notifier{UseColor: false}
	if n.green("hi") != "hi" {
		t.Error("green should be passthrough when no color")
	}
	if n.red("hi") != "hi" {
		t.Error("red should be passthrough when no color")
	}
	if n.yellow("hi") != "hi" {
		t.Error("yellow should be passthrough when no color")
	}
}

func TestColorHelpers_WithColor(t *testing.T) {
	n := &Notifier{UseColor: true}
	if !strings.Contains(n.green("hi"), "\033[32m") {
		t.Error("green should contain ANSI code")
	}
	if !strings.Contains(n.red("hi"), "\033[31m") {
		t.Error("red should contain ANSI code")
	}
	if !strings.Contains(n.yellow("hi"), "\033[33m") {
		t.Error("yellow should contain ANSI code")
	}
}

func TestDefaultStore(t *testing.T) {
	s, err := DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if s == nil {
		t.Fatal("DefaultStore returned nil")
	}
}

// TestDesktopNotifyDispatch_NonDarwinNoPanic asserts the dispatch helper degrades
// gracefully on non-darwin platforms: it must not panic regardless of whether the
// native channel (notify-send, PowerShell) is present. Best-effort by contract.
func TestDesktopNotifyDispatch_NoPanic(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "plan9", "freebsd", ""} {
		goos := goos
		t.Run(goos, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("desktopNotifyDispatch(%q) panicked: %v", goos, r)
				}
			}()
			desktopNotifyDispatch(goos, "trvl: test", "graceful degradation")
		})
	}
}

// TestDesktopNotify_NoPanic exercises the Notifier method wrapper end-to-end on
// the host OS; it must never panic since callers ignore failures.
func TestDesktopNotify_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("desktopNotify panicked: %v", r)
		}
	}()
	n := &Notifier{}
	n.desktopNotify("trvl: test", "no panic")
}
