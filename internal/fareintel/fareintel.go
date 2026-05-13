// Package fareintel converts observed price history into conservative
// buy/watch/wait guidance. It does not claim live availability.
package fareintel

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

const (
	VerdictBuy   = "buy"
	VerdictWatch = "watch"
	VerdictWait  = "wait"
)

type Result struct {
	Verdict         string  `json:"verdict"`
	Confidence      string  `json:"confidence"`
	CurrentPrice    float64 `json:"current_price"`
	Currency        string  `json:"currency"`
	MedianPrice     float64 `json:"median_price,omitempty"`
	PercentVsMedian float64 `json:"percent_vs_median,omitempty"`
	HistoryCount    int     `json:"history_count"`
	Explanation     string  `json:"explanation"`
}

func Analyze(currentPrice float64, currency string, history []watch.PricePoint) Result {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	points := pricesForCurrency(history, currency)
	res := Result{
		Verdict:      VerdictWatch,
		Confidence:   "low",
		CurrentPrice: currentPrice,
		Currency:     currency,
		HistoryCount: len(points),
		Explanation:  "No reliable route baseline yet; create or keep a watch and re-check before booking.",
	}
	if currentPrice <= 0 {
		res.Explanation = "Current price is missing; cannot compare against historical observations."
		return res
	}
	if len(points) < 3 {
		res.Explanation = "Too few historical observations for a strong fare verdict."
		return res
	}

	median := median(points)
	res.MedianPrice = median
	res.PercentVsMedian = (currentPrice - median) / median * 100
	res.Confidence = confidence(len(points))

	switch {
	case currentPrice <= median*0.85:
		res.Verdict = VerdictBuy
		res.Explanation = "Current price is materially below the observed median for this watch."
	case currentPrice >= median*1.15:
		res.Verdict = VerdictWait
		res.Explanation = "Current price is materially above the observed median; wait or keep monitoring unless timing matters more than price."
	default:
		res.Verdict = VerdictWatch
		res.Explanation = "Current price is close to the observed median; watch for movement or book if schedule certainty matters."
	}
	return res
}

func pricesForCurrency(history []watch.PricePoint, currency string) []float64 {
	var prices []float64
	for _, point := range history {
		if point.Price <= 0 {
			continue
		}
		if currency != "" && strings.ToUpper(point.Currency) != currency {
			continue
		}
		prices = append(prices, point.Price)
	}
	return prices
}

func median(values []float64) float64 {
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return math.Round((cp[mid-1]+cp[mid])*50) / 100
}

func confidence(n int) string {
	switch {
	case n >= 10:
		return "high"
	case n >= 5:
		return "medium"
	default:
		return "low"
	}
}

func Point(price float64, currency string, ts time.Time) watch.PricePoint {
	return watch.PricePoint{Price: price, Currency: strings.ToUpper(currency), Timestamp: ts}
}
