package flights

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- GridOptions defaults ---

func TestGridOptions_Defaults(t *testing.T) {
	opts := GridOptions{}
	opts.defaults()

	if opts.Adults != 1 {
		t.Errorf("Adults = %d, want 1", opts.Adults)
	}
	if opts.DepartFrom == "" {
		t.Error("DepartFrom should be set")
	}
	if opts.DepartTo == "" {
		t.Error("DepartTo should be set")
	}
	if opts.ReturnFrom == "" {
		t.Error("ReturnFrom should be set")
	}
	if opts.ReturnTo == "" {
		t.Error("ReturnTo should be set")
	}
}

func TestGridOptions_DefaultsPreserveSet(t *testing.T) {
	opts := GridOptions{
		DepartFrom: "2026-07-01",
		DepartTo:   "2026-07-07",
		ReturnFrom: "2026-07-08",
		ReturnTo:   "2026-07-14",
		Adults:     3,
	}
	opts.defaults()

	if opts.Adults != 3 {
		t.Errorf("Adults = %d, want 3", opts.Adults)
	}
	if opts.DepartFrom != "2026-07-01" {
		t.Errorf("DepartFrom = %q", opts.DepartFrom)
	}
	if opts.DepartTo != "2026-07-07" {
		t.Errorf("DepartTo = %q", opts.DepartTo)
	}
	if opts.ReturnFrom != "2026-07-08" {
		t.Errorf("ReturnFrom = %q", opts.ReturnFrom)
	}
	if opts.ReturnTo != "2026-07-14" {
		t.Errorf("ReturnTo = %q", opts.ReturnTo)
	}
}

func TestGridOptions_DefaultChaining(t *testing.T) {
	// Only DepartFrom set — rest should be derived.
	opts := GridOptions{DepartFrom: "2026-08-01"}
	opts.defaults()

	if opts.DepartTo != "2026-08-07" {
		t.Errorf("DepartTo = %q, want 2026-08-07", opts.DepartTo)
	}
	if opts.ReturnFrom != "2026-08-08" {
		t.Errorf("ReturnFrom = %q, want 2026-08-08", opts.ReturnFrom)
	}
	if opts.ReturnTo != "2026-08-14" {
		t.Errorf("ReturnTo = %q, want 2026-08-14", opts.ReturnTo)
	}
}

// --- encodePriceGridPayload ---

func TestEncodePriceGridPayload(t *testing.T) {
	opts := GridOptions{
		DepartFrom: "2026-07-01",
		DepartTo:   "2026-07-07",
		ReturnFrom: "2026-07-08",
		ReturnTo:   "2026-07-14",
		Adults:     1,
	}

	encoded := encodePriceGridPayload("/m/01lbs", "HEL", "/m/01f62", "BCN", opts)
	if encoded == "" {
		t.Fatal("encoded payload is empty")
	}
	if len(encoded) < 100 {
		t.Errorf("payload seems too short: %d chars", len(encoded))
	}
}

// --- parsePriceGridResponse ---

func TestParsePriceGridResponse_EmptyBody(t *testing.T) {
	_, err := parsePriceGridResponse([]byte{})
	if err == nil {
		t.Error("expected error for empty body")
	}
}

func TestParsePriceGridResponse_TooSmall(t *testing.T) {
	body := []byte(")]}'\n[3]")
	_, err := parsePriceGridResponse(body)
	if err == nil {
		t.Error("expected error for small response")
	}
}

// TestParsePriceGridResponse_ValidButEmpty guards the empty-grid contract:
// a parseable response carrying no priced offers must return (empty, nil),
// NOT an error. Google returns this for wide departure×return windows.
// The (empty, nil) result is what made the old SearchPriceGrid path render
// the bogus "%!w(<nil>)" via fmt.Errorf("...: %w", nil); the parser must keep
// returning a nil error here so the caller can emit a clear "no priced cells"
// message instead. Regression guard for the grid.go empty-cells fix.
func TestParsePriceGridResponse_ValidButEmpty(t *testing.T) {
	innerJSON := `[null,[]]` // valid grid section with zero offers
	// Pad the outer line past the 200-byte floor with an element the parser
	// ignores (only entry[2] is read for offers).
	pad := strings.Repeat("x", 240)
	entry := []any{[]any{"wrb.fr", nil, innerJSON, pad}}
	entryJSON, _ := json.Marshal(entry)
	body := []byte(")]}'\n" + string(entryJSON))

	cells, err := parsePriceGridResponse(body)
	if err != nil {
		t.Fatalf("valid-but-empty grid must not error, got: %v", err)
	}
	if len(cells) != 0 {
		t.Fatalf("expected 0 cells for offerless grid, got %d", len(cells))
	}
}

func TestParsePriceGridResponse_ValidData(t *testing.T) {
	// Simulate a grid response with price cells.
	// Response must be >= 200 bytes after stripping anti-XSSI prefix.
	innerJSON := `[null,[["2026-07-01","2026-07-08",[[null,250],""],1],["2026-07-02","2026-07-09",[[null,280],""],1],["2026-07-01","2026-07-09",[[null,310],""],1],["2026-07-03","2026-07-10",[[null,290],""],1],["2026-07-04","2026-07-11",[[null,300],""],1],["2026-07-05","2026-07-12",[[null,270],""],1]]]`
	entry := []any{[]any{"wrb.fr", nil, innerJSON}}
	entryJSON, _ := json.Marshal(entry)
	body := []byte(")]}'\n" + string(entryJSON))

	cells, err := parsePriceGridResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(cells) < 3 {
		t.Fatalf("expected at least 3 cells, got %d", len(cells))
	}

	// Find the entry for 2026-07-01 -> 2026-07-08.
	found := false
	for _, c := range cells {
		if c.DepartureDate == "2026-07-01" && c.ReturnDate == "2026-07-08" {
			found = true
			if c.Price != 250 {
				t.Errorf("cell price: got %v, want 250", c.Price)
			}
			if c.Currency != "" { // Currency stamped by SearchPriceGrid, not parser
				t.Errorf("cell currency: got %q, want empty (stamped later)", c.Currency)
			}
			break
		}
	}
	if !found {
		t.Error("missing cell for 2026-07-01 -> 2026-07-08")
	}
}

