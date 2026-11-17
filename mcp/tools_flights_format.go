package mcp

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- Summary builders ---

func flightSummary(result *models.FlightSearchResult, origin, dest string) string {
	if !result.Success || result.Count == 0 {
		if result.Error != "" {
			return fmt.Sprintf("Flight search from %s to %s failed: %s", origin, dest, result.Error)
		}
		// Evidence guard (MIK-4950): only claim a definitive "no flights"
		// when every relevant provider was actually reached. If some timed
		// out or failed, absence is not established — say so instead of lying.
		if !result.Completeness.MayClaimExhaustive() {
			return fmt.Sprintf("No flights returned from %s to %s yet. %s",
				origin, dest, result.Completeness.IncompleteNote())
		}
		return fmt.Sprintf("No flights found from %s to %s.", origin, dest)
	}

	summary := fmt.Sprintf("Found %d flights from %s to %s.", result.Count, origin, dest)
	if note := result.Completeness.IncompleteNote(); note != "" {
		summary += " " + note
	}

	// Find cheapest (the same picks a budget-cut reply keeps).
	cheapestAt, nonstopAt := flightSummaryPicks(result.Flights)
	cheapest := result.Flights[cheapestAt]
	if cheapest.Price > 0 {
		stopStr := "nonstop"
		if cheapest.Stops == 1 {
			stopStr = "1 stop"
		} else if cheapest.Stops > 1 {
			stopStr = fmt.Sprintf("%d stops", cheapest.Stops)
		}
		airline := ""
		if len(cheapest.Legs) > 0 {
			airline = cheapest.Legs[0].Airline
		}
		if airline == "" && cheapest.Provider != "" {
			airline = flightProviderSummaryLabel(cheapest.Provider)
		}
		label := "Cheapest"
		if cheapest.HasStalePrice() {
			label = "Lowest seen (price may have changed)"
		}
		summary += fmt.Sprintf(" %s: %s%.0f (%s, %s).",
			label, cheapest.Currency, cheapest.Price, airline, stopStr)
	}

	// Check for nonstop options.
	if nonstopAt >= 0 {
		cheapestNonstop := &result.Flights[nonstopAt]
		summary += fmt.Sprintf(" Nonstop options from %s%.0f.", cheapestNonstop.Currency, cheapestNonstop.Price)
	}

	selfConnectCount := 0
	for _, flight := range result.Flights {
		if flight.SelfConnect {
			selfConnectCount++
		}
	}
	if selfConnectCount > 0 {
		summary += fmt.Sprintf(" Includes %d Kiwi self-connect option%s with connection-risk warnings.",
			selfConnectCount, pluralSuffix(selfConnectCount))
	}

	return summary
}

func flightProviderSummaryLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google_flights":
		return "Google Flights"
	case "kiwi":
		return "Kiwi"
	default:
		return provider
	}
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- Suggestion builders ---

func flightSuggestions(result *models.FlightSearchResult, origin, dest, date string, opts flights.SearchOptions) []Suggestion {
	var suggestions []Suggestion

	if !result.Success || result.Count == 0 {
		return nil
	}

	// If searching one-way, suggest round-trip.
	if opts.ReturnDate == "" {
		suggestions = append(suggestions, Suggestion{
			Action:      "search_flights",
			Description: "Search round-trip for potentially lower fares",
			Params:      map[string]any{"origin": origin, "destination": dest, "departure_date": date, "return_date": "YYYY-MM-DD"},
		})
	}

	// If there are many stops, suggest nonstop filter.
	hasMultiStop := false
	for _, f := range result.Flights {
		if f.Stops >= 2 {
			hasMultiStop = true
			break
		}
	}
	if hasMultiStop && opts.MaxStops == 0 {
		suggestions = append(suggestions, Suggestion{
			Action:      "search_flights",
			Description: "Filter to nonstop flights only",
			Params:      map[string]any{"origin": origin, "destination": dest, "departure_date": date, "max_stops": "nonstop"},
		})
	}

	// If prices vary widely, suggest flexible dates.
	if result.Count >= 3 {
		minPrice := result.Flights[0].Price
		maxPrice := result.Flights[0].Price
		for _, f := range result.Flights[1:] {
			if f.Price > 0 && f.Price < minPrice {
				minPrice = f.Price
			}
			if f.Price > maxPrice {
				maxPrice = f.Price
			}
		}
		if maxPrice > 0 && minPrice > 0 && maxPrice > minPrice*2 {
			suggestions = append(suggestions, Suggestion{
				Action:      "search_dates",
				Description: "Find the cheapest departure date this month",
				Params:      map[string]any{"origin": origin, "destination": dest},
			})
		}
	}

	// If economy, suggest checking business class.
	if opts.CabinClass == 0 || opts.CabinClass == models.Economy {
		suggestions = append(suggestions, Suggestion{
			Action:      "search_flights",
			Description: "Check business class availability",
			Params:      map[string]any{"origin": origin, "destination": dest, "departure_date": date, "cabin_class": "business"},
		})
	}

	// Proactively surface the door-to-door transfer (MIK-5734 A.1): once a
	// flight is found, the next question is always "how do I get from the
	// airport to where I'm staying, and when do I leave home?". Offer both the
	// arrival-side transfer comparison and the departure-side leave-by schedule.
	if dest != "" {
		suggestions = append(suggestions, Suggestion{
			Action:      "search_airport_transfers",
			Description: fmt.Sprintf("Plan the airport transfer from %s to your accommodation (compare public transport, taxi, express)", dest),
			Params:      map[string]any{"airport_code": dest, "destination": "YOUR_HOTEL_OR_ADDRESS", "date": date},
		})
		suggestions = append(suggestions, Suggestion{
			Action:      "plan_journey",
			Description: fmt.Sprintf("Get a leave-by schedule for the %s departure (when to leave home for check-in + security)", origin),
			Params:      map[string]any{"airport_code": origin, "date": date, "departure_time": "HH:MM", "ground_minutes": 0, "ground_mode": "train"},
		})
	}

	return suggestions
}

// flightSummaryPicks returns the indices of the flights flightSummary names:
// the cheapest priced flight (index 0 when none is priced) and the cheapest
// nonstop (-1 when there is none). list must not be empty.
func flightSummaryPicks(list []models.FlightResult) (cheapest, nonstop int) {
	nonstop = -1
	for i, f := range list {
		if i > 0 && f.Price > 0 && f.Price < list[cheapest].Price {
			cheapest = i
		}
		if f.Stops == 0 && (nonstop < 0 || f.Price < list[nonstop].Price) {
			nonstop = i
		}
	}
	return cheapest, nonstop
}
