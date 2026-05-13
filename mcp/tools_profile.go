package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/profile"
)

// onboardProfileTool returns the MCP tool definition for onboard_profile.
func onboardProfileTool() ToolDef {
	return ToolDef{
		Name:  "onboard_profile",
		Title: "Onboard Traveller Profile",
		Description: `Returns questions for a progressive onboarding interview that builds the traveller's profile.

Call this tool to conduct a structured 4-phase interview:
- Phase 1 — Basics: home airport, travel frequency, companions, kids, loyalty memberships.
- Phase 2 — Travel Style: accommodation preferences, budget, transport modes, remote work, travel days.
- Phase 3 — Deep Patterns: favourite destinations, neighbourhoods, properties, food style, travel hacks, lounges.
- Phase 4 — Specifics: companion details, wishlist, avoidances, languages, travel motivation.

Questions already answerable from the existing profile are skipped automatically.
Ask questions conversationally — follow up on interesting answers and save responses using update_preferences or add_booking.`,
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"phase":   {Type: "integer", Description: "Onboarding phase: 1 (Basics), 2 (Travel Style), 3 (Deep Patterns), 4 (Specifics)."},
				"answers": {Type: "string", Description: "JSON object of answers collected so far, keyed by question key. Pass empty string or omit for first call."},
			},
			Required: []string{"phase"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"questions": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"key":      schemaString(),
						"text":     schemaString(),
						"type":     schemaString(),
						"options":  schemaStringArray(),
						"default":  schemaString(),
						"required": schemaBool(),
					},
				}),
				"llm_instructions": schemaString(),
				"phase_complete":   schemaBool(),
				"next_phase":       schemaInt(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Onboard Traveller Profile",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleOnboardProfile returns onboarding questions for the requested phase.
func handleOnboardProfile(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	return handleOnboardProfileWithPath(args, "")
}

// handleOnboardProfileWithPath is the testable core.
func handleOnboardProfileWithPath(args map[string]any, path string) ([]ContentBlock, interface{}, error) {
	phase := argInt(args, "phase", 1)
	if phase < 1 || phase > 4 {
		return nil, nil, fmt.Errorf("phase must be 1–4, got %d", phase)
	}

	answersRaw := argString(args, "answers")
	answers := map[string]string{}
	if answersRaw != "" {
		if err := json.Unmarshal([]byte(answersRaw), &answers); err != nil {
			return nil, nil, fmt.Errorf("parse answers JSON: %w", err)
		}
	}

	var (
		prof *profile.TravelProfile
		err  error
	)
	if path != "" {
		prof, err = profile.LoadFrom(path)
	} else {
		prof, err = profile.Load()
	}
	if err != nil {
		prof = &profile.TravelProfile{}
	}

	questions, instructions := profile.OnboardingQuestions(phase, prof, answers)

	phaseComplete := len(questions) == 0
	nextPhase := phase + 1
	if nextPhase > 4 {
		nextPhase = 4
	}

	result := map[string]interface{}{
		"questions":        questions,
		"llm_instructions": instructions,
		"phase_complete":   phaseComplete,
		"next_phase":       nextPhase,
	}

	var summary string
	if phaseComplete {
		summary = fmt.Sprintf("Phase %d complete — profile already covers these topics. Move to phase %d.", phase, nextPhase)
	} else {
		summary = fmt.Sprintf("Phase %d: ask the user these %d questions conversationally.", phase, len(questions))
	}

	content, buildErr := buildAnnotatedContentBlocks(summary, result)
	if buildErr != nil {
		return nil, nil, buildErr
	}
	return content, result, nil
}

// buildProfileTool returns the MCP tool definition for build_profile.
func buildProfileTool() ToolDef {
	return ToolDef{
		Name:  "build_profile",
		Title: "Build Travel Profile",
		Description: `Builds a traveller profile from booking history. The profile tracks patterns from what the user actually booked — airlines, routes, hotels, timing, and budget.

This is separate from preferences (what users want) — it's about patterns from what they did.

Sources:
- "email": Instruct the LLM to search Gmail for booking confirmations and parse them. Use the sampling capability if available, otherwise return instructions for the user.
- "manual": Return the current profile built from manually added bookings.
- (empty): Return the full profile from all sources.

The profile is stored at ~/.trvl/profile.json and rebuilt every time bookings are added.`,
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"source": {Type: "string", Description: "Source to build from: \"email\", \"manual\", or empty for all sources."},
			},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"total_trips":        schemaInt(),
				"total_flights":      schemaInt(),
				"total_hotel_nights": schemaInt(),
				"top_airlines": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"code": schemaString(), "name": schemaString(), "flights": schemaInt(),
					},
				}),
				"preferred_alliance":    schemaString(),
				"avg_flight_price":      schemaNum(),
				"avg_booking_lead_days": schemaInt(),
				"top_routes": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"from": schemaString(), "to": schemaString(), "count": schemaInt(), "avg_price": schemaNum(),
					},
				}),
				"top_destinations": schemaStringArray(),
				"home_detected":    schemaStringArray(),
				"top_hotel_chains": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": schemaString(), "nights": schemaInt(),
					},
				}),
				"avg_star_rating":  schemaNum(),
				"avg_nightly_rate": schemaNum(),
				"preferred_type":   schemaString(),
				"top_ground_modes": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"mode": schemaString(), "count": schemaInt(),
					},
				}),
				"avg_trip_length_days":     schemaNum(),
				"preferred_departure_days": schemaStringArray(),
				"seasonal_pattern":         schemaObject(),
				"avg_trip_cost":            schemaNum(),
				"budget_tier":              schemaString(),
				"generated_at":             schemaString(),
				"sources":                  schemaStringArray(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Build Travel Profile",
			ReadOnlyHint:   false,
			IdempotentHint: true,
		},
	}
}

