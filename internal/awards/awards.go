// Package awards finds the cheapest way to book a known bookable
// award seat across the user's currency portfolio (FB / Avios /
// Aeroplan / Virgin / Asia-Miles native balances plus transferable
// currencies like Amex MR, Chase UR, Bilt). Given a slice of
// AwardSeat fixtures (caller fetches via seats.aero adapter and
// hydrates) and the user's PointBalances, FindSweetSpots returns the
// ranked SweetSpot alternatives with the cheapest source program
// after transfer ratios are applied.
//
// MIK-3081. Pure function — no I/O — so the math stays trivially
// auditable. The network adapter for seats.aero ships as a separate
// change so this package can be exercised without auth tokens.
package awards

import (
	"sort"
	"strconv"
	"strings"
)

// AwardSeat is one bookable seat returned by an upstream availability
// source (seats.aero, AwardWallet, manual fixture). Program is the
// loyalty currency the seat is priced in (e.g. "AY" Finnair Plus,
// "VS" Virgin Atlantic, "AC" Air Canada Aeroplan, "BA" Avios).
//
// MilesCost is the native-currency price of the seat. CashFees is the
// fuel-surcharge / tax component the user pays in cash on top of
// miles — programs differ wildly (Virgin levies surcharges on partner
// award redemptions, Aeroplan doesn't).
type AwardSeat struct {
	Program          string  // 2-letter IATA airline code OR alliance partner key
	Origin           string  // IATA
	Destination      string  // IATA
	Date             string  // ISO 8601
	Cabin            string  // economy / premium_economy / business / first
	MilesCost        int     // native-program miles
	CashFees         float64 // taxes + surcharges in user currency
	CashEquivalent   float64 // estimated cash price the user is avoiding
	BookableSegments int     // how many segments the seat covers; 1 = direct
}

// PointBalance is one entry in the user's currency portfolio.
type PointBalance struct {
	Program string // source program (native airline OR transferable currency)
	Balance int    // current balance (whole points)
}

// TransferRatio is the ratio at which Source converts to Target.
// Numerator = Source units; Denominator = Target units. So Amex MR ->
// Virgin at "1:1" produces {Source: "MR", Target: "VS", Numerator: 1,
// Denominator: 1}; Bilt -> AY at "1:1" likewise; Chase UR -> Virgin
// at "1:1" likewise. Promotional bonuses (e.g. 30% to Virgin) are
// expressed by Numerator > Denominator so 100k MR -> 130k VS becomes
// {1, 1.3}.
type TransferRatio struct {
	Source      string
	Target      string
	Numerator   float64
	Denominator float64
}

// SweetSpot is one ranked alternative for booking the same AwardSeat.
type SweetSpot struct {
	Seat             AwardSeat
	BookingProgram   string  // program the user redeems out of
	SourceProgram    string  // currency the user spends FROM
	TransferRoute    string  // human-readable: "Amex MR -> Virgin VS @ 1:1"
	MilesSpentNative int     // miles spent in BookingProgram
	MilesSpentSource int     // points spent in SourceProgram (after transfer math)
	CashFees         float64 // copied from Seat for ranking
	CashEquivalent   float64 // copied from Seat
	// CentsPerPoint is the value-per-point yardstick: cents the user
	// effectively earned per point redeemed. Higher = better redemption.
	// Calculated as (CashEquivalent - CashFees) / MilesSpentSource * 100.
	CentsPerPoint float64
	// Affordable is true when the user has enough Source-program points
	// (after the transfer ratio is applied) to actually book. Sweet
	// spots with Affordable=false are kept in the result so callers can
	// render "you are 14k short on Virgin" guidance, but they sort
	// behind the affordable options.
	Affordable bool
	Reason     string
}

// DefaultTransferRatios returns the transfer-ratio table the package
// uses when the caller passes nil. Conservative — we only encode
// ratios stable enough to rely on without a per-month refresh, and
// promotional ratios are off by default to avoid misleading users
// after a promo lapses. Caller can override or extend by passing a
// custom slice into FindSweetSpots.
func DefaultTransferRatios() []TransferRatio {
	return []TransferRatio{
		// Amex Membership Rewards
		{Source: "MR", Target: "AY", Numerator: 1, Denominator: 1},
		{Source: "MR", Target: "VS", Numerator: 1, Denominator: 1},
		{Source: "MR", Target: "BA", Numerator: 1, Denominator: 1},
		{Source: "MR", Target: "AC", Numerator: 1, Denominator: 1},
		{Source: "MR", Target: "FB", Numerator: 1, Denominator: 1},
		// Chase Ultimate Rewards
		{Source: "UR", Target: "VS", Numerator: 1, Denominator: 1},
		{Source: "UR", Target: "AC", Numerator: 1, Denominator: 1},
		{Source: "UR", Target: "FB", Numerator: 1, Denominator: 1},
		// Bilt Rewards
		{Source: "BILT", Target: "AY", Numerator: 1, Denominator: 1},
		{Source: "BILT", Target: "AC", Numerator: 1, Denominator: 1},
		{Source: "BILT", Target: "VS", Numerator: 1, Denominator: 1},
		// Identity (native balance is the source itself).
		{Source: "AY", Target: "AY", Numerator: 1, Denominator: 1},
		{Source: "VS", Target: "VS", Numerator: 1, Denominator: 1},
		{Source: "AC", Target: "AC", Numerator: 1, Denominator: 1},
		{Source: "BA", Target: "BA", Numerator: 1, Denominator: 1},
		{Source: "FB", Target: "FB", Numerator: 1, Denominator: 1},
	}
}

