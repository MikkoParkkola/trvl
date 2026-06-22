package models

import (
	"strings"
	"time"
)

const (
	AccommodationTypeHotelRoom       = "hotel_room"
	AccommodationTypeEntireApartment = "entire_apartment"
	AccommodationTypePrivateRoom     = "private_room"
	AccommodationTypeSharedRoom      = "shared_room"
	AccommodationTypeHostelBed       = "hostel_bed"
	AccommodationTypeVilla           = "villa"

	AccommodationWarningLeadInOnly       = "lead_in_only"
	AccommodationWarningTaxStatusUnknown = "tax_status_unknown"
	AccommodationWarningPriceShock       = "price_shock_vs_lead_in"

	AccommodationEvidenceParserVersion = "accommodation-evidence-v1"

	BookingOrderAccommodationFirst      = "accommodation_first"
	BookingOrderFlightsFirstOK          = "flights_first_ok"
	BookingOrderNeedsRefundabilityCheck = "needs_refundability_check"
	BookingOrderNeedsPriceVerification  = "needs_price_verification"

	RoomInventoryMatchExact             = "exact_room_match"
	RoomInventoryMatchSimilar           = "similar_room_match"
	RoomInventoryMatchPropertyLevelOnly = "property_level_only"

	RoomInventoryCompletenessSingleProvider      = "single_provider"
	RoomInventoryCompletenessMultiProviderExact  = "multi_provider_exact"
	RoomInventoryCompletenessMultiProviderMixed  = "multi_provider_mixed"
	RoomInventoryCompletenessPropertyLevelOnly   = "property_level_only"
	RoomInventoryCompletenessNoProviderInventory = "no_provider_inventory"
)

// AccommodationNeed captures the room/apartment the user is actually asking
// for. Hotel/property search should be treated as candidate generation for this
// need, not as the final ranked offer surface.
type AccommodationNeed struct {
	Location                 string   `json:"location,omitempty"`
	CheckIn                  string   `json:"check_in,omitempty"`
	CheckOut                 string   `json:"check_out,omitempty"`
	Adults                   int      `json:"adults,omitempty"`
	ChildrenAges             []int    `json:"children_ages,omitempty"`
	Rooms                    int      `json:"rooms,omitempty"`
	AccommodationType        string   `json:"accommodation_type,omitempty"`
	MinBedrooms              int      `json:"min_bedrooms,omitempty"`
	MinBathrooms             int      `json:"min_bathrooms,omitempty"`
	MinBeds                  int      `json:"min_beds,omitempty"`
	MinStars                 int      `json:"min_stars,omitempty"`
	RequiredAmenities        []string `json:"required_amenities,omitempty"`
	PreferredAmenities       []string `json:"preferred_amenities,omitempty"`
	MaxDistanceKm            float64  `json:"max_distance_km,omitempty"`
	Neighborhoods            []string `json:"neighborhoods,omitempty"`
	MustHaveKitchen          bool     `json:"must_have_kitchen,omitempty"`
	MustHaveWifi             bool     `json:"must_have_wifi,omitempty"`
	MustHaveWorkspace        bool     `json:"must_have_workspace,omitempty"`
	BreakfastRequired        bool     `json:"breakfast_required,omitempty"`
	RefundableRequired       bool     `json:"refundable_required,omitempty"`
	FreeCancellationRequired bool     `json:"free_cancellation_required,omitempty"`
	Currency                 string   `json:"currency,omitempty"`
	MaxTotalPrice            float64  `json:"max_total_price,omitempty"`
}

