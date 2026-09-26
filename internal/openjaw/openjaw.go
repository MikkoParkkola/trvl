// Package openjaw prices a fly-into-A plus train-out-of-B journey as one bundle.
package openjaw

import (
	"fmt"
	"strings"
)

// Leg is one priced hop. Mode is "flight" or a ground mode such as "train".
type Leg struct {
	Mode          string
	Origin        string
	Destination   string
	Cost          float64
	Currency      string
	DurationMin   int
	TransferAfter string
}

// Bundle is the single price of the composed journey.
type Bundle struct {
	Legs     []Leg
	Total    float64
	Currency string
}

// Compose prices the flight into A and the onward ground leg out of A as one
// bundle. The ground leg must leave the flight's arrival city.
func Compose(flight, onward Leg) (Bundle, error) {
	if flight.Origin == "" || flight.Destination == "" || onward.Origin == "" || onward.Destination == "" {
		return Bundle{}, fmt.Errorf("both legs need an origin and a destination")
	}
	if flight.Cost < 0 || onward.Cost < 0 {
		return Bundle{}, fmt.Errorf("leg cost must be zero or positive")
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
		Total:    flight.Cost + onward.Cost,
		Currency: currency,
	}, nil
}

func oneCurrency(a, b string) (string, error) {
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))
	switch {
	case a == "":
		return b, nil
	case b == "":
		return a, nil
	case a != b:
		return "", fmt.Errorf("leg currencies differ: %s and %s", a, b)
	default:
		return a, nil
	}
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
