// Package imports normalizes external reservation records into trip workspace
// artifacts. It deliberately does not read mailboxes or calendars directly;
// callers pass already-approved text or profile bookings.
package imports

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/profile"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

type ReservationArtifacts struct {
	Records []trips.ImportedRecord `json:"records"`
	Legs    []trips.TripLeg        `json:"legs"`
	Actions []trips.ActionItem     `json:"actions"`
}

func ParseReservationText(subject, body, source string) (ReservationArtifacts, error) {
	if strings.TrimSpace(subject) == "" && strings.TrimSpace(body) == "" {
		return ReservationArtifacts{}, fmt.Errorf("reservation text is empty")
	}
	if source == "" {
		source = "text"
	}
	if b := profile.ParseFlightConfirmation(subject, body); b != nil {
		return FromProfileBooking(*b, source, rawHash(subject, body)), nil
	}
	if b := profile.ParseHotelConfirmation(subject, body); b != nil {
		return FromProfileBooking(*b, source, rawHash(subject, body)), nil
	}
	if b := profile.ParseGroundConfirmation(subject, body); b != nil {
		return FromProfileBooking(*b, source, rawHash(subject, body)), nil
	}
	return ReservationArtifacts{}, fmt.Errorf("no supported reservation found")
}

func FromProfileBooking(b profile.Booking, source, rawHash string) ReservationArtifacts {
	if source == "" {
		source = b.Source
	}
	now := time.Now()
	rec := trips.ImportedRecord{
		Type:       normalizedType(b.Type),
		Provider:   b.Provider,
		Reference:  b.Reference,
		Source:     source,
		RawHash:    rawHash,
		ImportedAt: now,
		TravelDate: b.TravelDate,
		From:       b.From,
		To:         b.To,
		Price:      b.Price,
		Currency:   b.Currency,
		Notes:      b.Notes,
	}
	rec.ID = trips.ImportedRecordID(rec)

	leg := trips.TripLeg{
		Type:      rec.Type,
		From:      b.From,
		To:        b.To,
		Provider:  b.Provider,
		StartTime: b.TravelDate,
		Price:     b.Price,
		Currency:  b.Currency,
		Confirmed: true,
		Reference: b.Reference,
	}
	if rec.Type == "hotel" || rec.Type == "airbnb" {
		leg.From = b.To
		leg.To = b.Provider
		leg.EndTime = checkoutDate(b.TravelDate, b.Nights)
	}
	if rec.Type == "ground" && leg.From == "" {
		leg.From = "unknown"
	}
	if leg.To == "" {
		leg.To = b.Provider
	}

	actionTitle := "Re-check imported booking"
	if b.Provider != "" {
		actionTitle = "Re-check imported " + b.Provider + " booking"
	}
	action := trips.ActionItem{
		Type:      "verification",
		Title:     actionTitle,
		Status:    "open",
		RelatedID: rec.ID,
		Notes:     "Imported confirmations can contain stale prices or changed schedule data; verify before booking follow-up travel.",
	}

	return ReservationArtifacts{
		Records: []trips.ImportedRecord{rec},
		Legs:    []trips.TripLeg{leg},
		Actions: []trips.ActionItem{action},
	}
}

func normalizedType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "airbnb":
		return "airbnb"
	case "hotel":
		return "hotel"
	case "ground", "train", "bus", "ferry":
		return "ground"
	default:
		if t == "" {
			return "other"
		}
		return strings.ToLower(t)
	}
}

func checkoutDate(checkin string, nights int) string {
	if checkin == "" || nights <= 0 {
		return ""
	}
	t, err := time.Parse("2006-01-02", checkin)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, nights).Format("2006-01-02")
}

func rawHash(parts ...string) string {
	h := sha1.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
