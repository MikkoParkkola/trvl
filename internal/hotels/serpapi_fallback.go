package hotels

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
)

const serpapiFallbackNotice = "verified via SerpAPI Google Hotels property details after Google returned no booking partner prices"

var (
	serpapiAPIKeyFunc                 = serpapi.APIKey
	serpapiResolveGoogleMapsPlaceFunc = serpapi.ResolveGoogleMapsPlace
	serpapiSearchHotelsFunc           = serpapi.SearchHotelsWithOptions
	serpapiGetPropertyDetailsFunc     = serpapi.GetPropertyDetails
)

func trySerpAPIPriceFallback(ctx context.Context, opts HotelPriceOpts) *models.HotelPriceResult {
	if strings.TrimSpace(serpapiAPIKeyFunc()) == "" {
		return nil
	}

	query, place := serpapiFallbackQuery(ctx, opts.HotelID, opts.Location)
	if query == "" {
		return nil
	}

	searchOpts := serpapiFallbackSearchOptions(
		query,
		opts.CheckIn,
		opts.CheckOut,
		opts.Currency,
		2,
		nil,
	)
	result, err := serpapiSearchHotelsFunc(ctx, searchOpts)
	if err != nil || result == nil {
		return nil
	}

	hotel := findSerpAPIHotel(result, place, opts.Location, opts.HotelID)
	if hotel == nil {
		return nil
	}
	hotel = selectedSerpAPIPropertyDetails(ctx, hotel, searchOpts)
	providers := providerPricesFromSerpAPIHotel(hotel, opts.Currency)
	if len(providers) == 0 {
		return nil
	}

	name := hotel.Name
	if name == "" && place != nil {
		name = place.Title
	}
	return &models.HotelPriceResult{
		Success:   true,
		HotelID:   opts.HotelID,
		Name:      name,
		CheckIn:   opts.CheckIn,
		CheckOut:  opts.CheckOut,
		Providers: providers,
		Notice:    serpapiFallbackNotice,
	}
}

func trySerpAPIRoomFallback(ctx context.Context, opts RoomSearchOptions) ([]RoomType, string, string) {
	if strings.TrimSpace(serpapiAPIKeyFunc()) == "" {
		return nil, "", ""
	}

	query, place := serpapiFallbackQuery(ctx, opts.HotelID, opts.Location)
	if query == "" {
		return nil, "", ""
	}

	guests := opts.Guests
	if guests <= 0 {
		guests = 2
	}
	searchOpts := serpapiFallbackSearchOptions(
		query,
		opts.CheckIn,
		opts.CheckOut,
		opts.Currency,
		guests,
		opts.ChildrenAges,
	)
	result, err := serpapiSearchHotelsFunc(ctx, searchOpts)
	if err != nil || result == nil {
		return nil, "", ""
	}

	hotel := findSerpAPIHotel(result, place, opts.Location, opts.HotelID)
	if hotel == nil {
		return nil, "", ""
	}
	hotel = selectedSerpAPIPropertyDetails(ctx, hotel, searchOpts)
	rooms := roomTypesFromSerpAPIHotel(hotel, opts.Currency)
	if len(rooms) == 0 {
		return nil, "", ""
	}

	name := hotel.Name
	if name == "" && place != nil {
		name = place.Title
	}
	return rooms, name, serpapiFallbackNotice
}

func serpapiFallbackQuery(ctx context.Context, hotelID, location string) (string, *serpapi.MapsPlace) {
	location = strings.TrimSpace(location)
	place, err := serpapiResolveGoogleMapsPlaceFunc(ctx, hotelID)
	if err == nil && place != nil {
		if query := mapsPlaceQuery(place, location); query != "" {
			return query, place
		}
	}
	return location, nil
}

