package profile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// BookingExtractionPrompt returns a prompt that asks an LLM to extract
// structured booking data from a raw email. This is the primary extraction
// path when MCP sampling is available — it handles any airline, hotel, or
// ground transport email format automatically.
func BookingExtractionPrompt(subject, body string) string {
	return fmt.Sprintf(`Extract booking details from this email. Return JSON only, no markdown fencing.

Subject: %s

Body (first 2000 chars):
%s

Return a JSON object with these fields (omit fields not found):
{
  "type": "flight|hotel|ground|airbnb",
  "date": "YYYY-MM-DD (booking date, when email was sent)",
  "travel_date": "YYYY-MM-DD (departure or check-in date)",
  "from": "IATA code or city name",
  "to": "IATA code or city name",
  "provider": "airline, hotel chain, or transport company name",
  "price": 123.45,
  "currency": "EUR",
  "nights": 2,
  "stars": 4,
  "reference": "booking reference/confirmation number",
  "notes": "brief summary: route, room type, etc."
}

Rules:
- type "flight" for airline bookings, "hotel" for hotel reservations, "ground" for bus/train/ferry, "airbnb" for Airbnb/vacation rentals
- For flights: "from" and "to" should be IATA codes if recognizable (e.g. HEL, BCN), otherwise city names
- For hotels: "from" should be omitted, "to" should be the city
- "provider" should be the airline name (e.g. "KLM", "Finnair") or hotel chain (e.g. "Marriott", "Hilton")
- If this email is NOT a travel booking confirmation, return: {"type": "not_booking"}`, subject, truncateStr(body, 2000))
}

// ProfileAnalysisPrompt returns a prompt for LLM-powered travel pattern analysis.
// It feeds all bookings as JSON and asks for insights beyond what BuildProfile
// computes mechanically.
func ProfileAnalysisPrompt(bookings []Booking) string {
	data, _ := json.Marshal(bookings)
	return fmt.Sprintf(`Analyze this travel booking history and provide insights. Return JSON only.

Bookings:
%s

Return a JSON object:
{
  "personality": "adventurer|comfort_seeker|budget_optimizer|business_traveller|mixed",
  "patterns": [
    "Always books 2-3 weeks ahead",
    "Prefers European city breaks in spring"
  ],
  "recommendations": [
    "You fly KLM 8x/year — consider Flying Blue Silver status",
    "Your average hotel is 4-star at EUR 120/night — Marriott Bonvoy could save 15%%"
  ],
  "predicted_next": [
    "Based on seasonal patterns, likely trip to Southern Europe in June-July"
  ]
}`, string(data))
}

// truncateStr returns s truncated to maxLen bytes.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// --- Regex-based fallback parsers ---
// Used when LLM sampling is not available (CLI mode, HTTP transport).
// These are best-effort and cover common formats only.

// ParseFlightConfirmation attempts to extract a flight booking from an email.
// Returns nil if the email does not appear to be a flight confirmation.
func ParseFlightConfirmation(subject, body string) *Booking {
	subjectLower := strings.ToLower(subject)
	bodyLower := strings.ToLower(body)
	combined := subjectLower + " " + bodyLower

	// Check if this looks like a flight confirmation.
	flightIndicators := []string{
		"booking confirmation", "e-ticket", "itinerary", "flight confirmation",
		"your flight", "boarding pass", "e-ticket receipt", "travel confirmation",
		"booking confirmed", "reservation confirmed", "ticket confirmation",
		"your trip", "flight booking",
	}
	if !containsAny(combined, flightIndicators) {
		return nil
	}

	// Exclude hotel-only confirmations.
	hotelOnly := []string{"check-in:", "check-out:", "room rate", "your stay"}
	if containsAny(combined, hotelOnly) && !containsAny(combined, []string{"flight", "depart", "arrival"}) {
		return nil
	}

	b := &Booking{
		Type:   "flight",
		Source: "email",
	}

	// Extract airline.
	b.Provider = extractAirline(subject, body)

	// Extract route (IATA codes).
	b.From, b.To = extractRoute(body)

	// Extract price.
	b.Price, b.Currency = extractPrice(body)

	// Extract travel date.
	b.TravelDate = extractDate(body, flightDatePatterns)

	// Extract booking reference.
	b.Reference = extractReference(body)

	return b
}

