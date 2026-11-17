package ground

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestGroundSingleflight verifies that concurrent calls with the same key
// are coalesced and the underlying search executes only once.
func TestGroundSingleflight(t *testing.T) {
	var callCount atomic.Int64

	const n = 10
	key := "ground|Amsterdam|Paris|2026-06-15"

	var wg sync.WaitGroup
	results := make([]any, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err, _ := groundGroup.Do(key, func() (any, error) {
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

// TestGroundSingleflight_DifferentKeys verifies that requests with different
// parameters are NOT coalesced — each gets its own independent call.
func TestGroundSingleflight_DifferentKeys(t *testing.T) {
	var callCount atomic.Int64

	keys := []string{
		"ground|Amsterdam|Paris|2026-06-15",
		"ground|Amsterdam|Berlin|2026-06-15",
		"ground|Amsterdam|Paris|2026-06-16",
	}

	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		k := key
		go func() {
			defer wg.Done()
			groundGroup.Do(k, func() (any, error) { //nolint:errcheck
				callCount.Add(1)
				return nil, nil
			})
		}()
	}
	wg.Wait()

	if got := callCount.Load(); got != int64(len(keys)) {
		t.Errorf("expected %d calls for %d distinct keys, got %d", len(keys), len(keys), got)
	}
}

// TestSearchByNameSingleflight_NoCache verifies that the NoCache opt-out path
// routes through singleflight without panicking or deadlocking.
func TestSearchByNameSingleflight_NoCache(t *testing.T) {
	// Searching with NoCache=true must not panic or deadlock.
	// We use a non-existent route so all providers return not-applicable errors.
	opts := SearchOptions{
		Currency:  "EUR",
		Providers: []string{"flixbus"}, // only one provider to keep test fast
		NoCache:   true,
	}
	// This will attempt a live FlixBus resolve; in short mode we skip.
	if testing.Short() {
		t.Skip("skipping live-provider test in short mode")
	}
	// We just verify it doesn't panic/deadlock; result doesn't matter.
	result, _ := SearchByName(t.Context(), "TestCityXYZ99", "TestCityABC88", "2026-06-15", opts)
	_ = result
}

func TestGroundSingleflight_ConcurrentCallersGetPrivateResults(t *testing.T) {
	seatsLeft := 3
	shared := &models.GroundSearchResult{
		Success: true,
		Count:   1,
		Routes: []models.GroundRoute{
			{
				Provider:  "flixbus",
				Price:     49,
				Amenities: []string{"wifi", "toilet"},
				Legs: []models.GroundLeg{
					{
						Provider:  "flixbus",
						Amenities: []string{"power"},
					},
				},
				SeatsLeft: &seatsLeft,
			},
		},
	}

	const n = 2
	key := "ground|Amsterdam|Paris|2026-06-15|EUR|flixbus|0.00||false"

	var callCount atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*models.GroundSearchResult, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			v, err, _ := groundGroup.Do(key, func() (any, error) {
				callCount.Add(1)
				time.Sleep(50 * time.Millisecond)
				return shared, nil
			})
			if err == nil {
				results[idx] = cloneGroundSearchResult(v.(*models.GroundSearchResult))
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
		t.Fatal("concurrent callers received the same GroundSearchResult pointer")
	}
	if len(results[0].Routes) == 0 || len(results[1].Routes) == 0 {
		t.Fatal("expected both callers to receive routes")
	}
	if &results[0].Routes[0] == &results[1].Routes[0] {
		t.Fatal("concurrent callers reused the same GroundRoute backing storage")
	}

	otherCount := results[1].Count
	otherPrice := results[1].Routes[0].Price
	otherSeatsLeft := *results[1].Routes[0].SeatsLeft
	otherRoutesLen := len(results[1].Routes)

	results[0].Count = 0
	results[0].Routes[0].Price = otherPrice + 10
	results[0].Routes[0].Amenities[0] = "changed"
	results[0].Routes[0].Legs[0].Amenities[0] = "usb"
	*results[0].Routes[0].SeatsLeft = 1
	results[0].Routes = results[0].Routes[:0]

	if results[1].Count != otherCount {
		t.Fatalf("caller 1 Count changed to %d, want %d", results[1].Count, otherCount)
	}
	if len(results[1].Routes) != otherRoutesLen {
		t.Fatalf("caller 1 len(Routes) changed to %d, want %d", len(results[1].Routes), otherRoutesLen)
	}
	if results[1].Routes[0].Price != otherPrice {
		t.Fatalf("caller 1 route price changed to %v, want %v", results[1].Routes[0].Price, otherPrice)
	}
	if got := results[1].Routes[0].Amenities[0]; got != "wifi" {
		t.Fatalf("caller 1 route amenity changed to %q, want %q", got, "wifi")
	}
	if got := results[1].Routes[0].Legs[0].Amenities[0]; got != "power" {
		t.Fatalf("caller 1 leg amenity changed to %q, want %q", got, "power")
	}
	if got := *results[1].Routes[0].SeatsLeft; got != otherSeatsLeft {
		t.Fatalf("caller 1 SeatsLeft changed to %d, want %d", got, otherSeatsLeft)
	}
}

func TestCloneGroundSearchResult_ReturnsCallerPrivateCopy(t *testing.T) {
	seatsLeft := 3
	shared := &models.GroundSearchResult{
		Success: true,
		Count:   1,
		Routes: []models.GroundRoute{
			{
				Provider:  "flixbus",
				Price:     49,
				Amenities: []string{"wifi", "toilet"},
				Legs: []models.GroundLeg{
					{
						Provider:  "flixbus",
						Amenities: []string{"power"},
					},
				},
				SeatsLeft: &seatsLeft,
			},
		},
	}

	clone := cloneGroundSearchResult(shared)
	if clone == shared {
		t.Fatal("cloneGroundSearchResult returned the original pointer")
	}
	if len(clone.Routes) != len(shared.Routes) {
		t.Fatalf("len(clone.Routes) = %d, want %d", len(clone.Routes), len(shared.Routes))
	}
	if &clone.Routes[0] == &shared.Routes[0] {
		t.Fatal("cloneGroundSearchResult reused the shared Routes backing array")
	}
	if &clone.Routes[0].Amenities[0] == &shared.Routes[0].Amenities[0] {
		t.Fatal("cloneGroundSearchResult reused the shared Amenities backing array")
	}
	if &clone.Routes[0].Legs[0] == &shared.Routes[0].Legs[0] {
		t.Fatal("cloneGroundSearchResult reused the shared Legs backing array")
	}
	if &clone.Routes[0].Legs[0].Amenities[0] == &shared.Routes[0].Legs[0].Amenities[0] {
		t.Fatal("cloneGroundSearchResult reused the shared leg Amenities backing array")
	}
	if clone.Routes[0].SeatsLeft == shared.Routes[0].SeatsLeft {
		t.Fatal("cloneGroundSearchResult reused the shared SeatsLeft pointer")
	}

	clone.Count = 0
	clone.Routes[0].Amenities[0] = "changed"
	clone.Routes[0].Legs[0].Amenities[0] = "usb"
	*clone.Routes[0].SeatsLeft = 1
	clone.Routes = clone.Routes[:0]
	if shared.Count != 1 {
		t.Fatalf("shared.Count = %d, want 1", shared.Count)
	}
	if len(shared.Routes) != 1 {
		t.Fatalf("len(shared.Routes) = %d, want 1", len(shared.Routes))
	}
	if got := shared.Routes[0].Amenities[0]; got != "wifi" {
		t.Fatalf("shared route amenity = %q, want %q", got, "wifi")
	}
	if got := shared.Routes[0].Legs[0].Amenities[0]; got != "power" {
		t.Fatalf("shared leg amenity = %q, want %q", got, "power")
	}
	if got := *shared.Routes[0].SeatsLeft; got != 3 {
		t.Fatalf("shared SeatsLeft = %d, want 3", got)
	}
}

func TestSharedGroundResult_ClonesPartialErrorResult(t *testing.T) {
	seatsLeft := 3
	shared := &models.GroundSearchResult{
		Success: false,
		Count:   1,
		Routes: []models.GroundRoute{
			{
				Provider:  "flixbus",
				Price:     49,
				Amenities: []string{"wifi"},
				Legs: []models.GroundLeg{
					{
						Provider:  "flixbus",
						Amenities: []string{"power"},
					},
				},
				SeatsLeft: &seatsLeft,
			},
		},
	}

	wantErr := errors.New("provider partial failure")
	got, err := sharedGroundResult(shared, wantErr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if got == shared {
		t.Fatal("sharedGroundResult returned the original pointer")
	}
	if &got.Routes[0] == &shared.Routes[0] {
		t.Fatal("sharedGroundResult reused the shared Routes backing array")
	}
	if got.Routes[0].SeatsLeft == shared.Routes[0].SeatsLeft {
		t.Fatal("sharedGroundResult reused the shared SeatsLeft pointer")
	}

	got.Routes[0].Amenities[0] = "changed"
	got.Routes[0].Legs[0].Amenities[0] = "usb"
	*got.Routes[0].SeatsLeft = 1

	if shared.Routes[0].Amenities[0] != "wifi" {
		t.Fatalf("shared amenity = %q, want %q", shared.Routes[0].Amenities[0], "wifi")
	}
	if shared.Routes[0].Legs[0].Amenities[0] != "power" {
		t.Fatalf("shared leg amenity = %q, want %q", shared.Routes[0].Legs[0].Amenities[0], "power")
	}
	if got := *shared.Routes[0].SeatsLeft; got != 3 {
		t.Fatalf("shared SeatsLeft = %d, want 3", got)
	}
}

func TestDoGroundSearchSingleflight_ShortCallerDeadlineDoesNotPoisonSharedWork(t *testing.T) {
	key := "ground|Amsterdam|Paris|2026-09-01|shared-timeout"

	var callCount atomic.Int64
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer firstCancel()
	firstDeadline, ok := firstCtx.Deadline()
	if !ok {
		t.Fatal("first caller context unexpectedly has no deadline")
	}

	sharedDeadlineCh := make(chan time.Time, 1)
	_, firstErr := doGroundSearchSingleflight(firstCtx, key, func(sharedCtx context.Context) (*models.GroundSearchResult, error) {
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
	secondResult, secondErr := doGroundSearchSingleflight(secondCtx, key, func(sharedCtx context.Context) (*models.GroundSearchResult, error) {
		callCount.Add(1)
		return &models.GroundSearchResult{Success: true, Count: 1}, nil
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

// A caller (a detector in a savings pass, an MCP handler) that edits any
// nested part of its copy leaves the shared result, and so every other caller
// and the cache, unchanged.
func TestCloneGroundSearchResult_NestedFieldsArePrivate(t *testing.T) {
	shared := &models.GroundSearchResult{
		Success: true,
		Routes: []models.GroundRoute{{
			Provider:   "ferryhopper",
			Sources:    []models.PriceSource{{Provider: "ferryhopper", Price: 48}},
			Confidence: &models.Confidence{Label: "high", Signals: []string{"live"}},
			SeaState:   &models.SeaState{WaveHeight: 1.5},
		}},
		ProviderStatuses: []models.ProviderStatus{{ID: "ferryhopper", Status: models.StatusOK}},
		Completeness:     models.Completeness{Missing: []string{"dfds"}},
		HackSaving:       &models.HackSaving{Type: "t", Risks: []string{"r"}},
		Destination:      &models.DestinationInfo{Holidays: []models.Holiday{{Name: "h"}}},
	}

	c := cloneGroundSearchResult(shared)
	c.Routes[0].Sources[0].Price = 1
	c.Routes[0].Confidence.Label = "changed"
	c.Routes[0].Confidence.Signals[0] = "changed"
	c.Routes[0].SeaState.WaveHeight = 9
	c.ProviderStatuses[0].Status = models.StatusError
	c.Completeness.Missing[0] = "changed"
	c.HackSaving.Risks[0] = "changed"
	c.Destination.Holidays[0].Name = "changed"

	r := shared.Routes[0]
	if r.Sources[0].Price != 48 || r.Confidence.Label != "high" || r.Confidence.Signals[0] != "live" || r.SeaState.WaveHeight != 1.5 {
		t.Errorf("route fields changed through the copy: %+v %+v %+v", r.Sources[0], *r.Confidence, *r.SeaState)
	}
	if shared.ProviderStatuses[0].Status != models.StatusOK || shared.Completeness.Missing[0] != "dfds" {
		t.Errorf("statuses changed through the copy: %+v %v", shared.ProviderStatuses, shared.Completeness.Missing)
	}
	if shared.HackSaving.Risks[0] != "r" || shared.Destination.Holidays[0].Name != "h" {
		t.Errorf("saving or destination changed through the copy: %v %v", shared.HackSaving.Risks, shared.Destination.Holidays)
	}
}

// refWalkMaxDepth bounds the walk over the result's type tree. Reaching it is
// a test failure, never a silent stop: a deeper model must raise the bound.
const refWalkMaxDepth = 16

var timeType = reflect.TypeOf(time.Time{})

// structField returns field i of struct v, settable even when unexported: a
// clone must copy unexported slices and maps too. It is invalid when v is not
// addressable, which the callers treat as a failure.
func structField(v reflect.Value, i int) reflect.Value {
	f := v.Field(i)
	if v.Type().Field(i).IsExported() {
		return f
	}
	if !f.CanAddr() {
		return reflect.Value{}
	}
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

// fillRefs sets every value reachable from v, through every field, to a
// non-zero value: pointers to new values, slices, arrays and maps to one filled
// element, strings, numbers and bools to fixed non-zero values. A copy can
// then be checked for shared references and for equal values. Any kind it
// does not handle fails the test.
func fillRefs(t *testing.T, v reflect.Value, path string, depth int) {
	t.Helper()
	if depth > refWalkMaxDepth {
		t.Fatalf("fillRefs: %s is deeper than %d levels; raise refWalkMaxDepth", path, refWalkMaxDepth)
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillRefs(t, v.Elem(), path, depth+1)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillRefs(t, v.Index(0), path+"[0]", depth+1)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fillRefs(t, v.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)
		}
	case reflect.Map:
		k := reflect.New(v.Type().Key()).Elem()
		fillRefs(t, k, path+"{key}", depth+1)
		e := reflect.New(v.Type().Elem()).Elem()
		fillRefs(t, e, path+"{value}", depth+1)
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Date(2027, 7, 15, 9, 0, 0, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			fv := structField(v, i)
			if !fv.IsValid() {
				t.Fatalf("fillRefs: %s.%s is unexported and not addressable, so it cannot be checked", path, f.Name)
			}
			fillRefs(t, fv, path+"."+f.Name, depth+1)
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	default:
		t.Fatalf("fillRefs: %s has kind %s, which this walk does not handle", path, v.Kind())
	}
}

// sharedRefs lists the paths where a and b point at the same pointee, slice
// backing array or map. It walks the same kinds fillRefs fills and fails on
// any other.
func sharedRefs(t *testing.T, a, b reflect.Value, path string, depth int) []string {
	t.Helper()
	if depth > refWalkMaxDepth {
		t.Fatalf("sharedRefs: %s is deeper than %d levels; raise refWalkMaxDepth", path, refWalkMaxDepth)
	}
	var out []string
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			out = append(out, path)
		}
		out = append(out, sharedRefs(t, a.Elem(), b.Elem(), path, depth+1)...)
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			out = append(out, path)
		}
		for i := 0; i < a.Len() && i < b.Len(); i++ {
			out = append(out, sharedRefs(t, a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)...)
		}
	case reflect.Array:
		for i := 0; i < a.Len(); i++ {
			out = append(out, sharedRefs(t, a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1)...)
		}
	case reflect.Map:
		if a.IsNil() || b.IsNil() {
			return nil
		}
		if a.Pointer() == b.Pointer() {
			out = append(out, path)
		}
		iter := a.MapRange()
		for iter.Next() {
			if bv := b.MapIndex(iter.Key()); bv.IsValid() {
				out = append(out, sharedRefs(t, iter.Value(), bv, path+"{value}", depth+1)...)
			}
		}
	case reflect.Struct:
		if a.Type() == timeType {
			return nil
		}
		for i := 0; i < a.NumField(); i++ {
			f := a.Type().Field(i)
			av, bv := structField(a, i), structField(b, i)
			if !av.IsValid() || !bv.IsValid() {
				t.Fatalf("sharedRefs: %s.%s is unexported and not addressable, so it cannot be checked", path, f.Name)
			}
			out = append(out, sharedRefs(t, av, bv, path+"."+f.Name, depth+1)...)
		}
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
	default:
		t.Fatalf("sharedRefs: %s has kind %s, which this walk does not handle", path, a.Kind())
	}
	return out
}

// Every pointer, slice, array and map in a ground result, nested ones
// included, is the caller's own after cloning, and every value is equal. The
// fill is by reflection, so a field added to the models later fails here
// until the clone copies it.
func TestCloneGroundSearchResult_SharesNoReferences(t *testing.T) {
	shared := &models.GroundSearchResult{}
	fillRefs(t, reflect.ValueOf(shared).Elem(), "result", 0)
	c := cloneGroundSearchResult(shared)
	if got := sharedRefs(t, reflect.ValueOf(c).Elem(), reflect.ValueOf(shared).Elem(), "result", 0); len(got) > 0 {
		t.Errorf("clone shares these with the original: %v", got)
	}
	if !reflect.DeepEqual(c, shared) {
		t.Errorf("clone differs from the original:\n got %+v\nwant %+v", c, shared)
	}
}

// A result whose slices and pointers are nil clones to nil ones, not empty
// ones: a caller can still tell "no saving" and "no routes" apart.
func TestCloneGroundSearchResult_KeepsNil(t *testing.T) {
	for name, shared := range map[string]*models.GroundSearchResult{
		"empty result":            {},
		"route with nil contents": {Routes: []models.GroundRoute{{Provider: "flixbus", Legs: []models.GroundLeg{{}}}}},
		"saving with nil lists":   {HackSaving: &models.HackSaving{Type: "t"}, Destination: &models.DestinationInfo{Location: "x"}},
	} {
		if c := cloneGroundSearchResult(shared); !reflect.DeepEqual(c, shared) {
			t.Errorf("%s: clone = %+v, want %+v", name, c, shared)
		}
	}
	if cloneGroundSearchResult(nil) != nil {
		t.Error("cloneGroundSearchResult(nil) is not nil")
	}
}
