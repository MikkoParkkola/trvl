package flights

// Wizz Air flight provider.
//
// Like Ryanair, Wizz Air sells almost exclusively through its own channels and
// is omitted from Google Flights / GDS aggregation, so a meta-search misses its
// (frequently cheapest) Central/Eastern-European fares entirely. This provider
// calls Wizz Air's public, unauthenticated timetable endpoint directly:
//
//	POST https://be.wizzair.com/{version}/Api/search/timetable
//
// The response carries the operating flight number (e.g. "W6 2401"), local
// departure dates and the lead-in price, so results map cleanly to
// models.FlightResult and feed comparable all-in pricing (Wizz bag fees live in
// internal/baggage).
//
// VERSION ROTATION (verified gotcha): the {version} path segment rotates
// periodically (observed "10.1.0", "29.3.0", "29.4.0", ...). It is NOT hardcoded
// into the URL builder. Resolution order (first hit wins):
//
//  1. WIZZAIR_API_VERSION env var (operator override, no code change / redeploy).
//  2. wizzVersion package var — seeded from the const, updated by runtime
//     self-healing (and loaded from the ~/.trvl cache at startup).
//
// wizzDefaultVersion is the last-known-good value and the cold-start fallback.
// Resolution order at request time (first hit wins): WIZZAIR_API_VERSION env
// override; a previously self-healed version cached in ~/.trvl/wizzair_version.json;
// then this constant. When the segment rotates, the timetable endpoint 404s and
// SearchWizzair self-heals at runtime (see wizzair_selfheal.go): it discovers the
// new version via the be.wizzair.com/<v>/Api/asset/culture oracle and retries —
// the retry is the end-to-end verification. If healing is disabled
// (WIZZAIR_API_VERSION pin or WIZZAIR_NO_AUTOHEAL=1) or no candidate is live, the
// rotation surfaces as a typed, actionable ProviderStatus (FixHintCode
// "WIZZ_VERSION_ROTATED") telling the operator to set WIZZAIR_API_VERSION.
//
// Tracking: low-cost-carrier provider breadth (flights domain); GH #115.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// ErrWizzVersionRotated is the sentinel returned when the Wizz timetable endpoint
// answers 404 — the signature of a rotated {version} path segment. Callers detect
// it with errors.Is and render a typed, actionable ProviderStatus instead of a
// silent failure. The wrapped message carries the version that was tried.
//
// On this error SearchWizzair self-heals (wizzair_selfheal.go): it discovers the
// current version via the asset/culture oracle and retries — verifying the new
// version end-to-end from the caller's real IP. This sentinel is still the
// terminal outcome when healing is disabled (WIZZAIR_API_VERSION pin /
// WIZZAIR_NO_AUTOHEAL=1) or discovery finds no live candidate, so graceful
// degradation plus the env override remains the floor.
var ErrWizzVersionRotated = errors.New("wizzair API version path rotated")

// ErrWizzBlocked is the sentinel for a non-200 whose body is NOT Wizz's own JSON
// — an HTML/edge "Bad Request", a bot challenge, or any CloudFront-origin reply
// that stopped the call before it reached the timetable API. The Wizz API sits
// behind CloudFront (verified: `server: CloudFront` on responses), which returns
// 4xx to traffic it treats as non-human. This is common from datacenter / CI
// egress IPs, so it is the most likely cause of a live-probe "status 400" that
// does not reproduce from a residential IP. Surfaced as an actionable typed
// status instead of an opaque "unexpected status 400".
var ErrWizzBlocked = errors.New("wizzair edge-blocked (CloudFront / non-human IP)")

// ErrWizzRejected is the sentinel for a structured Wizz validation refusal — a
// 4xx carrying JSON {"validationCodes":[...]} (e.g. "InvalidMarket" for a route
// Wizz does not fly, verified live 2026-06-20). This is an honest provider
// answer, not a trvl transport fault, so it gets its own typed status rather
// than being lumped into a generic failure.
var ErrWizzRejected = errors.New("wizzair declined the request (validationCodes)")

