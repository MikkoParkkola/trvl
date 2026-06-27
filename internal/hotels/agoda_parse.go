package hotels

import (
	"crypto/rand"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// parseAgodaSearch converts a citySearch response into HotelResults, applying
// the client-side MaxPrice / MinRating filters that the other providers honor.
func parseAgodaSearch(resp *agodaSearchResponse, opts HotelSearchOptions) []models.HotelResult {
	if resp == nil {
		return nil
	}
	out := make([]models.HotelResult, 0, len(resp.Data.CitySearch.Properties))
	// Agoda quotes a per-room-per-night rate; derive the stay total once so
	// multi-night offers are comparable and displayable alongside providers
	// that return a room total (Google/Booking). nights is floored at 1.
	nights := agodaNights(opts.CheckIn, opts.CheckOut)
	for _, p := range resp.Data.CitySearch.Properties {
		info := p.Content.InformationSummary
		if info.DisplayName == "" {
			continue
		}

		offerDetail := agodaFirstRoomOffer(p)
		price, currency, crossedOut := offerDetail.exclusive, offerDetail.currency, offerDetail.crossedOut

		// MaxPrice filter (per night). Skip priced properties above the cap;
		// keep unpriced ones so callers still see availability.
		if opts.MaxPrice > 0 && price > 0 && price > opts.MaxPrice {
			continue
		}

		hr := models.HotelResult{
			Name:         info.DisplayName,
			HotelID:      strconv.Itoa(p.PropertyID),
			Stars:        int(info.Rating),
			Price:        price,
			Currency:     currency,
			Address:      agodaAddress(p),
			Lat:          info.GeoInfo.Latitude,
			Lon:          info.GeoInfo.Longitude,
			PropertyType: agodaPropertyType(info.PropertyType),
			ImageURL:     agodaImageURL(p),
			BookingURL:   agodaBookingURL(info.PropertyLinks.PropertyPage),
		}

		if len(p.Content.Reviews.ContentReview) > 0 {
			cum := p.Content.Reviews.ContentReview[0].Cumulative
			hr.Rating = cum.Score
			hr.ReviewCount = cum.ReviewCount
		}

		// MinRating filter (0-10 guest score).
		if opts.MinRating > 0 && hr.Rating > 0 && hr.Rating < opts.MinRating {
			continue
		}

		if price > 0 {
			hr.Sources = []models.PriceSource{{
				Provider:   "agoda",
				Price:      price,
				MaxPrice:   crossedOut,
				Currency:   currency,
				BookingURL: hr.BookingURL,
				PriceBasis: "room_nightly",
			}}
			// Agoda's per-room-per-night exclusive price is a genuine room rate,
			// not a property lead-in, so surface it as room-level inventory and
			// let it reach the booking-ready gate instead of collapsing to a
			// property-level source. Tagged "similar" (not "exact") because the
			// citySearch response gives a room rate without a nameable room
			// identity. One room only: emitting the whole price ladder of
			// identity-less rooms would trip the multi-provider-mixed
			// completeness flag and re-block the offer.
			room := models.Room{
				Name:            "Agoda room rate",
				Price:           price,
				NightlyPrice:    price,
				TotalPrice:      agodaStayTotal(price, nights),
				Currency:        currency,
				Provider:        "agoda",
				ProviderURL:     hr.BookingURL,
				MatchConfidence: models.RoomInventoryMatchSimilar,
				PriceBasis:      models.PriceBasisRoomNightly,
				PriceConfidence: models.PriceConfidenceRoomLevel,
			}

			// Agoda also returns a tax-and-fees-inclusive per-room-per-night
			// rate in the same offer. It was previously discarded. When present
			// and above the pre-tax rate, surface the all-in stay total: this is
			// a tax-inclusive room total, which the trust model rates as verified
			// (the top tier), so the price-trust sort leads with this real all-in
			// Agoda price over unverified lead-ins from other providers. The
			// comparable headline Price/NightlyPrice stays the pre-tax rate so
			// cross-provider comparison is apples-to-apples.
			if offerDetail.inclusive > price {
				inclusiveTotal := agodaStayTotal(offerDetail.inclusive, nights)
				if inclusiveTotal > 0 {
					room.TotalPrice = inclusiveTotal
					room.TaxesAndFees = inclusiveTotal - room.NightlyPrice*float64(nights)
					taxIncluded := true
					room.TaxesFeesIncluded = &taxIncluded
					room.PriceBasis = models.PriceBasisTaxInclusiveTotal
					room.PriceConfidence = models.PriceConfidenceVerified
				}
			}

			// Surface the real cancellation terms Agoda returns rather than
			// discarding them. Only the two unambiguous values are mapped.
			if refundable, freeCanc, policy := agodaCancellationDetail(offerDetail.cancellationType); policy != "" {
				room.Refundable = refundable
				room.FreeCancellation = freeCanc
				room.CancellationPolicy = policy
			}

			hr.RoomTypes = []models.Room{room}
		}

		out = append(out, hr)
	}
	return out
}

// agodaRoomOffer is the room-level detail extracted from the first priced room
// offer in an Agoda citySearch response. The exclusive display is the comparable
// headline rate (pre-tax, what other providers quote); inclusive is the
// tax-and-fees-inclusive rate Agoda returns alongside it; cancellationType is
// the real refundability signal from payment.cancellation. All three come from
// the SAME room offer so they describe one consistent, bookable rate.
type agodaRoomOffer struct {
	exclusive        float64 // per-room-per-night, pre-tax (comparable headline)
	crossedOut       float64 // crossed-out pre-tax rate (savings signal)
	inclusive        float64 // per-room-per-night, taxes+fees included
	currency         string
	cancellationType string // e.g. "NonRefundable", "FreeCancellation"
	hasOffer         bool
}

// agodaFirstRoomOffer walks to the first room offer that carries a positive
// per-room-per-night exclusive display price and returns its room-level detail.
// Agoda's citySearch returns one room offer (the cheapest available room) per
// property, so this is that room's full priced detail — the exclusive and
// inclusive figures and the cancellation terms were all already in the response
// and previously discarded.
func agodaFirstRoomOffer(p agodaProperty) agodaRoomOffer {
	for _, offer := range p.Pricing.Offers {
		for _, ro := range offer.RoomOffers {
			cancellationType := ro.Room.Payment.Cancellation.CancellationType
			for _, pr := range ro.Room.Pricing {
				prpn := pr.Price.PerRoomPerNight
				if prpn.Exclusive.Display > 0 {
					return agodaRoomOffer{
						exclusive:        prpn.Exclusive.Display,
						crossedOut:       prpn.Exclusive.CrossedOutPrice,
						inclusive:        prpn.Inclusive.Display,
						currency:         pr.Currency,
						cancellationType: cancellationType,
						hasOffer:         true,
					}
				}
			}
		}
	}
	return agodaRoomOffer{}
}

// agodaCancellationDetail maps Agoda's payment.cancellation.cancellationType to
// the refundability fields on a Room. Only the two unambiguous values are
// mapped; anything else (or empty) leaves the fields unset rather than guessing.
func agodaCancellationDetail(cancellationType string) (refundable *bool, freeCancellation bool, policy string) {
	switch strings.ToLower(strings.TrimSpace(cancellationType)) {
	case "nonrefundable", "non-refundable":
		v := false
		return &v, false, "Non-refundable"
	case "freecancellation", "free cancellation":
		v := true
		return &v, true, "Free cancellation"
	default:
		return nil, false, ""
	}
}

func agodaAddress(p agodaProperty) string {
	a := p.Content.InformationSummary.Address
	parts := make([]string, 0, 2)
	if a.City.Name != "" {
		parts = append(parts, a.City.Name)
	}
	if a.Country.Name != "" {
		parts = append(parts, a.Country.Name)
	}
	return strings.Join(parts, ", ")
}

func agodaPropertyType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "hotel":
		return "hotel"
	case "hostel":
		return "hostel"
	case "apartment", "serviced apartment":
		return "apartment"
	default:
		return strings.ToLower(t)
	}
}

