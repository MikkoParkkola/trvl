package hotels

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// consentWall is a page behind Google's EU consent wall.
var consentWall = []byte(`<html><form action="https://consent.google.com/save">` + strings.Repeat("<!-- consent -->", 80) + `</form></html>`)

// searchAnswer serves the search GET: the consent wall first when consent is
// set, then (or directly) the given answer.
func searchAnswer(consent bool, final http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if consent && !strings.Contains(r.Header.Get("Cookie"), "SOCS=") {
			_, _ = w.Write(consentWall)
			return
		}
		final(w, r)
	}
}

// quotaAt answers with Google's quota body under the given status.
func quotaAt(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(googleQuotaBody))
	}
}

// TestGoogleRefusals_RealPaths drives each Google call site with every
// refusal the client can hand it and checks the refusal class and whether the
// cooldown marker is set. Only a 429, a 200 quota body or a cooldown skip arms
// the cooldown; a 403 or 503 is a refusal without one (#826 design C1-C5).
func TestGoogleRefusals_RealPaths(t *testing.T) {
	if !batchexec.QuotaRefusal(http.StatusOK, []byte(googleQuotaBody)) {
		t.Fatal("precondition: batchexec no longer reads the status-13 body as a quota refusal")
	}
	answers := []struct {
		name       string
		handler    http.HandlerFunc
		prime      bool
		wantMarker bool
	}{
		{"429", answer(http.StatusTooManyRequests), false, true},
		{"200 quota body", page([]byte(googleQuotaBody)), false, true},
		{"cooldown skip", noSacherPage, true, true},
		{"403", answer(http.StatusForbidden), false, false},
		{"503", answer(http.StatusServiceUnavailable), false, false},
		// batchexec arms the cooldown only for a 429 or a 200 quota body, so
		// a quota body on any other status must not be probed for a retry
		// time: the probe would reach Google (grok, review on 738ac158).
		// strictGoogle fails the test if it does.
		{"403 quota body", quotaAt(http.StatusForbidden), false, false},
		{"503 quota body", quotaAt(http.StatusServiceUnavailable), false, false},
	}
	opts := RoomSearchOptions{HotelID: sacherPlaceIDRooms, CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Currency: "USD"}
	searchOpts := HotelSearchOptions{CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Guests: 2, Currency: "USD"}
	sites := []struct {
		name  string
		route func(h http.HandlerFunc) googleRoutes
		call  func() error
	}{
		{"C1/C2 search page", func(h http.HandlerFunc) googleRoutes { return googleRoutes{shortEntity, answer(404), h} },
			func() error {
				_, err := fetchHotelPageFull(context.Background(), DefaultClient(), "Vienna", searchOpts, 0, "")
				return err
			}},
		{"C3 consent retry", func(h http.HandlerFunc) googleRoutes {
			return googleRoutes{shortEntity, answer(404), searchAnswer(true, h)}
		},
			func() error {
				_, err := fetchHotelPageFull(context.Background(), DefaultClient(), "Vienna", searchOpts, 0, "")
				return err
			}},
		{"C4 entity page", func(h http.HandlerFunc) googleRoutes { return googleRoutes{h, answer(404), noSacherPage} },
			func() error { _, _, _, err := tryEntityPage(context.Background(), opts); return err }},
		{"C5 price RPC", func(h http.HandlerFunc) googleRoutes { return googleRoutes{shortEntity, h, noSacherPage} },
			func() error {
				_, err := hotelPricesRPC(context.Background(), HotelPriceOpts{HotelID: opts.HotelID, CheckIn: opts.CheckIn, CheckOut: opts.CheckOut, Currency: "USD"})
				return err
			}},
	}
	for _, site := range sites {
		for _, a := range answers {
			if a.prime && site.name == "C3 consent retry" {
				continue // a skip never reaches the retry: the first request is skipped already
			}
			t.Run(site.name+"/"+a.name, func(t *testing.T) {
				roomLookupWorld(t, site.route(a.handler), a.prime)
				err := site.call()
				var c googleCall
				c.note(0, err)
				if c.refusal() == nil {
					t.Fatalf("err = %v, want a refusal", err)
				}
				if got := googleCooldown(err); got != a.wantMarker {
					t.Fatalf("err = %v\ncooldown marker %v, want %v", err, got, a.wantMarker)
				}
				// #813: a refusal that armed the cooldown says when a retry can
				// work, the arming 429 or quota body as much as a later skip.
				if a.wantMarker && !strings.Contains(err.Error(), "retry after") {
					t.Fatalf("err = %v\nwant the retry time", err)
				}
			})
		}
	}
}
