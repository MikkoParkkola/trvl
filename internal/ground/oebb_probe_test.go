package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

const oebbMgateURL = "https://fahrplan.oebb.at/bin/mgate.exe"

const probeBrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var probeTitle = regexp.MustCompile(`<title>([^<]*)</title>`)

// TestLiveProbe_OebbShopClientSignature asks the shop token endpoint once per
// client signature in one run, so every arm sees the same IP and minute. The
// shop's bot rule decides which signature SearchOebb has to send.
func TestLiveProbe_OebbShopClientSignature(t *testing.T) {
	testutil.RequireLiveProbe(t)
	tier1, err := providers.NewTier1Client()
	if err != nil {
		t.Fatalf("tier1 client: %v", err)
	}
	goClient := &http.Client{Timeout: 30 * time.Second}
	arms := []struct {
		name string
		do   func(*http.Request) (*http.Response, error)
		ua   string
	}{
		{"go-default-ua", goClient.Do, ""},
		{"go-browser-ua", goClient.Do, probeBrowserUA},
		{"tls-client-no-ua", tier1.Do, ""},
		{"tls-client-browser-ua", tier1.Do, probeBrowserUA},
	}
	passed := 0
	for _, arm := range arms {
		req, err := http.NewRequest(http.MethodGet, oebbShopBase+"/api/domain/v1/anonymousToken", nil)
		if err != nil {
			t.Fatal(err)
		}
		oebbShopSetHeaders(req, "")
		if arm.ua != "" {
			req.Header.Set("User-Agent", arm.ua)
		}
		resp, err := arm.do(req)
		if err != nil {
			t.Logf("arm=%s at=%s error=%v", arm.name, time.Now().UTC().Format(time.TimeOnly), err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		title := ""
		if m := probeTitle.FindSubmatch(body); m != nil {
			title = string(m[1])
		}
		hasToken := bytes.Contains(body, []byte(`"access_token"`))
		t.Logf("arm=%s at=%s status=%d token=%v title=%q", arm.name, time.Now().UTC().Format(time.TimeOnly), resp.StatusCode, hasToken, title)
		if resp.StatusCode == http.StatusOK && hasToken {
			passed++
		}
		time.Sleep(2 * time.Second)
	}
	if passed == 0 {
		t.Error("no client signature obtained a shop token")
	}
}

// oebbProbeMgate posts a HAFAS mgate envelope and returns the raw body.
func oebbProbeMgate(t *testing.T, envelope map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oebbMgateURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := oebbClient.Do(req)
	if err != nil {
		t.Fatalf("mgate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		t.Fatalf("mgate read: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mgate: HTTP %d", resp.StatusCode)
	}
	return body
}

// TestLiveProbe_OebbHafasTripSearch runs the timetable fallback's request
// (Prague -> Wien Hbf) against live HAFAS and parses it with the production
// parser. HAFAS answers errors with HTTP 200, so svcResL[0].err is checked.
func TestLiveProbe_OebbHafasTripSearch(t *testing.T) {
	testutil.RequireLiveProbe(t)
	from, _ := LookupOebbStation("prague")
	to, _ := LookupOebbStation("vienna")
	day := time.Now().AddDate(0, 0, 2)
	body := oebbProbeMgate(t, oebbTripSearchRequest(from.ExtID, to.ExtID, day.Format("20060102"), "060000"))
	printCapture(t, "oebb_hafas_tripsearch_praha_wien_"+time.Now().UTC().Format("2006-01-02")+".json", body)

	var env struct {
		SvcResL []struct {
			Err    string      `json:"err"`
			ErrTxt string      `json:"errTxt"`
			Res    oebbTripRes `json:"res"`
		} `json:"svcResL"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.SvcResL) == 0 || env.SvcResL[0].Err != "OK" {
		t.Fatalf("mgate service error: %+v", env.SvcResL)
	}
	routes := parseOebbConnections(env.SvcResL[0].Res, from, to, day.Format("2006-01-02"), "EUR")
	if len(routes) == 0 {
		t.Fatal("no connections Prague -> Wien Hbf")
	}
	for _, r := range routes {
		t.Logf("%s -> %s transfers=%d legs=%d", r.Departure.Time, r.Arrival.Time, r.Transfers, len(r.Legs))
	}
}

// TestLiveProbe_OebbStationIDs looks each audited station up by name in ÖBB
// HAFAS and checks the table's ExtID against the first match.
func TestLiveProbe_OebbStationIDs(t *testing.T) {
	testutil.RequireLiveProbe(t)
	for city, query := range map[string]string{"warsaw": "Warszawa Centralna", "krakow": "Krakow Glowny"} {
		body := oebbProbeMgate(t, oebbLocMatchRequest(query))
		got := oebbFirstLocMatch(t, body)
		station, _ := LookupOebbStation(city)
		t.Logf("%s: HAFAS %q extId=%s, table ExtID=%s", city, got.Name, got.ExtID, station.ExtID)
		if got.ExtID != station.ExtID {
			t.Errorf("%s: table ExtID %s, HAFAS says %q is %s", city, station.ExtID, got.Name, got.ExtID)
		}
	}
}