// wizzDefaultVersion is the last-known-good API version path segment. See the
// package comment: the segment rotates, so this is a fallback, not a contract.
// No runtime auto-discovery exists by design (JS-gated, undiscoverable
// server-side); WIZZAIR_API_VERSION is the operator's authoritative override.
//
// 2026-06-19: rotated "10.1.0" -> "29.3.0". Verified against the web app's own
// config (wizzair.com/en-gb embeds apiUrl:"https://be.wizzair.com/29.3.0/Api")
// and confirmed live: POST be.wizzair.com/29.3.0/Api/search/timetable -> 200,
// while the prior 10.1.0 path -> 404 (the rotation signature). (GH #115)
//
// 2026-06-20 (MIK-6167): re-verified 29.3.0 still returns 200 with priced
// results from a residential IP — the live-probe "status 400" was NOT a version
// rotation. Root cause: the timetable endpoint is fronted by CloudFront, which
// 4xx-blocks non-human (datacenter / CI) traffic, and additionally returns a
// structured 400 {"validationCodes":["InvalidMarket"]} for routes Wizz does not
// serve. Both were previously swallowed into an opaque "unexpected status 400"
// that red-flagged the probe. SearchWizzair now reads the body and classifies
// these into ErrWizzBlocked / ErrWizzRejected (see classifyWizzStatus).
//
// 2026-06-30 (#430): rotated "29.3.0" -> "29.4.0". The scheduled live probe
// 404'd on the 29.3.0 path (the rotation signature). New version read from the
// web app's live config — be.wizzair.com/29.4.0/Api/asset/culture serves from a
// residential IP — confirming the path segment advanced one minor.
// 2026-07-28 (#506): rotated "29.4.0" -> "29.8.0". The default had gone stale
// while the version sentinel kept discovering the right answer and failing to
// land it (a stale remote branch rejects its push, and PR creation is refused by
// repo policy — three orphan auto/wizzair-version-* branches, zero PRs). Verified
// by direct probe rather than from the sentinel log: GET /29.8.0/Api/asset/map
// returns 200 with the full route graph, while the same path on 29.4.0, 29.7.0
// and 29.9.0 returns 404. That endpoint is the cleanest liveness signal found so
// far — unauthenticated, GET-able, and 200-or-404 rather than the ambiguous 405
// the culture asset gives. 29.9.0's 404 is the negative control, proving the
// probe distinguishes a live version from an absent one instead of answering
// success to anything. Whether it survives a datacenter IP is unestablished: it
// is CloudFront-fronted like the timetable endpoint, and only a CI run settles
// that.
// 2026-08-04: rotated "29.8.0" -> "29.10.0". The prior path returned 404 and
// the new path answered the asset/culture oracle.
// 2026-08-12: rotated "29.10.0" -> "29.11.0". The scheduled sentinel observed
// the old path returning 404 and the new path live, but its pre-existing orphan
// branch caused a non-fast-forward push failure. The sentinel push path now
// recovers that exact automation-owned branch with a force-with-lease.
const wizzDefaultVersion = "29.15.0"

// wizzVersion is the active API version. Overridable in tests; the env var
// WIZZAIR_API_VERSION takes precedence at request time via wizzResolvedVersion.
var wizzVersion = wizzDefaultVersion

// wizzDefaultHost is the production base URL. Tests override it per-call via
// SearchOptions.wizzHost (see (SearchOptions).wizzBaseHost), rather than
// swapping a shared package global that would race across concurrent searches.
const wizzDefaultHost = "https" + "://" + "be.wizzair.com"

// wizzBaseHost returns the Wizz base URL for this search: the per-call test
// override when set, else the production host.
func (o SearchOptions) wizzBaseHost() string {
	if o.wizzHost != "" {
		return o.wizzHost
	}
	return wizzDefaultHost
}

var (
	wizzLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	wizzClient  = &http.Client{Timeout: 25 * time.Second}
)