// AccommodationOffer is a priced room/apartment offer matched against an
// AccommodationNeed. Final ranking should use this type rather than raw
// property-level lead-in prices.
type AccommodationOffer struct {
	PropertyName             string               `json:"property_name,omitempty"`
	PropertyID               string               `json:"property_id,omitempty"`
	OfferID                  string               `json:"offer_id,omitempty"`
	AccommodationType        string               `json:"accommodation_type,omitempty"`
	RoomName                 string               `json:"room_name,omitempty"`
	Provider                 string               `json:"provider,omitempty"`
	ProviderURL              string               `json:"provider_url,omitempty"`
	OccupancyAdults          int                  `json:"occupancy_adults,omitempty"`
	OccupancyChildren        []int                `json:"occupancy_children,omitempty"`
	Rooms                    int                  `json:"rooms,omitempty"`
	Bedrooms                 int                  `json:"bedrooms,omitempty"`
	Bathrooms                int                  `json:"bathrooms,omitempty"`
	Beds                     int                  `json:"beds,omitempty"`
	Amenities                []string             `json:"amenities,omitempty"`
	OccupancyMatched         bool                 `json:"occupancy_matched"`
	CriteriaMatched          bool                 `json:"criteria_matched"`
	BookingReadyStatus       bool                 `json:"booking_ready"`
	FinalTripCostReadyStatus bool                 `json:"final_trip_cost_ready"`
	MissingCriteria          []string             `json:"missing_criteria,omitempty"`
	UnknownCriteria          []string             `json:"unknown_criteria,omitempty"`
	NightlyPrice             float64              `json:"nightly_price,omitempty"`
	TotalPrice               float64              `json:"total_price,omitempty"`
	TaxesAndFees             float64              `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded        *bool                `json:"taxes_fees_included,omitempty"`
	Currency                 string               `json:"currency,omitempty"`
	PriceBasis               string               `json:"price_basis,omitempty"`
	PriceConfidence          string               `json:"price_confidence,omitempty"`
	CheckedAt                time.Time            `json:"checked_at,omitempty"`
	ExpiresAt                time.Time            `json:"expires_at,omitempty"`
	Freshness                string               `json:"freshness,omitempty"`
	CancellationPolicy       string               `json:"cancellation_policy,omitempty"`
	Refundable               *bool                `json:"refundable,omitempty"`
	FreeCancellation         *bool                `json:"free_cancellation,omitempty"`
	BookingOrderHint         string               `json:"booking_order_hint,omitempty"`
	Board                    string               `json:"board,omitempty"`
	BreakfastIncluded        *bool                `json:"breakfast_included,omitempty"`
	InventoryCompleteness    string               `json:"inventory_completeness,omitempty"`
	InventoryOptions         []RoomInventoryQuote `json:"inventory_options,omitempty"`
	Warnings                 []string             `json:"warnings,omitempty"`
}

// RoomInventoryQuote is a provider/OTA rate option for a specific canonical
// room/accommodation unit. It deliberately models "room + rate plan", not just
// "property + provider", because refundability, board, taxes, and occupancy can
// change the real booking choice for the same room.
type RoomInventoryQuote struct {
	Provider           string    `json:"provider,omitempty"`
	ProviderRoomName   string    `json:"provider_room_name,omitempty"`
	ProviderRateName   string    `json:"provider_rate_name,omitempty"`
	ProviderURL        string    `json:"provider_url,omitempty"`
	RateID             string    `json:"rate_id,omitempty"`
	MatchConfidence    string    `json:"match_confidence,omitempty"`
	NightlyPrice       float64   `json:"nightly_price,omitempty"`
	TotalPrice         float64   `json:"total_price,omitempty"`
	TaxesAndFees       float64   `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded  *bool     `json:"taxes_fees_included,omitempty"`
	Currency           string    `json:"currency,omitempty"`
	Refundable         *bool     `json:"refundable,omitempty"`
	FreeCancellation   *bool     `json:"free_cancellation,omitempty"`
	CancellationPolicy string    `json:"cancellation_policy,omitempty"`
	Board              string    `json:"board,omitempty"`
	BreakfastIncluded  *bool     `json:"breakfast_included,omitempty"`
	OccupancyAdults    int       `json:"occupancy_adults,omitempty"`
	OccupancyChildren  []int     `json:"occupancy_children,omitempty"`
	Rooms              int       `json:"rooms,omitempty"`
	PriceBasis         string    `json:"price_basis,omitempty"`
	PriceConfidence    string    `json:"price_confidence,omitempty"`
	CheckedAt          time.Time `json:"checked_at,omitempty"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
	Freshness          string    `json:"freshness,omitempty"`
	Warnings           []string  `json:"warnings,omitempty"`
}

// AccommodationEvidence records the machine-readable proof behind an
// accommodation recommendation or rejection. It is intentionally parallel to
// AccommodationOffer so clients can audit whether a shown price came from
// room-level verification, how fresh it is, and which criteria were unknown.
type AccommodationEvidence struct {
	EvidenceID           string            `json:"evidence_id,omitempty"`
	Provider             string            `json:"provider,omitempty"`
	Status               string            `json:"status,omitempty"`
	ParserVersion        string            `json:"parser_version,omitempty"`
	CheckedAt            time.Time         `json:"checked_at,omitempty"`
	ExpiresAt            time.Time         `json:"expires_at,omitempty"`
	TTLSeconds           int               `json:"ttl_seconds,omitempty"`
	Criteria             AccommodationNeed `json:"criteria"`
	PropertyName         string            `json:"property_name,omitempty"`
	PropertyID           string            `json:"property_id,omitempty"`
	OfferID              string            `json:"offer_id,omitempty"`
	RoomName             string            `json:"room_name,omitempty"`
	SourceURL            string            `json:"source_url,omitempty"`
	LeadInPrice          float64           `json:"lead_in_price,omitempty"`
	LeadInCurrency       string            `json:"lead_in_currency,omitempty"`
	VerifiedNightlyPrice float64           `json:"verified_nightly_price,omitempty"`
	VerifiedTotalPrice   float64           `json:"verified_total_price,omitempty"`
	TaxesAndFees         float64           `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded    *bool             `json:"taxes_fees_included,omitempty"`
	Currency             string            `json:"currency,omitempty"`
	PriceBasis           string            `json:"price_basis,omitempty"`
	PriceConfidence      string            `json:"price_confidence,omitempty"`
	PriceDelta           float64           `json:"price_delta,omitempty"`
	PriceDeltaPct        float64           `json:"price_delta_pct,omitempty"`
	CriteriaMatched      bool              `json:"criteria_matched"`
	OccupancyMatched     bool              `json:"occupancy_matched"`
	BookingReady         bool              `json:"booking_ready"`
	FinalTripCostReady   bool              `json:"final_trip_cost_ready"`
	MissingCriteria      []string          `json:"missing_criteria,omitempty"`
	UnknownCriteria      []string          `json:"unknown_criteria,omitempty"`
	Warnings             []string          `json:"warnings,omitempty"`
	DetailErrors         []string          `json:"detail_errors,omitempty"`
}

