package mcp

import (
	"fmt"
	"slices"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// fitHotelDetailsReply sends the search_hotels_with_details reply within the
// 50 KB reply budget (see fitReply).
func fitHotelDetailsReply(req hotelSearchRequest, result *models.HotelSearchResult, need models.AccommodationNeed, hotelsWithDetails []hotelWithDetails) ([]ContentBlock, interface{}, error) {
	totalRooms := 0
	for _, h := range hotelsWithDetails {
		totalRooms += len(h.RoomTypes)
	}
	newResp := func(shown []hotelWithDetails) hotelDetailsSearchResponse {
		return hotelDetailsSearchResponse{
			Success:          result.Success,
			Count:            len(shown),
			TotalAvailable:   result.Count,
			Hotels:           shown,
			ProviderStatuses: result.ProviderStatuses,
			Completeness:     result.Completeness,
			Suggestions:      hotelSuggestions(result, req.Options),
			MaxDistanceKm:    result.MaxDistanceKm,
			FartherOmitted:   result.FartherOmitted,
			MonthlyOmitted:   result.MonthlyOmitted,
			BudgetOmitted:    len(hotelsWithDetails) - len(shown),
			Error:            result.Error,
		}
	}
	// The summary describes the full enrichment, failed lookups included,
	// even when the budget cuts hotels or rooms; the note says what is shown.
	summary := hotelDetailsSummary(newResp(hotelsWithDetails), req.Location)
	cut := detailsDropStep(roomCutter(req.Options.Currency, stayNights(req.CheckIn, req.CheckOut), nil))
	return fitReply(hotelsWithDetails, cut, func(shown []hotelWithDetails, trimmed bool) (string, any) {
		// Offers and verified_rate are rebuilt from the rooms kept, so
		// room_types, accommodation_offers and verified_rate always name the
		// same rooms. shown is this probe's own copy.
		for i := range shown {
			if h := &shown[i]; h.RoomsOmitted > 0 {
				h.AccommodationOffers = accommodationOffersFromRooms(h.HotelResult, h.RoomTypes, need, h.checkedAt)
				h.VerifiedRate = verifiedRateFromRooms(h.HotelResult, h.RoomTypes, h.checkedAt)
			}
		}
		resp := newResp(shown)
		if !trimmed {
			return summary, resp
		}
		shownRooms := 0
		for _, h := range shown {
			shownRooms += len(h.RoomTypes)
		}
		return summary + budgetNote(shownRooms, totalRooms, fmt.Sprintf("rooms (%d of %d hotels)", len(shown), len(hotelsWithDetails)),
			"a lower max_hotels, or hotel_rooms for one hotel"), resp
	})
}

// detailsDropStep is the budget drop step for search_hotels_with_details.
// Rooms go before hotels, since comparing the shortlisted hotels is the point
// of the tool: the priciest room (by cutRoom) of the hotel listing the most,
// then hotels from the tail. Floor: one hotel with one room.
func detailsDropStep(cutRoom func([]hotels.RoomType) ([]hotels.RoomType, bool)) func([]hotelWithDetails) ([]hotelWithDetails, bool) {
	return func(rows []hotelWithDetails) ([]hotelWithDetails, bool) {
		most := -1
		for i := range rows {
			if len(rows[i].RoomTypes) > 1 && (most < 0 || len(rows[i].RoomTypes) > len(rows[most].RoomTypes)) {
				most = i
			}
		}
		switch {
		case most >= 0:
			h := &rows[most]
			rooms, _ := cutRoom(slices.Clone(h.RoomTypes))
			h.RoomsOmitted += len(h.RoomTypes) - len(rooms)
			h.RoomTypes = rooms
			return rows, true
		case len(rows) > 1:
			return rows[:len(rows)-1], true
		}
		return rows, false
	}
}
