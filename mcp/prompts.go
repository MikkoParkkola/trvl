package mcp

import "fmt"

// registerPrompts adds all prompt definitions to the server.
func registerPrompts(s *Server) {
	s.prompts = []PromptDef{
		{
			Name:        "plan-trip",
			Description: "Plan a complete trip with flights and hotels",
			Arguments: []PromptArgument{
				{Name: "origin", Description: "Departure airport IATA code (e.g., HEL)", Required: true},
				{Name: "destination", Description: "Destination city or airport code (e.g., Tokyo or NRT)", Required: true},
				{Name: "departure_date", Description: "Departure date in YYYY-MM-DD format", Required: true},
				{Name: "return_date", Description: "Return date in YYYY-MM-DD format", Required: true},
				{Name: "budget", Description: "Total budget in USD (e.g., 3000)", Required: false},
			},
		},
		{
			Name:        "find-cheapest-dates",
			Description: "Find the cheapest dates to fly between two cities",
			Arguments: []PromptArgument{
				{Name: "origin", Description: "Departure airport IATA code (e.g., HEL)", Required: true},
				{Name: "destination", Description: "Destination airport IATA code (e.g., NRT)", Required: true},
				{Name: "month", Description: "Month to search (e.g., june-2026 or 2026-06)", Required: true},
			},
		},
		{
			Name:        "compare-hotels",
			Description: "Compare hotels in a destination",
			Arguments: []PromptArgument{
				{Name: "location", Description: "Destination city or area (e.g., Shibuya Tokyo)", Required: true},
				{Name: "check_in", Description: "Check-in date in YYYY-MM-DD format", Required: true},
				{Name: "check_out", Description: "Check-out date in YYYY-MM-DD format", Required: true},
				{Name: "priorities", Description: "Comma-separated priorities: price, rating, location (e.g., price,rating)", Required: false},
			},
		},
		{
			Name:        "where-should-i-go",
			Description: "Discover the best travel destinations from your city within a budget",
			Arguments: []PromptArgument{
				{Name: "origin", Description: "Departure airport IATA code (e.g., HEL, JFK)", Required: true},
				{Name: "month", Description: "Travel month (e.g., july-2026 or 2026-07)", Required: false},
				{Name: "budget", Description: "Maximum flight budget in local currency (e.g., 500)", Required: false},
			},
		},
		{
			Name:        "packing-list",
			Description: "Generate a smart packing list tailored to your destination, dates, and activities",
			Arguments: []PromptArgument{
				{Name: "destination", Description: "Destination city or country (e.g., Tokyo, Iceland)", Required: true},
				{Name: "dates", Description: "Trip dates (e.g., 2026-06-15 to 2026-06-22)", Required: true},
				{Name: "trip_type", Description: "Trip type: business, beach, adventure, city break (default: leisure)", Required: false},
				{Name: "activities", Description: "Planned activities, comma-separated (e.g., hiking, swimming, formal dinner)", Required: false},
			},
		},
		{
			Name:        "setup_profile",
			Description: "Guide the user through setting up their travel preference profile",
			Arguments:   []PromptArgument{},
		},
		{
			Name:        "setup_providers",
			Description: "Guide the user through enabling additional hotel, transport, and restaurant search providers",
			Arguments:   []PromptArgument{},
		},
	}
}

// getPrompt generates a prompt by name and arguments.
func getPrompt(name string, args map[string]any) (*PromptsGetResult, error) {
	switch name {
	case "plan-trip":
		return promptPlanTrip(args)
	case "find-cheapest-dates":
		return promptFindCheapestDates(args)
	case "compare-hotels":
		return promptCompareHotels(args)
	case "where-should-i-go":
		return promptWhereShouldIGo(args)
	case "packing-list":
		return promptPackingList(args)
	case "setup_profile":
		return promptSetupProfile()
	case "setup_providers":
		return promptSetupProviders()
	default:
		return nil, fmt.Errorf("unknown prompt: %s", name)
	}
}

func argOr(args map[string]any, key, fallback string) string {
	if args == nil {
		return fallback
	}
	v, ok := args[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return fallback
	}
	return s
}

