package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

// registerResources adds all static resource definitions to the server.
func registerResources(s *Server) {
	s.resources = []ResourceDef{
		{
			URI:         "trvl://onboarding",
			Name:        "Onboarding Guide",
			Description: "First-run setup guide. Read this on first use to build the user's preference profile.",
			MimeType:    "text/plain",
		},
		{
			URI:         "trvl://airports/popular",
			Name:        "Popular Airports",
			Description: "List of 50 popular airport codes with city names",
			MimeType:    "text/plain",
		},
		{
			URI:         "trvl://help/flights",
			Name:        "Flight Search Guide",
			Description: "Flight search usage guide with examples",
			MimeType:    "text/markdown",
		},
		{
			URI:         "trvl://help/hotels",
			Name:        "Hotel Search Guide",
			Description: "Hotel search usage guide with examples",
			MimeType:    "text/markdown",
		},
		{
			URI:         "trvl://trip/summary",
			Name:        "Trip Planning Summary",
			Description: "Accumulated summary of all searches in this session",
			MimeType:    "text/plain",
		},
		{
			URI:         "trvl://watches",
			Name:        "Price Watches",
			Description: "List all active price watches with current prices",
			MimeType:    "text/plain",
		},
		{
			URI:         "trvl://trips",
			Name:        "Trips",
			Description: "All active planned and booked trips",
			MimeType:    "application/json",
		},
		{
			URI:         "trvl://trips/upcoming",
			Name:        "Upcoming Trips",
			Description: "Next trip with countdown to departure",
			MimeType:    "text/plain",
		},
		{
			URI:         "trvl://trips/alerts",
			Name:        "Trip Alerts",
			Description: "Current trip monitoring alerts and reminders",
			MimeType:    "application/json",
		},
	}
}

// listResources returns the static resources plus any dynamic watch resources
// created from flight searches in the session.
func (s *Server) listResources() []ResourceDef {
	// Start with static resources.
	resources := make([]ResourceDef, len(s.resources))
	copy(resources, s.resources)

	// Add dynamic watch resources from searches.
	s.tripState.mu.Lock()
	seen := make(map[string]bool)
	for _, sr := range s.tripState.Searches {
		if sr.Type == "flight" {
			// Extract origin-dest-date from query like "HEL->BCN 2026-07-01".
			uri := watchURIFromQuery(sr.Query)
			if uri != "" && !seen[uri] {
				seen[uri] = true
				resources = append(resources, ResourceDef{
					URI:         uri,
					Name:        fmt.Sprintf("Price watch: %s", sr.Query),
					Description: "Re-fetch to check for price changes",
					MimeType:    "text/plain",
				})
			}
		}
	}
	s.tripState.mu.Unlock()

	// Add dynamic watch resources from the watch store.
	if s.watchStore != nil {
		for _, w := range s.watchStore.List() {
			route := fmt.Sprintf("%s -> %s", w.Origin, w.Destination)
			priceInfo := ""
			if w.LastPrice > 0 {
				priceInfo = fmt.Sprintf(" (%.0f %s)", w.LastPrice, w.Currency)
			}
			resources = append(resources, ResourceDef{
				URI:         fmt.Sprintf("trvl://watch/%s", w.ID),
				Name:        fmt.Sprintf("Watch: %s %s%s", w.Type, route, priceInfo),
				Description: fmt.Sprintf("Price watch for %s on %s", route, w.DepartDate),
				MimeType:    "text/plain",
			})
		}
	}

	// Add dynamic trip resources from the trip store (best-effort; errors are ignored).
	if tripStore, err := defaultTripStore(); err == nil {
		for _, t := range tripStore.Active() {
			desc := fmt.Sprintf("%s (%d legs)", t.Status, len(t.Legs))
			first := trips.FirstLegStart(t)
			if !first.IsZero() {
				desc += fmt.Sprintf(", departs %s", first.Format("2006-01-02"))
			}
			resources = append(resources, ResourceDef{
				URI:         fmt.Sprintf("trvl://trips/%s", t.ID),
				Name:        fmt.Sprintf("Trip: %s", t.Name),
				Description: desc,
				MimeType:    "application/json",
			})
		}
	}

	return resources
}

