package hotels

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestHotelSingleflight verifies that concurrent calls with the same key
// are coalesced and the underlying search executes only once.
func TestHotelSingleflight(t *testing.T) {
	var callCount atomic.Int64

	const n = 10
	key := "hotel|Paris|2026-06-15|2026-06-18|2"

	var wg sync.WaitGroup
	results := make([]any, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err, _ := hotelGroup.Do(key, func() (any, error) {
				callCount.Add(1)
				return "result", nil
			})
			results[idx] = v
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	count := callCount.Load()
	if count == 0 {
		t.Fatal("expected inner function to be called at least once, got 0")
	}
	if count > int64(n) {
		t.Fatalf("expected inner function called ≤%d times, got %d", n, count)
	}
	t.Logf("inner function called %d times for %d concurrent goroutines", count, n)

	for i, r := range results {
		if r != "result" {
			t.Errorf("goroutine %d got result %v, want %q", i, r, "result")
		}
		if errs[i] != nil {
			t.Errorf("goroutine %d got error %v, want nil", i, errs[i])
		}
	}
}

// TestHotelSearchKey verifies that different parameter combinations produce
// distinct keys, preventing incorrect deduplication.
func TestHotelSearchKey(t *testing.T) {
	base := HotelSearchOptions{
		CheckIn:   "2026-06-15",
		CheckOut:  "2026-06-18",
		Guests:    2,
		Currency:  "USD",
		Amenities: []string{"pool", "wifi"},
	}
	changedCheckIn := base
	changedCheckIn.CheckIn = "2026-06-16"
	changedGuests := base
	changedGuests.Guests = 3
	changedCurrency := base
	changedCurrency.Currency = "EUR"
	changedStars := base
	changedStars.Stars = 5
	changedMaxPages := base
	changedMaxPages.MaxPages = 1
	changedFilter := base
	changedFilter.MinPrice = 100

	k1 := hotelSearchKey("Paris", base)
	k2 := hotelSearchKey("Paris", changedCheckIn)
	k3 := hotelSearchKey("London", base)
	k4 := hotelSearchKey("Paris", changedGuests)
	k5 := hotelSearchKey("Paris", changedCurrency)
	k6 := hotelSearchKey("Paris", changedStars)
	k7 := hotelSearchKey("Paris", changedMaxPages)
	k8 := hotelSearchKey("Paris", changedFilter)

	keys := []string{k1, k2, k3, k4, k5, k6, k7, k8}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] == keys[j] {
				t.Errorf("key collision: keys[%d] == keys[%d]: %q", i, j, keys[i])
			}
		}
	}

	// Same inputs must produce the same key.
	k1again := hotelSearchKey("Paris", base)
	if k1 != k1again {
		t.Errorf("same inputs produced different keys: %q vs %q", k1, k1again)
	}

	reorderedAmenities := base
	reorderedAmenities.Amenities = []string{"wifi", "pool"}
	k1reorderedAmenities := hotelSearchKey("Paris", reorderedAmenities)
	if k1 != k1reorderedAmenities {
		t.Errorf("same amenity set produced different keys: %q vs %q", k1, k1reorderedAmenities)
	}
}

func TestHotelSearchKey_FilterRegressionCoverage(t *testing.T) {
	base := HotelSearchOptions{
		CheckIn:          "2026-06-15",
		CheckOut:         "2026-06-18",
		Guests:           2,
		Currency:         "EUR",
		Amenities:        []string{"wifi", "pool"},
		FreeCancellation: true,
		PropertyType:     "hotel",
		Brand:            "Hilton",
		IncludeSoldOut:   true,
	}

	sameAmenitiesDifferentOrder := base
	sameAmenitiesDifferentOrder.Amenities = []string{"pool", "wifi"}

	if got, want := hotelSearchKey("Paris", base), hotelSearchKey("Paris", sameAmenitiesDifferentOrder); got != want {
		t.Fatalf("amenity ordering should not change key:\n%s\n%s", got, want)
	}

	cases := []struct {
		name string
		mut  func(HotelSearchOptions) HotelSearchOptions
	}{
		{
			name: "free cancellation",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.FreeCancellation = false
				return opts
			},
		},
		{
			name: "property type",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.PropertyType = "apartment"
				return opts
			},
		},
		{
			name: "include sold out",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.IncludeSoldOut = false
				return opts
			},
		},
		{
			name: "amenity membership",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.Amenities = []string{"wifi", "spa"}
				return opts
			},
		},
	}

	baseKey := hotelSearchKey("Paris", base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hotelSearchKey("Paris", tc.mut(base)); got == baseKey {
				t.Fatalf("%s should change key, but both were %q", tc.name, got)
			}
		})
	}

	brandVariant := base
	brandVariant.Brand = " hilton "
	if got, want := hotelSearchKey("Paris", brandVariant), baseKey; got != want {
		t.Fatalf("brand normalization should keep key stable:\n%s\n%s", got, want)
	}
}

