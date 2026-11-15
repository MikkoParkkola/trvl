package ground

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// oebbShopBlockPage is the shape of the shop's Cloudflare refusal: a full
// HTML page whose only useful text is its title.
const oebbShopBlockPage = `<!doctype html>
<html lang="de">
<head>
  <meta charset="utf-8">
  <title>Zugriff vorübergehend eingeschränkt | Access temporarily restricted</title>
  <style>:root { --brand: #e2001a; }</style>
</head>
<body><h1>Zugriff vorübergehend eingeschränkt</h1></body>
</html>`

// oebbMock routes SearchOebb's traffic to handlers keyed by URL path and
// records the HAFAS request bodies it receives.
type oebbMock struct {
	shopToken  http.HandlerFunc
	shopInit   http.HandlerFunc // nil answers 404
	mgateFile  string
	mu         sync.Mutex
	mgateCalls []map[string]any
}

func (m *oebbMock) install(t *testing.T) {
	t.Helper()
	origClient, origLimiter := oebbClient, oebbLimiter
	oebbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { oebbClient, oebbLimiter = origClient, origLimiter })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/domain/v1/anonymousToken":
			m.shopToken(w, r)
		case "/api/domain/v1/initUserData":
			if m.shopInit == nil {
				http.NotFound(w, r)
				return
			}
			m.shopInit(w, r)
		case "/bin/mgate.exe":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.mu.Lock()
			m.mgateCalls = append(m.mgateCalls, body)
			m.mu.Unlock()
			if m.mgateFile == "" {
				http.NotFound(w, r)
				return
			}
			data, err := os.ReadFile(filepath.Join("testdata", m.mgateFile))
			if err != nil {
				t.Errorf("fixture: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oebbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
}

// calls returns the HAFAS request bodies received so far.
func (m *oebbMock) calls() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any(nil), m.mgateCalls...)
}

func oebbShopRefuses(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(oebbShopBlockPage))
}

// oebbFixtureClickout returns the shop clickout HAFAS attached to the
// connection departing at dTimeS in a captured TripSearch.
func oebbFixtureClickout(t *testing.T, file, dTimeS string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		SvcResL []struct {
			Res struct {
				OutConL []struct {
					Dep struct {
						DTimeS string `json:"dTimeS"`
					} `json:"dep"`
					TrfRes struct {
						Clickout string `json:"clickout"`
					} `json:"trfRes"`
				} `json:"outConL"`
			} `json:"res"`
		} `json:"svcResL"`
	}
	if err := json.Unmarshal(data, &env); err != nil || len(env.SvcResL) == 0 {
		t.Fatalf("decode fixture: %v", err)
	}
	for _, c := range env.SvcResL[0].Res.OutConL {
		if c.Dep.DTimeS == dTimeS && c.TrfRes.Clickout != "" {
			return c.TrfRes.Clickout
		}
	}
	t.Fatalf("no clickout for %s in %s", dTimeS, file)
	return ""
}