// wizzResolvedVersion returns the API version to use for host, preferring the
// env override so operators can react to a rotation without a redeploy. The
// version is scoped by host: the production host (wizzDefaultHost) reads the
// shared wizzVersion global (self-healed and persisted across restarts); any
// other host (a per-call SearchOptions.wizzHost override) reads its own healed
// entry when one exists, else falls back to wizzVersion as the starting point —
// so a heal against that host can never be observed by, or overwrite, a
// concurrent search against a different host.
func wizzResolvedVersion(host string) string {
	if v := strings.TrimSpace(os.Getenv("WIZZAIR_API_VERSION")); v != "" {
		return v
	}
	wizzVersionMu.RLock()
	defer wizzVersionMu.RUnlock()
	return wizzLockedVersion(host)
}

func wizzTimetableURL(host string) string {
	return host + "/" + wizzResolvedVersion(host) + "/Api/search/timetable"
}

type wizzFlightLeg struct {
	DepartureStation string `json:"departureStation"`
	ArrivalStation   string `json:"arrivalStation"`
	From             string `json:"from"`
	To               string `json:"to"`
}

type wizzTimetableRequest struct {
	FlightList  []wizzFlightLeg `json:"flightList"`
	PriceType   string          `json:"priceType"`
	AdultCount  int             `json:"adultCount"`
	ChildCount  int             `json:"childCount"`
	InfantCount int             `json:"infantCount"`
}

type wizzPrice struct {
	Amount       float64 `json:"amount"`
	CurrencyCode string  `json:"currencyCode"`
}

type wizzFlight struct {
	DepartureStation string    `json:"departureStation"`
	ArrivalStation   string    `json:"arrivalStation"`
	DepartureDates   []string  `json:"departureDates"`
	Price            wizzPrice `json:"price"`
	PriceType        string    `json:"priceType"`
	FlightNumber     string    `json:"flightNumber"`
}

type wizzTimetableResponse struct {
	OutboundFlights []wizzFlight `json:"outboundFlights"`
	ReturnFlights   []wizzFlight `json:"returnFlights"`
}

// wizzValidationError is Wizz's structured 4xx body, e.g.
// {"validationCodes":["InvalidMarket"]} returned for a route Wizz does not
// serve. A meaningful API rejection, not a transport fault.
type wizzValidationError struct {
	ValidationCodes []string `json:"validationCodes"`
}

// classifyWizzStatus turns a non-200 Wizz timetable response into a typed,
// diagnosable error. Resolution order:
//
//   - 404 -> ErrWizzVersionRotated: the {version} path segment rotated (the
//     historical failure mode); the WIZZAIR_API_VERSION override restores it.
//   - 4xx carrying JSON {"validationCodes":[...]} -> ErrWizzRejected: Wizz
//     declined the request (e.g. "InvalidMarket" for a route it does not fly).
//   - any other 4xx/5xx whose body is NOT Wizz JSON (HTML, "Bad Request", a bot
//     challenge) -> ErrWizzBlocked: the request was stopped at the CloudFront
//     edge before reaching the API. Common from datacenter / CI IPs.
//
// The (bounded, whitespace-collapsed) body is echoed into the message so an
// operator can see exactly what Wizz said without re-running with a capture.
func classifyWizzStatus(host string, status int, body []byte) error {
	if status == http.StatusNotFound {
		return fmt.Errorf("wizzair: tried API version %q: %w", wizzResolvedVersion(host), ErrWizzVersionRotated)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var v wizzValidationError
		if err := json.Unmarshal(trimmed, &v); err == nil && len(v.ValidationCodes) > 0 {
			return fmt.Errorf("wizzair: status %d, validationCodes %v: %w", status, v.ValidationCodes, ErrWizzRejected)
		}
	}
	return fmt.Errorf("wizzair: status %d (edge-blocked, body=%q): %w", status, wizzBodySnippet(trimmed), ErrWizzBlocked)
}

// wizzBodySnippet bounds and whitespace-collapses an error body for inclusion in
// a typed error message, so HTML/edge responses stay readable in one line.
func wizzBodySnippet(b []byte) string {
	const maxSnippet = 200
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > maxSnippet {
		return s[:maxSnippet] + "..."
	}
	return s
}