// ParseHotelConfirmation attempts to extract a hotel booking from an email.
func ParseHotelConfirmation(subject, body string) *Booking {
	subjectLower := strings.ToLower(subject)
	bodyLower := strings.ToLower(body)
	combined := subjectLower + " " + bodyLower

	hotelIndicators := []string{
		"reservation confirmed", "booking confirmation", "hotel confirmation",
		"your reservation", "your stay", "check-in", "check-out",
		"room reservation", "hotel booking", "accommodation",
		"you're going to", // Airbnb
	}
	if !containsAny(combined, hotelIndicators) {
		return nil
	}

	// Must not look like a flight.
	if containsAny(combined, []string{"boarding pass", "e-ticket receipt"}) {
		return nil
	}

	b := &Booking{
		Type:   "hotel",
		Source: "email",
	}

	// Airbnb detection.
	if strings.Contains(combined, "airbnb") || strings.Contains(combined, "you're going to") {
		b.Type = "airbnb"
		b.Provider = "Airbnb"

		// Airbnb: "You're going to [City]!"
		if m := airbnbCityRe.FindStringSubmatch(body); len(m) > 1 {
			b.To = strings.TrimSpace(m[1])
		}
	}

	// Extract hotel name / provider.
	if b.Provider == "" {
		b.Provider = extractHotelName(subject, body)
	}

	// Booking.com detection.
	if strings.Contains(combined, "booking.com") {
		b.Source = "booking.com"
	}

	// Extract dates.
	b.TravelDate = extractDate(body, hotelCheckInPatterns)

	checkOut := extractDate(body, hotelCheckOutPatterns)
	b.Nights = calculateNights(b.TravelDate, checkOut)

	// Extract price.
	b.Price, b.Currency = extractPrice(body)

	// Extract star rating.
	b.Stars = extractStars(body)

	// Extract reference.
	b.Reference = extractReference(body)

	return b
}

// ParseGroundConfirmation attempts to extract a ground transport booking.
func ParseGroundConfirmation(subject, body string) *Booking {
	subjectLower := strings.ToLower(subject)
	bodyLower := strings.ToLower(body)
	combined := subjectLower + " " + bodyLower

	groundIndicators := []string{
		"ticket confirmation", "booking confirmation", "your ticket",
		"your journey", "travel confirmation", "your trip",
		"your bus", "your train", "your ferry",
	}
	groundProviders := []string{
		"flixbus", "eurostar", "thalys", "sncf", "trenitalia", "renfe",
		"deutsche bahn", "regiojet", "blablabus", "eurolines", "megabus",
		"stena line", "viking line", "tallink", "dfds", "ouigo", "italo",
		"greyhound", "amtrak", "national express", "irish ferries", "p&o",
	}

	isGround := containsAny(combined, groundIndicators) && containsAny(combined, groundProviders)
	if !isGround {
		// Also match if it's clearly a bus/train/ferry.
		transportModes := []string{"bus ticket", "train ticket", "ferry ticket", "rail ticket"}
		isGround = containsAny(combined, transportModes)
	}
	if !isGround {
		return nil
	}

	b := &Booking{
		Type:   "ground",
		Source: "email",
	}

	// Extract provider.
	for _, p := range groundProviders {
		if strings.Contains(combined, p) {
			b.Provider = titleCase(p)
			break
		}
	}
	if b.Provider == "" {
		b.Provider = extractGroundProvider(body)
	}

	// Extract route.
	b.From, b.To = extractRoute(body)
	if b.From == "" {
		b.From, b.To = extractCityRoute(body)
	}

	// Extract price.
	b.Price, b.Currency = extractPrice(body)

	// Extract travel date.
	b.TravelDate = extractDate(body, groundDatePatterns)

	// Extract reference.
	b.Reference = extractReference(body)

	return b
}

// --- Extraction helpers ---

