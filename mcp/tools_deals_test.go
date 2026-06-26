package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestSearchDealsTool_Definition(t *testing.T) {
	t.Parallel()
	tool := searchDealsTool()
	if tool.Name != "search_deals" {
		t.Errorf("Name = %q, want search_deals", tool.Name)
	}
	// origins is now optional: empty/absent returns deals from all origins,
	// mirroring the CLI `--from` flag.
	if len(tool.InputSchema.Required) != 0 {
		t.Errorf("Required = %v, want no required params", tool.InputSchema.Required)
	}
	if _, ok := tool.InputSchema.Properties["currency"]; !ok {
		t.Error("expected a currency input property")
	}
	if _, ok := tool.InputSchema.Properties["origins"]; !ok {
		t.Error("expected an origins input property")
	}
}

// TestHandleSearchDeals_GlobalOriginsLive proves empty/absent origins is accepted
// (no error) and yields a structured result across all origins. Network-gated to keep
// the default suite deterministic; it asserts acceptance, not a non-empty deal list
// (live feeds may be empty).
func TestHandleSearchDeals_GlobalOriginsLive(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveProbe(t)
	for _, args := range []map[string]any{
		{},                 // absent origins
		{"origins": ""},    // empty origins
		{"origins": "   "}, // whitespace-only origins
	} {
		_, structured, err := handleSearchDeals(context.Background(), args, nil, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error for args %v: %v", args, err)
		}
		if structured == nil {
			t.Fatalf("expected structured result for args %v", args)
		}
	}
}

// TestParseOrigins covers the origins-wiring contract: provided origins still
// produce a populated filter (origins keep filtering), while empty/absent origins
// yield nil so the handler fetches deals from all origins. Deterministic.
func TestParseOrigins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty returns nil (global)", "", nil},
		{"single origin", "HEL", []string{"HEL"}},
		{"multiple origins trimmed", "HEL, AMS , TLL", []string{"HEL", "AMS", "TLL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseOrigins(tt.raw)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parseOrigins(%q) = %v, want nil", tt.raw, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseOrigins(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseOrigins(%q)[%d] = %q, want %q", tt.raw, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestConvertDealPrices_EmptyCurrencyNoOp(t *testing.T) {
	t.Parallel()
	result := &deals.DealsResult{
		Success: true,
		Count:   1,
		Deals:   []deals.Deal{{Price: 199, Currency: "USD"}},
	}
	convertDealPrices(context.Background(), "", result)
	if result.Deals[0].Price != 199 || result.Deals[0].Currency != "USD" {
		t.Errorf("empty currency mutated deal: %+v", result.Deals[0])
	}
}

func TestConvertDealPrices_NilResultSafe(t *testing.T) {
	t.Parallel()
	// Must not panic.
	convertDealPrices(context.Background(), "EUR", nil)
}

func TestConvertDealPrices_SameCurrencyPreserved(t *testing.T) {
	t.Parallel()
	result := &deals.DealsResult{
		Success: true,
		Count:   1,
		Deals:   []deals.Deal{{Price: 250, Currency: "EUR"}},
	}
	convertDealPrices(context.Background(), "EUR", result)
	if result.Deals[0].Price != 250 {
		t.Errorf("Price = %v, want 250 (same currency must not convert)", result.Deals[0].Price)
	}
	if result.Deals[0].Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", result.Deals[0].Currency)
	}
}

func TestConvertDealPrices_ZeroPriceSkipped(t *testing.T) {
	t.Parallel()
	result := &deals.DealsResult{
		Success: true,
		Count:   1,
		Deals:   []deals.Deal{{Price: 0, Currency: "USD"}}, // no price => never converted
	}
	convertDealPrices(context.Background(), "EUR", result)
	if result.Deals[0].Price != 0 || result.Deals[0].Currency != "USD" {
		t.Errorf("zero-price deal mutated: %+v", result.Deals[0])
	}
}

// TestConvertDealPrices_ConvertsLive proves an actual cross-currency conversion
// changes the price via the same destinations.ConvertCurrency path the CLI uses.
// Network-gated because exchange rates are fetched live.
func TestConvertDealPrices_ConvertsLive(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveProbe(t)
	result := &deals.DealsResult{
		Success: true,
		Count:   1,
		Deals:   []deals.Deal{{Price: 100, Currency: "USD"}},
	}
	convertDealPrices(context.Background(), "EUR", result)
	got := result.Deals[0]
	if got.Currency != "EUR" {
		t.Fatalf("Currency = %q, want EUR after conversion", got.Currency)
	}
	if got.Price == 100 {
		t.Errorf("Price unchanged at 100; expected USD->EUR conversion to differ")
	}
	if got.Price <= 0 {
		t.Errorf("Price = %v, want a positive converted value", got.Price)
	}
}
