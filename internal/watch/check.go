package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotelarb"
)

var webhookHTTPClient = http.DefaultClient

// SetWebhookHTTPClientForTest swaps the webhook HTTP client and returns the previous client.
func SetWebhookHTTPClientForTest(client *http.Client) *http.Client {
	prev := webhookHTTPClient
	if client == nil {
		webhookHTTPClient = http.DefaultClient
	} else {
		webhookHTTPClient = client
	}
	return prev
}

// PriceChecker retrieves the current cheapest price for a route.
// Implementations bridge to flights.SearchFlights or hotels.SearchHotels
// without creating an import dependency from the watch package.
type PriceChecker interface {
	// CheckPrice returns the cheapest price and currency for the given watch.
	// For date-range and route watches, also returns the cheapest date found.
	// Returns 0 price if no results are found (not an error).
	CheckPrice(ctx context.Context, w Watch) (price float64, currency string, cheapestDate string, err error)
}

// RoomChecker retrieves available rooms for a hotel and matches them against criteria.
// Implementations bridge to hotels.GetRoomAvailability without creating an import
// dependency from the watch package.
type RoomChecker interface {
	// CheckRooms returns matching rooms for a room watch. Each returned RoomMatch
	// contains the room name, description, and price. Returns nil if no matches.
	CheckRooms(ctx context.Context, w Watch) ([]RoomMatch, error)
}

// RoomMatch represents a room that matched the watch keywords.
type RoomMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Provider    string  `json:"provider,omitempty"`
}

// CheckResult holds the outcome of checking a single watch.
type CheckResult struct {
	Watch                     Watch
	NewPrice                  float64
	Currency                  string
	PrevPrice                 float64
	BelowGoal                 bool    // price dropped below threshold
	PriceDrop                 float64 // negative = price decreased (good)
	CheapestDate              string  // for range/route watches: which date was cheapest
	RoomFound                 bool    // room watch: a matching room was found
	RoomMatches               []RoomMatch
	LastMinuteDeal            bool
	LastMinuteDiscountPercent float64
	Error                     error
}

// CheckAll checks all watches using the provided price checker and records
// results in the store. Pauses 3 seconds between checks to respect API rate limits.
// Returns a result for each watch.
func CheckAll(ctx context.Context, store *Store, checker PriceChecker) []CheckResult {
	return CheckAllWithRooms(ctx, store, checker, nil)
}

// CheckAllWithRooms checks all watches, using the room checker for room-type watches
// and the price checker for flight/hotel watches.
func CheckAllWithRooms(ctx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(ctx, ctx, store, checker, roomChecker, store.List())
}

// CheckAllWithRoomsAndWebhookContext checks all watches while allowing webhook
// delivery to outlive the check timeout. The webhook context should typically be
// a longer-lived parent context that is canceled when the caller is shutting
// down.
func CheckAllWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx, store, checker, roomChecker, store.List())
}

func checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker, watches []Watch) []CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	results := make([]CheckResult, 0, len(watches))

	for i, w := range watches {
		var r CheckResult
		if w.IsRoomWatch() && roomChecker != nil {
			r = checkRoomWithWebhookContext(checkCtx, webhookCtx, store, roomChecker, w)
		} else if w.IsRoomWatch() {
			r = CheckResult{Watch: w, Error: fmt.Errorf("room checker not configured")}
		} else {
			r = checkOneWithWebhookContext(checkCtx, webhookCtx, store, checker, w)
		}
		results = append(results, r)

		// Pause between checks to respect rate limits (skip after last).
		if i < len(watches)-1 {
			select {
			case <-checkCtx.Done():
				return results
			case <-time.After(3 * time.Second):
			}
		}
	}
	return results
}

// checkOne performs a price check for a single watch.
func checkOne(ctx context.Context, store *Store, checker PriceChecker, w Watch) CheckResult {
	return checkOneWithWebhookContext(ctx, ctx, store, checker, w)
}

func checkOneWithWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, w Watch) CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	price, currency, cheapestDate, err := checker.CheckPrice(checkCtx, w)
	if err != nil {
		return CheckResult{Watch: w, Error: err}
	}

	result := CheckResult{
		Watch:        w,
		NewPrice:     price,
		Currency:     currency,
		PrevPrice:    w.LastPrice,
		CheapestDate: cheapestDate,
	}

	if price > 0 {
		// Calculate price change.
		if w.LastPrice > 0 {
			result.PriceDrop = price - w.LastPrice
		}

		if signal := detectWatchLastMinuteDeal(w, price); signal.Triggered {
			result.LastMinuteDeal = true
			result.LastMinuteDiscountPercent = signal.DiscountPercent
		}

		// Check threshold.
		if w.BelowPrice > 0 && price <= w.BelowPrice {
			result.BelowGoal = true
		}

		// Update watch state.
		w.LastCheck = time.Now()
		w.LastPrice = price
		w.Currency = currency
		if cheapestDate != "" {
			w.CheapestDate = cheapestDate
		}
		if w.LowestPrice == 0 || price < w.LowestPrice {
			w.LowestPrice = price
		}

		// Persist updates.
		if err := store.UpdateWatch(w); err != nil {
			result.Error = fmt.Errorf("update watch: %w", err)
			return result
		}

		if err := store.RecordPrice(w.ID, price, currency); err != nil {
			result.Error = fmt.Errorf("record price: %w", err)
			return result
		}

		// Update the result's watch to reflect saved state.
		result.Watch = w

		// Fire webhook on price drop. The webhook context can outlive the check
		// timeout, but should still be canceled when the scheduler stops.
		if result.PriceDrop < 0 || result.LastMinuteDeal {
			go fireWebhook(webhookCtx, result)
		}
	}

	return result
}