// addBookingTool returns the MCP tool definition for add_booking.
func addBookingTool() ToolDef {
	return ToolDef{
		Name:  "add_booking",
		Title: "Add Booking to Profile",
		Description: `Adds a booking record to the traveller's profile. The profile stats are automatically rebuilt after adding.

Use this when the user tells you about a past booking, or when you extract booking data from emails.
Always confirm the details with the user before adding.`,
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"type":        {Type: "string", Description: "Booking type: \"flight\", \"hotel\", \"ground\", \"airbnb\", \"hostel\"."},
				"date":        {Type: "string", Description: "Booking date in YYYY-MM-DD format (when it was booked)."},
				"travel_date": {Type: "string", Description: "Actual travel/check-in date in YYYY-MM-DD format."},
				"from":        {Type: "string", Description: "Origin IATA code or city name (for flights/ground)."},
				"to":          {Type: "string", Description: "Destination IATA code or city name."},
				"provider":    {Type: "string", Description: "Airline, hotel chain, or transport company name."},
				"price":       {Type: "number", Description: "Total price paid."},
				"currency":    {Type: "string", Description: "ISO 4217 currency code (e.g. EUR, USD)."},
				"nights":      {Type: "integer", Description: "Number of nights (for accommodation)."},
				"stars":       {Type: "integer", Description: "Hotel star rating (1-5)."},
				"source":      {Type: "string", Description: "Where this data came from: \"email\", \"booking.com\", \"manual\", etc."},
				"reference":   {Type: "string", Description: "Booking reference/confirmation number."},
				"notes":       {Type: "string", Description: "Free-text notes."},
			},
			Required: []string{"type", "provider"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"total_trips":        schemaInt(),
				"total_flights":      schemaInt(),
				"total_hotel_nights": schemaInt(),
				"budget_tier":        schemaString(),
				"bookings": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type": schemaString(), "provider": schemaString(),
						"price": schemaNum(), "currency": schemaString(),
					},
				}),
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Add Booking to Profile",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  false,
		},
	}
}