// EvaluateAccommodationOffer applies the user's accommodation need to an offer
// and fills the machine-readable match, warning, and booking-order fields.
func EvaluateAccommodationOffer(need AccommodationNeed, offer AccommodationOffer, now time.Time) AccommodationOffer {
	if now.IsZero() {
		now = time.Now()
	}
	need = normalizeAccommodationNeed(need)
	offer = normalizeAccommodationOffer(offer, now)

	var missing []string
	var unknown []string

	if need.AccommodationType != "" {
		switch {
		case offer.AccommodationType == "":
			unknown = appendUniqueString(unknown, "accommodation_type")
		case normalizeAccommodationType(offer.AccommodationType) != need.AccommodationType:
			missing = appendUniqueString(missing, "accommodation_type")
		}
	}

	occupancyMatched := true
	if need.Adults > 0 {
		switch {
		case offer.OccupancyAdults == 0:
			unknown = appendUniqueString(unknown, "occupancy")
			occupancyMatched = false
		case offer.OccupancyAdults < need.Adults:
			missing = appendUniqueString(missing, "occupancy")
			occupancyMatched = false
		}
	}
	if len(need.ChildrenAges) > 0 {
		if len(offer.OccupancyChildren) == 0 {
			unknown = appendUniqueString(unknown, "occupancy")
			occupancyMatched = false
		} else if len(offer.OccupancyChildren) < len(need.ChildrenAges) {
			missing = appendUniqueString(missing, "occupancy")
			occupancyMatched = false
		}
	}
	if need.Rooms > 0 && offer.Rooms > 0 && offer.Rooms < need.Rooms {
		missing = appendUniqueString(missing, "rooms")
	}
	if need.Rooms > 0 && offer.Rooms == 0 && needsWholeUnitEvidence(need.AccommodationType) {
		unknown = appendUniqueString(unknown, "rooms")
	}

	if need.MinBedrooms > 0 {
		if offer.Bedrooms == 0 {
			unknown = appendUniqueString(unknown, "bedrooms")
		} else if offer.Bedrooms < need.MinBedrooms {
			missing = appendUniqueString(missing, "bedrooms")
		}
	}
	if need.MinBathrooms > 0 {
		if offer.Bathrooms == 0 {
			unknown = appendUniqueString(unknown, "bathrooms")
		} else if offer.Bathrooms < need.MinBathrooms {
			missing = appendUniqueString(missing, "bathrooms")
		}
	}
	if need.MinBeds > 0 {
		if offer.Beds == 0 {
			unknown = appendUniqueString(unknown, "beds")
		} else if offer.Beds < need.MinBeds {
			missing = appendUniqueString(missing, "beds")
		}
	}

	for _, amenity := range requiredAccommodationAmenities(need) {
		if !offerHasAmenity(offer.Amenities, amenity) {
			missing = appendUniqueString(missing, "amenity:"+amenity)
		}
	}

	if need.BreakfastRequired {
		if offer.BreakfastIncluded == nil {
			unknown = appendUniqueString(unknown, "breakfast")
		} else if !*offer.BreakfastIncluded {
			missing = appendUniqueString(missing, "breakfast")
		}
	}
	if need.RefundableRequired {
		if !truthyBoolPtr(offer.Refundable) && !truthyBoolPtr(offer.FreeCancellation) {
			if offer.Refundable == nil && offer.FreeCancellation == nil {
				unknown = appendUniqueString(unknown, "refundability")
			} else {
				missing = appendUniqueString(missing, "refundability")
			}
		}
	}
	if need.FreeCancellationRequired {
		if offer.FreeCancellation == nil {
			unknown = appendUniqueString(unknown, "free_cancellation")
		} else if !*offer.FreeCancellation {
			missing = appendUniqueString(missing, "free_cancellation")
		}
	}
	if need.Currency != "" {
		switch {
		case offer.Currency == "":
			unknown = appendUniqueString(unknown, "currency")
		case !strings.EqualFold(offer.Currency, need.Currency):
			missing = appendUniqueString(missing, "currency")
		}
	}
	if need.MaxTotalPrice > 0 && offer.TotalPrice > 0 && offer.TotalPrice > need.MaxTotalPrice {
		missing = appendUniqueString(missing, "budget")
	}

	// Multi-provider-mixed means some sources gave room-level inventory and
	// others only property-level pricing, so the room match is not fully
	// trustworthy and must be surfaced as undetermined for honesty.
	if offer.InventoryCompleteness == RoomInventoryCompletenessPropertyLevelOnly ||
		offer.InventoryCompleteness == RoomInventoryCompletenessMultiProviderMixed ||
		offer.PriceBasis == PriceBasisLeadIn ||
		offer.PriceConfidence == PriceConfidenceUnverified {
		unknown = appendUniqueString(unknown, "room_inventory")
	}
	if offer.PriceBasis == PriceBasisLeadIn || offer.PriceConfidence == PriceConfidenceUnverified {
		offer.Warnings = appendUniqueString(offer.Warnings, AccommodationWarningLeadInOnly)
	}
	if offer.TotalPrice > 0 && offer.TaxesFeesIncluded == nil {
		offer.Warnings = appendUniqueString(offer.Warnings, AccommodationWarningTaxStatusUnknown)
	}

	offer.OccupancyMatched = occupancyMatched
	offer.MissingCriteria = missing
	offer.UnknownCriteria = unknown
	offer.CriteriaMatched = len(missing) == 0 && len(unknown) == 0 && occupancyMatched
	offer.BookingReadyStatus = offer.BookingReady()
	offer.FinalTripCostReadyStatus = offer.FinalTripCostReady()
	offer.BookingOrderHint = bookingOrderHint(need, offer)
	return offer
}