// watchURIFromQuery converts a query like "HEL->BCN 2026-07-01" to
// "trvl://watch/HEL-BCN-2026-07-01".
func watchURIFromQuery(query string) string {
	// Expected format: "HEL->BCN 2026-07-01" or "HEL->BCN 2026-07-01 (round-trip ...)"
	parts := strings.Fields(query)
	if len(parts) < 2 {
		return ""
	}
	route := parts[0] // "HEL->BCN"
	date := parts[1]  // "2026-07-01"

	routeParts := strings.SplitN(route, "->", 2)
	if len(routeParts) != 2 {
		return ""
	}
	origin := routeParts[0]
	dest := routeParts[1]

	return fmt.Sprintf("trvl://watch/%s-%s-%s", origin, dest, date)
}

// readResource returns the content for a resource URI, including dynamic resources.
func (s *Server) readResource(uri string) (*ResourcesReadResult, error) {
	switch {
	case uri == "trvl://onboarding":
		return readOnboarding()
	case uri == "trvl://airports/popular":
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      uri,
				MimeType: "text/plain",
				Text:     popularAirports,
			}},
		}, nil
	case uri == "trvl://help/flights":
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      uri,
				MimeType: "text/markdown",
				Text:     flightSearchGuide,
			}},
		}, nil
	case uri == "trvl://help/hotels":
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      uri,
				MimeType: "text/markdown",
				Text:     hotelSearchGuide,
			}},
		}, nil
	case uri == "trvl://trip/summary":
		return s.readTripSummary()
	case uri == "trvl://watches":
		return s.readWatchesList()
	case strings.HasPrefix(uri, "trvl://watch/"):
		return s.readWatchResource(uri)
	case uri == "trvl://trips":
		return s.readTripsList()
	case strings.HasPrefix(uri, "trvl://trips/") && uri != "trvl://trips/upcoming" && uri != "trvl://trips/alerts":
		return s.readTripByURI(uri)
	case uri == "trvl://trips/upcoming":
		return s.readTripsUpcoming()
	case uri == "trvl://trips/alerts":
		return s.readTripsAlerts()
	default:
		return nil, fmt.Errorf("resource not found: %s", uri)
	}
}

// readTripSummary returns a formatted summary of all searches in the session.
func (s *Server) readTripSummary() (*ResourcesReadResult, error) {
	s.tripState.mu.Lock()
	searches := make([]SearchRecord, len(s.tripState.Searches))
	copy(searches, s.tripState.Searches)
	s.tripState.mu.Unlock()

	if len(searches) == 0 {
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      "trvl://trip/summary",
				MimeType: "text/plain",
				Text:     "Trip Planning Session Summary\n\nNo searches yet. Use search_flights, search_hotels, or destination_info to start planning.",
			}},
		}, nil
	}

	// Count by type.
	counts := make(map[string]int)
	for _, sr := range searches {
		counts[sr.Type]++
	}

	var b strings.Builder
	b.WriteString("Trip Planning Session Summary\n")
	b.WriteString(strings.Repeat("=", 40))
	b.WriteString("\n")

	// Searched line.
	var parts []string
	if n := counts["flight"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d flight(s)", n))
	}
	if n := counts["hotel"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d hotel(s)", n))
	}
	if n := counts["destination"]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d destination(s)", n))
	}
	_, _ = fmt.Fprintf(&b, "Searched: %s\n\n", strings.Join(parts, ", "))

	// Individual searches.
	var totalCost float64
	var currency string
	for _, sr := range searches {
		icon := "  "
		switch sr.Type {
		case "flight":
			icon = ">> "
		case "hotel":
			icon = "## "
		case "destination":
			icon = "** "
		}
		if sr.BestPrice > 0 {
			_, _ = fmt.Fprintf(&b, "%s%s: cheapest %s %.0f\n", icon, sr.Query, sr.Currency, sr.BestPrice)
			totalCost += sr.BestPrice
			if currency == "" {
				currency = sr.Currency
			}
		} else {
			_, _ = fmt.Fprintf(&b, "%s%s\n", icon, sr.Query)
		}
	}

	if totalCost > 0 {
		_, _ = fmt.Fprintf(&b, "\nEstimated total: %s %.0f", currency, totalCost)
	}

	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      "trvl://trip/summary",
			MimeType: "text/plain",
			Text:     b.String(),
		}},
	}, nil
}

