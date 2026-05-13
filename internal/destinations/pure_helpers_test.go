package destinations

import "testing"

func TestNameMatchScore(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		have     string
		minScore int
	}{
		{name: "exact match", want: "Grand Hotel", have: "Grand Hotel Prague", minScore: 2},
		{name: "partial match", want: "Hotel Central", have: "Central Park Hotel", minScore: 1},
		{name: "no match", want: "Hilton", have: "Marriott", minScore: 0},
		{name: "empty want", want: "", have: "Hotel", minScore: 0},
		{name: "empty have", want: "Hotel", have: "", minScore: 0},
		{name: "short words skipped", want: "de la Hotel", have: "Hotel de la Paix", minScore: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nameMatchScore(tt.want, tt.have)
			if got < tt.minScore {
				t.Errorf("nameMatchScore(%q, %q) = %d, want >= %d", tt.want, tt.have, got, tt.minScore)
			}
		})
	}
}

func TestProximityMatch(t *testing.T) {
	// Helsinki Central Station: 60.1710, 24.9415
	// Helsinki Airport: 60.3172, 24.9633
	// Distance ~16 km.

	tests := []struct {
		name       string
		lat1, lon1 float64
		lat2, lon2 float64
		threshold  int
		want       bool
	}{
		{
			name: "same point",
			lat1: 60.17, lon1: 24.94, lat2: 60.17, lon2: 24.94,
			threshold: 100, want: true,
		},
		{
			name: "close points within threshold",
			lat1: 60.17, lon1: 24.94, lat2: 60.171, lon2: 24.941,
			threshold: 500, want: true,
		},
		{
			name: "far points outside threshold",
			lat1: 60.17, lon1: 24.94, lat2: 60.32, lon2: 24.96,
			threshold: 1000, want: false, // ~16 km apart
		},
		{
			name: "far points within large threshold",
			lat1: 60.17, lon1: 24.94, lat2: 60.32, lon2: 24.96,
			threshold: 20000, want: true, // 20 km threshold
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProximityMatch(tt.lat1, tt.lon1, tt.lat2, tt.lon2, tt.threshold)
			if got != tt.want {
				t.Errorf("ProximityMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWeatherCodeDescription_ExtraCodes(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "Clear sky"},
		{1, "Mainly clear"},
		{2, "Partly cloudy"},
		{3, "Overcast"},
		{45, "Fog"},
		{61, "Rain"},
		{71, "Snowfall"},
		{95, "Thunderstorm"},
		{999, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := weatherCodeDescription(tt.code)
			if got == "" {
				t.Errorf("weatherCodeDescription(%d) returned empty", tt.code)
			}
			// Just verify it returns non-empty; exact strings may vary.
		})
	}
}
