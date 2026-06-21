package tripcoalesce

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func baseParams() Params {
	return Params{
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Travelers:   2,
		Currency:    "EUR",
	}
}

// newTestCoalescer returns a Coalescer wired to the supplied fake seams with a
// short timeout so timeout paths are fast.
func newTestCoalescer(f FlightSearchFunc, h HotelSearchFunc, g GroundSearchFunc) *Coalescer {
	return &Coalescer{
		FlightSearch:     f,
		HotelSearch:      h,
		GroundSearch:     g,
		Concurrency:      defaultDomainConcurrency,
		PerDomainTimeout: 500 * time.Millisecond,
		now:              time.Now,
	}
}

func okFlights() FlightSearchFunc {
	return func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{
				{Price: 350, Currency: "EUR"},
				{Price: 220, Currency: "EUR"},
			},
		}, nil
	}
}

func okHotels() HotelSearchFunc {
	return func(_ context.Context, _ string, _ hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{
			Success: true,
			Hotels: []models.HotelResult{
				{Name: "Grand", Price: 180, Currency: "EUR"},
				{Name: "Budget Inn", Price: 95, Currency: "EUR"},
			},
		}, nil
	}
}

func okGround() GroundSearchFunc {
	return func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{
			Success: true,
			Routes: []models.GroundRoute{
				{Provider: "flixbus", Price: 40, Currency: "EUR"},
				{Provider: "rail", Price: 65, Currency: "EUR"},
			},
		}, nil
	}
}

// TestPlanConcurrentFanOut proves the three domain searches run concurrently
// rather than sequentially. Each fake blocks on a barrier until all three have
// started; if the fan-out were sequential the barrier would deadlock and the
// per-call timeout would fire, leaving zero successful domains.
func TestPlanConcurrentFanOut(t *testing.T) {
	const domains = 3
	var started sync.WaitGroup
	started.Add(domains)
	release := make(chan struct{})
	var maxConcurrent int64
	var current int64

	enter := func() {
		// Increment current BEFORE signalling the barrier so the release
		// goroutine can only close the channel once all three domains are
		// provably counted in current. Under -race the scheduler overhead is
		// large enough that calling started.Done() first lets the barrier fire
		// and other goroutines drain through <-release before this one even
		// reaches atomic.AddInt64, making maxConcurrent never reach 3.
		n := atomic.AddInt64(&current, 1)
		for {
			old := atomic.LoadInt64(&maxConcurrent)
			if n <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, n) {
				break
			}
		}
		started.Done()
		<-release
		atomic.AddInt64(&current, -1)
	}

	c := newTestCoalescer(
		func(_ context.Context, _, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
			enter()
			return &models.FlightSearchResult{Success: true, Flights: []models.FlightResult{{Price: 200, Currency: "EUR"}}}, nil
		},
		func(_ context.Context, _ string, _ hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
			enter()
			return &models.HotelSearchResult{Success: true, Hotels: []models.HotelResult{{Price: 100, Currency: "EUR"}}}, nil
		},
		func(_ context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
			enter()
			return &models.GroundSearchResult{Success: true, Routes: []models.GroundRoute{{Price: 30, Currency: "EUR"}}}, nil
		},
	)
	c.PerDomainTimeout = 5 * time.Second

	// Release the barrier once all three goroutines have entered concurrently.
	go func() {
		started.Wait()
		close(release)
	}()

	plan := c.Plan(context.Background(), baseParams())

	if got := atomic.LoadInt64(&maxConcurrent); got != domains {
		t.Fatalf("max concurrent domains = %d, want %d (searches did not fan out concurrently)", got, domains)
	}
	okCount := 0
	for _, s := range plan.Statuses {
		if s.OK {
			okCount++
		}
	}
	if okCount != domains {
		t.Fatalf("ok domains = %d, want %d", okCount, domains)
	}
}

// TestPlanCombinedAssembly proves all three domains are assembled into one plan
// with the cheapest pick per domain and a summed floor estimate.
func TestPlanCombinedAssembly(t *testing.T) {
	c := newTestCoalescer(okFlights(), okHotels(), okGround())
	plan := c.Plan(context.Background(), baseParams())

	if plan.Flights == nil || plan.Hotels == nil || plan.Ground == nil {
		t.Fatalf("expected all three raw results attached: flights=%v hotels=%v ground=%v",
			plan.Flights != nil, plan.Hotels != nil, plan.Ground != nil)
	}
	if plan.CheapestFlight == nil || plan.CheapestFlight.Price != 220 {
		t.Fatalf("cheapest flight = %+v, want price 220", plan.CheapestFlight)
	}
	if plan.CheapestHotel == nil || plan.CheapestHotel.Price != 95 {
		t.Fatalf("cheapest hotel = %+v, want price 95", plan.CheapestHotel)
	}
	if plan.CheapestGround == nil || plan.CheapestGround.Price != 40 {
		t.Fatalf("cheapest ground = %+v, want price 40", plan.CheapestGround)
	}
	want := 220.0 + 95.0 + 40.0
	if plan.TotalCostEstimate != want {
		t.Fatalf("total estimate = %.2f, want %.2f", plan.TotalCostEstimate, want)
	}
	if len(plan.CostBreakdown) != 3 {
		t.Fatalf("cost breakdown len = %d, want 3", len(plan.CostBreakdown))
	}
}

