package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/arbreport"
)

func TestParseRebookCandidates(t *testing.T) {
	rb, err := parseRebookCandidates([]string{"Grand:300:240:EUR", "Plaza:200:180"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rb) != 2 {
		t.Fatalf("expected 2 rebooks, got %d", len(rb))
	}
	if rb[0].Hold.HotelName != "Grand" || rb[0].Quote.Price != 240 || rb[0].Hold.Currency != "EUR" {
		t.Errorf("first rebook parsed wrong: %+v", rb[0])
	}
	if rb[1].Hold.Currency != "EUR" {
		t.Errorf("default currency should be EUR, got %q", rb[1].Hold.Currency)
	}
	if !rb[0].Hold.Refundable {
		t.Error("rebook holds should default to refundable so savings can surface")
	}
}

func TestParseRebookCandidates_Errors(t *testing.T) {
	cases := []string{"Grand:300", ":300:240", "Grand:x:240", "Grand:300:y"}
	for _, c := range cases {
		if _, err := parseRebookCandidates([]string{c}); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestRenderArbReportTable(t *testing.T) {
	report := arbreport.ArbReport{
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-08-01",
		Currency:    "EUR",
		Opportunities: []arbreport.Opportunity{
			{Engine: "hotel", Type: "hotel_rebook", Description: "rebook lower", EstimatedSaving: 60, Currency: "EUR", Confidence: "high"},
		},
		Skipped: []arbreport.SkippedEngine{
			{Engine: "currency", Reason: "N/A: no currency arbitrage on this route"},
		},
		Count: 1,
	}
	var buf bytes.Buffer
	renderArbReportTableTo(&buf, report)
	out := buf.String()
	if !strings.Contains(out, "hotel_rebook") {
		t.Errorf("table missing opportunity row:\n%s", out)
	}
	if !strings.Contains(out, "Skipped engines") || !strings.Contains(out, "currency") {
		t.Errorf("table missing skipped section:\n%s", out)
	}
}

func TestRenderArbReportTable_Empty(t *testing.T) {
	report := arbreport.ArbReport{
		Origin: "HEL", Destination: "LHR", DepartDate: "2026-08-01", Currency: "EUR",
		Opportunities: []arbreport.Opportunity{},
		Skipped:       []arbreport.SkippedEngine{{Engine: "cabin", Reason: "N/A: no cabin fare ladder supplied for this trip"}},
	}
	var buf bytes.Buffer
	renderArbReportTableTo(&buf, report)
	if !strings.Contains(buf.String(), "No arbitrage opportunities") {
		t.Errorf("empty report should state none found:\n%s", buf.String())
	}
}

func TestArbitrageReportCmd_Wired(t *testing.T) {
	cmd := arbitrageReportCmd()
	if cmd.Use == "" || !strings.HasPrefix(cmd.Use, "arbitrage-report") {
		t.Errorf("unexpected command use: %q", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("command missing RunE")
	}
}
