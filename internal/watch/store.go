package watch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
)

// Scaling guards for ad-hoc route observations (MIK-6229 improve pass).
const (
	// maxObservationsPerRoute caps retained points per route key, bounding the
	// price-history file to cap x number-of-routes.
	maxObservationsPerRoute = 1000
	// maxObservationsPerWatch caps retained points per watch. Watch-keyed points
	// were originally exempt from every cap, which left the only truly unbounded
	// corpus in the store: one real price-history.json reached 320,028 points in
	// 41MB, of which 319,966 were watch-keyed and 36 were the capped route kind.
	// Loading that into memory is what made each `trvl mcp` process cost ~686MB.
	//
	// Nothing needs the raw tail: Sparkline asks for 10-20 points, and a watch's
	// all-time low is stored on the Watch record itself (LowestPrice /
	// CheapestDate), so it survives eviction independently of history.
	// At the 30-minute scheduler cadence this retains roughly three weeks of
	// full-resolution history per watch.
	maxObservationsPerWatch = 1000
	// maxWatchObservations is the global backstop across all watches, evicting
	// oldest first, so a large number of watches cannot multiply the per-watch
	// cap into an unbounded file.
	maxWatchObservations = 50000
	// routeWatchTTL is how long a dateless route watch keeps being checked
	// without the user re-expressing interest. Route watches have no travel date
	// to expire against, so isActive returned true for them unconditionally and
	// they accumulated forever: one real store carried 468 permanently-active
	// route watches, every one re-checked against live providers every 30
	// minutes. Re-watching a route renews it, so anything actually in use stays.
	routeWatchTTL = 90 * 24 * time.Hour
	// observationThrottle suppresses near-identical repeat observations for the
	// same route+currency within this window.
	observationThrottle = 15 * time.Minute
	// observationEpsilonPct is the relative price delta below which a throttled
	// observation is treated as a duplicate.
	observationEpsilonPct = 0.005
)

// Store manages persistence of watches and price history to disk.
// All methods are safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	dir     string
	watches []Watch
	history []PricePoint
}

// NewStore creates a store rooted at the given directory (typically ~/.trvl/).
// The directory is created on first write if it does not exist.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// DefaultStore returns a store at ~/.trvl/.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return NewStore(filepath.Join(home, ".trvl")), nil
}

func (s *Store) watchesPath() string {
	return filepath.Join(s.dir, "watches.json")
}

func (s *Store) historyPath() string {
	return filepath.Join(s.dir, "price-history.json")
}

func (s *Store) ensureDir() error {
	return os.MkdirAll(s.dir, 0o700)
}

// Load reads watches and history from disk. If the files do not exist,
// the store starts empty (not an error).
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// loadLocked is Load for callers that already hold s.mu -- notably withTxn,
// which reloads committed state inside a transaction so a mutation is applied
// to what is on disk rather than to whatever snapshot this process was holding
// (#512). Caller holds s.mu.
func (s *Store) loadLocked() error {
	s.watches = nil
	s.history = nil

	if err := loadJSON(s.watchesPath(), &s.watches); err != nil {
		return fmt.Errorf("load watches: %w", err)
	}
	if err := loadJSON(s.historyPath(), &s.history); err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	// Load is PURE READ. It deliberately performs no migration and never writes.
	//
	// An earlier revision stamped RenewedAt here and persisted it, which made
	// every process rewrite the whole store at startup — reintroducing exactly
	// the last-writer-wins hazard that batching was rejected for, on a hotter
	// path. Migration now lives in an explicit, reviewable command
	// (Store.Migrate, exposed as `trvl watch migrate`) that backs up first and
	// runs once, rather than implicitly in every reader.
	//
	// Round 18's currency canonicalization is the one exception, and
	// deliberately not a Migrate-only fix: it normalizes s.watches[i].Currency
	// and s.history[i].Currency in memory ONLY, issuing no write of its own --
	// this is not the RenewedAt mistake, which added a write Load never used
	// to make. Without it, every watch/history point written before this round
	// (or by any client that skips normalization) stays in its on-disk case
	// forever, and Add/check.go now normalize only the FRESH side of each
	// comparison (see round 18's finding above) -- a stored "usd" would
	// compare unequal to a freshly-normalized "USD" on the very next re-watch
	// or poll, wrongly reading a same-currency re-watch as a currency change
	// and wiping real accumulated state. The normalized value naturally rides
	// out on the next already-scheduled Save, same as any other in-memory
	// mutation between Load and Save. Found by GPT second-opinion review,
	// 2026-07-30 (round 18).
	for i := range s.watches {
		s.watches[i].Currency = strings.ToUpper(strings.TrimSpace(s.watches[i].Currency))
	}
	for i := range s.history {
		s.history[i].Currency = strings.ToUpper(strings.TrimSpace(s.history[i].Currency))
	}
	return nil
}

