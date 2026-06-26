package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotelarb"
)

func TestHandleCalculatePointsValue_OffersArbitrage(t *testing.T) {
	t.Parallel()
	content, structured, err := handleCalculatePointsValue(context.Background(), map[string]any{
		"cash_price": 300.0,
		"offers": []any{
			map[string]any{"program": "world-of-hyatt", "points": 12000.0},
			map[string]any{"program": "hilton-honors", "points": 80000.0},
		},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}

	result, ok := structured.(*hotelarb.PointsArbitrageResult)
	if !ok {
		t.Fatalf("structured type = %T, want *hotelarb.PointsArbitrageResult", structured)
	}
	if len(result.Offers) != 2 {
		t.Fatalf("offers len = %d, want 2", len(result.Offers))
	}
	// The leaner 12k-point Hyatt redemption should rank ahead of the 80k Hilton stay.
	if result.BestOffer.ProgramSlug != "world-of-hyatt" {
		t.Fatalf("best offer = %q, want world-of-hyatt", result.BestOffer.ProgramSlug)
	}
	// The best offer must rank at least as high as every evaluated offer.
	for _, offer := range result.Offers {
		if offer.SavingsVsCash > result.BestOffer.SavingsVsCash {
			t.Fatalf("offer %q savings %.2f beats best %.2f", offer.ProgramSlug, offer.SavingsVsCash, result.BestOffer.SavingsVsCash)
		}
	}
	if !strings.Contains(content[0].Text, "Arbitrage across 2 programs") {
		t.Fatalf("summary = %q, want arbitrage text", content[0].Text)
	}
}

func TestHandleCalculatePointsValue_OffersWithCashFees(t *testing.T) {
	t.Parallel()
	_, structured, err := handleCalculatePointsValue(context.Background(), map[string]any{
		"cash_price": 300.0,
		"currency":   "eur",
		"offers": []any{
			map[string]any{"program": "world-of-hyatt", "points": 12000.0, "cash_fees": 25.0},
		},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, ok := structured.(*hotelarb.PointsArbitrageResult)
	if !ok {
		t.Fatalf("structured type = %T, want *hotelarb.PointsArbitrageResult", structured)
	}
	if result.Currency != "EUR" {
		t.Fatalf("currency = %q, want EUR", result.Currency)
	}
	if got := result.Offers[0].CashFees; got != 25.0 {
		t.Fatalf("cash_fees = %.2f, want 25.00", got)
	}
}

func TestHandleCalculatePointsValue_OffersBadProgram(t *testing.T) {
	t.Parallel()
	_, _, err := handleCalculatePointsValue(context.Background(), map[string]any{
		"cash_price": 300.0,
		"offers": []any{
			map[string]any{"program": "no-such-program", "points": 12000.0},
		},
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for an unrecognized program")
	}
	if !strings.Contains(err.Error(), "unknown program") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleCalculatePointsValue_OffersInvalidCashPrice(t *testing.T) {
	t.Parallel()
	_, _, err := handleCalculatePointsValue(context.Background(), map[string]any{
		"cash_price": 0.0,
		"offers": []any{
			map[string]any{"program": "world-of-hyatt", "points": 12000.0},
		},
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected invalid cash price error")
	}
	if got := err.Error(); got != "cash_price must be greater than 0" {
		t.Fatalf("error = %q, want %q", got, "cash_price must be greater than 0")
	}
}

func TestHandleCalculatePointsValue_EmptyOffersUsesSingleProgram(t *testing.T) {
	t.Parallel()
	// An empty offers array falls through to the single-program path,
	// which then requires program.
	_, _, err := handleCalculatePointsValue(context.Background(), map[string]any{
		"cash_price": 450.0,
		"offers":     []any{},
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected single-program path to require program")
	}
	if got := err.Error(); got != "program is required" {
		t.Fatalf("error = %q, want %q", got, "program is required")
	}
}

func TestToolsCallCalculatePointsValueArbitrage(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "tools/call", 43, ToolCallParams{
		Name: "calculate_points_value",
		Arguments: map[string]any{
			"cash_price": 300.0,
			"offers": []any{
				map[string]any{"program": "world-of-hyatt", "points": 12000.0},
				map[string]any{"program": "hilton-honors", "points": 80000.0},
			},
		},
	})
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected top-level error: %+v", resp.Error)
	}

	resultJSON, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.IsError {
		t.Fatal("expected isError=false")
	}
	if result.StructuredContent == nil {
		t.Fatal("expected structuredContent")
	}

	structuredJSON, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structuredContent: %v", err)
	}
	var arb hotelarb.PointsArbitrageResult
	if err := json.Unmarshal(structuredJSON, &arb); err != nil {
		t.Fatalf("unmarshal structuredContent: %v", err)
	}
	if len(arb.Offers) != 2 {
		t.Fatalf("offers len = %d, want 2", len(arb.Offers))
	}
	if arb.BestOffer.ProgramSlug != "world-of-hyatt" {
		t.Fatalf("best offer = %q, want world-of-hyatt", arb.BestOffer.ProgramSlug)
	}
}
