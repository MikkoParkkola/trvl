package watch

import (
	"testing"
	"time"
)

// Store.Migrate and its backup.
//
// Split out of store_lifecycle_test.go, which crossed the 800-line ceiling.
// The seam is subject: migration is an explicit, backed-up, one-shot command,
// distinct from the load/save/add lifecycle the other file covers.

// Migrate must collapse duplicates the ad-hoc cleanup missed: DATED watches too,
// not just dateless route ones. A real store kept 380 copies of one room watch
// because the earlier pass only handled route watches.
func TestMigrateCollapsesDatedDuplicates(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	room := Watch{
		Type: "room", Destination: "AMS", HotelName: "Hilton",
		RoomKeywords: []string{"king"}, DepartDate: "2026-06-15", ReturnDate: "2026-06-18",
		BelowPrice: 200, Currency: "EUR",
	}
	// Bypass Add's dedup to build a pre-fix store.
	for i := 0; i < 380; i++ {
		w := room
		w.ID = shortID()
		w.CreatedAt = time.Now()
		s.watches = append(s.watches, w)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	rep, err := s.Migrate()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := len(s.List()); got != 1 {
		t.Errorf("380 identical room watches collapsed to %d, want 1", got)
	}
	if rep.DuplicatesRemoved != 379 {
		t.Errorf("reported %d duplicates removed, want 379", rep.DuplicatesRemoved)
	}
}

// The surviving record must be the richest one, so collapsing never trades away
// accumulated price history for an empty newer copy.
func TestMigrateKeepsRichestDuplicate(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}

	empty := base
	empty.ID = "empty"
	empty.CreatedAt = time.Now()

	rich := base
	rich.ID = "rich"
	rich.LowestPrice = 85
	rich.LastCheck = time.Now()
	rich.CreatedAt = time.Now().Add(-90 * 24 * time.Hour)

	s.watches = []Watch{empty, rich}
	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got := s.List()[0]
	if got.ID != "rich" {
		t.Errorf("collapse kept %q, want the record carrying price history", got.ID)
	}
	if got.LowestPrice != 85 {
		t.Errorf("collapse lost the all-time low: %v", got.LowestPrice)
	}
}

// Regression for the adversarial-review finding, 2026-07-29: when both
// duplicates in a group already carry observations, the surviving record's
// LowestPrice must be the true minimum of the two, not whichever duplicate
// happens to win on recency. A duplicate group with lows of 50 and 100 must
// never let migration keep 100 and silently erase the 50.
func TestMigrateMergesLowestPriceAcrossDuplicates(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}

	older := base
	older.ID = "older-cheaper"
	older.LowestPrice = 50
	older.LastCheck = time.Now().Add(-48 * time.Hour)
	older.CreatedAt = time.Now().Add(-90 * 24 * time.Hour)

	newer := base
	newer.ID = "newer-pricier"
	newer.LowestPrice = 100
	newer.LastCheck = time.Now()
	newer.CreatedAt = time.Now().Add(-1 * time.Hour)

	s.watches = []Watch{older, newer}
	s.history = []PricePoint{
		{WatchID: "older-cheaper", Price: 50, Timestamp: older.LastCheck},
		{WatchID: "newer-pricier", Price: 100, Timestamp: newer.LastCheck},
	}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 {
		t.Fatalf("duplicate group collapsed to %d watches, want 1", len(watches))
	}
	survivor := watches[0]
	if survivor.ID != "newer-pricier" {
		t.Fatalf("expected the more-recently-checked record to win identity, got %q", survivor.ID)
	}
	if survivor.LowestPrice != 50 {
		t.Errorf("LowestPrice = %v, want 50 (the group's true low must survive, not just the winner's recency)", survivor.LowestPrice)
	}

	// Neither duplicate's observation may be lost: both points must survive,
	// reassigned onto the surviving watch ID rather than dropped as orphans.
	var prices []float64
	for _, p := range s.history {
		if p.WatchID != "newer-pricier" {
			t.Errorf("history point still tagged with a collapsed-away ID %q", p.WatchID)
			continue
		}
		prices = append(prices, p.Price)
	}
	if len(prices) != 2 {
		t.Fatalf("history after migrate has %d points for the survivor, want 2 (50 and 100 both preserved)", len(prices))
	}
}