// BookingReady reports whether this offer can be shown as a real bookable
// accommodation option for the requested criteria.
func (o AccommodationOffer) BookingReady() bool {
	if !o.CriteriaMatched || !o.OccupancyMatched || o.Currency == "" {
		return false
	}
	if o.CheckedAt.IsZero() {
		return false
	}
	if comparableAccommodationPrice(o) <= 0 {
		return false
	}
	return o.PriceConfidence == PriceConfidenceRoomLevel || o.PriceConfidence == PriceConfidenceVerified
}

// FinalTripCostReady reports whether the offer can safely influence final trip
// totals. This is stricter than BookingReady because tax/fee status must be
// known.
func (o AccommodationOffer) FinalTripCostReady() bool {
	if !o.BookingReady() {
		return false
	}
	if o.TotalPrice <= 0 {
		return false
	}
	return o.TaxesFeesIncluded != nil
}

// HotelPriceEligibleForFinalTripCost rejects explicit property-level lead-in
// prices from final trip totals. Empty trust fields are allowed for older test
// fixtures, but production search paths should set PriceBasis/PriceConfidence
// via FinalizeHotelPriceTrust.
func HotelPriceEligibleForFinalTripCost(h HotelResult) bool {
	if h.Price <= 0 {
		return false
	}
	basis := strings.TrimSpace(h.PriceBasis)
	confidence := strings.TrimSpace(h.PriceConfidence)
	if basis == "" && confidence == "" {
		return true
	}
	if basis == PriceBasisLeadIn || confidence == PriceConfidenceUnverified {
		return false
	}
	if confidence != "" && confidence != PriceConfidenceRoomLevel && confidence != PriceConfidenceVerified {
		return false
	}
	switch basis {
	case "", PriceBasisRoomNightly, PriceBasisRoomTotal, PriceBasisTaxInclusiveTotal:
		return true
	default:
		return false
	}
}

