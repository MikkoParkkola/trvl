package afklm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	pathAvailableOffers          = "/opendata/offers/v3/available-offers"
	pathLowestFaresByDestination = "/opendata/offers/v3/lowest-fares-by-destination"
)

// AvailableOffers calls POST /opendata/offers/v3/available-offers.
// Returns (response, stale, error). stale=true means the response was served
// from a stale cache entry while a background refresh was triggered.
func (c *Client) AvailableOffers(ctx context.Context, req AvailableOffersRequest) (*AvailableOffersResponse, bool, error) {
	daysUntilDep := daysUntilDeparture(req, c.Now())
	rawBody, stale, err := c.do(ctx, pathAvailableOffers, req, daysUntilDep)
	if err != nil {
		return nil, false, err
	}
	var resp AvailableOffersResponse
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return nil, false, fmt.Errorf("afklm: parse available-offers response: %w", err)
	}
	return &resp, stale, nil
}

// LowestFaresByDestination calls POST /opendata/offers/v3/lowest-fares-by-destination.
func (c *Client) LowestFaresByDestination(ctx context.Context, req LowestFaresRequest) (*LowestFaresResponse, bool, error) {
	// Days until departure is computed from fromDate for TTL selection.
	daysUntilDep := daysFromISO(req.FromDate, c.Now())
	rawBody, stale, err := c.do(ctx, pathLowestFaresByDestination, req, daysUntilDep)
	if err != nil {
		return nil, false, err
	}
	var resp LowestFaresResponse
	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return nil, false, fmt.Errorf("afklm: parse lowest-fares response: %w", err)
	}
	return &resp, stale, nil
}

// daysUntilDeparture computes how many days from now until the first
// requested connection's departure. Returns 0 if it cannot be parsed.
func daysUntilDeparture(req AvailableOffersRequest, now time.Time) int {
	if len(req.RequestedConnections) == 0 {
		return 0
	}
	return daysFromISO(req.RequestedConnections[0].DepartureDate, now)
}

// daysFromISO parses an ISO date string ("2006-01-02") and returns days from now.
func daysFromISO(dateStr string, now time.Time) int {
	if dateStr == "" {
		return 0
	}
	dep, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		// Try RFC3339 (used by LowestFaresRequest.FromDate).
		dep, err = time.Parse(time.RFC3339, dateStr)
		if err != nil {
			return 0
		}
	}
	days := int(dep.UTC().Truncate(24*time.Hour).Sub(now.UTC().Truncate(24*time.Hour)).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}