// Pre-compiled regexps used in date extraction.
var (
	isoDateRe    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	ddmmyyyyRe   = regexp.MustCompile(`^\d{2}/\d{2}/\d{4}$`)
	dayMonYearRe = regexp.MustCompile(`(\d{1,2})\s+(\w+)\s+(\d{4})`)

	// airbnbCityRe matches "You're going to [City]!" in booking confirmation emails.
	airbnbCityRe = regexp.MustCompile(`(?i)you(?:'re|'re| are) going to ([^!]+)!`)

	// extractRoutePatterns is used in extractRoute as fallback patterns.
	extractRoutePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:flight|route|from)[:\s]+([A-Z]{3})\s*(?:→|->|–|—|-|to)\s*([A-Z]{3})`),
		regexp.MustCompile(`(?i)departing\s+([A-Z]{3}).*arriving\s+([A-Z]{3})`),
	}

	// extractCityFromRe / extractCityToRe are used in extractCityRoute.
	extractCityFromRe = regexp.MustCompile(`(?i)(?:from|departing|origin)[:\s]+([A-Za-z\s]+?)(?:\s*[,\n]|\s+to\b)`)
	extractCityToRe   = regexp.MustCompile(`(?i)(?:to|arriving|destination)[:\s]+([A-Za-z\s]+?)[\s,\n]`)

	// extractStarsRe is used in extractStars.
	extractStarsRe = regexp.MustCompile(`(?i)(\d)\s*(?:star|★|⭐)`)

	// extractHotelNamePatterns is used in extractHotelName.
	extractHotelNamePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)reservation at\s+(.+?)(?:\s*[,.\n]|$)`),
		regexp.MustCompile(`(?i)your stay at\s+(.+?)(?:\s*[,.\n]|$)`),
		regexp.MustCompile(`(?i)hotel[:\s]+(.+?)(?:\s*[,.\n]|$)`),
	}

	// flightDatePatterns is used when parsing flight confirmation emails.
	flightDatePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)depart(?:ure|ing)?[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
		regexp.MustCompile(`(?i)date[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
		regexp.MustCompile(`(?i)(\d{1,2}\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\w*\s+\d{4})`),
		regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`),
		regexp.MustCompile(`(\d{2}/\d{2}/\d{4})`),
	}

	// hotelCheckInPatterns / hotelCheckOutPatterns / groundDatePatterns for hotel/ground emails.
	hotelCheckInPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)check[\s-]?in[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
		regexp.MustCompile(`(?i)check[\s-]?in[:\s]+(\d{4}-\d{2}-\d{2})`),
		regexp.MustCompile(`(?i)check[\s-]?in[:\s]+(\d{2}/\d{2}/\d{4})`),
		regexp.MustCompile(`(?i)arrival[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
	}
	hotelCheckOutPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)check[\s-]?out[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
		regexp.MustCompile(`(?i)check[\s-]?out[:\s]+(\d{4}-\d{2}-\d{2})`),
		regexp.MustCompile(`(?i)check[\s-]?out[:\s]+(\d{2}/\d{2}/\d{4})`),
		regexp.MustCompile(`(?i)departure[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
	}
	groundDatePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)depart(?:ure|ing)?[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
		regexp.MustCompile(`(?i)date[:\s]+(\d{1,2}\s+\w+\s+\d{4})`),
		regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`),
	}
)

// titleCase capitalises the first letter of each word.
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	result := make([]byte, len(s))
	upper := true
	for i := range len(s) {
		c := s[i]
		if upper && c >= 'a' && c <= 'z' {
			c -= 32
		}
		result[i] = c
		upper = c == ' ' || c == '-'
	}
	return string(result)
}

// containsAny returns true if text contains any of the given substrings.
func containsAny(text string, substrings []string) bool {
	for _, s := range substrings {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

var airlinePatterns = map[string]string{
	"finnair":           "Finnair",
	"klm":               "KLM",
	"ryanair":           "Ryanair",
	"easyjet":           "easyJet",
	"norwegian":         "Norwegian",
	"sas scandinavian":  "SAS",
	"sas ":              "SAS",
	"lufthansa":         "Lufthansa",
	"british airways":   "British Airways",
	"air france":        "Air France",
	"iberia":            "Iberia",
	"vueling":           "Vueling",
	"wizz air":          "Wizz Air",
	"wizzair":           "Wizz Air",
	"transavia":         "Transavia",
	"eurowings":         "Eurowings",
	"tap portugal":      "TAP Portugal",
	"tap air":           "TAP Portugal",
	"aegean":            "Aegean",
	"turkish airlines":  "Turkish Airlines",
	"swiss ":            "SWISS",
	"austrian":          "Austrian",
	"brussels airlines": "Brussels Airlines",
	"lot polish":        "LOT",
	"aer lingus":        "Aer Lingus",
	"emirates":          "Emirates",
	"qatar airways":     "Qatar Airways",
	"delta":             "Delta",
	"united airlines":   "United",
	"american airlines": "American Airlines",
	"jetblue":           "JetBlue",
}

// extractAirline tries to identify the airline from subject/body.
func extractAirline(subject, body string) string {
	combined := strings.ToLower(subject + " " + body)
	for pattern, name := range airlinePatterns {
		if strings.Contains(combined, pattern) {
			return name
		}
	}
	return ""
}

// routePattern matches IATA code pairs like "HEL → BCN", "HEL - BCN", "HEL to BCN".
var routePattern = regexp.MustCompile(`\b([A-Z]{3})\s*(?:→|->|–|—|-|to)\s*([A-Z]{3})\b`)

// extractRoute extracts origin and destination IATA codes.
func extractRoute(body string) (from, to string) {
	m := routePattern.FindStringSubmatch(body)
	if len(m) >= 3 {
		return m[1], m[2]
	}

	// Try "Flight: HEL → BCN" or "Route: HEL - BCN".
	for _, re := range extractRoutePatterns {
		m = re.FindStringSubmatch(body)
		if len(m) >= 3 {
			return m[1], m[2]
		}
	}

	return "", ""
}

// extractCityRoute extracts city names from "From: City" / "To: City" patterns.
func extractCityRoute(body string) (from, to string) {
	if m := extractCityFromRe.FindStringSubmatch(body); len(m) > 1 {
		from = strings.TrimSpace(m[1])
	}
	if m := extractCityToRe.FindStringSubmatch(body); len(m) > 1 {
		to = strings.TrimSpace(m[1])
	}
	return from, to
}

// pricePatterns match common price formats: EUR 234.00, 234,00 EUR, $99, etc.
var pricePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:total|amount|price|cost|charge|fare)[:\s]*(EUR|USD|GBP|CZK|SEK|NOK|DKK|CHF|PLN|JPY)\s*(\d+[.,]?\d*)`),
	regexp.MustCompile(`(?i)(?:total|amount|price|cost|charge|fare)[:\s]*(\d+[.,]?\d*)\s*(EUR|USD|GBP|CZK|SEK|NOK|DKK|CHF|PLN|JPY)`),
	regexp.MustCompile(`(?i)(EUR|USD|GBP|CZK|SEK|NOK|DKK|CHF|PLN|JPY)\s*(\d+[.,]?\d*)`),
	regexp.MustCompile(`(?i)(\d+[.,]?\d*)\s*(EUR|USD|GBP|CZK|SEK|NOK|DKK|CHF|PLN|JPY)`),
	regexp.MustCompile(`(?i)(?:total|amount|price)[:\s]*[€$£]\s*(\d+[.,]?\d*)`),
	regexp.MustCompile(`[€$£]\s*(\d+[.,]?\d*)`),
}

