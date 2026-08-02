package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotelarb"
	"github.com/MikkoParkkola/trvl/internal/pricealert"
)

// IsValidCurrencyFormat reports whether c (already trimmed+uppercased) looks
// like an ISO-4217 alphabetic code: exactly three letters A-Z. Prior rounds
// only trimmed and uppercased provider currency strings, so a malformed value
// like "EU R" passed through as if it were a real currency code -- it is
// non-empty, so it satisfies every `currency != w.Currency` comparison
// downstream and triggers a genuine currency-change reset (clearing alert
// thresholds, purging price history) from a single garbled provider
// response. Reject anything that isn't three letters instead of trusting it.
// Found by GPT second-opinion review, 2026-07-30 (round 20).
//
// Exported (round 22) so Watch.Validate and Store.Add can apply the same
// check to USER-supplied currency at watch-creation/re-watch time -- round
// 21 found provider values were validated but a user could still create or
// re-watch with a malformed currency like "EU R" and trigger a destructive
// reset immediately via applyIntent.
func IsValidCurrencyFormat(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// newSafeWebhookClient builds the HTTP client used for user-supplied webhook
// URLs. Round 22 found the previous default (http.DefaultClient, no
// restriction at all) let a watch's WebhookURL reach ANY address reachable
// from this process -- loopback, RFC1918/RFC4193 private ranges, and
// link-local metadata endpoints included -- a classic SSRF (server-side
// request forgery: tricking a server into making a request the attacker
// couldn't make directly) primitive. The guard lives in the dial's Control
// hook rather than a one-time URL check so it also re-validates every
// redirect hop's resolved address, not just the original host. Found by GPT
// second-opinion review, 2026-07-30 (round 22).
func newSafeWebhookClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("webhook: invalid dial address: %w", err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("webhook: refusing to dial non-literal address %q", host)
			}
			if !isPublicWebhookIP(ip) {
				return fmt.Errorf("webhook: refusing to dial non-public address")
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext},
		// Round 25: a hop-count cap alone still lets Go's default redirect
		// handling run -- which builds a Referer header from the FULL prior
		// URL (path + query, exactly where the Slack/Discord-style webhook
		// tokens documented above live) and sends it cross-origin, and
		// replays the JSON body on a 307/308 to whatever host the response
		// names. A watch's WebhookURL is not a trusted redirect chain: the
		// receiving server controls where it points next. Refuse to follow
		// redirects at all and surface the 3xx as-is; no legitimate webhook
		// receiver needs its notifier to chase a redirect. Found by GPT
		// second-opinion review, 2026-07-31 (round 25).
		// Hoisted to the package-level webhookCheckRedirect var (rather
		// than inlined) so the regression test in
		// webhook_redirect_round25_test.go exercises the SAME policy value
		// production uses, not a hand-copied duplicate that could silently
		// drift out of sync (Grok second-opinion review, round 25,
		// optional finding 3).
		CheckRedirect: webhookCheckRedirect,
	}
}

