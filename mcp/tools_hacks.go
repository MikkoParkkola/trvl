package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/hacks"
)

// detectTravelHacksTool returns the MCP tool definition for hack detection.
func detectTravelHacksTool() ToolDef {
	return ToolDef{
		Name:        "detect_travel_hacks",
		Title:       "Detect Travel Optimization Hacks",
		Description: "Automatically detect money-saving travel hacks for a route: throwaway ticketing, hidden city, positioning flights, split ticketing, overnight transport (saved hotel night), airline stopover programs, and date flexibility.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"origin":      {Type: "string", Description: "Origin IATA airport code (e.g. HEL)"},
				"destination": {Type: "string", Description: "Destination IATA airport code (e.g. PRG)"},
				"date":        {Type: "string", Description: "Departure date (YYYY-MM-DD)"},
				"return_date": {Type: "string", Description: "Return date for round-trip analysis (YYYY-MM-DD); enables split and throwaway checks"},
				"currency":    {Type: "string", Description: "Display currency (default: EUR)"},
				"carry_on":    {Type: "boolean", Description: "Carry-on only trip — enables hidden city suggestions"},
				"naive_price": {Type: "number", Description: "Known baseline one-way price for comparison (optional)"},
				"passengers":  {Type: "integer", Description: "Number of passengers (group split hack fires at 3+)"},
			},
			Required: []string{"origin", "destination", "date"},
		},
		OutputSchema: hacksOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Detect Travel Optimization Hacks",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func hacksOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"origin":      schemaString(),
			"destination": schemaString(),
			"date":        schemaString(),
			"count":       schemaInt(),
			"hacks": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        schemaString(),
					"title":       schemaString(),
					"description": schemaString(),
					"savings":     schemaNum(),
					"currency":    schemaString(),
					"risks":       schemaStringArray(),
					"steps":       schemaStringArray(),
					"citations":   schemaStringArray(),
				},
			}),
		},
		"required": []string{"origin", "destination", "date", "count", "hacks"},
	}
}

// detectorNames lists the 20 parallel hack detectors for progress reporting.
var detectorNames = []string{
	"throwaway ticketing", "hidden city", "positioning flights",
	"split ticketing", "night transport", "airline stopovers",
	"date flexibility", "open jaw", "ferry positioning",
	"multi-stop routing", "currency arbitrage", "calendar conflicts",
	"Tuesday booking", "low-cost carriers",
	"multimodal skip flight", "multimodal positioning",
	"multimodal open jaw ground", "multimodal return split",
	"advance purchase", "group split",
}

func handleDetectTravelHacks(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	origin := strings.ToUpper(argString(args, "origin"))
	destination := strings.ToUpper(argString(args, "destination"))
	date := argString(args, "date")
	returnDate := argString(args, "return_date")
	currency := argString(args, "currency")
	if currency == "" {
		currency = "EUR"
	}
	carryOn := argBool(args, "carry_on", false)
	naivePrice := argFloat(args, "naive_price", 0)
	passengers := argInt(args, "passengers", 1)

	sendProgress(progress, 0, 100, fmt.Sprintf("Analysing %s→%s for travel hacks...", origin, destination))

	input := hacks.DetectorInput{
		Origin:      origin,
		Destination: destination,
		Date:        date,
		ReturnDate:  returnDate,
		Currency:    currency,
		CarryOnOnly: carryOn,
		NaivePrice:  naivePrice,
		Passengers:  passengers,
	}

	// Emit progress for each detector group (detectors run in parallel internally).
	n := float64(len(detectorNames))
	for i, name := range detectorNames {
		sendProgress(progress, float64(i+1)/n*80, 100, fmt.Sprintf("Checking %s...", name))
	}

	detected := hacks.DetectAll(ctx, input)

	sendProgress(progress, 90, 100, "Scoring and filtering results...")

	type response struct {
		Origin      string       `json:"origin"`
		Destination string       `json:"destination"`
		Date        string       `json:"date"`
		Count       int          `json:"count"`
		Hacks       []hacks.Hack `json:"hacks"`
	}

	resp := response{
		Origin:      origin,
		Destination: destination,
		Date:        date,
		Count:       len(detected),
		Hacks:       detected,
	}
	if resp.Hacks == nil {
		resp.Hacks = []hacks.Hack{}
	}

	sendProgress(progress, 100, 100, fmt.Sprintf("Found %d hacks", len(detected)))

	summary := buildHacksSummary(origin, destination, date, detected)
	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured hack data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}
	return content, resp, nil
}

