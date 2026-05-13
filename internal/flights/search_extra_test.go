package flights

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/jsonutil"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- parseOneFlight ---

func TestParseOneFlight_MinimalEntry(t *testing.T) {
	// entry[0] = flight info (minimal), entry[1] = price info.
	flightInfo := make([]any, 10)
	// No legs, no duration — should produce a valid but empty FlightResult.
	entry := []any{flightInfo, []any{[]any{nil, 350.0}, "token"}}

	fr, err := parseOneFlight(entry)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if fr.Price != 350 {
		t.Errorf("Price = %v, want 350", fr.Price)
	}
	if fr.Stops != 0 {
		t.Errorf("Stops = %d, want 0", fr.Stops)
	}
}

func TestParseOneFlight_NotArray(t *testing.T) {
	entry := []any{"not an array", []any{}}
	_, err := parseOneFlight(entry)
	if err == nil {
		t.Error("expected error when entry[0] is not array")
	}
}

func TestParseOneFlight_WithDuration(t *testing.T) {
	flightInfo := make([]any, 10)
	flightInfo[9] = float64(780) // duration in minutes
	entry := []any{flightInfo, []any{[]any{nil, 500.0}}}

	fr, err := parseOneFlight(entry)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if fr.Duration != 780 {
		t.Errorf("Duration = %d, want 780", fr.Duration)
	}
}

// --- parseLegs ---

func TestParseLegs_Nil(t *testing.T) {
	legs := parseLegs(nil)
	if legs != nil {
		t.Errorf("expected nil for nil input, got %v", legs)
	}
}

func TestParseLegs_NotArray(t *testing.T) {
	legs := parseLegs("not an array")
	if legs != nil {
		t.Errorf("expected nil for non-array, got %v", legs)
	}
}

func TestParseLegs_MixedEntries(t *testing.T) {
	// One valid leg, one invalid entry.
	validLeg := make([]any, 23)
	validLeg[3] = "HEL"
	validLeg[4] = "Helsinki-Vantaa"
	validLeg[6] = "NRT"
	validLeg[5] = "Narita"
	validLeg[11] = float64(780)

	legs := parseLegs([]any{validLeg, "invalid", nil, 42})
	if len(legs) != 1 {
		t.Fatalf("expected 1 valid leg, got %d", len(legs))
	}
	if legs[0].DepartureAirport.Code != "HEL" {
		t.Errorf("dep code = %q, want HEL", legs[0].DepartureAirport.Code)
	}
}

// --- parseOneLeg ---

func TestParseOneLeg_Full(t *testing.T) {
	leg := make([]any, 23)
	leg[3] = "HEL"
	leg[4] = "Helsinki-Vantaa Airport"
	leg[5] = "Narita International Airport"
	leg[6] = "NRT"
	leg[8] = []any{float64(10), float64(30)}
	leg[10] = []any{float64(7), float64(15)}
	leg[11] = float64(780)
	leg[20] = []any{float64(2026), float64(6), float64(15)}
	leg[21] = []any{float64(2026), float64(6), float64(16)}
	leg[22] = []any{"AY", "79", nil, "Finnair"}

	fl := parseOneLeg(leg)

	if fl.DepartureAirport.Code != "HEL" {
		t.Errorf("dep code = %q", fl.DepartureAirport.Code)
	}
	if fl.DepartureAirport.Name != "Helsinki-Vantaa Airport" {
		t.Errorf("dep name = %q", fl.DepartureAirport.Name)
	}
	if fl.ArrivalAirport.Code != "NRT" {
		t.Errorf("arr code = %q", fl.ArrivalAirport.Code)
	}
	if fl.ArrivalAirport.Name != "Narita International Airport" {
		t.Errorf("arr name = %q", fl.ArrivalAirport.Name)
	}
	if fl.Duration != 780 {
		t.Errorf("duration = %d, want 780", fl.Duration)
	}
	if fl.Airline != "Finnair" {
		t.Errorf("airline = %q", fl.Airline)
	}
	if fl.AirlineCode != "AY" {
		t.Errorf("airline code = %q", fl.AirlineCode)
	}
	if fl.FlightNumber != "AY 79" {
		t.Errorf("flight number = %q", fl.FlightNumber)
	}
	if fl.DepartureTime != "2026-06-15T10:30" {
		t.Errorf("dep time = %q", fl.DepartureTime)
	}
	if fl.ArrivalTime != "2026-06-16T07:15" {
		t.Errorf("arr time = %q", fl.ArrivalTime)
	}
}

