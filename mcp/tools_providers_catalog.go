package mcp

// providerSuggestion describes an available reviewed provider definition.
// Returned by suggest_providers to help an LLM explain what can be enabled.
type providerSuggestion struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Category       string         `json:"category"`
	Description    string         `json:"description"`
	AuthPattern    string         `json:"auth_pattern"`
	AuthHint       string         `json:"auth_hint"`
	Reference      string         `json:"reference"`
	TosURL         string         `json:"tos_url"`
	TLS            string         `json:"tls"`
	RateLimit      string         `json:"rate_limit"`
	Configured     bool           `json:"configured"`
	ConfigSkeleton map[string]any `json:"config_skeleton,omitempty"`
}

// availableProviders is the descriptive catalog for reviewed definitions. In a
// source-only registry, suggest_providers filters this list to IDs actually
// embedded in the current binary.
var availableProviders = []providerSuggestion{
	{
		ID:             "openstreetmap-hotels",
		Name:           "OpenStreetMap accommodation discovery",
		Category:       "hotels",
		Description:    "Keyless accommodation discovery via the public Overpass API; returns map candidates rather than bookable prices.",
		AuthPattern:    "none",
		AuthHint:       "Reviewed definition embedded in the trvl binary.",
		Reference:      "https://wiki.openstreetmap.org/wiki/Overpass_API",
		TosURL:         "https://wiki.openstreetmap.org/wiki/Overpass_API",
		TLS:            "standard",
		RateLimit:      "0.2 req/s",
		ConfigSkeleton: map[string]any{
			"response_mapping": skeletonResponseMapping(),
		},
	},
	{
		ID:          "booking",
		Name:        "Booking.com",
		Category:    "hotels",
		Description: "Hotels and apartments worldwide.",
		AuthPattern: "graphql_browser_cookies",
		AuthHint: "GraphQL with browser cookies (no CSRF needed).\n" +
			"Reference: github.com/opentabs-dev/opentabs\n" +
			"  - Endpoint: https://www.booking.com/dml/graphql?lang=en-gb\n" +
			"  - Auth: browser cookies from user's installed browser (kooky auto-detect)\n" +
			"  - Set cookies.source: \"browser\" and auth.browser_escape_hatch: true\n" +
			"  - Rating: 0-10 natively (totalScore), no rating_scale needed\n" +
			"  - Note: WAF-protected, standard HTTP client works better with browser cookies",
		Reference:      "github.com/opentabs-dev/opentabs",
		TosURL:         "https://www.booking.com/content/terms.html",
		TLS:            "chrome",
		RateLimit:      "0.5 req/s",
		ConfigSkeleton: skeletonBookingCSRF(),
	},
	{
		ID:          "airbnb",
		Name:        "Airbnb",
		Category:    "hotels",
		Description: "Vacation rentals, apartments, and unique stays.",
		AuthPattern: "graphql_apikey",
		AuthHint: "SSR HTML extraction (Niobe deferred-state-0 cache).\n" +
			"Reference: github.com/johnbalvin/gobnb\n" +
			"  - See api/search.go for the search URL pattern and request structure\n" +
			"  - See api/types.go for response field names (ListingResult schema)\n" +
			"  - Auth: browser-like GET request, extract JSON from HTML data-deferred-state-0 script\n" +
			"  - Rating: 0-5 scale, set rating_scale: 2.0 to normalize to 0-10\n" +
			"  - Note: SSR extraction, not GraphQL API — use body_extract_pattern in response_mapping",
		Reference:      "github.com/johnbalvin/gobnb",
		TosURL:         "https://www.airbnb.com/terms",
		TLS:            "chrome",
		RateLimit:      "0.5 req/s",
		ConfigSkeleton: skeletonGraphQLAPIKey(),
	},
	{
		ID:          "vrbo",
		Name:        "VRBO",
		Category:    "hotels",
		Description: "Vacation rentals (Expedia Group).",
		AuthPattern: "graphql_headers",
		AuthHint: "GraphQL with browser-like headers.\n" +
			"Reference: search GitHub for 'vrbo graphql' or 'vrbo api'\n" +
			"  - Look for the GraphQL endpoint in network requests or OSS client code\n" +
			"  - Auth: browser-like headers required, no explicit token\n" +
			"  - Note: Expedia Group backend, similar patterns to Hotels.com",
		Reference:      "search GitHub for vrbo graphql",
		TosURL:         "https://www.vrbo.com/legal/terms-and-conditions",
		TLS:            "chrome",
		RateLimit:      "0.5 req/s",
		ConfigSkeleton: skeletonGraphQLHeaders(),
	},
	{
		ID:          "hostelworld",
		Name:        "Hostelworld",
		Category:    "hotels",
		Description: "Hostels and budget accommodation worldwide.",
		AuthPattern: "rest_apikey",
		AuthHint: "REST API with API key from page source. Global coverage (100+ cities).\n" +
			"Reference: search GitHub for 'hostelworld api' or 'hostelworld-api'\n" +
			"  - Look for src/client.ts or similar for the API base URL and endpoints\n" +
			"  - Auth: APIGEE API key extracted from homepage HTML via regex APIGEE_KEY:\"([^\"]+)\"\n" +
			"  - City IDs: numeric (e.g. Paris=14, London=3, Bangkok=149, Tokyo=452, New York=13, Sydney=81)\n" +
			"  - Resolve city IDs: visit hostelworld.com/hostels/CityName, extract id= from map link\n" +
			"  - Coverage: Europe, Asia-Pacific, Americas, Middle East/Africa, Oceania\n" +
			"  - Rating: 0-100 scale, set rating_scale: 0.1 to normalize to 0-10\n" +
			"  - Note: REST API, results in .properties[] array",
		Reference:      "search GitHub for hostelworld api",
		TosURL:         "https://www.hostelworld.com/securityprivacy/terms-and-conditions",
		TLS:            "standard",
		RateLimit:      "1 req/s",
		ConfigSkeleton: skeletonHostelworld(),
	},
	{
		ID:          "tripadvisor",
		Name:        "TripAdvisor",
		Category:    "reviews",
		Description: "Hotel and restaurant reviews and ratings.",
		AuthPattern: "graphql_queryid",
		AuthHint: "GraphQL with pre-registered query IDs.\n" +
			"Reference: search GitHub for 'tripadvisor graphql'\n" +
			"  - Look for the GraphQL endpoint and query hash/ID in OSS client code\n" +
			"  - Auth: session-based, may need cookies from an initial page visit\n" +
			"  - Note: uses numeric location IDs for cities",
		Reference:      "search GitHub for tripadvisor graphql",
		TosURL:         "https://www.tripadvisor.com/pages/terms.html",
		TLS:            "standard",
		RateLimit:      "1 req/s",
		ConfigSkeleton: skeletonGraphQLQueryID(),
	},
	{
		ID:          "blablacar",
		Name:        "BlaBlaCar",
		Category:    "ground",
		Description: "Ridesharing across Europe.",
		AuthPattern: "rest_apikey",
		AuthHint: "REST API requiring developer API key.\n" +
			"Reference: search GitHub for 'blablacar api'\n" +
			"  - Register at dev.blablacar.com for an API key\n" +
			"  - Look for endpoint URL and query params in OSS client code\n" +
			"  - Note: public REST API with straightforward JSON responses",
		Reference:      "search GitHub for blablacar api",
		TosURL:         "https://www.blablacar.com/about-us/terms-and-conditions",
		TLS:            "standard",
		RateLimit:      "2 req/s",
		ConfigSkeleton: skeletonRESTAPIKey(),
	},
	{
		ID:          "opentable",
		Name:        "OpenTable",
		Category:    "restaurants",
		Description: "Restaurant availability and reservations.",
		AuthPattern: "graphql_csrf",
		AuthHint: "GraphQL with session token.\n" +
			"Reference: search GitHub for 'opentable api'\n" +
			"  - Look for the GraphQL or REST endpoint in OSS client code\n" +
			"  - Auth: CSRF/session token extracted from page source\n" +
			"  - Note: restaurant search uses lat/lon or location name",
		Reference:      "search GitHub for opentable api",
		TosURL:         "https://www.opentable.com/legal/terms-and-conditions",
		TLS:            "chrome",
		RateLimit:      "0.5 req/s",
		ConfigSkeleton: skeletonGraphQLCSRF(),
	},
}

