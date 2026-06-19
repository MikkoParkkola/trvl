package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/arbreport"
)

func TestArbitrageReportTool_Definition(t *testing.T) {
	td := arbitrageReportTool()
	if td.Name != "arbitrage_report" {
		t.Fatalf("unexpected tool name %q", td.Name)
	}
	if td.Description == "" {
		t.Error("tool must have a description")
	}
	for _, req := range []string{"origin", "destination", "depart_date"} {
		if _, ok := td.InputSchema.Properties[req]; !ok {
			t.Errorf("input schema missing required property %q", req)
		}
	}
	if td.Annotations == nil || !td.Annotations.ReadOnlyHint {
		t.Error("arbitrage_report should be marked read-only")
	}
}

func TestArbitrageReportTool_Registered(t *testing.T) {
	t.Setenv("TRVL_MCP_TOOL_MODE", "legacy")
	s := NewServer()
	if _, ok := s.handlers["arbitrage_report"]; !ok {
		t.Fatal("arbitrage_report handler not registered")
	}
	if !toolRegistered(s.tools, "arbitrage_report") {
		t.Error("arbitrage_report not advertised in legacy mode")
	}
}

func TestParseCabinFareStrings(t *testing.T) {
	fares, err := parseCabinFareStrings([]string{"economy:500", "premium_economy:540:AY"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fares) != 2 {
		t.Fatalf("expected 2 fares, got %d", len(fares))
	}
	if fares[0].Price != 500 || fares[1].Carrier != "AY" {
		t.Errorf("parsed fares wrong: %+v", fares)
	}
	if _, err := parseCabinFareStrings([]string{"economy"}); err == nil {
		t.Error("expected error for malformed cabin fare")
	}
	if _, err := parseCabinFareStrings([]string{"economy:notanumber"}); err == nil {
		t.Error("expected error for non-numeric price")
	}
}

func TestParseRebookStrings(t *testing.T) {
	rb, err := parseRebookStrings([]string{"Grand:300:240:EUR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rb) != 1 {
		t.Fatalf("expected 1 rebook, got %d", len(rb))
	}
	if rb[0].Hold.HotelName != "Grand" || rb[0].Hold.OriginalPrice != 300 || rb[0].Quote.Price != 240 {
		t.Errorf("parsed rebook wrong: %+v", rb[0])
	}
	if !rb[0].Hold.Refundable {
		t.Error("CLI/MCP rebook holds should be treated as refundable so a saving can surface")
	}
	if _, err := parseRebookStrings([]string{"Grand:300"}); err == nil {
		t.Error("expected error for malformed rebook")
	}
	if _, err := parseRebookStrings([]string{"Grand:x:240"}); err == nil {
		t.Error("expected error for non-numeric original price")
	}
}

func TestHandleArbitrageReport_AggregatesCabinAndHotel(t *testing.T) {
	// Offline-deterministic: cabin + hotel engines need no network. depart_date
	// is omitted so the currency engine short-circuits on its missing-date guard
	// before any flight lookup, keeping the test hermetic and fast.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	args := map[string]any{
		"origin":      "HEL",
		"destination": "LHR",
		"cabin_fares": []any{"economy:500", "premium_economy:460"}, // strict upgrade, saves 40
		"rebooks":     []any{"Grand:300:240:EUR"},                  // saves 60
	}

	_, structured, err := handleArbitrageReport(ctx, args, nil, nil, nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	rep, ok := structured.(arbreport.ArbReport)
	if !ok {
		t.Fatalf("expected arbreport.ArbReport, got %T", structured)
	}
	if rep.Count < 2 {
		t.Fatalf("expected at least cabin+hotel opportunities, got %d: %+v", rep.Count, rep.Opportunities)
	}
	var sawCabin, sawHotel bool
	for _, o := range rep.Opportunities {
		switch o.Engine {
		case "cabin":
			sawCabin = true
		case "hotel":
			sawHotel = true
		}
	}
	if !sawCabin || !sawHotel {
		t.Errorf("expected cabin and hotel opportunities, got %+v", rep.Opportunities)
	}
	// Hotel saving (60) must rank above cabin saving (40).
	if rep.Opportunities[0].Engine != "hotel" {
		t.Errorf("expected hotel ranked first by saving, got %q", rep.Opportunities[0].Engine)
	}
}

func TestHandleArbitrageReport_BadCabinFareErrors(t *testing.T) {
	_, _, err := handleArbitrageReport(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "LHR",
		"depart_date": "2026-08-01",
		"cabin_fares": []any{"economy"},
	}, nil, nil, nil)
	if err == nil {
		t.Error("expected error for malformed cabin fare input")
	}
}
