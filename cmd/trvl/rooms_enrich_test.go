package main

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func bptr(v bool) *bool { return &v }

func TestBoardLabel(t *testing.T) {
	cases := []struct {
		name string
		room hotels.RoomType
		want string
	}{
		{"normalized board", hotels.RoomType{Board: "breakfast_included"}, "Breakfast included"},
		{"half board", hotels.RoomType{Board: "half_board"}, "Half board"},
		{"breakfast flag only", hotels.RoomType{BreakfastIncluded: bptr(true)}, "Breakfast included"},
		{"no breakfast flag", hotels.RoomType{BreakfastIncluded: bptr(false)}, "No breakfast"},
		{"absent renders blank", hotels.RoomType{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := boardLabel(tc.room); got != tc.want {
				t.Fatalf("boardLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCancellationLabel(t *testing.T) {
	cases := []struct {
		name string
		room hotels.RoomType
		want string
	}{
		{"normalized policy", hotels.RoomType{CancellationPolicy: "free_cancellation"}, "Free cancellation"},
		{"non refundable policy", hotels.RoomType{CancellationPolicy: "non_refundable"}, "Non refundable"},
		{"free cancellation flag", hotels.RoomType{FreeCancellation: bptr(true)}, "Free cancellation"},
		{"refundable flag", hotels.RoomType{Refundable: bptr(true)}, "Refundable"},
		{"non refundable flag", hotels.RoomType{Refundable: bptr(false)}, "Non refundable"},
		{"absent renders blank", hotels.RoomType{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cancellationLabel(tc.room); got != tc.want {
				t.Fatalf("cancellationLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatRoomsTable_RendersEnrichmentFields verifies that populated
// cancellation/board/bed metadata is surfaced in the CLI table and summary.
func TestFormatRoomsTable_RendersEnrichmentFields(t *testing.T) {
	models.UseColor = false
	defer func() { models.UseColor = false }()

	result := &hotels.RoomAvailability{
		Success:  true,
		Name:     "Test Hotel",
		CheckIn:  "2026-07-10",
		CheckOut: "2026-07-12",
		Rooms: []hotels.RoomType{
			{
				Name:               "Deluxe Room",
				Price:              160,
				Currency:           "EUR",
				BedType:            "1 king bed",
				Board:              "breakfast_included",
				BreakfastIncluded:  bptr(true),
				CancellationPolicy: "free_cancellation",
				FreeCancellation:   bptr(true),
			},
		},
	}

	out := captureStdout(t, func() {
		if err := formatRoomsTable(result); err != nil {
			t.Fatalf("formatRoomsTable: %v", err)
		}
	})

	for _, want := range []string{"Board", "Cancellation", "1 king bed", "Breakfast included", "Free cancellation"} {
		if !strings.Contains(out, want) {
			t.Errorf("rooms table output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// TestFormatRoomsTable_AbsentSafe verifies that rooms without enrichment data
// do not fabricate cancellation/board labels: the cells render blank and no
// false claim text appears.
func TestFormatRoomsTable_AbsentSafe(t *testing.T) {
	models.UseColor = false
	defer func() { models.UseColor = false }()

	result := &hotels.RoomAvailability{
		Success:  true,
		Name:     "Plain Hotel",
		CheckIn:  "2026-07-10",
		CheckOut: "2026-07-12",
		Rooms: []hotels.RoomType{
			{Name: "Standard Room", Price: 100, Currency: "EUR"},
		},
	}

	out := captureStdout(t, func() {
		if err := formatRoomsTable(result); err != nil {
			t.Fatalf("formatRoomsTable: %v", err)
		}
	})

	for _, forbidden := range []string{"Free cancellation", "Breakfast included", "Refundable", "Non refundable", "No breakfast"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("absent-safe violation: output contains fabricated %q\n--- output ---\n%s", forbidden, out)
		}
	}
}

func TestRoomHighlight(t *testing.T) {
	got := roomHighlight(hotels.RoomType{Board: "breakfast_included", CancellationPolicy: "free_cancellation"})
	if got != "Breakfast included, Free cancellation" {
		t.Fatalf("roomHighlight = %q", got)
	}
	if got := roomHighlight(hotels.RoomType{}); got != "" {
		t.Fatalf("roomHighlight(empty) = %q, want blank", got)
	}
}