// readWatchesList returns a formatted list of all active watches.
func (s *Server) readWatchesList() (*ResourcesReadResult, error) {
	if s.watchStore == nil {
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      "trvl://watches",
				MimeType: "text/plain",
				Text:     "Price Watches\n\nNo watch store available.",
			}},
		}, nil
	}

	watches := s.watchStore.List()
	if len(watches) == 0 {
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      "trvl://watches",
				MimeType: "text/plain",
				Text:     "Price Watches\n\nNo active watches. Use the CLI to add watches: trvl watch add",
			}},
		}, nil
	}

	var b strings.Builder
	b.WriteString("Price Watches\n")
	b.WriteString(strings.Repeat("=", 40))
	_, _ = fmt.Fprintf(&b, "\n%d active watch(es)\n\n", len(watches))

	for _, w := range watches {
		route := fmt.Sprintf("%s -> %s", w.Origin, w.Destination)
		_, _ = fmt.Fprintf(&b, "[%s] %s  %s  %s", w.ID, w.Type, route, w.DepartDate)
		if w.ReturnDate != "" {
			_, _ = fmt.Fprintf(&b, " (return %s)", w.ReturnDate)
		}
		b.WriteString("\n")
		if w.LastPrice > 0 {
			_, _ = fmt.Fprintf(&b, "  Current: %.0f %s", w.LastPrice, w.Currency)
			if w.LowestPrice > 0 && w.LowestPrice < w.LastPrice {
				_, _ = fmt.Fprintf(&b, "  Lowest: %.0f", w.LowestPrice)
			}
			b.WriteString("\n")
		}
		if w.BelowPrice > 0 {
			_, _ = fmt.Fprintf(&b, "  Goal: below %.0f %s\n", w.BelowPrice, w.Currency)
		}
		if !w.LastCheck.IsZero() {
			_, _ = fmt.Fprintf(&b, "  Last checked: %s\n", w.LastCheck.Format("2006-01-02 15:04"))
		}
		b.WriteString("\n")
	}

	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      "trvl://watches",
			MimeType: "text/plain",
			Text:     b.String(),
		}},
	}, nil
}

// readWatchByID returns details and price history for a single watch.
func (s *Server) readWatchByID(id string) (*ResourcesReadResult, error) {
	if s.watchStore == nil {
		return nil, fmt.Errorf("watch store not available")
	}

	w, ok := s.watchStore.Get(id)
	if !ok {
		return nil, fmt.Errorf("watch %s not found", id)
	}

	uri := fmt.Sprintf("trvl://watch/%s", id)
	route := fmt.Sprintf("%s -> %s", w.Origin, w.Destination)

	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Watch: %s %s\n", w.Type, route)
	b.WriteString(strings.Repeat("=", 40))
	b.WriteString("\n")
	_, _ = fmt.Fprintf(&b, "ID:        %s\n", w.ID)
	_, _ = fmt.Fprintf(&b, "Type:      %s\n", w.Type)
	_, _ = fmt.Fprintf(&b, "Route:     %s\n", route)
	_, _ = fmt.Fprintf(&b, "Date:      %s\n", w.DepartDate)
	if w.ReturnDate != "" {
		_, _ = fmt.Fprintf(&b, "Return:    %s\n", w.ReturnDate)
	}
	if w.BelowPrice > 0 {
		_, _ = fmt.Fprintf(&b, "Goal:      below %.0f %s\n", w.BelowPrice, w.Currency)
	}
	if w.LastPrice > 0 {
		_, _ = fmt.Fprintf(&b, "Current:   %.0f %s\n", w.LastPrice, w.Currency)
	}
	if w.LowestPrice > 0 {
		_, _ = fmt.Fprintf(&b, "Lowest:    %.0f %s\n", w.LowestPrice, w.Currency)
	}
	if !w.LastCheck.IsZero() {
		_, _ = fmt.Fprintf(&b, "Checked:   %s\n", w.LastCheck.Format("2006-01-02 15:04"))
	}
	_, _ = fmt.Fprintf(&b, "Created:   %s\n", w.CreatedAt.Format("2006-01-02 15:04"))

	// Price history.
	history := s.watchStore.History(w.ID)
	if len(history) > 0 {
		_, _ = fmt.Fprintf(&b, "\nPrice History (%d points)\n", len(history))
		b.WriteString(strings.Repeat("-", 30))
		b.WriteString("\n")
		for _, p := range history {
			_, _ = fmt.Fprintf(&b, "  %s  %.0f %s\n",
				p.Timestamp.Format("2006-01-02 15:04"), p.Price, p.Currency)
		}
	}

	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      uri,
			MimeType: "text/plain",
			Text:     b.String(),
		}},
	}, nil
}