// --- Config skeleton builders ---

// skeletonResponseMapping returns the common response_mapping skeleton.
func skeletonResponseMapping() map[string]any {
	return map[string]any{
		"results_path": "FILL: dot-notation path to results array in response JSON",
		"rating_scale": "FILL: multiply raw rating by this to normalize to 0-10 (e.g. 2.0 for 0-5, 0.1 for 0-100, omit if already 0-10)",
		"fields": map[string]any{
			"name":     "FILL: path to hotel/property name",
			"hotel_id": "FILL: path to unique ID",
			"rating":   "FILL: path to rating score",
			"price":    "FILL: path to price amount",
			"currency": "FILL: path to currency code",
			"lat":      "FILL: path to latitude",
			"lon":      "FILL: path to longitude",
		},
	}
}

func skeletonGraphQLCSRF() map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"type":          "preflight",
			"preflight_url": "FILL: URL of a search results page on the target service",
			"extractions": map[string]any{
				"csrf_token": map[string]any{
					"pattern":  "FILL: regex with capture group to extract CSRF/session token from HTML",
					"variable": "csrf_token",
				},
			},
		},
		"endpoint": "FILL: GraphQL endpoint URL",
		"method":   "POST",
		"headers": map[string]any{
			"Content-Type":     "application/json",
			"FILL_csrf_header": "${csrf_token}",
		},
		"body_template":    "FILL: GraphQL query JSON with ${checkin}, ${checkout}, ${location} or ${ne_lat}/${sw_lat} variables",
		"response_mapping": skeletonResponseMapping(),
	}
}

