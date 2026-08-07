package mcp

// schema_hotels.go holds the JSON Schema + input-property builders for the hotel/accommodation MCP tools, extracted from tools_hotels.go to keep that file focused on tool registration + orchestration (file hygiene: <=800 LOC).

// hotelSearchOutputSchema returns the JSON Schema for HotelSearchResult.
func hotelSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"count":   schemaInt(),
			"hotels": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":             schemaString(),
					"hotel_id":         schemaString(),
					"rating":           schemaNum(),
					"review_count":     schemaInt(),
					"stars":            schemaInt(),
					"price":            schemaNum(),
					"currency":         schemaString(),
					"address":          schemaString(),
					"lat":              schemaNum(),
					"lon":              schemaNum(),
					"property_type":    schemaStringDesc("Inferred lodging type: hotel, hostel, apartment, vacation_rental, resort, bnb, villa, or unknown."),
					"booking_url":      schemaString(),
					"amenities":        schemaStringArray(),
					"eco_certified":    schemaBool(),
					"price_basis":      schemaStringDesc("Basis for primary price: lead_in, room_nightly, room_total, or tax_inclusive_total."),
					"price_confidence": schemaStringDesc("Confidence for primary price: unverified, room_level, or verified."),
					"retrieved_at":     schemaStringDesc("Time trvl retrieved the primary price."),
					"freshness":        schemaStringDesc("Freshness class for primary price: live, recent, or stale."),
					"price_warnings":   schemaStringArray(),
					"savings":          schemaNumDesc("Price savings vs most expensive source"),
					"cheapest_source":  schemaStringDesc("Provider with lowest price"),
					"sources": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"provider":         schemaString(),
							"price":            schemaNum(),
							"max_price":        schemaNum(),
							"currency":         schemaString(),
							"room_count":       schemaInt(),
							"booking_url":      schemaString(),
							"price_basis":      schemaString(),
							"price_confidence": schemaString(),
							"retrieved_at":     schemaString(),
							"freshness":        schemaString(),
						},
					}),
				},
			}),
			"completeness": schemaObject(),
			"suggestions": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action":      schemaString(),
					"description": schemaString(),
					"params":      schemaObject(),
				},
			}),
			"provider_statuses": schemaArrayDesc("Per-provider outcome (Google Hotels / Trivago / Booking / Airbnb / Hostelworld / configured providers). Status may be checked_hit, checked_no_hit, timeout, failed, skipped, disabled, or circuit_broken.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":            schemaString(),
					"name":          schemaString(),
					"status":        schemaString(),
					"results":       schemaInt(),
					"error":         schemaString(),
					"fix_hint":      schemaString(),
					"fix_hint_code": schemaString(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

// hotelPricesOutputSchema returns the JSON Schema for HotelPriceResult.
func hotelPricesOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":   schemaBool(),
			"hotel_id":  schemaString(),
			"name":      schemaString(),
			"check_in":  schemaString(),
			"check_out": schemaString(),
			"providers": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"provider":              schemaString(),
					"price":                 schemaNum(),
					"currency":              schemaString(),
					"nightly_price":         schemaNumDesc("Nightly rate when the provider exposes it separately."),
					"total_price":           schemaNumDesc("Total stay price when the provider exposes it separately."),
					"provider_url":          schemaString(),
					"price_basis":           schemaStringDesc("lead_in, room_nightly, room_total, or tax_inclusive_total."),
					"price_confidence":      schemaStringDesc("unverified, room_level, or verified."),
					"official":              schemaBoolDesc("Present and true only when the upstream source explicitly marks this seller as the property's official site. Absence means not established, never 'not official'. Does not affect price ordering."),
					"link_durability":       schemaStringDesc("stable for a direct OTA link; expiring for a google.com aclk ad-click redirect lasting a day to two. Empty when absent. Dead vacation-rental redirects are stripped."),
					"tax_added_at_checkout": schemaBoolDesc("True when the shown total equals the pre-tax figure, so taxes and fees are added at checkout and the price will grow."),
					"free_cancellation":     schemaBoolDesc("Present and true only when the seller explicitly offers free cancellation; absence means unknown, not non-refundable."),
					"free_cancellation_until": schemaStringDesc(
						"Seller-stated free-cancellation deadline as received, when present.",
					),
				},
				"required": []string{"provider", "price", "currency"},
			}),
			"booking_fallback_url": schemaStringDesc("Durable Booking.com property+date deep-link that never 404s, used when a provider link expires."),
			"tourist_tax_note":     schemaStringDesc("Descriptive caveat that a local tourist or city tax may be payable in cash at the property and is not in any online total. Never a numeric estimate; never affects ranking."),
			"price_position": map[string]interface{}{
				"type":        "object",
				"description": "Where this stay's cheapest price sits in its own history (MIK-6229). Only assert a verdict when confident=true.",
				"properties": map[string]interface{}{
					"band":          schemaStringDesc("one of: low, typical, high (plus a not-confident marker when history is too sparse)"),
					"verdict":       schemaStringDesc("one of: buy, wait, neutral (plus a not-confident marker when history is too sparse)"),
					"current":       schemaNum(),
					"low":           schemaNum(),
					"high":          schemaNum(),
					"median":        schemaNum(),
					"vs_median_pct": schemaNum(),
					"observations":  schemaInt(),
					"confident":     schemaBool(),
				},
			},
			"booking_readiness":                 schemaStringDesc("Composite booking-readiness verdict (MIK-6232), one of: ready, caution, unverified. 'ready' requires verified price AND stable link AND confirmed identity AND known refundability; any missing signal downgrades it. Treat anything below ready as 'verify before booking' and tell the user why via booking_readiness_reasons."),
			"booking_readiness_reasons":         schemaStringArrayDesc("Which signals drove the readiness verdict down (e.g. 'refundability not known')."),
			"booking_readiness_ceiling":         schemaStringDesc("Present only when this response cannot reach 'ready' because no seller supplied cancellation terms. Seller refundability is carried when the provider exposes it, so this ceiling is conditional rather than permanent. IMPORTANT: it explains missing evidence only; booking_readiness_reasons may still report actionable findings such as an expiring link or an unverified price. Use hotel_rooms for richer room-level evidence."),
			"booking_readiness_ceiling_reasons": schemaStringArrayDesc("Which signals this endpoint cannot supply at all, as distinct from ones it looked for and did not find."),
			"notice":                            schemaString(),
			"error":                             schemaString(),
		},
		"required": []string{"success"},
	}
}