// webhookCheckRedirect is the shared redirect policy for outbound webhook
// requests: never follow, always surface the 3xx as-is. Defined once so
// newSafeWebhookClient (production) and the round-25 redirect regression test
// build their *http.Client from the identical func value.
var webhookCheckRedirect = func(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// specialUseWebhookBlocks lists IETF special-use / non-public address ranges
// that net.IP.IsPrivate() and friends do NOT cover, so a webhook URL could
// still be steered at internal infrastructure through them. Found by GPT
// second-opinion review, 2026-07-30 (round 23): the round-22 SSRF guard only
// checked IsLoopback/IsPrivate/IsLinkLocalUnicast/IsLinkLocalMulticast/
// IsUnspecified/IsMulticast, which lets RFC 6598 shared/CGNAT space and
// several RFC 5735/6890 reserved-for-documentation-or-testing blocks through
// untouched -- all of them can be assigned to real internal infrastructure
// behind a NAT or shared address translator.
var specialUseWebhookBlocks = mustParseWebhookCIDRs(
	"0.0.0.0/8",       // "this" network
	"100.64.0.0/10",   // RFC 6598 carrier-grade NAT / shared address space
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1 documentation
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2 documentation
	"203.0.113.0/24",  // TEST-NET-3 documentation
	"240.0.0.0/4",     // reserved for future use
	"2001:db8::/32",   // IPv6 documentation

	// Round 24: net.IP.IsPrivate() only covers RFC1918 (IPv4) and RFC4193 ULA
	// (fc00::/7) -- it does NOT cover the older/adjacent IPv6 special-use
	// ranges below, so without these entries a literal like
	// "http://[fec0::1]/" passed isPublicWebhookIP as "public" and was
	// dialed if the host happened to route that prefix. Found by GPT
	// second-opinion review, 2026-07-31 (round 24).
	"fec0::/10",      // deprecated IPv6 site-local (RFC 3879 -- existing deployments may still route it)
	"64:ff9b:1::/48", // NAT64 local-use translation prefix (RFC 8215, explicitly non-globally-reachable)
	"2001::/32",      // Teredo tunneling (RFC 4380)
	"2002::/16",      // 6to4 (RFC 3056)
	"100::/64",       // discard-only prefix (RFC 6666)
	"2001:2::/48",    // benchmarking (RFC 5180)
	"3fff::/20",      // documentation (RFC 9637)
	"5f00::/16",      // segment routing SRv6 (RFC 9602)
)

func mustParseWebhookCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("webhook: invalid special-use CIDR %q: %v", c, err))
		}
		nets = append(nets, n)
	}
	return nets
}

// isPublicWebhookIP reports whether ip is safe to let a user-supplied webhook
// URL resolve to: routable on the public internet, not loopback, not
// RFC1918/RFC4193 private, not link-local (includes the 169.254.169.254
// cloud-metadata address class), not unspecified, not multicast, and not one
// of the IETF special-use ranges in specialUseWebhookBlocks (CGNAT,
// documentation/test, reserved).
func isPublicWebhookIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	for _, n := range specialUseWebhookBlocks {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var webhookHTTPClient = newSafeWebhookClient()

// SetWebhookHTTPClientForTest swaps the webhook HTTP client and returns the previous client.
func SetWebhookHTTPClientForTest(client *http.Client) *http.Client {
	prev := webhookHTTPClient
	if client == nil {
		webhookHTTPClient = newSafeWebhookClient()
	} else {
		webhookHTTPClient = client
	}
	return prev
}

// PriceChecker retrieves the current cheapest price for a route.
// Implementations bridge to flights.SearchFlights or hotels.SearchHotels
// without creating an import dependency from the watch package.
type PriceChecker interface {
	// CheckPrice returns the cheapest price and currency for the given watch.
	// For date-range and route watches, also returns the cheapest date found.
	// Returns 0 price if no results are found (not an error).
	CheckPrice(ctx context.Context, w Watch) (price float64, currency string, cheapestDate string, err error)
}

// RoomChecker retrieves available rooms for a hotel and matches them against criteria.
// Implementations bridge to hotels.GetRoomAvailability without creating an import
// dependency from the watch package.
type RoomChecker interface {
	// CheckRooms returns matching rooms for a room watch. Each returned RoomMatch
	// contains the room name, description, and price. Returns nil if no matches.
	CheckRooms(ctx context.Context, w Watch) ([]RoomMatch, error)
}

// RoomMatch represents a room that matched the watch keywords.
type RoomMatch struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	Provider    string  `json:"provider,omitempty"`
}