func normalizeAccommodationNeed(need AccommodationNeed) AccommodationNeed {
	need.AccommodationType = normalizeAccommodationType(need.AccommodationType)
	need.Currency = strings.ToUpper(strings.TrimSpace(need.Currency))
	need.RequiredAmenities = normalizeAmenityList(need.RequiredAmenities)
	need.PreferredAmenities = normalizeAmenityList(need.PreferredAmenities)
	return need
}

func normalizeAccommodationOffer(offer AccommodationOffer, now time.Time) AccommodationOffer {
	offer.AccommodationType = normalizeAccommodationType(offer.AccommodationType)
	offer.Currency = strings.ToUpper(strings.TrimSpace(offer.Currency))
	offer.Amenities = normalizeAmenityList(offer.Amenities)
	if offer.PriceBasis == "" {
		offer.PriceBasis = PriceBasisLeadIn
	}
	if offer.PriceConfidence == "" {
		offer.PriceConfidence = PriceConfidenceUnverified
	}
	// Only stamp a check time when the price was actually verified this pass.
	// Stamping `now` for a lead-in / unverified price would fabricate freshness
	// the provider never gave us. Such offers already fail BookingReady on
	// confidence, so this changes only the (otherwise misleading) Freshness label.
	if offer.CheckedAt.IsZero() && comparableAccommodationPrice(offer) > 0 &&
		(offer.PriceConfidence == PriceConfidenceRoomLevel || offer.PriceConfidence == PriceConfidenceVerified) {
		offer.CheckedAt = now
	}
	if offer.Freshness == "" && !offer.CheckedAt.IsZero() {
		offer.Freshness = ClassifyFreshness(offer.Provider, offer.CheckedAt, now)
	}
	return offer
}