// readWatchResource handles trvl://watch/{id} URIs.
// First checks the watch store for an ID match, then falls back to
// the legacy trvl://watch/{origin}-{dest}-{date} flight price format.
func (s *Server) readWatchResource(uri string) (*ResourcesReadResult, error) {
	path := strings.TrimPrefix(uri, "trvl://watch/")

	// Try watch store lookup first (8-char hex IDs).
	if s.watchStore != nil {
		if _, ok := s.watchStore.Get(path); ok {
			return s.readWatchByID(path)
		}
	}

	// Legacy format: "trvl://watch/HEL-BCN-2026-07-01"
	parts := strings.SplitN(path, "-", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("invalid watch URI: %s (expected trvl://watch/ORIGIN-DEST-YYYY-MM-DD)", uri)
	}
	origin := strings.ToUpper(parts[0])
	dest := strings.ToUpper(parts[1])
	date := parts[2]

	// Run a quick search for the cheapest flight.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	opts := flights.SearchOptions{}
	result, err := flights.SearchFlights(ctx, origin, dest, date, opts)
	if err != nil {
		return nil, fmt.Errorf("watch search failed: %w", err)
	}

	cacheKey := fmt.Sprintf("%s-%s-%s", origin, dest, date)

	if !result.Success || result.Count == 0 {
		return &ResourcesReadResult{
			Contents: []ResourceContent{{
				URI:      uri,
				MimeType: "text/plain",
				Text:     fmt.Sprintf("No flights found for %s -> %s on %s.", origin, dest, date),
			}},
		}, nil
	}

	// Find cheapest.
	cheapest := result.Flights[0]
	for _, f := range result.Flights[1:] {
		if f.Price > 0 && f.Price < cheapest.Price {
			cheapest = f
		}
	}

	// Check delta from cached price.
	var text string
	if prev, ok := s.priceCache.get(cacheKey); ok {
		delta := cheapest.Price - prev
		direction := "unchanged"
		if delta > 0 {
			direction = fmt.Sprintf("up %.0f", delta)
		} else if delta < 0 {
			direction = fmt.Sprintf("down %.0f", -delta)
		}
		text = fmt.Sprintf("%s -> %s on %s: %s%.0f (%s from previous %s%.0f)",
			origin, dest, date, cheapest.Currency, cheapest.Price,
			direction, cheapest.Currency, prev)
	} else {
		text = fmt.Sprintf("%s -> %s on %s: %s%.0f (first check)",
			origin, dest, date, cheapest.Currency, cheapest.Price)
	}

	// Update cache.
	s.priceCache.set(cacheKey, cheapest.Price)

	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      uri,
			MimeType: "text/plain",
			Text:     text,
		}},
	}, nil
}

// readTripsList returns all active trips as JSON.