// SearchWizzair queries Wizz Air's public timetable endpoint for one-way fares
// on the given route and date, returning canonical FlightResults tagged provider
// "wizzair" with carrier code "W6".
//
// The timetable endpoint returns the cheapest fare per day within a window. We
// request a single day (from == to == date) and keep only flights whose local
// departure date matches the requested date.
func SearchWizzair(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	if currency == "" {
		currency = "EUR"
	}
	host := opts.wizzBaseHost()
	// Adopt a previously-healed version (if any) before the first request.
	wizzMaybeLoadCache(host)
	if err := wizzLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	reqBody := wizzTimetableRequest{
		FlightList: []wizzFlightLeg{{
			DepartureStation: strings.ToUpper(origin),
			ArrivalStation:   strings.ToUpper(destination),
			From:             date,
			To:               date,
		}},
		PriceType:   "regular",
		AdultCount:  max(opts.Adults, 1),
		ChildCount:  0,
		InfantCount: 0,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("wizzair: marshal request: %w", err)
	}

	// Capture the version this attempt will use BEFORE the request, so a
	// concurrent heal between the failure and the heal call can't make us walk
	// past an already-working version (we'd otherwise treat the healed version as
	// the stale one and probe its successors).
	used := wizzResolvedVersion(host)
	results, err := wizzSearchOnce(ctx, host, payload, date, currency)
	// Self-heal on a version rotation: discover the new version, swap it in, and
	// retry once. The retry IS the end-to-end verification — it only succeeds if
	// the discovered version actually serves a priced timetable response.
	if errors.Is(err, ErrWizzVersionRotated) && wizzHealEnabled() {
		if newV, ok := wizzHeal(ctx, host, used); ok && newV != used {
			slog.Warn("wizzair self-healed API version after rotation", "from", used, "to", newV)
			if werr := wizzLimiter.Wait(ctx); werr == nil {
				results, err = wizzSearchOnce(ctx, host, payload, date, currency)
			}
		}
	}
	return results, err
}

// wizzSearchOnce performs one timetable request at the currently-resolved
// version and maps the response. A rotated version surfaces as
// ErrWizzVersionRotated (404) for the caller to optionally heal and retry.
func wizzSearchOnce(ctx context.Context, host string, payload []byte, date, currency string) ([]models.FlightResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wizzTimetableURL(host), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("wizzair: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https"+"://"+"wizzair.com")
	req.Header.Set("Referer", "https"+"://"+"wizzair.com/")
	req.Header.Set("User-Agent", wizzBrowserUA)

	resp, err := wizzClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wizzair: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Read a bounded slice of the body so the failure is diagnosable and so
		// we can distinguish: version rotation (404), Wizz's structured
		// validation refusals (JSON validationCodes), and CloudFront/edge
		// bot-walls (non-JSON 4xx). The search continues and returns other
		// providers' results regardless of which typed error we surface here.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, classifyWizzStatus(host, resp.StatusCode, errBody)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("wizzair: read body: %w", err)
	}

	var parsed wizzTimetableResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("wizzair: decode: %w", err)
	}

	results := make([]models.FlightResult, 0, len(parsed.OutboundFlights))
	for _, f := range parsed.OutboundFlights {
		mapped, ok := mapWizzFlight(f, date, currency)
		if ok {
			results = append(results, mapped)
		}
	}
	return results, nil
}

// mapWizzFlight converts a single Wizz timetable flight to a FlightResult,
// keeping only the departure date that matches the requested day. Returns
// ok=false when the flight has no departure on the requested date.
func mapWizzFlight(f wizzFlight, wantDate, fallbackCurrency string) (models.FlightResult, bool) {
	dep := wizzPickDeparture(f.DepartureDates, wantDate)
	if dep == "" {
		return models.FlightResult{}, false
	}
	cur := f.Price.CurrencyCode
	if cur == "" {
		cur = fallbackCurrency
	}
	leg := models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: strings.ToUpper(f.DepartureStation)},
		ArrivalAirport:   models.AirportInfo{Code: strings.ToUpper(f.ArrivalStation)},
		DepartureTime:    wizzDisplayTime(dep),
		Airline:          "Wizz Air",
		AirlineCode:      "W6",
		FlightNumber:     f.FlightNumber,
	}
	return models.FlightResult{
		Price:      f.Price.Amount,
		Currency:   cur,
		Stops:      0,
		Provider:   "wizzair",
		Legs:       []models.FlightLeg{leg},
		BookingURL: wizzBookingURL(f.DepartureStation, f.ArrivalStation, dep),
	}, true
}

