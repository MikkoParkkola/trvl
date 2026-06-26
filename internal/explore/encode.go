package explore

import (
	"fmt"
	"net/url"
)

// EncodeExplorePayload builds the URL-encoded f.req body for GetExploreDestinations.
//
// The payload structure matches Google's internal format as used by the
// FlightsFrontendUi/GetExploreDestinations endpoint. Derived from the
// gflights project's explore.go.
func EncodeExplorePayload(srcAirport string, opts ExploreOptions) string {
	adults := opts.Adults
	if adults <= 0 {
		adults = 1
	}

	serSrc := fmt.Sprintf(`[\"%s\",0]`, srcAirport)

	// Coordinates: null for worldwide, [[N,E],[S,W]] for bounding box
	serCoords := "null"
	if opts.NorthLat != 0 || opts.EastLng != 0 || opts.SouthLat != 0 || opts.WestLng != 0 {
		serCoords = fmt.Sprintf(`[[%f,%f],[%f,%f]]`,
			opts.NorthLat, opts.EastLng, opts.SouthLat, opts.WestLng)
	}

	// Trip type: 1 = round trip, 2 = one way
	tripType := 2
	if opts.ReturnDate != "" {
		tripType = 1
	}

	// Travelers: [adults, children, infants_on_lap, infants_in_seat]
	serTravelers := fmt.Sprintf(`[%d,0,0,0]`, adults)

	// Cabin class slot: 1=economy, 2=premium economy, 3=business, 4=first
	// (matches models.CabinClass). Zero/unset defaults to economy.
	cabin := int(opts.CabinClass)
	if cabin <= 0 {
		cabin = 1
	}

	rawData := fmt.Sprintf(`%s,null,[null,null,%d,null,[],%d,%s,null,null,null,null,null,null,`,
		serCoords, tripType, cabin, serTravelers)

	// Outbound leg
	rawData += fmt.Sprintf(`[[[[%s]],[],null,0,null,null,\"%s\"]`, serSrc, opts.DepartureDate)

	// Return leg
	if opts.ReturnDate != "" {
		rawData += fmt.Sprintf(`,[[],[[%s]],null,0,null,null,\"%s\"]`, serSrc, opts.ReturnDate)
	}

	rawData += `]`

	prefix := `[null,"[[],`
	suffix := `],null,null,null,1],null,1,null,0,null,1,[694,867],4]"]`

	reqData := prefix + rawData + suffix
	return url.QueryEscape(reqData)
}