func hotelSearchInputProperties() map[string]Property {
	return map[string]Property{
		"location":            {Type: "string", Description: "Location name or address (e.g., Helsinki, Tokyo, Manhattan New York)"},
		"check_in":            {Type: "string", Description: "Check-in date in YYYY-MM-DD format"},
		"check_out":           {Type: "string", Description: "Check-out date in YYYY-MM-DD format"},
		"guests":              {Type: "integer", Description: "Number of guests (default: 2)"},
		"children_ages":       {Type: "array", Description: "Child ages for occupancy-aware room searches, e.g. [7,10]", Items: &Property{Type: "integer"}},
		"rooms":               {Type: "integer", Description: "Number of rooms or units requested (default: provider default)"},
		"currency":            {Type: "string", Description: "Currency code (e.g. USD, EUR). Defaults to display_currency preference, then USD"},
		"stars":               {Type: "integer", Description: "Minimum star rating 1-5 (default: no filter)"},
		"sort":                {Type: "string", Description: "Sort order: price, rating, distance, or stars (default: price)"},
		"min_price":           {Type: "number", Description: "Minimum price per night (default: no filter)"},
		"max_price":           {Type: "number", Description: "Maximum price per night (default: no filter)"},
		"min_rating":          {Type: "number", Description: "Minimum guest rating on 0-10 scale, e.g. 8.0 (default: no filter)"},
		"max_distance":        {Type: "number", Description: "Maximum distance from city center in km (default: no filter)"},
		"amenities":           {Type: "string", Description: "Filter by amenities (comma-separated, e.g. pool,wifi,breakfast)"},
		"enrich_amenities":    {Type: "boolean", Description: "Fetch detail pages for top results to get full amenity lists (slower, default: false)"},
		"enrich_rooms":        {Type: "boolean", Description: "Drill into the top results for real room-level prices (Google/Booking/SerpAPI/Agoda) instead of headline teasers; verified room rates lead the ranking. Bounded to the top 5 hotels and rate-limit aware. Default: true; set false for a faster headline-only search."},
		"free_cancellation":   {Type: "boolean", Description: "Only show hotels with free cancellation (default: false)"},
		"refundable_required": {Type: "boolean", Description: "Only treat refundable or free-cancellation room rates as matching the requested need (default: false)"},
		"property_type":       {Type: "string", Description: "Filter by property type: hotel, apartment, hostel, resort, bnb, or villa (default: no filter)"},
		"brand":               {Type: "string", Description: "Filter by hotel brand/chain name (case-insensitive substring match, e.g. hilton, marriott, ibis)"},
		"eco_certified":       {Type: "boolean", Description: "Only show eco-certified hotels with sustainability certifications (default: false)"},
		"min_bedrooms":        {Type: "integer", Description: "Minimum number of bedrooms (Airbnb, default: no filter)"},
		"min_bathrooms":       {Type: "integer", Description: "Minimum number of bathrooms (Airbnb, default: no filter)"},
		"min_beds":            {Type: "integer", Description: "Minimum number of beds (Airbnb, default: no filter)"},
		"room_type":           {Type: "string", Description: "Room type filter: entire_home, private_room, shared_room, hotel_room (Airbnb, default: no filter)"},
		"superhost":           {Type: "boolean", Description: "Only show Superhost listings (Airbnb, default: false)"},
		"instant_book":        {Type: "boolean", Description: "Only show instant-bookable listings (Airbnb, default: false)"},
		"max_distance_m":      {Type: "integer", Description: "Maximum distance from city center in meters (Booking, default: no filter)"},
		"sustainable":         {Type: "boolean", Description: "Only show eco/sustainable properties (Booking, default: false)"},
		"meal_plan":           {Type: "boolean", Description: "Only show properties with breakfast/meals included (Booking, default: false)"},
		"include_sold_out":    {Type: "boolean", Description: "Include sold-out properties in results (Booking, default: false)"},
		"must_have_kitchen":   {Type: "boolean", Description: "Require a kitchen in the returned accommodation offer (default: false)"},
		"must_have_wifi":      {Type: "boolean", Description: "Require Wi-Fi in the returned accommodation offer (default: false)"},
		"must_have_workspace": {Type: "boolean", Description: "Require a dedicated workspace or desk in the returned accommodation offer (default: false)"},
	}
}

func hotelReviewsOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":  schemaBool(),
			"hotel_id": schemaString(),
			"name":     schemaString(),
			"summary": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"average_rating":   schemaNum(),
					"total_reviews":    schemaInt(),
					"rating_breakdown": schemaObject(),
				},
			},
			"reviews": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"rating": schemaNum(),
					"text":   schemaString(),
					"author": schemaString(),
					"date":   schemaString(),
				},
			}),
			"count": schemaInt(),
			"error": schemaString(),
		},
		"required": []string{"success"},
	}
}

func hotelRoomsOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":                   schemaBool(),
			"hotel_id":                  schemaString(),
			"name":                      schemaString(),
			"check_in":                  schemaString(),
			"check_out":                 schemaString(),
			"rooms":                     schemaArray(hotelRoomTypeSchema()),
			"booking_readiness":         schemaStringDesc("Composite booking-readiness verdict (MIK-6232), one of: ready, caution, unverified. Reachable to 'ready' here because rooms carry refundability and a classifiable link. Anything below ready means verify before booking."),
			"booking_readiness_reasons": schemaStringArrayDesc("Which signals drove the readiness verdict down."),
			"error":                     schemaString(),
		},
		"required": []string{"success"},
	}
}
