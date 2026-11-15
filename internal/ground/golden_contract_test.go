package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// goldenFerryhopperFrame is a one-itinerary Ferryhopper MCP answer in the
// current structuredContent shape (trimmed from the captured fixture).
const goldenFerryhopperFrame = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Found 1 trips matching the search criteria."}],"structuredContent":{"foundDirectItinerariesForTrip":[{"segments":[{"departurePort":{"name":"Piraeus, Athens"},"arrivalPort":{"name":"Santorini (Thira)"},"departureDateTime":"2026-07-15T07:00:00+03:00","arrivalDateTime":"2026-07-15T18:15:00+03:00","ownerCompany":{"name":"FAST FERRIES"},"vessel":{"name":"DIONISIOS SOLOMOS"},"accommodations":[{"type":"LOUNGE_DECK","expectedPrice":{"totalPriceInCents":4800}},{"type":"CABIN_FULL","expectedPrice":{"totalPriceInCents":7900}}]}],"deepLink":"https://www.ferryhopper.com/en/booking/results?golden=1"}]}}}`

// goldenGroundSearchResult is the wire contract of one ground search result
// as produced today by the full pipeline (normalise, filter, resolve, sort,
// confidence, statuses, completeness). Changing a field name or a status value
// must change this constant in the same commit, deliberately.
const goldenGroundSearchResult = `{
  "completeness": {
    "queried": 1,
    "state": "complete",
    "succeeded": 1
  },
  "count": 1,
  "provider_statuses": [
    {
      "id": "ferryhopper",
      "name": "ferryhopper",
      "results": 1,
      "status": "ok"
    }
  ],
  "routes": [
    {
      "arrival": {
        "city": "Santorini (Thira)",
        "station": "Santorini (Thira)",
        "time": "2026-07-15T18:15:00+03:00"
      },
      "booking_url": "https://www.ferryhopper.com/en/booking/results?golden=1",
      "cheapest_source": "fast ferries",
      "comparable_price": 48,
      "confidence": {
        "basis": "82% likely bookable — live price, 1-source corroboration",
        "freshness": "live",
        "label": "high",
        "rated": true,
        "score": 0.8166666666666667,
        "signals": [
          "freshness:live",
          "sources:1"
        ]
      },
      "currency": "EUR",
      "departure": {
        "city": "Piraeus, Athens",
        "station": "Piraeus, Athens",
        "time": "2026-07-15T07:00:00+03:00"
      },
      "duration_minutes": 675,
      "legs": null,
      "price": 48,
      "provider": "fast ferries",
      "sources": [
        {
          "booking_url": "https://www.ferryhopper.com/en/booking/results?golden=1",
          "currency": "EUR",
          "freshness": "live",
          "price": 48,
          "provider": "fast ferries",
          "retrieved_at": "RETRIEVED_AT"
        }
      ],
      "transfers": 0,
      "type": "ferry"
    }
  ],
  "success": true
}`

// maskGoldenVolatile replaces wall-clock fields so the golden is stable.
func maskGoldenVolatile(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "retrieved_at" {
				x[k] = "RETRIEVED_AT"
				continue
			}
			x[k] = maskGoldenVolatile(val)
		}
	case []any:
		for i := range x {
			x[i] = maskGoldenVolatile(x[i])
		}
	}
	return v
}

// TestGoldenGroundSearchResultContract pins today's JSON field names and status
// values for one ground search result produced end to end by searchByNameCore.
// dfds is requested but its route gate is false for this pair, so today it has
// no status row at all.
func TestGoldenGroundSearchResultContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", goldenFerryhopperFrame)
	}))
	defer srv.Close()

	origClient, origURL, origLimiter := ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter
	ferryhopperClient, ferryhopperMCPURL = srv.Client(), srv.URL
	ferryhopperLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		ferryhopperClient, ferryhopperMCPURL, ferryhopperLimiter = origClient, origURL, origLimiter
	})
	groundBreaker.RecordSuccess("ferryhopper")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := SearchOptions{Currency: "EUR", Providers: []string{"ferryhopper", "dfds"}, NoCache: true, NoHacks: true}
	res, err := searchByNameCore(ctx, "Piraeus", "Santorini", "2026-07-15", opts, "golden-contract", false)
	if err != nil {
		t.Fatalf("searchByNameCore: %v", err)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := json.MarshalIndent(maskGoldenVolatile(generic), "", "  ")
	if err != nil {
		t.Fatalf("indent: %v", err)
	}
	if string(got) != goldenGroundSearchResult {
		t.Fatalf("ground search result JSON drifted from the golden contract.\n--- got ---\n%s", got)
	}
}