// currencySymbols maps symbols to ISO codes.
var currencySymbols = map[string]string{
	"€": "EUR", "$": "USD", "£": "GBP",
}

// extractPrice extracts price and currency from body text.
func extractPrice(body string) (float64, string) {
	for _, re := range pricePatterns {
		m := re.FindStringSubmatch(body)
		if len(m) < 2 {
			continue
		}

		// Figure out which group is the number and which is the currency.
		var numStr, curr string
		for _, g := range m[1:] {
			if g == "" {
				continue
			}
			if isNumeric(g) {
				numStr = g
			} else {
				curr = g
			}
		}
		if numStr == "" && len(m) > 1 {
			numStr = m[1]
		}

		price := parseNumber(numStr)
		if price <= 0 {
			continue
		}

		// Resolve currency.
		if curr == "" {
			// Try to find currency symbol near the match.
			for sym, code := range currencySymbols {
				if strings.Contains(body, sym) {
					curr = code
					break
				}
			}
		}
		if len(curr) == 1 {
			if code, ok := currencySymbols[curr]; ok {
				curr = code
			}
		}
		curr = strings.ToUpper(curr)

		return price, curr
	}
	return 0, ""
}

// isNumeric returns true if s looks like a number (digits, dots, commas).
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' && c != ',' {
			return false
		}
	}
	return true
}