func agodaImageURL(p agodaProperty) string {
	for _, img := range p.Content.Images.HotelImages {
		for _, u := range img.URLs {
			if u.Value != "" {
				return agodaAbsoluteURL(u.Value)
			}
		}
	}
	return ""
}

func agodaBookingURL(page string) string {
	page = strings.TrimSpace(page)
	if page == "" {
		return ""
	}
	if strings.HasPrefix(page, "http") {
		return page
	}
	if !strings.HasPrefix(page, "/") {
		page = "/" + page
	}
	return agodaBaseURL + page
}

// agodaAbsoluteURL upgrades a protocol-relative ("//host/path") or root-relative
// image URL to an absolute https URL.
func agodaAbsoluteURL(u string) string {
	u = strings.TrimSpace(u)
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(u, "http"):
		return u
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	case strings.HasPrefix(u, "/"):
		return agodaBaseURL + u
	default:
		return u
	}
}

// agodaUUID returns a random RFC-4122 v4 UUID string. Agoda accepts any UUID for
// the correlation/request/userId fields; there is no server-side validation tied
// to a session, so a freshly generated one per call is fine.
func agodaUUID() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		// Fall back to a time-seeded value; correctness does not depend on the
		// UUID being cryptographically strong, only well-formed and unique-ish.
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (uint(i%8) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