func TestParsePriceGridResponse_Deduplication(t *testing.T) {
	// Same departure+return combination should be deduplicated.
	// Pad the response to exceed the 200-byte minimum.
	innerJSON := `[null,[["2026-07-01","2026-07-08",[[null,250],""],1],["2026-07-02","2026-07-09",[[null,280],""],1],["2026-07-03","2026-07-10",[[null,310],""],1],["2026-07-04","2026-07-11",[[null,290],""],1],["2026-07-05","2026-07-12",[[null,270],""],1],["2026-07-06","2026-07-13",[[null,260],""],1]]]`
	entry := []any{[]any{"wrb.fr", nil, innerJSON}}
	entryJSON, _ := json.Marshal(entry)
	body := []byte(")]}'\n" + string(entryJSON))

	cells, err := parsePriceGridResponse(body)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Count occurrences of the same date pair.
	key := "2026-07-01|2026-07-08"
	count := 0
	for _, c := range cells {
		if c.DepartureDate+"|"+c.ReturnDate == key {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 occurrence, got %d", count)
	}
}

// --- parseGridOffer ---

func TestParseGridOffer_Valid(t *testing.T) {
	raw, _ := json.Marshal([]any{"2026-07-01", "2026-07-08", []any{[]any{nil, float64(299)}}})
	cell := parseGridOffer(raw)
	if cell == nil {
		t.Fatal("expected non-nil result")
	}
	if cell.DepartureDate != "2026-07-01" {
		t.Errorf("departure = %q", cell.DepartureDate)
	}
	if cell.ReturnDate != "2026-07-08" {
		t.Errorf("return = %q", cell.ReturnDate)
	}
	if cell.Price != 299 {
		t.Errorf("price = %v", cell.Price)
	}
	if cell.Currency != "" { // Currency stamped by SearchPriceGrid, not parser
		t.Errorf("currency = %q, want empty (stamped later)", cell.Currency)
	}
}

func TestParseGridOffer_ZeroPrice(t *testing.T) {
	raw, _ := json.Marshal([]any{"2026-07-01", "2026-07-08", []any{[]any{nil, float64(0)}}})
	cell := parseGridOffer(raw)
	if cell != nil {
		t.Error("expected nil for zero price")
	}
}

func TestParseGridOffer_InvalidDepartDate(t *testing.T) {
	raw, _ := json.Marshal([]any{"bad-date", "2026-07-08", []any{[]any{nil, float64(299)}}})
	cell := parseGridOffer(raw)
	if cell != nil {
		t.Error("expected nil for invalid departure date")
	}
}

func TestParseGridOffer_InvalidReturnDate(t *testing.T) {
	raw, _ := json.Marshal([]any{"2026-07-01", "bad-date", []any{[]any{nil, float64(299)}}})
	cell := parseGridOffer(raw)
	if cell != nil {
		t.Error("expected nil for invalid return date")
	}
}

func TestParseGridOffer_MalformedJSON(t *testing.T) {
	cell := parseGridOffer([]byte("not json"))
	if cell != nil {
		t.Error("expected nil for malformed JSON")
	}
}

// --- parseGridPriceData ---

func TestParseGridPriceData_ValidSection(t *testing.T) {
	data := []any{nil, []any{
		[]any{"2026-07-01", "2026-07-08", []any{[]any{nil, float64(199)}}, float64(1)},
		[]any{"2026-07-02", "2026-07-09", []any{[]any{nil, float64(229)}}, float64(1)},
	}}
	raw, _ := json.Marshal(data)

	cells := parseGridPriceData(raw)
	if len(cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(cells))
	}
	if cells[0].DepartureDate != "2026-07-01" {
		t.Errorf("cell 0 dep = %q", cells[0].DepartureDate)
	}
	if cells[1].Price != 229 {
		t.Errorf("cell 1 price = %v", cells[1].Price)
	}
}

func TestParseGridPriceData_InvalidJSON(t *testing.T) {
	cells := parseGridPriceData([]byte("not json"))
	if len(cells) != 0 {
		t.Errorf("expected 0 cells for invalid JSON, got %d", len(cells))
	}
}

// --- sortedKeys ---

func TestSortedKeys(t *testing.T) {
	m := map[string]bool{
		"2026-07-03": true,
		"2026-07-01": true,
		"2026-07-02": true,
	}

	keys := sortedKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "2026-07-01" || keys[1] != "2026-07-02" || keys[2] != "2026-07-03" {
		t.Errorf("keys not sorted: %v", keys)
	}
}

func TestSortedKeys_Empty(t *testing.T) {
	keys := sortedKeys(map[string]bool{})
	if len(keys) != 0 {
		t.Errorf("expected empty, got %v", keys)
	}
}

// --- SearchPriceGrid validation ---

func TestSearchPriceGrid_MissingParams(t *testing.T) {
	ctx := t.Context()
	_, err := SearchPriceGrid(ctx, "", "BCN", GridOptions{})
	if err == nil {
		t.Error("expected error for empty origin")
	}

	_, err = SearchPriceGrid(ctx, "HEL", "", GridOptions{})
	if err == nil {
		t.Error("expected error for empty destination")
	}
}
