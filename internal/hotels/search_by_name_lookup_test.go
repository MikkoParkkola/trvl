package hotels

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
)

const lookupCheckIn, lookupCheckOut = "2026-06-15", "2026-06-18"

// serveGooglePage answers every Google Hotels request with the given hotels.
func serveGooglePage(t *testing.T, hotels ...hotelWithPrice) {
	t.Helper()
	freshRateManager(t)
	page := fakeHotelPageWithPrices(hotels...)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	}))
}

var viennaPage = []hotelWithPrice{{"ARTIST Boutique Hotel", 120}, {"Hotel Sacher", 300}}

func hotelNames(hotels []models.HotelResult) []string {
	out := make([]string, 0, len(hotels))
	for _, h := range hotels {
		out = append(out, h.Name)
	}
	return out
}

// TestSearchHotelsByName_RetriesWithoutTrailingLocationWord: "ARTIST
// Boutique Hotel Vienna" with location Vienna. No hotel name contains
// "vienna", so the lookup retries once without the trailing word because it
// equals the location argument, and finds exactly the ARTIST hotel.
func TestSearchHotelsByName_RetriesWithoutTrailingLocationWord(t *testing.T) {
	serveGooglePage(t, viennaPage...)
	seedGeo(t, "ARTIST Boutique Hotel Vienna")

	got, err := SearchHotelsByName(context.Background(), "ARTIST Boutique Hotel Vienna", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if names := hotelNames(got); len(names) != 1 || names[0] != "ARTIST Boutique Hotel" {
		t.Fatalf("matches = %v, want only ARTIST Boutique Hotel", names)
	}
}

// TestSearchHotelsByName_TwoWordNameAllowedWithLocation: with a location
// set, the stripped name may be as short as two words.
func TestSearchHotelsByName_TwoWordNameAllowedWithLocation(t *testing.T) {
	serveGooglePage(t, viennaPage...)
	seedGeo(t, "Hotel Sacher Vienna")

	got, err := SearchHotelsByName(context.Background(), "Hotel Sacher Vienna", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if names := hotelNames(got); len(names) != 1 || names[0] != "Hotel Sacher" {
		t.Fatalf("matches = %v, want only Hotel Sacher", names)
	}
}

// TestSearchHotelsByName_TrailingWordMustBeLocation: "Graz" is neither the
// location argument nor in any candidate's address, so it is not stripped;
// the ARTIST hotel in Vienna is not a match for a Graz property. The Booking
// candidate carries a real (Vienna) address, so the address check is live.
func TestSearchHotelsByName_TrailingWordMustBeLocation(t *testing.T) {
	serveGooglePage(t, viennaPage...)
	withBookingResult(t, bookingArtist("Kirchengasse 41, 1070 Vienna, Austria"))
	seedGeo(t, "ARTIST Boutique Hotel Graz, Vienna")
	requireCandidate(t, "ARTIST Boutique Hotel Graz, Vienna", "ARTIST Boutique Hotel")

	got, err := SearchHotelsByName(context.Background(), "ARTIST Boutique Hotel Graz", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want ErrNoNameMatch", err)
	}
	if len(got) != 0 {
		t.Fatalf("matches = %v, want none", hotelNames(got))
	}
}

// TestSearchHotelsByName_NoMatchIsEmptyWithSentinel: when nothing matches,
// the lookup returns no hotels and ErrNoNameMatch, never every candidate as
// if it were the requested property.
func TestSearchHotelsByName_NoMatchIsEmptyWithSentinel(t *testing.T) {
	serveGooglePage(t, viennaPage...)
	seedGeo(t, "Palais Nonexistent, Vienna")

	got, err := SearchHotelsByName(context.Background(), "Palais Nonexistent", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want ErrNoNameMatch", err)
	}
	if !strings.Contains(err.Error(), "Palais Nonexistent") {
		t.Errorf("err = %q, want it to name the requested property (the MCP notice is built from it)", err)
	}
	if len(got) != 0 {
		t.Fatalf("returned %v as matches for a property that is not there", hotelNames(got))
	}
}

// TestSearchHotelsByName_StrippedNameNeedsTwoWords: "Sacher Vienna" would
// strip to the single word "Sacher", below the two-word floor, so there is no
// retry and no match.
func TestSearchHotelsByName_StrippedNameNeedsTwoWords(t *testing.T) {
	serveGooglePage(t, viennaPage...)
	seedGeo(t, "Sacher Vienna")

	got, err := SearchHotelsByName(context.Background(), "Sacher Vienna", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want ErrNoNameMatch", err)
	}
	if len(got) != 0 {
		t.Fatalf("matches = %v, want none from a one-word stripped name", hotelNames(got))
	}
}

// TestSearchHotelsByName_GoogleErrorSurfaced: Google refused (HTTP 403) and
// nothing matched. The Google error travels with ErrNoNameMatch instead of
// being logged and dropped.
func TestSearchHotelsByName_GoogleErrorSurfaced(t *testing.T) {
	freshRateManager(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	seedGeo(t, "ARTIST Boutique Hotel, Vienna")
	if _, err := SearchHotels(context.Background(), "ARTIST Boutique Hotel, Vienna", HotelSearchOptions{
		CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Guests: 2, Currency: "USD", MaxPages: 1,
	}); !errors.Is(err, batchexec.ErrBlocked) {
		t.Fatalf("precondition: Google search against the 403 server = %v, want batchexec.ErrBlocked", err)
	}

	got, err := SearchHotelsByName(context.Background(), "ARTIST Boutique Hotel", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if len(got) != 0 {
		t.Fatalf("matches = %v, want none", hotelNames(got))
	}
	// No provider produced a candidate, so this is a failed search, not a
	// name miss: the Google error is the answer.
	if errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %q is ErrNoNameMatch; with zero candidates nothing was matched against", err)
	}
	if !errors.Is(err, batchexec.ErrBlocked) || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %q, want the Google 403 error", err)
	}
}

// TestSearchHotelsByName_NoCandidatesIsNotANameMiss: every provider answered
// with zero hotels. That is "no results", which the MCP layer must not show
// as an empty success for a missing name.
func TestSearchHotelsByName_NoCandidatesIsNotANameMiss(t *testing.T) {
	freshRateManager(t)
	page := fakeHotelPageMulti()
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	}))
	seedGeo(t, "ARTIST Boutique Hotel, Vienna")

	got, err := SearchHotelsByName(context.Background(), "ARTIST Boutique Hotel", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if len(got) != 0 {
		t.Fatalf("matches = %v, want none", hotelNames(got))
	}
	if err == nil || errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want a no-results error that is not ErrNoNameMatch", err)
	}
}