// Save writes watches and history to disk atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	return s.persistLocked()
}

// persistLocked writes both files. Caller holds s.mu.
//
// NOTE: this rewrites the store WHOLE. It is deliberately not batched across a
// scheduler round: deferring the flush would hold a stale in-memory snapshot for
// the length of the round, and another `trvl mcp` process persisting a new watch
// in that window would be silently overwritten (the store is last-writer-wins
// across processes). The write volume that motivated batching is already
// addressed by watch dedup, the history cap, and the scheduler singleton —
// together ~99.8%% — so batching bought ~25MB per round in exchange for a
// multi-minute data-loss window. See docs/design for the store-coordination gap.
func (s *Store) persistLocked() error {
	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	if err := saveJSON(s.watchesPath(), s.watches); err != nil {
		return fmt.Errorf("save watches: %w", err)
	}
	if err := saveJSON(s.historyPath(), s.history); err != nil {
		return fmt.Errorf("save history: %w", err)
	}
	return nil
}

// Add inserts a watch, or returns the existing watch for the same user intent,
// and persists. Returns the watch ID and whether a NEW watch was created.
//
// The `created` flag matters because Add is idempotent: callers used to report
// "Added watch <id>" unconditionally and hand back an ID that was not new, which
// is simply false on every re-watch.
//
// Identity is dedupeKey, which is the polled TARGET alone. Re-watching a route
// with a different target price ADJUSTS the existing watch rather than adding a
// second one (operator decision, 2026-08-02, reversing #509's threshold-aware
// identity -- see dedupeKey for what that costs and why it was accepted).
//
// DIVERGENCE resolved here, worth knowing about: the release line folded
// Currency into the SAME key it deduplicated on, which made a re-watch in a new
// currency fork a second watch. main's SameTarget excluded Currency so the
// re-watch migrated the existing one. Both rationales were written
// deliberately, and both are right about their own job — see targetKey. Poll
// identity keeps currency (two currencies are two provider requests); dedupe
// identity drops it (a currency change is the same intent re-expressed).
//
// Runs inside a store transaction: the existence check and the append must be
// atomic against another process doing the same thing, or both observe "absent"
// and both append (#512).
func (s *Store) Add(w Watch) (string, bool, error) {
	if err := w.Validate(); err != nil {
		return "", false, err
	}
	// Normalize once at the single entry point every caller-supplied watch
	// passes through, so every currency comparison downstream (applyIntent
	// below, migrate.go's dedup merge, check.go's mismatch checks) sees a
	// consistent case. A caller-supplied "eur" next to an existing "EUR"
	// watch would otherwise satisfy every `!=` comparison and be misread as
	// a real currency change, needlessly wiping accumulated history and
	// thresholds. Found by adversarial review, 2026-07-30 (round 18).
	w.Currency = strings.ToUpper(strings.TrimSpace(w.Currency))

	key := w.dedupeKey()
	var id string
	created := false
	err := s.withTxn(func() error {
		if i := findByDedupeKeyIndex(s.watches, key); i >= 0 {
			// Same intent already stored. Accumulated price history
			// (LowestPrice, BaselinePrice, LastCheck, ...) is preserved — that
			// history is the value of a long-running watch and must survive a
			// re-watch.
			if s.watches[i].applyIntent(w) {
				// Currency changed. applyIntent already reset this watch's own
				// currency-denominated fields; the history corpus needs the
				// same treatment. Every PricePoint recorded for this watch is
				// denominated in the OLD currency, and Sparkline/TrendArrow/
				// RoutePrices make no currency distinction within a single
				// watch's series — leaving them in place would plot (and could
				// re-derive a "low" from) numbers in a currency the watch no
				// longer reports. Purge rather than convert: no FX rate is
				// available at this layer. Found by adversarial review,
				// 2026-07-28.
				s.purgeHistoryLocked(s.watches[i].ID)
			}
			id = s.watches[i].ID
			return nil
		}
		w.ID = shortID()
		w.CreatedAt = time.Now()
		w.RenewedAt = w.CreatedAt
		s.watches = append(s.watches, w)
		id, created = w.ID, true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return id, created, nil
}

// applyIntent copies the caller-adjustable fields of `next` onto an existing
// watch, leaving identity and accumulated observation state untouched.
//
// Zero values do not overwrite: a re-watch that omits a webhook must not silently
// delete the webhook already configured on that route.
//
// Returns true if Currency actually changed. Currency is deliberately not
// part of SameTarget's identity (a re-watch with a new target currency
// updates the existing watch rather than forking a rival one), but every
// SYSTEM-COMPUTED numeric field this struct carries -- LastPrice,
// LowestPrice, CheapestDate, BaselinePrice, LastAlertedPrice -- is
// denominated in the OLD currency. Leaving them in place after a currency
// change is not a display quirk: the next check compares a NEW-currency
// price against an OLD-currency "last price," which can fabricate a huge
// fake drop (e.g. a ~20,000 JPY watch re-added in EUR compares a ~180 EUR
// price against 20,000 as if both were the same unit) and fire false
// alerts/webhooks off it. Reset rather than convert: no FX rate is
// available at this layer, and a fresh baseline in the new currency is
// correct, not merely simpler.
//
// User-SET thresholds (BelowPrice, AlertDropPct, AlertDropAbs) are
// adjustable on any re-watch, the caller's own choice each time, consistent
// with this function's existing design (see the doc comment on SameTarget).
// Found by adversarial review, 2026-07-28.
//
// Exception: BelowPrice and AlertDropAbs are ABSOLUTE currency-denominated
// magnitudes, same as the system-computed fields above. On a currency
// change, both are zeroed unless the SAME call supplies a fresh value --
// otherwise an old-currency absolute threshold silently persists attached
// to the new currency (e.g. a JPY re-watch to EUR that only changes
// AlertDropPct keeps comparing quotes against a stale JPY BelowPrice as if
// it were EUR). AlertDropPct is a percentage and is currency-invariant, so
// it is never touched by a currency change. Found by adversarial review,
// 2026-07-30 (round 15).
func (w *Watch) applyIntent(next Watch) (currencyChanged bool) {
	// Re-watching is the renewal signal that keeps a route watch alive.
	w.RenewedAt = time.Now()
	if next.Currency != "" && next.Currency != w.Currency {
		currencyChanged = true
		w.Currency = next.Currency
		w.LastPrice = 0
		w.LowestPrice = 0
		w.CheapestDate = ""
		w.BaselinePrice = 0
		w.LastAlertedPrice = 0
		// BelowPrice and AlertDropAbs are absolute magnitudes denominated in
		// the OLD currency, exactly like the system-computed fields above --
		// a re-watch that changes currency but omits a fresh threshold (e.g.
		// switches JPY->EUR while only touching AlertDropPct) must not leave
		// the old JPY absolute values silently attached to the new EUR
		// watch. Zero both here; the explicit-override blocks below layer
		// any caller-supplied new-currency value back on top in the same
		// call. Found by adversarial review, 2026-07-30 (round 15).
		w.BelowPrice = 0
		// Round 17: same regression as check.go's currencyMismatch handling
		// -- if AlertDropAbs was this watch's ONLY threshold (AlertDropPct
		// <= 0), zeroing it here lets pricealert.Evaluate's
		// Threshold.effective() silently substitute DefaultDropPercent
		// (10%) on the next poll unless a fresh value is supplied in THIS
		// same call. Mark it pending reconfirmation; the explicit-override
		// blocks below clear the marker the instant the caller supplies
		// either limb. Found by adversarial review, 2026-07-30 (round 17).
		if w.AlertDropAbs > 0 && w.AlertDropPct <= 0 {
			w.AlertDropAbsClearedByCurrency = true
		}
		w.AlertDropAbs = 0
	}
	if next.BelowPrice > 0 {
		w.BelowPrice = next.BelowPrice
	}
	if next.WebhookURL != "" {
		w.WebhookURL = next.WebhookURL
	}
	if next.AlertDropPct > 0 {
		w.AlertDropPct = next.AlertDropPct
		w.AlertDropAbsClearedByCurrency = false
	}
	if next.AlertDropAbs > 0 {
		w.AlertDropAbs = next.AlertDropAbs
		w.AlertDropAbsClearedByCurrency = false
	}
	if next.LastMinuteMode {
		w.LastMinuteMode = true
	}
	// Gated on LastMinuteMode, not merely on a positive value.
	//
	// Callers supply a 25% default for this field whether or not the request
	// mentions last-minute mode at all (mcp/tools_watch_price.go argFloat
	// default, and the CLI's --last-minute-drop flag default). A positive value
	// therefore does NOT mean "the caller asked for 25" -- it usually means the
	// caller said nothing. Applying it anyway overwrote a stored custom
	// threshold with the default on every re-watch.
	//
	// That became a main-path bug when re-watching turned into the way a user
	// changes their target price (operator decision, 2026-08-02): adjusting the
	// price on a hotel watch silently reset a 40% last-minute threshold to 25%.
	// Found by grok second-opinion review, 2026-08-02.
	if next.LastMinuteMode && next.LastMinuteDropPct > 0 {
		w.LastMinuteDropPct = next.LastMinuteDropPct
	}
	return currencyChanged
}

// List returns all active watches.
func (s *Store) List() []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Watch, len(s.watches))
	copy(out, s.watches)
	return out
}