func buildHacksSummary(origin, destination, date string, detected []hacks.Hack) string {
	if len(detected) == 0 {
		return "No travel hacks detected for " + origin + "→" + destination + " on " + date + "."
	}
	var sb strings.Builder
	sb.WriteString("Travel hacks for " + origin + "→" + destination + " on " + date + ":\n\n")
	for i, h := range detected {
		_, _ = fmt.Fprintf(&sb, "%d. %s", i+1, h.Title)
		if h.Savings > 0 {
			_, _ = fmt.Fprintf(&sb, " — saves %s %.0f", h.Currency, h.Savings)
		}
		sb.WriteString("\n")
		sb.WriteString("   " + h.Description + "\n\n")
	}
	return sb.String()
}

// detectAccommodationHacksTool returns the MCP tool definition for accommodation
// split detection.
func detectAccommodationHacksTool() ToolDef {
	return ToolDef{
		Name:        "detect_accommodation_hacks",
		Title:       "Detect Accommodation Split Opportunities",
		Description: "Find cheaper hotel stays by splitting a long booking across 2-3 properties. Accounts for moving costs (EUR 15/move) and only reports splits saving at least EUR 50 and 15% vs a single booking.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"city":       {Type: "string", Description: "City name (e.g. Prague, Amsterdam)"},
				"checkin":    {Type: "string", Description: "Check-in date (YYYY-MM-DD)"},
				"checkout":   {Type: "string", Description: "Check-out date (YYYY-MM-DD)"},
				"currency":   {Type: "string", Description: "Display currency (default: EUR)"},
				"max_splits": {Type: "integer", Description: "Maximum properties to split across, 2 or 3 (default: 3)"},
				"guests":     {Type: "integer", Description: "Number of guests (default: 2)"},
			},
			Required: []string{"city", "checkin", "checkout"},
		},
		OutputSchema: accommodationHacksOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Detect Accommodation Split Opportunities",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func accommodationHacksOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"city":     schemaString(),
			"checkin":  schemaString(),
			"checkout": schemaString(),
			"count":    schemaInt(),
			"hacks": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        schemaString(),
					"title":       schemaString(),
					"description": schemaString(),
					"savings":     schemaNum(),
					"currency":    schemaString(),
					"risks":       schemaStringArray(),
					"steps":       schemaStringArray(),
					"citations":   schemaStringArray(),
				},
			}),
		},
		"required": []string{"city", "checkin", "checkout", "count", "hacks"},
	}
}

func handleDetectAccommodationHacks(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	city := argString(args, "city")
	checkin := argString(args, "checkin")
	checkout := argString(args, "checkout")
	currency := argString(args, "currency")
	if currency == "" {
		currency = "EUR"
	}
	maxSplits := argInt(args, "max_splits", 3)
	guests := argInt(args, "guests", 2)

	in := hacks.AccommodationSplitInput{
		City:      city,
		CheckIn:   checkin,
		CheckOut:  checkout,
		Currency:  currency,
		MaxSplits: maxSplits,
		Guests:    guests,
	}

	detected := hacks.DetectAccommodationSplit(ctx, in)

	type response struct {
		City     string       `json:"city"`
		CheckIn  string       `json:"checkin"`
		CheckOut string       `json:"checkout"`
		Count    int          `json:"count"`
		Hacks    []hacks.Hack `json:"hacks"`
	}

	resp := response{
		City:     city,
		CheckIn:  checkin,
		CheckOut: checkout,
		Count:    len(detected),
		Hacks:    detected,
	}
	if resp.Hacks == nil {
		resp.Hacks = []hacks.Hack{}
	}

	summary := buildAccomHacksSummary(city, checkin, checkout, detected)
	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured accommodation hack data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}
	return content, resp, nil
}

func buildAccomHacksSummary(city, checkin, checkout string, detected []hacks.Hack) string {
	if len(detected) == 0 {
		return fmt.Sprintf("No accommodation split opportunities found for %s (%s to %s).", city, checkin, checkout)
	}
	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Accommodation hacks for %s (%s to %s):\n\n", city, checkin, checkout)
	for i, h := range detected {
		_, _ = fmt.Fprintf(&sb, "%d. %s", i+1, h.Title)
		if h.Savings > 0 {
			_, _ = fmt.Fprintf(&sb, " — saves %s %.0f", h.Currency, h.Savings)
		}
		sb.WriteString("\n")
		sb.WriteString("   " + h.Description + "\n\n")
	}
	return sb.String()
}