// withBookingResult makes the Booking.com slot of the search return hotels.
func withBookingResult(t *testing.T, hotels ...models.HotelResult) {
	t.Helper()
	origBooking, origBreaker := SearchBooking, hotelBreaker
	t.Cleanup(func() { SearchBooking, hotelBreaker = origBooking, origBreaker })
	hotelBreaker = breaker.New()
	SearchBooking = func(context.Context, string, HotelSearchOptions) ([]models.HotelResult, error) {
		return hotels, nil
	}
}

// requireCandidate checks that the search the single-result lookup runs
// returns the stubbed candidate with its address, so a "not found" below is
// about matching, not about the fixture never arriving.
func requireCandidate(t *testing.T, query, name string) {
	t.Helper()
	res, err := SearchHotels(context.Background(), query, HotelSearchOptions{
		CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Guests: 2, Currency: "USD",
	})
	if err != nil {
		t.Fatalf("precondition: search %q: %v", query, err)
	}
	for _, h := range res.Hotels {
		if h.Name == name && strings.Contains(h.Address, "Vienna") {
			return
		}
	}
	t.Fatalf("precondition: search %q returned %v, want %q with its Vienna address", query, hotelNames(res.Hotels), name)
}

func bookingArtist(address string) models.HotelResult {
	return models.HotelResult{
		Name: "ARTIST Boutique Hotel", Address: address, HotelID: "booking-artist",
		Price: 120, Currency: "USD", Lat: 60.1699, Lon: 24.9384,
	}
}