func promptPlanTrip(args map[string]any) (*PromptsGetResult, error) {
	origin := argOr(args, "origin", "")
	dest := argOr(args, "destination", "")
	depart := argOr(args, "departure_date", "")
	ret := argOr(args, "return_date", "")
	budget := argOr(args, "budget", "")

	if origin == "" || dest == "" || depart == "" || ret == "" {
		return nil, fmt.Errorf("origin, destination, departure_date, and return_date are required")
	}

	budgetLine := ""
	if budget != "" {
		budgetLine = fmt.Sprintf("\n\nThe total budget is $%s USD. Prioritize options that fit within this budget and flag any that exceed it.", budget)
	}

	prompt := fmt.Sprintf(`Plan a complete trip from %s to %s, departing %s and returning %s.%s

Follow these steps:

1. **Search flights**: Use search_flights to find outbound flights from %s to %s on %s with return_date %s. Look at the top 5 options by price and note airlines, stops, duration, and price.

2. **Search hotels**: Use search_hotels to find hotels in %s from %s to %s. Note the name, price per night, rating, and star level for the top options.

3. **Compare options**: Create a comparison table with:
   - Flight options (price, airline, duration, stops)
   - Hotel options (price/night, total cost, rating, stars)
   - Total trip cost for each combination

4. **Recommend**: Suggest the best value combination considering price, convenience, and quality. If there are nonstop flight options, highlight them even if slightly more expensive.

5. **Suggest alternatives**: Mention if flexible dates could save money (use search_dates if the price seems high) and if upgrading cabin class or hotel tier is worth considering.`,
		origin, dest, depart, ret, budgetLine,
		origin, dest, depart, ret,
		dest, depart, ret)

	return &PromptsGetResult{
		Description: fmt.Sprintf("Trip plan: %s to %s, %s - %s", origin, dest, depart, ret),
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}

func promptFindCheapestDates(args map[string]any) (*PromptsGetResult, error) {
	origin := argOr(args, "origin", "")
	dest := argOr(args, "destination", "")
	month := argOr(args, "month", "")

	if origin == "" || dest == "" || month == "" {
		return nil, fmt.Errorf("origin, destination, and month are required")
	}

	prompt := fmt.Sprintf(`Find the cheapest dates to fly from %s to %s in %s.

Follow these steps:

1. **Search the full month**: Use search_dates with origin=%s, destination=%s, and the appropriate start_date and end_date for %s. Set is_round_trip=true with trip_duration=7 for a typical week-long trip.

2. **Analyze results**: Identify:
   - The single cheapest departure date and its price
   - The cheapest week (7-day window)
   - Any patterns (e.g., midweek departures being cheaper, avoid holidays)
   - Price range across the month (cheapest vs most expensive)

3. **Present findings**: Create a summary with:
   - Top 3 cheapest dates with prices
   - A brief price calendar showing relative prices across the month
   - Recommendation for the best dates to book

4. **Follow up**: For the cheapest date, use search_flights to show the actual flight options available that day.`,
		origin, dest, month,
		origin, dest, month)

	return &PromptsGetResult{
		Description: fmt.Sprintf("Cheapest dates: %s to %s in %s", origin, dest, month),
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}

func promptCompareHotels(args map[string]any) (*PromptsGetResult, error) {
	location := argOr(args, "location", "")
	checkIn := argOr(args, "check_in", "")
	checkOut := argOr(args, "check_out", "")
	priorities := argOr(args, "priorities", "price,rating")

	if location == "" || checkIn == "" || checkOut == "" {
		return nil, fmt.Errorf("location, check_in, and check_out are required")
	}

	prompt := fmt.Sprintf(`Compare hotels in %s from %s to %s, prioritizing: %s.

Follow these steps:

1. **Search hotels**: Use search_hotels to find hotels in %s from %s to %s.

2. **Rank by priorities**: Re-rank the results according to the priorities: %s. Create a weighted ranking if multiple priorities are given.

3. **Get detailed pricing**: For the top 3 hotels, use hotel_prices with their hotel_id to compare booking provider prices. Note which provider offers the best deal for each.

4. **Create comparison table**:
   | Hotel | Stars | Rating | Price/Night | Best Provider | Total Cost |
   Include amenities and location details where available.

5. **Recommend**: Based on the priorities (%s), recommend the best hotel with reasoning. Mention any trade-offs (e.g., "Hotel X is $20/night more but rated 4.8 vs 4.2").

6. **Budget alternatives**: If the top picks are expensive, suggest filtering by a lower star rating or searching a nearby area.`,
		location, checkIn, checkOut, priorities,
		location, checkIn, checkOut,
		priorities,
		priorities)

	return &PromptsGetResult{
		Description: fmt.Sprintf("Hotel comparison: %s, %s to %s", location, checkIn, checkOut),
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}

func promptWhereShouldIGo(args map[string]any) (*PromptsGetResult, error) {
	origin := argOr(args, "origin", "")
	month := argOr(args, "month", "")
	budget := argOr(args, "budget", "")

	if origin == "" {
		return nil, fmt.Errorf("origin is required")
	}

	budgetLine := ""
	if budget != "" {
		budgetLine = fmt.Sprintf("\n\nMy flight budget is %s (local currency). Filter out destinations that exceed this budget.", budget)
	}

	monthLine := ""
	if month != "" {
		monthLine = fmt.Sprintf(" in %s", month)
	}

	destinationStep := fmt.Sprintf("Use search_deals with origin=%s to discover affordable destinations and their prices. Note the cheapest 10-15 options.", origin)
	if month != "" {
		destinationStep += fmt.Sprintf("\n   - Also run weekend_getaway with origin=%s and month=%s as a second pass for weekend-sized options.", origin, month)
	}

	prompt := fmt.Sprintf(`I want to travel from %s%s but I'm not sure where to go.%s

Follow these steps:

1. **Explore destinations**: %s

2. **Filter and rank**: From the results:
   - Filter by budget if specified
   - Group by region (Europe, Asia, Americas, etc.)
   - Highlight the top 3 cheapest destinations
   - Highlight any surprisingly affordable long-haul options

3. **Get destination details**: For the top 3 cheapest destinations, search for actual flights using search_flights to confirm availability and show specific options.

4. **Present recommendations**: Create a summary with:
   - A ranked list of top destinations with prices, airlines, and stop counts
   - A "Best value" pick (cheapest with good connectivity)
   - A "Hidden gem" pick (surprisingly affordable or interesting destination)
   - A "Premium pick" (best destination if budget allows)

5. **Next steps**: Suggest searching hotels at the top pick, or checking flexible dates with search_dates for the recommended destination.`,
		origin, monthLine, budgetLine, destinationStep)

	desc := fmt.Sprintf("Destination discovery from %s", origin)
	if month != "" {
		desc += " in " + month
	}

	return &PromptsGetResult{
		Description: desc,
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}

func promptPackingList(args map[string]any) (*PromptsGetResult, error) {
	dest := argOr(args, "destination", "")
	dates := argOr(args, "dates", "")
	tripType := argOr(args, "trip_type", "leisure")
	activities := argOr(args, "activities", "")

	if dest == "" || dates == "" {
		return nil, fmt.Errorf("destination and dates are required")
	}

	activitiesLine := ""
	if activities != "" {
		activitiesLine = fmt.Sprintf("\n\nPlanned activities: %s. Include activity-specific gear for each (e.g., hiking boots, swimsuit, formal attire).", activities)
	}

	prompt := fmt.Sprintf(`Create a smart packing list for a %s trip to %s (%s).%s

Follow these steps:

1. **Check weather**: Use get_weather to look up the forecast for %s during %s. This determines clothing weight, rain gear, and sun protection needs.

2. **Calculate trip duration**: Parse the dates %s to determine the number of nights. Use this to calculate clothing quantities (e.g., underwear = nights + 1, tops = nights / 2 rounded up for re-wear).

3. **Check bag allowance**: Read the user's travel profile (trvl://preferences) to check if they prefer carry-on only or checked bags. Tailor the list accordingly:
   - **Carry-on only**: Strict minimalism — compression bags, multi-purpose items, travel-size toiletries. Flag anything that won't fit or pass security.
   - **Checked bag**: More flexibility, but still organized by category.

4. **Build the packing list** organized by category:

   **Documents & Tech**
   - Passport/ID, boarding passes, travel insurance, chargers, adapters (destination-specific plug type)

   **Clothing** (weather-appropriate for %s)
   - Base layers, outerwear, footwear — quantities based on trip length
   - Activity-specific clothing if applicable

   **Toiletries & Health**
   - Essentials, medications, sunscreen/insect repellent based on destination
   - Note any items to buy on arrival vs. pack

   **Day bag & Accessories**
   - Day pack, water bottle, travel pillow, packing cubes

5. **Smart suggestions**:
   - Flag items people commonly forget for %s trips (e.g., power adapter type, visa printout)
   - Suggest what NOT to pack (available cheaply at destination)
   - Note any destination-specific considerations (dress codes, cultural norms, altitude, etc.)

6. **Summary**: Present a final checklist with [ ] checkboxes, grouped by bag (carry-on vs personal item vs checked), with a total estimated weight.`,
		tripType, dest, dates, activitiesLine,
		dest, dates,
		dates,
		dest,
		tripType)

	return &PromptsGetResult{
		Description: fmt.Sprintf("Packing list: %s trip to %s, %s", tripType, dest, dates),
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}

func promptSetupProfile() (*PromptsGetResult, error) {
	prompt := `Help me set up my trvl travel profile so you can personalise all searches.

First, read the trvl://onboarding resource to get the full questionnaire and
check whether a profile already exists.

If the profile is COMPLETE: show me the current profile summary and ask if
anything needs updating.

If the profile is EMPTY OR MISSING: work through the six categories below,
one at a time. Ask one category, wait for my answers, then move on.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 1 — ESSENTIALS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Ask:
• "What airport do you usually fly from?" → home_airports (IATA codes)
• "What currency should prices be shown in?" → display_currency (ISO 4217)
• "What's your nationality?" → nationality (ISO 3166-1 alpha-2)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 2 — LOYALTY & STATUS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Ask:
• "Do you have any airline alliance status?
  E.g. Oneworld Sapphire, Star Alliance Gold, SkyTeam Elite Plus."
  → frequent_flyer_programs: [{"alliance": "oneworld", "tier": "sapphire", "airline_code": "AY"}]
  Valid alliances: oneworld | star_alliance | skyteam
  Oneworld tiers: ruby | sapphire | emerald
  Star Alliance tiers: silver | gold
  SkyTeam tiers: elite | elite_plus

• "Which frequent flyer programmes are you a member of?
  (Even without status. E.g. AY Plus, Flying Blue, Miles&More)"
  → loyalty_airlines: IATA codes e.g. ["AY","KL","LH"]

• "Do you have any lounge access cards?
  E.g. Priority Pass, Diners Club, DragonPass, Amex Platinum."
  → lounge_cards: card names e.g. ["Priority Pass","Diners Club"]

• "Any hotel loyalty programmes?
  E.g. Marriott Bonvoy, IHG One Rewards, Hilton Honors, World of Hyatt."
  → loyalty_hotels: programme names e.g. ["Marriott Bonvoy"]

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 3 — TRAVEL STYLE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Ask:
• "Carry-on only or checked bags?" → carry_on_only: true/false
• "Direct flights preferred, or connections fine?" → prefer_direct: true/false
• "Window, aisle, or no preference?" → seat_preference: "window"|"aisle"|"no_preference"
• "Are overnight red-eye flights OK?" → red_eye_ok: true/false
• "Any flights you won't take — earliest or latest departure time?"
  → flight_time_earliest: "HH:MM", flight_time_latest: "HH:MM"

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 4 — ACCOMMODATION
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Ask:
• "Minimum hotel star rating or review score?"
  → min_hotel_stars: 0-5, min_hotel_rating: e.g. 4.0
• "Hostels / dorms OK, or hotels only?" → no_dormitories: true/false
• "En-suite bathroom required?" → ensuite_only: true/false
• "Fast wifi needed for work?" → fast_wifi_needed: true/false

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 5 — BUDGET
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Ask:
• "Typical hotel budget per night (min–max)?"
  → budget_per_night_min, budget_per_night_max
• "Max one-way flight price you'd ever pay?"
  → budget_flight_max
• "Deal style — price (take the 6am connection), comfort (pay for convenience),
  or balanced?"
  → deal_tolerance: "price"|"comfort"|"balanced"

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 6 — CONTEXT (optional)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Ask:
• "Solo, couple, or family?" → default_companions: 0|1|2+
• "What kinds of trips? city_break|beach|adventure|business|remote_work"
  → trip_types: array
• "Languages you speak?" → languages: ISO 639-1 codes e.g. ["en","fi"]
• "Dream destinations on your bucket list?" → bucket_list: place names

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
SAVE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
After collecting all answers:
1. Show a plain-text summary of everything collected.
2. Ask: "Does this look right? Anything to change?"
3. On confirmation, call update_preferences with all fields at once.
4. Confirm: "Profile saved — I'll use these preferences for all your searches."
5. Ask: "Would you also like to set up additional search providers?
   trvl can search Booking.com, Airbnb, Hostelworld, and other services
   beyond Google Hotels. Each requires a quick one-time setup."
   If yes, proceed with the setup_providers flow.
`

	return &PromptsGetResult{
		Description: "Interactive setup for your trvl travel preference profile",
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}

func promptSetupProviders() (*PromptsGetResult, error) {
	prompt := `Help me enable additional search providers for trvl.

trvl searches Google Hotels and Google Flights by default. Additional providers
for hotels, transport and restaurants ship WITH THE BINARY as reviewed
definitions. Each is off until I enable it, and each asks me for consent.

## What changed in 1.21.0 — read this before doing anything

Provider definitions used to be JSON you generated and trvl wrote to
~/.trvl/providers/<id>.json, from where it was loaded and executed. That made a
file on disk into executable request-building instructions, so anything able to
write that directory could point trvl HTTP client wherever it liked (#538).

That path is gone. configure_provider now RETURNS AN ERROR. Do not try to
generate a provider config, and do not treat a refusal as something to retry or
work around — there is nothing to fix, the tool is closed on purpose.

## How to enable a provider

### Step 1: Discover
Call suggest_providers to see the catalog. Show providers grouped by category:
- Hotels and Accommodation
- Ground Transport
- Restaurants and Reviews

For each, show: name, description, and whether it is already enabled. Ask me
which ones I want.

### Step 2: Enable
Tell me to run:

    trvl providers enable <id>

I will be asked directly for consent, outside this conversation. Some providers
reach services whose Terms of Service restrict automated access, and that
consent is mine to give rather than yours to relay.

### Step 3: Verify
Call test_provider for each newly enabled provider, then provider_health to
show what is working. Report the result count.

### If a provider is not in the catalog
It cannot be added at runtime. The route is a reviewed source change:
a definition under internal/providers/definitions, contributed by pull request,
or carried in my own fork. Say so plainly rather than attempting a workaround.

### If an enabled provider stops working
Services change their APIs between trvl releases. Call provider_health for the
recorded reason and tell me; a fix needs a source change, not a config edit.

IMPORTANT NOTES:
- Definitions are reviewed before release. They are not generated by you, and
  you should not offer to write one.
- Older ~/.trvl/providers/*.json files may still exist. They are NOT loaded.
  They are kept so I can roll back or migrate by hand, and are safe to delete.
- All providers rate-limit themselves to be respectful of the target services.
`

	return &PromptsGetResult{
		Description: "Set up additional search providers for hotels, transport, and restaurants",
		Messages: []PromptMessage{
			{
				Role:    "user",
				Content: ContentBlock{Type: "text", Text: prompt},
			},
		},
	}, nil
}
