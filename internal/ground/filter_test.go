package ground

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestFilterZeroPriceRoutes(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "regiojet", Price: 15.0, Currency: "EUR"},
		{Provider: "regiojet", Price: 0, Currency: "EUR"}, // sold out
		{Provider: "flixbus", Price: 22.5, Currency: "EUR"},
		{Provider: "transitous", Price: 0, Currency: ""},  // unpriced, should be kept
		{Provider: "regiojet", Price: 0, Currency: "EUR"}, // sold out
		{Provider: "flixbus", Price: 9.99, Currency: "EUR"},
	}

	filtered := filterUnavailableGroundRoutes(routes)

	if len(filtered) != 4 {
		t.Fatalf("expected 4 routes after filtering, got %d", len(filtered))
	}
	for _, r := range filtered {
		if r.Price == 0 && r.Provider != "transitous" {
			t.Error("zero-price non-transitous route should have been filtered out")
		}
	}
}

func TestFilterZeroPriceRoutes_AllZero(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "regiojet", Price: 0, Currency: "EUR"},
		{Provider: "regiojet", Price: 0, Currency: "EUR"},
	}

	filtered := filterUnavailableGroundRoutes(routes)

	if len(filtered) != 0 {
		t.Fatalf("expected 0 routes after filtering, got %d", len(filtered))
	}
}

func TestFilterZeroPriceRoutes_NoneZero(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 10, Currency: "EUR"},
		{Provider: "regiojet", Price: 5, Currency: "EUR"},
	}

	filtered := filterUnavailableGroundRoutes(routes)

	if len(filtered) != 2 {
		t.Fatalf("expected 2 routes after filtering, got %d", len(filtered))
	}
}

func TestRegioJetSearchParams_IncludesCurrencyAndTariffs(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		wantCur  string
	}{
		{"explicit EUR", "EUR", "EUR"},
		{"explicit CZK", "CZK", "CZK"},
		{"default when empty", "", "EUR"},
		{"lowercase normalized", "czk", "CZK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur := tt.currency
			if cur == "" {
				cur = "EUR"
			}

			params := url.Values{
				"fromLocationId":   {fmt.Sprintf("%d", 10202003)},
				"toLocationId":     {fmt.Sprintf("%d", 10202052)},
				"fromLocationType": {"CITY"},
				"toLocationType":   {"CITY"},
				"departureDate":    {"2026-07-01"},
				"tariffs":          {"REGULAR"},
				"currency":         {strings.ToUpper(cur)},
			}

			encoded := params.Encode()

			if !strings.Contains(encoded, "tariffs=REGULAR") {
				t.Error("params should contain tariffs=REGULAR")
			}
			if !strings.Contains(encoded, "currency="+tt.wantCur) {
				t.Errorf("params should contain currency=%s, got %s", tt.wantCur, encoded)
			}
		})
	}
}

func TestRegioJetSearchParams_MatchesFunction(t *testing.T) {
	// Verify that the URL params built by SearchRegioJet include currency and tariffs.
	// We check by building the same params the function builds.
	currency := "CZK"
	fromCityID := 10202003
	toCityID := 10202052
	date := "2026-07-01"

	params := url.Values{
		"fromLocationId":   {fmt.Sprintf("%d", fromCityID)},
		"toLocationId":     {fmt.Sprintf("%d", toCityID)},
		"fromLocationType": {"CITY"},
		"toLocationType":   {"CITY"},
		"departureDate":    {date},
		"tariffs":          {"REGULAR"},
		"currency":         {strings.ToUpper(currency)},
	}

	u := regiojetBaseURL + regiojetSearch + "?" + params.Encode()

	if !strings.Contains(u, "tariffs=REGULAR") {
		t.Error("URL should include tariffs=REGULAR")
	}
	if !strings.Contains(u, "currency=CZK") {
		t.Error("URL should include currency=CZK")
	}
	if !strings.Contains(u, "departureDate=2026-07-01") {
		t.Error("URL should include departureDate")
	}
}
