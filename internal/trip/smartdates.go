package trip

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// SmartDateOptions configures a smart date suggestion search.
type SmartDateOptions struct {
	TargetDate string // YYYY-MM-DD center date to search around
	FlexDays   int    // Number of days of flexibility (default: 7)
	RoundTrip  bool   // Whether to search round-trip prices
	Duration   int    // Trip duration in days for round-trip (default: 7)
}

// DateInsight is a single pricing insight.
type DateInsight struct {
	Type        string  `json:"type"`        // "cheapest", "saving", "average", "pattern"
	Description string  `json:"description"` // Human-readable insight
	Date        string  `json:"date,omitempty"`
	Price       float64 `json:"price,omitempty"`
	Savings     float64 `json:"savings,omitempty"`
}

// CheapDate represents a single cheap departure date.
type CheapDate struct {
	Date       string  `json:"date"`
	DayOfWeek  string  `json:"day_of_week"`
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
	ReturnDate string  `json:"return_date,omitempty"`
}

// SmartDateResult is the top-level response for smart date suggestions.
type SmartDateResult struct {
	Success       bool          `json:"success"`
	Origin        string        `json:"origin"`
	Destination   string        `json:"destination"`
	CheapestDates []CheapDate   `json:"cheapest_dates"`
	AveragePrice  float64       `json:"average_price"`
	Currency      string        `json:"currency"`
	Insights      []DateInsight `json:"insights"`
	Error         string        `json:"error,omitempty"`
}

// defaults fills in zero-value fields.
func (o *SmartDateOptions) defaults() {
	if o.FlexDays <= 0 {
		o.FlexDays = 7
	}
	if o.RoundTrip && o.Duration <= 0 {
		o.Duration = 7
	}
}

// SuggestDates analyzes flight prices across a date range and returns
// the cheapest dates along with actionable insights.
//
// It uses the CalendarGraph API to fetch all prices in a single request,
// then analyzes patterns (weekday vs weekend, cheapest dates, average price)
// and generates human-readable insights.
func SuggestDates(ctx context.Context, origin, dest string, opts SmartDateOptions) (*SmartDateResult, error) {
	opts.defaults()

	if origin == "" || dest == "" {
		return nil, fmt.Errorf("origin and destination are required")
	}
	if opts.TargetDate == "" {
		return nil, fmt.Errorf("target_date is required")
	}

	target, err := models.ParseDate(opts.TargetDate)
	if err != nil {
		return nil, fmt.Errorf("invalid target_date %q: expected YYYY-MM-DD", opts.TargetDate)
	}

	fromDate := target.AddDate(0, 0, -opts.FlexDays).Format("2006-01-02")
	toDate := target.AddDate(0, 0, opts.FlexDays).Format("2006-01-02")

	calOpts := flights.CalendarOptions{
		FromDate:   fromDate,
		ToDate:     toDate,
		RoundTrip:  opts.RoundTrip,
		TripLength: opts.Duration,
		Adults:     1,
	}

	dateResult, err := flights.SearchCalendar(ctx, origin, dest, calOpts)
	if err != nil {
		return nil, fmt.Errorf("search calendar: %w", err)
	}

	return assembleDateResult(origin, dest, target, dateResult), nil
}

// assembleDateResult builds a SmartDateResult from raw calendar data.
// It filters zero prices, sorts, picks the cheapest dates, and generates insights.
func assembleDateResult(origin, dest string, target time.Time, dateResult *models.DateSearchResult) *SmartDateResult {
	if !dateResult.Success || len(dateResult.Dates) == 0 {
		return &SmartDateResult{
			Success:     false,
			Origin:      origin,
			Destination: dest,
			Error:       "no price data found for the given date range",
		}
	}

	// Filter out zero prices.
	var validDates []models.DatePriceResult
	for _, d := range dateResult.Dates {
		if d.Price > 0 {
			validDates = append(validDates, d)
		}
	}

	if len(validDates) == 0 {
		return &SmartDateResult{
			Success:     false,
			Origin:      origin,
			Destination: dest,
			Error:       "no valid prices found",
		}
	}

	// Sort by price.
	sort.Slice(validDates, func(i, j int) bool {
		return validDates[i].Price < validDates[j].Price
	})

	// Take cheapest 3 dates.
	top := 3
	if len(validDates) < top {
		top = len(validDates)
	}

	var cheapestDates []CheapDate
	for _, d := range validDates[:top] {
		t, _ := models.ParseDate(d.Date)
		cheapestDates = append(cheapestDates, CheapDate{
			Date:       d.Date,
			DayOfWeek:  t.Weekday().String(),
			Price:      d.Price,
			Currency:   d.Currency,
			ReturnDate: d.ReturnDate,
		})
	}

	// Calculate average price.
	var sum float64
	for _, d := range validDates {
		sum += d.Price
	}
	avgPrice := sum / float64(len(validDates))

	// Build insights.
	insights := buildInsights(validDates, target, avgPrice)

	// Use whatever currency the API returned — no hardcoded default.
	currency := ""
	for _, d := range validDates {
		if d.Currency != "" {
			currency = d.Currency
			break
		}
	}

	return &SmartDateResult{
		Success:       true,
		Origin:        origin,
		Destination:   dest,
		CheapestDates: cheapestDates,
		AveragePrice:  math.Round(avgPrice),
		Currency:      currency,
		Insights:      insights,
	}
}