// CheckResult holds the outcome of checking a single watch.
type CheckResult struct {
	Watch                     Watch
	NewPrice                  float64
	Currency                  string
	PrevPrice                 float64
	BelowGoal                 bool    // price dropped below threshold
	PriceDrop                 float64 // negative = price decreased (good)
	CheapestDate              string  // for range/route watches: which date was cheapest
	RoomFound                 bool    // room watch: a matching room was found
	RoomMatches               []RoomMatch
	LastMinuteDeal            bool
	LastMinuteDiscountPercent float64
	// Proactive price-drop alert (innovation #6). PriceDropAlert is true when the
	// fare fell past the configured threshold below the baseline and this is a
	// new, deduplicated event. AlertBaseline / AlertDropPercent describe it.
	PriceDropAlert   bool
	AlertBaseline    float64
	AlertDropPercent float64
	// AlertsClearedByCurrencyChange is true when this check detected a
	// currency mismatch against the watch's prior observation/baseline and,
	// as a side effect, cleared BelowPrice and/or AlertDropAbs so a stale
	// threshold denominated in the old currency doesn't misfire against a
	// price in the new currency. Round 21 found this happened silently --
	// no notification or error told the user their alert threshold was
	// wiped. Callers that render CheckResult to the user (notify.go, MCP
	// JSON DTOs) MUST surface this so the user knows to re-set their
	// threshold. Found by GPT second-opinion review, 2026-07-30 (round 21).
	AlertsClearedByCurrencyChange bool
	Error                         error
}

// CheckAll checks all watches using the provided price checker and records
// results in the store. Pauses 3 seconds between checks to respect API rate limits.
// Returns a result for each watch.
func CheckAll(ctx context.Context, store *Store, checker PriceChecker) []CheckResult {
	return CheckAllWithRooms(ctx, store, checker, nil)
}

// CheckAllWithRooms checks all watches, using the room checker for room-type watches
// and the price checker for flight/hotel watches.
func CheckAllWithRooms(ctx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(ctx, ctx, store, checker, roomChecker, store.List())
}

// CheckAllWithRoomsAndWebhookContext checks all watches while allowing webhook
// delivery to outlive the check timeout. The webhook context should typically be
// a longer-lived parent context that is canceled when the caller is shutting
// down.
//
// Always re-derives its list from store.List() -- every watch, active or
// not. A caller that needs to check only a pre-filtered subset (e.g.
// ActiveWatches) must use CheckWatchesWithRoomsAndWebhookContext instead:
// see that function's doc comment for why this distinction is load-bearing,
// not cosmetic.
func CheckAllWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx, store, checker, roomChecker, store.List())
}

// CheckWatchesWithRoomsAndWebhookContext checks exactly the given watches,
// pre-selected by the caller, while allowing webhook delivery to outlive the
// check timeout. Exported so a caller outside this package (e.g. the
// standalone `trvl watch daemon`) can apply its own activity filter (see
// ActiveWatches) before checking, matching the in-process scheduler, which
// filters internally.
//
// This is not an optimization. checkWatchesWithRoomsAndWebhookContext pauses
// 3 seconds between checks and stops as soon as checkCtx's deadline fires; a
// caller that hands it store.List() unfiltered pays that pause for every
// stale watch too, in whatever order the store returns them. A store holding
// many expired watches ahead of one truly active watch can exhaust the
// entire check-cycle timeout before ever reaching the active one -- which
// then goes unchecked for that cycle with no error surfaced, because the
// caller's own active-count (computed separately, before this call) reports
// success regardless of whether the active watch was actually reached in
// time. Passing the SAME filtered slice here that produced that count is
// what closes the gap. Found by adversarial review, 2026-07-28.
func CheckWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker, watches []Watch) []CheckResult {
	return checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx, store, checker, roomChecker, watches)
}

func checkWatchesWithRoomsAndWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker, watches []Watch) []CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	// One provider call per distinct polled target for the whole round, and
	// single-flight within it, so concurrent checks of one target wait for the
	// in-flight call rather than issuing their own.
	checker = newRoundCache(checker)

	results := make([]CheckResult, 0, len(watches))

	for i, w := range watches {
		var r CheckResult
		if w.IsRoomWatch() && roomChecker != nil {
			r = checkRoomWithWebhookContext(checkCtx, webhookCtx, store, roomChecker, w)
		} else if w.IsRoomWatch() {
			r = CheckResult{Watch: w, Error: fmt.Errorf("room checker not configured")}
		} else {
			r = checkOneWithWebhookContext(checkCtx, webhookCtx, store, checker, w)
		}
		results = append(results, r)

		// Pause between checks to respect rate limits (skip after last).
		if i < len(watches)-1 {
			select {
			case <-checkCtx.Done():
				return results
			case <-time.After(3 * time.Second):
			}
		}
	}
	return results
}

