// Package openjaw prices a fly-into-A plus train-out-of-B journey as one bundle.
package openjaw

import "fmt"

// Leg is one priced hop. Mode is "flight" or a ground mode such as "train".
type Leg struct {
	Mode          string
	Origin        string
	Destination   string
	Cost          float64
	DurationMin   int
	TransferAfter string
}

// Bundle is the single price of the composed journey.
type Bundle struct {
	Legs  []Leg
	Total float64
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
	if !samePlace(flight.Destination, onward.Origin) {
		return Bundle{}, fmt.Errorf("onward leg leaves %s, flight arrives %s", onward.Origin, flight.Destination)
	}
	flight.TransferAfter = flight.Destination
	return Bundle{
		Legs:  []Leg{flight, onward},
		Total: flight.Cost + onward.Cost,
	}, nil
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
