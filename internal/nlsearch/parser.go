// Package nlsearch parses free-form natural-language travel queries into
// structured search parameters. It is shared with the MCP search_natural
// tool path so the CLI and the AI surface use identical parsing semantics
// for the basic intent + weekend resolution. The CLI surface additionally
// extracts IATA codes and "from X to Y" patterns so it can dispatch real
// searches without an LLM.
package nlsearch

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Params holds the structured parameters extracted from a free-form query.
type Params struct {
	Intent        string   `json:"intent"`          // "route", "flight", "hotel", "deals"
	Origin        string   `json:"origin"`          // IATA code (uppercase) when extractable
	Destination   string   `json:"destination"`     // IATA code (uppercase) when extractable
	Date          string   `json:"date"`            // ISO 8601 calendar date or empty
	ReturnDate    string   `json:"return_date"`     // ISO 8601 calendar date or empty
	CheckIn       string   `json:"check_in"`        // ISO 8601 calendar date (hotels)
	CheckOut      string   `json:"check_out"`       // ISO 8601 calendar date (hotels)
	MaxBudget     float64  `json:"max_budget"`      // 0 = unlimited
	TravelerCount int      `json:"traveler_count"`  // 0 = unspecified (default 1 or 2)
	Modes         []string `json:"transport_modes"` // "flight", "train", "bus", "ferry"
	Location      string   `json:"location"`        // hotel location when intent=hotel
}

// iataPattern matches a 3-letter uppercase IATA airport code.
var iataPattern = regexp.MustCompile(`\b([A-Z]{3})\b`)

// fromToPattern captures "from X to Y" with X and Y being IATA codes.
var fromToPattern = regexp.MustCompile(`(?i)from\s+([A-Z]{3})\s+to\s+([A-Z]{3})`)

// fromToCityPattern captures "from <CityName> to <CityName>" with city names.
// City names may be 1-3 capitalized words (e.g. "New York", "San Francisco").
// We match anything between "from" and "to"/"on"/end-of-string and resolve
// against models.AirportNames in cityToIATA().
var fromToCityPattern = regexp.MustCompile(`(?i)from\s+([A-Za-z][A-Za-z\s.\-]{1,40}?)\s+to\s+([A-Za-z][A-Za-z\s.\-]{1,40}?)(?:\s+on|\s+for|\s+next|\s+this|\s+in|,|$)`)

// isoDatePattern captures ISO 8601 calendar dates (year-month-day, dash-separated).
var isoDatePattern = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)

