package hotels

import (
	"context"
	"testing"
)

func TestParseBookingJSONLD(t *testing.T) {
	page := `<html><head>
<script type="application/ld+json">
{
  "@type": "Hotel",
  "name": "Beverly Hills Heights",
  "makesOffer": [
    {
      "@type": "Offer",
      "name": "Deluxe Double Room with Sea View",
      "description": "This spacious 35 m² room has a balcony, sea view, and minibar. Sleeps 2 adults.",
      "priceSpecification": {"price": "189", "priceCurrency": "EUR"}
    },
    {
      "@type": "Offer",
      "name": "Two-Bedroom Apartment with Balcony",
      "description": "This 65 m² apartment features a balcony, kitchen, and washing machine. Accommodates 4 guests.",
      "priceSpecification": {"price": "329", "priceCurrency": "EUR"}
    },
    {
      "@type": "Offer",
      "name": "Standard Twin Room",
      "description": "Compact room with 2 twin beds and air conditioning.",
      "priceSpecification": {"price": "119", "priceCurrency": "EUR"}
    }
  ]
}
</script>
</head></html>`

	offers, err := parseBookingJSONLD(page)
	if err != nil {
		t.Fatalf("parseBookingJSONLD() error: %v", err)
	}

	if len(offers) != 3 {
		t.Fatalf("expected 3 offers, got %d", len(offers))
	}

	// Check first room (Deluxe Double with Sea View).
	r := offers[0]
	if r.Name != "Deluxe Double Room with Sea View" {
		t.Errorf("offer[0].Name = %q, want %q", r.Name, "Deluxe Double Room with Sea View")
	}
	if r.Price != 189 {
		t.Errorf("offer[0].Price = %v, want 189", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("offer[0].Currency = %q, want EUR", r.Currency)
	}
	if r.SizeM2 != 35 {
		t.Errorf("offer[0].SizeM2 = %v, want 35", r.SizeM2)
	}
	if r.MaxGuests != 2 {
		t.Errorf("offer[0].MaxGuests = %v, want 2", r.MaxGuests)
	}
	if r.BedType != "1 double bed" {
		t.Errorf("offer[0].BedType = %q, want %q", r.BedType, "1 double bed")
	}
	// Check amenities from description.
	wantAmenities := map[string]bool{"Balcony": true, "Sea View": true, "Minibar": true}
	for _, a := range r.Amenities {
		delete(wantAmenities, a)
	}
	if len(wantAmenities) > 0 {
		t.Errorf("offer[0] missing amenities: %v (got %v)", wantAmenities, r.Amenities)
	}

	// Check second room (Apartment).
	r2 := offers[1]
	if r2.Price != 329 {
		t.Errorf("offer[1].Price = %v, want 329", r2.Price)
	}
	if r2.SizeM2 != 65 {
		t.Errorf("offer[1].SizeM2 = %v, want 65", r2.SizeM2)
	}
	if r2.MaxGuests != 4 {
		t.Errorf("offer[1].MaxGuests = %v, want 4", r2.MaxGuests)
	}

	// Third room: twin beds.
	r3 := offers[2]
	if r3.BedType != "2 twin beds" {
		t.Errorf("offer[2].BedType = %q, want %q", r3.BedType, "2 twin beds")
	}
}

func TestParseBookingJSONLD_RoomDecisionMetadata(t *testing.T) {
	page := `<html><head>
<script type="application/ld+json">
{
  "@type": "Hotel",
  "name": "Metadata Hotel",
  "makesOffer": [
    {
      "@type": "Offer",
      "name": "Flexible Queen Room",
      "description": "28 m² queen room sleeps 2 adults. Free cancellation until June 20. Breakfast included. Taxes and fees included.",
      "priceSpecification": {
        "price": "140",
        "nightlyPrice": "140",
        "totalPrice": "420",
        "taxesAndFees": "35",
        "priceCurrency": "EUR",
        "taxesFeesIncluded": true
      }
    },
    {
      "@type": "Offer",
      "name": "Advance Purchase Room Only",
      "description": "Non-refundable room only rate. Taxes and fees not included.",
      "priceSpecification": {
        "price": "300",
        "nightlyPrice": "150",
        "totalPrice": "300",
        "priceCurrency": "EUR",
        "taxesFeesIncluded": false
      }
    }
  ]
}
</script>
</head></html>`

	offers, err := parseBookingJSONLD(page)
	if err != nil {
		t.Fatalf("parseBookingJSONLD() error: %v", err)
	}
	if len(offers) != 2 {
		t.Fatalf("expected 2 offers, got %d", len(offers))
	}

	flex := offers[0]
	if flex.NightlyPrice != 140 {
		t.Errorf("NightlyPrice = %v, want 140", flex.NightlyPrice)
	}
	if flex.TotalPrice != 420 {
		t.Errorf("TotalPrice = %v, want 420", flex.TotalPrice)
	}
	if flex.TaxesAndFees != 35 {
		t.Errorf("TaxesAndFees = %v, want 35", flex.TaxesAndFees)
	}
	assertBoolPtr(t, "TaxesFeesIncluded", flex.TaxesFeesIncluded, true)
	if flex.CancellationPolicy != "free_cancellation" {
		t.Errorf("CancellationPolicy = %q, want free_cancellation", flex.CancellationPolicy)
	}
	assertBoolPtr(t, "Refundable", flex.Refundable, true)
	assertBoolPtr(t, "FreeCancellation", flex.FreeCancellation, true)
	if flex.Board != "breakfast_included" {
		t.Errorf("Board = %q, want breakfast_included", flex.Board)
	}
	assertBoolPtr(t, "BreakfastIncluded", flex.BreakfastIncluded, true)

	advance := offers[1]
	assertBoolPtr(t, "advance.Refundable", advance.Refundable, false)
	assertBoolPtr(t, "advance.FreeCancellation", advance.FreeCancellation, false)
	assertBoolPtr(t, "advance.TaxesFeesIncluded", advance.TaxesFeesIncluded, false)
	if advance.CancellationPolicy != "non_refundable" {
		t.Errorf("advance.CancellationPolicy = %q, want non_refundable", advance.CancellationPolicy)
	}
	if advance.Board != "room_only" {
		t.Errorf("advance.Board = %q, want room_only", advance.Board)
	}
	assertBoolPtr(t, "advance.BreakfastIncluded", advance.BreakfastIncluded, false)
}

func TestParseBookingJSONLD_GraphArray(t *testing.T) {
	page := `<html>
<script type="application/ld+json">
{
  "@graph": [
    {
      "@type": "Hotel",
      "name": "Test Hotel",
      "makesOffer": [
        {
          "@type": "Offer",
          "name": "Superior Suite",
          "description": "Luxurious suite with jacuzzi and city view.",
          "priceSpecification": {"price": 450, "priceCurrency": "USD"}
        }
      ]
    }
  ]
}
</script>
</html>`

	offers, err := parseBookingJSONLD(page)
	if err != nil {
		t.Fatalf("parseBookingJSONLD() error: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(offers))
	}
	if offers[0].Name != "Superior Suite" {
		t.Errorf("Name = %q, want Superior Suite", offers[0].Name)
	}
	if offers[0].Price != 450 {
		t.Errorf("Price = %v, want 450", offers[0].Price)
	}
}

func TestParseBookingJSONLD_NoOffers(t *testing.T) {
	page := `<html><script type="application/ld+json">{"@type":"Organization","name":"Booking.com"}</script></html>`
	_, err := parseBookingJSONLD(page)
	if err == nil {
		t.Error("expected error for page with no room offers, got nil")
	}
}

func TestParseBookingJSONLD_NoJSONLD(t *testing.T) {
	page := `<html><body>No structured data here</body></html>`
	_, err := parseBookingJSONLD(page)
	if err == nil {
		t.Error("expected error for page without JSON-LD, got nil")
	}
}

func TestParseBookingJSONLD_Deduplication(t *testing.T) {
	// Same room name appears in two JSON-LD blocks (can happen).
	page := `<html>
<script type="application/ld+json">
{"@type":"Hotel","name":"Test","makesOffer":[{"name":"Standard Room","priceSpecification":{"price":"100","priceCurrency":"EUR"}}]}
</script>
<script type="application/ld+json">
{"@type":"Hotel","name":"Test","makesOffer":[{"name":"Standard Room","description":"Nice room with balcony.","priceSpecification":{"price":"100","priceCurrency":"EUR"}}]}
</script>
</html>`

	offers, err := parseBookingJSONLD(page)
	if err != nil {
		t.Fatalf("parseBookingJSONLD() error: %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("expected 1 deduplicated offer, got %d", len(offers))
	}
	// The version with description should win.
	if offers[0].Description == "" {
		t.Error("expected description from second JSON-LD block to be preserved")
	}
}

func assertBoolPtr(t *testing.T, name string, got *bool, want bool) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func TestExtractBedType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Deluxe Double Room with Sea View", "1 double bed"},
		{"Standard Twin Room", "2 twin beds"},
		{"King Suite", "1 king bed"},
		{"Queen Room", "1 queen bed"},
		{"Single Room Economy", "1 single bed"},
		{"Junior Suite", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractBedType(tt.input)
		if got != tt.want {
			t.Errorf("extractBedType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractSizeM2(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"This spacious 35 m² room", 35},
		{"Room size: 28m2", 28},
		{"40 sqm apartment", 40},
		{"No size info here", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := extractSizeM2(tt.input)
		if got != tt.want {
			t.Errorf("extractSizeM2(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtractMaxGuests(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"Sleeps 2 adults", 2},
		{"Accommodates 4 guests", 4},
		{"Max 6 people", 6},
		{"For 3 persons", 3},
		{"Up to 5 guests", 5},
		{"No guest info", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := extractMaxGuests(tt.input)
		if got != tt.want {
			t.Errorf("extractMaxGuests(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtractRoomAmenities(t *testing.T) {
	text := "This room has a balcony, sea view, and minibar. Free wifi available."
	amenities := extractRoomAmenities(text)

	want := map[string]bool{
		"Balcony":   true,
		"Sea View":  true,
		"Minibar":   true,
		"Free Wifi": true,
	}

	got := make(map[string]bool)
	for _, a := range amenities {
		got[a] = true
	}

	for k := range want {
		if !got[k] {
			t.Errorf("missing amenity %q in result %v", k, amenities)
		}
	}
}

func TestBuildBookingDetailURL(t *testing.T) {
	tests := []struct {
		baseURL  string
		checkIn  string
		checkOut string
		currency string
		want     string
	}{
		{
			"https://www.booking.com/hotel/es/beverly-hills-heights.html",
			"2026-06-15", "2026-06-18", "EUR",
			"https://www.booking.com/hotel/es/beverly-hills-heights.html?lang=en-us&checkin=2026-06-15&checkout=2026-06-18&selected_currency=EUR",
		},
		{
			"https://www.booking.com/hotel/es/test.html?existing=param",
			"2026-01-01", "2026-01-05", "USD",
			"https://www.booking.com/hotel/es/test.html?lang=en-us&checkin=2026-01-01&checkout=2026-01-05&selected_currency=USD",
		},
		{
			"https://www.booking.com/hotel/fr/paris-grand.html",
			"", "", "",
			"https://www.booking.com/hotel/fr/paris-grand.html?lang=en-us",
		},
	}
	for _, tt := range tests {
		got := buildBookingDetailURL(tt.baseURL, tt.checkIn, tt.checkOut, tt.currency)
		if got != tt.want {
			t.Errorf("buildBookingDetailURL(%q, %q, %q, %q)\n  got  %q\n  want %q",
				tt.baseURL, tt.checkIn, tt.checkOut, tt.currency, got, tt.want)
		}
	}
}

func TestMergeRoomTypes(t *testing.T) {
	google := []RoomType{
		{Name: "Standard Double Room", Price: 120, Currency: "EUR"},
		{Name: "Superior Suite", Price: 250, Currency: "EUR"},
	}
	booking := []RoomType{
		{
			Name:        "Standard Double Room",
			Price:       125,
			Currency:    "EUR",
			Provider:    "Booking.com",
			Description: "Cozy room with city view.",
			BedType:     "1 double bed",
			Amenities:   []string{"City View", "Air Conditioning"},
			TotalPrice:  250,
			Board:       "breakfast_included",
			Refundable:  boolValue(true),
		},
		{
			Name:        "Two-Bedroom Apartment",
			Price:       350,
			Currency:    "EUR",
			Provider:    "Booking.com",
			Description: "Spacious apartment with balcony.",
			Amenities:   []string{"Balcony", "Kitchen"},
		},
	}

	merged := mergeRoomTypes(google, booking)

	if len(merged) != 3 {
		t.Fatalf("expected 3 merged rooms, got %d", len(merged))
	}

	// First room should be enriched Google room (keeps Google price).
	if merged[0].Name != "Standard Double Room" {
		t.Errorf("merged[0].Name = %q, want Standard Double Room", merged[0].Name)
	}
	if merged[0].Price != 120 {
		t.Errorf("merged[0].Price = %v, want 120 (Google price)", merged[0].Price)
	}
	if merged[0].Description != "Cozy room with city view." {
		t.Errorf("merged[0].Description = %q, want enriched description", merged[0].Description)
	}
	if merged[0].BedType != "1 double bed" {
		t.Errorf("merged[0].BedType = %q, want 1 double bed", merged[0].BedType)
	}
	if merged[0].TotalPrice != 250 {
		t.Errorf("merged[0].TotalPrice = %v, want enriched total price", merged[0].TotalPrice)
	}
	if merged[0].Board != "breakfast_included" {
		t.Errorf("merged[0].Board = %q, want breakfast_included", merged[0].Board)
	}
	assertBoolPtr(t, "merged[0].Refundable", merged[0].Refundable, true)

	// Second room: unchanged Google room.
	if merged[1].Name != "Superior Suite" {
		t.Errorf("merged[1].Name = %q, want Superior Suite", merged[1].Name)
	}

	// Third room: appended Booking-only room.
	if merged[2].Name != "Two-Bedroom Apartment" {
		t.Errorf("merged[2].Name = %q, want Two-Bedroom Apartment", merged[2].Name)
	}
	if merged[2].Provider != "Booking.com" {
		t.Errorf("merged[2].Provider = %q, want Booking.com", merged[2].Provider)
	}
}

func TestMergeRoomTypes_EmptyInputs(t *testing.T) {
	google := []RoomType{{Name: "Test Room", Price: 100, Currency: "USD"}}

	// Empty booking = return Google as-is.
	if got := mergeRoomTypes(google, nil); len(got) != 1 {
		t.Errorf("mergeRoomTypes(google, nil) = %d rooms, want 1", len(got))
	}

	// Empty Google = return Booking as-is.
	booking := []RoomType{{Name: "Booking Room", Price: 200, Currency: "EUR"}}
	if got := mergeRoomTypes(nil, booking); len(got) != 1 {
		t.Errorf("mergeRoomTypes(nil, booking) = %d rooms, want 1", len(got))
	}
}

func TestExtractRoomNamesFromSSR(t *testing.T) {
	page := `some html before "room_name":"Deluxe Double Room" and "room_name":"Standard Single Room" and "room_name":"Deluxe Double Room" more html`

	offers := extractRoomNamesFromSSR(page)
	if len(offers) != 2 {
		t.Fatalf("expected 2 deduplicated SSR rooms, got %d", len(offers))
	}
	if offers[0].Name != "Deluxe Double Room" {
		t.Errorf("offers[0].Name = %q, want Deluxe Double Room", offers[0].Name)
	}
	if offers[1].Name != "Standard Single Room" {
		t.Errorf("offers[1].Name = %q, want Standard Single Room", offers[1].Name)
	}
}

func TestFetchBookingRooms_EmptyURL(t *testing.T) {
	_, err := FetchBookingRooms(context.Background(), "", "", "", "")
	if err == nil {
		t.Error("expected error for empty booking URL")
	}
}

func TestTitleCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sea view", "Sea View"},
		{"free wifi", "Free Wifi"},
		{"air conditioning", "Air Conditioning"},
		{"balcony", "Balcony"},
		{"", ""},
	}
	for _, tt := range tests {
		got := titleCase(tt.input)
		if got != tt.want {
			t.Errorf("titleCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
