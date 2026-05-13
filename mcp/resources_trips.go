package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

func (s *Server) readTripsList() (*ResourcesReadResult, error) {
	store, err := defaultTripStore()
	if err != nil {
		return nil, err
	}
	active := store.Active()
	b, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return nil, err
	}
	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      "trvl://trips",
			MimeType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

// readTripByURI returns a specific trip by its URI (trvl://trips/{id}).
func (s *Server) readTripByURI(uri string) (*ResourcesReadResult, error) {
	id := strings.TrimPrefix(uri, "trvl://trips/")
	store, err := defaultTripStore()
	if err != nil {
		return nil, err
	}
	tripObj, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(tripObj, "", "  ")
	if err != nil {
		return nil, err
	}
	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      uri,
			MimeType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

// readTripsUpcoming returns the next upcoming trip as plain text.
func (s *Server) readTripsUpcoming() (*ResourcesReadResult, error) {
	store, err := defaultTripStore()
	if err != nil {
		return nil, err
	}
	upcoming := store.Upcoming(30 * 24 * time.Hour)

	var text string
	if len(upcoming) == 0 {
		text = "No upcoming trips in the next 30 days."
	} else {
		t := upcoming[0]
		now := time.Now()
		first := trips.FirstLegStart(t)
		if first.IsZero() {
			text = fmt.Sprintf("Next: %s (%s)", t.Name, t.Status)
		} else {
			d := first.Sub(now)
			days := int(d.Hours()) / 24
			text = fmt.Sprintf("Next: %s — departs in %d days (%s)", t.Name, days, first.Format("Mon Jan 02 15:04"))
		}
		for _, leg := range t.Legs {
			conf := "planned"
			if leg.Confirmed {
				conf = "confirmed"
			}
			line := fmt.Sprintf("  %s %s->%s", leg.Type, leg.From, leg.To)
			if leg.Provider != "" {
				line += " " + leg.Provider
			}
			if leg.StartTime != "" {
				line += " " + leg.StartTime
			}
			if leg.Price > 0 {
				line += fmt.Sprintf(" %.0f %s", leg.Price, leg.Currency)
			}
			line += " (" + conf + ")"
			if leg.Reference != "" {
				line += " ref:" + leg.Reference
			}
			text += "\n" + line
		}
	}

	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      "trvl://trips/upcoming",
			MimeType: "text/plain",
			Text:     text,
		}},
	}, nil
}

// readTripsAlerts returns current trip alerts as JSON.
func (s *Server) readTripsAlerts() (*ResourcesReadResult, error) {
	store, err := defaultTripStore()
	if err != nil {
		return nil, err
	}
	alerts := store.Alerts(false)
	b, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return nil, err
	}
	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      "trvl://trips/alerts",
			MimeType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

// readOnboarding returns the onboarding guide. When ~/.trvl/preferences.json
// exists and has a home airport configured it returns a compact profile
// summary; otherwise it returns the full questionnaire.
func readOnboarding() (*ResourcesReadResult, error) {
	// Detect preferences file state.
	home, _ := os.UserHomeDir()
	prefPath := filepath.Join(home, ".trvl", "preferences.json")

	p, _ := preferences.LoadFrom(prefPath)
	profileDone := p != nil && len(p.HomeAirports) > 0

	var text string
	if profileDone {
		text = buildProfileSummary(p)
	} else {
		text = onboardingQuestionnaire
	}

	return &ResourcesReadResult{
		Contents: []ResourceContent{{
			URI:      "trvl://onboarding",
			MimeType: "text/plain",
			Text:     text,
		}},
	}, nil
}