// BoundedOptions tunes CheckAllBounded for synchronous callers (e.g. the MCP
// check_watches tool) that must return within a request deadline rather than
// running as an unbounded background daemon.
type BoundedOptions struct {
	// Concurrency caps how many watches are re-priced in parallel. Defaults to
	// DefaultBoundedConcurrency when <= 0.
	Concurrency int
	// PerWatchTimeout bounds each individual live search. A watch that exceeds it
	// is reported with an explicit timeout Error rather than a fabricated price.
	// Defaults to DefaultBoundedPerWatchTimeout when <= 0.
	PerWatchTimeout time.Duration
}

const (
	// DefaultBoundedConcurrency is the default parallelism for CheckAllBounded.
	DefaultBoundedConcurrency = 4
	// DefaultBoundedPerWatchTimeout is the default per-watch deadline.
	DefaultBoundedPerWatchTimeout = 15 * time.Second
)

// CheckAllBounded re-prices every watch concurrently with a per-watch timeout
// and a concurrency cap, recording results in the store. Unlike CheckAll it does
// not pause between checks, making it suitable for synchronous request/response
// callers. Results are returned in the same order as store.List(). A watch whose
// live search exceeds PerWatchTimeout (or whose parent context is canceled) is
// returned with a non-nil Error so callers can render an honest "not checked"
// status instead of a misleading price of 0.
func CheckAllBounded(ctx context.Context, store *Store, checker PriceChecker, roomChecker RoomChecker, opts BoundedOptions) []CheckResult {
	if ctx == nil {
		ctx = context.Background()
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultBoundedConcurrency
	}
	perWatch := opts.PerWatchTimeout
	if perWatch <= 0 {
		perWatch = DefaultBoundedPerWatchTimeout
	}

	watches := store.List()
	results := make([]CheckResult, len(watches))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	// Single-flight per polled target: duplicate routes issue one provider call
	// for the round even under concurrency (#509, MULTIPRICE.2).
	checker = newRoundCache(checker)

	for i, w := range watches {
		wg.Add(1)
		go func(i int, w Watch) {
			defer wg.Done()

			// Respect parent cancellation before acquiring a slot.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = CheckResult{Watch: w, Error: ctx.Err()}
				return
			}

			// The webhook context is the parent ctx so delivery is not killed by
			// the per-watch search deadline; the check context is bounded.
			checkCtx, cancel := context.WithTimeout(ctx, perWatch)
			defer cancel()

			switch {
			case w.IsRoomWatch() && roomChecker != nil:
				results[i] = checkRoomWithWebhookContext(checkCtx, ctx, store, roomChecker, w)
			case w.IsRoomWatch():
				results[i] = CheckResult{Watch: w, Error: fmt.Errorf("room checker not configured")}
			default:
				results[i] = checkOneWithWebhookContext(checkCtx, ctx, store, checker, w)
			}
		}(i, w)
	}

	wg.Wait()
	return results
}

// checkOne performs a price check for a single watch.
func checkOne(ctx context.Context, store *Store, checker PriceChecker, w Watch) CheckResult {
	return checkOneWithWebhookContext(ctx, ctx, store, checker, w)
}