func TestParseOneLeg_ShortArray(t *testing.T) {
	leg := []any{"only", "two"}
	fl := parseOneLeg(leg)
	if fl.DepartureAirport.Code != "" {
		t.Errorf("expected empty dep code for short array, got %q", fl.DepartureAirport.Code)
	}
}

func TestParseOneLeg_NoAirlineInfo(t *testing.T) {
	leg := make([]any, 23)
	leg[3] = "JFK"
	leg[6] = "LAX"
	leg[22] = nil // no airline info

	fl := parseOneLeg(leg)
	if fl.Airline != "" {
		t.Errorf("expected empty airline, got %q", fl.Airline)
	}
}

// --- parsePrice ---

func TestParsePrice_Nil(t *testing.T) {
	price, currency := parsePrice(nil)
	if price != 0 || currency != "" {
		t.Errorf("expected (0, \"\"), got (%v, %q)", price, currency)
	}
}

func TestParsePrice_NotArray(t *testing.T) {
	price, currency := parsePrice("not an array")
	if price != 0 || currency != "" {
		t.Errorf("expected (0, \"\"), got (%v, %q)", price, currency)
	}
}

func TestParsePrice_DirectNumeric(t *testing.T) {
	// Price as direct numeric element in the array.
	raw := []any{nil, float64(250)}
	price, _ := parsePrice(raw)
	if price != 250 {
		t.Errorf("price = %v, want 250", price)
	}
}

func TestParsePrice_WithCurrencyCode(t *testing.T) {
	// Array with numeric and 3-letter uppercase code.
	raw := []any{float64(350), "EUR"}
	price, currency := parsePrice(raw)
	if price != 350 {
		t.Errorf("price = %v, want 350", price)
	}
	if currency != "EUR" {
		t.Errorf("currency = %q, want EUR", currency)
	}
}

func TestParsePrice_SubArrayPrice(t *testing.T) {
	// Price in sub-array at [0]: [null, 523] -> 523
	raw := []any{[]any{nil, float64(523)}}
	price, _ := parsePrice(raw)
	if price != 523 {
		t.Errorf("price = %v, want 523", price)
	}
}

// --- formatDateTime ---

