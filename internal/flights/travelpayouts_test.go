package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTravelpayouts(t *testing.T) {
	body := []byte(`{"success":true,"currency":"eur","data":[
		{"origin":"AMS","destination":"HEL","departure_at":"2026-06-10","return_at":"","price":129.0,"airline":"AY","transfers":0,"link":"/AMSHEL?t=abc"},
		{"origin":"AMS","destination":"HEL","departure_at":"2026-06-12","return_at":"2026-06-20","price":210.0,"airline":"KL","transfers":1,"link":"https://www.aviasales.com/full"},
		{"origin":"AMS","destination":"HEL","departure_at":"2026-06-15","price":0,"airline":"XX","transfers":0,"link":""}
	]}`)
	signals, err := parseTravelpayouts(body, "EUR")
	if err != nil {
		t.Fatalf("parseTravelpayouts: %v", err)
	}
	if len(signals) != 2 { // zero-price row skipped
		t.Fatalf("got %d signals, want 2", len(signals))
	}
	if signals[0].Airline != "AY" || signals[0].Price != 129 || signals[0].Currency != "EUR" {
		t.Errorf("signal 0 mismatch: %+v", signals[0])
	}
	if !strings.HasPrefix(signals[0].BookingURL, "https://www.aviasales.com/AMSHEL") {
		t.Errorf("relative link not absolutized: %q", signals[0].BookingURL)
	}
	if signals[1].BookingURL != "https://www.aviasales.com/full" {
		t.Errorf("absolute link mangled: %q", signals[1].BookingURL)
	}
}

func TestParseTravelpayouts_NotSuccess(t *testing.T) {
	if _, err := parseTravelpayouts([]byte(`{"success":false,"error":"bad token"}`), "EUR"); err == nil {
		t.Error("expected error on success=false")
	}
}

func TestSearchTravelpayouts_OptInSkip(t *testing.T) {
	t.Setenv("TRAVELPAYOUTS_TOKEN", "")
	got, err := SearchTravelpayoutsPrices(context.Background(), "AMS", "HEL", "2026-06", "EUR", true)
	if err != nil || got != nil {
		t.Errorf("expected no-op (nil,nil) when token unset, got %v / %v", got, err)
	}
}

func TestSearchTravelpayouts_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "tok123" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"currency":"eur","data":[{"origin":"AMS","destination":"HEL","departure_at":"2026-06-10","price":99,"airline":"AY","transfers":0,"link":"/x"}]}`))
	}))
	defer srv.Close()
	oldHost := travelpayoutsHost
	travelpayoutsHost = srv.URL
	defer func() { travelpayoutsHost = oldHost }()
	t.Setenv("TRAVELPAYOUTS_TOKEN", "tok123")

	got, err := SearchTravelpayoutsPrices(context.Background(), "AMS", "HEL", "2026-06", "EUR", true)
	if err != nil {
		t.Fatalf("SearchTravelpayoutsPrices: %v", err)
	}
	if len(got) != 1 || got[0].Price != 99 || got[0].Airline != "AY" {
		t.Fatalf("unexpected result: %+v", got)
	}
}