func checkOneWithWebhookContext(checkCtx, webhookCtx context.Context, store *Store, checker PriceChecker, w Watch) CheckResult {
	checkCtx, webhookCtx = normalizeCheckAndWebhookContexts(checkCtx, webhookCtx)

	price, currency, cheapestDate, err := checker.CheckPrice(checkCtx, w)
	if err != nil {
		return CheckResult{Watch: w, Error: err}
	}
	// Currency codes are provider-supplied (IP/market-driven, not
	// user-selectable -- see round 14's finding above) and arrive with no
	// guaranteed case. w.Currency, by contrast, is typically user/API-set and
	// tends to be uppercase. A case difference alone ("eur" vs "EUR") would
	// satisfy every `!=` comparison below and be misread as a real currency
	// change, needlessly zeroing thresholds and invalidating price history.
	// Canonicalize to uppercase once, at the boundary, so every comparison
	// and every persisted w.Currency assignment downstream (including
	// migrate.go's merge/dedup logic and store.go's applyIntent) stays
	// consistent. Found by adversarial review, 2026-07-30 (round 18).
	rawCurrency := currency
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if price > 0 && currency != "" && !IsValidCurrencyFormat(currency) {
		// A non-empty value that survives trim+uppercase but isn't a
		// three-letter code (e.g. "EU R") is exactly as unusable as
		// whitespace-garbage, and must be rejected the same way -- accepting
		// it would satisfy the mismatch comparisons below and clear alert
		// thresholds / purge history from a single malformed provider
		// response. Found by GPT second-opinion review, 2026-07-30 (round 20).
		return CheckResult{Watch: w, Error: fmt.Errorf("checker returned malformed currency %q for price %v", rawCurrency, price)}
	}
	if price > 0 && currency == "" && rawCurrency != "" {
		// The provider returned a non-empty but unusable currency (e.g.
		// whitespace-only). Falling through would make currencyMismatch's
		// `currency != ""` check treat this exactly like "provider omitted
		// currency" and skip the mismatch guard entirely -- silently
		// persisting an empty currency and comparing this price against
		// whatever currency the watch was already in. Reject the quote
		// instead of guessing. Found by GPT second-opinion review,
		// 2026-07-30 (round 18).
		return CheckResult{Watch: w, Error: fmt.Errorf("checker returned unusable currency %q for price %v", rawCurrency, price)}
	}
	// Round 19 found a genuinely absent currency (provider gave none at
	// all, not whitespace-garbage) was still accepted below at
	// `w.Currency = currency`, silently overwriting an already-known
	// watch currency with "" -- distinct from the whitespace-garbage case
	// above, which only fires when the provider sent SOMETHING unusable.
	// A provider that simply omits currency on an established watch must
	// not blank out what we already know. Found by GPT second-opinion
	// review, 2026-07-30 (round 19).
	if price > 0 && currency == "" && w.Currency != "" {
		return CheckResult{Watch: w, Error: fmt.Errorf("checker returned no currency for price %v on a watch already tracking %s", price, w.Currency)}
	}

	result := CheckResult{
		Watch:        w,
		NewPrice:     price,
		Currency:     currency,
		PrevPrice:    w.LastPrice,
		CheapestDate: cheapestDate,
	}

	if price > 0 {
		// Everything below -- the currency decision, the threshold wipes, the
		// observation resets and the derived result signals -- is computed
		// INSIDE the store transaction, against the committed record, not
		// against the copy taken before the provider call.
		//
		// That placement is the fix for a data-loss race found by adversarial
		// review (2026-08-02). The old shape decided "the currency changed"
		// from the pre-provider snapshot and replayed the consequences onto
		// freshly reloaded state. If another process performed that same
		// migration while this check was in flight, the replay zeroed the
		// threshold that process had just re-set and purged the history it had
		// just written in the new currency. A field-scoped write does not help
		// when the DECISION is stale; only the decision moving under the lock
		// does.
		//
		// TRVL.STORE.TXN.4 still holds: this callback is pure bookkeeping. The
		// provider round trip already happened above.
		var (
			alert         pricealert.Alert
			alertFired    bool
			alertsCleared bool
			prevPrice     float64
			priceDrop     float64
			belowGoal     bool
			lastMinute    bool
			lastMinutePct float64
		)
		saved, err := store.MutateAndRecordPrice(w.ID, price, currency, func(cur *Watch) bool {
			prevPrice = cur.LastPrice

			// Round 18: cur.LastPrice == 0 is not a reliable "no prior
			// observation" signal on its own. dedupWatchesLocked's merge
			// (migrate.go) can recompute a survivor's LowestPrice from a
			// duplicate it merges away without touching the survivor's own
			// LastPrice, leaving LastPrice == 0 beside a nonzero LowestPrice --
			// a real prior observation this guard would otherwise miss, letting
			// a currency-mismatched poll compare a NEW-currency price against
			// the OLD-currency LowestPrice.
			hasPriorObservation := cur.LastPrice > 0 || cur.LowestPrice > 0
			// Round 19: cur.Currency == "" is NOT a safe "no mismatch possible"
			// signal once there is prior history. Load normalizes a legacy
			// whitespace-only stored currency to "", so a watch can carry real
			// history denominated in an unknown currency while reading empty.
			unknownCurrencyWithHistory := cur.Currency == "" && hasPriorObservation
			currencyMismatch := currency != "" && ((cur.Currency != "" && cur.Currency != currency) || unknownCurrencyWithHistory)
			currencyChanged := hasPriorObservation && currencyMismatch
			firstQuoteMismatch := !hasPriorObservation && currencyMismatch
			skipThresholdChecks := currencyChanged || firstQuoteMismatch

			// Signals derived from the committed prior price and the committed
			// threshold. Deriving them from the stale copy let a late poll
			// recompute a drop against a price a concurrent check had already
			// advanced, and fire a second webhook for one move.
			if cur.LastPrice > 0 {
				priceDrop = price - cur.LastPrice
			}
			if !skipThresholdChecks {
				if signal := detectWatchLastMinuteDeal(*cur, price); signal.Triggered {
					lastMinute = true
					lastMinutePct = signal.DiscountPercent
				}
				if cur.BelowPrice > 0 && price <= cur.BelowPrice {
					belowGoal = true
				}
			}

			if currencyMismatch {
				// Round 21: record that a REAL threshold was lost, so notify.go
				// and the MCP DTO can say so. Silently wiping BelowPrice left
				// users with no way to know short of noticing alerts stopped.
				if cur.BelowPrice > 0 || cur.AlertDropAbs > 0 {
					alertsCleared = true
				}
				// Round 16: BelowPrice/AlertDropAbs are absolute and
				// currency-denominated, so they cannot survive ANY currency
				// adoption -- not merely a change with prior observations.
				cur.BelowPrice = 0
				// Round 17: zeroing AlertDropAbs when it is the watch's ONLY
				// threshold leaves Threshold.effective() reading both limbs as
				// zero and silently substituting DefaultDropPercent -- a policy
				// the user never chose. Mark it pending reconfirmation so the
				// Evaluate below suspends alerting until applyIntent supplies a
				// fresh threshold.
				if cur.AlertDropAbs > 0 && cur.AlertDropPct <= 0 {
					cur.AlertDropAbsClearedByCurrency = true
				}
				cur.AlertDropAbs = 0
			}
			if currencyChanged {
				cur.LowestPrice = 0
				cur.CheapestDate = ""
				cur.BaselinePrice = 0
				cur.LastAlertedPrice = 0
				// Round 15: PrevPrice was captured above in the OLD currency.
				// Left as-is it surfaces an old-currency price beside this
				// result's NEW currency -- MCP and the notifier read
				// PrevPrice+Currency as one pair -- and a transition the
				// notifier cannot express renders as "unchanged". Mask it so
				// callers see "no comparable prior price".
				prevPrice = 0
				priceDrop = 0
			}

			cur.LastCheck = time.Now()
			cur.LastPrice = price
			cur.Currency = currency
			if cheapestDate != "" {
				cur.CheapestDate = cheapestDate
			}
			if cur.LowestPrice == 0 || price < cur.LowestPrice {
				cur.LowestPrice = price
			}

			// Proactive price-drop alert: capture/track a baseline and fire
			// exactly one alert when the fare falls past the configured
			// threshold. State is stored on the watch so it survives daemon
			// restarts and reloads.
			//
			// Round 17 guard, reading cur: when AlertDropAbs was force-zeroed as
			// the watch's only threshold, skip Evaluate entirely rather than let
			// Threshold.effective() substitute DefaultDropPercent.
			if !(cur.AlertDropAbsClearedByCurrency && cur.AlertDropPct <= 0 && cur.AlertDropAbs <= 0) {
				state, a, fired := pricealert.Evaluate(
					pricealert.State{Baseline: cur.BaselinePrice, LastAlertedAt: cur.LastAlertedPrice},
					price,
					pricealert.Threshold{DropPercent: cur.AlertDropPct, DropAbsolute: cur.AlertDropAbs},
				)
				cur.BaselinePrice = state.Baseline
				cur.LastAlertedPrice = state.LastAlertedAt
				alert, alertFired = a, fired
			}

			// Purge decided here, from committed state: if another process
			// already migrated this watch, cur.Currency now matches the quote,
			// currencyChanged is false, and its new-currency history survives.
			return currencyChanged
		})
		if err != nil {
			result.Error = fmt.Errorf("update watch and record price: %w", err)
			return result
		}
		result.PrevPrice = prevPrice
		result.PriceDrop = priceDrop
		result.BelowGoal = belowGoal
		result.LastMinuteDeal = lastMinute
		result.LastMinuteDiscountPercent = lastMinutePct
		result.AlertsClearedByCurrencyChange = alertsCleared
		if alertFired {
			result.PriceDropAlert = true
			result.AlertBaseline = alert.Baseline
			result.AlertDropPercent = alert.DropPercent
		}
		w = saved

		// Update the result's watch to reflect saved state.
		result.Watch = w

		// Fire webhook on price drop. The webhook context can outlive the check
		// timeout, but should still be canceled when the scheduler stops.
		if result.PriceDrop < 0 || result.LastMinuteDeal {
			go fireWebhook(webhookCtx, result)
		}
	}

	return result
}

