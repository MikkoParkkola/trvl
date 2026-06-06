// Package mobility provides micro-mobility data (bike-share, scooters) from
// free, unauthenticated sources.
//
// CityBikes (api.citybik.es) is a free service. Attribution is REQUIRED:
// callers MUST surface the CityBikes credit in user-facing output.
package mobility

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CityBikesAttribution is the required credit line for CityBikes data.
const CityBikesAttribution = "Bike-share data: CityBikes (citybik.es)"

var httpClient = &http.Client{Timeout: 12 * time.Second}

// Network is a bike-share network (e.g. "Citi Bike", "Vélib'").
type Network struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	City string  `json:"city"`
	Lat  float64 `json:"latitude"`
	Lon  float64 `json:"longitude"`
}

// Station is a single dock/stand with live availability.
type Station struct {
	Name       string  `json:"name"`
	Lat        float64 `json:"latitude"`
	Lon        float64 `json:"longitude"`
	FreeBikes  int     `json:"free_bikes"`
	EmptySlots int     `json:"empty_slots"`
}

// BikesResult is the output of a bike-share lookup for a location.
type BikesResult struct {
	Success     bool      `json:"success"`
	Query       string    `json:"query"`
	Network     Network   `json:"network"`
	DistanceKm  float64   `json:"network_distance_km"`
	Stations    []Station `json:"stations"`
	Attribution string    `json:"attribution"`
	Error       string    `json:"error,omitempty"`
}

// --- raw API shapes ---

type networksResponse struct {
	Networks []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Location struct {
			Lat  float64 `json:"latitude"`
			Lon  float64 `json:"longitude"`
			City string  `json:"city"`
		} `json:"location"`
	} `json:"networks"`
}

type networkDetailResponse struct {
	Network struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Location struct {
			City string `json:"city"`
		} `json:"location"`
		Stations []struct {
			Name       string  `json:"name"`
			Lat        float64 `json:"latitude"`
			Lon        float64 `json:"longitude"`
			FreeBikes  *int    `json:"free_bikes"`
			EmptySlots *int    `json:"empty_slots"`
		} `json:"stations"`
	} `json:"network"`
}

// FindBikes resolves a city to coordinates, picks the nearest CityBikes
// network, and returns its stations sorted by proximity to the city centre.
// maxStations caps the returned list (<=0 means all).
func FindBikes(ctx context.Context, city string, maxStations int) (*BikesResult, error) {
	lat, lon, err := geocode(ctx, city)
	if err != nil {
		return &BikesResult{Success: false, Query: city, Error: fmt.Sprintf("geocode: %v", err)}, nil
	}

	networks, err := listNetworks(ctx)
	if err != nil {
		return &BikesResult{Success: false, Query: city, Error: err.Error()}, nil
	}
	net, dist, ok := nearestNetwork(networks, lat, lon)
	if !ok {
		return &BikesResult{Success: false, Query: city, Error: "no bike-share networks found"}, nil
	}

	stations, err := networkStations(ctx, net.ID)
	if err != nil {
		return &BikesResult{Success: false, Query: city, Error: err.Error()}, nil
	}
	// Sort stations by distance to the geocoded point so the nearest are first.
	sort.Slice(stations, func(i, j int) bool {
		return haversineKm(lat, lon, stations[i].Lat, stations[i].Lon) <
			haversineKm(lat, lon, stations[j].Lat, stations[j].Lon)
	})
	if maxStations > 0 && len(stations) > maxStations {
		stations = stations[:maxStations]
	}

	return &BikesResult{
		Success:     true,
		Query:       city,
		Network:     net,
		DistanceKm:  dist,
		Stations:    stations,
		Attribution: CityBikesAttribution,
	}, nil
}

func listNetworks(ctx context.Context) ([]Network, error) {
	apiURL := "https://api.citybik.es/v2/networks?fields=id,name,location"
	body, err := getJSON(ctx, apiURL, 4*1024*1024)
	if err != nil {
		return nil, fmt.Errorf("citybikes networks: %w", err)
	}
	var raw networksResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("citybikes networks decode: %w", err)
	}
	out := make([]Network, 0, len(raw.Networks))
	for _, n := range raw.Networks {
		out = append(out, Network{
			ID:   n.ID,
			Name: n.Name,
			City: n.Location.City,
			Lat:  n.Location.Lat,
			Lon:  n.Location.Lon,
		})
	}
	return out, nil
}

func networkStations(ctx context.Context, id string) ([]Station, error) {
	apiURL := "https://api.citybik.es/v2/networks/" + url.PathEscape(id) + "?fields=stations"
	body, err := getJSON(ctx, apiURL, 8*1024*1024)
	if err != nil {
		return nil, fmt.Errorf("citybikes network %s: %w", id, err)
	}
	var raw networkDetailResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("citybikes network decode: %w", err)
	}
	out := make([]Station, 0, len(raw.Network.Stations))
	for _, s := range raw.Network.Stations {
		st := Station{Name: s.Name, Lat: s.Lat, Lon: s.Lon}
		if s.FreeBikes != nil {
			st.FreeBikes = *s.FreeBikes
		}
		if s.EmptySlots != nil {
			st.EmptySlots = *s.EmptySlots
		}
		out = append(out, st)
	}
	return out, nil
}

// nearestNetwork returns the network closest to (lat, lon) and its distance.
func nearestNetwork(networks []Network, lat, lon float64) (Network, float64, bool) {
	best := Network{}
	bestDist := math.MaxFloat64
	found := false
	for _, n := range networks {
		d := haversineKm(lat, lon, n.Lat, n.Lon)
		if d < bestDist {
			bestDist, best, found = d, n, true
		}
	}
	return best, bestDist, found
}

// geocode resolves a place name to lat/lon via Nominatim (free, no key).
func geocode(ctx context.Context, query string) (float64, float64, error) {
	params := url.Values{"q": {query}, "format": {"json"}, "limit": {"1"}}
	apiURL := "https://nominatim.openstreetmap.org/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("nominatim: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("nominatim: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return 0, 0, err
	}
	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return 0, 0, fmt.Errorf("nominatim decode: %w", err)
	}
	if len(results) == 0 {
		return 0, 0, fmt.Errorf("no geocoding results for %q", query)
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(results[0].Lat), 64)
	if err != nil {
		return 0, 0, err
	}
	lon, err := strconv.ParseFloat(strings.TrimSpace(results[0].Lon), 64)
	if err != nil {
		return 0, 0, err
	}
	return lat, lon, nil
}

func getJSON(ctx context.Context, apiURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// haversineKm returns the great-circle distance in km between two points.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return r * 2 * math.Asin(math.Sqrt(a))
}

// replaceHTTPClient swaps the package-level httpClient (used by tests).
func replaceHTTPClient(c *http.Client) func() {
	old := httpClient
	httpClient = c
	return func() { httpClient = old }
}
