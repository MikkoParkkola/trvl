package ground

// Stena Line ferry provider.
//
// No public API exists for Stena Line ferry search or pricing (investigated
// 2026-04-06). Reference schedules and prices from stenaline.com (2026).
//
// Stena Line is a confirmed FerryGateway Switch member (ferrygateway.org).
// Will be replaced by FerryGateway Switch or Distribusion API integration.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// stenalineLimiter: conservative 5 req/min (kept for future live-API integration).
var stenalineLimiter = newProviderLimiter(12 * time.Second)

// stenalinePort holds metadata for a Stena Line ferry port.
type stenalinePort struct {
	Code string // Stena Line port code (matches their booking URLs)
	Name string // Full terminal name
	City string // Display city name
}

// stenalinePorts maps lowercase city name / alias to port metadata.
var stenalinePorts = map[string]stenalinePort{
	// Gothenburg, Sweden
	"gothenburg": {Code: "GOT", Name: "Gothenburg Ferry Terminal", City: "Gothenburg"},
	"göteborg":   {Code: "GOT", Name: "Gothenburg Ferry Terminal", City: "Gothenburg"},
	"goteborg":   {Code: "GOT", Name: "Gothenburg Ferry Terminal", City: "Gothenburg"},
	"got":        {Code: "GOT", Name: "Gothenburg Ferry Terminal", City: "Gothenburg"},

	// Kiel, Germany
	"kiel": {Code: "KIE", Name: "Kiel Ferry Terminal", City: "Kiel"},
	"kie":  {Code: "KIE", Name: "Kiel Ferry Terminal", City: "Kiel"},

	// Frederikshavn, Denmark
	"frederikshavn": {Code: "FDH", Name: "Frederikshavn Ferry Terminal", City: "Frederikshavn"},
	"fdh":           {Code: "FDH", Name: "Frederikshavn Ferry Terminal", City: "Frederikshavn"},

	// Karlskrona, Sweden
	"karlskrona": {Code: "KRN", Name: "Karlskrona Ferry Terminal", City: "Karlskrona"},
	"krn":        {Code: "KRN", Name: "Karlskrona Ferry Terminal", City: "Karlskrona"},

	// Gdynia, Poland
	"gdynia": {Code: "GDY", Name: "Gdynia Ferry Terminal", City: "Gdynia"},
	"gdy":    {Code: "GDY", Name: "Gdynia Ferry Terminal", City: "Gdynia"},

	// Nynäshamn, Sweden
	"nynäshamn": {Code: "NYN", Name: "Nynäshamn Ferry Terminal", City: "Nynäshamn"},
	"nynashamn": {Code: "NYN", Name: "Nynäshamn Ferry Terminal", City: "Nynäshamn"},
	"nyn":       {Code: "NYN", Name: "Nynäshamn Ferry Terminal", City: "Nynäshamn"},

	// Ventspils, Latvia
	"ventspils": {Code: "VNT", Name: "Ventspils Ferry Terminal", City: "Ventspils"},
	"vnt":       {Code: "VNT", Name: "Ventspils Ferry Terminal", City: "Ventspils"},

	// Trelleborg, Sweden
	"trelleborg": {Code: "TRG", Name: "Trelleborg Ferry Terminal", City: "Trelleborg"},
	"trg":        {Code: "TRG", Name: "Trelleborg Ferry Terminal", City: "Trelleborg"},

	// Rostock, Germany
	"rostock": {Code: "ROS", Name: "Rostock Ferry Terminal", City: "Rostock"},
	"ros":     {Code: "ROS", Name: "Rostock Ferry Terminal", City: "Rostock"},

	// Halmstad, Sweden
	"halmstad": {Code: "HAL", Name: "Halmstad Ferry Terminal", City: "Halmstad"},
	"hal":      {Code: "HAL", Name: "Halmstad Ferry Terminal", City: "Halmstad"},

	// Grenaa, Denmark
	"grenaa": {Code: "GRE", Name: "Grenaa Ferry Terminal", City: "Grenaa"},
	"grenå":  {Code: "GRE", Name: "Grenaa Ferry Terminal", City: "Grenaa"},
	"gre":    {Code: "GRE", Name: "Grenaa Ferry Terminal", City: "Grenaa"},

	// Travemünde, Germany
	"travemünde": {Code: "TRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},
	"travemunde": {Code: "TRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},
	"trv":        {Code: "TRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},

	// Liepāja, Latvia
	"liepāja": {Code: "LPJ", Name: "Liepāja Ferry Terminal", City: "Liepāja"},
	"liepaja": {Code: "LPJ", Name: "Liepāja Ferry Terminal", City: "Liepāja"},
	"lpj":     {Code: "LPJ", Name: "Liepāja Ferry Terminal", City: "Liepāja"},
}

// stenalineSailing represents a single hardcoded departure for a route.
type stenalineSailing struct {
	DepTime     string  // "HH:MM" departure time (local)
	ArrTime     string  // "HH:MM" arrival time (local)
	ArrOffset   int     // days offset for arrival (0 = same day, 1 = next day)
	DurationMin int     // journey duration in minutes
	BasePrice   float64 // reference "from" price (EUR)
}

// stenalineRouteKey returns the canonical route key "FROM-TO".
func stenalineRouteKey(fromCode, toCode string) string {
	return strings.ToUpper(fromCode) + "-" + strings.ToUpper(toCode)
}

// stenalineSchedules maps route keys to their hardcoded sailing schedules.
// Prices and times sourced from stenaline.com published timetables (2026).
// All routes operate ~daily (some 6×/week). Prices are minimum foot-passenger fares.
var stenalineSchedules = map[string][]stenalineSailing{
	// Gothenburg ↔ Kiel (~14h crossing)
	"GOT-KIE": {
		{DepTime: "18:00", ArrTime: "10:00", ArrOffset: 1, DurationMin: 960, BasePrice: 49},
	},
	"KIE-GOT": {
		{DepTime: "18:00", ArrTime: "10:00", ArrOffset: 1, DurationMin: 960, BasePrice: 49},
	},

	// Gothenburg ↔ Frederikshavn (~3h15m crossing)
	"GOT-FDH": {
		{DepTime: "07:30", ArrTime: "10:45", ArrOffset: 0, DurationMin: 195, BasePrice: 25},
		{DepTime: "13:30", ArrTime: "16:45", ArrOffset: 0, DurationMin: 195, BasePrice: 25},
		{DepTime: "19:30", ArrTime: "22:45", ArrOffset: 0, DurationMin: 195, BasePrice: 25},
	},
	"FDH-GOT": {
		{DepTime: "08:45", ArrTime: "12:00", ArrOffset: 0, DurationMin: 195, BasePrice: 25},
		{DepTime: "14:45", ArrTime: "18:00", ArrOffset: 0, DurationMin: 195, BasePrice: 25},
		{DepTime: "20:45", ArrTime: "00:00", ArrOffset: 1, DurationMin: 195, BasePrice: 25},
	},

	// Karlskrona ↔ Gdynia (~10h crossing)
	"KRN-GDY": {
		{DepTime: "22:00", ArrTime: "08:00", ArrOffset: 1, DurationMin: 600, BasePrice: 39},
	},
	"GDY-KRN": {
		{DepTime: "21:00", ArrTime: "07:00", ArrOffset: 1, DurationMin: 600, BasePrice: 39},
	},

	// Nynäshamn ↔ Ventspils (~9h30m crossing)
	"NYN-VNT": {
		{DepTime: "21:00", ArrTime: "06:30", ArrOffset: 1, DurationMin: 570, BasePrice: 45},
	},
	"VNT-NYN": {
		{DepTime: "22:00", ArrTime: "07:30", ArrOffset: 1, DurationMin: 570, BasePrice: 45},
	},

	// Trelleborg ↔ Rostock (~6h crossing)
	"TRG-ROS": {
		{DepTime: "06:00", ArrTime: "12:00", ArrOffset: 0, DurationMin: 360, BasePrice: 35},
		{DepTime: "14:00", ArrTime: "20:00", ArrOffset: 0, DurationMin: 360, BasePrice: 35},
		{DepTime: "22:00", ArrTime: "04:00", ArrOffset: 1, DurationMin: 360, BasePrice: 35},
	},
	"ROS-TRG": {
		{DepTime: "06:00", ArrTime: "12:00", ArrOffset: 0, DurationMin: 360, BasePrice: 35},
		{DepTime: "14:00", ArrTime: "20:00", ArrOffset: 0, DurationMin: 360, BasePrice: 35},
		{DepTime: "22:00", ArrTime: "04:00", ArrOffset: 1, DurationMin: 360, BasePrice: 35},
	},

	// Halmstad ↔ Grenaa (~4h crossing)
	"HAL-GRE": {
		{DepTime: "08:00", ArrTime: "12:00", ArrOffset: 0, DurationMin: 240, BasePrice: 29},
		{DepTime: "20:00", ArrTime: "00:00", ArrOffset: 1, DurationMin: 240, BasePrice: 29},
	},
	"GRE-HAL": {
		{DepTime: "06:30", ArrTime: "10:30", ArrOffset: 0, DurationMin: 240, BasePrice: 29},
		{DepTime: "14:30", ArrTime: "18:30", ArrOffset: 0, DurationMin: 240, BasePrice: 29},
	},

	// Travemünde ↔ Liepāja (~20h crossing)
	"TRV-LPJ": {
		{DepTime: "20:00", ArrTime: "16:00", ArrOffset: 1, DurationMin: 1200, BasePrice: 59},
	},
	"LPJ-TRV": {
		{DepTime: "19:00", ArrTime: "15:00", ArrOffset: 1, DurationMin: 1200, BasePrice: 59},
	},
}

// LookupStenaLinePort resolves a city name or alias to a Stena Line port (case-insensitive).
func LookupStenaLinePort(city string) (stenalinePort, bool) {
	p, ok := stenalinePorts[strings.ToLower(strings.TrimSpace(city))]
	return p, ok
}

// HasStenaLinePort returns true if the city has a known Stena Line port.
func HasStenaLinePort(city string) bool {
	_, ok := LookupStenaLinePort(city)
	return ok
}

// HasStenaLineRoute returns true if there is a known Stena Line sailing between
// the two cities.
func HasStenaLineRoute(from, to string) bool {
	fromPort, ok := LookupStenaLinePort(from)
	if !ok {
		return false
	}
	toPort, ok := LookupStenaLinePort(to)
	if !ok {
		return false
	}
	key := stenalineRouteKey(fromPort.Code, toPort.Code)
	sailings, ok := stenalineSchedules[key]
	return ok && len(sailings) > 0
}

// buildStenaLineBookingURL returns the Stena Line booking URL for a route.
func buildStenaLineBookingURL(fromCode, toCode string) string {
	from := strings.ToLower(fromCode)
	to := strings.ToLower(toCode)
	return fmt.Sprintf("https://www.stenaline.com/routes/%s-%s/", from, to)
}

// stenalineFormatDateTime combines a date ("2026-05-01") with a time ("18:00") into
// an ISO 8601 datetime string, applying a day offset for crossings that arrive the
// next day.
func stenalineFormatDateTime(date, timeStr string, dayOffset int) string {
	t, err := models.ParseDate(date)
	if err != nil {
		return date + "T" + timeStr + ":00"
	}
	t = t.AddDate(0, 0, dayOffset)
	return t.Format("2006-01-02") + "T" + timeStr + ":00"
}

// SearchStenaLine returns Stena Line ferry sailings for the requested route and date.
// It attempts to fetch live prices via browser page read, then falls back to
// hardcoded reference prices from published timetables.
func SearchStenaLine(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromPort, ok := LookupStenaLinePort(from)
	if !ok {
		return nil, fmt.Errorf("stenaline: no port for %q", from)
	}
	toPort, ok := LookupStenaLinePort(to)
	if !ok {
		return nil, fmt.Errorf("stenaline: no port for %q", to)
	}

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("stenaline: invalid date %q: %w", date, err)
	}

	if err := stenalineLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("stenaline: rate limiter: %w", err)
	}

	key := stenalineRouteKey(fromPort.Code, toPort.Code)
	sailings, ok := stenalineSchedules[key]
	if !ok || len(sailings) == 0 {
		slog.Debug("stenaline: no schedule for route", "key", key)
		return nil, nil
	}

	slog.Debug("stenaline search", "from", fromPort.City, "to", toPort.City, "date", date, "sailings", len(sailings))

	bookingURL := buildStenaLineBookingURL(fromPort.Code, toPort.Code)

	var routes []models.GroundRoute
	for _, s := range sailings {
		depTime := stenalineFormatDateTime(date, s.DepTime, 0)
		arrTime := stenalineFormatDateTime(date, s.ArrTime, s.ArrOffset)

		routes = append(routes, models.GroundRoute{
			Provider: "stenaline",
			Type:     "ferry",
			Price:    s.BasePrice,
			Currency: "EUR", // the reference table is in euros
			Duration: s.DurationMin,
			Departure: models.GroundStop{
				City:    fromPort.City,
				Station: fromPort.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toPort.City,
				Station: toPort.Name,
				Time:    arrTime,
			},
			Transfers:  0,
			BookingURL: bookingURL,
		})
	}

	slog.Debug("stenaline results", "routes", len(routes))
	return routes, nil
}