// interviewTripTool returns the MCP tool definition for interview_trip.
func interviewTripTool() ToolDef {
	return ToolDef{
		Name:  "interview_trip",
		Title: "Pre-Search Trip Interview",
		Description: `Returns a minimal set of questions to ask the user before searching, based on what's already known from their profile and preferences. Questions that can be answered from existing data are skipped.

Call this before your first search to gather trip context. The user's answers should inform search parameters (budget, dates flexibility, accommodation type, etc.).

The returned questions are in priority order — ask the most important ones first and skip any the user doesn't want to answer.`,
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"questions": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"key":      schemaString(),
						"text":     schemaString(),
						"type":     schemaString(),
						"options":  schemaStringArray(),
						"default":  schemaString(),
						"required": schemaBool(),
					},
				}),
				"profile_summary": schemaString(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Pre-Search Trip Interview",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleBuildProfile loads or builds the travel profile.
func handleBuildProfile(_ context.Context, args map[string]any, _ ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	return handleBuildProfileWithPath(args, "", sampling, progress)
}

// handleBuildProfileWithPath is the testable core.
func handleBuildProfileWithPath(args map[string]any, path string, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	source := argString(args, "source")

	var (
		p   *profile.TravelProfile
		err error
	)
	if path != "" {
		p, err = profile.LoadFrom(path)
	} else {
		p, err = profile.Load()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load profile: %w", err)
	}

	switch source {
	case "email":
		// When sampling is available, the LLM can process emails directly.
		// Return instructions for the AI to search Gmail and parse results.
		instructions := `To build a profile from email, search Gmail for booking confirmations:

1. Search: from:(booking.com OR airbnb.com OR finnair OR klm OR ryanair OR norwegian OR sas OR easyjet OR flixbus OR eurostar OR hotels.com OR expedia) subject:(confirmed OR confirmation OR booking OR ticket OR itinerary OR e-ticket OR reservation)
2. For each email found, extract: type (flight/hotel/ground/airbnb), travel date, route, provider, price, currency, nights (for hotels)
3. Call add_booking for each extracted booking
4. Call build_profile again to see the full analysis

The profile will be automatically rebuilt with updated stats after each booking is added.`

		if len(p.Bookings) > 0 {
			instructions += fmt.Sprintf("\n\nCurrent profile has %d bookings. New bookings will be added to the existing profile.", len(p.Bookings))
		}

		content, buildErr := buildAnnotatedContentBlocks(instructions, p)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		return content, p, nil

	case "manual", "":
		if len(p.Bookings) == 0 {
			summary := "No bookings recorded yet. Add bookings with the add_booking tool or use source=\"email\" to scan Gmail for booking confirmations."
			content, buildErr := buildAnnotatedContentBlocks(summary, p)
			if buildErr != nil {
				return nil, nil, buildErr
			}
			return content, p, nil
		}

		// Rebuild from bookings to ensure stats are current.
		rebuilt := profile.BuildProfile(p.Bookings)
		rebuilt.Sources = p.Sources

		summary := formatProfileSummary(rebuilt)
		content, buildErr := buildAnnotatedContentBlocks(summary, rebuilt)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		return content, rebuilt, nil

	default:
		return nil, nil, fmt.Errorf("unknown source %q: use \"email\", \"manual\", or empty", source)
	}
}

// handleAddBooking adds a booking to the profile.
func handleAddBooking(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	return handleAddBookingWithPath(args, "", progress)
}

// handleAddBookingWithPath is the testable core.
func handleAddBookingWithPath(args map[string]any, path string, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	b := profile.Booking{
		Type:       argString(args, "type"),
		Date:       argString(args, "date"),
		TravelDate: argString(args, "travel_date"),
		From:       argString(args, "from"),
		To:         argString(args, "to"),
		Provider:   argString(args, "provider"),
		Price:      argFloat(args, "price", 0),
		Currency:   argString(args, "currency"),
		Nights:     argInt(args, "nights", 0),
		Stars:      argInt(args, "stars", 0),
		Source:     argString(args, "source"),
		Reference:  argString(args, "reference"),
		Notes:      argString(args, "notes"),
	}

	if b.Type == "" {
		return nil, nil, fmt.Errorf("type is required (flight, hotel, ground, airbnb, hostel)")
	}
	if b.Provider == "" {
		return nil, nil, fmt.Errorf("provider is required")
	}
	if b.Source == "" {
		b.Source = "manual"
	}

	var err error
	if path != "" {
		err = profile.AddBookingTo(path, b)
	} else {
		err = profile.AddBooking(b)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("add booking: %w", err)
	}

	// Load the rebuilt profile to return.
	var p *profile.TravelProfile
	if path != "" {
		p, err = profile.LoadFrom(path)
	} else {
		p, err = profile.Load()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load updated profile: %w", err)
	}

	summary := fmt.Sprintf("Added %s booking: %s", b.Type, b.Provider)
	if b.From != "" && b.To != "" {
		summary += fmt.Sprintf(" (%s -> %s)", b.From, b.To)
	}
	if b.Price > 0 {
		summary += fmt.Sprintf(" %s %.0f", b.Currency, b.Price)
	}
	summary += fmt.Sprintf(". Profile now has %d bookings.", len(p.Bookings))

	content, buildErr := buildAnnotatedContentBlocks(summary, p)
	if buildErr != nil {
		return nil, nil, buildErr
	}
	return content, p, nil
}

// handleInterviewTrip returns pre-search interview questions.
func handleInterviewTrip(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	return handleInterviewTripWithPath(args, "", "", progress)
}

// handleInterviewTripWithPath is the testable core.
func handleInterviewTripWithPath(args map[string]any, profilePath, prefsPath string, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	var (
		prof  *profile.TravelProfile
		prefs *preferences.Preferences
		err   error
	)

	if profilePath != "" {
		prof, err = profile.LoadFrom(profilePath)
	} else {
		prof, err = profile.Load()
	}
	if err != nil {
		prof = &profile.TravelProfile{}
	}

	if prefsPath != "" {
		prefs, err = preferences.LoadFrom(prefsPath)
	} else {
		prefs, err = preferences.Load()
	}
	if err != nil {
		prefs = preferences.Default()
	}

	questions := profile.InterviewQuestions(prof, prefs)

	result := map[string]interface{}{
		"questions": questions,
	}

	// Add profile summary if available.
	if len(prof.Bookings) > 0 {
		result["profile_summary"] = formatProfileSummary(prof)
	} else {
		result["profile_summary"] = "No booking history yet."
	}

	summary := fmt.Sprintf("Ask the user these %d questions before searching. Skip any they don't want to answer.", len(questions))
	content, buildErr := buildAnnotatedContentBlocks(summary, result)
	if buildErr != nil {
		return nil, nil, buildErr
	}
	return content, result, nil
}

// formatProfileSummary generates a human-readable profile summary.
func formatProfileSummary(p *profile.TravelProfile) string {
	if p == nil || len(p.Bookings) == 0 {
		return "No booking history."
	}

	s := fmt.Sprintf("Travel profile: %d trips, %d flights, %d hotel nights.", p.TotalTrips, p.TotalFlights, p.TotalHotelNights)

	if len(p.TopAirlines) > 0 {
		s += fmt.Sprintf(" Top airline: %s (%d flights).", p.TopAirlines[0].Name, p.TopAirlines[0].Flights)
		if p.TopAirlines[0].Name == "" {
			s = s[:len(s)-1] // remove trailing period if no name
			s += fmt.Sprintf(" [%s].", p.TopAirlines[0].Code)
		}
	}
	if p.PreferredAlliance != "" {
		s += fmt.Sprintf(" Alliance: %s.", p.PreferredAlliance)
	}
	if p.AvgFlightPrice > 0 {
		s += fmt.Sprintf(" Avg flight: %.0f.", p.AvgFlightPrice)
	}
	if len(p.HomeDetected) > 0 {
		s += fmt.Sprintf(" Home airport(s): %s.", joinStrs(p.HomeDetected))
	}
	if p.BudgetTier != "" {
		s += fmt.Sprintf(" Budget tier: %s.", p.BudgetTier)
	}
	if p.AvgNightlyRate > 0 {
		s += fmt.Sprintf(" Avg hotel rate: %.0f/night.", p.AvgNightlyRate)
	}

	return s
}

// joinStrs joins strings with ", ".
func joinStrs(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += ", " + s
	}
	return result
}