// buildProfileSummary returns a short "profile complete" block.
func buildProfileSummary(p *preferences.Preferences) string {
	var b strings.Builder
	b.WriteString("TRVL PROFILE — COMPLETE\n")
	b.WriteString(strings.Repeat("=", 40))
	b.WriteString("\n\n")
	_, _ = fmt.Fprintf(&b, "Home airports: %s\n", strings.Join(p.HomeAirports, ", "))
	if len(p.HomeCities) > 0 {
		_, _ = fmt.Fprintf(&b, "Home cities:   %s\n", strings.Join(p.HomeCities, ", "))
	}
	if p.DisplayCurrency != "" {
		_, _ = fmt.Fprintf(&b, "Currency:      %s\n", p.DisplayCurrency)
	}
	if p.Nationality != "" {
		_, _ = fmt.Fprintf(&b, "Nationality:   %s\n", p.Nationality)
	}
	if len(p.FrequentFlyerPrograms) > 0 {
		b.WriteString("FFP status:    ")
		for i, ff := range p.FrequentFlyerPrograms {
			if i > 0 {
				b.WriteString(", ")
			}
			_, _ = fmt.Fprintf(&b, "%s %s", ff.Alliance, ff.Tier)
			if ff.AirlineCode != "" {
				_, _ = fmt.Fprintf(&b, " (%s)", ff.AirlineCode)
			}
		}
		b.WriteString("\n")
	}
	if len(p.LoyaltyAirlines) > 0 {
		_, _ = fmt.Fprintf(&b, "Loyalty air:   %s\n", strings.Join(p.LoyaltyAirlines, ", "))
	}
	if len(p.LoungeCards) > 0 {
		_, _ = fmt.Fprintf(&b, "Lounge cards:  %s\n", strings.Join(p.LoungeCards, ", "))
	}
	if len(p.LoyaltyHotels) > 0 {
		_, _ = fmt.Fprintf(&b, "Loyalty hotel: %s\n", strings.Join(p.LoyaltyHotels, ", "))
	}
	if p.BudgetPerNightMax > 0 {
		_, _ = fmt.Fprintf(&b, "Hotel budget:  %.0f-%.0f/night\n", p.BudgetPerNightMin, p.BudgetPerNightMax)
	}
	if p.BudgetFlightMax > 0 {
		_, _ = fmt.Fprintf(&b, "Max flight:    %.0f\n", p.BudgetFlightMax)
	}
	b.WriteString("\nProfile is ready. Use get_preferences for full detail.")
	return b.String()
}