// A duplicate group can legitimately span two currencies (SameTarget ignores
// Currency; a route can be re-watched in a different one). Merging LowestPrice
// numerically across currencies would mislabel one currency's amount as
// another's -- e.g. reporting a EUR low as if it were JPY. Found by
// adversarial review, 2026-07-29.
func TestMigrateDoesNotMergeLowestPriceAcrossCurrencies(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200}

	eur := base
	eur.ID = "eur-watch"
	eur.Currency = "EUR"
	eur.LowestPrice = 50
	eur.LastCheck = time.Now().Add(-48 * time.Hour)

	jpy := base
	jpy.ID = "jpy-watch"
	jpy.Currency = "JPY"
	jpy.LowestPrice = 10000
	jpy.LastCheck = time.Now()

	s.watches = []Watch{eur, jpy}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 {
		t.Fatalf("duplicate group collapsed to %d watches, want 1", len(watches))
	}
	survivor := watches[0]
	if survivor.ID != "jpy-watch" {
		t.Fatalf("expected the more-recently-checked record to win identity, got %q", survivor.ID)
	}
	// The survivor is JPY; a EUR 50 has no meaning as a JPY price and must
	// never be inherited. The survivor's OWN JPY 10000 is still a valid JPY
	// observation, though, and collapseDuplicatesLocked recomputes the low
	// from every group member that shares the survivor's currency -- so the
	// correct result is the JPY side's own true low, not a blanket reset to
	// 0. (A reset to 0 was this test's expectation until round 3 of
	// adversarial review showed the blanket-reset version discarded a real
	// same-currency low when a chain mixed in a THIRD, same-currency record;
	// see TestMigrateRecoversTrueLowAcrossCurrencyResetMidChain.)
	if survivor.LowestPrice != 10000 {
		t.Errorf("LowestPrice = %v, want 10000 (the survivor's own JPY low; the EUR 50 must not be inherited, but the JPY 10000 must not be discarded either)", survivor.LowestPrice)
	}
}

// Duplicate chains run more than two deep in practice (one real store held 380
// copies of a single watch). Every collapsed ID in the chain must resolve to
// the FINAL survivor, not an intermediate record superseded by a later merge.
func TestMigrateReassignsHistoryAcrossChainOfThreeOrMoreDuplicates(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200, Currency: "EUR"}

	first := base
	first.ID = "first"
	first.LowestPrice = 80
	first.LastCheck = time.Now().Add(-72 * time.Hour)

	second := base
	second.ID = "second"
	second.LowestPrice = 50
	second.LastCheck = time.Now().Add(-24 * time.Hour)

	third := base
	third.ID = "third"
	third.LowestPrice = 100
	third.LastCheck = time.Now()

	s.watches = []Watch{first, second, third}
	s.history = []PricePoint{
		{WatchID: "first", Price: 80, Timestamp: first.LastCheck},
		{WatchID: "second", Price: 50, Timestamp: second.LastCheck},
		{WatchID: "third", Price: 100, Timestamp: third.LastCheck},
	}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 {
		t.Fatalf("duplicate chain collapsed to %d watches, want 1", len(watches))
	}
	survivor := watches[0]
	if survivor.ID != "third" {
		t.Fatalf("expected the most-recently-checked record to win identity, got %q", survivor.ID)
	}
	if survivor.LowestPrice != 50 {
		t.Errorf("LowestPrice = %v, want 50 (true low across the whole chain, not just the final pairwise merge)", survivor.LowestPrice)
	}

	var prices []float64
	for _, p := range s.history {
		if p.WatchID != "third" {
			t.Errorf("history point still tagged with a collapsed-away chain ID %q", p.WatchID)
			continue
		}
		prices = append(prices, p.Price)
	}
	if len(prices) != 3 {
		t.Fatalf("history after migrate has %d points for the survivor, want 3 (80, 50, and 100 all preserved)", len(prices))
	}
}

