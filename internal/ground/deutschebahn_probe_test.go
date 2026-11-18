package ground

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

// probeCaptureEnv makes a live probe print each raw upstream body as one
// "CAPTURE <file> <base64>" line, so a fixture can be refreshed from the
// probe's output without the body ever being shown.
const probeCaptureEnv = "TRVL_PROBE_CAPTURE"

// captureTransport keeps the body of every response whose path ends in
// suffix and hands the caller an identical copy.
type captureTransport struct {
	base   http.RoundTripper
	suffix string
	mu     sync.Mutex
	bodies [][]byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.base.RoundTrip(req)
	if err != nil || !strings.HasSuffix(req.URL.Path, c.suffix) {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func printCapture(t *testing.T, file string, body []byte) {
	t.Helper()
	if os.Getenv(probeCaptureEnv) != "1" || len(body) == 0 {
		return
	}
	fmt.Printf("CAPTURE %s %s\n", file, base64.StdEncoding.EncodeToString(body))
}

// TestLiveProbe_DBStationIDs searches DB journeys through SearchDeutscheBahn
// (same endpoint, body and headers as production) to the dbStations entries
// and requires every journey to end at a final halt with the table's EVA and
// name: an id DB resolves to another station (8101003 -> Wien Blumental)
// fails here.
func TestLiveProbe_DBStationIDs(t *testing.T) {
	testutil.RequireLiveProbe(t)

	capture := &captureTransport{base: http.DefaultTransport, suffix: "/fahrplan"}
	origClient := dbClient
	dbClient = &http.Client{Transport: capture, Timeout: 30 * time.Second}
	t.Cleanup(func() { dbClient = origClient })

	date := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	stamp := time.Now().UTC().Format("2006-01-02")
	cases := []struct {
		slug, from, to string
		eva            string // only for the reproduction id, which is not in the table
	}{
		{slug: "praha_8101003", from: "prague", eva: "8101003"}, // the old Vienna id: needs journeys, any final halt
		{slug: "praha_8103000", from: "prague", to: "vienna"},
		{slug: "berlin_5100065", from: "berlin", to: "warsaw"},
		{slug: "berlin_5100028", from: "berlin", to: "krakow"},
	}
	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			to := tc.to
			if tc.eva != "" {
				to = "probe-" + tc.eva
				dbStations[to] = DBStation{EVA: tc.eva, City: to}
				t.Cleanup(func() { delete(dbStations, to) })
			}
			station, ok := LookupDBStation(to)
			if !ok {
				t.Fatalf("no DB station for %q", to)
			}
			capture.mu.Lock()
			capture.bodies = nil
			capture.mu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			routes, err := SearchDeutscheBahn(ctx, tc.from, to, date, "EUR")
			capture.mu.Lock()
			bodies := capture.bodies
			capture.mu.Unlock()
			if len(bodies) > 0 {
				printCapture(t, fmt.Sprintf("db_fahrplan_%s_%s.json", tc.slug, stamp), bodies[0])
			}
			if err := dbProbeSearchErr(routes, err); err != nil {
				testutil.SkipIfTransient(t, err)
				t.Fatalf("SearchDeutscheBahn(%s -> %s): %v", tc.from, station.EVA, err)
			}
			ends := map[string]int{}
			for _, r := range routes {
				ends[r.Arrival.Station]++
			}
			t.Logf("%s -> %s on %s: %d routes ending at %v", tc.from, station.EVA, date, len(routes), ends)

			if len(bodies) == 0 {
				t.Fatal("no fahrplan body captured")
			}
			halts := dbFinalHalts(t, bodies[0])
			if len(halts) == 0 {
				t.Fatal("no journeys in the fahrplan answer")
			}
			if tc.eva != "" {
				return
			}
			for _, h := range halts {
				if h.EvaNr != station.EVA || h.Name != station.Name {
					t.Errorf("journey ends at %q (%s), table has %q (%s)", h.Name, h.EvaNr, station.Name, station.EVA)
				}
			}
		})
	}
}

// dbProbeSearchErr is the station-ID probe's verdict on a SearchDeutscheBahn
// answer. The probe proves the ids resolve to journeys, so DB's honest
// no-fare notice, which it only sends with journeys, passes; no journeys or
// any other error fails.
func dbProbeSearchErr(routes []models.GroundRoute, err error) error {
	if len(routes) > 0 && errors.Is(err, errDBNoFares) {
		return nil
	}
	return err
}

func TestDBProbeSearchErr(t *testing.T) {
	one := []models.GroundRoute{{Provider: "db"}}
	noFares := dbNoFaresNotice(nil)
	for name, err := range map[string]error{
		"priced journeys":        nil,
		"no-fare notice":         noFares,
		"wrapped no-fare notice": fmt.Errorf("ground: %w", noFares),
	} {
		if got := dbProbeSearchErr(one, err); got != nil {
			t.Errorf("%s with journeys: got %v, want pass", name, got)
		}
	}
	// Failures come back unchanged, so SkipIfTransient still sees a 429.
	for name, tc := range map[string]struct {
		routes []models.GroundRoute
		err    error
	}{
		"no-fare notice without journeys": {nil, noFares},
		"same text, not the sentinel":     {one, errors.New(errDBNoFares.Error())},
		"hard error":                      {one, errors.New("db search: HTTP 500: boom")},
		"price lookup failed":             {one, dbPriceLookupNotice(errors.New("tagesbestpreis: HTTP 500"))},
	} {
		if got := dbProbeSearchErr(tc.routes, tc.err); got != tc.err {
			t.Errorf("%s: got %v, want the error unchanged", name, got)
		}
	}
}

// TestLiveProbe_DBBestPrice calls the tagesbestpreis endpoint through
// fetchDBBestPrice (production body and headers) for Berlin Hbf to München
// Hbf three weeks out, a domestic pair DB always prices, and requires a fare.
// A zero means the request body stopped working or the answer changed shape.
func TestLiveProbe_DBBestPrice(t *testing.T) {
	testutil.RequireLiveProbe(t)
	from, okFrom := LookupDBStation("Berlin")
	to, okTo := LookupDBStation("Munich")
	if !okFrom || !okTo {
		t.Fatal("Berlin or Munich missing from the DB station table")
	}
	date := time.Now().AddDate(0, 0, 21).Format("2006-01-02")
	price, currency, err := fetchDBBestPrice(context.Background(), from.EVA, to.EVA, date)
	testutil.SkipIfTransient(t, err) // timeouts, 429 and 452 OPS_BLOCKED are not drift
	if err != nil {
		t.Fatalf("tagesbestpreis %s -> %s on %s: %v", from.EVA, to.EVA, date, err)
	}
	if price <= 0 || currency == "" {
		t.Fatalf("tagesbestpreis %s -> %s on %s = %v %q, want a priced answer", from.EVA, to.EVA, date, price, currency)
	}
	t.Logf("tagesbestpreis Berlin -> München on %s: %.2f %s", date, price, currency)
}
