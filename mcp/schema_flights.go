package mcp

// schema_flights.go holds the JSON Schema builders for the flight search MCP tools, extracted from tools_flights.go to keep that file focused on tool registration + orchestration (file hygiene: <=800 LOC).

// flightSearchOutputSchema returns the JSON Schema for FlightSearchResult.
func flightSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":   schemaBool(),
			"count":     schemaInt(),
			"trip_type": schemaString(),
			"flights": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"price":         schemaNum(),
					"currency":      schemaString(),
					"duration":      schemaInt(),
					"stops":         schemaInt(),
					"provider":      schemaString(),
					"booking_url":   schemaString(),
					"all_in_cost":   schemaNumDesc("Total cost including baggage fees adjusted for FF status"),
					"bag_breakdown": schemaStringDesc("Baggage cost explanation, e.g. '+€35 checked bag' or 'bags included'"),
					"self_connect":  schemaBool(),
					"miles_earned": schemaArrayDesc("Estimated miles/points earned per FF programme", map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"program":      schemaString(),
							"miles_earned": schemaInt(),
							"method":       schemaStringDesc("'revenue' or 'distance'"),
						},
					}),
					"miles_value": schemaNumDesc("Cents-per-mile value if this flight were redeemed with points"),
					"warnings":    schemaStringArray(),
					"legs": schemaArray(map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"departure_airport": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"code": schemaString(),
									"name": schemaString(),
								},
							},
							"arrival_airport": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"code": schemaString(),
									"name": schemaString(),
								},
							},
							"departure_time": schemaString(),
							"arrival_time":   schemaString(),
							"duration":       schemaInt(),
							"airline":        schemaString(),
							"airline_code":   schemaString(),
							"flight_number":  schemaString(),
						},
					}),
				},
			}),
			"suggestions": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action":      schemaString(),
					"description": schemaString(),
					"params":      schemaObject(),
				},
			}),
			"hacks": schemaArrayDesc("Auto-detected travel optimization tips for this route (zero-API-call detectors only)", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        schemaString(),
					"title":       schemaString(),
					"description": schemaString(),
					"savings":     schemaNum(),
					"currency":    schemaString(),
					"steps":       schemaStringArray(),
				},
			}),
			"provider_statuses": schemaArrayDesc("Per-provider outcome (Google Flights / Kiwi / Skiplagged / configured providers). Status: 'ok'|'error'|'skipped'|'circuit_broken'. Surfaces why a provider was skipped or which ones failed so callers can recover.", map[string]interface{}{
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
			"price_position": map[string]interface{}{
				"type":        "object",
				"description": "Where today's cheapest fare sits in this route's own price history (MIK-6229). Only assert a verdict when confident=true; otherwise tell the user there is not enough history yet.",
				"properties": map[string]interface{}{
					"band":          schemaStringDesc("low, typical, or high (a not-confident marker when history is too sparse)"),
					"verdict":       schemaStringDesc("buy, wait, or neutral (a not-confident marker when history is too sparse)"),
					"current":       schemaNum(),
					"low":           schemaNum(),
					"high":          schemaNum(),
					"median":        schemaNum(),
					"vs_median_pct": schemaNum(),
					"observations":  schemaInt(),
					"confident":     schemaBool(),
				},
			},
			"savings": schemaArrayDesc("Call-free money-saving options (MIK-6234): cheaper same-day fare, vs-history, depart-a-nearby-date shift-day, and routes pre-computed by the watch monitor. No extra searches were made to produce these. Surface them; respect as_of staleness and never present stale data as a live quote.", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"kind":        schemaStringDesc("shift_day, same_day_alternative, vs_history, or probe"),
					"description": schemaString(),
					"amount":      schemaNum(),
					"currency":    schemaString(),
					"as_of":       schemaStringDesc("RFC3339 time the underlying data was observed"),
					"call_free":   schemaBool(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}

// dateSearchOutputSchema returns the JSON Schema for DateSearchResult.
func dateSearchOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success":    schemaBool(),
			"count":      schemaInt(),
			"trip_type":  schemaString(),
			"date_range": schemaString(),
			"dates": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date":        schemaString(),
					"price":       schemaNum(),
					"currency":    schemaString(),
					"return_date": schemaString(),
				},
				"required": []string{"date", "price", "currency"},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "count"},
	}
}
