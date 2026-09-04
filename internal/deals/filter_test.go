package deals

import (
	"strings"
	"testing"
	"time"
)

func TestOriginMatchesDeal(t *testing.T) {
	tests := []struct {
		name       string
		dealOrigin string
		filters    []string
		want       bool
	}{
		{"exact city name", "Helsinki", []string{"Helsinki"}, true},
		{"IATA to city", "Helsinki", []string{"HEL"}, true},
		{"exact city amsterdam", "Amsterdam", []string{"Amsterdam"}, true},
		{"IATA AMS", "Amsterdam", []string{"AMS"}, true},
		{"IATA JFK to New York", "New York", []string{"JFK"}, true},
		{"city name Prague", "Prague", []string{"Prague"}, true},
		{"IATA PRG", "Prague", []string{"PRG"}, true},
		{"IATA still works directly", "HEL", []string{"HEL"}, true},
		{"reverse IATA resolve", "HEL", []string{"helsinki"}, true},
		{"case insensitive", "helsinki", []string{"HELSINKI"}, true},
		{"substring deal contains filter", "Amsterdam Schiphol", []string{"Amsterdam"}, true},
		{"no match", "Helsinki", []string{"Paris"}, false},
		{"empty deal origin", "", []string{"HEL"}, false},
		{"empty filter", "Helsinki", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := originMatchesDeal(tt.dealOrigin, tt.filters)
			if got != tt.want {
				t.Errorf("originMatchesDeal(%q, %v) = %v, want %v",
					tt.dealOrigin, tt.filters, got, tt.want)
			}
		})
	}
}

func TestFilterDealsOriginFlexible(t *testing.T) {
	now := time.Now()
	deals := []Deal{
		{Title: "Deal from Helsinki", Origin: "Helsinki", Published: now},
		{Title: "Deal from Amsterdam", Origin: "Amsterdam", Published: now},
		{Title: "Deal from Prague", Origin: "Prague", Published: now},
		{Title: "Deal from New York", Origin: "New York", Published: now},
		{Title: "No origin deal", Origin: "", Published: now},
	}

	t.Run("filter by IATA HEL", func(t *testing.T) {
		result := FilterDeals(deals, DealFilter{Origins: []string{"HEL"}})
		if len(result) != 1 || result[0].Origin != "Helsinki" {
			t.Errorf("expected 1 Helsinki deal, got %d deals", len(result))
		}
	})

	t.Run("filter by city name", func(t *testing.T) {
		result := FilterDeals(deals, DealFilter{Origins: []string{"Amsterdam"}})
		if len(result) != 1 || result[0].Origin != "Amsterdam" {
			t.Errorf("expected 1 Amsterdam deal, got %d deals", len(result))
		}
	})

	t.Run("filter by JFK matches New York", func(t *testing.T) {
		result := FilterDeals(deals, DealFilter{Origins: []string{"JFK"}})
		if len(result) != 1 || result[0].Origin != "New York" {
			t.Errorf("expected 1 New York deal, got %d deals", len(result))
		}
	})

	t.Run("no filter returns all", func(t *testing.T) {
		result := FilterDeals(deals, DealFilter{})
		if len(result) != 5 {
			t.Errorf("expected 5 deals, got %d", len(result))
		}
	})
}

func TestFilterDeals_TitleFallbackWhenOriginEmpty(t *testing.T) {
	now := time.Now()
	deals := []Deal{
		{Title: "Warsaw to Barcelona from EUR49", Origin: "", Published: now},
		{Title: "getaway easier cities", Origin: "", Published: now},
		{Title: "HEL-BCN flash sale", Origin: "", Published: now},
	}
	got := FilterDeals(deals, DealFilter{Origins: []string{"WAW"}})
	if len(got) != 1 || !strings.Contains(got[0].Title, "Warsaw") {
		t.Fatalf("expected the Warsaw headline, got %+v", got)
	}
	hel := FilterDeals(deals, DealFilter{Origins: []string{"HEL"}})
	if len(hel) != 1 || !strings.Contains(hel[0].Title, "HEL-BCN") {
		t.Fatalf("expected HEL-BCN headline, got %+v", hel)
	}
}
