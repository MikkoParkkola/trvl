package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TRVL.HOTELDATE.2 -- an MCP-created hotel watch and the CLI-created watch for
// the same stay must be ONE record.
//
// The MCP tool stored the stay in DepartFrom/DepartTo while both CLI paths use
// DepartDate/ReturnDate. targetKey (internal/watch/identity.go) hashes those
// four date fields separately, so the two surfaces produced different
// identities for the same request: asking for the same watch through both
// created two records that then polled and alerted independently.
//
// This drives the MCP handler and then adds the CLI-shaped watch through the
// store directly, which is what cmd/trvl does.
func TestHotelWatchIdentityMatchesCLIShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	_, structured, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "hotel",
		"location":     "Lisbon",
		"check_in":     "2027-03-01",
		"check_out":    "2027-03-05",
		"target_price": 200.0,
		"currency":     "EUR",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleWatchPrice: %v", err)
	}
	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mcpID, _ := resp["watch_id"].(string)
	if mcpID == "" {
		t.Fatal("no watch_id in response")
	}

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	// Exactly what cmd/trvl writes for the same stay.
	cliID, created, err := store.Add(watch.Watch{
		Type:        "hotel",
		Destination: "Lisbon",
		DepartDate:  "2027-03-01",
		ReturnDate:  "2027-03-05",
		BelowPrice:  200,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("Add CLI-shaped watch: %v", err)
	}

	if created {
		t.Errorf("the CLI-shaped watch was reported as newly created; it is the same stay the MCP tool just registered")
	}
	if cliID != mcpID {
		t.Errorf("CLI-shaped watch got id %q, MCP got %q -- the two surfaces disagree on identity, "+
			"so the same stay accumulates two records that poll and alert independently", cliID, mcpID)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(store.List()); got != 1 {
		t.Errorf("store holds %d watches for one stay, want 1", got)
	}

	// The stay must be on the canonical fields, or livecheck's checkHotel reads
	// empty dates and the watch silently polls nothing (TRVL.HOTELDATE.1).
	w := store.List()[0]
	if w.DepartDate != "2027-03-01" || w.ReturnDate != "2027-03-05" {
		t.Errorf("stored stay is (%q, %q) on the canonical fields, want (2027-03-01, 2027-03-05)",
			w.DepartDate, w.ReturnDate)
	}
}