// ProfileProgram is one loyalty program seeded from the traveller's
// saved profile: the program/currency code plus an optional known
// balance. A balance of 0 means the user redeems with this program but
// the balance is not recorded; it still surfaces the program as a
// source option (Affordable=false) so the user sees the redemption path.
type ProfileProgram struct {
	Program string // source program / currency IATA code (e.g. "AY", "KL")
	Balance int    // known balance in whole points; 0 if not recorded
}

// LoyaltyInput is the plain, dependency-free projection of a traveller's
// saved loyalty profile that ProfileProgramsFrom consumes. Surfaces
// (CLI, MCP) translate their own preferences types into this so the
// program-set construction lives in exactly one place.
//
// FrequentFlyer carries the per-program (carrier code, known balance)
// pairs; Airlines carries bare loyalty-airline IATA codes the user
// redeems with but for which no balance is recorded.
type LoyaltyInput struct {
	FrequentFlyer []ProfileProgram
	Airlines      []string
}

// ProfileProgramsFrom flattens a traveller's saved loyalty profile into
// the ordered ProfileProgram set used to seed an award search. It is the
// single shared translation point for both surfaces: frequent-flyer
// programs come first (they may carry a real balance), then any bare
// loyalty airlines that were not already covered. Blank codes are
// skipped and duplicates are dropped (first occurrence wins, preserving
// any balance from the frequent-flyer entry).
func ProfileProgramsFrom(in LoyaltyInput) []ProfileProgram {
	out := make([]ProfileProgram, 0, len(in.FrequentFlyer)+len(in.Airlines))
	seen := make(map[string]struct{}, cap(out))
	add := func(code string, balance int) {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" {
			return
		}
		if _, dup := seen[code]; dup {
			return
		}
		seen[code] = struct{}{}
		out = append(out, ProfileProgram{Program: code, Balance: balance})
	}
	for _, p := range in.FrequentFlyer {
		add(p.Program, p.Balance)
	}
	for _, code := range in.Airlines {
		add(code, 0)
	}
	return out
}

// SeedBalancesFromProfile builds the PointBalance set for an award
// search from the traveller's saved loyalty profile when the user did
// not pass any explicit balances. It is the single seeding entrypoint
// shared by the CLI and MCP surfaces so both behave identically.
//
// explicit takes strict precedence: when the user supplied one or more
// balances, it is returned unchanged and the profile is ignored, so an
// explicit value always wins over the profile default.
//
// When explicit is empty, balances are seeded from profile. Duplicate
// program codes are merged (highest known balance wins) and blank codes
// are skipped, so a malformed profile cannot inject empty programs. An
// empty profile yields no change (explicit is returned as-is).
func SeedBalancesFromProfile(explicit []PointBalance, profile []ProfileProgram) []PointBalance {
	if len(explicit) > 0 {
		return explicit
	}
	merged := make(map[string]int, len(profile))
	order := make([]string, 0, len(profile))
	for _, p := range profile {
		code := strings.ToUpper(strings.TrimSpace(p.Program))
		if code == "" {
			continue
		}
		if _, seen := merged[code]; !seen {
			order = append(order, code)
		}
		if p.Balance > merged[code] {
			merged[code] = p.Balance
		}
	}
	if len(order) == 0 {
		return explicit
	}
	out := make([]PointBalance, 0, len(order))
	for _, code := range order {
		out = append(out, PointBalance{Program: code, Balance: merged[code]})
	}
	return out
}

