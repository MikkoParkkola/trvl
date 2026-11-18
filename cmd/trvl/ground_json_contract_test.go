package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// goldenGroundCLIJSON is what `trvl ground ... --format json` writes for one
// result: the command hands the search result to models.FormatJSON. The
// command itself has no offline seam for the search, so this pins the
// serializer output for a representative result (live-priced route with one
// source, price_provenance on route and source, comparable_price on the source,
// and ok / timeout / rate_limited / checked_no_hit / route_not_served status
// rows), not the command end to end.
const goldenGroundCLIJSON = `{
  "success": true,
  "count": 1,
  "routes": [
    {
      "provider": "fast ferries",
      "type": "ferry",
      "price": 48,
      "currency": "EUR",
      "price_provenance": "live",
      "comparable_price": 48,
      "duration_minutes": 675,
      "departure": {
        "city": "Piraeus, Athens",
        "station": "Piraeus, Athens",
        "time": "2026-07-15T07:00:00+03:00"
      },
      "arrival": {
        "city": "Santorini (Thira)",
        "station": "Santorini (Thira)",
        "time": "2026-07-15T18:15:00+03:00"
      },
      "transfers": 0,
      "legs": null,
      "booking_url": "https://www.ferryhopper.com/en/booking/results?golden=1",
      "sources": [
        {
          "provider": "fast ferries",
          "price": 48,
          "currency": "EUR",
          "price_provenance": "live",
          "comparable_price": 48,
          "booking_url": "https://www.ferryhopper.com/en/booking/results?golden=1",
          "retrieved_at": "2026-07-01T12:00:00Z",
          "freshness": "live"
        }
      ],
      "cheapest_source": "fast ferries",
      "confidence": {
        "rated": true,
        "score": 0.8166666666666667,
        "label": "high",
        "freshness": "live",
        "basis": "82% likely bookable — live price, 1-source corroboration",
        "signals": [
          "freshness:live",
          "sources:1"
        ]
      }
    }
  ],
  "provider_statuses": [
    {
      "id": "eckeroline",
      "name": "eckeroline",
      "status": "timeout",
      "error": "eckeroline homepage: context deadline exceeded",
      "reason": "timeout"
    },
    {
      "id": "ferryhopper",
      "name": "ferryhopper",
      "status": "ok",
      "results": 1
    },
    {
      "id": "rome2rio",
      "name": "rome2rio",
      "status": "rate_limited",
      "error": "rome2rio: HTTP 429: upstream provider rate-limited or temporarily unavailable",
      "reason": "rate_limited"
    },
    {
      "id": "stenaline",
      "name": "stenaline",
      "status": "checked_no_hit"
    },
    {
      "id": "vr",
      "name": "vr",
      "status": "skipped",
      "reason": "route_not_served"
    }
  ],
  "completeness": {
    "state": "partial",
    "queried": 4,
    "succeeded": 2,
    "missing": [
      "eckeroline",
      "rome2rio"
    ]
  }
}
`

func goldenGroundCLIResult() *models.GroundSearchResult {
	statuses := []models.ProviderStatus{
		{ID: "eckeroline", Name: "eckeroline", Status: models.StatusTimeout, Reason: models.ReasonTimeout, Error: "eckeroline homepage: context deadline exceeded"},
		{ID: "ferryhopper", Name: "ferryhopper", Status: models.StatusOK, Results: 1},
		{ID: "rome2rio", Name: "rome2rio", Status: models.StatusRateLimited, Error: "rome2rio: HTTP 429: " + models.ErrRateLimited.Error()},
		{ID: "stenaline", Name: "stenaline", Status: models.StatusCheckedNoHit},
		{ID: "vr", Name: "vr", Status: models.StatusSkipped, Reason: models.ReasonRouteNotServed},
	}
	url := "https://www.ferryhopper.com/en/booking/results?golden=1"
	return &models.GroundSearchResult{
		Success: true,
		Count:   1,
		Routes: []models.GroundRoute{{
			Provider:        "fast ferries",
			Type:            "ferry",
			Price:           48,
			Currency:        "EUR",
			PriceProvenance: models.PriceLive,
			ComparablePrice: 48,
			Duration:        675,
			Departure:       models.GroundStop{City: "Piraeus, Athens", Station: "Piraeus, Athens", Time: "2026-07-15T07:00:00+03:00"},
			Arrival:         models.GroundStop{City: "Santorini (Thira)", Station: "Santorini (Thira)", Time: "2026-07-15T18:15:00+03:00"},
			BookingURL:      url,
			Sources: []models.PriceSource{{
				Provider:        "fast ferries",
				Price:           48,
				Currency:        "EUR",
				Provenance:      models.PriceLive,
				ComparablePrice: 48,
				BookingURL:      url,
				RetrievedAt:     time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
				Freshness:       models.FreshnessLive,
			}},
			CheapestSource: "fast ferries",
			Confidence: &models.Confidence{
				Rated:     true,
				Score:     0.8166666666666667,
				Label:     models.ConfidenceHigh,
				Freshness: models.FreshnessLive,
				Basis:     "82% likely bookable — live price, 1-source corroboration",
				Signals:   []string{"freshness:live", "sources:1"},
			},
		}},
		ProviderStatuses: statuses,
		Completeness:     models.ComputeCompleteness(statuses),
	}
}

// TestGoldenGroundCLIJSONContract pins today's CLI JSON field names and status
// values for one ground result.
func TestGoldenGroundCLIJSONContract(t *testing.T) {
	var buf bytes.Buffer
	if err := models.FormatJSON(&buf, goldenGroundCLIResult()); err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	if buf.String() != goldenGroundCLIJSON {
		t.Fatalf("ground CLI JSON drifted from the golden contract.\n--- got ---\n%s", buf.String())
	}
}