// TestPlanPartialResultOnDomainFailure proves one domain failing yields a
// partial plan with the other two intact — failure isolation, never an abort.
func TestPlanPartialResultOnDomainFailure(t *testing.T) {
	failingHotels := func(_ context.Context, _ string, _ hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return nil, errors.New("hotels upstream 503")
	}
	c := newTestCoalescer(okFlights(), failingHotels, okGround())
	plan := c.Plan(context.Background(), baseParams())

	if plan.CheapestFlight == nil {
		t.Fatal("flights should survive a hotel failure")
	}
	if plan.CheapestGround == nil {
		t.Fatal("ground should survive a hotel failure")
	}
	if plan.CheapestHotel != nil {
		t.Fatalf("hotels failed; cheapest hotel should be nil, got %+v", plan.CheapestHotel)
	}

	var hotelStatus *DomainStatus
	for i := range plan.Statuses {
		if plan.Statuses[i].Domain == "hotels" {
			hotelStatus = &plan.Statuses[i]
		}
	}
	if hotelStatus == nil || hotelStatus.OK {
		t.Fatalf("hotel status should be present and not OK, got %+v", hotelStatus)
	}
	if hotelStatus.Error == "" {
		t.Fatal("hotel status should carry the failure reason")
	}
	// Total still sums the two surviving domains (220 + 40).
	if plan.TotalCostEstimate != 260 {
		t.Fatalf("total estimate = %.2f, want 260 (flights+ground only)", plan.TotalCostEstimate)
	}
}

// TestPlanTimeoutIsolation proves a domain that exceeds its per-domain timeout
// is reported as a typed failure without aborting the fast domains.
func TestPlanTimeoutIsolation(t *testing.T) {
	slowGround := func(ctx context.Context, _, _, _ string, _ ground.SearchOptions) (*models.GroundSearchResult, error) {
		select {
		case <-time.After(2 * time.Second):
			return okGround()(ctx, "", "", "", ground.SearchOptions{})
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c := newTestCoalescer(okFlights(), okHotels(), slowGround)
	c.PerDomainTimeout = 100 * time.Millisecond

	plan := c.Plan(context.Background(), baseParams())
	if plan.CheapestFlight == nil || plan.CheapestHotel == nil {
		t.Fatal("fast domains should complete despite a slow ground domain")
	}
	if plan.CheapestGround != nil {
		t.Fatalf("slow ground should have timed out, got %+v", plan.CheapestGround)
	}
	var groundOK bool
	for _, s := range plan.Statuses {
		if s.Domain == "ground" {
			groundOK = s.OK
		}
	}
	if groundOK {
		t.Fatal("ground domain should be reported not OK after timeout")
	}
}

// TestPlanMixedCurrencyNotSummed proves a component in a different currency is
// itemised but not folded into the headline same-currency floor.
func TestPlanMixedCurrencyNotSummed(t *testing.T) {
	usdHotels := func(_ context.Context, _ string, _ hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		return &models.HotelSearchResult{Success: true, Hotels: []models.HotelResult{{Price: 100, Currency: "USD"}}}, nil
	}
	c := newTestCoalescer(okFlights(), usdHotels, okGround())
	plan := c.Plan(context.Background(), baseParams())

	// EUR flight (220) + EUR ground (40) summed; USD hotel itemised only.
	if plan.TotalCostEstimate != 260 {
		t.Fatalf("total estimate = %.2f, want 260 (USD hotel excluded from EUR floor)", plan.TotalCostEstimate)
	}
	if len(plan.CostBreakdown) != 3 {
		t.Fatalf("cost breakdown len = %d, want 3 (USD hotel still itemised)", len(plan.CostBreakdown))
	}
}

// TestPackagePlanUsesRealSeams smoke-checks the package-level Plan wiring builds
// a Coalescer with non-nil seams (does not invoke network).
func TestNewWiresRealSeams(t *testing.T) {
	c := New()
	if c.FlightSearch == nil || c.HotelSearch == nil || c.GroundSearch == nil {
		t.Fatal("New() must wire all three real search seams")
	}
	if c.Concurrency != defaultDomainConcurrency {
		t.Fatalf("concurrency = %d, want %d", c.Concurrency, defaultDomainConcurrency)
	}
}