func TestHotelSearchKey_NormalizesEffectiveSearchInputs(t *testing.T) {
	base := HotelSearchOptions{
		CheckIn:   "2026-06-15",
		CheckOut:  "2026-06-18",
		Guests:    2,
		Currency:  "EUR",
		MaxPages:  0,
		Amenities: []string{" wifi ", "POOL"},
	}

	cases := []struct {
		name string
		mut  func(HotelSearchOptions) HotelSearchOptions
	}{
		{
			name: "default max pages and normalized amenities",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.MaxPages = maxPages
				opts.Amenities = []string{"pool", "wifi"}
				return opts
			},
		},
		{
			name: "max pages above limit still uses effective page limit",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.MaxPages = maxPages + 4
				opts.Amenities = []string{"Pool", "WiFi"}
				return opts
			},
		},
		{
			name: "negative max pages falls back to default page limit",
			mut: func(opts HotelSearchOptions) HotelSearchOptions {
				opts.MaxPages = -1
				opts.Amenities = []string{"wifi", "pool"}
				return opts
			},
		},
	}

	baseKey := hotelSearchKey("Paris", base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hotelSearchKey("Paris", tc.mut(base)); got != baseKey {
				t.Fatalf("%s should keep the key stable:\n%s\n%s", tc.name, got, baseKey)
			}
		})
	}

	changed := base
	changed.MaxPages = 1
	if got := hotelSearchKey("Paris", changed); got == baseKey {
		t.Fatalf("changing the effective page limit should change the key, but both were %q", got)
	}
}

// TestSearchHotelsWithClient_MissingDates verifies that concurrent calls with
// missing dates all return errors without panicking.
func TestSearchHotelsWithClient_MissingDates(t *testing.T) {
	const n = 5

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := SearchHotels(t.Context(), "Paris", HotelSearchOptions{})
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Errorf("goroutine %d: expected error for missing dates, got nil", i)
		}
	}
}

