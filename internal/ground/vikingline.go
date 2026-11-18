package ground

// Viking Line ferry provider.
//
// Architecture: Viking Line's website (www.vikingline.fi/se/com) is behind
// Imperva WAF with no public REST API. Their booking site
// (booking.vikingline.com) exposes an agent-only API (/agent-api/v1/) that
// requires authentication (VL-CST CSRF token + agent login) and is not
// suitable for automated integration.
//
// Reference schedules sourced from vikingline.com/fi published timetables (2026).
// All routes operate daily. Prices are minimum foot-passenger fares.
//
// Integration path (investigated 2026-04-11):
//   - Distribusion: covers bus/train/ferry but Viking Line is NOT confirmed as a
//     Distribusion carrier. Distribusion may cover these routes via other
//     operators. Requires a live API key to verify (GET /marketing_carriers).
//   - FerryGateway Switch (ferrygateway.org): Viking Line IS a confirmed member.
//     Standard API v1.3.0 covers search, booking, meals, vehicles, transfers.
//     Contact: [email protected] to register as an agent.
//   - Lyko (lyko.tech): multi-operator ferry API (200+ lines), Viking Line
//     coverage unconfirmed but likely given Baltic coverage.
//   - Direct Ferries / go-ferry.com / Omio: all distribute Viking Line but via
//     affiliate/redirect models, not direct API access.
//
// Recommended next step: register at ferrygateway.org for FerryGateway Switch
// access. This gives direct API access to Viking Line (plus Tallink/Silja,
// Eckerö Line, Stena Line, DFDS, and others already in this codebase).

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// vikinglinePort holds metadata for a Viking Line ferry port.
type vikinglinePort struct {
	Code string // Port code (HEL, TLL, STO, TUR, MAR)
	Name string // Full terminal name
	City string // Display city name
}