// A currency-mismatched merge resets LowestPrice to 0 (unset), which the
// PREVIOUS pairwise-merge implementation then fed straight into comparing
// against a LATER same-currency duplicate's own price -- so 0 always "won"
// as the lower value and a real, still-valid same-currency low from an
// earlier chain member was silently discarded. EUR 80 -> JPY 5,000 -> JPY
// 10,000 must keep the true JPY low of 5,000, not the second JPY record's
// value alone. Found by adversarial review, 2026-07-29 (round 3).
func TestMigrateRecoversTrueLowAcrossCurrencyResetMidChain(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200}

	eur := base
	eur.ID = "eur-watch"
	eur.Currency = "EUR"
	eur.LowestPrice = 80
	eur.LastCheck = time.Now().Add(-72 * time.Hour)

	jpyLow := base
	jpyLow.ID = "jpy-low"
	jpyLow.Currency = "JPY"
	jpyLow.LowestPrice = 5000
	jpyLow.CheapestDate = "2026-08-15"
	jpyLow.LastCheck = time.Now().Add(-24 * time.Hour)

	jpyHigh := base
	jpyHigh.ID = "jpy-high"
	jpyHigh.Currency = "JPY"
	jpyHigh.LowestPrice = 10000
	jpyHigh.CheapestDate = "2026-09-01"
	jpyHigh.LastCheck = time.Now()

	s.watches = []Watch{eur, jpyLow, jpyHigh}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 {
		t.Fatalf("duplicate chain collapsed to %d watches, want 1", len(watches))
	}
	survivor := watches[0]
	if survivor.Currency != "JPY" {
		t.Fatalf("survivor currency = %q, want JPY", survivor.Currency)
	}
	if survivor.LowestPrice != 5000 {
		t.Errorf("LowestPrice = %v, want 5000 (the true JPY low across both JPY records, not just the winner of the final pairwise step)", survivor.LowestPrice)
	}
	if survivor.CheapestDate != "2026-08-15" {
		t.Errorf("CheapestDate = %q, want 2026-08-15 (must track whichever record actually supplied the surviving low)", survivor.CheapestDate)
	}
}

// losing record's own price-history points are a currency series in their own
// right (EUR history under a EUR watch). Retagging them onto a JPY survivor
// via idMap would still mix the two currencies into one numeric series for
// History/Sparkline/TrendArrow even though the scalar LowestPrice was fixed.
// Found by adversarial review, 2026-07-29 (round 2).
func TestMigrateDoesNotRetagHistoryAcrossCurrencies(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200}

	eur := base
	eur.ID = "eur-watch"
	eur.Currency = "EUR"
	eur.LowestPrice = 50
	eur.LastCheck = time.Now().Add(-48 * time.Hour)

	jpy := base
	jpy.ID = "jpy-watch"
	jpy.Currency = "JPY"
	jpy.LowestPrice = 10000
	jpy.LastCheck = time.Now()

	s.watches = []Watch{eur, jpy}
	s.history = []PricePoint{
		{WatchID: "eur-watch", Price: 50, Currency: "EUR", Timestamp: eur.LastCheck},
		{WatchID: "jpy-watch", Price: 10000, Currency: "JPY", Timestamp: jpy.LastCheck},
	}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, p := range s.history {
		if p.WatchID == "eur-watch" {
			t.Errorf("EUR history point survived retagged onto the JPY survivor instead of being dropped as an orphan")
		}
	}
	watches := s.List()
	if len(watches) != 1 || watches[0].ID != "jpy-watch" {
		t.Fatalf("expected jpy-watch alone to survive, got %+v", watches)
	}
	for _, p := range s.history {
		if p.WatchID == "jpy-watch" && p.Currency != "JPY" {
			t.Errorf("surviving history point has currency %q, want JPY only", p.Currency)
		}
	}
}

// CheapestDate is documented (dedup_test.go) as tied to LowestPrice: whenever
// LowestPrice is recomputed, CheapestDate must come from the SAME record the
// price came from, never a stale date left over from a different currency's
// entry. The survivor here is JPY (more recent LastCheck); the EUR side's
// price AND date must both be excluded, but the JPY side's own true price and
// date must survive intact -- the group-materialization rewrite (round 3)
// recomputes the low from every group member sharing the survivor's
// currency, so a lone same-currency member keeps its own number rather than
// being blanket-reset to 0/empty. Found by adversarial review, 2026-07-29
// (round 2); assertion updated for the round-3 fix.
func TestMigrateClearsCheapestDateOnCrossCurrencyMerge(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200}

	eur := base
	eur.ID = "eur-watch"
	eur.Currency = "EUR"
	eur.LowestPrice = 50
	eur.CheapestDate = "2026-08-01"
	eur.LastCheck = time.Now().Add(-48 * time.Hour)

	jpy := base
	jpy.ID = "jpy-watch"
	jpy.Currency = "JPY"
	jpy.LowestPrice = 10000
	jpy.CheapestDate = "2026-09-01"
	jpy.LastCheck = time.Now()

	s.watches = []Watch{eur, jpy}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 {
		t.Fatalf("duplicate group collapsed to %d watches, want 1", len(watches))
	}
	if watches[0].CheapestDate != jpy.CheapestDate {
		t.Errorf("CheapestDate = %q, want %q (survivor's own JPY date; EUR's date must never leak in)", watches[0].CheapestDate, jpy.CheapestDate)
	}
}

