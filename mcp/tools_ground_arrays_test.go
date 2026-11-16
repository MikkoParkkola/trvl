package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
)

// assertDeclaredArraysNotNull fails when a route carries null for a field the
// shared route schema declares as an array; a schema-validating client
// rejects such a reply.
func assertDeclaredArraysNotNull(t *testing.T, where string, routes []map[string]any) {
	t.Helper()
	props := groundRoutesOutputSchema().(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
	for i, r := range routes {
		for _, k := range sortedKeys(props) {
			if props[k].(map[string]interface{})["type"] != "array" {
				continue
			}
			if v, ok := r[k]; ok && v == nil {
				t.Errorf("%s route %d: %q is null, the schema declares an array", where, i, k)
			}
		}
	}
}

func stubAirportTransfers(t *testing.T, result *trip.AirportTransferResult) {
	t.Helper()
	orig := searchAirportTransfersFunc
	t.Cleanup(func() { searchAirportTransfersFunc = orig })
	searchAirportTransfersFunc = func(context.Context, trip.AirportTransferInput) (*trip.AirportTransferResult, error) {
		cp := *result
		cp.Routes = append([]models.GroundRoute(nil), result.Routes...)
		return &cp, nil
	}
}

var airportArgs = map[string]any{"airport_code": "CDG", "destination": "Louvre", "date": "2027-03-12"}

// TestAirportTransfersDeclaredArraysNeverNull pins the airport reply to the
// shared route schema: the taxi row sets no legs, and must still send [].
func TestAirportTransfersDeclaredArraysNeverNull(t *testing.T) {
	stubAirportTransfers(t, &trip.AirportTransferResult{
		Success: true, AirportCode: "CDG", Airport: "Paris Charles de Gaulle", AirportCity: "Paris",
		Destination: "Louvre", Date: "2027-03-12", Count: 2, ExactMatches: 2,
		Routes: []models.GroundRoute{
			{
				Provider: "taxi", Type: "taxi", Price: 55, Currency: "EUR", PriceProvenance: models.PriceEstimated, Duration: 45,
				Departure: models.GroundStop{City: "Paris", Station: "CDG", Time: "2027-03-12T10:00:00+01:00"},
				Arrival:   models.GroundStop{City: "Paris", Station: "Louvre", Time: "2027-03-12T10:45:00+01:00"},
			},
			{
				Provider: "transitous", Type: "train", PriceProvenance: models.PriceUnavailable, Duration: 50,
				Departure: models.GroundStop{City: "Paris", Station: "Aéroport CDG 2 TGV", Time: "2027-03-12T10:05:00+01:00"},
				Arrival:   models.GroundStop{City: "Paris", Station: "Châtelet", Time: "2027-03-12T10:55:00+01:00"},
				Legs: []models.GroundLeg{{Type: "train", Provider: "transitous", Duration: 50,
					Departure: models.GroundStop{City: "Paris", Station: "Aéroport CDG 2 TGV", Time: "2027-03-12T10:05:00+01:00"},
					Arrival:   models.GroundStop{City: "Paris", Station: "Châtelet", Time: "2027-03-12T10:55:00+01:00"}}},
			},
		},
	})
	got := callGroundTool(t, "search_airport_transfers", airportArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	routes := structuredRoutes(t, got)
	assertDeclaredArraysNotNull(t, "search_airport_transfers", routes)
	if legs, ok := routes[0]["legs"].([]any); !ok || len(legs) != 0 {
		t.Errorf("taxi legs = %#v, want []", routes[0]["legs"])
	}
	if strings.Contains(got.assistant, `"legs":null`) {
		t.Errorf("assistant copy carries \"legs\":null")
	}
}

// TestGroundToolsNoRouteRepliesKeepRoutesArray pins routes as an array on the
// replies that carry no routes at all.
func TestGroundToolsNoRouteRepliesKeepRoutesArray(t *testing.T) {
	stubAirportTransfers(t, &trip.AirportTransferResult{AirportCode: "CDG", Airport: "Paris Charles de Gaulle", Destination: "Louvre", Date: "2027-03-12"})
	stubGroundNoRoutes(t)
	for name, args := range map[string]map[string]any{
		"search_airport_transfers": airportArgs,
		"search_ground":            groundContractArgs,
	} {
		got := callGroundTool(t, name, args)
		if r, ok := got.structured["routes"].([]any); !ok || len(r) != 0 {
			t.Errorf("%s: structured routes = %#v, want []", name, got.structured["routes"])
		}
	}
}

func stubGroundNoRoutes(t *testing.T) {
	t.Helper()
	orig := searchGroundByNameFunc
	t.Cleanup(func() { searchGroundByNameFunc = orig })
	searchGroundByNameFunc = func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: false}, nil
	}
}

// TestSearchGroundDeclaredArraysNeverNull walks the same rule over the
// search_ground reply, where routes may come without legs or sources.
func TestSearchGroundDeclaredArraysNeverNull(t *testing.T) {
	routes := groundContractFixture(3)
	routes[1].Legs, routes[1].Sources, routes[1].Amenities = nil, nil, nil
	stubGroundRoutes(t, routes)
	assertDeclaredArraysNotNull(t, "search_ground", structuredRoutes(t, callSearchGround(t, groundContractArgs)))
}
