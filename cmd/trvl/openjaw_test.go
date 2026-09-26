package main

import (
	"strings"
	"testing"
)

func TestOpenJawCommandPricesOneBundle(t *testing.T) {
	cmd := openJawCmd()
	addInheritedFormatFlag(t, cmd, "table")
	out := executeCommandWithStdout(t, cmd, "HEL:VIE:180:EUR", "VIE:BUD:40:EUR")
	if !strings.Contains(out, "220.00 EUR") {
		t.Fatalf("output = %q", out)
	}
}

func TestOpenJawCommandRejectsADifferentArrival(t *testing.T) {
	cmd := openJawCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"HEL:VIE:180:EUR", "BUD:VIE:40:EUR"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected the ground leg to be rejected")
	}
}
