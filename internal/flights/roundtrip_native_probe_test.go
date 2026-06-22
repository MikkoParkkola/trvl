package flights

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestProbeNativeRoundTrip hits live Google Flights with a NATIVE round-trip
// request (tripType=1, two segments) and reports the real response shape so we
// can decide whether the existing parser handles it. Opt-in:
//
//	TRVL_TEST_LIVE_PROBES=1 go test ./internal/flights -run TestProbeNativeRoundTrip -v
func TestProbeNativeRoundTrip(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run live native round-trip probe")
	}

	origin, destination := "HEL", "BCN"
	dep := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	ret := time.Now().AddDate(0, 1, 7).Format("2006-01-02")

	client := batchexec.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := SearchOptions{ReturnDate: ret, Adults: 1}
	opts.defaults()
	opts.ReturnDate = ret

	filters := buildFilters(origin, destination, dep, opts)
	encoded, err := batchexec.EncodeFlightFilters(filters)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	status, body, err := client.SearchFlightsGL(ctx, encoded, CurrencyToGL("EUR"))
	t.Logf("HTTP status=%d bodyLen=%d err=%v", status, len(body), err)
	if err != nil || status != 200 {
		t.Fatalf("request failed: status=%d err=%v", status, err)
	}

	inner, derr := batchexec.DecodeFlightResponse(body)
	if derr != nil {
		t.Fatalf("decode: %v (first 400 bytes: %s)", derr, truncBytes(body, 400))
	}

	t.Logf("isFlightPayload=%v", isFlightPayload(inner))
	if arr, ok := inner.([]any); ok {
		t.Logf("inner is []any len=%d", len(arr))
		for i, v := range arr {
			t.Logf("  inner[%d]: %s", i, kindOf(v))
		}
	} else {
		t.Logf("inner is NOT []any, kind=%s; dump=%s", kindOf(inner), jsonShort(inner, 600))
	}

	rawFlights, eerr := batchexec.ExtractFlightData(inner)
	if eerr != nil {
		t.Logf("ExtractFlightData ERROR: %v", eerr)
	} else {
		t.Logf("ExtractFlightData -> %d raw entries", len(rawFlights))
		flights := parseFlights(rawFlights)
		t.Logf("parseFlights -> %d flights", len(flights))
		for i, f := range flights {
			if i >= 5 {
				break
			}
			t.Logf("  flight[%d]: price=%.2f %s legs=%d stops=%d dur=%d",
				i, f.Price, f.Currency, len(f.Legs), f.Stops, f.Duration)
		}
	}
}

func truncBytes(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}

func kindOf(v any) string {
	switch x := v.(type) {
	case []any:
		return fmt.Sprintf("array(len=%d)", len(x))
	case map[string]any:
		return fmt.Sprintf("object(keys=%d)", len(x))
	case string:
		if len(x) > 40 {
			return fmt.Sprintf("string(len=%d)", len(x))
		}
		return fmt.Sprintf("string(%q)", x)
	case float64:
		return fmt.Sprintf("number(%v)", x)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func jsonShort(v any, n int) string {
	b, _ := json.Marshal(v)
	return truncBytes(b, n)
}

// TestProbeRoundTripOrchestrator runs the full round-trip orchestrator live and
// confirms native Google round-trip fares (FareRoundTrip) appear in the merged
// results. Opt-in via TRVL_TEST_LIVE_PROBES=1.
func TestProbeRoundTripOrchestrator(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("set TRVL_TEST_LIVE_PROBES=1 to run live orchestrator probe")
	}
	dep := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	ret := time.Now().AddDate(0, 1, 7).Format("2006-01-02")
	client := batchexec.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	res, err := searchRoundTripComposed(ctx, client, "HEL", "BCN", dep, SearchOptions{ReturnDate: ret, Adults: 1, Currency: "EUR"})
	if err != nil {
		t.Logf("orchestrator err: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	var native, split int
	for _, f := range res.Flights {
		switch f.FareType {
		case "round_trip":
			native++
		case "split_tickets":
			split++
		}
	}
	t.Logf("tripType=%s count=%d native_round_trip=%d split_tickets=%d", res.TripType, len(res.Flights), native, split)
	for i, f := range res.Flights {
		if i >= 4 {
			break
		}
		t.Logf("  [%d] %.2f %s fare=%s provider=%q legs=%d", i, f.Price, f.Currency, f.FareType, f.Provider, len(f.Legs))
	}
	for _, s := range res.ProviderStatuses {
		t.Logf("  status %s: %s results=%d", s.ID, s.Status, s.Results)
	}
	if native == 0 {
		t.Errorf("expected at least one native round-trip fare (Google/Kiwi) in merged results")
	}
}
