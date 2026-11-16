package models

import "testing"

// sacherRows are the two H1 rows at identical coordinates: Booking's row with
// the street address, and Google's ad row whose only address is the city.
func sacherRows() (street, cityOnly HotelResult) {
	street = HotelResult{
		Name: "Hotel Sacher Wien", Address: "Philharmoniker-Straße 4, 1010 Wien, Austria",
		HotelID: "4711", Price: 480, Currency: "EUR", Lat: 48.2040009, Lon: 16.3697963,
		Sources: []PriceSource{{Provider: "booking", Price: 480, Currency: "EUR"}},
	}
	cityOnly = HotelResult{
		Name: "Hotel Sacher Vienna", Address: "Vienna, Austria",
		Price: 1002, Currency: "EUR", Lat: 48.2040009, Lon: 16.3697963,
		Sources: []PriceSource{{Provider: "google_hotels", Price: 1002, Currency: "EUR"}},
	}
	return street, cityOnly
}

// TestMergeHotelResults_CityOnlyAddressDoesNotSplit: a city-only address is no
// address. It must neither refuse the merge nor replace the street address, in
// either arrival order.
func TestMergeHotelResults_CityOnlyAddressDoesNotSplit(t *testing.T) {
	street, cityOnly := sacherRows()
	for _, order := range [][]HotelResult{{street, cityOnly}, {cityOnly, street}} {
		got := MergeHotelResults(order[:1], order[1:])
		if len(got) != 1 {
			t.Fatalf("merge of %q then %q = %d rows, want 1", order[0].Name, order[1].Name, len(got))
		}
		if got[0].Address != street.Address {
			t.Errorf("merge of %q then %q kept address %q, want the street address", order[0].Name, order[1].Name, got[0].Address)
		}
	}
}

// TestMergeHotelResults_PriceZeroRowKeepsSources: a row with no headline price
// still names where it was found; dropping its sources hides the Booking page
// the rooms lookup needs.
func TestMergeHotelResults_PriceZeroRowKeepsSources(t *testing.T) {
	const url = "https://www.booking.com/hotel/at/sacher.html"
	got := MergeHotelResults([]HotelResult{{
		Name: "Hotel Sacher Wien", HotelID: "4711",
		Sources: []PriceSource{{Provider: "booking", BookingURL: url}},
	}})
	if len(got) != 1 || len(got[0].Sources) != 1 || got[0].Sources[0].BookingURL != url {
		t.Fatalf("sources = %+v, want the booking source with its URL", got[0].Sources)
	}
}

// TestMergeHotelResults_GoogleIDBeatsOtherIDs: the merged row carries the
// Google ID whichever row arrives first; a Booking id must not shadow it.
func TestMergeHotelResults_GoogleIDBeatsOtherIDs(t *testing.T) {
	for _, googleID := range []string{"ChIJI85wbpwHbUcRiJL_xm_zLE4", "0x476d079e5136ca9f:0xfdc2e58a51a25b46"} {
		booking, google := sacherRows()
		google.HotelID = googleID
		for _, order := range [][]HotelResult{{booking, google}, {google, booking}} {
			got := MergeHotelResults(order[:1], order[1:])
			if len(got) != 1 || got[0].HotelID != googleID {
				t.Errorf("merge of %q then %q = %+v, want one row with HotelID %q", order[0].Name, order[1].Name, got, googleID)
			}
		}
	}
}

// TestStreetAddress_Forms: an address is city-only when every comma part is
// an alias-table city, a country, or a postcode followed by such a city;
// anything else is a street.
func TestStreetAddress_Forms(t *testing.T) {
	cases := map[string]bool{ // address -> is a street
		"Vienna, Austria":       false,
		"1010 Wien, Austria":    false,
		"Wien":                  false,
		"Austria":               false,
		"Paris, France":         false,
		"Rome, Italy":           false,
		"75001 Paris, France":   false,
		"75001 Paris":           false,
		"Rue de Rivoli, Paris":  true,
		"Kärntner Ring, Vienna": true,
		"Philharmoniker-Straße 4, 1010 Wien, Austria": true,
		"123 Main St": true,
	}
	for address, street := range cases {
		if got := streetAddress(address) != ""; got != street {
			t.Errorf("streetAddress(%q) is a street = %v, want %v", address, got, street)
		}
	}
}
