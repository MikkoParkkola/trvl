package models

import (
	"testing"
	"time"
)

func TestParseMoney(t *testing.T) {
	cases := []struct {
		amount, currency string
		wantAmt          float64
		wantCur          string
	}{
		{"120", "EUR", 120, "EUR"},
		{"120.50", "USD", 120.50, "USD"},
		{"120,50", "EUR", 120.50, "EUR"},    // EU decimal comma
		{"1.200,50", "EUR", 1200.50, "EUR"}, // EU thousands + decimal
		{"1,200.00", "USD", 1200.00, "USD"}, // US thousands + decimal
		{"€120", "", 120, "EUR"},            // symbol-derived currency
		{"$1,299", "", 1299, "USD"},         // symbol + thousands
		{"£99.99", "", 99.99, "GBP"},        // pound
		{"", "EUR", 0, "EUR"},               // empty amount
		{"abc", "USD", 0, "USD"},            // garbage
		{"120", "eur", 120, "EUR"},          // lowercase currency normalized
	}
	for _, c := range cases {
		got := ParseMoney(c.amount, c.currency)
		if got.Amount != c.wantAmt || got.Currency != c.wantCur {
			t.Errorf("ParseMoney(%q,%q) = {%v %q}, want {%v %q}", c.amount, c.currency, got.Amount, got.Currency, c.wantAmt, c.wantCur)
		}
	}
}

func TestParseTemporal(t *testing.T) {
	cases := []struct {
		in     string
		wantOK bool
	}{
		{"2026-05-23T14:30:00Z", true},
		{"2026-05-23T14:30:00", true},
		{"2026-05-23 14:30:05", true},
		{"2026-05-23T14:30", true},
		{"20260523143005", true},
		{"20260523", true},
		{"2026-05-23", true},
		{"23.05.2026 14:30", true},
		{"23.05.2026", true},
		{"1747999800", true},    // unix seconds
		{"1747999800000", true}, // unix millis
		{"", false},
		{"not-a-date", false},
	}
	for _, c := range cases {
		got, ok := ParseTemporal(c.in)
		if ok != c.wantOK {
			t.Errorf("ParseTemporal(%q) ok=%v want %v", c.in, ok, c.wantOK)
		}
		if ok && got.IsZero() {
			t.Errorf("ParseTemporal(%q) returned ok but zero time", c.in)
		}
	}
	// Verify a known value round-trips correctly.
	got, ok := ParseTemporal("2026-05-23T14:30:00Z")
	if !ok || got.Year() != 2026 || got.Month() != time.May || got.Day() != 23 || got.Hour() != 14 {
		t.Errorf("ParseTemporal RFC3339 mismatch: %v", got)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"330", 330},
		{"PT5H30M", 330},
		{"PT45M", 45},
		{"PT2H", 120},
		{"5:30", 330},
		{"5h30", 330},
		{"5h", 300},
		{"90m", 90},
		{"", 0},
		{"garbage", 0},
	}
	for _, c := range cases {
		if got := ParseDuration(c.in); got != c.want {
			t.Errorf("ParseDuration(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseCabinClassAliases(t *testing.T) {
	// IATA letter codes and colloquial forms added for canonicalization.
	cases := []struct {
		in   string
		want CabinClass
	}{
		{"Y", Economy},
		{"coach", Economy},
		{"2nd", Economy},
		{"W", PremiumEconomy},
		{"C", Business},
		{"J", Business},
		{"F", First},
		{"first class", First},
	}
	for _, c := range cases {
		got, err := ParseCabinClass(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseCabinClass(%q) = %v (err %v), want %v", c.in, got, err, c.want)
		}
	}
}

func TestParsePlace(t *testing.T) {
	// IATA code resolves to airport with city.
	p := ParsePlace("CDG")
	if p.Kind != PlaceAirport || p.Code != "CDG" {
		t.Errorf("ParsePlace(CDG) = %+v, want airport CDG", p)
	}
	// Empty -> unknown.
	if e := ParsePlace(""); e.Kind != PlaceUnknown {
		t.Errorf("ParsePlace(empty) kind = %v, want unknown", e.Kind)
	}
	// Lowercase iata still resolves.
	if l := ParsePlace("jfk"); l.Kind != PlaceAirport || l.Code != "JFK" {
		t.Errorf("ParsePlace(jfk) = %+v, want airport JFK", l)
	}
	if c := ParsePlace("Paris"); c.Kind != PlaceCity {
		t.Errorf("ParsePlace(Paris) kind = %v, want city", c.Kind)
	}
	if u := ParsePlace("Zzqxwlandia"); u.Kind != PlaceUnknown {
		t.Errorf("ParsePlace fallthrough kind = %v", u.Kind)
	}
}

func TestFormatProviderDate(t *testing.T) {
	cases := []struct {
		in, layout, want string
		ok               bool
	}{
		{"2026-06-15", DateLayoutDMY, "15/06/2026", true},
		{"2026-06-15T08:00:00Z", DateLayoutDMY, "15/06/2026", true},
		{"2026-06-15", DateLayoutCompact, "20260615", true},
		{"2026-06-15", DateLayoutDottedDMY, "15.06.2026", true},
		{"15/06/2026", DateLayoutYMD, "2026-06-15", true},
		{"garbage", DateLayoutDMY, "garbage", false},
	}
	for _, c := range cases {
		got, ok := FormatProviderDate(c.in, c.layout)
		if got != c.want || ok != c.ok {
			t.Errorf("FormatProviderDate(%q,%q) = %q,%v want %q,%v", c.in, c.layout, got, ok, c.want, c.ok)
		}
	}
}
