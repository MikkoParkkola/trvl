package flights

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// calendarCityBody is a successful H028ib row. Slot 3 is the city JSON and
// the status slot is null, so city resolution must not look like a refusal.
const calendarCityBody = `)]}'

33
[["wrb.fr","H028ib","[[[[3,\"Helsinki\",\"Helsinki\",\"Finland\",\"/m/01lbs\",1,0,null,null,null,null,null,[\"HEL\"]]],null]]",null,null,null,"generic"]]
`

func TestRefuseCalendarFallback(t *testing.T) {
	code13 := []byte(`)]}'` + "\n\n" + `[["wrb.fr",null,null,null,null,[13]]]`)
	code3 := []byte(`)]}'` + "\n\n" + `[["wrb.fr",null,null,null,null,[3]]]`)

	if err := refuseCalendarFallback(403, nil); !errors.Is(err, batchexec.ErrBlocked) {
		t.Fatalf("403 err = %v", err)
	}
	if err := refuseCalendarFallback(429, []byte("no")); err == nil || !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("429 err = %v", err)
	}
	if err := refuseCalendarFallback(200, code13); err == nil || !strings.Contains(err.Error(), "code 13") {
		t.Fatalf("code 13 err = %v", err)
	}
	if err := refuseCalendarFallback(200, code3); err == nil || !strings.Contains(err.Error(), "code 3") {
		t.Fatalf("code 3 err = %v", err)
	}
	if err := refuseCalendarFallback(500, []byte("down")); err != nil {
		t.Fatalf("500 should still be allowed to fall back, got %v", err)
	}
	if err := refuseCalendarFallback(200, []byte("not a wrb refusal")); err != nil {
		t.Fatalf("ordinary short body should still be allowed to fall back, got %v", err)
	}
}

func TestSearchCalendar_Code13DoesNotScanDates(t *testing.T) {
	assertCalendarDoesNotScan(t, http.StatusOK, []byte(`)]}'`+"\n\n"+`[["wrb.fr",null,null,null,null,[13]],["di",34]]`), "code 13")
}

func TestSearchCalendar_Code3DoesNotScanDates(t *testing.T) {
	assertCalendarDoesNotScan(t, http.StatusOK, []byte(`)]}'`+"\n\n"+`[["wrb.fr",null,null,null,null,[3]]]`), "code 3")
}

func TestSearchCalendar_HTTP429DoesNotScanDates(t *testing.T) {
	assertCalendarDoesNotScan(t, http.StatusTooManyRequests, []byte("rate limited"), "rate-limited")
}

func TestSearchCalendar_CityRefusalDoesNotScanDates(t *testing.T) {
	batchexec.ResetCityCache()
	t.Cleanup(batchexec.ResetCityCache)

	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.Contains(r.URL.Path, "batchexecute") {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`)]}'` + "\n\n" + `[["wrb.fr",null,null,null,null,[13]]]`))
	}))
	defer ts.Close()

	scanned := false
	_, err := searchCalendarWithClient(context.Background(), batchexec.NewTestClient(ts.URL), "AMS", "HEL", calendarOpts(), func(context.Context, string, string, CalendarOptions) (*models.DateSearchResult, error) {
		scanned = true
		return nil, errors.New("per-day scan")
	})
	if scanned {
		t.Fatal("a refused city lookup started a per-day scan")
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 city lookup and no calendar call", hits.Load())
	}
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want rate limited", err)
	}
}

func TestSearchCalendar_UnparsedBodyStillFallsBack(t *testing.T) {
	batchexec.ResetCityCache()
	t.Cleanup(batchexec.ResetCityCache)

	var calendarHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "GetCalendarGraph") {
			calendarHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", 250)))
			return
		}
		if strings.Contains(r.URL.Path, "batchexecute") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(calendarCityBody))
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer ts.Close()

	scanned := false
	_, err := searchCalendarWithClient(context.Background(), batchexec.NewTestClient(ts.URL), "AMS", "HEL", calendarOpts(), func(context.Context, string, string, CalendarOptions) (*models.DateSearchResult, error) {
		scanned = true
		return nil, errors.New("per-day scan")
	})
	if !scanned {
		t.Fatal("an unparsed calendar body should still fall back to a per-day scan")
	}
	if calendarHits.Load() != 1 {
		t.Fatalf("calendar calls = %d, want 1", calendarHits.Load())
	}
	if err == nil || err.Error() != "per-day scan" {
		t.Fatalf("err = %v", err)
	}
}

func assertCalendarDoesNotScan(t *testing.T, status int, body []byte, want string) {
	t.Helper()
	batchexec.ResetCityCache()
	t.Cleanup(batchexec.ResetCityCache)

	var calendarHits atomic.Int32
	var other atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "GetCalendarGraph"):
			calendarHits.Add(1)
			w.WriteHeader(status)
			_, _ = w.Write(body)
		case strings.Contains(r.URL.Path, "batchexecute"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(calendarCityBody))
		default:
			other.Add(1)
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	scanned := false
	_, err := searchCalendarWithClient(context.Background(), batchexec.NewTestClient(ts.URL), "AMS", "HEL", calendarOpts(), func(context.Context, string, string, CalendarOptions) (*models.DateSearchResult, error) {
		scanned = true
		return nil, errors.New("per-day scan")
	})
	if scanned {
		t.Fatalf("%s started a per-day scan", want)
	}
	if calendarHits.Load() != 1 {
		t.Fatalf("%s calendar calls = %d, want 1", want, calendarHits.Load())
	}
	if other.Load() != 0 {
		t.Fatalf("%s made %d unexpected calls", want, other.Load())
	}
	if !errors.Is(err, models.ErrRateLimited) || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s err = %v", want, err)
	}
}

func calendarOpts() CalendarOptions {
	return CalendarOptions{FromDate: "2026-10-09", ToDate: "2026-10-16", Adults: 1}
}
