package ground

import (
	"regexp"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Rome2Rio's SSR anchor text spells out every segment of a route in a regular
// prose grammar, e.g.:
//
//	"Take the ferry from Helsinki to Tallinn ferry ferry
//	 Fly from Lennart Meri International Airport (TLL) to Luton Airport (LTN) plane plane TLL - LTN
//	 Take the train from Luton Airport Parkway to London St Pancras Intl train train"
//
// Each segment is "<verb> from <A> to <B> <mode-token>...". Parsing this yields
// the true per-leg modes AND endpoints — which the route= URL param alone does
// not carry — so downstream per-leg pricing can query the real intermediate
// stops instead of mispricing every leg as the overall origin→destination.
var rome2rioSegmentRE = regexp.MustCompile(
	`(?:Take the (car ferry|car train|ferry|bus|train|rideshare|subway|tram|taxi)|(Drive)|(Fly)) from (.+?) to (.+?) (?:carferry|cartrain|ferry|bus|train|plane|car|rideshare|subway|tram|taxi)\b`)

// iataInParen matches an airport's IATA code in trailing parentheses, e.g.
// "Luton Airport (LTN)" -> "LTN".
var iataInParen = regexp.MustCompile(`\(([A-Z]{3})\)`)

// segmentModeNormalize maps a Rome2Rio verb/mode to trvl's canonical leg type.
func segmentModeNormalize(takeMode, drive, fly string) string {
	switch {
	case fly != "":
		return "fly"
	case drive != "":
		return "drive"
	}
	switch strings.ToLower(strings.TrimSpace(takeMode)) {
	case "car ferry", "ferry":
		return "ferry"
	case "car train", "train":
		return "train"
	case "bus":
		return "bus"
	default:
		return strings.ToLower(strings.TrimSpace(takeMode))
	}
}

// cleanRome2RioStop splits a raw stop string into a pricing-friendly city token
// and the full station label. An airport keeps its IATA code as the city (so a
// flight leg can resolve it); a "City, Terminal" string keeps the city prefix.
func cleanRome2RioStop(raw string) (city, station string) {
	station = strings.TrimSpace(raw)
	if m := iataInParen.FindStringSubmatch(station); m != nil {
		return m[1], station
	}
	if i := strings.IndexByte(station, ','); i > 0 {
		return strings.TrimSpace(station[:i]), station
	}
	return station, station
}

// parseRome2RioSegments extracts the ordered per-leg breakdown from a route's
// anchor text. Returns nil when the text carries no recognizable segments, so
// callers can fall back to the route-name mode chain.
func parseRome2RioSegments(text string) []models.GroundLeg {
	matches := rome2rioSegmentRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	legs := make([]models.GroundLeg, 0, len(matches))
	for _, m := range matches {
		mode := segmentModeNormalize(m[1], m[2], m[3])
		fromCity, fromStation := cleanRome2RioStop(m[4])
		toCity, toStation := cleanRome2RioStop(m[5])
		if mode == "" || fromCity == "" || toCity == "" {
			continue
		}
		legs = append(legs, models.GroundLeg{
			Type:      mode,
			Provider:  "rome2rio",
			Departure: models.GroundStop{City: fromCity, Station: fromStation},
			Arrival:   models.GroundStop{City: toCity, Station: toStation},
		})
	}
	if len(legs) == 0 {
		return nil
	}
	return legs
}

// distinctSegmentModes returns the ordered, de-duplicated modes of a parsed leg
// chain (used to type the route and detect multi-mode "mixed" itineraries).
func distinctSegmentModes(legs []models.GroundLeg) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range legs {
		if l.Type == "" || seen[l.Type] {
			continue
		}
		seen[l.Type] = true
		out = append(out, l.Type)
	}
	return out
}
