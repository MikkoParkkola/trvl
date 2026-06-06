package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAirQuality(t *testing.T) {
	body := []byte(`{"current":{"time":"2026-06-10T14:00","european_aqi":42.0,"pm2_5":8.1,"pm10":12.3,"ozone":61.0,"nitrogen_dioxide":15.5}}`)
	res, err := parseAirQuality("Funchal", body)
	if err != nil {
		t.Fatalf("parseAirQuality: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}
	if res.AQI != 42 || res.Category != "Moderate" {
		t.Errorf("AQI=%.0f cat=%q, want 42/Moderate", res.AQI, res.Category)
	}
	if res.PM25 != 8.1 || res.PM10 != 12.3 || res.Ozone != 61 || res.NO2 != 15.5 {
		t.Errorf("pollutants mismatch: %+v", res.AirQuality)
	}
}

func TestAQICategory(t *testing.T) {
	cases := []struct {
		aqi  float64
		want string
	}{
		{10, "Good"}, {30, "Fair"}, {50, "Moderate"},
		{70, "Poor"}, {90, "Very poor"}, {150, "Extremely poor"},
	}
	for _, c := range cases {
		if got := AQICategory(c.aqi); got != c.want {
			t.Errorf("AQICategory(%.0f)=%q, want %q", c.aqi, got, c.want)
		}
	}
}

func TestGetAirQuality_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Host == "nominatim.openstreetmap.org" || r.URL.Path == "/search":
			_, _ = w.Write([]byte(`[{"lat":"32.6669","lon":"-16.9241"}]`))
		default:
			_, _ = w.Write([]byte(`{"current":{"time":"2026-06-10T14:00","european_aqi":18,"pm2_5":4.0,"pm10":6.0,"ozone":40,"nitrogen_dioxide":5}}`))
		}
	}))
	defer srv.Close()
	restore := replaceHTTPClient(&http.Client{Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL}})
	defer restore()
	clearGeoCache()

	res, err := GetAirQuality(context.Background(), "TestVille")
	if err != nil {
		t.Fatalf("GetAirQuality: %v", err)
	}
	if !res.Success || res.Category != "Good" {
		t.Errorf("got success=%v cat=%q, want true/Good", res.Success, res.Category)
	}
}

func TestParseSunTimes(t *testing.T) {
	body := []byte(`{"results":{"sunrise":"2026-06-21T05:00:00+00:00","sunset":"2026-06-21T20:00:00+00:00","solar_noon":"2026-06-21T12:30:00+00:00","day_length":54000,"civil_twilight_begin":"2026-06-21T04:30:00+00:00","civil_twilight_end":"2026-06-21T20:30:00+00:00"},"status":"OK"}`)
	res, err := parseSunTimes("Helsinki", body)
	if err != nil {
		t.Fatalf("parseSunTimes: %v", err)
	}
	if !res.Success || res.Sunrise != "2026-06-21T05:00:00+00:00" || res.DayLength != 54000 {
		t.Errorf("unexpected: %+v", res.SunTimes)
	}
}

func TestParseSunTimes_BadStatus(t *testing.T) {
	body := []byte(`{"results":{},"status":"INVALID_REQUEST"}`)
	res, err := parseSunTimes("Nowhere", body)
	if err != nil {
		t.Fatalf("parseSunTimes: %v", err)
	}
	if res.Success {
		t.Errorf("expected failure on bad status")
	}
}