// parseNumber parses "234.00" or "234,00" to float64.
func parseNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}

	// Handle European format: 1.234,56 -> 1234.56
	if strings.Contains(s, ",") {
		// If comma is last separator, treat as decimal.
		lastComma := strings.LastIndex(s, ",")
		lastDot := strings.LastIndex(s, ".")
		if lastComma > lastDot {
			// European: dots are thousands, comma is decimal.
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			// US: commas are thousands.
			s = strings.ReplaceAll(s, ",", "")
		}
	}

	var result float64
	_, _ = fmt.Sscanf(s, "%f", &result)
	return result
}

// monthMap maps month abbreviations to numbers.
var monthMap = map[string]string{
	"jan": "01", "feb": "02", "mar": "03", "apr": "04",
	"may": "05", "jun": "06", "jul": "07", "aug": "08",
	"sep": "09", "oct": "10", "nov": "11", "dec": "12",
	"january": "01", "february": "02", "march": "03", "april": "04",
	"june": "06", "july": "07", "august": "08", "september": "09",
	"october": "10", "november": "11", "december": "12",
}

// extractDate tries patterns in order, returning the first match as YYYY-MM-DD.
func extractDate(body string, patterns []*regexp.Regexp) string {
	for _, re := range patterns {
		m := re.FindStringSubmatch(body)
		if len(m) < 2 {
			continue
		}
		dateStr := strings.TrimSpace(m[1])

		// Already ISO format?
		if isoDateRe.MatchString(dateStr) {
			return dateStr
		}

		// DD/MM/YYYY format.
		if ddmmyyyyRe.MatchString(dateStr) {
			parts := strings.Split(dateStr, "/")
			return parts[2] + "-" + parts[1] + "-" + parts[0]
		}

		// "15 Jun 2026" or "15 June 2026" format.
		dm := dayMonYearRe.FindStringSubmatch(dateStr)
		if len(dm) >= 4 {
			day := dm[1]
			if len(day) == 1 {
				day = "0" + day
			}
			monthStr := strings.ToLower(dm[2])
			month, ok := monthMap[monthStr]
			if !ok {
				// Try 3-letter abbreviation.
				if len(monthStr) >= 3 {
					month, ok = monthMap[monthStr[:3]]
				}
			}
			if ok {
				return dm[3] + "-" + month + "-" + day
			}
		}
	}
	return ""
}

// calculateNights computes nights between checkin and checkout dates (YYYY-MM-DD).
func calculateNights(checkin, checkout string) int {
	if checkin == "" || checkout == "" {
		return 0
	}
	// Simple calculation: parse dates and diff.
	ci := parseSimpleDate(checkin)
	co := parseSimpleDate(checkout)
	if ci == 0 || co == 0 || co <= ci {
		return 0
	}
	return int((co - ci) / (24 * 60 * 60))
}

// parseSimpleDate parses YYYY-MM-DD to unix timestamp (enough for day diff).
func parseSimpleDate(s string) int64 {
	if len(s) != 10 {
		return 0
	}
	var y, m, d int
	_, err := fmt.Sscanf(s, "%d-%d-%d", &y, &m, &d)
	if err != nil {
		return 0
	}
	// Rough days since epoch (good enough for diff).
	return int64(y)*365*24*60*60 + int64(m)*30*24*60*60 + int64(d)*24*60*60
}

