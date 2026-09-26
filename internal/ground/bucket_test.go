package ground

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestPreferBucketRanksIcelandAhead(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Arrival: models.GroundStop{City: "Brussels"}, Price: 20},
		{Provider: "dfds", Arrival: models.GroundStop{City: "Reykjavik"}, Price: 90},
	}
	PreferBucket(routes, []string{"Iceland", "Balkans"})
	if routes[0].Arrival.City != "Reykjavik" {
		t.Fatalf("first = %s, want Reykjavik", routes[0].Arrival.City)
	}
}

func TestPreferBucketKeepsPriceOrderInsideAGroup(t *testing.T) {
	routes := []models.GroundRoute{
		{Arrival: models.GroundStop{City: "Split"}, Price: 80},
		{Arrival: models.GroundStop{City: "Dubrovnik"}, Price: 40},
		{Arrival: models.GroundStop{City: "Paris"}, Price: 10},
	}
	PreferBucket(routes, []string{"balkans"})
	if routes[0].Arrival.City != "Split" || routes[1].Arrival.City != "Dubrovnik" || routes[2].Arrival.City != "Paris" {
		t.Fatalf("order = %s, %s, %s", routes[0].Arrival.City, routes[1].Arrival.City, routes[2].Arrival.City)
	}
}

func TestPreferBucketDoesNotMixDirections(t *testing.T) {
	routes := []models.GroundRoute{
		{Direction: "inbound", Arrival: models.GroundStop{City: "Helsinki"}, Price: 10},
		{Direction: "outbound", Arrival: models.GroundStop{City: "Reykjavik"}, Price: 90},
	}
	PreferBucket(routes, []string{"Iceland"})
	if routes[0].Direction != "inbound" || routes[1].Arrival.City != "Reykjavik" {
		t.Fatalf("order = %s/%s then %s/%s", routes[0].Direction, routes[0].Arrival.City, routes[1].Direction, routes[1].Arrival.City)
	}
}
