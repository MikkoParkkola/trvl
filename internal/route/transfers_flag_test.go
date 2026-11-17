package route

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// A ground route whose provider sent no change data keeps that fact as a leg
// of a planned route.
func TestSearchGroundLeg_CarriesTransfersFlag(t *testing.T) {
	withSearchMocks(t, nil, func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
		return &models.GroundSearchResult{Success: true, Count: 1, Routes: []models.GroundRoute{
			{Provider: "oebb", Type: "train", Price: 30, Currency: "EUR", TransfersUnknown: true,
				Departure: models.GroundStop{Time: "2026-11-20T10:21:00"}, Arrival: models.GroundStop{Time: "2026-11-20T15:32:00"}},
		}}, nil
	}, nil)
	legs := searchGroundLeg(context.Background(), Hub{City: "Prague"}, Hub{City: "Vienna"}, "2026-11-20", Options{})
	if len(legs) != 1 || !legs[0].TransfersUnknown {
		t.Fatalf("legs = %+v, want one leg flagged TransfersUnknown", legs)
	}
}

// A single-leg itinerary reports the leg's own changes, as combineSegments
// already does for hub routes, and never calls an unknown count direct.
func TestSingleLegItinerary_ReportsLegChanges(t *testing.T) {
	if it := singleLegItinerary(models.RouteLeg{Mode: "train", Transfers: 1}); it.Transfers != 1 {
		t.Errorf("Transfers = %d, want the leg's 1 change", it.Transfers)
	}
	if it := singleLegItinerary(models.RouteLeg{Mode: "train", TransfersUnknown: true}); !it.TransfersUnknown {
		t.Errorf("TransfersUnknown = false, want the leg's flag carried")
	}
}

func TestCombineSegments_CarriesTransfersFlag(t *testing.T) {
	p := path{cities: []Hub{{City: "Helsinki"}, {City: "Prague"}, {City: "Vienna"}}}
	segResults := map[string]*segResult{
		"Helsinki→Prague": {flights: []models.RouteLeg{{Mode: "flight", Provider: "Finnair",
			Departure: "2026-11-20T06:00:00", Arrival: "2026-11-20T08:00:00", Duration: 120, Price: 90, Currency: "EUR"}}},
		"Prague→Vienna": {ground: []models.RouteLeg{{Mode: "train", Provider: "oebb", TransfersUnknown: true,
			Departure: "2026-11-20T10:21:00", Arrival: "2026-11-20T15:32:00", Duration: 311, Price: 30, Currency: "EUR"}}},
	}
	its := combineSegments(p, segResults, Options{MaxTransfers: 3})
	if len(its) != 1 || !its[0].TransfersUnknown {
		t.Fatalf("itineraries = %+v, want one flagged TransfersUnknown", its)
	}
}

// An unknown count cannot be shown to be no worse, so it neither dominates
// nor is dominated on transfers: both itineraries survive.
func TestParetoFilter_KeepsTransfersNotKnown(t *testing.T) {
	known := models.RouteItinerary{TotalPrice: 100, TotalDuration: 200, Transfers: 0}
	cheaperNotKnown := models.RouteItinerary{TotalPrice: 90, TotalDuration: 190, Transfers: 0, TransfersUnknown: true}
	if got := paretoFilter([]models.RouteItinerary{known, cheaperNotKnown}); len(got) != 2 {
		t.Errorf("paretoFilter kept %d, want both", len(got))
	}
}

func TestSortItineraries_ByTransfersPutsKnownFirst(t *testing.T) {
	its := []models.RouteItinerary{
		{TotalPrice: 50, Transfers: 0, TransfersUnknown: true},
		{TotalPrice: 80, Transfers: 1},
	}
	sortItineraries(its, Options{SortBy: "transfers"})
	if its[0].TransfersUnknown {
		t.Errorf("first itinerary's count is not known; want the known one first")
	}
}
