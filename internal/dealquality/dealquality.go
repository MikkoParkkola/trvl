// Package dealquality scores how good a travel deal is relative to historical prices.
// All scoring is pure (no I/O in scoring functions). Store provides persistence.
package dealquality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
)

const historyLayout = "2006-01-02"

// Sample is one observed price for a route+season.
type Sample struct {
	Route  string  `json:"route"`  // "HEL-BCN" (sorted IATA pair, uppercase)
	Season string  `json:"season"` // "Q1", "Q2", "Q3", "Q4"
	Date   string  `json:"date"`   // YYYY-MM-DD of observation
	Price  float64 `json:"price"`  // price in any consistent currency
	Kind   string  `json:"kind"`   // "flight" or "hotel"
}

// Score holds the result of a DealQuality computation.
type Score struct {
	Total   int     // 0-100
	Reason  string  // e.g. "price_at_p08" or "insufficient_history"
	Samples int     // how many historical samples were used
	P10     float64 // 10th-percentile price (0 if insufficient)
	P20     float64 // 20th-percentile price
	P50     float64 // median price
}

// SeasonOf returns "Q1"/"Q2"/"Q3"/"Q4" for a YYYY-MM-DD date string.
// Returns "Q1" for unparseable dates.
func SeasonOf(date string) string {
	t, err := time.Parse(historyLayout, date)
	if err != nil {
		return "Q1"
	}
	switch (int(t.Month()) - 1) / 3 {
	case 0:
		return "Q1"
	case 1:
		return "Q2"
	case 2:
		return "Q3"
	default:
		return "Q4"
	}
}

// RouteKey returns a canonical "ORG-DST" key: sorted alphabetically, uppercase.
func RouteKey(a, b string) string {
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))
	if a > b {
		a, b = b, a
	}
	return a + "-" + b
}

// ScoreAgainst computes DealQuality 0-100 for a given price against historical samples.
//
// Scoring bands:
//
//	price ≤ p10 → 95-100 (lerp)
//	price ≤ p20 → 80-95 (lerp)
//	price ≤ p50 → 50-80 (lerp)
//	price > p50 → ramp linearly to 0 at 2×p50; clamp to 0 below that
//
// Sparse (<10 samples): returns Score{Total: 50, Reason: "insufficient_history", Samples: n}.
func ScoreAgainst(price float64, samples []Sample) Score {
	n := len(samples)
	if n < 10 {
		return Score{Total: 50, Reason: "insufficient_history", Samples: n}
	}

	prices := make([]float64, 0, n)
	for _, s := range samples {
		prices = append(prices, s.Price)
	}
	sort.Float64s(prices)

	p10 := percentile(prices, 10)
	p20 := percentile(prices, 20)
	p50 := percentile(prices, 50)

	var total int
	var reason string

	switch {
	case price <= p10:
		total = lerp(p10, 95, 100, price, p10)
		// price at or below p10 → find approximate percentile for reason
		pct := pricePercentile(prices, price)
		reason = fmt.Sprintf("price_at_p%02d", pct)
	case price <= p20:
		total = lerp(p20, 80, 95, price, p10)
		pct := pricePercentile(prices, price)
		reason = fmt.Sprintf("price_at_p%02d", pct)
	case price <= p50:
		total = lerp(p50, 50, 80, price, p20)
		pct := pricePercentile(prices, price)
		reason = fmt.Sprintf("price_at_p%02d", pct)
	default:
		// linear ramp from 50 at p50 to 0 at 2×p50
		if p50 <= 0 {
			total = 0
			reason = "above_median"
		} else {
			cap := 2 * p50
			if price >= cap {
				total = 0
			} else {
				frac := (cap - price) / p50 // goes from 1.0 at p50 to 0 at 2*p50
				total = int(frac * 50)
				if total < 0 {
					total = 0
				}
			}
			reason = "above_median"
		}
	}

	return Score{
		Total:   total,
		Reason:  reason,
		Samples: n,
		P10:     p10,
		P20:     p20,
		P50:     p50,
	}
}

// percentile returns the p-th percentile of a sorted slice (0-100).
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := float64(p) / 100.0 * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// pricePercentile returns the approximate percentile rank of price in sorted prices.
func pricePercentile(sorted []float64, price float64) int {
	count := 0
	for _, p := range sorted {
		if p <= price {
			count++
		}
	}
	return count * 100 / len(sorted)
}

// lerp interpolates linearly. Returns minScore when price==high, maxScore when price==low.
// Used for scoring bands where lower price → higher score.
func lerp(high float64, minScore, maxScore int, price, low float64) int {
	if high <= low {
		return maxScore
	}
	span := high - low
	pos := price - low
	if pos <= 0 {
		return maxScore
	}
	if pos >= span {
		return minScore
	}
	frac := pos / span
	score := float64(maxScore) - frac*float64(maxScore-minScore)
	return int(score)
}

// Store is a concurrency-safe, atomic-write store for deal history.
// Persists to dir/deal-history.json.
type Store struct {
	mu      sync.Mutex
	dir     string
	samples []Sample
}

// NewStore creates a store rooted at the given directory.
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

func (s *Store) historyPath() string {
	return filepath.Join(s.dir, "deal-history.json")
}

// Load reads samples from disk. If the file does not exist, the store starts empty.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = nil
	return s.loadLocked()
}

func (s *Store) loadLocked() error {
	data, err := os.ReadFile(s.historyPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read deal history: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &s.samples)
}

func (s *Store) saveLocked() error {
	return atomicjson.Write(s.historyPath(), s.samples)
}

// Append adds a sample, deduplicating exact (Route,Kind,Date,Price) matches
// and pruning entries older than 90 days.
func (s *Store) Append(sample Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load latest from disk first so concurrent writers don't lose data.
	if err := s.loadLocked(); err != nil {
		return fmt.Errorf("reload before append: %w", err)
	}

	// Dedup exact matches.
	for _, existing := range s.samples {
		if existing.Route == sample.Route &&
			existing.Kind == sample.Kind &&
			existing.Date == sample.Date &&
			existing.Price == sample.Price {
			return nil // already present
		}
	}

	// Prune samples older than 90 days.
	cutoff := time.Now().AddDate(0, 0, -90).Format(historyLayout)
	pruned := s.samples[:0]
	for _, existing := range s.samples {
		if existing.Date >= cutoff {
			pruned = append(pruned, existing)
		}
	}
	s.samples = pruned

	s.samples = append(s.samples, sample)
	return s.saveLocked()
}

// Query returns all samples for route+kind+season within 90 days.
func (s *Store) Query(route, kind, season string) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -90).Format(historyLayout)
	var out []Sample
	for _, sample := range s.samples {
		if sample.Route == route &&
			sample.Kind == kind &&
			sample.Season == season &&
			sample.Date >= cutoff {
			out = append(out, sample)
		}
	}
	return out
}
