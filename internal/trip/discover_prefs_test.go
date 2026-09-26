package trip

import (
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/match"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

func TestRankDiscoverTrialsOmitsAnExcludedCity(t *testing.T) {
	start := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	window := candidateWindow{start: start, end: start.AddDate(0, 0, 3), nights: 3}
	trials := []discoverTrial{
		{window: window, dest: models.ExploreDestination{CityName: "Rome", AirportCode: "FCO", Price: 100}},
		{window: window, dest: models.ExploreDestination{CityName: "Reykjavik", AirportCode: "KEF", Price: 100}},
	}
	hotels := map[discoverTrialKey]*discoverHotelInfo{
		{airport: "FCO", nights: 3}: {total: 100, name: "a", rating: 8},
		{airport: "KEF", nights: 3}: {total: 100, name: "b", rating: 8},
	}
	prefs := preferences.Default()
	prefs.ExcludedDestinations = []string{"Rome"}
	prefs.BucketList = []string{"Iceland"}
	results := rankDiscoverTrials(trials, hotels, 500, "EUR", 5, prefs, match.Request{})
	if len(results) != 1 || results[0].AirportCode != "KEF" {
		t.Fatalf("results = %#v", results)
	}
}