func TestHotelSingleflight_ConcurrentCallersGetPrivateResults(t *testing.T) {
	shared := &models.HotelSearchResult{
		Success: true,
		Count:   1,
		Hotels: []models.HotelResult{
			{
				Name:        "Hotel Example",
				Price:       120,
				Amenities:   []string{"wifi", "spa"},
				RoomTypes:   []models.Room{{Name: "Deluxe", Amenities: []string{"balcony", "breakfast"}}},
				Sources:     []models.PriceSource{{Provider: "google_hotels", Price: 120, Currency: "EUR"}},
				ReviewCount: 42,
			},
		},
		ProviderStatuses: []models.ProviderStatus{{ID: "google_hotels", Name: "Google Hotels", Status: "ok", Results: 1}},
	}

	const n = 2
	key := "hotel|Paris|2026-06-15|2026-06-18|2"

	var callCount atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*models.HotelSearchResult, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			v, err, _ := hotelGroup.Do(key, func() (any, error) {
				callCount.Add(1)
				time.Sleep(50 * time.Millisecond)
				return shared, nil
			})
			if err == nil {
				results[idx] = cloneHotelSearchResult(v.(*models.HotelSearchResult))
			}
			errs[idx] = err
		}(i)
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d singleflight error: %v", i, err)
		}
	}
	if got := callCount.Load(); got != 1 {
		t.Fatalf("callCount = %d, want 1 shared singleflight execution", got)
	}
	if results[0] == results[1] {
		t.Fatal("concurrent callers received the same HotelSearchResult pointer")
	}
	if &results[0].Hotels[0] == &results[1].Hotels[0] {
		t.Fatal("concurrent callers reused the same HotelResult backing storage")
	}
	if &results[0].Hotels[0].Amenities[0] == &results[1].Hotels[0].Amenities[0] {
		t.Fatal("concurrent callers reused the same hotel Amenities backing storage")
	}
	if &results[0].Hotels[0].RoomTypes[0] == &results[1].Hotels[0].RoomTypes[0] {
		t.Fatal("concurrent callers reused the same Room backing storage")
	}
	if &results[0].Hotels[0].RoomTypes[0].Amenities[0] == &results[1].Hotels[0].RoomTypes[0].Amenities[0] {
		t.Fatal("concurrent callers reused the same room Amenities backing storage")
	}
	if &results[0].Hotels[0].Sources[0] == &results[1].Hotels[0].Sources[0] {
		t.Fatal("concurrent callers reused the same Sources backing storage")
	}
	if &results[0].ProviderStatuses[0] == &results[1].ProviderStatuses[0] {
		t.Fatal("concurrent callers reused the same ProviderStatuses backing storage")
	}

	otherCount := results[1].Count
	otherPrice := results[1].Hotels[0].Price
	otherHotelsLen := len(results[1].Hotels)
	otherAmenity := results[1].Hotels[0].Amenities[0]
	otherRoomAmenity := results[1].Hotels[0].RoomTypes[0].Amenities[0]
	otherProvider := results[1].Hotels[0].Sources[0].Provider
	otherStatus := results[1].ProviderStatuses[0].Status

	results[0].Count = 0
	results[0].Hotels[0].Price = otherPrice + 10
	results[0].Hotels[0].Amenities[0] = "changed"
	results[0].Hotels[0].RoomTypes[0].Amenities[0] = "suite"
	results[0].Hotels[0].Sources[0].Provider = "booking"
	results[0].ProviderStatuses[0].Status = "error"
	results[0].Hotels = results[0].Hotels[:0]

	if results[1].Count != otherCount {
		t.Fatalf("caller 1 Count changed to %d, want %d", results[1].Count, otherCount)
	}
	if len(results[1].Hotels) != otherHotelsLen {
		t.Fatalf("caller 1 len(Hotels) changed to %d, want %d", len(results[1].Hotels), otherHotelsLen)
	}
	if results[1].Hotels[0].Price != otherPrice {
		t.Fatalf("caller 1 hotel price changed to %v, want %v", results[1].Hotels[0].Price, otherPrice)
	}
	if got := results[1].Hotels[0].Amenities[0]; got != otherAmenity {
		t.Fatalf("caller 1 amenity changed to %q, want %q", got, otherAmenity)
	}
	if got := results[1].Hotels[0].RoomTypes[0].Amenities[0]; got != otherRoomAmenity {
		t.Fatalf("caller 1 room amenity changed to %q, want %q", got, otherRoomAmenity)
	}
	if got := results[1].Hotels[0].Sources[0].Provider; got != otherProvider {
		t.Fatalf("caller 1 source provider changed to %q, want %q", got, otherProvider)
	}
	if got := results[1].ProviderStatuses[0].Status; got != otherStatus {
		t.Fatalf("caller 1 provider status changed to %q, want %q", got, otherStatus)
	}
}

