package travelctx

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// Clock supplies the current time. The system clock is the default; tests
// inject a fixed clock for determinism.
type Clock interface {
	Now() time.Time
}

// systemClock returns the real wall clock in the host's local timezone.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// GeoResolver discovers the user's approximate physical location. The
// production implementation calls a free, keyless geo-IP endpoint; tests
// inject a stub. Implementations MUST be best-effort: a non-nil error means
// "could not determine", never a hard failure for the caller.
type GeoResolver interface {
	Resolve(ctx context.Context) (Location, error)
}

// Options tune a single Resolve call.
type Options struct {
	// ExplicitOrigin, when non-empty, wins outright (precedence rule 1).
	// This is the CLI ORIGIN argument; passing it means the user already
	// said where they are, so no detection runs.
	ExplicitOrigin string

	// AllowGeoIP gates the network detection path (precedence rule 3).
	// When false, resolution stops at preferences. Callers pass false in
	// the deterministic test suite, in CI, or when the user opted out.
	AllowGeoIP bool

	// Clock overrides the time source (tests). nil => system clock.
	Clock Clock

	// Geo overrides the location source (tests). nil => httpGeoResolver.
	Geo GeoResolver
}

// geoDisabledByEnv reports whether the environment forbids the network geo
// path: explicit opt-out, or a detected CI/sandbox where outbound calls are
// noise. Mirrors the kill-switch philosophy of the selfupdate checker.
func geoDisabledByEnv() bool {
	if v := os.Getenv("TRVL_NO_GEO"); v != "" && v != "0" && v != "false" {
		return true
	}
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI"} {
		if v := os.Getenv(name); v != "" && v != "0" && v != "false" {
			return true
		}
	}
	return false
}

// timezoneName returns the IANA zone name for t's location. Go's time.Local
// stringifies as "Local", which is useless to a caller trying to display or
// reason about the zone. When we hit that, fall back to the TZ env var (set
// on most Unix hosts) and finally to the zone abbreviation from the timestamp
// (e.g. "EEST"), so the field is always meaningful rather than "Local".
func timezoneName(t time.Time) string {
	name := t.Location().String()
	if name != "" && name != "Local" {
		return name
	}
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		return tz
	}
	if abbr, _ := t.Zone(); abbr != "" {
		return abbr
	}
	return name
}

// Resolve assembles the ambient search Context: current time + timezone
// (always), and the best-available origin per the precedence in the package
// doc. It never returns an error — every source degrades gracefully, because
// a search must still run with partial context.
func Resolve(ctx context.Context, prefs *preferences.Preferences, opts Options) Context {
	clk := opts.Clock
	if clk == nil {
		clk = systemClock{}
	}
	now := clk.Now()

	out := Context{
		Now:        now,
		Timezone:   timezoneName(now),
		TimeSource: SourceClock,
	}

	// Precedence 1: explicit caller value wins, no detection.
	if code := normalizeIATA(opts.ExplicitOrigin); code != "" {
		out.Origin = Location{
			Airport: code,
			City:    models.LookupAirportName(code),
			Source:  SourceExplicit,
		}
		return out
	}

	// Precedence 2: configured home airport — zero network, zero privacy cost.
	if prefs != nil {
		if home := normalizeIATA(prefs.HomeAirport()); home != "" {
			loc := Location{Airport: home, City: models.LookupAirportName(home), Source: SourcePrefs}
			if len(prefs.HomeCities) > 0 {
				loc.City = prefs.HomeCities[0]
			}
			out.Origin = loc
			return out
		}
	}

	// Precedence 3: geo-IP, best-effort and gated.
	if opts.AllowGeoIP {
		geo := opts.Geo
		// The env kill-switch (CI / TRVL_NO_GEO) only suppresses the REAL
		// network resolver. An explicitly injected resolver (tests, or a
		// caller supplying its own location source) is never gated — the
		// gate exists to avoid outbound calls, not to disable injection.
		if geo == nil {
			if geoDisabledByEnv() {
				return out
			}
			geo = newHTTPGeoResolver()
		}
		if loc, err := geo.Resolve(ctx); err == nil {
			// Fill the airport from city/country if the resolver didn't.
			if loc.Airport == "" {
				loc.Airport = airportForLocation(loc.City, loc.Country)
			}
			loc.Airport = normalizeIATA(loc.Airport)
			if loc.Source == SourceUnknown {
				loc.Source = SourceGeoIP
			}
			out.Origin = loc
			return out
		}
	}

	// Nothing resolved — Origin stays zero; callers must handle (and the CLI
	// falls back to requiring an explicit ORIGIN, as before).
	return out
}

// --- network geo resolver (free, keyless, best-effort) ---

const (
	// geoEndpoint is ipinfo.io's anonymous JSON endpoint. Keyless, no signup,
	// generous unauthenticated allowance — consistent with trvl's
	// "no API keys on the default path" rule. Failure is non-fatal.
	geoEndpoint    = "https://ipinfo.io/json"
	geoUserAgent   = "trvl-travelctx/1"
	geoHTTPTimeout = 3 * time.Second
)

type httpGeoResolver struct {
	client   *http.Client
	endpoint string
}

func newHTTPGeoResolver() *httpGeoResolver {
	return &httpGeoResolver{
		client:   &http.Client{Timeout: geoHTTPTimeout},
		endpoint: geoEndpoint,
	}
}

// ipinfoResponse is the subset of ipinfo.io's payload we consume. "loc" is
// "lat,lon" as a single string.
type ipinfoResponse struct {
	City    string `json:"city"`
	Country string `json:"country"`
	Loc     string `json:"loc"`
}

func (r *httpGeoResolver) Resolve(ctx context.Context) (Location, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return Location{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", geoUserAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return Location{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Location{}, &geoStatusError{code: resp.StatusCode}
	}

	var raw ipinfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Location{}, err
	}
	lat, lon := parseLatLon(raw.Loc)
	return Location{
		City:    strings.TrimSpace(raw.City),
		Country: strings.ToUpper(strings.TrimSpace(raw.Country)),
		Lat:     lat,
		Lon:     lon,
		Source:  SourceGeoIP,
	}, nil
}

type geoStatusError struct{ code int }

func (e *geoStatusError) Error() string {
	return "travelctx: geo endpoint returned status " + itoa(e.code)
}

// itoa is a tiny dependency-free int->string (avoids importing strconv just
// for one error path).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// parseLatLon splits ipinfo's "lat,lon" string into two floats. Returns
// (0,0) on any parse problem — coordinates are advisory, never load-bearing.
func parseLatLon(s string) (lat, lon float64) {
	parts := strings.SplitN(strings.TrimSpace(s), ",", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	lat = atof(parts[0])
	lon = atof(parts[1])
	return lat, lon
}

// atof is a minimal, tolerant float parser for decimal-degree strings.
func atof(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var neg bool
	if s[0] == '-' {
		neg = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	var intPart, fracPart float64
	var fracDiv float64 = 1
	seenDot := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.':
			if seenDot {
				return 0
			}
			seenDot = true
		case c >= '0' && c <= '9':
			d := float64(c - '0')
			if seenDot {
				fracDiv *= 10
				fracPart += d / fracDiv
			} else {
				intPart = intPart*10 + d
			}
		default:
			return 0
		}
	}
	v := intPart + fracPart
	if neg {
		v = -v
	}
	return v
}