const onboardingQuestionnaire = `TRVL ONBOARDING — BUILD USER PROFILE
=====================================

No preference profile found (~/.trvl/preferences.json). Ask the user the
questions below to build their profile, then save it with update_preferences.

APPROACH: Ask one category at a time. Do not dump all questions at once.
Wait for each answer before asking the next group. Skip any question the
user says is not applicable. After all categories, confirm and save.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 1 — ESSENTIALS (ask first)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Q1.1  Home airport
      "What airport do you usually fly from? (e.g. HEL, LHR, JFK)"
      → field: home_airports (array of IATA codes)

Q1.2  Display currency
      "What currency should I show prices in? (e.g. EUR, GBP, USD)"
      → field: display_currency

Q1.3  Nationality
      "What's your nationality? (2-letter country code, e.g. FI, GB, US)
       I use this to warn you about visa requirements."
      → field: nationality

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 2 — LOYALTY & STATUS (ask second — key differentiator)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Q2.1  Frequent flyer status
      "Do you have any airline loyalty status?
       E.g. Oneworld Sapphire/Emerald, Star Alliance Gold, SkyTeam Elite Plus.
       This unlocks lounge access, seat upgrades, and priority boarding hints."
      → field: frequent_flyer_programs
        format: [{"alliance": "oneworld", "tier": "sapphire", "airline_code": "AY"}]
        alliance values: oneworld | star_alliance | skyteam
        tier values (oneworld): ruby | sapphire | emerald
        tier values (star alliance): silver | gold
        tier values (skyteam): elite | elite_plus

Q2.2  Frequent flyer memberships
      "Which airline frequent flyer programs are you a member of?
       (Even without status — miles still count. E.g. AY Plus, Flying Blue, Miles&More)"
      → field: loyalty_airlines (array of IATA codes, e.g. ["AY", "KL", "LH"])

Q2.3  Lounge access cards
      "Do you have any lounge access cards?
       E.g. Priority Pass, Diners Club, DragonPass, Amex Platinum lounge benefit."
      → field: lounge_cards (array of card names)

Q2.4  Hotel loyalty programs
      "Any hotel loyalty programs?
       E.g. Marriott Bonvoy, IHG One Rewards, Hilton Honors, World of Hyatt."
      → field: loyalty_hotels (array of programme names)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 3 — TRAVEL STYLE (ask third)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Q3.1  Luggage
      "Do you travel carry-on only, or do you check bags?"
      → field: carry_on_only (true/false)

Q3.2  Stops
      "Do you prefer direct/nonstop flights, or are connections fine?"
      → field: prefer_direct (true/false)

Q3.3  Seat preference
      "Window, aisle, or no preference?"
      → field: seat_preference ("window" | "aisle" | "no_preference")

Q3.4  Red-eye flights
      "Are overnight (red-eye) flights OK for you?"
      → field: red_eye_ok (true/false)

Q3.5  Flight time window (optional)
      "Any flights you won't take? E.g. 'nothing before 7am' or 'not after 10pm'."
      → fields: flight_time_earliest ("07:00"), flight_time_latest ("22:00")

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 4 — ACCOMMODATION (ask fourth)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Q4.1  Hotel standard
      "What's the minimum hotel star rating or review score you'd stay at?
       E.g. '4 stars', '4.0 on Google', or 'any'."
      → fields: min_hotel_stars (int), min_hotel_rating (float 0-10, e.g. 8.0)

Q4.2  Shared rooms
      "Would you ever stay in a hostel or shared dorm, or hotels only?"
      → field: no_dormitories (true = hotels only)

Q4.3  Private bathroom
      "Do you need a private en-suite bathroom, or shared facilities are OK?"
      → field: ensuite_only (true/false)

Q4.4  Wifi
      "Do you need fast wifi for work / co-working?"
      → field: fast_wifi_needed (true/false)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 5 — BUDGET (ask fifth)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Q5.1  Hotel budget
      "What's your typical hotel budget per night?
       E.g. '80-150 EUR' or 'up to 200 USD'."
      → fields: budget_per_night_min, budget_per_night_max

Q5.2  Max flight price
      "Is there a one-way flight price above which you won't book?
       E.g. 'I won't pay more than 400 EUR for a flight'."
      → field: budget_flight_max

Q5.3  Deal style
      "How do you approach deals? Pick one:
        price    — you'll take the 6am connection to save money
        comfort  — you'll pay more for convenience / direct / better seats
        balanced — you weigh both"
      → field: deal_tolerance ("price" | "comfort" | "balanced")

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CATEGORY 6 — CONTEXT (optional, ask last)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Q6.1  Usual travel companions
      "Do you usually travel solo, as a couple, or with family?"
      → field: default_companions (0=solo, 1=couple, 2+=family)

Q6.2  Trip types
      "What kinds of trips do you take most?
       city_break | beach | adventure | business | remote_work"
      → field: trip_types (array)

Q6.3  Languages
      "What languages do you speak? (helps with destination suggestions)"
      → field: languages (array of ISO 639-1 codes, e.g. ["en","fi"])

Q6.4  Bucket list (optional)
      "Any dream destinations you'd love to visit?"
      → field: bucket_list (array of place names)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
AFTER COLLECTING ANSWERS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. Summarise what you've collected: show the user a plain-text recap.
2. Ask: "Does this look right? Anything to change?"
3. Once confirmed, call update_preferences with all collected fields.
4. Tell the user: "Profile saved — I'll use these preferences for all searches."

The profile is a living document. Update it whenever the user mentions a
change (new status, moved city, new lounge card, etc.).
`

const popularAirports = `HEL - Helsinki, Finland
JFK - New York (John F. Kennedy), USA
LHR - London Heathrow, UK
NRT - Tokyo Narita, Japan
CDG - Paris Charles de Gaulle, France
LAX - Los Angeles, USA
SIN - Singapore Changi, Singapore
DXB - Dubai, UAE
FRA - Frankfurt, Germany
AMS - Amsterdam Schiphol, Netherlands
HND - Tokyo Haneda, Japan
ICN - Seoul Incheon, South Korea
SFO - San Francisco, USA
ORD - Chicago O'Hare, USA
BKK - Bangkok Suvarnabhumi, Thailand
IST - Istanbul, Turkey
MUC - Munich, Germany
FCO - Rome Fiumicino, Italy
MAD - Madrid Barajas, Spain
BCN - Barcelona El Prat, Spain
ZRH - Zurich, Switzerland
HKG - Hong Kong, China
SYD - Sydney Kingsford Smith, Australia
MIA - Miami, USA
EWR - Newark Liberty, USA
ARN - Stockholm Arlanda, Sweden
OSL - Oslo Gardermoen, Norway
CPH - Copenhagen, Denmark
LIS - Lisbon, Portugal
VIE - Vienna, Austria
ATL - Atlanta Hartsfield-Jackson, USA
DEN - Denver, USA
SEA - Seattle-Tacoma, USA
BOS - Boston Logan, USA
DOH - Doha Hamad, Qatar
DEL - Delhi Indira Gandhi, India
BOM - Mumbai, India
PEK - Beijing Capital, China
PVG - Shanghai Pudong, China
KUL - Kuala Lumpur, Malaysia
MEX - Mexico City, Mexico
GRU - Sao Paulo Guarulhos, Brazil
JNB - Johannesburg, South Africa
CAI - Cairo, Egypt
DUS - Dusseldorf, Germany
HAM - Hamburg, Germany
MXP - Milan Malpensa, Italy
PMI - Palma de Mallorca, Spain
TLV - Tel Aviv Ben Gurion, Israel
AKL - Auckland, New Zealand`