func mapsPlaceQuery(place *serpapi.MapsPlace, fallback string) string {
	if place == nil {
		return strings.TrimSpace(fallback)
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	if locality := mapsPlaceLocality(place.Address); locality != "" {
		return locality
	}
	title := strings.TrimSpace(place.Title)
	if title != "" {
		return title
	}
	return ""
}

func mapsPlaceLocality(address string) string {
	parts := strings.Split(address, ",")
	for i := len(parts) - 2; i >= 0; i-- {
		if locality := cleanAddressLocality(parts[i]); locality != "" {
			return locality
		}
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if locality := cleanAddressLocality(parts[i]); locality != "" {
			return locality
		}
	}
	return ""
}

func cleanAddressLocality(part string) string {
	fields := strings.Fields(strings.TrimSpace(part))
	if len(fields) == 0 {
		return ""
	}
	keep := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, " .;:-")
		if field == "" || containsDigit(field) || shortAllUpper(field) {
			continue
		}
		keep = append(keep, field)
	}
	return strings.Join(keep, " ")
}

func containsDigit(value string) bool {
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func shortAllUpper(value string) bool {
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > 3 {
		return false
	}
	hasLetter := false
	for _, r := range runes {
		if !unicode.IsLetter(r) {
			return false
		}
		hasLetter = true
		if unicode.ToUpper(r) != r {
			return false
		}
	}
	return hasLetter
}

func serpapiFallbackSearchOptions(query, checkIn, checkOut, currency string, adults int, childrenAges []int) serpapi.SearchOptions {
	if adults <= 0 {
		adults = 2
	}
	if currency == "" {
		currency = "USD"
	}
	return serpapi.SearchOptions{
		Query:        query,
		CheckIn:      checkIn,
		CheckOut:     checkOut,
		Currency:     currency,
		Adults:       adults,
		ChildrenAges: append([]int(nil), childrenAges...),
		GL:           "us",
		HL:           "en",
	}
}

func selectedSerpAPIPropertyDetails(ctx context.Context, hotel *serpapi.Hotel, opts serpapi.SearchOptions) *serpapi.Hotel {
	if hotel == nil || len(hotel.ProviderOptions()) > 0 || strings.TrimSpace(hotel.PropertyToken) == "" {
		return hotel
	}
	detail, err := serpapiGetPropertyDetailsFunc(ctx, opts, hotel.PropertyToken)
	if err != nil || detail == nil {
		return hotel
	}
	if detail.Name == "" {
		detail.Name = hotel.Name
	}
	if detail.PropertyToken == "" {
		detail.PropertyToken = hotel.PropertyToken
	}
	if detail.Link == "" {
		detail.Link = hotel.Link
	}
	if detail.RatePerNight.Extracted <= 0 {
		detail.RatePerNight = hotel.RatePerNight
	}
	if detail.TotalRate.Extracted <= 0 {
		detail.TotalRate = hotel.TotalRate
	}
	if detail.PriceVerification == nil {
		detail.PriceVerification = hotel.PriceVerification
	}
	return detail
}

func findSerpAPIHotel(result *serpapi.Response, place *serpapi.MapsPlace, nameHints ...string) *serpapi.Hotel {
	if result == nil {
		return nil
	}
	hotels := make([]serpapi.Hotel, 0, len(result.Properties)+len(result.Ads))
	hotels = append(hotels, result.Properties...)
	hotels = append(hotels, result.Ads...)
	if len(hotels) == 0 {
		return nil
	}

	if place != nil && place.Title != "" {
		for i := range hotels {
			if sameSerpAPIHotelName(hotels[i].Name, place.Title) {
				return &hotels[i]
			}
		}
		return nil
	}

	// No resolved Google Maps place: only return a result that name-matches the
	// requested property. When the caller asked for a specific hotel that does
	// not match any result, we must NOT fall through to an arbitrary priced
	// hotel — that would label a *different* property's prices as verified.
	// (Reported by RobertoReale: querying "Hotel Villa Maria" with SerpAPI
	// active returned "Miramare Sea Resort & Spa" at verified confidence.) The
	// safe outcome is no match, which surfaces upstream as providers: null.
	requestedProperty := false
	for _, hint := range nameHints {
		hint = strings.TrimSpace(hint)
		if hint == "" || !locationHintLooksLikePropertyName(hint) {
			continue
		}
		requestedProperty = true
		for i := range hotels {
			if sameSerpAPIHotelName(hotels[i].Name, hint) {
				return &hotels[i]
			}
		}
	}
	if requestedProperty {
		return nil
	}

	// No specific-property intent (pure locality/area search): a single priced
	// lead is acceptable.
	for i := range hotels {
		if len(hotels[i].ProviderOptions()) > 0 || hotels[i].TotalPrice() > 0 {
			return &hotels[i]
		}
	}
	return nil
}

func providerPricesFromSerpAPIHotel(hotel *serpapi.Hotel, currency string) []models.ProviderPrice {
	if hotel == nil {
		return nil
	}
	currency = serpapiCurrency(hotel, currency)
	var providers []models.ProviderPrice
	for _, option := range hotel.ProviderOptions() {
		price := option.TotalRate.Extracted
		if price <= 0 {
			price = option.RatePerNight.Extracted
		}
		if price <= 0 {
			continue
		}
		basis := models.PriceBasisTaxInclusiveTotal
		if option.TotalRate.Extracted <= 0 {
			basis = models.PriceBasisRoomNightly
		}
		// #171: when the shown total equals the pre-tax figure, taxes/fees are
		// added at checkout and this price will grow. Only flag when both
		// numbers are present.
		taxAtCheckout := option.TotalRate.Extracted > 0 &&
			option.TotalRate.BeforeFeesExtracted > 0 &&
			pricesEqual(option.TotalRate.Extracted, option.TotalRate.BeforeFeesExtracted)
		providers = append(providers, models.ProviderPrice{
			Provider:           serpapiProviderName(option.Source),
			Price:              price,
			NightlyPrice:       option.RatePerNight.Extracted,
			TotalPrice:         option.TotalRate.Extracted,
			Currency:           currency,
			ProviderURL:        option.Link,
			PriceBasis:         basis,
			PriceConfidence:    models.PriceConfidenceVerified,
			TaxAddedAtCheckout: taxAtCheckout,
		})
	}
	return dedupeAndSortProviderPrices(providers)
}

func roomTypesFromSerpAPIHotel(hotel *serpapi.Hotel, currency string) []RoomType {
	if hotel == nil {
		return nil
	}
	currency = serpapiCurrency(hotel, currency)
	var rooms []RoomType
	for _, offer := range hotel.ProviderOptions() {
		if len(offer.Rooms) == 0 {
			if room, ok := roomTypeFromSerpAPIOffer(offer, nil, currency); ok {
				rooms = append(rooms, room)
			}
			continue
		}
		for i := range offer.Rooms {
			roomOption := offer.Rooms[i]
			if len(roomOption.Rates) == 0 {
				if room, ok := roomTypeFromSerpAPIOffer(offer, &roomOption, currency); ok {
					rooms = append(rooms, room)
				}
				continue
			}
			for _, rate := range roomOption.Rates {
				if rate.Source == "" {
					rate.Source = offer.Source
				}
				if rate.FreeCancellationUntilDate == "" {
					rate.FreeCancellationUntilDate = offer.FreeCancellationUntilDate
				}
				if rate.FreeCancellationUntilTime == "" {
					rate.FreeCancellationUntilTime = offer.FreeCancellationUntilTime
				}
				if !rate.FreeCancellation {
					rate.FreeCancellation = offer.FreeCancellation
				}
				if rate.Benefits == "" {
					rate.Benefits = offer.Benefits
				}
				if room, ok := roomTypeFromSerpAPIOffer(rate, &roomOption, currency); ok {
					rooms = append(rooms, room)
				}
			}
		}
	}
	if len(rooms) == 0 && hotel.TotalPrice() > 0 {
		rooms = append(rooms, RoomType{
			Name:            "Standard Room",
			Price:           hotel.TotalPrice(),
			TotalPrice:      hotel.TotalPrice(),
			Currency:        currency,
			Provider:        serpapiProviderName(serpapiVerifiedSource(hotel)),
			MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
		})
	}
	return dedupeAndSortRoomTypes(rooms)
}

func dedupeAndSortProviderPrices(providers []models.ProviderPrice) []models.ProviderPrice {
	if len(providers) == 0 {
		return providers
	}
	seen := make(map[string]bool, len(providers))
	deduped := make([]models.ProviderPrice, 0, len(providers))
	for _, provider := range providers {
		key := provider.Provider + "|" + provider.Currency + "|" + formatPriceKey(provider.Price)
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, provider)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Price == deduped[j].Price {
			return deduped[i].Provider < deduped[j].Provider
		}
		return deduped[i].Price < deduped[j].Price
	})
	return deduped
}