// wizzTimeLayout is the local datetime Wizz returns (no zone).
const wizzTimeLayout = "2006-01-02T15:04:05"

// wizzPickDeparture returns the departureDates entry whose calendar day matches
// wantDate (YYYY-MM-DD). If wantDate is empty it returns the first entry. The
// timetable endpoint can return adjacent days, so this scoping is required.
func wizzPickDeparture(dates []string, wantDate string) string {
	for _, d := range dates {
		if wantDate == "" {
			return d
		}
		if strings.HasPrefix(d, wantDate) {
			return d
		}
	}
	if wantDate == "" && len(dates) > 0 {
		return dates[0]
	}
	return ""
}

func wizzDisplayTime(s string) string {
	if t, err := time.Parse(wizzTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	// Already trimmed to the flight layout, or an unexpected format: pass through.
	if t, err := time.Parse(flightTimeLayout, s); err == nil {
		return t.Format(flightTimeLayout)
	}
	return s
}

// wizzairFailureStatus maps a SearchWizzair error to a typed ProviderStatus. The
// host names which base URL this attempt used, so the version echoed in the
// rotation fix hint reflects that host's own resolved version rather than
// another concurrent search's. A version-rotation 404 (ErrWizzVersionRotated)
// renders an actionable status carrying a typed FixHintCode and a hint naming
// the WIZZAIR_API_VERSION env override plus the last-known-good version, so an
// operator or orchestrating LLM can restore the provider without a code change.
// All other errors fall through to the standard classification (timeout vs
// failed). This helper is pure so it can be unit-tested offline, independent of
// the live aggregate search.
func wizzairFailureStatus(host string, err error) models.ProviderStatus {
	st := models.ProviderStatus{
		ID:     "wizzair",
		Name:   "Wizz Air",
		Status: models.ClassifyProviderError(err),
		Error:  err.Error(),
	}
	if errors.Is(err, ErrWizzVersionRotated) {
		st.FixHintCode = "WIZZ_VERSION_ROTATED"
		st.FixHint = fmt.Sprintf(
			"Wizz API version path rotated; set WIZZAIR_API_VERSION=<current> to restore (tried %q; last-known-good: %s)",
			wizzResolvedVersion(host), wizzDefaultVersion,
		)
	}
	if errors.Is(err, ErrWizzBlocked) {
		st.FixHintCode = "WIZZ_BLOCKED"
		st.FixHint = "Wizz Air edge-blocked the request (CloudFront 4xx). Common from datacenter/CI egress IPs Wizz treats as non-human; retry from a residential IP or an allowed egress."
	}
	if errors.Is(err, ErrWizzRejected) {
		st.FixHintCode = "WIZZ_MARKET_REJECTED"
		st.FixHint = "Wizz Air declined the request (validationCodes, e.g. InvalidMarket): the route is not a market Wizz serves. Verify origin/destination are Wizz-served IATA codes."
	}
	return st
}

func wizzBookingURL(origin, destination, departure string) string {
	day := departure
	if t, err := time.Parse(wizzTimeLayout, departure); err == nil {
		day = t.Format("2006-01-02")
	} else if t, err := time.Parse(flightTimeLayout, departure); err == nil {
		day = t.Format("2006-01-02")
	}
	q := url.Values{}
	q.Set("departureDate", day)
	q.Set("origin", strings.ToUpper(origin))
	q.Set("destination", strings.ToUpper(destination))
	q.Set("adultCount", "1")
	return "https" + "://" + "www.wizzair.com/en-gb/flights/select?" + q.Encode()
}
