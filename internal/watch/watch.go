// Package watch provides price tracking for flights and hotels.
// It stores watch definitions and price history as JSON files
// under ~/.trvl/ and supports threshold-based alerting.
package watch

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Watch represents a price tracking rule for a flight or hotel route.
//
// Three watch modes:
//   - Specific date: DepartDate set, no DepartFrom/DepartTo → checks one date
//   - Date range: DepartFrom + DepartTo set → checks cheapest across range
//   - Route watch: no dates at all → checks next 60 days for cheapest on any date
type Watch struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"` // "flight", "hotel", or "room"
	Origin       string    `json:"origin"`
	Destination  string    `json:"destination"`
	DepartDate   string    `json:"depart_date,omitempty"`
	ReturnDate   string    `json:"return_date,omitempty"`
	DepartFrom   string    `json:"depart_from,omitempty"` // date range start (YYYY-MM-DD)
	DepartTo     string    `json:"depart_to,omitempty"`   // date range end (YYYY-MM-DD)
	BelowPrice   float64   `json:"below_price"`
	Currency     string    `json:"currency"`
	CreatedAt    time.Time `json:"created_at"`
	LastCheck    time.Time `json:"last_check"`
	LastPrice    float64   `json:"last_price"`
	LowestPrice  float64   `json:"lowest_price"`
	CheapestDate string    `json:"cheapest_date,omitempty"` // which date had the lowest price

	// RenewedAt is the last time a USER expressed interest in this watch: set on
	// creation and refreshed whenever Store.Add is called for the same target.
	// It is deliberately distinct from LastCheck, which the scheduler updates on
	// its own and therefore never signals abandonment. Route watches age out
	// against this (see isActive / routeWatchTTL).
	RenewedAt time.Time `json:"renewed_at,omitempty"`

	// Last-minute hotel mode flags sub-48h availability when the current price
	// is materially below LastPrice. Drop threshold defaults to 25%.
	LastMinuteMode    bool    `json:"last_minute_mode,omitempty"`
	LastMinuteDropPct float64 `json:"last_minute_drop_pct,omitempty"`

	// Webhook notification URL. When set and a price drop is detected, a JSON
	// payload is POSTed to this URL (fire-and-forget, 10s timeout).
	WebhookURL string `json:"webhook_url,omitempty"`

	// Proactive price-drop alerting (innovation #6: pull -> push).
	// AlertDropPct / AlertDropAbs configure how far the fare must fall below the
	// captured baseline before trvl proactively alerts. Either limb qualifies;
	// when both are zero a sane default (10%) applies. BaselinePrice is the
	// reference fare (captured on first observation, tracks the running peak) and
	// LastAlertedPrice is dedup state so a single drop alerts exactly once.
	AlertDropPct     float64 `json:"alert_drop_pct,omitempty"`
	AlertDropAbs     float64 `json:"alert_drop_abs,omitempty"`
	BaselinePrice    float64 `json:"baseline_price,omitempty"`
	LastAlertedPrice float64 `json:"last_alerted_price,omitempty"`

	// Room watch fields (Type == "room").
	HotelName    string   `json:"hotel_name,omitempty"`    // hotel name for room availability lookups
	RoomKeywords []string `json:"room_keywords,omitempty"` // all keywords must match room name+description
	MatchedRoom  string   `json:"matched_room,omitempty"`  // last matched room name (for display)

	// Opportunity watch fields (Type == "opportunity").
	// Favourites defaults to BucketList ∪ PreviousTrips ∩ AirportAffinity≥0.3 if empty.
	Favourites []string `json:"favourites,omitempty"`  // IATA codes
	WindowFrom string   `json:"window_from,omitempty"` // YYYY-MM-DD or "next_Nd" (e.g. "next_30d")
	WindowTo   string   `json:"window_to,omitempty"`   // YYYY-MM-DD or "next_Nd"
	MinScore   int      `json:"min_score,omitempty"`   // default 85
	MinNights  int      `json:"min_nights,omitempty"`  // default 3
	MaxNights  int      `json:"max_nights,omitempty"`  // default 14
}