const flightSearchGuide = `# Flight Search Guide

## search_flights

Search for flights on a specific date. Returns real-time pricing from Google Flights.

### Required Parameters
- **origin**: IATA airport code (e.g., HEL, JFK, NRT)
- **destination**: IATA airport code
- **departure_date**: Date in YYYY-MM-DD format

### Optional Parameters
- **return_date**: For round-trip searches (YYYY-MM-DD)
- **cabin_class**: economy (default), premium_economy, business, first
- **max_stops**: any (default), nonstop, one_stop, two_plus
- **sort_by**: cheapest (default), duration, departure, arrival

### Examples

**One-way flight:**
` + "```json" + `
{"origin": "HEL", "destination": "NRT", "departure_date": "2026-06-15"}
` + "```" + `

**Round-trip flight:**
` + "```json" + `
{"origin": "HEL", "destination": "NRT", "departure_date": "2026-06-15", "return_date": "2026-06-22"}
` + "```" + `

**Nonstop business class:**
` + "```json" + `
{"origin": "JFK", "destination": "LHR", "departure_date": "2026-06-15", "cabin_class": "business", "max_stops": "nonstop"}
` + "```" + `

## search_dates

Find the cheapest flight prices across a date range.

### Required Parameters
- **origin**: IATA airport code
- **destination**: IATA airport code
- **start_date**: Start of range (YYYY-MM-DD)
- **end_date**: End of range (YYYY-MM-DD)

### Optional Parameters
- **trip_duration**: Days for round-trip (e.g., 7)
- **is_round_trip**: true/false (default: false)

### Examples

**Cheapest one-way dates in June:**
` + "```json" + `
{"origin": "HEL", "destination": "NRT", "start_date": "2026-06-01", "end_date": "2026-06-30"}
` + "```" + `

**Cheapest round-trip week in June:**
` + "```json" + `
{"origin": "HEL", "destination": "NRT", "start_date": "2026-06-01", "end_date": "2026-06-30", "is_round_trip": true, "trip_duration": 7}
` + "```" + `

## Tips

- Use **search_dates** first to find the cheapest day, then **search_flights** for full details on that day
- Airport codes are case-insensitive (hel = HEL)
- Prices are real-time from Google Flights and may change
- Round-trip searches often show lower per-leg prices than one-way
`

const hotelSearchGuide = `# Hotel Search Guide

## search_hotels

Search for hotels in a location. Returns real-time pricing from Google Hotels.

### Required Parameters
- **location**: City name, neighborhood, or address (e.g., "Helsinki", "Shibuya Tokyo", "Manhattan New York")
- **check_in**: Check-in date (YYYY-MM-DD)
- **check_out**: Check-out date (YYYY-MM-DD)

### Optional Parameters
- **guests**: Number of guests (default: 2)
- **stars**: Minimum star rating 1-5 (default: no filter)
- **sort**: relevance (default), price, rating, distance

### Examples

**Basic hotel search:**
` + "```json" + `
{"location": "Helsinki", "check_in": "2026-06-15", "check_out": "2026-06-18"}
` + "```" + `

**4+ star hotels sorted by price:**
` + "```json" + `
` + "```" + `

## hotel_prices

Compare prices across booking providers for a specific hotel.

### Required Parameters
- **hotel_id**: Google Hotels property ID (from search_hotels results)
- **check_in**: Check-in date (YYYY-MM-DD)
- **check_out**: Check-out date (YYYY-MM-DD)

### Example

` + "```json" + `
{"hotel_id": "/g/11b6d4_v_4", "check_in": "2026-06-15", "check_out": "2026-06-18"}
` + "```" + `

## Tips

- Use **search_hotels** to find options, then **hotel_prices** to compare booking providers for the best deal
- Location can be specific ("Shibuya Tokyo") or general ("Tokyo")
- Prices shown are per night
- The hotel_id from search results is needed for price comparison
- Star ratings and guest ratings are different: stars = hotel class, rating = guest reviews out of 5
`