// The scalar currency guard must fire on ANY inequality, not just "both sides
// labeled and different" -- a blank Currency has no known currency either, so
// treating blank-vs-labeled as compatible let a labeled survivor inherit a
// price with no verified currency. The survivor's OWN currency's true low
// must still be preserved, though -- the group-materialization rewrite
// (round 3) recomputes it from every group member sharing the survivor's
// currency, so the guarantee under test is "blank's 30 never leaks in", not
// "the survivor's own JPY number gets discarded". Found by adversarial
// review, 2026-07-29 (round 2); assertion updated for the round-3 fix.
func TestMigrateTreatsBlankCurrencyAsIncompatibleWithLabeled(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200}

	blank := base
	blank.ID = "blank-watch"
	blank.Currency = ""
	blank.LowestPrice = 30
	blank.LastCheck = time.Now().Add(-48 * time.Hour)

	jpy := base
	jpy.ID = "jpy-watch"
	jpy.Currency = "JPY"
	jpy.LowestPrice = 10000
	jpy.LastCheck = time.Now()

	s.watches = []Watch{blank, jpy}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 || watches[0].ID != "jpy-watch" {
		t.Fatalf("expected jpy-watch alone to survive, got %+v", watches)
	}
	if watches[0].LowestPrice != jpy.LowestPrice {
		t.Errorf("LowestPrice = %v, want %v (survivor's own JPY low; blank-currency price must not be inherited)", watches[0].LowestPrice, jpy.LowestPrice)
	}
}

