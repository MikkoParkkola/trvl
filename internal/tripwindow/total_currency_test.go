package tripwindow

import (
	"context"
	"math"
	"testing"
)

// stubConverter converts to EUR using a fixed rate table. It mirrors the real
// destinations.ConvertCurrency failure contract: on an unknown currency it
// returns the original amount and the *source* currency (conversion did not
// happen), so callers must check the returned currency to know it succeeded.
func stubConverter(rates map[string]float64) currencyConverter {
	return func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == to || from == "" || to == "" || amount == 0 {
			return amount, to
		}
		if to != "EUR" {
			return amount, from // this stub only reaches EUR
		}
		r, ok := rates[from]
		if !ok || r == 0 {
			return amount, from // can't convert
		}
		return amount * r, "EUR"
	}
}

// TestNormalizeTripTotalEUR_SameCurrency: both legs already EUR -> plain sum.
func TestNormalizeTripTotalEUR_SameCurrency(t *testing.T) {
	conv := stubConverter(nil)
	total, _, _, cur := normalizeTripTotalEUR(context.Background(), conv, 200, "EUR", 300, "EUR")
	if cur != "EUR" || total != 500 {
		t.Fatalf("same-currency sum: got %v %s, want 500 EUR", total, cur)
	}
}

// TestNormalizeTripTotalEUR_ConvertsForeignHotel: flight EUR, hotel GBP with a
// live rate -> both normalized to EUR before summation (the honest total).
func TestNormalizeTripTotalEUR_ConvertsForeignHotel(t *testing.T) {
	conv := stubConverter(map[string]float64{"GBP": 1.16})
	total, flightEUR, hotelEUR, cur := normalizeTripTotalEUR(context.Background(), conv, 200, "EUR", 450, "GBP")
	if cur != "EUR" {
		t.Fatalf("currency: got %s, want EUR", cur)
	}
	if want := 200.0 + 450.0*1.16; total != want {
		t.Fatalf("converted total: got %v, want %v", total, want)
	}
	// Components must be the converted EUR amounts, not the native quotes, so
	// the response never labels a GBP hotel price as EUR.
	if flightEUR != 200.0 {
		t.Fatalf("flight component: got %v, want 200 (already EUR)", flightEUR)
	}
	if want := 450.0 * 1.16; hotelEUR != want {
		t.Fatalf("hotel component: got %v, want %v (converted to EUR)", hotelEUR, want)
	}
}

// TestNormalizeTripTotalEUR_RefusesUnconvertibleMix is the core anti-lie
// regression: flight EUR 200 + hotel GBP 450 with NO available GBP rate must
// NOT be summed into a franken-total labelled EUR. Since a nonzero component
// cannot be brought into EUR, the honest answer is "unknown" (0, "") — which
// makes the window sort last and escape the EUR budget filter rather than
// silently admit an over-budget or mis-ranked trip.
func TestNormalizeTripTotalEUR_RefusesUnconvertibleMix(t *testing.T) {
	conv := stubConverter(nil) // GBP has no rate -> conversion fails
	total, flightEUR, hotelEUR, cur := normalizeTripTotalEUR(context.Background(), conv, 200, "EUR", 450, "GBP")
	if total != 0 || cur != "" {
		t.Fatalf("unconvertible mix must be refused: got %v %q, want 0 \"\"", total, cur)
	}
	// A refused total must not leak partial component amounts either.
	if flightEUR != 0 || hotelEUR != 0 {
		t.Fatalf("refused mix must zero components: got flight=%v hotel=%v, want 0 0", flightEUR, hotelEUR)
	}
}

// TestNormalizeTripTotalEUR_ZeroComponentDoesNotBlock: a component priced 0
// (search failed for that leg) contributes nothing and never triggers refusal;
// the other, convertible leg still yields an honest EUR total.
func TestNormalizeTripTotalEUR_ZeroComponentDoesNotBlock(t *testing.T) {
	conv := stubConverter(map[string]float64{"USD": 0.92})
	total, _, _, cur := normalizeTripTotalEUR(context.Background(), conv, 0, "", 500, "USD")
	if cur != "EUR" || total != 500*0.92 {
		t.Fatalf("zero flight + convertible hotel: got %v %s, want %v EUR", total, cur, 500*0.92)
	}
}

// TestNormalizeTripTotalEUR_ForeignFlightConverts: flight USD converts, hotel 0.
func TestNormalizeTripTotalEUR_ForeignFlightConverts(t *testing.T) {
	conv := stubConverter(map[string]float64{"USD": 0.92})
	total, _, _, cur := normalizeTripTotalEUR(context.Background(), conv, 500, "USD", 0, "")
	if cur != "EUR" || total != 500*0.92 {
		t.Fatalf("convertible flight + zero hotel: got %v %s, want %v EUR", total, cur, 500*0.92)
	}
}

// TestNormalizeTripTotalEUR_RefusesNonFinite: a converter that yields NaN or
// +Inf (corrupt/overflowing rate) must be refused, not summed. A non-finite
// total would slip past the eur<=0 guard, bypass the budget filter, corrupt the
// ascending-price ranking, and break JSON encoding of the whole MCP response.
func TestNormalizeTripTotalEUR_RefusesNonFinite(t *testing.T) {
	for _, tc := range []struct {
		name string
		rate float64
	}{
		{"NaN", math.NaN()},
		{"PosInf", math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv := stubConverter(map[string]float64{"XXX": tc.rate})
			total, flightEUR, hotelEUR, cur := normalizeTripTotalEUR(context.Background(), conv, 200, "EUR", 100, "XXX")
			if total != 0 || cur != "" || flightEUR != 0 || hotelEUR != 0 {
				t.Fatalf("non-finite %s rate must be refused: got total=%v flight=%v hotel=%v cur=%q, want all zero", tc.name, total, flightEUR, hotelEUR, cur)
			}
		})
	}
}