func dedupeAndSortRoomTypes(rooms []RoomType) []RoomType {
	if len(rooms) == 0 {
		return rooms
	}
	seen := make(map[string]bool, len(rooms))
	deduped := make([]RoomType, 0, len(rooms))
	for _, room := range rooms {
		key := room.Provider + "|" + room.Name + "|" + room.Currency + "|" + formatPriceKey(room.Price) + "|" + room.CancellationPolicy
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, room)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Price == deduped[j].Price {
			if deduped[i].Name == deduped[j].Name {
				return deduped[i].Provider < deduped[j].Provider
			}
			return deduped[i].Name < deduped[j].Name
		}
		return deduped[i].Price < deduped[j].Price
	})
	return deduped
}

func formatPriceKey(price float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(price, 'f', 2, 64), "0"), ".")
}

func roomTypeFromSerpAPIOffer(offer serpapi.PriceOption, room *serpapi.RoomOption, currency string) (RoomType, bool) {
	total := offer.TotalRate.Extracted
	nightly := offer.RatePerNight.Extracted
	name := "Standard Room"
	maxGuests := 0
	providerURL := offer.Link
	matchConfidence := models.RoomInventoryMatchPropertyLevelOnly
	if room != nil {
		if room.Name != "" {
			name = room.Name
		}
		if room.Link != "" {
			providerURL = room.Link
		}
		if room.TotalRate.Extracted > 0 {
			total = room.TotalRate.Extracted
		}
		if room.RatePerNight.Extracted > 0 {
			nightly = room.RatePerNight.Extracted
		}
		maxGuests = room.NumGuests
		matchConfidence = models.RoomInventoryMatchExact
	}
	price := total
	if price <= 0 {
		price = nightly
	}
	if price <= 0 {
		return RoomType{}, false
	}

	result := RoomType{
		Name:            name,
		Price:           price,
		NightlyPrice:    nightly,
		TotalPrice:      total,
		Currency:        currency,
		Provider:        serpapiProviderName(offer.Source),
		ProviderURL:     providerURL,
		RatePlanName:    offer.Benefits,
		MatchConfidence: matchConfidence,
		MaxGuests:       maxGuests,
	}
	if offer.FreeCancellation {
		result.FreeCancellation = boolPtr(true)
		result.Refundable = boolPtr(true)
		result.CancellationPolicy = freeCancellationPolicy(offer)
	}
	if strings.Contains(strings.ToLower(offer.Benefits), "breakfast") {
		result.BreakfastIncluded = boolPtr(true)
		result.Board = "breakfast included"
	}
	return result, true
}

