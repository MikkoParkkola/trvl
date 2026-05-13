// Package evidence centralizes lightweight provenance, freshness, and
// confidence scoring for traveller-facing claims.
package evidence

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

const (
	FreshnessFresh   = "fresh"
	FreshnessStale   = "stale"
	FreshnessUnknown = "unknown"

	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

type Ref struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	Provider    string    `json:"provider,omitempty"`
	URL         string    `json:"url,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Freshness   string    `json:"freshness"`
	Confidence  string    `json:"confidence"`
	Explanation string    `json:"explanation,omitempty"`
}

func NewRef(source, provider, url string, checkedAt, now time.Time, ttl time.Duration) Ref {
	ref := Ref{
		Source:     strings.TrimSpace(source),
		Provider:   strings.TrimSpace(provider),
		URL:        strings.TrimSpace(url),
		CheckedAt:  checkedAt,
		Freshness:  AssessFreshness(checkedAt, now, ttl),
		Confidence: ConfidenceFor(checkedAt, now, ttl),
	}
	ref.ID = refID(ref)
	return ref
}

func AssessFreshness(checkedAt, now time.Time, ttl time.Duration) string {
	if checkedAt.IsZero() {
		return FreshnessUnknown
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if now.Sub(checkedAt) > ttl {
		return FreshnessStale
	}
	return FreshnessFresh
}

func ConfidenceFor(checkedAt, now time.Time, ttl time.Duration) string {
	switch AssessFreshness(checkedAt, now, ttl) {
	case FreshnessFresh:
		return ConfidenceHigh
	case FreshnessStale:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

var (
	emailRe = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	refRe   = regexp.MustCompile(`\b[A-Z0-9]{5,10}\b`)
)

func RedactSensitive(s string) string {
	s = emailRe.ReplaceAllString(s, "[redacted-email]")
	return refRe.ReplaceAllStringFunc(s, func(match string) string {
		hasLetter := false
		hasDigit := false
		for _, r := range match {
			if r >= 'A' && r <= 'Z' {
				hasLetter = true
			}
			if r >= '0' && r <= '9' {
				hasDigit = true
			}
		}
		if hasLetter && hasDigit {
			return "[redacted-ref]"
		}
		return match
	})
}

func refID(ref Ref) string {
	h := sha1.New()
	for _, part := range []string{ref.Source, ref.Provider, ref.URL, ref.CheckedAt.Format(time.RFC3339)} {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(part))))
		_, _ = h.Write([]byte{0})
	}
	return "ev_" + hex.EncodeToString(h.Sum(nil))[:12]
}
