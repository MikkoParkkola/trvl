package main

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestFormatProviderWarning covers the transparency warning that surfaces
// errored/disabled hotel providers below the results table.
func TestFormatProviderWarning(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []models.ProviderStatus
		wantEmpty bool
		// substrings that must appear when a warning is produced
		want []string
	}{
		{
			name:      "nil statuses produce no warning",
			statuses:  nil,
			wantEmpty: true,
		},
		{
			name: "all ok produce no warning",
			statuses: []models.ProviderStatus{
				{ID: "google_hotels", Name: "Google Hotels", Status: "ok", Results: 10},
				{ID: "booking", Name: "Booking.com", Status: "ok", Results: 5},
			},
			wantEmpty: true,
		},
		{
			name: "two of five errored shows count and provider names",
			statuses: []models.ProviderStatus{
				{ID: "google_hotels", Name: "Google Hotels", Status: "ok", Results: 10},
				{ID: "airbnb", Name: "Airbnb", Status: "ok", Results: 3},
				{ID: "trivago", Name: "Trivago", Status: "ok", Results: 2},
				{ID: "booking", Name: "Booking.com", Status: "error", Error: "browser cookies missing for booking"},
				{ID: "hostelworld", Name: "Hostelworld", Status: "error", Error: "context deadline exceeded"},
			},
			want: []string{
				"2 of 5 sources unavailable",
				"Booking.com:",
				"Hostelworld:",
				"prices may be incomplete",
			},
		},
		{
			name: "disabled counts as unavailable",
			statuses: []models.ProviderStatus{
				{ID: "google_hotels", Name: "Google Hotels", Status: "ok"},
				{ID: "booking", Name: "Booking.com", Status: "disabled"},
			},
			want: []string{"1 of 2 sources unavailable", "Booking.com:"},
		},
		{
			name: "fix hint is surfaced on a follow-up line",
			statuses: []models.ProviderStatus{
				{ID: "booking", Name: "Booking.com", Status: "error", Error: "no cookies", FixHint: "open Booking.com in a browser"},
			},
			want: []string{"Fix:", "open Booking.com in a browser"},
		},
		{
			name: "falls back to ID and status when name/error empty",
			statuses: []models.ProviderStatus{
				{ID: "booking", Status: "error"},
			},
			want: []string{"booking: error"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatProviderWarning(tc.statuses)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("formatProviderWarning() = %q, want empty", got)
				}
				return
			}
			if got == "" {
				t.Fatalf("formatProviderWarning() = empty, want a warning")
			}
			for _, sub := range tc.want {
				if !strings.Contains(got, sub) {
					t.Errorf("formatProviderWarning() = %q, missing substring %q", got, sub)
				}
			}
		})
	}
}

// TestTruncateErr verifies long/multiline error strings are trimmed for inline
// display.
func TestTruncateErr(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"short unchanged", "boom", "boom"},
		{"first line only", "line one\nline two\nline three", "line one"},
		{"trimmed whitespace", "  spaced  ", "spaced"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateErr(tc.in); got != tc.want {
				t.Errorf("truncateErr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	long := strings.Repeat("x", 200)
	got := truncateErr(long)
	if len([]rune(got)) > 60 {
		t.Errorf("truncateErr long result has %d runes, want <= 60", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateErr long result = %q, want ellipsis suffix", got)
	}
}