func TestFormatDateTime(t *testing.T) {
	tests := []struct {
		name    string
		dateRaw any
		timeRaw any
		want    string
	}{
		{
			"full datetime",
			[]any{float64(2026), float64(6), float64(15)},
			[]any{float64(14), float64(30)},
			"2026-06-15T14:30",
		},
		{
			"midnight",
			[]any{float64(2026), float64(1), float64(1)},
			[]any{float64(0), float64(0)},
			"2026-01-01T00:00",
		},
		{
			"date only - no time",
			[]any{float64(2026), float64(12), float64(25)},
			nil,
			"2026-12-25",
		},
		{
			"date only - time not array",
			[]any{float64(2026), float64(6), float64(15)},
			"10:30",
			"2026-06-15",
		},
		{
			"hour only - minute omitted (treated as :00)",
			[]any{float64(2026), float64(6), float64(15)},
			[]any{float64(10)},
			"2026-06-15T10:00",
		},
		{
			"nil date",
			nil,
			[]any{float64(10), float64(30)},
			"",
		},
		{
			"date too short",
			[]any{float64(2026), float64(6)},
			[]any{float64(10), float64(30)},
			"",
		},
		{
			"zero year",
			[]any{float64(0), float64(6), float64(15)},
			[]any{float64(10), float64(30)},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDateTime(tt.dateRaw, tt.timeRaw)
			if got != tt.want {
				t.Errorf("formatDateTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- toString edge cases ---

func TestToString_NonStringNonFloat(t *testing.T) {
	// Should fall through to fmt.Sprintf.
	got := toString(true)
	if got != "true" {
		t.Errorf("toString(true) = %q, want %q", got, "true")
	}

	got = toString([]int{1, 2, 3})
	if got == "" {
		t.Error("expected non-empty result for slice")
	}
}

// --- toFloat ---

func TestToFloat(t *testing.T) {
	f, ok := jsonutil.ToFloat(float64(42.5))
	if !ok || f != 42.5 {
		t.Errorf("toFloat(42.5) = (%v, %v)", f, ok)
	}

	if _, ok := jsonutil.ToFloat(nil); ok {
		t.Error("expected ok=false for nil")
	}

	if _, ok := jsonutil.ToFloat("not a number"); ok {
		t.Error("expected ok=false for string")
	}
}

// --- extractCurrencyFromToken ---

func TestExtractCurrencyFromToken_Empty(t *testing.T) {
	got := extractCurrencyFromToken("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestExtractCurrencyFromToken_InvalidBase64(t *testing.T) {
	got := extractCurrencyFromToken("not!valid!base64!!!!!")
	if got != "" {
		t.Errorf("expected empty for invalid base64, got %q", got)
	}
}

// --- isKnownCurrency ---

func TestIsKnownCurrency(t *testing.T) {
	known := []string{"USD", "EUR", "GBP", "JPY", "PLN", "SEK", "NOK"}
	for _, c := range known {
		if !isKnownCurrency(c) {
			t.Errorf("isKnownCurrency(%q) = false, want true", c)
		}
	}

	unknown := []string{"XYZ", "AAA", "ZZZ"}
	for _, c := range unknown {
		if isKnownCurrency(c) {
			t.Errorf("isKnownCurrency(%q) = true, want false", c)
		}
	}
}

// --- buildFilters ---

func TestBuildFilters_AllSortOrders(t *testing.T) {
	sortOrders := []struct {
		sort     models.SortBy
		expected int // Google's sort code
	}{
		{models.SortCheapest, 2},
		{models.SortDuration, 3},
		{models.SortDepartureTime, 4},
		{models.SortArrivalTime, 5},
	}

	for _, tt := range sortOrders {
		opts := SearchOptions{Adults: 1, SortBy: tt.sort}
		filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

		data, _ := json.Marshal(filters)
		var arr []any
		_ = json.Unmarshal(data, &arr)

		sortBy := int(arr[2].(float64))
		if sortBy != tt.expected {
			t.Errorf("SortBy %v -> google sort %d, want %d", tt.sort, sortBy, tt.expected)
		}
	}
}

func TestBuildFilters_WithAirlines(t *testing.T) {
	opts := SearchOptions{
		Adults:   1,
		Airlines: []string{"AY", "LH", "JL"},
	}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	_ = json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	segments := settings[13].([]any)
	seg := segments[0].([]any)

	// Airlines at seg[4].
	airlines := seg[4].([]any)
	if len(airlines) != 3 {
		t.Fatalf("expected 3 airlines, got %d", len(airlines))
	}
	if airlines[0] != "AY" || airlines[1] != "LH" || airlines[2] != "JL" {
		t.Errorf("airlines = %v", airlines)
	}
}

func TestBuildFilters_WithMaxStops(t *testing.T) {
	tests := []struct {
		stops    models.MaxStops
		expected int
	}{
		{models.AnyStops, 0},
		{models.NonStop, 1},
		{models.OneStop, 2},
		{models.TwoPlusStops, 3},
	}

	for _, tt := range tests {
		opts := SearchOptions{Adults: 1, MaxStops: tt.stops}
		filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

		data, _ := json.Marshal(filters)
		var arr []any
		_ = json.Unmarshal(data, &arr)

		settings := arr[1].([]any)
		segments := settings[13].([]any)
		seg := segments[0].([]any)

		stops := int(seg[3].(float64))
		if stops != tt.expected {
			t.Errorf("MaxStops %v -> %d, want %d", tt.stops, stops, tt.expected)
		}
	}
}

func TestBuildFilters_CabinClasses(t *testing.T) {
	tests := []struct {
		cabin    models.CabinClass
		expected int
	}{
		{models.Economy, 1},
		{models.PremiumEconomy, 2},
		{models.Business, 3},
		{models.First, 4},
	}

	for _, tt := range tests {
		opts := SearchOptions{Adults: 1, CabinClass: tt.cabin}
		filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

		data, _ := json.Marshal(filters)
		var arr []any
		_ = json.Unmarshal(data, &arr)

		settings := arr[1].([]any)
		cabin := int(settings[5].(float64))
		if cabin != tt.expected {
			t.Errorf("CabinClass %v -> %d, want %d", tt.cabin, cabin, tt.expected)
		}
	}
}

// --- SearchOptions defaults ---

func TestSearchOptions_Defaults(t *testing.T) {
	opts := SearchOptions{}
	opts.defaults()

	if opts.Adults != 1 {
		t.Errorf("Adults = %d, want 1", opts.Adults)
	}
	if opts.CabinClass != models.Economy {
		t.Errorf("CabinClass = %v, want Economy", opts.CabinClass)
	}
}

func TestSearchOptions_DefaultsPreserveSet(t *testing.T) {
	opts := SearchOptions{Adults: 3, CabinClass: models.Business}
	opts.defaults()

	if opts.Adults != 3 {
		t.Errorf("Adults = %d, want 3", opts.Adults)
	}
	if opts.CabinClass != models.Business {
		t.Errorf("CabinClass = %v, want Business", opts.CabinClass)
	}
}

// --- SearchFlightsWithClient validation ---

func TestSearchFlightsWithClient_MissingParams(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		destination string
		date        string
	}{
		{"empty origin", "", "NRT", "2026-06-15"},
		{"empty destination", "HEL", "", "2026-06-15"},
		{"empty date", "HEL", "NRT", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SearchFlightsWithClient(t.Context(), nil, tt.origin, tt.destination, tt.date, SearchOptions{})
			if err == nil {
				t.Error("expected error for missing params")
			}
		})
	}
}

// --- DateSearchOptions defaults ---

func TestDateSearchOptions_Defaults(t *testing.T) {
	opts := DateSearchOptions{}
	opts.defaults()

	if opts.Adults != 1 {
		t.Errorf("Adults = %d, want 1", opts.Adults)
	}
	if opts.FromDate == "" {
		t.Error("FromDate should be set to tomorrow")
	}
	if opts.ToDate == "" {
		t.Error("ToDate should be set to FromDate + 30 days")
	}
}

func TestDateSearchOptions_RoundTripDuration(t *testing.T) {
	opts := DateSearchOptions{RoundTrip: true}
	opts.defaults()

	if opts.Duration != 7 {
		t.Errorf("Duration = %d, want 7 for round-trip default", opts.Duration)
	}
}

func TestDateSearchOptions_PreserveSet(t *testing.T) {
	opts := DateSearchOptions{
		FromDate: "2026-07-01",
		ToDate:   "2026-07-15",
		Adults:   2,
	}
	opts.defaults()

	if opts.FromDate != "2026-07-01" {
		t.Errorf("FromDate = %q, want 2026-07-01", opts.FromDate)
	}
	if opts.ToDate != "2026-07-15" {
		t.Errorf("ToDate = %q, want 2026-07-15", opts.ToDate)
	}
	if opts.Adults != 2 {
		t.Errorf("Adults = %d, want 2", opts.Adults)
	}
}

// --- SearchDates validation ---

func TestSearchDates_MissingParams(t *testing.T) {
	_, err := SearchDates(t.Context(), "", "NRT", DateSearchOptions{FromDate: "2026-06-01", ToDate: "2026-06-02"})
	if err == nil {
		t.Error("expected error for empty origin")
	}

	_, err = SearchDates(t.Context(), "HEL", "", DateSearchOptions{FromDate: "2026-06-01", ToDate: "2026-06-02"})
	if err == nil {
		t.Error("expected error for empty destination")
	}
}

func TestSearchDates_InvalidDates(t *testing.T) {
	_, err := SearchDates(t.Context(), "HEL", "NRT", DateSearchOptions{FromDate: "bad", ToDate: "2026-06-02"})
	if err == nil {
		t.Error("expected error for bad from_date")
	}

	_, err = SearchDates(t.Context(), "HEL", "NRT", DateSearchOptions{FromDate: "2026-06-01", ToDate: "bad"})
	if err == nil {
		t.Error("expected error for bad to_date")
	}
}

func TestSearchDates_ReversedDates(t *testing.T) {
	_, err := SearchDates(t.Context(), "HEL", "NRT", DateSearchOptions{
		FromDate: "2026-06-30",
		ToDate:   "2026-06-01",
	})
	if err == nil {
		t.Error("expected error for to_date before from_date")
	}
}

// --- buildFlightBookingURL ---

func TestBuildFlightBookingURL_Basic(t *testing.T) {
	url := buildFlightBookingURL("HEL", "NRT", "2026-06-15", "", "")
	if url == "" {
		t.Fatal("expected non-empty URL")
	}
	if !strings.Contains(url, "google.com/travel/flights") {
		t.Errorf("URL missing google.com/travel/flights: %s", url)
	}
	if !strings.Contains(url, "Tokyo") {
		t.Errorf("URL missing destination city Tokyo (NRT): %s", url)
	}
	if !strings.Contains(url, "Helsinki") {
		t.Errorf("URL missing origin city Helsinki (HEL): %s", url)
	}
	if !strings.Contains(url, "2026-06-15") {
		t.Errorf("URL missing date: %s", url)
	}
}

func TestBuildFlightBookingURL_Format(t *testing.T) {
	url := buildFlightBookingURL("JFK", "LAX", "2027-01-01", "", "")
	expected := "https://www.google.com/travel/flights?q=Flights+to+Los+Angeles+from+New+York+on+2027-01-01"
	if url != expected {
		t.Errorf("URL = %q, want %q", url, expected)
	}
}

func TestBuildFlightBookingURL_DifferentRoutes(t *testing.T) {
	tests := []struct {
		origin, dest, date   string
		originCity, destCity string
	}{
		{"CDG", "SIN", "2026-12-25", "Paris", "Singapore"},
		{"LHR", "DXB", "2026-03-01", "London", "Dubai"},
		{"SFO", "BCN", "2027-07-15", "San+Francisco", "Barcelona"},
	}
	for _, tt := range tests {
		url := buildFlightBookingURL(tt.origin, tt.dest, tt.date, "", "")
		if !strings.Contains(url, tt.destCity) {
			t.Errorf("URL for %s->%s missing destination city %s: %s", tt.origin, tt.dest, tt.destCity, url)
		}
		if !strings.Contains(url, tt.originCity) {
			t.Errorf("URL for %s->%s missing origin city %s: %s", tt.origin, tt.dest, tt.originCity, url)
		}
		if !strings.Contains(url, tt.date) {
			t.Errorf("URL for %s->%s missing date: %s", tt.origin, tt.dest, url)
		}
	}
}

// --- buildFilters passengers ---

func TestBuildFlightBookingURL_RoundTrip(t *testing.T) {
	url := buildFlightBookingURL("HEL", "NRT", "2026-06-15", "2026-06-30", "")
	if !strings.Contains(url, "through+2026-06-30") {
		t.Errorf("round-trip URL missing return date: %s", url)
	}
	if !strings.Contains(url, "on+2026-06-15") {
		t.Errorf("round-trip URL missing departure date: %s", url)
	}

	oneWay := buildFlightBookingURL("HEL", "NRT", "2026-06-15", "", "")
	if strings.Contains(oneWay, "through") {
		t.Errorf("one-way URL should not contain return date: %s", oneWay)
	}
}

func TestBuildFlightBookingURL_Currency(t *testing.T) {
	url := buildFlightBookingURL("MDE", "MAD", "2026-05-01", "2026-05-20", "USD")
	if !strings.Contains(url, "&curr=USD") {
		t.Errorf("URL missing currency param: %s", url)
	}
	if !strings.Contains(url, "through+2026-05-20") {
		t.Errorf("URL missing return date: %s", url)
	}

	noCurrency := buildFlightBookingURL("HEL", "NRT", "2026-06-15", "", "")
	if strings.Contains(noCurrency, "curr=") {
		t.Errorf("URL should not contain currency: %s", noCurrency)
	}
}

// --- buildFilters passengers ---

func TestBuildFilters_MultipleAdults(t *testing.T) {
	opts := SearchOptions{Adults: 4}
	filters := buildFilters("HEL", "NRT", "2026-06-15", opts)

	data, _ := json.Marshal(filters)
	var arr []any
	_ = json.Unmarshal(data, &arr)

	settings := arr[1].([]any)
	passengers := settings[6].([]any)
	adults := int(passengers[0].(float64))
	if adults != 4 {
		t.Errorf("adults = %d, want 4", adults)
	}
}

// --- CurrencyToGL ---

func TestCurrencyToGL_KnownCurrencies(t *testing.T) {
	tests := []struct {
		currency string
		wantGL   string
	}{
		{"EUR", "FI"},
		{"USD", "US"},
		{"GBP", "GB"},
		{"CHF", "CH"},
		{"SEK", "SE"},
		{"NOK", "NO"},
		{"DKK", "DK"},
		{"PLN", "PL"},
		{"JPY", "JP"},
		{"RUB", "RU"},
		{"AUD", "AU"},
		{"CAD", "CA"},
	}

	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			got := CurrencyToGL(tt.currency)
			if got != tt.wantGL {
				t.Errorf("CurrencyToGL(%q) = %q, want %q", tt.currency, got, tt.wantGL)
			}
		})
	}
}

func TestCurrencyToGL_CaseInsensitive(t *testing.T) {
	if got := CurrencyToGL("eur"); got != "FI" {
		t.Errorf("CurrencyToGL(\"eur\") = %q, want \"FI\"", got)
	}
	if got := CurrencyToGL("Gbp"); got != "GB" {
		t.Errorf("CurrencyToGL(\"Gbp\") = %q, want \"GB\"", got)
	}
}

func TestCurrencyToGL_Empty(t *testing.T) {
	if got := CurrencyToGL(""); got != "" {
		t.Errorf("CurrencyToGL(\"\") = %q, want \"\"", got)
	}
}

func TestCurrencyToGL_Unknown(t *testing.T) {
	if got := CurrencyToGL("XYZ"); got != "" {
		t.Errorf("CurrencyToGL(\"XYZ\") = %q, want \"\"", got)
	}
}

// --- SearchOptions.Currency ---

func TestSearchOptions_CurrencyField(t *testing.T) {
	// Verify Currency field is preserved through defaults.
	opts := SearchOptions{Currency: "EUR"}
	opts.defaults()
	if opts.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR after defaults()", opts.Currency)
	}
}

func TestSearchOptions_CurrencyEmpty(t *testing.T) {
	// Empty currency should remain empty (no forced default).
	opts := SearchOptions{}
	opts.defaults()
	if opts.Currency != "" {
		t.Errorf("Currency = %q, want \"\" (empty) after defaults()", opts.Currency)
	}
}