// Get returns a single watch by ID, or false if not found.
func (s *Store) Get(id string) (Watch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, w := range s.watches {
		if w.ID == id {
			return w, true
		}
	}
	return Watch{}, false
}

// Remove deletes a watch by ID. Returns true if found and removed.
func (s *Store) Remove(id string) (bool, error) {
	found := false
	err := s.withTxn(func() error {
		for i, w := range s.watches {
			if w.ID == id {
				s.watches = append(s.watches[:i], s.watches[i+1:]...)
				found = true
				return nil
			}
		}
		// Nothing to remove: unwind without rewriting both files. Saving here
		// would republish this process's whole snapshot over a concurrent
		// writer's, which is the #512 hazard on a no-op path.
		return errTxnNoop
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// UpdateWatch replaces a watch in-place by ID and persists.
func (s *Store) UpdateWatch(updated Watch) error {
	return s.withTxn(func() error {
		for i, w := range s.watches {
			if w.ID == updated.ID {
				s.watches[i] = updated
				return nil
			}
		}
		return fmt.Errorf("watch %s not found", updated.ID)
	})
}

// PurgeHistory drops every PricePoint recorded for watchID and persists.
//
// Callers use this when a watch's currency changes outside of Store.Add's
// own re-watch path (e.g. a mid-poll currency flip detected in
// checkOneWithWebhookContext / checkRoomWithWebhookContext): every PricePoint
// already recorded is denominated in the OLD currency, and
// History/Sparkline/TrendArrow make no currency distinction within a single
// watch's series, so leaving them in place would plot (and could re-derive a
// "low" from) numbers in a currency the watch no longer reports. Purge
// rather than convert: no FX rate is available at this layer. Found by
// adversarial review, 2026-07-29 (round 10).
func (s *Store) PurgeHistory(watchID string) error {
	return s.withTxn(func() error {
		s.purgeHistoryLocked(watchID)
		return nil
	})
}

// purgeHistoryLocked drops every PricePoint recorded for watchID. Caller
// holds s.mu.
func (s *Store) purgeHistoryLocked(watchID string) {
	kept := s.history[:0:0]
	for _, p := range s.history {
		if p.WatchID != watchID {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// RecordPrice appends a price point to history and persists.
//
// It also updates the watch's LastPrice/LowestPrice scalar fields, if the
// watch exists AND the recorded currency agrees with the watch's own
// Currency (or the watch has none set yet). Those two fields are what
// checkOneWithWebhookContext's hasPriorObservation gate (round 18) reads to
// decide whether a currency mismatch is a real change or a first-quote
// baseline; a caller that appends history through this method without going
// through the normal UpdateWatchAndRecordPrice poll path would otherwise
// build real history while hasPriorObservation still reports false, letting
// a later currency change slip past the mismatch guard undetected. Found by
// GPT second-opinion review, 2026-07-30 (round 18).
//
// Round 19 found the round-18 fix synced LastPrice/LowestPrice
// unconditionally, so a caller recording a DIFFERENT currency than the
// watch's own (e.g. a USD watch fed a JPY observation) left the watch's
// scalars silently mislabeled -- a USD-tagged watch carrying a raw JPY
// number, with LowestPrice possibly "dropping" to a JPY value that is
// numerically smaller but not actually cheaper. Only sync when the
// currencies agree, or the watch has not yet recorded one (first
// observation establishes the baseline currency implicitly, same as the
// poll path). Found by GPT second-opinion review, 2026-07-30 (round 19).
// Round 20 found two remaining gaps. First, the round-19 fix appended the
// observation to history BEFORE checking currency, so a mismatched
// observation still landed in history even though its scalar sync was
// skipped -- a USD watch fed a JPY point kept mixed-currency history that
// Sparkline/TrendArrow (watch.go) then compare as raw numbers with no
// currency filtering. Second, a currencyless watch given a labeled
// observation never adopted that currency, so a LATER poll in that same
// currency misclassified this very history as unknownCurrencyWithHistory
// (checkOneWithWebhookContext's round-19 guard) and purged it. Reject a
// mismatched observation outright (no append, no sync) instead of building
// history this raw recorder cannot reconcile -- a currency change must go
// through the full reset the check.go poll path performs, which this method
// does not -- and adopt the recorded currency as the watch's baseline on its
// first observation, mirroring the poll path's own first-quote handling.
// Found by GPT second-opinion review, 2026-07-30 (round 20).
func (s *Store) RecordPrice(watchID string, price float64, currency string) error {
	return s.withTxn(func() error {
		rawCurrency := currency
		cur := strings.ToUpper(strings.TrimSpace(currency))
		if price > 0 && cur != "" && !IsValidCurrencyFormat(cur) {
			return fmt.Errorf("record price: malformed currency %q", rawCurrency)
		}
		if price > 0 && cur == "" && rawCurrency != "" {
			return fmt.Errorf("record price: unusable currency %q", rawCurrency)
		}

		if price > 0 {
			for i := range s.watches {
				if s.watches[i].ID != watchID {
					continue
				}
				hasPriorObservation := s.watches[i].LastPrice > 0 || s.watches[i].LowestPrice > 0
				watchCurrency := s.watches[i].Currency
				unknownCurrencyWithHistory := watchCurrency == "" && hasPriorObservation
				// Round 21 found two remaining gaps in this gate. First, reject
				// was previously conditioned on `mismatch && hasPriorObservation`:
				// a watch with an explicit non-empty Currency (set at
				// watch-creation, before any observation) but zero prior
				// observations fell through this gate untouched -- it silently
				// accepted a differently-labeled first observation, kept its
				// stale Currency label, and stored mismatched-currency
				// scalars/history under it. The Currency field IS the
				// established baseline the moment it's non-empty; it does not
				// need a prior observation to mean something. Second, an
				// empty-currency observation was unconditionally accepted onto
				// an established (non-empty Currency) watch because the
				// mismatch formula required `cur != ""` -- reject that too,
				// rather than mixing an unlabeled scalar/history point into a
				// series the rest of the package assumes is single-currency.
				// Found by GPT second-opinion review, 2026-07-30 (round 21).
				if watchCurrency != "" && cur != "" && watchCurrency != cur {
					return fmt.Errorf("record price: currency %q does not match watch currency %q", cur, watchCurrency)
				}
				if watchCurrency != "" && cur == "" {
					return fmt.Errorf("record price: missing currency for watch established in %q", watchCurrency)
				}
				if unknownCurrencyWithHistory && cur != "" {
					// Round 20: a currencyless watch that already has real
					// price history cannot safely accept ANY labeled
					// observation through this raw recorder -- that requires
					// the full reset check.go's poll path performs, which
					// RecordPrice does not.
					return fmt.Errorf("record price: currency %q does not match watch currency %q", cur, watchCurrency)
				}
				if watchCurrency == "" && cur != "" {
					s.watches[i].Currency = cur
				}
				s.watches[i].LastPrice = price
				if s.watches[i].LowestPrice == 0 || price < s.watches[i].LowestPrice {
					s.watches[i].LowestPrice = price
				}
				break
			}
		}

		s.history = append(s.history, PricePoint{
			WatchID:   watchID,
			Price:     price,
			Currency:  cur,
			Timestamp: time.Now(),
		})
		s.pruneWatchLocked(watchID)
		s.pruneGlobalWatchLocked()
		return nil
	})
}

// pruneWatchLocked drops the oldest observations for watchID beyond
// maxObservationsPerWatch, preserving order. Caller holds s.mu.
func (s *Store) pruneWatchLocked(watchID string) {
	s.evictOldestLocked(
		func(p PricePoint) bool { return p.WatchID == watchID },
		maxObservationsPerWatch,
	)
}

// pruneGlobalWatchLocked evicts the oldest watch-keyed observations once their
// total exceeds maxWatchObservations, so many watches cannot multiply the
// per-watch cap into an unbounded file. Route-keyed points have their own cap
// and are not touched here. Caller holds s.mu.
func (s *Store) pruneGlobalWatchLocked() {
	s.evictOldestLocked(
		func(p PricePoint) bool { return p.WatchID != "" },
		maxWatchObservations,
	)
}

// evictOldestLocked keeps at most `limit` points matching `match`, dropping the
// oldest first and preserving the order of everything else. Caller holds s.mu.
func (s *Store) evictOldestLocked(match func(PricePoint) bool, limit int) {
	var idx []int
	for i, p := range s.history {
		if match(p) {
			idx = append(idx, i)
		}
	}
	if len(idx) <= limit {
		return
	}
	drop := make(map[int]bool, len(idx)-limit)
	for _, i := range idx[:len(idx)-limit] {
		drop[i] = true
	}
	kept := s.history[:0:0]
	for i, p := range s.history {
		if !drop[i] {
			kept = append(kept, p)
		}
	}
	s.history = kept
}

// History returns all price points for a given watch ID, ordered by time.
func (s *Store) History(watchID string) []PricePoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []PricePoint
	for _, p := range s.history {
		if p.WatchID == watchID {
			out = append(out, p)
		}
	}
	return out
}

// shortID generates a 4-byte hex string (8 characters).
func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp-based ID
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(b)
}

// loadJSON reads a JSON file into dst. Returns nil if file does not exist.
func loadJSON(path string, dst interface{}) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, dst)
}

// saveJSON writes data as pretty-printed JSON.
func saveJSON(path string, data interface{}) error {
	return atomicjson.Write(path, data)
}

// Dir returns the directory backing this store.
func (s *Store) Dir() string { return s.dir }
