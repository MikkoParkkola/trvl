package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestOfflineCommandValidationErrors(t *testing.T) {
	t.Setenv("TRAVELPAYOUTS_TOKEN", "")

	if err := executeCommandExpectError(t, pricetrendsCmd(), "HEL", "BCN"); err == nil || !strings.Contains(err.Error(), "pricetrends is opt-in") {
		t.Fatalf("pricetrends error = %v", err)
	}

	if err := executeCommandExpectError(t, nestedCmd(), "HEL", "AMS", "bad", "2026-07-05", "2026-07-20", "2026-07-24"); err == nil || !strings.Contains(err.Error(), "invalid date") {
		t.Fatalf("nested invalid-date error = %v", err)
	}

	cmd := forecastCmd()
	if err := executeCommandExpectError(t, cmd, "HEL-CDG", "--price", "120", "--trip-date", "tomorrow"); err == nil || !strings.Contains(err.Error(), "invalid trip-date") {
		t.Fatalf("forecast invalid-date error = %v", err)
	}
}

func executeCommandExpectError(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	return cmd.Execute()
}