func detectWatchLastMinuteDeal(w Watch, currentPrice float64) hotelarb.LastMinuteSignal {
	if !w.LastMinuteMode || w.Type != "hotel" {
		return hotelarb.LastMinuteSignal{}
	}
	checkIn := w.DepartDate
	if checkIn == "" {
		checkIn = w.DepartFrom
	}
	parsed, err := time.Parse(watchDateLayout, checkIn)
	if err != nil {
		return hotelarb.LastMinuteSignal{}
	}
	return hotelarb.DetectLastMinuteDeal(time.Now(), parsed, w.LastPrice, currentPrice, hotelarb.LastMinuteOptions{
		DropPercentThreshold: w.LastMinuteDropPct,
	})
}

// checkRoom performs a room availability check for a room watch.
func normalizeCheckAndWebhookContexts(checkCtx, webhookCtx context.Context) (context.Context, context.Context) {
	if checkCtx == nil {
		checkCtx = context.Background()
	}
	if webhookCtx == nil {
		webhookCtx = checkCtx
	}
	return checkCtx, webhookCtx
}

// webhookPayload is the JSON body POSTed to a watch's WebhookURL on price drop.
type webhookPayload struct {
	WatchID                   string  `json:"watch_id"`
	Type                      string  `json:"type"`
	Origin                    string  `json:"origin,omitempty"`
	Destination               string  `json:"destination,omitempty"`
	HotelName                 string  `json:"hotel_name,omitempty"`
	NewPrice                  float64 `json:"new_price"`
	PrevPrice                 float64 `json:"prev_price"`
	Currency                  string  `json:"currency"`
	PriceDrop                 float64 `json:"price_drop"`
	BelowGoal                 bool    `json:"below_goal"`
	LastMinuteDeal            bool    `json:"last_minute_deal,omitempty"`
	LastMinuteDiscountPercent float64 `json:"last_minute_discount_percent,omitempty"`
	Timestamp                 string  `json:"timestamp"`
}

