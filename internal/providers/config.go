// Package providers implements a generic HTTP execution engine for reviewed,
// source-controlled travel data providers. Provider definitions are embedded
// JSON files that describe how to call an external API and map its response to
// shared model types; runtime files contain preferences and health state only.
package providers

import (
	"fmt"
	"net/url"
	"regexp"
	"time"
)

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ProviderConfig is the main configuration for a single external provider.
type ProviderConfig struct {
	// SchemaVersion is the semver version of the provider-config schema this
	// document was authored against (MIK-3075). Older configs without this
	// field are treated as the v0 baseline and migrated forward at load time.
	// Configs declaring a version newer than CurrentSchemaVersion are
	// rejected with a clear error rather than loaded silently.
	SchemaVersion string            `json:"schema_version,omitempty"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Category      string            `json:"category"` // "hotel", "transport", "restaurant", ...
	Endpoint      string            `json:"endpoint"`
	Method        string            `json:"method"` // "GET" or "POST"
	Headers       map[string]string `json:"headers,omitempty"`
	// HeaderOrder specifies the order in which request headers should be sent.
	// When set, headers are added to the request in this order rather than
	// map iteration order (which is random in Go). This is critical for
	// providers whose WAF/bot-detection systems fingerprint header ordering
	// (e.g. Booking.com, Airbnb). Only header names listed here are subject
	// to ordering; any headers in the Headers map not listed here are appended
	// after the ordered ones.
	HeaderOrder     []string          `json:"header_order,omitempty"`
	QueryParams     map[string]string `json:"query_params,omitempty"`
	BodyTemplate    string            `json:"body_template,omitempty"`
	Auth            *AuthConfig       `json:"auth,omitempty"`
	ResponseMapping ResponseMapping   `json:"response_mapping"`
	RateLimit       RateLimitConfig   `json:"rate_limit"`
	TLS             TLSConfig         `json:"tls"`
	Cookies         CookieConfig      `json:"cookies"`
	Consent         *ConsentRecord    `json:"consent,omitempty"`
	LastSuccess     time.Time         `json:"last_success,omitempty"`
	LastError       string            `json:"last_error,omitempty"`
	// LastErrorAt records the timestamp of the most recent failure. The
	// circuit breaker uses this to time the cooldown window: once
	// `now - LastErrorAt >= circuitBreakerCooldown`, the provider is
	// allowed back into half-open probe state. Before this field existed
	// the breaker compared `now - LastSuccess > cooldown`, which made it
	// permanently trip providers whose last success was simply long ago
	// (regression: hotel providers Booking/Airbnb/Hostelworld stayed
	// locked out for days after their first ErrorCount-threshold burst).
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
	ErrorCount  int       `json:"error_count,omitempty"`
	Version     int       `json:"version"`

	// Personal marks a provider as owned by a specific user and carrying
	// personally-obtained credentials (e.g. AF-KLM, Ticketmaster personal keys).
	// Registry.ListPublic() skips personal providers so they are never included
	// in exported or shared configurations. Registry.List() still returns them
	// for the owner's own use.
	Personal bool `json:"personal,omitempty"`

	// CityLookup maps normalized city names to provider-specific identifiers
	// (e.g. Hostelworld city IDs). Used when the endpoint template contains
	// ${city_id} and requires a lookup step rather than direct text
	// substitution of the ${location} variable. Example:
	//   {"prague": "19", "amsterdam": "3", "helsinki": "45"}
	CityLookup map[string]string `json:"city_lookup,omitempty"`

	// CityResolver describes how to dynamically resolve a city name to a
	// provider-specific ID via an HTTP autocomplete/suggestions API. When a
	// city is not found in CityLookup, the runtime calls this endpoint and
	// caches the result back into CityLookup for future use.
	CityResolver *CityResolverConfig `json:"city_resolver,omitempty"`

	// PropertyTypeLookup maps normalized property type names to provider-specific
	// identifiers (e.g. Booking uses numeric codes, Airbnb uses string IDs).
	// Used when the endpoint/query template contains ${property_type} and the
	// provider needs a translation from the canonical names ("hotel", "apartment",
	// "hostel", "resort", "bnb", "villa") to its own vocabulary. Example:
	//   {"hotel": "204", "apartment": "201", "hostel": "203"}
	PropertyTypeLookup map[string]string `json:"property_type_lookup,omitempty"`

	// AmenityLookup maps canonical amenity names to provider-specific IDs.
	// Used to translate "wifi" → "107" (Booking) or "wifi" → "4" (Airbnb).
	// The runtime resolves each requested amenity through this map and
	// substitutes the result into ${amenity_ids} as a comma-separated list.
	AmenityLookup map[string]string `json:"amenity_lookup,omitempty"`

	// SortLookup maps canonical sort names ("price", "rating", "distance")
	// to provider-specific sort parameter values.
	SortLookup map[string]string `json:"sort_lookup,omitempty"`

	// FilterComposite builds a compound URL parameter from active filter vars.
	// Used by providers like Booking.com that combine multiple filters into a
	// single semicolon-delimited parameter (nflt=ht_id%3D204%3Bfc%3D1%3B...).
	//
	// The map keys are the names of active filter ${vars} (without the ${} wrapper).
	// The values are the URL-encoded prefix to prepend to the resolved var value.
	// The runtime joins all active (non-empty) entries with Separator and stores
	// the result as ${TargetVar}.
	//
	// Example for Booking:
	//   "filter_composite": {
	//     "target_var": "nflt",
	//     "separator": "%3B",
	//     "parts": {
	//       "property_type": "ht_id%3D",
	//       "free_cancellation": "fc%3D",
	//       "stars": "class%3D",
	//       "min_rating": "review_score%3D"
	//     }
	//   }
	FilterComposite *FilterComposite `json:"filter_composite,omitempty"`
}

// FilterComposite describes how to build a compound URL parameter from
// individual filter variables. See ProviderConfig.FilterComposite.
type FilterComposite struct {
	TargetVar string             `json:"target_var"`       // output variable name (e.g. "nflt")
	Separator string             `json:"separator"`        // URL-encoded separator (e.g. "%3B")
	Parts     map[string]string  `json:"parts"`            // filter_var → url_prefix mapping
	Scales    map[string]float64 `json:"scales,omitempty"` // filter_var → multiplier (e.g. min_rating × 10 for Booking's 0-100 scale)
}

// CityResolverConfig describes how to dynamically resolve a city name to a
// provider-specific ID via an HTTP autocomplete/suggestions API.
//
// Example for Booking.com:
//
//	{
//	  "url": "https://accommodations.booking.com/autocomplete.json?lang=en&text=${location}&limit=1",
//	  "method": "GET",
//	  "result_path": "results",
//	  "id_field": "dest_id",
//	  "name_field": "city_name"
//	}
type CityResolverConfig struct {
	// URL is the autocomplete endpoint. ${location} is replaced with the
	// URL-encoded search city name.
	URL string `json:"url"`
	// Method defaults to "GET".
	Method string `json:"method,omitempty"`
	// Headers are optional HTTP headers for the autocomplete request.
	Headers map[string]string `json:"headers,omitempty"`
	// ResultPath is the dot-notation path to the first result object in the
	// JSON response. Use "results.0" for array indexing. If the path points
	// to an array, the first element is used.
	ResultPath string `json:"result_path"`
	// IDField is the JSON key within the result object that holds the city ID.
	IDField string `json:"id_field"`
	// NameField is the JSON key within the result object that holds the
	// display name (used for cache-key normalization). Optional.
	NameField string `json:"name_field,omitempty"`
	// ExtraFields maps additional variable names (without ${}) to JSON keys
	// in the result object. For example, Booking returns "dest_type" alongside
	// "dest_id" — setting {"dest_type": "dest_type"} captures both. These
	// are stored in the vars map as ${varname} for URL template substitution.
	ExtraFields map[string]string `json:"extra_fields,omitempty"`
}

// AuthConfig describes how to authenticate with the provider.
type AuthConfig struct {
	Type         string `json:"type"` // "none", "header", "preflight"
	PreflightURL string `json:"preflight_url,omitempty"`
	// PreflightMethod defaults to GET. Set to POST for OAuth2-style token exchange.
	PreflightMethod string `json:"preflight_method,omitempty"`
	// PreflightBody is the request body for POST preflights (supports ${var} substitution).
	PreflightBody string `json:"preflight_body,omitempty"`
	// PreflightHeaders are additional headers for the preflight request.
	PreflightHeaders map[string]string     `json:"preflight_headers,omitempty"`
	Extractions      map[string]Extraction `json:"extractions,omitempty"`
	// BrowserEscapeHatch enables the Tier 4 browser-delegation fallback. When
	// set and the caller has marked the context as interactive via
	// WithInteractive, runPreflight will open the preflight URL in the user's
	// browser so they can clear any JS challenge, then re-read cookies via
	// kooky. Defaults to false for backwards compatibility — providers must
	// opt in to avoid spontaneously launching browsers for every caller.
	BrowserEscapeHatch bool `json:"browser_escape_hatch,omitempty"`
}

// Extraction describes a regex extraction from a preflight response.
//
// By default the pattern is matched against the body of the main preflight
// request (or against the named response Header). When URL is set, the
// preflight client issues a separate GET to that URL (reusing cookies already
// in the jar and substituting ${var} placeholders from previously-matched
// extractions) and runs the pattern against that response instead. This
// enables two-stage preflights such as "fetch HTML for CSRF, then fetch the
// JS bundle referenced by the HTML for a persisted-query sha256Hash" —
// mirroring exactly what the provider's own web app does.
type Extraction struct {
	Pattern  string `json:"pattern"`           // regex with capture group
	Variable string `json:"variable"`          // variable name for substitution
	Header   string `json:"header"`            // response header to extract from (empty = body)
	URL      string `json:"url,omitempty"`     // fetch this URL first; supports ${var} from prior extractions
	Default  string `json:"default,omitempty"` // value to use when pattern does not match (e.g. last-known persisted-query hash)
}

// ResponseMapping describes how to extract results from the provider response.
//
// The default flow parses the HTTP response body as JSON and walks ResultsPath
// to locate the results array. Two extensions are available:
//
//   - BodyExtractPattern: when set, the regex is first applied to the raw
//     HTTP body and capture group 1 becomes the input for JSON parsing.
//     This is the hook for server-side-rendered providers that embed their
//     API response JSON inside an HTML `<script type="application/json">`
//     block (e.g. Booking.com's Apollo cache blob).
//
//   - ResultsPath wildcards: a segment ending in `*` matches the first child
//     key starting with that prefix. Required for Apollo's normalized cache
//     where the query key is `search({"input":{...giant-JSON...}})` and
//     varies per request; the path becomes `.searchQueries.search*.results`.
type ResponseMapping struct {
	ResultsPath        string            `json:"results_path"`                   // dot-notation path to results array, supports `prefix*` segment wildcard
	Fields             map[string]string `json:"fields"`                         // model field -> JSON path mapping
	BodyExtractPattern string            `json:"body_extract_pattern,omitempty"` // optional regex applied to the HTTP body; capture group 1 becomes the JSON input
	RatingScale        float64           `json:"rating_scale,omitempty"`         // multiply raw rating by this to normalize to 0-10 (e.g. 2.0 for Booking's 0-5 scale)
}

// RateLimitConfig controls request pacing.
type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
}

// TLSConfig controls TLS fingerprint behaviour.
type TLSConfig struct {
	Fingerprint string `json:"fingerprint"` // "standard" or "chrome"
}

// CookieConfig controls how cookies are obtained.
type CookieConfig struct {
	Source  string `json:"source"`            // "none", "preflight", "browser"
	Browser string `json:"browser,omitempty"` // browser name when source is "browser"
}

// ConsentRecord tracks user consent for a provider domain.
type ConsentRecord struct {
	Granted   bool      `json:"granted"`
	Timestamp time.Time `json:"timestamp"`
	Domain    string    `json:"domain"`
}

// EndpointDomain extracts the host from the provider's endpoint URL.
// Returns an empty string if the URL cannot be parsed.
func (c *ProviderConfig) EndpointDomain() string {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return ""
	}
	return u.Host
}

// Status returns a summary status string for the provider.
// "ok" if the last request succeeded, "error" if there are recent errors,
// "new" if no requests have been made yet.
func (c *ProviderConfig) Status() string {
	if c.ErrorCount > 0 {
		return "error"
	}
	if !c.LastSuccess.IsZero() {
		return "ok"
	}
	return "new"
}

// IsStale returns true if the provider has errors and no successful request
// in the last 24 hours.
func (c *ProviderConfig) IsStale() bool {
	if c.ErrorCount == 0 {
		return false
	}
	if c.LastSuccess.IsZero() {
		return c.ErrorCount > 0
	}
	return time.Since(c.LastSuccess) > 24*time.Hour
}

// validCategories lists accepted provider category values.
var validCategories = map[string]bool{
	"hotel":       true,
	"hotels":      true,
	"flight":      true,
	"flights":     true,
	"ground":      true,
	"restaurant":  true,
	"restaurants": true,
	"review":      true,
	"reviews":     true,
}

// Validate checks that required fields are present and values are valid.
func (c *ProviderConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("provider config: id is required")
	}
	if !validProviderID(c.ID) {
		return fmt.Errorf("provider config: id %q must match %s", c.ID, providerIDPattern.String())
	}
	if c.Name == "" {
		return fmt.Errorf("provider config: name is required")
	}
	if c.Category == "" {
		return fmt.Errorf("provider config: category is required")
	}
	if !validCategories[c.Category] {
		return fmt.Errorf("provider config: invalid category %q", c.Category)
	}
	if c.Endpoint == "" {
		return fmt.Errorf("provider config: endpoint is required")
	}

	// Validate endpoint is a parseable URL with scheme and host.
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return fmt.Errorf("provider config: endpoint is not a valid URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("provider config: endpoint must include scheme and host (e.g. https://api.example.com/path)")
	}

	if c.ResponseMapping.ResultsPath == "" {
		return fmt.Errorf("provider config: response_mapping.results_path is required")
	}

	// Validate rate limit bounds.
	if c.RateLimit.RequestsPerSecond < 0 {
		return fmt.Errorf("provider config: rate_limit.requests_per_second must be non-negative, got %v", c.RateLimit.RequestsPerSecond)
	}
	if c.RateLimit.RequestsPerSecond > 100 {
		return fmt.Errorf("provider config: rate_limit.requests_per_second must be <= 100, got %v (higher rates risk IP bans)", c.RateLimit.RequestsPerSecond)
	}

	return nil
}

func validProviderID(id string) bool {
	return providerIDPattern.MatchString(id)
}
