package multimodal

import (
	"context"
	"testing"
)

// #824: the production planner attaches no travel-hack saving. The flight
// detectors measure their savings on flight fares, so subtracting one from an
// itinerary total (here a EUR 40 train-and-bus trip with no flight at all)
// quoted a price nobody sells. The first check fails on main, where NewPlanner
// wires the flight annotator, before anything could reach a provider.
func TestNewPlanner_AttachesNoSavingToGroundOnlyTrip(t *testing.T) {
	p := NewPlanner(false)
	if p.Hacks != nil {
		t.Fatal("NewPlanner wires a savings annotator (#824): it measures flight savings and subtracts them from itinerary totals")
	}

	p.Discover = discoverFixed(route2("train", "Brno", "bus", "Prague", "Vienna", 40, "EUR"))
	p.Price = fakePricer{
		"train": {Price: 25, Currency: "EUR"},
		"bus":   {Price: 15, Currency: "EUR"},
	}.price
	plan, err := p.Plan(context.Background(), "Prague", "Vienna", "2027-07-15", "EUR")
	if err != nil || plan == nil || len(plan.Itineraries) == 0 {
		t.Fatalf("plan = %+v, err = %v; want one itinerary", plan, err)
	}
	if s := plan.Itineraries[0].HackSaving; s != nil {
		t.Errorf("ground-only itinerary carries a saving: %+v", s)
	}
}