// extractReference extracts a booking reference/confirmation number.
// Booking references are typically 5-10 alphanumeric characters with at
// least one digit (to distinguish from English words like "reference").
var refPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:booking reference|confirmation number|reservation number|booking no|confirmation code|booking code|reference number|confirmation #|booking #)[:\s]+([A-Za-z0-9]{5,10})`),
	regexp.MustCompile(`(?i)(?:PNR|booking ref)[:\s]+([A-Za-z0-9]{5,8})`),
	regexp.MustCompile(`(?i)(?:confirmation|reservation|reference|booking)\s*(?:number|#|no\.?|code)[:\s]+([A-Za-z0-9]{5,10})`),
	regexp.MustCompile(`(?i)(?:confirmation|reservation|booking No|reference)[:\s]+([A-Za-z0-9]{5,10})`),
}

func extractReference(body string) string {
	for _, re := range refPatterns {
		m := re.FindStringSubmatch(body)
		if len(m) >= 2 {
			ref := m[1]
			if looksLikeReference(ref) {
				return ref
			}
		}
	}
	return ""
}

// looksLikeReference returns true if the string looks like a booking reference
// rather than an English word. References either contain digits or are all
// uppercase (HMABCDE is a valid Airbnb ref; "reference" is not).
func looksLikeReference(s string) bool {
	if s == "" {
		return false
	}
	hasDigit := false
	allUpper := true
	for _, c := range s {
		if c >= '0' && c <= '9' {
			hasDigit = true
		}
		if c >= 'a' && c <= 'z' {
			allUpper = false
		}
	}
	return hasDigit || allUpper
}

// extractStars extracts a star rating (1-5) from body text.
func extractStars(body string) int {
	m := extractStarsRe.FindStringSubmatch(body)
	if len(m) >= 2 {
		var stars int
		_, _ = fmt.Sscanf(m[1], "%d", &stars)
		if stars >= 1 && stars <= 5 {
			return stars
		}
	}
	return 0
}

// extractHotelName tries to identify the hotel name from email content.
func extractHotelName(subject, body string) string {
	combined := subject + " " + body
	lower := strings.ToLower(combined)

	// Known hotel chains.
	chains := map[string]string{
		"marriott": "Marriott", "hilton": "Hilton", "ihg": "IHG",
		"hyatt": "Hyatt", "accor": "Accor", "wyndham": "Wyndham",
		"best western": "Best Western", "radisson": "Radisson",
		"melia": "Melia", "nh hotel": "NH Hotels",
		"booking.com": "Booking.com",
	}
	for pattern, name := range chains {
		if strings.Contains(lower, pattern) {
			return name
		}
	}

	// Try "Hotel Name" or "Your reservation at Hotel Name".
	for _, re := range extractHotelNamePatterns {
		m := re.FindStringSubmatch(combined)
		if len(m) >= 2 {
			name := strings.TrimSpace(m[1])
			if len(name) > 0 && len(name) < 100 {
				return name
			}
		}
	}

	return ""
}

// extractGroundProvider tries to identify the transport provider.
func extractGroundProvider(body string) string {
	lower := strings.ToLower(body)
	providers := map[string]string{
		"flixbus":     "FlixBus",
		"eurostar":    "Eurostar",
		"thalys":      "Thalys",
		"sncf":        "SNCF",
		"trenitalia":  "Trenitalia",
		"renfe":       "Renfe",
		"regiojet":    "RegioJet",
		"blablabus":   "BlaBlaBus",
		"megabus":     "Megabus",
		"greyhound":   "Greyhound",
		"amtrak":      "Amtrak",
		"stena line":  "Stena Line",
		"viking line": "Viking Line",
		"tallink":     "Tallink",
		"dfds":        "DFDS",
		"ouigo":       "Ouigo",
		"italo":       "Italo",
	}
	for pattern, name := range providers {
		if strings.Contains(lower, pattern) {
			return name
		}
	}
	return ""
}