// TestSearchHotelByName_TrailingWordFoundInAddress: the single-result lookup
// has no location argument. "Vienna" may still be stripped because it appears
// in the candidate's address, and three words remain.
func TestSearchHotelByName_TrailingWordFoundInAddress(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Sacher", 300})
	withBookingResult(t, bookingArtist("Kirchengasse 41, 1070 Vienna, Austria"))
	seedGeo(t, "ARTIST Boutique Hotel Vienna")
	requireCandidate(t, "ARTIST Boutique Hotel Vienna", "ARTIST Boutique Hotel")

	got, err := SearchHotelByName(context.Background(), "ARTIST Boutique Hotel Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Name != "ARTIST Boutique Hotel" {
		t.Fatalf("matched %q, want ARTIST Boutique Hotel", got.Name)
	}
}

// TestSearchHotelByName_TwoWordsNeedLocation guards the other half of the
// word rule: without a location argument a stripped name needs three words,
// so "Hotel Sacher" from "Hotel Sacher Vienna" is not enough on its own.
func TestSearchHotelByName_TwoWordsNeedLocation(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Imperial", 300})
	withBookingResult(t, models.HotelResult{
		Name: "Hotel Sacher", Address: "Philharmoniker Str. 4, 1010 Vienna", HotelID: "booking-sacher",
		Price: 300, Currency: "USD", Lat: 60.1699, Lon: 24.9384,
	})
	seedGeo(t, "Hotel Sacher Vienna")
	requireCandidate(t, "Hotel Sacher Vienna", "Hotel Sacher")

	got, err := SearchHotelByName(context.Background(), "Hotel Sacher Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err == nil {
		t.Fatalf("matched %q from a two-word stripped name without a location", got.Name)
	}
	if !errors.Is(err, ErrNoNameMatch) {
		t.Fatalf("err = %v, want ErrNoNameMatch", err)
	}
}

// TestSearchHotelsByName_StripsOnlyAfterAMiss: the full name is tried first.
// "Hotel Sacher Vienna" exists under that exact name, so the lookup must not
// strip "Vienna" up front and also return the different "Hotel Sacher".
func TestSearchHotelsByName_StripsOnlyAfterAMiss(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Sacher Vienna", 300}, hotelWithPrice{"Hotel Sacher", 280})
	seedGeo(t, "Hotel Sacher Vienna")

	got, err := SearchHotelsByName(context.Background(), "Hotel Sacher Vienna", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if names := hotelNames(got); len(names) != 1 || names[0] != "Hotel Sacher Vienna" {
		t.Fatalf("matches = %v, want only the exact Hotel Sacher Vienna", names)
	}
}

// TestSearchHotelsByName_StripsWordFoundInAddress: "Neubau" is not the
// location argument, but the candidate's address contains it, so the plural
// lookup may retry without it.
func TestSearchHotelsByName_StripsWordFoundInAddress(t *testing.T) {
	serveGooglePage(t, hotelWithPrice{"Hotel Sacher", 300})
	withBookingResult(t, bookingArtist("Kirchengasse 41, Neubau, 1070 Vienna"))
	seedGeo(t, "ARTIST Boutique Hotel Neubau, Vienna")
	requireCandidate(t, "ARTIST Boutique Hotel Neubau, Vienna", "ARTIST Boutique Hotel")

	got, err := SearchHotelsByName(context.Background(), "ARTIST Boutique Hotel Neubau", "Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if names := hotelNames(got); len(names) != 1 || names[0] != "ARTIST Boutique Hotel" {
		t.Fatalf("matches = %v, want only ARTIST Boutique Hotel", names)
	}
}