// buildInsights generates human-readable pricing insights.
func buildInsights(dates []models.DatePriceResult, target time.Time, avgPrice float64) []DateInsight {
	var insights []DateInsight

	if len(dates) == 0 {
		return nil
	}

	cheapest := dates[0]
	cheapestTime, _ := models.ParseDate(cheapest.Date)

	// Insight 1: Cheapest date.
	insights = append(insights, DateInsight{
		Type:        "cheapest",
		Description: fmt.Sprintf("Cheapest: %s %s at %s %.0f", cheapestTime.Weekday(), cheapest.Date, cheapest.Currency, cheapest.Price),
		Date:        cheapest.Date,
		Price:       cheapest.Price,
	})

	// Insight 2: Savings vs target date.
	for _, d := range dates {
		if d.Date == target.Format("2006-01-02") {
			savings := d.Price - cheapest.Price
			if savings > 0 {
				insights = append(insights, DateInsight{
					Type: "saving",
					Description: fmt.Sprintf("Flying %s %s saves %s %.0f vs %s %s",
						cheapestTime.Weekday(), cheapest.Date, cheapest.Currency, savings,
						target.Weekday(), target.Format("2006-01-02")),
					Savings: savings,
				})
			}
			break
		}
	}

	// Insight 3: Weekday vs weekend analysis.
	var weekdayPrices, weekendPrices []float64
	for _, d := range dates {
		t, err := models.ParseDate(d.Date)
		if err != nil {
			continue
		}
		wd := t.Weekday()
		if wd == time.Saturday || wd == time.Sunday || wd == time.Friday {
			weekendPrices = append(weekendPrices, d.Price)
		} else {
			weekdayPrices = append(weekdayPrices, d.Price)
		}
	}

	if len(weekdayPrices) > 0 && len(weekendPrices) > 0 {
		weekdayAvg := avg(weekdayPrices)
		weekendAvg := avg(weekendPrices)
		diff := weekendAvg - weekdayAvg

		if diff > 0 {
			insights = append(insights, DateInsight{
				Type:        "pattern",
				Description: fmt.Sprintf("Weekday flights average %.0f cheaper than weekend departures", diff),
				Savings:     math.Round(diff),
			})
		} else if diff < 0 {
			insights = append(insights, DateInsight{
				Type:        "pattern",
				Description: fmt.Sprintf("Weekend flights average %.0f cheaper than weekday departures", -diff),
				Savings:     math.Round(-diff),
			})
		}
	}

	// Insight 4: Average price.
	cheapVsAvg := strings.Builder{}
	pctSaving := ((avgPrice - cheapest.Price) / avgPrice) * 100
	if pctSaving > 5 {
		_, _ = fmt.Fprintf(&cheapVsAvg, "Average price is %.0f; cheapest is %.0f%% below average", avgPrice, pctSaving)
	} else {
		_, _ = fmt.Fprintf(&cheapVsAvg, "Average price is %.0f; prices are fairly consistent", avgPrice)
	}
	insights = append(insights, DateInsight{
		Type:        "average",
		Description: cheapVsAvg.String(),
		Price:       avgPrice,
	})

	return insights
}

func avg(prices []float64) float64 {
	if len(prices) == 0 {
		return 0
	}
	var sum float64
	for _, p := range prices {
		sum += p
	}
	return sum / float64(len(prices))
}