// vikinglinePorts maps lowercase city name / alias to port metadata.
var vikinglinePorts = map[string]vikinglinePort{
	// Helsinki
	"helsinki": {Code: "HEL", Name: "Helsinki West Terminal", City: "Helsinki"},
	"hel":      {Code: "HEL", Name: "Helsinki West Terminal", City: "Helsinki"},

	// Tallinn
	"tallinn": {Code: "TLL", Name: "Tallinn Old City Harbour", City: "Tallinn"},
	"tll":     {Code: "TLL", Name: "Tallinn Old City Harbour", City: "Tallinn"},
	"tln":     {Code: "TLL", Name: "Tallinn Old City Harbour", City: "Tallinn"},

	// Stockholm
	"stockholm": {Code: "STO", Name: "Stockholm Stadsgårdshamnen", City: "Stockholm"},
	"sto":       {Code: "STO", Name: "Stockholm Stadsgårdshamnen", City: "Stockholm"},

	// Turku (also known as Åbo in Swedish)
	"turku": {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},
	"tur":   {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},
	"åbo":   {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},
	"abo":   {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},

	// Mariehamn (Åland)
	"mariehamn": {Code: "MAR", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"mar":       {Code: "MAR", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
}

// vikinglineSchedule represents a single hardcoded departure for a route.
type vikinglineSchedule struct {
	Ship      string  // Ship name
	DepTime   string  // "HH:MM" departure time (local)
	ArrTime   string  // "HH:MM" arrival time (local, may have +1 suffix handled via ArrOffset)
	ArrOffset int     // days offset for arrival (0 = same day, 1 = next day)
	Duration  int     // journey duration in minutes
	BasePrice float64 // reference "from" price (EUR, foot passenger)
	Currency  string  // base currency for the price
}

// vikinglineSchedules maps route keys "FROM-TO" to their hardcoded schedules.
// Times and prices sourced from vikingline.com/fi published timetables (2026).
// All routes operate daily. Prices are minimum foot-passenger fares.
var vikinglineSchedules = map[string][]vikinglineSchedule{
	// Helsinki ↔ Tallinn (Viking XPRS, ~2h crossing)
	"HEL-TLL": {
		{Ship: "Viking XPRS", DepTime: "09:30", ArrTime: "11:30", ArrOffset: 0, Duration: 120, BasePrice: 22, Currency: "EUR"},
		{Ship: "Viking XPRS", DepTime: "12:30", ArrTime: "14:30", ArrOffset: 0, Duration: 120, BasePrice: 22, Currency: "EUR"},
		{Ship: "Viking XPRS", DepTime: "18:30", ArrTime: "20:30", ArrOffset: 0, Duration: 120, BasePrice: 22, Currency: "EUR"},
	},
	"TLL-HEL": {
		{Ship: "Viking XPRS", DepTime: "07:30", ArrTime: "09:30", ArrOffset: 0, Duration: 120, BasePrice: 22, Currency: "EUR"},
		{Ship: "Viking XPRS", DepTime: "15:30", ArrTime: "17:30", ArrOffset: 0, Duration: 120, BasePrice: 22, Currency: "EUR"},
		{Ship: "Viking XPRS", DepTime: "22:45", ArrTime: "00:45", ArrOffset: 1, Duration: 120, BasePrice: 22, Currency: "EUR"},
	},

	// Helsinki ↔ Stockholm (overnight, ~17-18h crossing)
	"HEL-STO": {
		{Ship: "Viking Line", DepTime: "17:00", ArrTime: "10:30", ArrOffset: 1, Duration: 1050, BasePrice: 39, Currency: "EUR"},
	},
	"STO-HEL": {
		{Ship: "Viking Line", DepTime: "16:30", ArrTime: "10:10", ArrOffset: 1, Duration: 1060, BasePrice: 39, Currency: "EUR"},
	},

	// Turku ↔ Stockholm via Åland (Viking Grace / Viking Glory)
	"TUR-STO": {
		{Ship: "Viking Grace", DepTime: "08:45", ArrTime: "18:55", ArrOffset: 0, Duration: 610, BasePrice: 29, Currency: "EUR"},
		{Ship: "Viking Glory", DepTime: "20:55", ArrTime: "06:10", ArrOffset: 1, Duration: 555, BasePrice: 29, Currency: "EUR"},
	},
	"STO-TUR": {
		{Ship: "Viking Grace", DepTime: "07:45", ArrTime: "18:55", ArrOffset: 0, Duration: 670, BasePrice: 29, Currency: "EUR"},
		{Ship: "Viking Glory", DepTime: "19:45", ArrTime: "06:00", ArrOffset: 1, Duration: 615, BasePrice: 29, Currency: "EUR"},
	},

	// Stockholm → Mariehamn (Viking Cinderella)
	"STO-MAR": {
		{Ship: "Viking Cinderella", DepTime: "18:00", ArrTime: "23:30", ArrOffset: 0, Duration: 330, BasePrice: 19, Currency: "EUR"},
	},
}

// LookupVikingLinePort resolves a city name or alias to a Viking Line port (case-insensitive).
func LookupVikingLinePort(city string) (vikinglinePort, bool) {
	p, ok := vikinglinePorts[strings.ToLower(strings.TrimSpace(city))]
	return p, ok
}

// HasVikingLinePort returns true if the city has a known Viking Line port.
func HasVikingLinePort(city string) bool {
	_, ok := LookupVikingLinePort(city)
	return ok
}

// HasVikingLineRoute returns true if there is a known Viking Line sailing between
// the two cities.
func HasVikingLineRoute(from, to string) bool {
	fromPort, ok := LookupVikingLinePort(from)
	if !ok {
		return false
	}
	toPort, ok := LookupVikingLinePort(to)
	if !ok {
		return false
	}
	key := fromPort.Code + "-" + toPort.Code
	sailings, ok := vikinglineSchedules[key]
	return ok && len(sailings) > 0
}

// buildVikingLineBookingURL returns the Viking Line booking URL for a route and date.
func buildVikingLineBookingURL(fromCode, toCode, date string) string {
	return fmt.Sprintf(
		"https://www.vikingline.fi/find-trip/single/?dep=%s&arr=%s&depDate=%s&adults=1",
		fromCode, toCode, date,
	)
}

// vikinglineFormatDateTime combines a date ("2026-05-01") with a time ("HH:MM") into
// an ISO 8601 datetime string, applying a day offset for crossings that arrive the
// next day.
func vikinglineFormatDateTime(date, timeStr string, dayOffset int) string {
	t, err := models.ParseDate(date)
	if err != nil {
		return date + "T" + timeStr + ":00"
	}
	t = t.AddDate(0, 0, dayOffset)
	return t.Format("2006-01-02") + "T" + timeStr + ":00"
}

// SearchVikingLine searches for Viking Line ferry sailings using the published
// reference timetables. Will be replaced by FerryGateway Switch API integration
// (see package doc for details).
func SearchVikingLine(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromPort, ok := LookupVikingLinePort(from)
	if !ok {
		return nil, fmt.Errorf("vikingline: no port for %q: %w", from, models.ErrNotApplicable)
	}
	toPort, ok := LookupVikingLinePort(to)
	if !ok {
		return nil, fmt.Errorf("vikingline: no port for %q: %w", to, models.ErrNotApplicable)
	}

	if currency == "" {
		currency = "EUR"
	}

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("vikingline: invalid date %q: %w", date, err)
	}

	bookingURL := buildVikingLineBookingURL(fromPort.Code, toPort.Code, date)

	key := fromPort.Code + "-" + toPort.Code
	sailings, ok := vikinglineSchedules[key]
	if !ok || len(sailings) == 0 {
		return nil, fmt.Errorf("vikingline: no sailing %s: %w", key, models.ErrNotApplicable)
	}

	slog.Debug("vikingline reference schedule", "from", fromPort.City, "to", toPort.City, "date", date, "sailings", len(sailings))

	var routes []models.GroundRoute
	for _, s := range sailings {
		depTime := vikinglineFormatDateTime(date, s.DepTime, 0)
		arrTime := vikinglineFormatDateTime(date, s.ArrTime, s.ArrOffset)

		outCurrency := s.Currency
		if outCurrency == "" {
			outCurrency = strings.ToUpper(currency)
		}

		routes = append(routes, models.GroundRoute{
			Provider: "vikingline",
			Type:     "ferry",
			Price:    s.BasePrice,
			Currency: outCurrency,
			Duration: s.Duration,
			Departure: models.GroundStop{
				City:    fromPort.City,
				Station: fromPort.Name + " (" + s.Ship + ")",
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toPort.City,
				Station: toPort.Name,
				Time:    arrTime,
			},
			Transfers:  0,
			Amenities:  []string{"Reference schedule"},
			BookingURL: bookingURL,
		})
	}

	slog.Debug("vikingline reference results", "routes", len(routes))
	return routes, nil
}