// skeletonBookingCSRF returns the Booking.com-specific skeleton with
// browser_escape_hatch defaulted on, since Booking.com is WAF-protected.
func skeletonBookingCSRF() map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"type":                 "preflight",
			"preflight_url":        "FILL: URL of a search results page on the target service",
			"browser_escape_hatch": true, // WAF-protected, needs browser delegation
			"extractions": map[string]any{
				"csrf_token": map[string]any{
					"pattern":  "FILL: regex with capture group to extract CSRF/session token from HTML",
					"variable": "csrf_token",
				},
			},
		},
		"endpoint": "FILL: GraphQL endpoint URL",
		"method":   "POST",
		"headers": map[string]any{
			"Content-Type":     "application/json",
			"FILL_csrf_header": "${csrf_token}",
		},
		"tls_fingerprint":  "chrome",
		"body_template":    "FILL: GraphQL query JSON with ${checkin}, ${checkout}, ${location} or ${ne_lat}/${sw_lat} variables",
		"response_mapping": skeletonResponseMapping(),
	}
}

func skeletonGraphQLAPIKey() map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"type":          "preflight",
			"preflight_url": "FILL: homepage or page that contains the API key in source",
			"extractions": map[string]any{
				"api_key": map[string]any{
					"pattern":  "FILL: regex to extract API key from page source",
					"variable": "api_key",
				},
			},
		},
		"endpoint": "FILL: GraphQL endpoint URL",
		"method":   "POST",
		"headers": map[string]any{
			"Content-Type":    "application/json",
			"FILL_key_header": "${api_key}",
		},
		"body_template":    "FILL: GraphQL persisted query JSON with ${checkin}, ${checkout}, ${ne_lat}/${sw_lat} variables",
		"response_mapping": skeletonResponseMapping(),
	}
}

func skeletonGraphQLHeaders() map[string]any {
	return map[string]any{
		"endpoint": "FILL: GraphQL endpoint URL",
		"method":   "POST",
		"headers": map[string]any{
			"Content-Type": "application/json",
			"FILL_header1": "FILL: browser-like header value",
			"FILL_header2": "FILL: additional required header",
		},
		"body_template":    "FILL: GraphQL persisted query JSON with ${checkin}, ${checkout}, ${ne_lat}/${sw_lat} variables",
		"response_mapping": skeletonResponseMapping(),
	}
}

func skeletonRESTAPIKey() map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"type":          "preflight",
			"preflight_url": "FILL: page that contains the API key in source or headers",
			"extractions": map[string]any{
				"api_key": map[string]any{
					"pattern":  "FILL: regex to extract API key",
					"variable": "api_key",
				},
			},
		},
		"endpoint": "FILL: REST API endpoint URL with path parameters",
		"method":   "GET",
		"headers": map[string]any{
			"FILL_key_header": "${api_key}",
		},
		"query_params": map[string]any{
			"FILL_location_param": "${location}",
			"FILL_checkin_param":  "${checkin}",
			"FILL_checkout_param": "${checkout}",
			"FILL_guests_param":   "${guests}",
			"FILL_currency_param": "${currency}",
		},
		"response_mapping": skeletonResponseMapping(),
	}
}

func skeletonHostelworld() map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"type":          "preflight",
			"preflight_url": "FILL: Hostelworld search page URL (e.g. https://www.hostelworld.com/hostels/paris)",
			"extractions": map[string]any{
				"api_key": map[string]any{
					"pattern":  "FILL: regex to extract API key from page source",
					"variable": "api_key",
				},
			},
		},
		"endpoint": "FILL: REST API endpoint (e.g. https://api.hostelworld.com/v2/properties/)",
		"method":   "GET",
		"headers": map[string]any{
			"FILL_key_header": "${api_key}",
		},
		"query_params": map[string]any{
			"FILL_city_id_param":  "FILL: numeric city_id (Paris=14, London=3, Bangkok=149, Tokyo=452, New York=13, Sydney=81)",
			"FILL_checkin_param":  "${checkin}",
			"FILL_checkout_param": "${checkout}",
			"FILL_guests_param":   "${guests}",
			"FILL_currency_param": "${currency}",
		},
		"response_mapping": skeletonResponseMapping(),
	}
}

func skeletonGraphQLQueryID() map[string]any {
	return map[string]any{
		"endpoint": "FILL: GraphQL endpoint URL",
		"method":   "POST",
		"headers": map[string]any{
			"Content-Type": "application/json",
		},
		"body_template":    "FILL: GraphQL query with pre-registered query ID, includes ${checkin}, ${checkout}, ${location} variables",
		"response_mapping": skeletonResponseMapping(),
	}
}