func detectWatchLastMinuteDeal(w Watch, currentPrice float64) hotelarb.LastMinuteSignal {
	if !w.LastMinuteMode || w.Type != "hotel" {
		return hotelarb.LastMinuteSignal{}
	}
	checkIn := w.DepartDate
	if checkIn == "" {
		checkIn = w.DepartFrom
	}
	parsed, err := time.Parse(watchDateLayout, checkIn)
	if err != nil {
		return hotelarb.LastMinuteSignal{}
	}
	return hotelarb.DetectLastMinuteDeal(time.Now(), parsed, w.LastPrice, currentPrice, hotelarb.LastMinuteOptions{
		DropPercentThreshold: w.LastMinuteDropPct,
	})
}

// checkRoom performs a room availability check for a room watch.
func checkRoom(ctx context.Context, store *Store, checker RoomChecker, w Watch) CheckResult {
	return checkRoomWithWebhookContext(ctx, ctx, store, checker, w)
}

func checkRoomWithWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker RoomChecker, w Watch) CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	matches, err := checker.CheckRooms(checkCtx, w)
	if err != nil {
		return CheckResult{Watch: w, Error: err}
	}

	result := CheckResult{
		Watch:       w,
		RoomMatches: matches,
		RoomFound:   len(matches) > 0,
	}

	// If matches found, record the cheapest matching room price.
	if len(matches) > 0 {
		cheapest := matches[0]
		for _, m := range matches[1:] {
			if m.Price > 0 && (cheapest.Price == 0 || m.Price < cheapest.Price) {
				cheapest = m
			}
		}
		result.NewPrice = cheapest.Price
		result.Currency = cheapest.Currency

		// Check threshold.
		if w.BelowPrice > 0 && cheapest.Price > 0 && cheapest.Price <= w.BelowPrice {
			result.BelowGoal = true
		}

		// Calculate price change from last check.
		result.PrevPrice = w.LastPrice
		if w.LastPrice > 0 && cheapest.Price > 0 {
			result.PriceDrop = cheapest.Price - w.LastPrice
		}

		// Update watch state.
		w.LastCheck = time.Now()
		w.MatchedRoom = cheapest.Name
		if cheapest.Price > 0 {
			w.LastPrice = cheapest.Price
			w.Currency = cheapest.Currency
			if w.LowestPrice == 0 || cheapest.Price < w.LowestPrice {
				w.LowestPrice = cheapest.Price
			}
		}
	} else {
		// No matches — still mark as checked.
		w.LastCheck = time.Now()
	}

	// Persist updates.
	if err := store.UpdateWatch(w); err != nil {
		result.Error = fmt.Errorf("update watch: %w", err)
		return result
	}
	if result.NewPrice > 0 {
		if err := store.RecordPrice(w.ID, result.NewPrice, result.Currency); err != nil {
			result.Error = fmt.Errorf("record price: %w", err)
			return result
		}
	}

	result.Watch = w

	// Fire webhook on price drop.
	if result.PriceDrop < 0 {
		go fireWebhook(webhookCtx, result)
	}

	return result
}

func normalizeCheckAndWebhookContexts(checkCtx, webhookCtx context.Context) (context.Context, context.Context) {
	if checkCtx == nil {
		checkCtx = context.Background()
	}
	if webhookCtx == nil {
		webhookCtx = checkCtx
	}
	return checkCtx, webhookCtx
}

// webhookPayload is the JSON body POSTed to a watch's WebhookURL on price drop.
type webhookPayload struct {
	WatchID                   string  `json:"watch_id"`
	Type                      string  `json:"type"`
	Origin                    string  `json:"origin,omitempty"`
	Destination               string  `json:"destination,omitempty"`
	HotelName                 string  `json:"hotel_name,omitempty"`
	NewPrice                  float64 `json:"new_price"`
	PrevPrice                 float64 `json:"prev_price"`
	Currency                  string  `json:"currency"`
	PriceDrop                 float64 `json:"price_drop"`
	BelowGoal                 bool    `json:"below_goal"`
	LastMinuteDeal            bool    `json:"last_minute_deal,omitempty"`
	LastMinuteDiscountPercent float64 `json:"last_minute_discount_percent,omitempty"`
	Timestamp                 string  `json:"timestamp"`
}

// fireWebhook sends a price-drop notification to the watch's WebhookURL.
// It is fire-and-forget with a 10-second timeout; errors are logged but not returned.
func fireWebhook(ctx context.Context, r CheckResult) {
	if r.Watch.WebhookURL == "" {
		return
	}

	payload := webhookPayload{
		WatchID:                   r.Watch.ID,
		Type:                      r.Watch.Type,
		Origin:                    r.Watch.Origin,
		Destination:               r.Watch.Destination,
		HotelName:                 r.Watch.HotelName,
		NewPrice:                  r.NewPrice,
		PrevPrice:                 r.PrevPrice,
		Currency:                  r.Currency,
		PriceDrop:                 r.PriceDrop,
		BelowGoal:                 r.BelowGoal,
		LastMinuteDeal:            r.LastMinuteDeal,
		LastMinuteDiscountPercent: r.LastMinuteDiscountPercent,
		Timestamp:                 time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("webhook: marshal payload", "watch_id", r.Watch.ID, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Watch.WebhookURL, bytes.NewReader(body))
	if err != nil {
		slog.Warn("webhook: create request", "watch_id", r.Watch.ID, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := webhookHTTPClient.Do(req)
	if err != nil {
		slog.Warn("webhook: POST failed", "watch_id", r.Watch.ID, "url", r.Watch.WebhookURL, "err", err)
		return
	}
	_ = resp.Body.Close()
}