func TestCloneHotelSearchResult_ReturnsCallerPrivateCopy(t *testing.T) {
	shared := &models.HotelSearchResult{
		Success: true,
		Count:   1,
		Hotels: []models.HotelResult{
			{
				Name:      "Hotel Example",
				Price:     120,
				Amenities: []string{"wifi", "spa"},
				RoomTypes: []models.Room{
					{Name: "Deluxe", Amenities: []string{"balcony", "breakfast"}},
				},
				Sources: []models.PriceSource{
					{Provider: "google_hotels", Price: 120, Currency: "EUR"},
				},
			},
		},
		ProviderStatuses: []models.ProviderStatus{{ID: "google_hotels", Name: "Google Hotels", Status: "ok", Results: 1}},
	}

	clone := cloneHotelSearchResult(shared)
	if clone == shared {
		t.Fatal("cloneHotelSearchResult returned the original pointer")
	}
	if &clone.Hotels[0] == &shared.Hotels[0] {
		t.Fatal("cloneHotelSearchResult reused the shared Hotels backing array")
	}
	if &clone.Hotels[0].Amenities[0] == &shared.Hotels[0].Amenities[0] {
		t.Fatal("cloneHotelSearchResult reused the shared hotel Amenities backing array")
	}
	if &clone.Hotels[0].RoomTypes[0] == &shared.Hotels[0].RoomTypes[0] {
		t.Fatal("cloneHotelSearchResult reused the shared RoomTypes backing array")
	}
	if &clone.Hotels[0].RoomTypes[0].Amenities[0] == &shared.Hotels[0].RoomTypes[0].Amenities[0] {
		t.Fatal("cloneHotelSearchResult reused the shared room Amenities backing array")
	}
	if &clone.Hotels[0].Sources[0] == &shared.Hotels[0].Sources[0] {
		t.Fatal("cloneHotelSearchResult reused the shared Sources backing array")
	}
	if &clone.ProviderStatuses[0] == &shared.ProviderStatuses[0] {
		t.Fatal("cloneHotelSearchResult reused the shared ProviderStatuses backing array")
	}

	clone.Count = 0
	clone.Hotels[0].Price = 99
	clone.Hotels[0].Amenities[0] = "changed"
	clone.Hotels[0].RoomTypes[0].Amenities[0] = "suite"
	clone.Hotels[0].Sources[0].Provider = "booking"
	clone.ProviderStatuses[0].Status = "error"
	clone.Hotels = clone.Hotels[:0]

	if shared.Count != 1 {
		t.Fatalf("shared.Count = %d, want 1", shared.Count)
	}
	if len(shared.Hotels) != 1 {
		t.Fatalf("len(shared.Hotels) = %d, want 1", len(shared.Hotels))
	}
	if got := shared.Hotels[0].Price; got != 120 {
		t.Fatalf("shared hotel price = %v, want 120", got)
	}
	if got := shared.Hotels[0].Amenities[0]; got != "wifi" {
		t.Fatalf("shared amenity = %q, want %q", got, "wifi")
	}
	if got := shared.Hotels[0].RoomTypes[0].Amenities[0]; got != "balcony" {
		t.Fatalf("shared room amenity = %q, want %q", got, "balcony")
	}
	if got := shared.Hotels[0].Sources[0].Provider; got != "google_hotels" {
		t.Fatalf("shared source provider = %q, want %q", got, "google_hotels")
	}
	if got := shared.ProviderStatuses[0].Status; got != "ok" {
		t.Fatalf("shared provider status = %q, want %q", got, "ok")
	}
}

func TestDoHotelSearchSingleflight_ShortCallerDeadlineDoesNotPoisonSharedWork(t *testing.T) {
	key := "hotel|Paris|2026-09-01|2026-09-03|shared-timeout"

	var callCount atomic.Int64
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer firstCancel()
	firstDeadline, ok := firstCtx.Deadline()
	if !ok {
		t.Fatal("first caller context unexpectedly has no deadline")
	}

	sharedDeadlineCh := make(chan time.Time, 1)
	_, firstErr := doHotelSearchSingleflight(firstCtx, key, func(sharedCtx context.Context) (*models.HotelSearchResult, error) {
		callCount.Add(1)
		sharedDeadline, ok := sharedCtx.Deadline()
		if !ok {
			t.Fatal("shared context unexpectedly has no deadline")
		}
		sharedDeadlineCh <- sharedDeadline
		<-sharedCtx.Done()
		return nil, sharedCtx.Err()
	})

	if !errors.Is(firstErr, context.DeadlineExceeded) {
		t.Fatalf("firstErr = %v, want context deadline exceeded", firstErr)
	}
	sharedDeadline := <-sharedDeadlineCh
	if !sharedDeadline.After(firstDeadline.Add(100 * time.Millisecond)) {
		t.Fatalf("shared deadline %v unexpectedly inherited short caller deadline %v", sharedDeadline, firstDeadline)
	}
	secondCtx, secondCancel := context.WithTimeout(context.Background(), time.Second)
	defer secondCancel()
	secondResult, secondErr := doHotelSearchSingleflight(secondCtx, key, func(sharedCtx context.Context) (*models.HotelSearchResult, error) {
		callCount.Add(1)
		return &models.HotelSearchResult{Success: true, Count: 1}, nil
	})
	if secondErr != nil {
		t.Fatalf("secondErr = %v, want nil", secondErr)
	}
	if secondResult == nil || !secondResult.Success || secondResult.Count != 1 {
		t.Fatalf("secondResult = %#v, want successful result from a fresh execution", secondResult)
	}
	if got := callCount.Load(); got != 2 {
		t.Fatalf("callCount = %d, want 2 executions after the timed-out winner stops", got)
	}
}