// SameTarget reports whether two watches monitor the SAME thing, ignoring
// accumulated state (prices, check times) and adjustable thresholds.
//
// This is watch identity. "Watch HEL->BCN" asked twice is one watch, not two:
// re-asking expresses the same intent and should update it, not accumulate.
// Without this, every agent session that called watch_price added another row.
// One real store reached 468 permanently-active watches covering 4 distinct
// routes — HEL->BCN alone was watched 319 times — and every one of them was
// re-checked against live providers every 30 minutes, forever.
//
// BelowPrice, Currency, webhook and alert settings are deliberately NOT part of
// identity: re-watching a route with a new target price updates the target
// rather than creating a rival watch for the same route.
func (w Watch) SameTarget(other Watch) bool {
	if w.Type != other.Type {
		return false
	}
	if w.IsOpportunityWatch() {
		return w.WindowFrom == other.WindowFrom &&
			w.WindowTo == other.WindowTo &&
			w.MinScore == other.MinScore &&
			w.MinNights == other.MinNights &&
			w.MaxNights == other.MaxNights &&
			equalStrings(w.Favourites, other.Favourites)
	}
	return w.Origin == other.Origin &&
		w.Destination == other.Destination &&
		w.DepartDate == other.DepartDate &&
		w.ReturnDate == other.ReturnDate &&
		w.DepartFrom == other.DepartFrom &&
		w.DepartTo == other.DepartTo &&
		w.HotelName == other.HotelName &&
		equalStrings(w.RoomKeywords, other.RoomKeywords)
}

// equalStrings compares two string slices order-insensitively, treating nil and
// empty as equal. Keyword order is not part of a watch's meaning.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// IsRouteWatch returns true if this watch monitors a route without specific dates.
func (w Watch) IsRouteWatch() bool {
	return w.DepartDate == "" && w.DepartFrom == "" && w.DepartTo == ""
}

// IsDateRange returns true if this watch monitors a date range.
func (w Watch) IsDateRange() bool {
	return w.DepartFrom != "" && w.DepartTo != ""
}

// IsRoomWatch returns true if this watch monitors room availability.
func (w Watch) IsRoomWatch() bool {
	return w.Type == "room"
}

// IsOpportunityWatch returns true if this watch is an opportunity watch.
func (w Watch) IsOpportunityWatch() bool {
	return w.Type == "opportunity"
}

// MatchRoomKeywords checks whether all keywords appear (case-insensitive) in the
// combined room name and description. Returns true if every keyword is found.
func MatchRoomKeywords(keywords []string, roomName, roomDescription string) bool {
	if len(keywords) == 0 {
		return false
	}
	text := strings.ToLower(roomName + " " + roomDescription)
	for _, kw := range keywords {
		if !strings.Contains(text, strings.ToLower(kw)) {
			return false
		}
	}
	return true
}

const watchDateLayout = "2006-01-02"

// Validate rejects malformed or ambiguous watch date combinations before they
// get persisted and later fail during background checks.
func (w Watch) Validate() error {
	if err := validateWatchDate("depart date", w.DepartDate); err != nil {
		return err
	}
	if err := validateWatchDate("return date", w.ReturnDate); err != nil {
		return err
	}
	if err := validateWatchDate("date range start", w.DepartFrom); err != nil {
		return err
	}
	if err := validateWatchDate("date range end", w.DepartTo); err != nil {
		return err
	}

	// Room watch validation.
	if w.IsRoomWatch() {
		if w.HotelName == "" {
			return fmt.Errorf("room watch requires a hotel name")
		}
		if len(w.RoomKeywords) == 0 {
			return fmt.Errorf("room watch requires at least one keyword")
		}
		if w.DepartDate == "" || w.ReturnDate == "" {
			return fmt.Errorf("room watch requires check-in (depart_date) and check-out (return_date)")
		}
		return nil
	}

	// Opportunity watch validation.
	if w.IsOpportunityWatch() {
		if len(w.Favourites) == 0 && w.MinNights <= 0 {
			return fmt.Errorf("opportunity watch requires favourites or a min_nights value")
		}
		return nil
	}

	if w.LastMinuteMode {
		if w.Type != "hotel" {
			return fmt.Errorf("last-minute mode is only supported for hotel watches")
		}
		if w.LastMinuteDropPct < 0 {
			return fmt.Errorf("last-minute drop threshold must be non-negative")
		}
	}

	if w.DepartDate != "" && (w.DepartFrom != "" || w.DepartTo != "") {
		return fmt.Errorf("cannot combine a specific depart date with a date range")
	}
	if (w.DepartFrom == "") != (w.DepartTo == "") {
		return fmt.Errorf("date range requires both start and end dates")
	}
	if w.IsDateRange() {
		from, _ := time.Parse(watchDateLayout, w.DepartFrom)
		to, _ := time.Parse(watchDateLayout, w.DepartTo)
		if from.After(to) {
			return fmt.Errorf("date range start must be on or before end")
		}
	}
	return nil
}

