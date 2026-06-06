// Package models — sourcequality.go: a typed registry of per-provider data
// characteristics (API vs scrape, staleness threshold, no-hit semantics) plus a
// freshness classifier. Folds the scattered provider-reliability knowledge into
// one machine-readable table so renderers can avoid asserting "cheapest"/"best"
// on stale scraped prices. Pure data + stdlib only (no internal imports).
//
// Tracking: MIK-4952 (parent MIK-4948).
package models

import (
	"strings"
	"time"
)

// Freshness classes.
const (
	FreshnessLive   = "live"   // within the provider's live window
	FreshnessRecent = "recent" // aging but usable
	FreshnessStale  = "stale"  // older than the staleness threshold; do not call "cheapest"
)

// SourceProfile describes a data source's reliability characteristics.
type SourceProfile struct {
	ID           string // provider id, e.g. "ryanair", "google_flights"
	API          bool   // true = structured API, false = scrape/browser-assisted
	LiveMinutes  int    // age below which a price is "live"
	StaleMinutes int    // age at/above which a price is "stale"
	NoHitMeaning string // what an empty result means for this source
}

// sourceRegistry is keyed by provider id (lowercase).
var sourceRegistry = map[string]SourceProfile{
	"google_flights": {ID: "google_flights", API: false, LiveMinutes: 30, StaleMinutes: 180, NoHitMeaning: "no fares Google indexed for this query; LCCs excluded"},
	"kiwi":           {ID: "kiwi", API: true, LiveMinutes: 30, StaleMinutes: 180, NoHitMeaning: "no Kiwi itinerary for this query"},
	"skiplagged":     {ID: "skiplagged", API: true, LiveMinutes: 30, StaleMinutes: 180, NoHitMeaning: "no hidden-city/standard fare found"},
	"ryanair":        {ID: "ryanair", API: true, LiveMinutes: 60, StaleMinutes: 360, NoHitMeaning: "Ryanair does not fly this route/date"},
	"booking":        {ID: "booking", API: false, LiveMinutes: 60, StaleMinutes: 720, NoHitMeaning: "no Booking.com availability for these dates"},
	"airbnb":         {ID: "airbnb", API: false, LiveMinutes: 60, StaleMinutes: 720, NoHitMeaning: "no Airbnb listings matched"},
	"google_hotels":  {ID: "google_hotels", API: false, LiveMinutes: 60, StaleMinutes: 720, NoHitMeaning: "no hotels Google indexed for this query"},
}

// defaultProfile applies when a provider is not in the registry.
var defaultProfile = SourceProfile{LiveMinutes: 60, StaleMinutes: 360, API: false}

// SourceProfileFor returns the profile for a provider id (case-insensitive),
// falling back to a conservative default.
func SourceProfileFor(provider string) SourceProfile {
	if p, ok := sourceRegistry[strings.ToLower(strings.TrimSpace(provider))]; ok {
		return p
	}
	return defaultProfile
}

// ClassifyFreshness returns live/recent/stale for a price retrieved at the given
// time, judged against the provider's profile. A zero retrievedAt is treated as
// unknown -> recent (neither a freshness claim nor a stale warning).
func ClassifyFreshness(provider string, retrievedAt, now time.Time) string {
	if retrievedAt.IsZero() {
		return FreshnessRecent
	}
	p := SourceProfileFor(provider)
	age := now.Sub(retrievedAt)
	switch {
	case age >= time.Duration(p.StaleMinutes)*time.Minute:
		return FreshnessStale
	case age <= time.Duration(p.LiveMinutes)*time.Minute:
		return FreshnessLive
	default:
		return FreshnessRecent
	}
}

// MayClaimFreshSuperlative reports whether a renderer may use superlatives
// ("cheapest", "best deal") for a price of the given freshness. Stale prices
// must not carry superlatives — they may have changed.
func MayClaimFreshSuperlative(freshness string) bool {
	return freshness != FreshnessStale
}
