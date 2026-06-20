// confidence.go — bookability confidence scoring (innovation #3).
//
// ScoreConfidence converts the evidence trvl already has about a result —
// price freshness, provider reliability, multi-source corroboration, hotel
// price verification, and (optionally) historical price position — into an
// honest 0..1 "likely bookable" score plus a high/medium/low label. It REUSES
// the existing engines rather than reimplementing them:
//
//   - models.ClassifyFreshness / models.SourceProfileFor — freshness + API-vs-scrape
//   - dealquality.ScoreAgainst                            — historical price position
//   - fareintel.Analyze (this package)                    — fare-history verdict + confidence
//
// When the available signal is too thin to judge, ScoreConfidence returns an
// honest unrated assessment (models.ConfidenceUnrated). It NEVER fabricates a
// number from nothing.
package fareintel

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dealquality"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// Signal weights. Each contributes only when its evidence is actually present,
// so a result is scored on the signals it has, not penalised for ones it lacks.
const (
	weightFreshness     = 0.40 // how fresh the quoted price is
	weightProvider      = 0.25 // structured API vs scrape/browser-assisted
	weightCorroboration = 0.20 // how many distinct sources returned the same result
	weightVerification  = 0.10 // hotel price basis confidence (unverified..verified)
	weightDealPosition  = 0.15 // sane market position from historical samples

	// minRatedWeight is the minimum total active-signal weight required before
	// trvl will commit to a score. Below this we are honest and return unrated.
	minRatedWeight = 0.45
	// minRatedSignals is the minimum number of independent signals required.
	minRatedSignals = 2

	// Label thresholds on the 0..1 score.
	thresholdHigh   = 0.75
	thresholdMedium = 0.50
)

// ConfidenceInput captures the evidence available to assess one result. All
// fields are optional; omit what you do not have and the scorer degrades
// honestly rather than inventing signal.
type ConfidenceInput struct {
	Price       float64
	Currency    string
	Provider    string               // headline provider id, e.g. "kiwi", "google_flights"
	RetrievedAt time.Time            // when the headline price was obtained (zero = unknown)
	Now         time.Time            // evaluation time (zero => derived from sources / treated as unknown)
	Sources     []models.PriceSource // every source that returned this same physical result
	// PriceVerification is the hotel price-basis confidence
	// (unverified|room_level|verified); "" when not applicable (flights/ground).
	PriceVerification string
	// SeparateTickets marks a synthetic book-direct / self-connect itinerary whose
	// legs are booked on separate tickets — inherently less certain to hold.
	SeparateTickets bool

	// Optional historical price context. Provide DealSamples (dealquality) and/or
	// FareHistory (fareintel.Analyze) to add a price-position signal. Omit both
	// when no history exists.
	DealSamples []dealquality.Sample
	FareHistory []watch.PricePoint
}

// ScoreConfidence assesses how likely the result is bookable at the shown price.
func ScoreConfidence(in ConfidenceInput) models.Confidence {
	if in.Price <= 0 {
		return models.UnratedConfidence("no price to assess")
	}

	now := in.Now
	if now.IsZero() {
		now = freshestSourceTime(in.Sources)
	}

	var sum, total float64
	var nSignals int
	var tags []string
	add := func(value, weight float64, tag string) {
		sum += value * weight
		total += weight
		nSignals++
		tags = append(tags, tag)
	}

	// ── Freshness ──────────────────────────────────────────────────────────
	retrievedAt, freshKnown := in.RetrievedAt, !in.RetrievedAt.IsZero()
	if !freshKnown {
		if t := freshestSourceTime(in.Sources); !t.IsZero() {
			retrievedAt, freshKnown = t, true
		}
	}
	freshness := ""
	if freshKnown {
		evalNow := now
		if evalNow.IsZero() {
			evalNow = retrievedAt
		}
		freshness = models.ClassifyFreshness(in.Provider, retrievedAt, evalNow)
		add(freshnessValue(freshness), weightFreshness, "freshness:"+freshness)
	}

	// ── Provider reliability (API vs scrape) ───────────────────────────────
	if prof := models.SourceProfileFor(in.Provider); prof.ID != "" {
		if prof.API {
			add(1.0, weightProvider, "provider:api")
		} else {
			add(0.5, weightProvider, "provider:scrape")
		}
	}

	// ── Multi-source corroboration ─────────────────────────────────────────
	if n := distinctSourceCount(in.Sources, in.Provider); n > 0 {
		add(corroborationValue(n), weightCorroboration, fmt.Sprintf("sources:%d", n))
	}

	// ── Hotel price verification ───────────────────────────────────────────
	if v := verificationValue(in.PriceVerification); v >= 0 {
		add(v, weightVerification, "verification:"+strings.ToLower(in.PriceVerification))
	}

	// ── Historical price position (dealquality / fareintel) ────────────────
	if v, tag, ok := dealPositionValue(in); ok {
		add(v, weightDealPosition, tag)
	}

	// Honest unrated when signal is too thin.
	if total < minRatedWeight || nSignals < minRatedSignals {
		c := models.UnratedConfidence("insufficient signal to assess bookability")
		c.Freshness = freshness
		if len(tags) > 0 {
			c.Signals = tags
		}
		return c
	}

	score := sum / total

	// Separate-tickets itineraries carry real booking risk: cap and discount.
	if in.SeparateTickets {
		score *= 0.7
		tags = append(tags, "separate_tickets_risk")
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	c := models.Confidence{
		Rated:     true,
		Score:     score,
		Label:     labelFor(score),
		Freshness: freshness,
		Signals:   tags,
	}
	c.Basis = basisFor(c)
	return c
}

func freshnessValue(freshness string) float64 {
	switch freshness {
	case models.FreshnessLive:
		return 1.0
	case models.FreshnessRecent:
		return 0.6
	case models.FreshnessStale:
		return 0.15
	default:
		return 0.6
	}
}

func corroborationValue(n int) float64 {
	switch {
	case n >= 3:
		return 1.0
	case n == 2:
		return 0.75
	default:
		return 0.45
	}
}

// verificationValue maps a hotel price-basis confidence to [0,1]; returns -1
// when there is no verification signal.
func verificationValue(v string) float64 {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "verified":
		return 1.0
	case "room_level":
		return 0.7
	case "unverified":
		return 0.3
	default:
		return -1
	}
}

