package hotels

import (
	"context"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestSearchHotels_ReturnsPartialResultsWhenAuxProviderIgnoresCancellation(t *testing.T) {
	origFetch := fetchHotelPageFullFn
	origBooking := SearchBooking
	origTimeout := hotelAuxProviderTimeout
	t.Cleanup(func() {
		fetchHotelPageFullFn = origFetch
		SearchBooking = origBooking
		hotelAuxProviderTimeout = origTimeout
	})

	fetchHotelPageFullFn = func(_ context.Context, _ *batchexec.Client, _ string, _ HotelSearchOptions, _ int, _ string) (parseResult, error) {
		return parseResult{Hotels: []models.HotelResult{{
			Name:     "Fast Ischia Hotel",
			Price:    120,
			Currency: "EUR",
		}}}, nil
	}

	releaseSlowProvider := make(chan struct{})
	slowProviderReturned := make(chan struct{})
	SearchBooking = func(_ context.Context, _ string, _ HotelSearchOptions) ([]models.HotelResult, error) {
		defer close(slowProviderReturned)
		<-releaseSlowProvider // deliberately ignores cancellation
		return []models.HotelResult{{Name: "Late Hotel", Price: 90, Currency: "EUR"}}, nil
	}
	hotelAuxProviderTimeout = 40 * time.Millisecond

	type searchResponse struct {
		result *models.HotelSearchResult
		err    error
	}
	responseCh := make(chan searchResponse, 1)
	go func() {
		result, err := SearchHotels(context.Background(), "Ischia timeout regression", HotelSearchOptions{
			CheckIn:   "2026-09-03",
			CheckOut:  "2026-09-08",
			Currency:  "EUR",
			MaxPages:  1,
			CenterLat: 40.73,
			CenterLon: 13.96,
		})
		responseCh <- searchResponse{result: result, err: err}
	}()

	var response searchResponse
	select {
	case response = <-responseCh:
		// The bounded collector returned while the deliberately non-cooperative
		// provider was still blocked.
	case <-time.After(5 * time.Second):
		close(releaseSlowProvider)
		<-slowProviderReturned
		t.Fatal("SearchHotels still waited for an auxiliary provider after its budget")
	}
	close(releaseSlowProvider)
	<-slowProviderReturned
	result, err := response.result, response.err

	if err != nil {
		t.Fatalf("SearchHotels returned an error despite a completed primary result: %v", err)
	}
	if len(result.Hotels) != 1 || result.Hotels[0].Name != "Fast Ischia Hotel" {
		t.Fatalf("hotels = %+v, want only the completed primary result", result.Hotels)
	}
	var bookingStatus *models.ProviderStatus
	for i := range result.ProviderStatuses {
		if result.ProviderStatuses[i].ID == "booking" {
			bookingStatus = &result.ProviderStatuses[i]
			break
		}
	}
	if bookingStatus == nil || bookingStatus.Status != models.StatusTimeout {
		t.Fatalf("booking status = %+v, want timeout", bookingStatus)
	}
	if result.Completeness.State != models.CompletenessPartial {
		t.Fatalf("completeness = %+v, want partial", result.Completeness)
	}
}