// fireWebhook sends a price-drop notification to the watch's WebhookURL.
// It is fire-and-forget with a 10-second timeout; errors are logged but not returned.
func fireWebhook(ctx context.Context, r CheckResult) {
	if r.Watch.WebhookURL == "" {
		return
	}

	// Round 22: reject anything but plain http/https up front -- a scheme
	// like "file://" or "gopher://" has no business here and some of Go's
	// non-HTTP RoundTrippers (if ever configured) would otherwise treat it
	// as a local resource read rather than a network request.
	parsedURL, err := url.Parse(r.Watch.WebhookURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		slog.Warn("webhook: rejecting unsupported URL scheme", "watch_id", r.Watch.ID)
		return
	}

	payload := webhookPayload{
		WatchID:                   r.Watch.ID,
		Type:                      r.Watch.Type,
		Origin:                    r.Watch.Origin,
		Destination:               r.Watch.Destination,
		HotelName:                 r.Watch.HotelName,
		NewPrice:                  r.NewPrice,
		PrevPrice:                 r.PrevPrice,
		Currency:                  r.Currency,
		PriceDrop:                 r.PriceDrop,
		BelowGoal:                 r.BelowGoal,
		LastMinuteDeal:            r.LastMinuteDeal,
		LastMinuteDiscountPercent: r.LastMinuteDiscountPercent,
		Timestamp:                 time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("webhook: marshal payload", "watch_id", r.Watch.ID, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		slog.Warn("webhook: create request", "watch_id", r.Watch.ID, "err", webhookSafeErr(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := webhookHTTPClient.Do(req)
	if err != nil {
		// Round 24: log scheme+host only, never url.Redacted(). Redacted() masks
		// only an embedded userinfo password -- it preserves username, path and
		// query, which is exactly where Slack/Discord-style webhook tokens live.
		// webhookSafeErr additionally unwraps *url.Error, whose Error() string
		// re-embeds the FULL request URL and would otherwise undo the redaction on
		// the same log line. Found by GPT second-opinion review, 2026-07-31.
		slog.Warn("webhook: POST failed", "watch_id", r.Watch.ID, "host", webhookLogTarget(r.Watch.WebhookURL), "err", webhookSafeErr(err))
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Round 25: with CheckRedirect refusing to follow, a redirecting
		// receiver now surfaces here as a 3xx instead of silently succeeding
		// -- log it as an undelivered notification rather than treating a
		// redirect response as delivery.
		safeHost := parsedURL.Scheme + "://" + parsedURL.Host
		slog.Warn("webhook: receiver redirected, notification not delivered", "watch_id", r.Watch.ID, "host", safeHost, "status", resp.StatusCode)
		return
	}
	if resp.StatusCode >= 400 {
		// Grok round-25 optional finding #1: only the 3xx branch above ever
		// logged a non-2xx response -- a receiver returning 4xx (bad
		// payload, auth failure, rate limit) or 5xx (outage) failed the
		// delivery just as silently as a redirect, with nothing in the logs
		// to explain a "why did my webhook never fire" report. Treat 4xx/5xx
		// the same as a redirect: log host + status as undelivered, never
		// the response body (could echo the request back, including any
		// token embedded in the URL by the receiver's own error page).
		// Fixed as trvl#547.
		safeHost := parsedURL.Scheme + "://" + parsedURL.Host
		slog.Warn("webhook: receiver returned an error status, notification not delivered", "watch_id", r.Watch.ID, "host", safeHost, "status", resp.StatusCode)
	}
}

// webhookLogTarget reduces a user-supplied webhook URL to a form that is safe to
// log. Slack and Discord both carry the shared secret in the PATH, so the path,
// query and fragment are all dropped and only the host survives. A URL that does
// not parse yields a constant rather than an echo of the input, because the
// unparseable case is precisely where a malformed secret would otherwise ride
// through.
func webhookLogTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return u.Host
}

// webhookSafeErr strips the URL out of a *url.Error.
//
// This is the part that is easy to miss: net/http returns *url.Error from both
// NewRequestWithContext and Client.Do, and url.Error.Error() prints the full URL
// it was given. Redacting the "url" log attribute alone would therefore still
// disclose the secret through the "err" attribute on the very same line.
func webhookSafeErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s %s: %w", ue.Op, webhookLogTarget(ue.URL), ue.Err)
	}
	return err
}
