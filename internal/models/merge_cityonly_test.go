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