// naturalDatePattern captures "<day> <Month> <year>" e.g. "7 May 2026".
var naturalDatePattern = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(jan|january|feb|february|mar|march|apr|april|may|jun|june|jul|july|aug|august|sep|september|sept|oct|october|nov|november|dec|december)\s+(\d{4})\b`)

// naturalDateAltPattern captures "<Month> <day>,? <year>" e.g. "May 7, 2026".
var naturalDateAltPattern = regexp.MustCompile(`(?i)\b(jan|january|feb|february|mar|march|apr|april|may|jun|june|jul|july|aug|august|sep|september|sept|oct|october|nov|november|dec|december)\s+(\d{1,2}),?\s+(\d{4})\b`)

// monthNumberByName maps short and long month names (lowercase) to month numbers.
var monthNumberByName = map[string]int{
	"jan": 1, "january": 1,
	"feb": 2, "february": 2,
	"mar": 3, "march": 3,
	"apr": 4, "april": 4,
	"may": 5,
	"jun": 6, "june": 6,
	"jul": 7, "july": 7,
	"aug": 8, "august": 8,
	"sep": 9, "sept": 9, "september": 9,
	"oct": 10, "october": 10,
	"nov": 11, "november": 11,
	"dec": 12, "december": 12,
}

// cityToIATAOnce builds the reverse lookup table from models.AirportNames
// exactly once. Names with airport disambiguation suffixes ("London Heathrow",
// "Paris CDG") are also indexed by their bare city name pointing at the
// alphabetically-first IATA so common queries like "from Helsinki to London"
// resolve to a primary airport.
var (
	cityToIATAOnce sync.Once
	cityIATAIndex  map[string]string
)

func ensureCityIATAIndex() {
	cityToIATAOnce.Do(func() {
		cityIATAIndex = make(map[string]string, len(models.AirportNames)*2)
		for code, name := range models.AirportNames {
			lower := strings.ToLower(strings.TrimSpace(name))
			if lower == "" {
				continue
			}
			if _, exists := cityIATAIndex[lower]; !exists {
				cityIATAIndex[lower] = code
			}
			// Also index the bare first word for "Helsinki", "Prague", "Amsterdam".
			if idx := strings.IndexAny(lower, " -"); idx > 0 {
				bare := lower[:idx]
				if _, exists := cityIATAIndex[bare]; !exists {
					cityIATAIndex[bare] = code
				}
			}
		}
		// Common manual aliases / disambiguation winners.
		// When multiple airports share a city name we pick the primary
		// one most users mean by default.
		manual := map[string]string{
			"london":        "LHR",
			"paris":         "CDG",
			"new york":      "JFK",
			"new york city": "JFK",
			"nyc":           "JFK",
			"rome":          "FCO",
			"milan":         "MXP",
			"berlin":        "BER",
			"chicago":       "ORD",
			"los angeles":   "LAX",
			"la":            "LAX",
			"san francisco": "SFO",
			"sf":            "SFO",
			"tokyo":         "HND",
			"moscow":        "SVO",
			"beijing":       "PEK",
			"shanghai":      "PVG",
			"istanbul":      "IST",
			"helsinki":      "HEL",
			"amsterdam":     "AMS",
			"prague":        "PRG",
			"krakow":        "KRK",
			"warsaw":        "WAW",
			"copenhagen":    "CPH",
			"oslo":          "OSL",
			"stockholm":     "ARN",
			"dublin":        "DUB",
			"barcelona":     "BCN",
			"madrid":        "MAD",
			"lisbon":        "LIS",
			"frankfurt":     "FRA",
			"munich":        "MUC",
			"vienna":        "VIE",
			"zurich":        "ZRH",
			"geneva":        "GVA",
			"brussels":      "BRU",
			"budapest":      "BUD",
			"athens":        "ATH",
			"riga":          "RIX",
			"tallinn":       "TLL",
			"vilnius":       "VNO",
		}
		for k, v := range manual {
			cityIATAIndex[k] = v
		}
	})
}

// cityToIATA resolves a city name (case-insensitive) to its primary IATA
// airport code, or returns "" when no match is found.
func cityToIATA(name string) string {
	ensureCityIATAIndex()
	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "" {
		return ""
	}
	if code, ok := cityIATAIndex[clean]; ok {
		return code
	}
	return ""
}

// extractNaturalDate looks for "<day> <Month> <year>" or "<Month> <day>, <year>"
// patterns in the query and converts the first match to ISO 8601 calendar form.
// Returns "" when no match is found.
func extractNaturalDate(query string) string {
	if m := naturalDatePattern.FindStringSubmatch(query); len(m) == 4 {
		day := m[1]
		month := monthNumberByName[strings.ToLower(m[2])]
		year := m[3]
		if month > 0 {
			d := 0
			_, _ = fmt.Sscanf(day, "%d", &d)
			if d >= 1 && d <= 31 {
				if date := fmt.Sprintf("%s-%02d-%02d", year, month, d); validISODate(date) {
					return date
				}
			}
		}
	}
	if m := naturalDateAltPattern.FindStringSubmatch(query); len(m) == 4 {
		month := monthNumberByName[strings.ToLower(m[1])]
		day := m[2]
		year := m[3]
		if month > 0 {
			d := 0
			_, _ = fmt.Sscanf(day, "%d", &d)
			if d >= 1 && d <= 31 {
				if date := fmt.Sprintf("%s-%02d-%02d", year, month, d); validISODate(date) {
					return date
				}
			}
		}
	}
	return ""
}

func validISODate(date string) bool {
	parsed, err := time.Parse(time.DateOnly, date)
	return err == nil && parsed.Format(time.DateOnly) == date
}

// Heuristic extracts travel intent and parameters from a free-form query
// using keyword matching and simple date resolution. `today` must be in
// ISO 8601 calendar form.
//
// Extraction order:
//  1. Intent detection (hotel/flight/deals/route default).
//  2. IATA codes via fromToPattern (uppercase), then "from <City> to <City>"
//     resolution against models.AirportNames, then bare 3-letter uppercase tokens.
//  3. ISO 8601 dates, then natural-language dates ("7 May 2026", "May 7 2026").
//  4. "next weekend" / "this weekend" relative dates (only if no date found).
func Heuristic(query, today string) Params {
	lower := strings.ToLower(query)
	upper := strings.ToUpper(query)

	p := Params{Intent: "route"}

	// 1. Intent detection.
	switch {
	case strings.Contains(lower, "hotel") || strings.Contains(lower, "hostel") ||
		strings.Contains(lower, "accommodation") || strings.Contains(lower, "stay") ||
		strings.Contains(lower, "sleep") || strings.Contains(lower, "room") ||
		strings.Contains(lower, "check-in") || strings.Contains(lower, "check in"):
		p.Intent = "hotel"
	case strings.Contains(lower, "fly ") || strings.Contains(lower, "flying") ||
		strings.Contains(lower, "flight") || strings.Contains(lower, "airport"):
		p.Intent = "flight"
	case strings.Contains(lower, "deal") || strings.Contains(lower, "inspiration"):
		p.Intent = "deals"
	}

	// 2a. IATA extraction — explicit 3-letter codes first.
	if m := fromToPattern.FindStringSubmatch(query); len(m) == 3 {
		p.Origin = strings.ToUpper(m[1])
		p.Destination = strings.ToUpper(m[2])
	}

	// 2b. City-name extraction — only when IATA codes are absent. This handles
	// "from Helsinki to Prague" / "from New York to London" patterns advertised
	// in the tool description.
	if (p.Origin == "" || p.Destination == "") && fromToCityPattern.MatchString(query) {
		if m := fromToCityPattern.FindStringSubmatch(query); len(m) == 3 {
			if p.Origin == "" {
				if code := cityToIATA(m[1]); code != "" {
					p.Origin = code
				}
			}
			if p.Destination == "" {
				if code := cityToIATA(m[2]); code != "" {
					p.Destination = code
				}
			}
		}
	}

	// 2c. Bare uppercase IATA fallback.
	if p.Origin == "" || p.Destination == "" {
		if codes := iataPattern.FindAllString(upper, -1); len(codes) > 0 {
			filtered := filterFalsePositiveIATA(codes)
			if p.Origin == "" && p.Destination == "" {
				if len(filtered) >= 2 {
					p.Origin = filtered[0]
					p.Destination = filtered[1]
				} else if len(filtered) == 1 {
					p.Destination = filtered[0]
				}
			} else if p.Destination == "" && len(filtered) >= 1 {
				p.Destination = filtered[0]
			} else if p.Origin == "" && len(filtered) >= 1 {
				p.Origin = filtered[0]
			}
		}
	}

	// 3a. ISO 8601 dates.
	if dates := isoDatePattern.FindAllString(query, -1); len(dates) > 0 {
		validDates := make([]string, 0, len(dates))
		for _, date := range dates {
			if validISODate(date) {
				validDates = append(validDates, date)
			}
		}
		if len(validDates) > 0 {
			p.Date = validDates[0]
			p.CheckIn = validDates[0]
			if len(validDates) >= 2 {
				p.ReturnDate = validDates[1]
				p.CheckOut = validDates[1]
			}
		}
	}

	// 3b. Natural-language dates ("7 May 2026", "May 7 2026") — only if no
	// ISO date was found.
	if p.Date == "" {
		if natural := extractNaturalDate(query); natural != "" {
			p.Date = natural
			p.CheckIn = natural
		}
	}

	// 4. Relative weekend dates — only if no date was extracted.
	if p.Date == "" && (strings.Contains(lower, "next weekend") || strings.Contains(lower, "this weekend")) {
		t, _ := models.ParseDate(today)
		daysUntilSat := (6 - int(t.Weekday()) + 7) % 7
		if daysUntilSat == 0 {
			daysUntilSat = 7
		}
		sat := t.AddDate(0, 0, daysUntilSat)
		mon := sat.AddDate(0, 0, 2)
		p.Date = sat.Format("2006-01-02")
		p.CheckIn = p.Date
		p.CheckOut = mon.Format("2006-01-02")
	}

	// 5. Hotel location: when intent=hotel and we have a destination IATA,
	// surface it as Location too so the dispatcher has one source of truth.
	if p.Intent == "hotel" && p.Location == "" && p.Destination != "" {
		p.Location = p.Destination
	}

	return p
}

// commonEnglishUppercase lists three-letter words that look like IATA codes
// but are common English words. This is intentionally narrow — we only need
// to filter the words that actually appear in travel queries.
var commonEnglishUppercase = map[string]bool{
	"THE": true, "AND": true, "FOR": true, "ARE": true, "BUT": true,
	"NOT": true, "YOU": true, "ALL": true, "CAN": true, "HER": true,
	"WAS": true, "ONE": true, "OUR": true, "OUT": true, "DAY": true,
	"GET": true, "HAS": true, "HIM": true, "HIS": true, "HOW": true,
	"MAN": true, "NEW": true, "NOW": true, "OLD": true, "SEE": true,
	"TWO": true, "WAY": true, "WHO": true, "BOY": true, "DID": true,
	"ITS": true, "LET": true, "PUT": true, "SAY": true, "SHE": true,
	"TOO": true, "USE": true, "DAD": true, "MOM": true, "MAY": true,
	"USA": true, // ambiguous: could be a country code, not an airport
}

func filterFalsePositiveIATA(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if !commonEnglishUppercase[c] {
			out = append(out, c)
		}
	}
	return out
}
