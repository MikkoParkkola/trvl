package hotels

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestExtractOrganicPrice_Valid pins the price block contract: [6][1] holds the
// search-wide params (the [[maxPrice, 0], null, null, "cur"] shape is the
// search's price ceiling, the same for every hotel) and only [6][2][1] carries
// this hotel's price. A hotel without a usable [6][2] is unpriced; the
// search-wide value must never be reported as its price.
func TestExtractOrganicPrice_Valid(t *testing.T) {
	searchWide := []any{[]any{189.0, 0.0}, nil, nil, "EUR"}
	tests := []struct {
		name      string
		raw       []any
		wantPrice float64
	}{
		{"per-hotel rounded price", []any{nil, searchWide, []any{nil, []any{"€61", nil, 60.72, nil, 61.0}}}, 61},
		{"per-hotel exact price when rounded absent", []any{nil, searchWide, []any{nil, []any{"€61", nil, 60.72}}}, 60.72},
		{"per-hotel block absent", []any{nil, searchWide}, 0},
		{"per-hotel block null", []any{nil, searchWide, nil}, 0},
		{"per-hotel block malformed", []any{nil, searchWide, "€61"}, 0},
		{"per-hotel price info null", []any{nil, searchWide, []any{nil, nil}}, 0},
		{"per-hotel numeric slots null", []any{nil, searchWide, []any{nil, []any{"€61", nil, nil, nil, nil}}}, 0},
		{"per-hotel numeric slots strings", []any{nil, searchWide, []any{nil, []any{"€61", nil, "60.72", nil, "61"}}}, 0},
		{"per-hotel numeric slots zero", []any{nil, searchWide, []any{nil, []any{"€0", nil, 0.0, nil, 0.0}}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, cur := extractOrganicPrice(tt.raw)
			if price != tt.wantPrice {
				t.Errorf("price = %v, want %v (search-wide 189 must never become a hotel price)", price, tt.wantPrice)
			}
			if tt.wantPrice > 0 && cur != "EUR" {
				t.Errorf("currency = %q, want EUR", cur)
			}
		})
	}
}

// TestParseHotelsFromPage_SearchWidePriceNeverCopied parses a page where every
// hotel carries the same search-wide params at [6][1] and per-hotel prices
// differ at [6][2]. Each priced hotel must keep its own price, and the hotel
// with no per-hotel block must stay unpriced instead of inheriting 500.
func TestParseHotelsFromPage_SearchWidePriceNeverCopied(t *testing.T) {
	const searchWideMax = 500.0
	searchWide := []any{[]any{searchWideMax, 0.0}, nil, nil, "EUR"}
	perHotel := func(p float64) []any {
		return []any{nil, []any{"€", nil, p, nil, p}}
	}
	type fixture struct {
		name  string
		block []any
		want  float64
	}
	fixtures := []fixture{
		{"Hotel Alpha", []any{nil, searchWide, perHotel(61)}, 61},
		{"Hotel Bravo", []any{nil, searchWide, perHotel(95)}, 95},
		{"Hotel Charlie", []any{nil, searchWide}, 0},
	}

	var entries []any
	for _, f := range fixtures {
		offset := nameOffset(f.name)
		hotel := make([]any, 12)
		hotel[1] = f.name
		hotel[2] = []any{[]any{48.2 + offset, 16.37 + offset}}
		hotel[3] = []any{"4-star hotel", 4.0}
		hotel[6] = f.block
		hotel[9] = "/g/" + strings.ToLower(strings.ReplaceAll(f.name, " ", "_"))
		entries = append(entries, []any{nil, map[string]any{"397419284": []any{hotel}}})
	}
	dataJSON, err := json.Marshal([]any{[]any{[]any{[]any{nil, entries}}}})
	if err != nil {
		t.Fatal(err)
	}
	page := `<html>` + strings.Repeat("<!-- padding -->", 100) +
		`AF_initDataCallback({key: 'ds:0', data:` + string(dataJSON) + `});</html>`

	got := parseHotelsFromPageFull(page, "EUR").Hotels
	if len(got) != len(fixtures) {
		t.Fatalf("parsed %d hotels, want %d", len(got), len(fixtures))
	}
	byName := make(map[string]models.HotelResult, len(got))
	for _, h := range got {
		byName[h.Name] = h
	}
	for _, f := range fixtures {
		h, ok := byName[f.name]
		if !ok {
			t.Fatalf("hotel %q missing from parse result", f.name)
		}
		if h.Price == searchWideMax {
			t.Errorf("%s: price = search-wide %v; the [6][1] params were reported as a hotel price", f.name, searchWideMax)
		}
		if h.Price != f.want {
			t.Errorf("%s: price = %v, want %v", f.name, h.Price, f.want)
		}
	}
}
