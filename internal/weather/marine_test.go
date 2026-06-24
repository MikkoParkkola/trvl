package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSeaStateLabel(t *testing.T) {
	tests := []struct {
		wave float64
		want string
	}{
		{0, "calm"},
		{0.5, "calm"},
		{1.24, "calm"},
		{1.25, "moderate"},
		{2.0, "moderate"},
		{2.49, "moderate"},
		{2.5, "rough"},
		{4.0, "rough"},
	}
	for _, tc := range tests {
		if got := seaStateLabel(tc.wave); got != tc.want {
			t.Errorf("seaStateLabel(%.2f) = %q, want %q", tc.wave, got, tc.want)
		}
	}
}

func TestParseSeaState(t *testing.T) {
	body := []byte(`{"daily":{"time":["2026-07-01"],"wave_height_max":[1.8],"swell_wave_height_max":[1.1]}}`)
	state, err := parseSeaState(body)
	if err != nil {
		t.Fatalf("parseSeaState: %v", err)
	}
	if state.WaveHeight != 1.8 {
		t.Errorf("WaveHeight = %.2f, want 1.8", state.WaveHeight)
	}
	if state.SwellHeight != 1.1 {
		t.Errorf("SwellHeight = %.2f, want 1.1", state.SwellHeight)
	}
	if state.Label != "moderate" {
		t.Errorf("Label = %q, want %q", state.Label, "moderate")
	}
}

func TestParseSeaState_NoSwell(t *testing.T) {
	body := []byte(`{"daily":{"time":["2026-07-01"],"wave_height_max":[0.4]}}`)
	state, err := parseSeaState(body)
	if err != nil {
		t.Fatalf("parseSeaState: %v", err)
	}
	if state.SwellHeight != 0 {
		t.Errorf("SwellHeight = %.2f, want 0", state.SwellHeight)
	}
	if state.Label != "calm" {
		t.Errorf("Label = %q, want %q", state.Label, "calm")
	}
}

func TestParseSeaState_Empty(t *testing.T) {
	if _, err := parseSeaState([]byte(`{"daily":{"time":[],"wave_height_max":[]}}`)); err == nil {
		t.Error("expected error for empty wave-height data, got nil")
	}
}

func TestGetSeaState_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("daily"); got != "wave_height_max,swell_wave_height_max" {
			t.Errorf("daily param = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"daily":{"time":["2026-07-01"],"wave_height_max":[3.2],"swell_wave_height_max":[2.4]}}`))
	}))
	defer srv.Close()

	prev := marineAPIURL
	marineAPIURL = srv.URL
	defer func() { marineAPIURL = prev }()

	state, err := GetSeaState(context.Background(), 60.16, 24.94)
	if err != nil {
		t.Fatalf("GetSeaState: %v", err)
	}
	if state.WaveHeight != 3.2 {
		t.Errorf("WaveHeight = %.2f, want 3.2", state.WaveHeight)
	}
	if state.Label != "rough" {
		t.Errorf("Label = %q, want %q", state.Label, "rough")
	}
}

func TestGetSeaState_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	prev := marineAPIURL
	marineAPIURL = srv.URL
	defer func() { marineAPIURL = prev }()

	state, err := GetSeaState(context.Background(), 60.16, 24.94)
	if err == nil {
		t.Error("expected error on HTTP 503, got nil")
	}
	if state.Label != "" {
		t.Errorf("expected zero-value SeaState on error, got %+v", state)
	}
}