func TestSharedHotelResult_ClonesPartialErrorResult(t *testing.T) {
	shared := &models.HotelSearchResult{
		Success: false,
		Count:   1,
		Hotels: []models.HotelResult{
			{
				Name:      "Hotel Example",
				Price:     120,
				Amenities: []string{"wifi"},
				RoomTypes: []models.Room{
					{Name: "Deluxe", Amenities: []string{"balcony"}},
				},
				Sources: []models.PriceSource{
					{Provider: "google_hotels", Price: 120, Currency: "EUR"},
				},
			},
		},
		ProviderStatuses: []models.ProviderStatus{{ID: "google_hotels", Name: "Google Hotels", Status: "partial", Results: 1}},
	}

	wantErr := errors.New("provider partial failure")
	got, err := sharedHotelResult(shared, wantErr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got == shared {
		t.Fatal("sharedHotelResult returned the original pointer")
	}
	if &got.Hotels[0] == &shared.Hotels[0] {
		t.Fatal("sharedHotelResult reused the shared Hotels backing array")
	}
	if &got.Hotels[0].Amenities[0] == &shared.Hotels[0].Amenities[0] {
		t.Fatal("sharedHotelResult reused the shared hotel Amenities backing array")
	}
	if &got.Hotels[0].RoomTypes[0] == &shared.Hotels[0].RoomTypes[0] {
		t.Fatal("sharedHotelResult reused the shared RoomTypes backing array")
	}
	if &got.Hotels[0].RoomTypes[0].Amenities[0] == &shared.Hotels[0].RoomTypes[0].Amenities[0] {
		t.Fatal("sharedHotelResult reused the shared room Amenities backing array")
	}
	if &got.Hotels[0].Sources[0] == &shared.Hotels[0].Sources[0] {
		t.Fatal("sharedHotelResult reused the shared Sources backing array")
	}
	if &got.ProviderStatuses[0] == &shared.ProviderStatuses[0] {
		t.Fatal("sharedHotelResult reused the shared ProviderStatuses backing array")
	}

	got.Hotels[0].Amenities[0] = "changed"
	got.Hotels[0].RoomTypes[0].Amenities[0] = "suite"
	got.Hotels[0].Sources[0].Provider = "booking"
	got.ProviderStatuses[0].Status = "error"

	if shared.Hotels[0].Amenities[0] != "wifi" {
		t.Fatalf("shared amenity = %q, want %q", shared.Hotels[0].Amenities[0], "wifi")
	}
	if shared.Hotels[0].RoomTypes[0].Amenities[0] != "balcony" {
		t.Fatalf("shared room amenity = %q, want %q", shared.Hotels[0].RoomTypes[0].Amenities[0], "balcony")
	}
	if shared.Hotels[0].Sources[0].Provider != "google_hotels" {
		t.Fatalf("shared source provider = %q, want %q", shared.Hotels[0].Sources[0].Provider, "google_hotels")
	}
	if shared.ProviderStatuses[0].Status != "partial" {
		t.Fatalf("shared provider status = %q, want %q", shared.ProviderStatuses[0].Status, "partial")
	}
}