func validateWatchDate(label, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(watchDateLayout, value); err != nil {
		return fmt.Errorf("%s must use YYYY-MM-DD", label)
	}
	return nil
}

// PricePoint records a single price observation.
//
// Observations come from two sources, distinguished by which key is set:
//   - Watch scheduler checks set WatchID (the original, watch-scoped corpus).
//   - Ad-hoc searches set RouteKey (MIK-6229), so the history corpus compounds
//     across every searched route, not only the watched ones.
//
// Exactly one of WatchID / RouteKey is expected to be set on a given point;
// older records carry only WatchID and remain valid.
type PricePoint struct {
	WatchID   string    `json:"watch_id,omitempty"`
	RouteKey  string    `json:"route_key,omitempty"`
	Price     float64   `json:"price"`
	Currency  string    `json:"currency"`
	Timestamp time.Time `json:"timestamp"`
}

// RouteKey builds the canonical history key for an ad-hoc observation. The key
// is provider-agnostic and date-scoped so that a "flight AMS->VLC on
// 2026-07-15" search accrues to the same series regardless of which provider
// returned the cheapest fare. Inputs are upper-cased and trimmed; an empty
// date is allowed (route-level series).
func RouteKey(kind, origin, destination, date string) string {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	return strings.Join([]string{norm(kind), norm(origin), norm(destination), strings.TrimSpace(date)}, "|")
}

// Sparkline renders a compact Unicode sparkline from price history.
// Uses the last N points (up to maxPoints) scaled to 8 block-element levels.
// Returns "" if fewer than 2 data points exist.
func Sparkline(history []PricePoint, maxPoints int) string {
	if len(history) < 2 {
		return ""
	}

	// Take the tail.
	start := 0
	if len(history) > maxPoints {
		start = len(history) - maxPoints
	}
	pts := history[start:]

	// Find min/max for scaling.
	lo, hi := pts[0].Price, pts[0].Price
	for _, p := range pts[1:] {
		if p.Price < lo {
			lo = p.Price
		}
		if p.Price > hi {
			hi = p.Price
		}
	}

	bars := []rune("▁▂▃▄▅▆▇█")
	spread := hi - lo
	var b []rune
	for _, p := range pts {
		idx := 0
		if spread > 0 {
			idx = int((p.Price - lo) / spread * float64(len(bars)-1))
			if idx >= len(bars) {
				idx = len(bars) - 1
			}
		} else {
			idx = len(bars) / 2 // flat line
		}
		b = append(b, bars[idx])
	}
	return string(b)
}

// TrendArrow returns a directional indicator comparing the last two prices.
// Returns "" if there are fewer than 2 data points.
func TrendArrow(history []PricePoint) string {
	if len(history) < 2 {
		return ""
	}
	prev := history[len(history)-2].Price
	curr := history[len(history)-1].Price
	switch {
	case curr < prev:
		return "↓"
	case curr > prev:
		return "↑"
	default:
		return "→"
	}
}
