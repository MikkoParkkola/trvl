package livecheck

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TRVL.HOTELDATE.1 -- a hotel watch must poll the stay dates it was created
// with, whichever surface created it.
//
// The MCP watch_price tool stores check_in/check_out in DepartFrom/DepartTo
// (mcp/tools_watch_price.go: "Store hotel check-in/check-out using the
// date-range fields"), while checkHotel reads DepartDate/ReturnDate. Neither is
// wrong on its own; together they mean an MCP-created hotel watch polls with
// EMPTY dates and no error is raised anywhere -- the watch just never reports
// anything useful. The CLI writes DepartDate, so it works, which is why this
// went unnoticed.
//
// IsRouteWatch's next-weekend fallback does not rescue it either: that requires
// DepartDate, DepartFrom and DepartTo all empty, and DepartFrom is set here.
//
// This tests hotelStayDates rather than checkHotel itself because checkHotel
// issues a live provider request. The extraction is the whole fix: one place
// that answers "what stay does this watch mean", used by the checker.
func TestHotelStayDates_MCPStyleWatch(t *testing.T) {
	// Exactly what mcp/tools_watch_price.go stores for a hotel watch.
	w := watch.Watch{
		Type:        "hotel",
		Destination: "Lisbon",
		DepartFrom:  "2027-03-01",
		DepartTo:    "2027-03-05",
	}

	in, out := hotelStayDates(w)
	if in != "2027-03-01" || out != "2027-03-05" {
		t.Errorf("MCP-created hotel watch resolves to stay (%q, %q), want (2027-03-01, 2027-03-05) -- "+
			"it would poll with the dates the caller never sees being dropped", in, out)
	}
}

// TRVL.HOTELDATE.1 -- the CLI shape must keep working unchanged.
func TestHotelStayDates_CLIStyleWatch(t *testing.T) {
	w := watch.Watch{
		Type:        "hotel",
		Destination: "Lisbon",
		DepartDate:  "2027-03-01",
		ReturnDate:  "2027-03-05",
	}

	in, out := hotelStayDates(w)
	if in != "2027-03-01" || out != "2027-03-05" {
		t.Errorf("CLI-created hotel watch resolves to stay (%q, %q), want (2027-03-01, 2027-03-05)", in, out)
	}
}

// A dateless hotel watch keeps the next-weekend default. Without this, a fix
// that simply preferred DepartFrom/DepartTo could satisfy the test above while
// silently dropping the fallback.
func TestHotelStayDates_DatelessFallsBackToNextWeekend(t *testing.T) {
	w := watch.Watch{Type: "hotel", Destination: "Lisbon"}

	in, out := hotelStayDates(w)
	if in == "" || out == "" {
		t.Fatalf("dateless hotel watch resolved to (%q, %q); the next-weekend default was lost", in, out)
	}
	if !strings.HasPrefix(in, "20") || !strings.HasPrefix(out, "20") {
		t.Errorf("fallback produced non-date values (%q, %q)", in, out)
	}
	if in >= out {
		t.Errorf("fallback check-in %q is not before check-out %q", in, out)
	}
}
