package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/pricesignal"
)

func TestPrintPricePosition_NotConfident(t *testing.T) {
	var b bytes.Buffer
	printPricePosition(&b, &pricesignal.Position{Observations: 3, Confident: false, Band: pricesignal.BandUnknown})
	out := b.String()
	if !strings.Contains(out, "not enough history") {
		t.Fatalf("sparse data must say 'not enough history', got %q", out)
	}
	// Must NOT assert a trend/verdict.
	for _, banned := range []string{"BUY", "WAIT", "cheap third", "expensive third"} {
		if strings.Contains(out, banned) {
			t.Fatalf("sparse output must not assert %q: %q", banned, out)
		}
	}
}

func TestPrintPricePosition_Confident(t *testing.T) {
	var b bytes.Buffer
	printPricePosition(&b, &pricesignal.Position{
		Confident: true, Band: pricesignal.BandLow, Verdict: pricesignal.VerdictBuy,
		Low: 100, Median: 150, High: 200, Observations: 12, VsMedianPct: -20,
	})
	out := b.String()
	if !strings.Contains(out, "BUY now") || !strings.Contains(out, "cheap third") {
		t.Fatalf("confident buy must show verdict+band, got %q", out)
	}
	if !strings.Contains(out, "-20%") {
		t.Fatalf("want vs-median pct, got %q", out)
	}
}

func TestPrintPricePosition_Nil(t *testing.T) {
	var b bytes.Buffer
	printPricePosition(&b, nil)
	if b.Len() != 0 {
		t.Fatalf("nil position must print nothing, got %q", b.String())
	}
}