// dealPositionValue derives a bookability-plausibility signal from historical
// price context. A price in a sane market band is most plausible; a price wildly
// below history is treated as slightly less certain (it may be stale or an
// error). Prefers dealquality samples; falls back to fareintel.Analyze.
func dealPositionValue(in ConfidenceInput) (value float64, tag string, ok bool) {
	if len(in.DealSamples) > 0 {
		score := dealquality.ScoreAgainst(in.Price, in.DealSamples)
		if score.Reason == "insufficient_history" {
			return 0, "", false
		}
		// score.Total is 0..100 deal quality; map to a plausibility curve.
		return plausibilityFromDeal(score.Total), fmt.Sprintf("deal:%s", score.Reason), true
	}
	if len(in.FareHistory) > 0 {
		res := Analyze(in.Price, in.Currency, in.FareHistory)
		if res.HistoryCount < 3 {
			return 0, "", false
		}
		// Reuse fareintel's own confidence + percent-vs-median.
		return plausibilityFromPercent(res.PercentVsMedian), fmt.Sprintf("fare:%s", res.Verdict), true
	}
	return 0, "", false
}

// plausibilityFromDeal turns a 0..100 deal-quality score into a 0..1
// bookability-plausibility value. Mid-market prices are most plausible; a price
// far below history (deal≈100) is discounted as possibly-too-good-to-be-true.
func plausibilityFromDeal(total int) float64 {
	switch {
	case total >= 95:
		return 0.6 // suspiciously cheap
	case total >= 20:
		return 1.0 // healthy market band
	default:
		return 0.75 // expensive but real
	}
}

func plausibilityFromPercent(pctVsMedian float64) float64 {
	switch {
	case pctVsMedian <= -40:
		return 0.6 // far below median — possibly stale/error
	case pctVsMedian <= 25:
		return 1.0 // around the typical band
	default:
		return 0.8 // above median but plausible
	}
}

func labelFor(score float64) string {
	switch {
	case score >= thresholdHigh:
		return models.ConfidenceHigh
	case score >= thresholdMedium:
		return models.ConfidenceMedium
	default:
		return models.ConfidenceLow
	}
}

func basisFor(c models.Confidence) string {
	parts := make([]string, 0, 3)
	if c.Freshness != "" {
		parts = append(parts, c.Freshness+" price")
	}
	for _, s := range c.Signals {
		switch {
		case s == "provider:api":
			parts = append(parts, "structured API source")
		case s == "provider:scrape":
			parts = append(parts, "scraped source")
		case strings.HasPrefix(s, "sources:"):
			parts = append(parts, strings.TrimPrefix(s, "sources:")+"-source corroboration")
		case s == "separate_tickets_risk":
			parts = append(parts, "separate-tickets risk")
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d%% likely bookable", c.Percent())
	}
	return fmt.Sprintf("%d%% likely bookable — %s", c.Percent(), strings.Join(parts, ", "))
}

// distinctSourceCount counts unique source providers, including the headline
// provider when sources are absent or do not list it.
func distinctSourceCount(sources []models.PriceSource, headline string) int {
	seen := map[string]struct{}{}
	for _, s := range sources {
		id := strings.ToLower(strings.TrimSpace(s.Provider))
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
	}
	if h := strings.ToLower(strings.TrimSpace(headline)); h != "" {
		seen[h] = struct{}{}
	}
	return len(seen)
}

// freshestSourceTime returns the most recent non-zero RetrievedAt across sources.
func freshestSourceTime(sources []models.PriceSource) time.Time {
	times := make([]time.Time, 0, len(sources))
	for _, s := range sources {
		if !s.RetrievedAt.IsZero() {
			times = append(times, s.RetrievedAt)
		}
	}
	if len(times) == 0 {
		return time.Time{}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].After(times[j]) })
	return times[0]
}
