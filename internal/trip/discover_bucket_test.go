package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestPreferBucketDestinationsKeepsIcelandInTheCut(t *testing.T) {
	dests := []models.ExploreDestination{
		{CityName: "Brussels", AirportCode: "BRU", Price: 40},
		{CityName: "Paris", AirportCode: "CDG", Price: 50},
		{CityName: "Rome", AirportCode: "FCO", Price: 60},
		{CityName: "Madrid", AirportCode: "MAD", Price: 70},
		{CityName: "Berlin", AirportCode: "BER", Price: 80},
		{CityName: "Reykjavik", AirportCode: "KEF", Price: 200},
	}
	preferBucketDestinations(dests, []string{"Iceland"})
	if dests[0].CityName != "Reykjavik" {
		t.Fatalf("first = %s, want Reykjavik", dests[0].CityName)
	}
	if dests[1].CityName != "Brussels" {
		t.Fatalf("second = %s, want the cheapest non-match", dests[1].CityName)
	}
}
