package hotels

import (
	"net/url"
	"testing"
)

func TestValidateDestinationResponseURL(t *testing.T) {
	parse := func(raw string) *url.URL {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse URL %q: %v", raw, err)
		}
		return u
	}

	tests := []struct {
		name      string
		requested *url.URL
		effective *url.URL
		wantErr   bool
	}{
		{
			name:      "exact destination",
			requested: parse("https://www.flatio.com/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon"),
		},
		{
			name:      "query-only change",
			requested: parse("https://www.flatio.com/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon?currency=EUR"),
		},
		{
			name:      "trailing slash",
			requested: parse("https://www.flatio.com/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon/"),
		},
		{
			name:      "scheme upgrade",
			requested: parse("http://www.flatio.com/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon"),
		},
		{
			name:      "explicit default port",
			requested: parse("https://www.flatio.com:443/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon"),
		},
		{
			name:      "generic parent fallback",
			requested: parse("https://www.flatio.com/s/Ischia_Italy"),
			effective: parse("https://www.flatio.com/s"),
			wantErr:   true,
		},
		{
			name:      "different destination",
			requested: parse("https://www.flatio.com/s/Ischia_Italy"),
			effective: parse("https://www.flatio.com/s/Reykjavik_Iceland"),
			wantErr:   true,
		},
		{
			name:      "destination prefix lookalike",
			requested: parse("https://www.flatio.com/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon-centre"),
			wantErr:   true,
		},
		{
			name:      "different host",
			requested: parse("https://www.flatio.com/s/Lisbon"),
			effective: parse("https://example.com/s/Lisbon"),
			wantErr:   true,
		},
		{
			name:      "non-default port removed",
			requested: parse("https://www.flatio.com:8443/s/Lisbon"),
			effective: parse("https://www.flatio.com/s/Lisbon"),
			wantErr:   true,
		},
		{
			name:      "different non-default port",
			requested: parse("https://www.flatio.com:8443/s/Lisbon"),
			effective: parse("https://www.flatio.com:9443/s/Lisbon"),
			wantErr:   true,
		},
		{
			name:      "missing requested URL",
			effective: parse("https://www.flatio.com/s/Lisbon"),
			wantErr:   true,
		},
		{
			name:      "missing effective URL",
			requested: parse("https://www.flatio.com/s/Lisbon"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDestinationResponseURL(tt.requested, tt.effective)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDestinationResponseURL(%v, %v) error = %v, wantErr %v", tt.requested, tt.effective, err, tt.wantErr)
			}
		})
	}
}

func TestValidateBluegroundDestinationResponseURL(t *testing.T) {
	parse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	requested := parse("https://www.theblueground.com/furnished-apartments-athens-gr")
	for _, tt := range []struct {
		name      string
		effective string
		wantErr   bool
	}{
		{name: "exact ISO path", effective: "https://www.theblueground.com/furnished-apartments-athens-gr"},
		{name: "canonical country name", effective: "https://www.theblueground.com/furnished-apartments-athens-greece"},
		{name: "different city", effective: "https://www.theblueground.com/furnished-apartments-paris-greece", wantErr: true},
		{name: "different country", effective: "https://www.theblueground.com/furnished-apartments-athens-portugal", wantErr: true},
		{name: "lookalike suffix", effective: "https://www.theblueground.com/furnished-apartments-athens-greece-cheap", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBluegroundDestinationResponseURL(requested, parse(tt.effective))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
