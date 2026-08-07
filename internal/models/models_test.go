package models

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- CabinClass ---

func TestCabinClass_String(t *testing.T) {
	tests := []struct {
		cc   CabinClass
		want string
	}{
		{Economy, "economy"},
		{PremiumEconomy, "premium_economy"},
		{Business, "business"},
		{First, "first"},
		{CabinClass(99), "economy"}, // unknown defaults to economy
		{CabinClass(-1), "economy"},
	}
	for _, tt := range tests {
		got := tt.cc.String()
		if got != tt.want {
			t.Errorf("CabinClass(%d).String() = %q, want %q", tt.cc, got, tt.want)
		}
	}
}

func TestParseCabinClass(t *testing.T) {
	tests := []struct {
		input   string
		want    CabinClass
		wantErr bool
	}{
		{"economy", Economy, false},
		{"ECONOMY", Economy, false},
		{"e", Economy, false},
		{"1", Economy, false},
		{"premium_economy", PremiumEconomy, false},
		{"premium-economy", PremiumEconomy, false},
		{"premiumeconomy", PremiumEconomy, false},
		{"pe", PremiumEconomy, false},
		{"PE", PremiumEconomy, false},
		{"2", PremiumEconomy, false},
		{"business", Business, false},
		{"BUSINESS", Business, false},
		{"b", Business, false},
		{"3", Business, false},
		{"first", First, false},
		{"FIRST", First, false},
		{"f", First, false},
		{"4", First, false},
		{"  economy  ", Economy, false}, // whitespace trimmed
		{"invalid", Economy, true},
		{"", Economy, true},
		{"5", Economy, true},
	}
	for _, tt := range tests {
		got, err := ParseCabinClass(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseCabinClass(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCabinClass(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseCabinClass(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- MaxStops ---

func TestMaxStops_String(t *testing.T) {
	tests := []struct {
		ms   MaxStops
		want string
	}{
		{AnyStops, "any"},
		{NonStop, "nonstop"},
		{OneStop, "one_stop"},
		{TwoPlusStops, "two_plus"},
		{MaxStops(99), "any"}, // unknown defaults to any
	}
	for _, tt := range tests {
		got := tt.ms.String()
		if got != tt.want {
			t.Errorf("MaxStops(%d).String() = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestParseMaxStops(t *testing.T) {
	tests := []struct {
		input   string
		want    MaxStops
		wantErr bool
	}{
		{"any", AnyStops, false},
		{"ANY", AnyStops, false},
		{"0", AnyStops, false},
		{"", AnyStops, false},
		{"nonstop", NonStop, false},
		{"non_stop", NonStop, false},
		{"non-stop", NonStop, false},
		{"NONSTOP", NonStop, false},
		{"1", NonStop, false},
		{"one_stop", OneStop, false},
		{"one-stop", OneStop, false},
		{"onestop", OneStop, false},
		{"2", OneStop, false},
		{"two_plus", TwoPlusStops, false},
		{"two-plus", TwoPlusStops, false},
		{"twoplus", TwoPlusStops, false},
		{"3", TwoPlusStops, false},
		{"  nonstop  ", NonStop, false},
		{"invalid", AnyStops, true},
		{"4", AnyStops, true},
	}
	for _, tt := range tests {
		got, err := ParseMaxStops(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMaxStops(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMaxStops(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseMaxStops(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- SortBy ---

func TestSortBy_String(t *testing.T) {
	tests := []struct {
		sb   SortBy
		want string
	}{
		{SortCheapest, "cheapest"},
		{SortDuration, "duration"},
		{SortDepartureTime, "departure"},
		{SortArrivalTime, "arrival"},
		{SortBy(99), "cheapest"}, // unknown defaults to cheapest
	}
	for _, tt := range tests {
		got := tt.sb.String()
		if got != tt.want {
			t.Errorf("SortBy(%d).String() = %q, want %q", tt.sb, got, tt.want)
		}
	}
}

func TestParseSortBy(t *testing.T) {
	tests := []struct {
		input   string
		want    SortBy
		wantErr bool
	}{
		{"cheapest", SortCheapest, false},
		{"price", SortCheapest, false},
		{"0", SortCheapest, false},
		{"", SortCheapest, false},
		{"duration", SortDuration, false},
		{"time", SortDuration, false},
		{"1", SortDuration, false},
		{"departure", SortDepartureTime, false},
		{"departure_time", SortDepartureTime, false},
		{"depart", SortDepartureTime, false},
		{"2", SortDepartureTime, false},
		{"arrival", SortArrivalTime, false},
		{"arrival_time", SortArrivalTime, false},
		{"arrive", SortArrivalTime, false},
		{"3", SortArrivalTime, false},
		{"  cheapest  ", SortCheapest, false},
		{"DURATION", SortDuration, false},
		{"invalid", SortCheapest, true},
		{"4", SortCheapest, true},
	}
	for _, tt := range tests {
		got, err := ParseSortBy(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseSortBy(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSortBy(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseSortBy(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- Price ---

func TestPrice_JSON(t *testing.T) {
	p := Price{Amount: 99.50, Currency: "EUR"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed Price
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Amount != 99.50 {
		t.Errorf("Amount = %v, want 99.50", parsed.Amount)
	}
	if parsed.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", parsed.Currency)
	}
}

func TestPrice_ZeroValue(t *testing.T) {
	var p Price
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"amount":0`) {
		t.Errorf("zero price should have amount:0, got %s", data)
	}
}

// --- DateRange ---

func TestDateRange_JSON(t *testing.T) {
	dr := DateRange{CheckIn: "2026-06-15", CheckOut: "2026-06-18"}
	data, err := json.Marshal(dr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed DateRange
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.CheckIn != "2026-06-15" {
		t.Errorf("CheckIn = %q, want 2026-06-15", parsed.CheckIn)
	}
	if parsed.CheckOut != "2026-06-18" {
		t.Errorf("CheckOut = %q, want 2026-06-18", parsed.CheckOut)
	}
}

// --- Location ---

func TestLocation_JSON(t *testing.T) {
	loc := Location{Name: "Helsinki", Latitude: 60.17, Longitude: 24.94}
	data, err := json.Marshal(loc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed Location
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Name != "Helsinki" {
		t.Errorf("Name = %q, want Helsinki", parsed.Name)
	}
	if parsed.Latitude != 60.17 {
		t.Errorf("Latitude = %v, want 60.17", parsed.Latitude)
	}
}

// --- FlightResult ---

func TestFlightResult_JSON(t *testing.T) {
	fr := FlightResult{
		Price:       523.00,
		Currency:    "EUR",
		Duration:    780,
		Stops:       0,
		Provider:    "kiwi",
		SelfConnect: true,
		Warnings:    []string{"Self-connect risk"},
		Legs: []FlightLeg{
			{
				DepartureAirport: AirportInfo{Code: "HEL", Name: "Helsinki-Vantaa"},
				ArrivalAirport:   AirportInfo{Code: "NRT", Name: "Narita"},
				DepartureTime:    "2026-06-15T10:30",
				ArrivalTime:      "2026-06-16T07:15",
				Duration:         780,
				Airline:          "Finnair",
				AirlineCode:      "AY",
				FlightNumber:     "AY 79",
			},
		},
	}

	data, err := json.Marshal(fr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed FlightResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Price != 523 {
		t.Errorf("Price = %v, want 523", parsed.Price)
	}
	if len(parsed.Legs) != 1 {
		t.Fatalf("Legs = %d, want 1", len(parsed.Legs))
	}
	if parsed.Legs[0].AirlineCode != "AY" {
		t.Errorf("AirlineCode = %q, want AY", parsed.Legs[0].AirlineCode)
	}
	if parsed.Provider != "kiwi" {
		t.Errorf("Provider = %q, want kiwi", parsed.Provider)
	}
	if !parsed.SelfConnect {
		t.Error("expected SelfConnect=true")
	}
	if len(parsed.Warnings) != 1 || parsed.Warnings[0] != "Self-connect risk" {
		t.Errorf("Warnings = %v, want [Self-connect risk]", parsed.Warnings)
	}
}

// --- FlightSearchResult ---

func TestFlightSearchResult_JSON(t *testing.T) {
	fsr := FlightSearchResult{
		Success:  true,
		Count:    1,
		TripType: "one_way",
		Flights:  []FlightResult{{Price: 100, Currency: "USD"}},
	}

	data, err := json.Marshal(fsr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed FlightSearchResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !parsed.Success {
		t.Error("expected Success=true")
	}
	if parsed.Error != "" {
		t.Errorf("Error = %q, want empty", parsed.Error)
	}
}

func TestFlightSearchResult_WithError(t *testing.T) {
	fsr := FlightSearchResult{
		Success: false,
		Error:   "something went wrong",
	}

	data, err := json.Marshal(fsr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed FlightSearchResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Success {
		t.Error("expected Success=false")
	}
	if parsed.Error != "something went wrong" {
		t.Errorf("Error = %q", parsed.Error)
	}
}

// --- DatePriceResult / DateSearchResult ---

func TestDateSearchResult_JSON(t *testing.T) {
	dsr := DateSearchResult{
		Success:   true,
		Count:     2,
		TripType:  "round_trip",
		DateRange: "2026-06-01 to 2026-06-30",
		Dates: []DatePriceResult{
			{Date: "2026-06-15", Price: 450, Currency: "EUR", ReturnDate: "2026-06-22"},
			{Date: "2026-06-16", Price: 430, Currency: "EUR", ReturnDate: "2026-06-23"},
		},
	}

	data, err := json.Marshal(dsr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed DateSearchResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Count != 2 {
		t.Errorf("Count = %d, want 2", parsed.Count)
	}
}

func TestDatePriceResult_OmitsReturnDate(t *testing.T) {
	dp := DatePriceResult{Date: "2026-06-15", Price: 450, Currency: "EUR"}
	data, err := json.Marshal(dp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "return_date") {
		t.Error("return_date should be omitted when empty")
	}
}

// --- HotelResult / HotelSearchResult ---

func TestHotelResult_JSON(t *testing.T) {
	hr := HotelResult{
		Name:        "Grand Hotel",
		HotelID:     "/g/11abc",
		Rating:      4.5,
		ReviewCount: 1200,
		Stars:       5,
		Price:       250.00,
		Currency:    "EUR",
		Address:     "Main Street 1",
		Lat:         60.17,
		Lon:         24.94,
		Amenities:   []string{"wifi", "pool", "spa"},
	}

	data, err := json.Marshal(hr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed HotelResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Name != "Grand Hotel" {
		t.Errorf("Name = %q", parsed.Name)
	}
	if len(parsed.Amenities) != 3 {
		t.Errorf("Amenities = %d, want 3", len(parsed.Amenities))
	}
}

func TestHotelResult_OmitsAmenities(t *testing.T) {
	hr := HotelResult{Name: "Budget Inn"}
	data, err := json.Marshal(hr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "amenities") {
		t.Error("amenities should be omitted when nil")
	}
}

// --- HotelPriceResult / ProviderPrice ---

func TestHotelPriceResult_JSON(t *testing.T) {
	hpr := HotelPriceResult{
		Success:  true,
		HotelID:  "/g/11abc",
		Name:     "Grand Hotel",
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Providers: []ProviderPrice{
			{Provider: "Booking.com", Price: 250, Currency: "EUR"},
			{Provider: "Expedia", Price: 260, Currency: "EUR"},
		},
	}

	data, err := json.Marshal(hpr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed HotelPriceResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Providers) != 2 {
		t.Errorf("Providers = %d, want 2", len(parsed.Providers))
	}
}

func TestProviderPriceOfficialJSONIsPositiveOnly(t *testing.T) {
	withoutEvidence, err := json.Marshal(ProviderPrice{Provider: "OTA", Price: 100, Currency: "EUR"})
	if err != nil {
		t.Fatalf("marshal unknown relationship: %v", err)
	}
	if strings.Contains(string(withoutEvidence), "official") {
		t.Fatalf("missing evidence was serialized as an official-site claim: %s", withoutEvidence)
	}

	withEvidence, err := json.Marshal(ProviderPrice{
		Provider: "Property site", Price: 120, Currency: "EUR", Official: true,
	})
	if err != nil {
		t.Fatalf("marshal official relationship: %v", err)
	}
	if !strings.Contains(string(withEvidence), `"official":true`) {
		t.Fatalf("upstream official-site fact was omitted: %s", withEvidence)
	}
}

// --- FormatJSON additional tests ---

func TestFormatJSON_AllModelTypes(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{"FlightResult", FlightResult{Price: 100, Currency: "USD", Legs: []FlightLeg{}}},
		{"HotelResult", HotelResult{Name: "Hotel", Price: 200}},
		{"HotelSearchResult", HotelSearchResult{Success: true, Hotels: []HotelResult{}}},
		{"DateSearchResult", DateSearchResult{Success: true, Dates: []DatePriceResult{}}},
		{"ProviderPrice", ProviderPrice{Provider: "Test", Price: 100, Currency: "USD"}},
		{"Price", Price{Amount: 99.50, Currency: "EUR"}},
		{"Location", Location{Name: "Test", Latitude: 1.0, Longitude: 2.0}},
		{"nil", nil},
		{"empty slice", []any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := FormatJSON(&buf, tt.value)
			if err != nil {
				t.Fatalf("FormatJSON error: %v", err)
			}
			if buf.Len() == 0 {
				t.Error("expected non-empty output")
			}
		})
	}
}

// --- FormatTable additional tests ---

func TestFormatTable_SingleColumn(t *testing.T) {
	var buf bytes.Buffer
	FormatTable(&buf, []string{"Items"}, [][]string{
		{"Apple"},
		{"Banana"},
		{"Cherry"},
	})

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 5 { // header + separator + 3 rows
		t.Errorf("expected 5 lines, got %d", len(lines))
	}
}

func TestFormatTable_EmptyHeaders(t *testing.T) {
	var buf bytes.Buffer
	FormatTable(&buf, []string{}, nil)
	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty headers, got %q", buf.String())
	}
}

func TestFormatTable_WideContent(t *testing.T) {
	var buf bytes.Buffer
	headers := []string{"A"}
	rows := [][]string{
		{"This is a very long cell value that should expand the column"},
	}

	FormatTable(&buf, headers, rows)
	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// All lines should have the same length.
	if len(lines) >= 2 {
		for i, line := range lines {
			if len(line) != len(lines[0]) {
				t.Errorf("line %d length %d != line 0 length %d", i, len(line), len(lines[0]))
			}
		}
	}
}

func TestFormatTable_MoreCellsThanHeaders(t *testing.T) {
	var buf bytes.Buffer
	headers := []string{"A"}
	rows := [][]string{
		{"1", "extra1", "extra2"}, // more cells than headers
	}

	// Should not panic.
	FormatTable(&buf, headers, rows)
	if buf.Len() == 0 {
		t.Error("expected output even with extra cells")
	}
}

func TestFormatJSON_SpecialCharacters(t *testing.T) {
	v := map[string]string{
		"html":      "<script>alert('xss')</script>",
		"ampersand": "a&b",
		"unicode":   "Helsinki - \u00e4\u00f6",
	}

	var buf bytes.Buffer
	if err := FormatJSON(&buf, v); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}

	// Should NOT escape HTML entities.
	output := buf.String()
	if strings.Contains(output, `\u003c`) {
		t.Error("< should not be escaped")
	}
	if strings.Contains(output, `\u0026`) {
		t.Error("& should not be escaped")
	}
}

// --- HotelSearchResult ---

func TestHotelSearchResult_JSON(t *testing.T) {
	hsr := HotelSearchResult{
		Success: true,
		Count:   2,
		Hotels: []HotelResult{
			{Name: "Hotel A", Price: 100},
			{Name: "Hotel B", Price: 200},
		},
	}

	data, err := json.Marshal(hsr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed HotelSearchResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Count != 2 {
		t.Errorf("Count = %d, want 2", parsed.Count)
	}
	if !parsed.Success {
		t.Error("expected Success=true")
	}
}

func TestHotelSearchResult_WithError(t *testing.T) {
	hsr := HotelSearchResult{Error: "failed"}
	data, err := json.Marshal(hsr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "failed") {
		t.Error("expected error in JSON")
	}
}