// A chain can merge two same-currency duplicates first and only hit a
// currency mismatch on the THIRD record. The earlier same-currency pair's
// idMap entry (e.g. second -> first) must not be blindly re-pointed onto the
// cross-currency final survivor, or second's EUR history leaks into third's
// JPY series one hop later than the direct two-watch case. Found by
// adversarial review, 2026-07-29 (round 2).
func TestMigrateStopsHistoryLeakAtCurrencyBoundaryMidChain(t *testing.T) {
	s := NewStore(t.TempDir())
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", BelowPrice: 200}

	first := base
	first.ID = "first"
	first.Currency = "EUR"
	first.LowestPrice = 80
	first.LastCheck = time.Now().Add(-72 * time.Hour)

	second := base
	second.ID = "second"
	second.Currency = "EUR"
	second.LowestPrice = 50
	second.LastCheck = time.Now().Add(-48 * time.Hour)

	third := base
	third.ID = "third"
	third.Currency = "JPY"
	third.LowestPrice = 10000
	third.LastCheck = time.Now()

	s.watches = []Watch{first, second, third}
	s.history = []PricePoint{
		{WatchID: "first", Price: 80, Currency: "EUR", Timestamp: first.LastCheck},
		{WatchID: "second", Price: 50, Currency: "EUR", Timestamp: second.LastCheck},
		{WatchID: "third", Price: 10000, Currency: "JPY", Timestamp: third.LastCheck},
	}

	if err := s.Save(); err != nil {
		t.Fatalf("save seeded store: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	watches := s.List()
	if len(watches) != 1 || watches[0].ID != "third" {
		t.Fatalf("expected third (JPY, most recent) alone to survive, got %+v", watches)
	}
	for _, p := range s.history {
		if p.Currency == "EUR" {
			t.Errorf("EUR history point (WatchID %q) leaked past the currency boundary onto the JPY survivor", p.WatchID)
		}
		if p.WatchID != "third" {
			t.Errorf("history point still tagged with a collapsed-away chain ID %q", p.WatchID)
		}
	}
}

// TRVL.MIGRATE.3 -- collapsing duplicates must merge the running state, not
// hand over one record's fields wholesale.
//
// richer() picks a survivor on LowestPrice-presence, then LastCheck, then
// CreatedAt, and the winner's OTHER fields survive intact. LowestPrice,
// CheapestDate and CreatedAt are merged explicitly; RenewedAt, BaselinePrice
// and LastAlertedPrice were not. So a recently-renewed duplicate could lose to
// an older-but-more-recently-checked one and have its state discarded outright
// (trvl#563):
//
//   - a lost RenewedAt leaves the survivor eligible for TTL expiry even though
//     a group member was renewed moments ago;
//   - a lost LastAlertedPrice re-alerts for a drop already reported, because
//     Evaluate stays silent only while current >= LastAlertedAt.
func TestMigrateMergesRunningStateAcrossDuplicates(t *testing.T) {
	dir := t.TempDir()
	base := Watch{
		Type: "flight", Origin: "HEL", Destination: "BCN",
		DepartDate: "2027-01-01", Currency: "EUR", BelowPrice: 200,
	}

	// Loses richer(): checked longer ago. Carries the newest renewal and the
	// highest alert state.
	stale := base
	stale.ID = "renewed"
	stale.LowestPrice = 150
	stale.LastCheck = time.Now().Add(-2 * time.Hour)
	stale.CreatedAt = time.Now().Add(-30 * 24 * time.Hour)
	stale.RenewedAt = time.Now()
	stale.BaselinePrice = 500
	stale.LastAlertedPrice = 320

	// Wins richer(): checked most recently. Its own state is older/lower.
	fresh := base
	fresh.ID = "recent"
	fresh.LowestPrice = 150
	fresh.LastCheck = time.Now()
	fresh.CreatedAt = time.Now().Add(-30 * 24 * time.Hour)
	fresh.RenewedAt = time.Now().Add(-80 * 24 * time.Hour)
	fresh.BaselinePrice = 300
	fresh.LastAlertedPrice = 280

	s := NewStore(dir)
	s.watches = []Watch{stale, fresh}
	if err := s.Save(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got := s.List()
	if len(got) != 1 {
		t.Fatalf("want 1 surviving watch, got %d", len(got))
	}
	w := got[0]

	if w.RenewedAt.Before(stale.RenewedAt) {
		t.Errorf("RenewedAt = %v, want the group's newest (%v) -- the survivor is left eligible for "+
			"TTL expiry despite a member being renewed moments ago", w.RenewedAt, stale.RenewedAt)
	}
	if w.BaselinePrice != 500 {
		t.Errorf("BaselinePrice = %v, want the group's peak 500 -- a lower reference understates "+
			"every subsequent drop", w.BaselinePrice)
	}
	if w.LastAlertedPrice != 320 {
		t.Errorf("LastAlertedPrice = %v, want the group's highest 320 -- a lower dedup floor "+
			"re-alerts for a drop already reported", w.LastAlertedPrice)
	}
}

// TRVL.MIGRATE.3 -- and the currency-denominated halves merge only within the
// survivor's currency, the same rule LowestPrice already follows. Without this
// the fix would import a JPY baseline into a EUR watch.
func TestMigrateDoesNotMergeAlertStateAcrossCurrencies(t *testing.T) {
	dir := t.TempDir()
	base := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2027-01-01"}

	eur := base
	eur.ID = "eur"
	eur.Currency = "EUR"
	eur.LowestPrice = 150
	eur.LastCheck = time.Now()
	eur.BaselinePrice = 300
	eur.LastAlertedPrice = 280

	jpy := base
	jpy.ID = "jpy"
	jpy.Currency = "JPY"
	jpy.LowestPrice = 20000
	jpy.LastCheck = time.Now().Add(-2 * time.Hour)
	jpy.BaselinePrice = 50000
	jpy.LastAlertedPrice = 42000

	s := NewStore(dir)
	s.watches = []Watch{eur, jpy}
	if err := s.Save(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, w := range s.List() {
		if w.Currency != "EUR" {
			continue
		}
		if w.BaselinePrice > 1000 {
			t.Errorf("the EUR survivor took a JPY baseline (%v); currency-denominated state must "+
				"merge only within the survivor's own currency", w.BaselinePrice)
		}
		if w.LastAlertedPrice > 1000 {
			t.Errorf("the EUR survivor took a JPY alert floor (%v)", w.LastAlertedPrice)
		}
	}
}
