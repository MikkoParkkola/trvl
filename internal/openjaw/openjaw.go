// Package openjaw prices a fly-into-A plus train-out-of-B journey as one bundle.
package openjaw

import (
	"fmt"
	"math"
	"strings"
)

// Leg is one priced hop. Mode is "flight" or a ground mode such as "train".
type Leg struct {
	Mode          string  `json:"mode"`
	Origin        string  `json:"origin"`
	Destination   string  `json:"destination"`
	Cost          float64 `json:"cost"`
	Currency      string  `json:"currency"`
	DurationMin   int     `json:"duration_min,omitempty"`
	TransferAfter string  `json:"transfer_after,omitempty"`
}

// Bundle is the single price of the composed journey.
type Bundle struct {
	Legs     []Leg   `json:"legs"`
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
}

// Compose prices the flight into A and the onward ground leg out of A as one
// bundle. The ground leg must leave the flight's arrival city.
func Compose(flight, onward Leg) (Bundle, error) {
	if flight.Origin == "" || flight.Destination == "" || onward.Origin == "" || onward.Destination == "" {
		return Bundle{}, fmt.Errorf("both legs need an origin and a destination")
	}
	if !finiteNonNegative(flight.Cost) || !finiteNonNegative(onward.Cost) {
		return Bundle{}, fmt.Errorf("leg cost must be a finite zero or positive number")
	}
	total := flight.Cost + onward.Cost
	if !finiteNonNegative(total) {
		return Bundle{}, fmt.Errorf("bundle total is not a finite number")
	}
	currency, err := oneCurrency(flight.Currency, onward.Currency)
	if err != nil {
		return Bundle{}, err
	}
	if !samePlace(flight.Destination, onward.Origin) {
		return Bundle{}, fmt.Errorf("onward leg leaves %s, flight arrives %s", onward.Origin, flight.Destination)
	}
	flight.Currency = currency
	onward.Currency = currency
	flight.TransferAfter = flight.Destination
	return Bundle{
		Legs:     []Leg{flight, onward},
		Total:    total,
		Currency: currency,
	}, nil
}

func finiteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func oneCurrency(a, b string) (string, error) {
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))
	if a == "" || b == "" {
		return "", fmt.Errorf("both legs need a currency")
	}
	if a != b {
		return "", fmt.Errorf("leg currencies differ: %s and %s", a, b)
	}
	return a, nil
}

func samePlace(a, b string) bool {
	return trimLower(a) == trimLower(b)
}

func trimLower(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}