// parseBookingFromJSON parses a JSON object (from LLM sampling response) into a Booking.
func parseBookingFromJSON(data []byte) (*profile.Booking, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse booking JSON: %w", err)
	}

	// Check if it's not a booking.
	if typ, ok := raw["type"].(string); ok && typ == "not_booking" {
		return nil, nil
	}

	b := &profile.Booking{}
	if v, ok := raw["type"].(string); ok {
		b.Type = v
	}
	if v, ok := raw["date"].(string); ok {
		b.Date = v
	}
	if v, ok := raw["travel_date"].(string); ok {
		b.TravelDate = v
	}
	// Also accept "checkin" as travel_date for hotels.
	if b.TravelDate == "" {
		if v, ok := raw["checkin"].(string); ok {
			b.TravelDate = v
		}
	}
	if v, ok := raw["from"].(string); ok {
		b.From = v
	}
	if v, ok := raw["to"].(string); ok {
		b.To = v
	}
	if v, ok := raw["provider"].(string); ok {
		b.Provider = v
	}
	// Also accept "hotel_name" as provider for hotels.
	if b.Provider == "" {
		if v, ok := raw["hotel_name"].(string); ok {
			b.Provider = v
		}
	}
	if v, ok := raw["price"].(float64); ok {
		b.Price = v
	}
	if v, ok := raw["currency"].(string); ok {
		b.Currency = v
	}
	if v, ok := raw["nights"].(float64); ok {
		b.Nights = int(v)
	}
	if v, ok := raw["stars"].(float64); ok {
		b.Stars = int(v)
	}
	if v, ok := raw["reference"].(string); ok {
		b.Reference = v
	}
	if v, ok := raw["notes"].(string); ok {
		b.Notes = v
	}
	b.Source = "email"

	return b, nil
}
