package hotels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression tests for GH #187 / #188 Bug 1: Trivago dropped the
// trivago-search-suggestions tool and trivago-accommodation-search now takes a
// free-text `query` (no ns/id). Its review_count is also a thousands-separated
// string (e.g. "25,711") which previously broke the typed decode.

func readTrivagoFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseTrivagoToolNamesLiveFixture(t *testing.T) {
	body := readTrivagoFixture(t, "trivago_tools_list.json")
	names, err := parseTrivagoToolNames(body)
	if err != nil {
		t.Fatalf("parseTrivagoToolNames: %v", err)
	}
	// The live server no longer advertises trivago-search-suggestions.
	for _, n := range names {
		if n == "trivago-search-suggestions" {
			t.Fatalf("did not expect trivago-search-suggestions in tools list: %v", names)
		}
	}
	if len(names) == 0 {
		t.Fatal("expected at least one tool name")
	}
}

func TestSelectTrivagoSearchTool(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
		err   bool
	}{
		{"exact", []string{"trivago-accommodation-radius-search", "trivago-accommodation-search"}, "trivago-accommodation-search", false},
		{"renamed_non_radius", []string{"trivago-accommodation-radius-search", "trivago-v2-accommodation-search"}, "trivago-v2-accommodation-search", false},
		{"radius_only_fallback", []string{"trivago-accommodation-radius-search"}, "trivago-accommodation-radius-search", false},
		{"none", []string{"trivago-something-else"}, "", true},
		{"empty", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectTrivagoSearchTool(tt.names)
			if tt.err {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTrivagoAccommodationsLiveFixture(t *testing.T) {
	body := readTrivagoFixture(t, "trivago_accommodation_search.json")
	raw, err := parseTrivagoResponse(body)
	if err != nil {
		t.Fatalf("parseTrivagoResponse: %v", err)
	}
	hotels, err := parseTrivagoAccommodations(raw, "USD")
	if err != nil {
		t.Fatalf("parseTrivagoAccommodations: %v", err)
	}
	if len(hotels) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(hotels))
	}
	h := hotels[0]
	if h.Name != "Hotel Riu Plaza Fishermans Wharf" {
		t.Errorf("name: got %q", h.Name)
	}
	if h.Price != 223 {
		t.Errorf("price: got %v, want 223", h.Price)
	}
	// review_count "25,711" must parse to 25711 (comma-string tolerance).
	if h.ReviewCount != 25711 {
		t.Errorf("review count: got %d, want 25711", h.ReviewCount)
	}
	if len(h.Sources) == 0 || h.Sources[0].Provider != "trivago" {
		t.Errorf("expected trivago price source, got %+v", h.Sources)
	}
}

func TestTrivagoFlexIntFormats(t *testing.T) {
	cases := map[string]int{
		`"25,711"`: 25711,
		`"512"`:    512,
		`512`:      512,
		`"1 234"`:  1234,
		`null`:     0,
		`""`:       0,
		`"abc"`:    0,
	}
	for in, want := range cases {
		var c trivagoFlexInt
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if int(c) != want {
			t.Errorf("flexint(%s) = %d, want %d", in, int(c), want)
		}
	}
}

// TestSearchTrivagoNoSuggestionsCall is the end-to-end guard: SearchTrivago must
// NOT call trivago-search-suggestions, must discover the tool via tools/list,
// and must pass a free-text `query` to the accommodation-search tool.
func TestSearchTrivagoNoSuggestionsCall(t *testing.T) {
	origEnabled := trivagoEnabled
	trivagoEnabled = true
	defer func() { trivagoEnabled = origEnabled }()

	toolsList := readTrivagoFixture(t, "trivago_tools_list.json")
	accom := readTrivagoFixture(t, "trivago_accommodation_search.json")

	var calledTools []string
	var lastArgs map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req trivagoRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(toolsList)
		case "tools/call":
			// Capture which tool + args were requested.
			b, _ := json.Marshal(req.Params)
			var p trivagoToolCallParams
			_ = json.Unmarshal(b, &p)
			calledTools = append(calledTools, p.Name)
			lastArgs = p.Arguments
			if p.Name == "trivago-search-suggestions" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"tool 'trivago-search-suggestions' not found: tool not found"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(accom)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		}
	}))
	defer srv.Close()

	origClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{Transport: &rewriteTransport{target: srv.URL}}
	defer func() { trivagoHTTPClient = origClient }()

	hotels, err := SearchTrivago(context.Background(), "San Francisco", HotelSearchOptions{
		CheckIn:      "2026-09-05",
		CheckOut:     "2026-09-09",
		Guests:       2,
		ChildrenAges: []int{5, 7},
		Currency:     "USD",
	})
	if err != nil {
		t.Fatalf("SearchTrivago: %v", err)
	}
	if len(hotels) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(hotels))
	}
	for _, tool := range calledTools {
		if tool == "trivago-search-suggestions" {
			t.Fatalf("SearchTrivago must not call trivago-search-suggestions; called: %v", calledTools)
		}
	}
	if len(calledTools) != 1 || calledTools[0] != "trivago-accommodation-search" {
		t.Fatalf("expected single trivago-accommodation-search call, got %v", calledTools)
	}
	if q, _ := lastArgs["query"].(string); q != "San Francisco" {
		t.Errorf("expected query=San Francisco, got %v", lastArgs["query"])
	}
	if _, hasNS := lastArgs["ns"]; hasNS {
		t.Errorf("must not pass ns/id to accommodation-search: %v", lastArgs)
	}
	if ages, _ := lastArgs["children_ages"].(string); !strings.Contains(ages, "5") {
		t.Errorf("expected children_ages to include child ages, got %v", lastArgs["children_ages"])
	}
}