func freeCancellationPolicy(offer serpapi.PriceOption) string {
	if offer.FreeCancellationUntilDate == "" {
		return "Free cancellation"
	}
	policy := "Free cancellation until " + offer.FreeCancellationUntilDate
	if offer.FreeCancellationUntilTime != "" {
		policy += " " + offer.FreeCancellationUntilTime
	}
	return policy
}

func serpapiProviderName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "Google Hotels"
	}
	return source
}

func serpapiVerifiedSource(hotel *serpapi.Hotel) string {
	if hotel != nil && hotel.PriceVerification != nil && hotel.PriceVerification.Source != "" {
		return hotel.PriceVerification.Source
	}
	return "Google Hotels"
}

func serpapiCurrency(hotel *serpapi.Hotel, fallback string) string {
	if fallback != "" {
		return fallback
	}
	if hotel != nil && hotel.PriceVerification != nil && hotel.PriceVerification.Currency != "" {
		return hotel.PriceVerification.Currency
	}
	return "USD"
}

func boolPtr(value bool) *bool {
	return &value
}

func sameSerpAPIHotelName(a, b string) bool {
	na := normalizeSerpAPIHotelName(a)
	nb := normalizeSerpAPIHotelName(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	na = strings.TrimPrefix(na, "hotel ")
	nb = strings.TrimPrefix(nb, "hotel ")
	return na == nb || strings.Contains(na, nb) || strings.Contains(nb, na)
}

func normalizeSerpAPIHotelName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '-' || r == '_' || r == '\'' || r == 8217:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
