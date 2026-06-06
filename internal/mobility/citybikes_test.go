package mobility

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// rewriteTransport sends every request to the test server regardless of host.
type rewriteTransport struct{ target string }

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(rt.target)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestNearestNetwork(t *testing.T) {
	nets := []Network{
		{ID: "velib", Name: "Velib", City: "Paris", Lat: 48.85, Lon: 2.35},
		{ID: "citi", Name: "Citi Bike", City: "NYC", Lat: 40.71, Lon: -74.0},
	}
	// Amsterdam ~ 52.37, 4.90 -> Paris is far closer than NYC.
	got, dist, ok := nearestNetwork(nets, 52.37, 4.90)
	if !ok || got.ID != "velib" {
		t.Fatalf("nearest=%q ok=%v, want velib", got.ID, ok)
	}
	if dist <= 0 {
		t.Errorf("distance should be positive, got %f", dist)
	}
}

func TestFindBikes_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/search"): // nominatim
			_, _ = w.Write([]byte(`[{"lat":"52.3702","lon":"4.8952"}]`))
		case strings.HasPrefix(r.URL.Path, "/v2/networks/"): // network detail
			_, _ = w.Write([]byte(`{"network":{"id":"amsterdam","name":"OV-fiets","location":{"city":"Amsterdam"},"stations":[
				{"name":"Centraal","latitude":52.3791,"longitude":4.9003,"free_bikes":12,"empty_slots":3},
				{"name":"Zuid","latitude":52.3389,"longitude":4.8730,"free_bikes":0,"empty_slots":20}]}}`))
		default: // networks list
			_, _ = w.Write([]byte(`{"networks":[
				{"id":"amsterdam","name":"OV-fiets","location":{"latitude":52.37,"longitude":4.90,"city":"Amsterdam"}},
				{"id":"citi","name":"Citi Bike","location":{"latitude":40.71,"longitude":-74.0,"city":"NYC"}}]}`))
		}
	}))
	defer srv.Close()
	restore := replaceHTTPClient(&http.Client{Transport: &rewriteTransport{target: srv.URL}})
	defer restore()

	res, err := FindBikes(context.Background(), "Amsterdam", 10)
	if err != nil {
		t.Fatalf("FindBikes: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, err=%s", res.Error)
	}
	if res.Network.ID != "amsterdam" {
		t.Errorf("network=%q, want amsterdam", res.Network.ID)
	}
	if len(res.Stations) != 2 {
		t.Fatalf("got %d stations, want 2", len(res.Stations))
	}
	// Nearest to Amsterdam centre (52.3702,4.8952) is Centraal.
	if res.Stations[0].Name != "Centraal" || res.Stations[0].FreeBikes != 12 {
		t.Errorf("first station=%+v, want Centraal/12", res.Stations[0])
	}
	if res.Attribution != CityBikesAttribution {
		t.Errorf("missing attribution: %q", res.Attribution)
	}
}

func TestHaversineKm(t *testing.T) {
	// Amsterdam -> Paris is ~430 km.
	d := haversineKm(52.37, 4.90, 48.85, 2.35)
	if d < 400 || d > 460 {
		t.Errorf("AMS->Paris = %.0f km, want ~430", d)
	}
}
