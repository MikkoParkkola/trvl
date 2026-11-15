package ground

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
// (same endpoint, body and headers as production) to each audited station id
// and logs where the journeys end. Each id must end at a final halt carrying
// that same evaNr and the expected name: an id DB resolves to a different
// station (8101003 -> Wien Blumental) fails here.
func TestLiveProbe_DBStationIDs(t *testing.T) {
	testutil.RequireLiveProbe(t)

	capture := &captureTransport{base: http.DefaultTransport, suffix: "/fahrplan"}
	origClient := dbClient
	dbClient = &http.Client{Transport: capture, Timeout: 30 * time.Second}
	t.Cleanup(func() { dbClient = origClient })

	date := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	stamp := time.Now().UTC().Format("2006-01-02")
	cases := []struct {
		slug, from, eva, want string
	}{
		{"praha_8101003", "prague", "8101003", ""}, // the id the table used for Vienna: reproduction only
		{"praha_8103000", "prague", "8103000", "Wien Hbf"},
		{"berlin_5100065", "berlin", "5100065", "Warszawa Centralna"},
		{"berlin_5100028", "berlin", "5100028", "Krakow Glowny"},
	}
	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			key := "probe-" + tc.eva
			dbStations[key] = DBStation{EVA: tc.eva, Name: tc.want, City: key}
			t.Cleanup(func() { delete(dbStations, key) })
			capture.mu.Lock()
			capture.bodies = nil
			capture.mu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			routes, err := SearchDeutscheBahn(ctx, tc.from, key, date, "EUR")
			capture.mu.Lock()
			bodies := capture.bodies
			capture.mu.Unlock()
			if len(bodies) > 0 {
				printCapture(t, fmt.Sprintf("db_fahrplan_%s_%s.json", tc.slug, stamp), bodies[0])
			}
			if err != nil {
				t.Fatalf("SearchDeutscheBahn(%s -> %s): %v", tc.from, tc.eva, err)
			}
			ends := map[string]int{}
			for _, r := range routes {
				ends[r.Arrival.Station]++
			}
			t.Logf("%s -> %s on %s: %d routes ending at %v", tc.from, tc.eva, date, len(routes), ends)

			if tc.want == "" {
				return
			}
			if len(bodies) == 0 {
				t.Fatal("no fahrplan body captured")
			}
			halts := dbFinalHalts(t, bodies[0])
			for _, h := range halts {
				if h.EvaNr == tc.eva && h.Name == tc.want {
					return
				}
			}
			t.Errorf("no journey to %s ends at %q with that evaNr; final halts: %+v", tc.eva, tc.want, halts)
		})
	}
}
