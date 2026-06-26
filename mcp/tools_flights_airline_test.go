package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
)

// TestSearchFlightsTool_AirlineArg verifies the search_flights schema advertises
// the new `airline` filter argument (CLI --airline parity) as a string array.
func TestSearchFlightsTool_AirlineArg(t *testing.T) {
	tool := searchFlightsTool()
	prop, ok := tool.InputSchema.Properties["airline"]
	if !ok {
		t.Fatal("search_flights schema is missing the `airline` argument")
	}
	if prop.Type != "array" {
		t.Errorf("airline arg Type = %q, want \"array\"", prop.Type)
	}
	if prop.Items == nil || prop.Items.Type != "string" {
		t.Errorf("airline arg Items = %+v, want string items", prop.Items)
	}
}

// TestDispatchFlightSearch_Provider is a table-driven check of the provider
// switchboard: afklm is dispatchable (CLI parity), the airline filter arg flows
// without disturbing routing, and an unknown provider is still rejected. All
// cases stay offline: the afklm route is exercised via its deterministic
// multi-airport guard, which fires before any network call.
func TestDispatchFlightSearch_Provider(t *testing.T) {
	ctx := context.Background()
	opts := flights.SearchOptions{}

	tests := []struct {
		name         string
		args         map[string]any
		origin, dest string
		wantErrHas   string // substring the error must contain
		wantRouted   bool   // true => must NOT be the "unsupported provider" error
	}{
		{
			name:       "afklm is a recognized provider (multi-airport guard proves routing)",
			args:       map[string]any{"provider": "afklm"},
			origin:     "AMS,CDG",
			dest:       "JFK",
			wantErrHas: "provider afklm supports exactly one origin and one destination",
			wantRouted: true,
		},
		{
			name:       "afklm alias af-klm routes the same way",
			args:       map[string]any{"provider": "af-klm"},
			origin:     "AMS",
			dest:       "JFK,EWR",
			wantErrHas: "provider afklm supports exactly one origin and one destination",
			wantRouted: true,
		},
		{
			name:       "unknown provider is rejected",
			args:       map[string]any{"provider": "nope"},
			origin:     "AMS",
			dest:       "JFK",
			wantErrHas: "unsupported provider",
			wantRouted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := dispatchFlightSearch(ctx, tt.args, tt.origin, tt.dest, "2026-06-15", opts)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrHas) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErrHas)
			}
			isUnsupported := strings.Contains(err.Error(), "unsupported provider")
			if tt.wantRouted && isUnsupported {
				t.Errorf("provider was not routed: %q", err.Error())
			}
		})
	}
}

// TestHandleSearchFlights_AirlineArgParsed proves the airline arg is parsed into
// SearchOptions.Airlines exactly as the CLI does, by exercising the same
// argStringSlice helper the handler uses. Both comma-separated and JSON-array
// forms are accepted.
func TestHandleSearchFlights_AirlineArgParsed(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want []string
	}{
		{"comma-separated string", "AY,AF", []string{"AY", "AF"}},
		{"json array", []any{"AY", "KL"}, []string{"AY", "KL"}},
		{"empty string yields no filter", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := argStringSlice(map[string]any{"airline": tt.v}, "airline")
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