func normalizeAccommodationType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "apartment", "entire_home", "entire_apartment", "vacation_rental":
		return AccommodationTypeEntireApartment
	case "hotel", "hotel_room":
		return AccommodationTypeHotelRoom
	case "private", "private_room":
		return AccommodationTypePrivateRoom
	case "shared", "shared_room":
		return AccommodationTypeSharedRoom
	case "hostel", "hostel_bed":
		return AccommodationTypeHostelBed
	case "villa":
		return AccommodationTypeVilla
	default:
		return value
	}
}

func requiredAccommodationAmenities(need AccommodationNeed) []string {
	out := append([]string(nil), need.RequiredAmenities...)
	if need.MustHaveKitchen {
		out = appendUniqueString(out, "kitchen")
	}
	if need.MustHaveWifi {
		out = appendUniqueString(out, "wifi")
	}
	if need.MustHaveWorkspace {
		out = appendUniqueString(out, "workspace")
	}
	return normalizeAmenityList(out)
}

func offerHasAmenity(amenities []string, required string) bool {
	required = normalizeAmenity(required)
	for _, amenity := range amenities {
		if amenity == required || strings.Contains(amenity, required) || strings.Contains(required, amenity) {
			return true
		}
	}
	return false
}

func normalizeAmenityList(values []string) []string {
	var out []string
	for _, value := range values {
		value = normalizeAmenity(value)
		if value == "" {
			continue
		}
		out = appendUniqueString(out, value)
	}
	return out
}

func normalizeAmenity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.Join(strings.Fields(value), " ")
	switch value {
	case "free wifi", "wi fi", "wireless internet":
		return "wifi"
	case "washer", "washing machine":
		return "washing machine"
	case "work desk", "dedicated workspace":
		return "workspace"
	default:
		return value
	}
}

func bookingOrderHint(need AccommodationNeed, offer AccommodationOffer) string {
	if !offer.BookingReady() {
		if containsCriterion(offer.UnknownCriteria, "refundability") || containsCriterion(offer.UnknownCriteria, "free_cancellation") {
			return BookingOrderNeedsRefundabilityCheck
		}
		return BookingOrderNeedsPriceVerification
	}
	if need.RefundableRequired || need.FreeCancellationRequired {
		if truthyBoolPtr(offer.Refundable) || truthyBoolPtr(offer.FreeCancellation) {
			return BookingOrderFlightsFirstOK
		}
		if offer.Refundable == nil && offer.FreeCancellation == nil {
			return BookingOrderNeedsRefundabilityCheck
		}
		return BookingOrderAccommodationFirst
	}
	return BookingOrderAccommodationFirst
}

func comparableAccommodationPrice(offer AccommodationOffer) float64 {
	if offer.TotalPrice > 0 {
		return offer.TotalPrice
	}
	if offer.NightlyPrice > 0 {
		return offer.NightlyPrice
	}
	return 0
}

func needsWholeUnitEvidence(accommodationType string) bool {
	switch accommodationType {
	case AccommodationTypeEntireApartment, AccommodationTypeVilla:
		return true
	default:
		return false
	}
}

func truthyBoolPtr(value *bool) bool {
	return value != nil && *value
}

func containsCriterion(values []string, criterion string) bool {
	for _, value := range values {
		if value == criterion {
			return true
		}
	}
	return false
}
