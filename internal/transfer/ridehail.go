package transfer

import (
	"net/url"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// RideHailOptions returns ride-hail deep-link options (Uber, Bolt) for a leg.
// These are pure deep-links — trvl does not call any ride-hail API, so there is
// no price, no duration, and no ToS exposure: the app shows the real price
// before the user confirms. They are honest "tap to check availability and
// price" options, not priced/timed routes, so they are excluded from the
// cheapest/fastest/best-value labels (see labelComparison).
//
// from/to are human place labels (e.g. "BCN airport", "Hotel Eixample").
func RideHailOptions(from, to string) []models.TransferOption {
	if from == "" || to == "" {
		return nil
	}
	uber := "https://m.uber.com/ul/?action=setPickup" +
		"&pickup[formatted_address]=" + url.QueryEscape(from) +
		"&dropoff[formatted_address]=" + url.QueryEscape(to)
	bolt := "https://bolt.eu/?pickup=" + url.QueryEscape(from) + "&destination=" + url.QueryEscape(to)

	mk := func(label, deeplink string) models.TransferOption {
		return models.TransferOption{
			Mode:            "ride_hail",
			Label:           label,
			PriceIsEstimate: true, // price unknown until the app quotes it
			DoorToDoorMin:   0,    // unknown — depends on live traffic
			BookURL:         deeplink,
			Pros:            []string{"door-to-door, no luggage hassle", "price shown in-app before you confirm"},
			Cons:            []string{"price and availability vary by city and demand"},
			Steps: []models.Step{
				{Order: 1, Text: "Open the " + label + " app or the link; set pickup to " + from + " and destination to " + to + ".", Grounded: true},
				{Order: 2, Text: "Confirm the fare quote in the app before you ride.", Grounded: true},
			},
		}
	}
	return []models.TransferOption{mk("Uber", uber), mk("Bolt", bolt)}
}
