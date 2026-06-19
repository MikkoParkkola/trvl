package hotels

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestCaptureProbe_PriceRequestVariants isolates whether the empty yY52ce price
// response is caused by a stale ID format or a stale request-arg shape. It
// confirms the hotel ID is valid via the reviews RPC (known-good shape), then
// tries the price RPC with several ID encodings and logs payload presence.
//
// Opt-in: TRVL_TEST_LIVE_PROBES=1.
func TestCaptureProbe_PriceRequestVariants(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}

	checkIn := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	checkOut := time.Now().AddDate(0, 0, 31).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	search, err := SearchHotels(ctx, "Paris", HotelSearchOptions{
		CheckIn: checkIn, CheckOut: checkOut, Guests: 2, Currency: "EUR",
	})
	if err != nil || len(search.Hotels) == 0 {
		t.Fatalf("search failed: %v (n=%d)", err, len(search.Hotels))
	}
	var fullID string
	for _, h := range search.Hotels {
		if h.HotelID != "" {
			fullID = h.HotelID
			t.Logf("hotel %q id=%s", h.Name, h.HotelID)
			break
		}
	}
	if fullID == "" {
		t.Fatal("no HotelID in search results")
	}

	client := DefaultClient()

	// (1) Reviews RPC with the full ID — known-good shape, proves ID validity.
	revStatus, revBody, _ := client.BatchExecute(ctx, batchexec.BuildHotelReviewPayload(fullID, 5))
	t.Logf("[reviews ocp93e] full-id status=%d bodyLen=%d nonNullPayload=%v",
		revStatus, len(revBody), payloadNonNull(revBody, "ocp93e"))

	// (2) Price RPC across ID-format variants.
	ciArr, _ := parseDateArray(checkIn)
	coArr, _ := parseDateArray(checkOut)

	variants := map[string]string{"full": fullID}
	if i := strings.Index(fullID, ":"); i >= 0 {
		cidHex := fullID[i+1:]
		variants["cid_hex"] = cidHex
		if n, err := strconv.ParseUint(strings.TrimPrefix(cidHex, "0x"), 16, 64); err == nil {
			variants["cid_dec"] = strconv.FormatUint(n, 10)
		}
	}

	for name, id := range variants {
		status, body, err := client.BatchExecute(ctx, batchexec.BuildHotelPricePayload(id, ciArr, coArr, "EUR"))
		t.Logf("[price yY52ce] id-variant=%-8s id=%-40s status=%d bodyLen=%d nonNullPayload=%v err=%v",
			name, id, status, len(body), payloadNonNull(body, "yY52ce"), err)
	}
}

// payloadNonNull reports whether the wrb.fr envelope for rpcid carries a
// non-null payload string (index 2).
func payloadNonNull(body []byte, rpcid string) bool {
	entries, err := batchexec.DecodeBatchResponse(body)
	if err != nil {
		return false
	}
	for _, e := range entries {
		arr, ok := e.([]any)
		if !ok || len(arr) < 3 {
			continue
		}
		if id, _ := arr[1].(string); id == rpcid {
			if s, ok := arr[2].(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}
