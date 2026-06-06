package flights

// Travelpayouts / Aviasales price-signal source (OPT-IN, token-gated).
//
// Travelpayouts (consumer brands Aviasales for flights, Hotellook for hotels)
// is a travel affiliate meta-search that aggregates cached "cheapest price
// seen" data across many airlines/OTAs. It is useful as a supplementary
// PRICE SIGNAL (cheapest fares found by other users in the last ~48h), NOT as
// a source of live, bookable itineraries — so it is deliberately kept OUT of
// the bookable flight-result merge and surfaced only via `trvl pricetrends`.
//
// OPT-IN: this source is a no-op unless TRAVELPAYOUTS_TOKEN is set, mirroring
// the AFKLM / Transavia opt-in pattern.
//
// CAVEAT (operator decision): Travelpayouts/Aviasales is a Russia-origin
// company. We default the query currency to EUR (their API historically
// defaults to RUB) and never route by default — the operator must explicitly
// opt in with their own free token. Coverage is strongest for Russia/CIS and
// weaker for Western Europe than the always-on sources (Google/Kiwi).
//
// Endpoint (no auth beyond the affiliate token):
//
//	GET https://api.travelpayouts.com/aviasales/v3/prices_for_dates
//	    ?origin=AMS&destination=HEL&departure_at=2026-06&currency=eur
//	    &one_way=true&sorting=price&limit=30&token=<TRAVELPAYOUTS_TOKEN>

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var travelpayoutsHost = "https" + "://" + "api.travelpayouts.com"

var travelpayoutsClient = &http.Client{Timeout: 20 * time.Second}

// PriceSignal is a single cached "cheapest seen" fare from Travelpayouts.
// It is indicative (not guaranteed bookable) and carries a deeplink.
type PriceSignal struct {
	Origin      string  `json:"origin"`
	Destination string  `json:"destination"`
	DepartAt    string  `json:"depart_at"`
	ReturnAt    string  `json:"return_at,omitempty"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Airline     string  `json:"airline"`
	Transfers   int     `json:"transfers"`
	BookingURL  string  `json:"booking_url"`
}

// TravelpayoutsToken returns the configured affiliate token, or "".
func TravelpayoutsToken() string {
	return strings.TrimSpace(os.Getenv("TRAVELPAYOUTS_TOKEN"))
}

// TravelpayoutsConfigured reports whether the opt-in token is present.
func TravelpayoutsConfigured() bool {
	return TravelpayoutsToken() != ""
}

type travelpayoutsResponse struct {
	Success  bool   `json:"success"`
	Currency string `json:"currency"`
	Error    string `json:"error"`
	Data     []struct {
		Origin      string  `json:"origin"`
		Destination string  `json:"destination"`
		DepartureAt string  `json:"departure_at"`
		ReturnAt    string  `json:"return_at"`
		Price       float64 `json:"price"`
		Airline     string  `json:"airline"`
		Transfers   int     `json:"transfers"`
		Link        string  `json:"link"`
	} `json:"data"`
}

// SearchTravelpayoutsPrices returns cached cheapest-price signals for a route.
// `when` is a YYYY-MM month or YYYY-MM-DD day (empty = next cheapest). It is a
// no-op (nil, nil) when TRAVELPAYOUTS_TOKEN is unset.
func SearchTravelpayoutsPrices(ctx context.Context, origin, destination, when, currency string, oneWay bool) ([]PriceSignal, error) {
	token := TravelpayoutsToken()
	if token == "" {
		return nil, nil
	}
	if currency == "" {
		currency = "EUR"
	}

	q := url.Values{}
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	if when != "" {
		q.Set("departure_at", when)
	}
	q.Set("currency", strings.ToLower(currency))
	q.Set("one_way", fmt.Sprintf("%t", oneWay))
	q.Set("sorting", "price")
	q.Set("limit", "30")
	q.Set("token", token)
	reqURL := travelpayoutsHost + "/aviasales/v3/prices_for_dates?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("travelpayouts: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := travelpayoutsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("travelpayouts: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("travelpayouts: unauthorized (status %d) — check TRAVELPAYOUTS_TOKEN", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("travelpayouts: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("travelpayouts: read body: %w", err)
	}
	return parseTravelpayouts(body, currency)
}

func parseTravelpayouts(body []byte, fallbackCurrency string) ([]PriceSignal, error) {
	var raw travelpayoutsResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("travelpayouts: decode: %w", err)
	}
	if !raw.Success {
		if raw.Error != "" {
			return nil, fmt.Errorf("travelpayouts: %s", raw.Error)
		}
		return nil, fmt.Errorf("travelpayouts: request not successful")
	}
	cur := strings.ToUpper(firstNonEmpty(raw.Currency, fallbackCurrency))
	out := make([]PriceSignal, 0, len(raw.Data))
	for _, d := range raw.Data {
		if d.Price <= 0 {
			continue
		}
		out = append(out, PriceSignal{
			Origin:      strings.ToUpper(d.Origin),
			Destination: strings.ToUpper(d.Destination),
			DepartAt:    d.DepartureAt,
			ReturnAt:    d.ReturnAt,
			Price:       d.Price,
			Currency:    cur,
			Airline:     strings.ToUpper(d.Airline),
			Transfers:   d.Transfers,
			BookingURL:  travelpayoutsLink(d.Link),
		})
	}
	return out, nil
}

// travelpayoutsLink turns the API's relative path into an absolute Aviasales URL.
func travelpayoutsLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "http") {
		return link
	}
	if !strings.HasPrefix(link, "/") {
		link = "/" + link
	}
	return "https" + "://" + "www.aviasales.com" + link
}