// FindSweetSpots scans every AwardSeat against the user's available
// balances and the transfer table. For each seat, it constructs every
// (source-program, target-program=seat.Program) path the table allows
// and emits a SweetSpot if the user holds the source currency. Result
// is sorted: affordable first, then by lowest MilesSpentSource, then
// by best CentsPerPoint.
//
// Callers can pass nil ratios to use DefaultTransferRatios. Pass an
// empty (non-nil) slice to disable transfers entirely (only direct
// native redemptions).
func FindSweetSpots(seats []AwardSeat, balances []PointBalance, ratios []TransferRatio) []SweetSpot {
	if len(seats) == 0 {
		return nil
	}
	if ratios == nil {
		ratios = DefaultTransferRatios()
	}
	balanceBy := indexBalances(balances)
	out := make([]SweetSpot, 0, len(seats)*4)
	for _, seat := range seats {
		if seat.MilesCost <= 0 {
			continue
		}
		target := strings.ToUpper(strings.TrimSpace(seat.Program))
		if target == "" {
			continue
		}
		for _, r := range ratios {
			if !strings.EqualFold(r.Target, target) {
				continue
			}
			source := strings.ToUpper(strings.TrimSpace(r.Source))
			ratio := safeRatio(r)
			if ratio <= 0 {
				continue
			}
			pointsNeeded := pointsAtSource(seat.MilesCost, ratio)
			held := balanceBy[source]
			s := SweetSpot{
				Seat:             seat,
				BookingProgram:   target,
				SourceProgram:    source,
				TransferRoute:    formatTransferRoute(source, target, r),
				MilesSpentNative: seat.MilesCost,
				MilesSpentSource: pointsNeeded,
				CashFees:         seat.CashFees,
				CashEquivalent:   seat.CashEquivalent,
				CentsPerPoint:    centsPerPoint(seat, pointsNeeded),
				Affordable:       held >= pointsNeeded,
				Reason:           reasonFor(source, target, ratio, held, pointsNeeded),
			}
			out = append(out, s)
		}
	}
	sortSweetSpots(out)
	return out
}

func indexBalances(b []PointBalance) map[string]int {
	out := make(map[string]int, len(b))
	for _, x := range b {
		k := strings.ToUpper(strings.TrimSpace(x.Program))
		if k == "" {
			continue
		}
		out[k] += x.Balance
	}
	return out
}

func safeRatio(r TransferRatio) float64 {
	if r.Numerator <= 0 {
		return 0
	}
	if r.Denominator <= 0 {
		return 0
	}
	return r.Numerator / r.Denominator
}

// pointsAtSource is how many SourceProgram units the user must spend
// to land MilesCost units in the target. With ratio = Numerator /
// Denominator (source per target unit), source = miles * ratio. We
// round up because partial points cannot be transferred.
func pointsAtSource(milesCost int, ratio float64) int {
	raw := float64(milesCost) * ratio
	whole := int(raw)
	if raw-float64(whole) > 0 {
		whole++
	}
	return whole
}

func centsPerPoint(seat AwardSeat, pointsSpent int) float64 {
	if pointsSpent <= 0 {
		return 0
	}
	value := seat.CashEquivalent - seat.CashFees
	if value < 0 {
		value = 0
	}
	return (value / float64(pointsSpent)) * 100
}

func formatTransferRoute(source, target string, r TransferRatio) string {
	if strings.EqualFold(source, target) {
		return source + " native redemption"
	}
	return source + " -> " + target + " " + ratioLabel(r)
}

func ratioLabel(r TransferRatio) string {
	if r.Numerator == r.Denominator {
		return "1:1"
	}
	return formatFloat(r.Numerator) + ":" + formatFloat(r.Denominator)
}

func formatFloat(f float64) string {
	if f == float64(int(f)) {
		return intToString(int(f))
	}
	return trimTrailingZeros(formatFloatN(f, 2))
}

func formatFloatN(f float64, n int) string {
	return strconv.FormatFloat(f, 'f', n, 64)
}

func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	out := strings.TrimRight(s, "0")
	out = strings.TrimRight(out, ".")
	if out == "" {
		return "0"
	}
	return out
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	buf := make([]byte, 0, 8)
	for i > 0 {
		buf = append([]byte{byte('0' + i%10)}, buf...)
		i /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

func reasonFor(source, target string, ratio float64, held, needed int) string {
	switch {
	case held >= needed && strings.EqualFold(source, target):
		return "covered by native " + target + " balance"
	case held >= needed:
		return "covered after " + source + " -> " + target + " transfer at " + formatFloat(ratio) + ":1"
	case held > 0:
		shortfall := needed - held
		return source + " short by " + intToString(shortfall) + " — earn-or-buy gap"
	default:
		return source + " balance is empty; transfer not possible"
	}
}

func sortSweetSpots(s []SweetSpot) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].Affordable != s[j].Affordable {
			return s[i].Affordable
		}
		if s[i].MilesSpentSource != s[j].MilesSpentSource {
			return s[i].MilesSpentSource < s[j].MilesSpentSource
		}
		return s[i].CentsPerPoint > s[j].CentsPerPoint
	})
}
