package main

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

var tablePartialStatuses = []models.ProviderStatus{
	{ID: "kiwi", Name: "Kiwi", Status: models.StatusTimeout, Error: "deadline exceeded"},
	{ID: "google_flights", Name: "Google Flights", Status: models.StatusOK},
}

func tableFlights(statuses []models.ProviderStatus) *models.FlightSearchResult {
	return &models.FlightSearchResult{Success: true, Count: 1, TripType: "one_way", ProviderStatuses: statuses,
		Flights: []models.FlightResult{{Price: 120, Currency: "EUR", Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}, Airline: "Finnair",
		}}}}}
}

// MIK-8088: a non-empty flight table from a partial search names the failed
// providers and does not present an unqualified cheapest.
func TestPrintFlightsTable_PartialCoverage(t *testing.T) {
	models.UseColor = false
	withTempHome(t)
	out := captureStdout(t, func() {
		_ = printFlightsTable(cancelledTestContext(t), "HEL", "AMS", "", tableFlights(tablePartialStatuses), false)
	})
	if !strings.Contains(out, "Partial coverage:") || !strings.Contains(out, "- Kiwi: ") {
		t.Errorf("table must name the failed provider:\n%s", out)
	}
	if !strings.Contains(out, "Cheapest found:") {
		t.Errorf("table must qualify the cheapest line:\n%s", out)
	}

	complete := captureStdout(t, func() {
		_ = printFlightsTable(cancelledTestContext(t), "HEL", "AMS", "", tableFlights(tablePartialStatuses[1:]), false)
	})
	if strings.Contains(complete, "Partial coverage") || !strings.Contains(complete, "Cheapest:") {
		t.Errorf("a complete search keeps the plain cheapest line and no note:\n%s", complete)
	}
}

// The cabin table marks a "no flights" cabin from a partial search and names
// each cabin's failed providers, even when other cabins answered.
func TestPrintCabinTable_PartialCoverage(t *testing.T) {
	models.UseColor = false
	results := []cabinResult{
		{Cabin: "Economy", Price: 120, Currency: "EUR", Airline: "Finnair", ProviderStatuses: tablePartialStatuses[1:]},
		{Cabin: "Business", Error: "no flights", ProviderStatuses: tablePartialStatuses},
		{Cabin: "Premium Economy", Price: 300, Currency: "EUR", Airline: "KLM", ProviderStatuses: tablePartialStatuses},
		{Cabin: "First", Error: "search failed: every provider timed out"},
	}
	out := captureStdout(t, func() { printCabinTable("HEL → AMS", "2026-11-01", results) })
	if !strings.Contains(out, "no flights (partial coverage)") {
		t.Errorf("a partial \"no flights\" cabin must say coverage was partial:\n%s", out)
	}
	if !strings.Contains(out, "Partial coverage (Business):") || !strings.Contains(out, "- Kiwi: ") {
		t.Errorf("cabin table must name the provider that failed for Business:\n%s", out)
	}
	if !strings.Contains(out, "Partial coverage (Premium Economy):") || !strings.Contains(out, "EUR 300") {
		t.Errorf("a priced cabin from a partial search must keep its price and carry a note:\n%s", out)
	}
	if !strings.Contains(out, "search failed: every provider timed out") {
		t.Errorf("an outright failure keeps its error:\n%s", out)
	}
	if strings.Contains(out, "Partial coverage (Economy)") {
		t.Errorf("a complete cabin must not carry a note:\n%s", out)
	}
}
