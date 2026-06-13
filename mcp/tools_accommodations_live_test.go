package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestHandleSearchAccommodationsLiveNoKeyProbe(t *testing.T) {
	testutil.RequireLiveProbe(t)
	t.Setenv("SERPAPI_KEY", "")
	registry, err := providers.NewRegistry()
	if err != nil {
		t.Fatalf("load provider registry for live probe: %v", err)
	}
	hotels.SetExternalProviderRuntime(providers.NewRuntime(registry))
	t.Cleanup(func() {
		hotels.SetExternalProviderRuntime(nil)
	})

	checkIn := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	checkOut := time.Now().AddDate(0, 1, 2).Format("2006-01-02")
	args := map[string]any{
		"location":                   "Paris",
		"check_in":                   checkIn,
		"check_out":                  checkOut,
		"adults":                     2,
		"children_ages":              []any{float64(7)},
		"rooms":                      1,
		"currency":                   "EUR",
		"accommodation_type":         "entire_apartment",
		"amenities":                  "kitchen,wifi",
		"must_have_kitchen":          true,
		"must_have_wifi":             true,
		"must_have_washing_machine":  true,
		"free_cancellation_required": true,
		"max_candidates":             2,
		"max_offers":                 6,
		"include_unmatched":          true,
		"include_candidates":         true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	content, structured, err := handleSearchAccommodations(ctx, args, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleSearchAccommodations live no-key probe: %v", err)
	}
	resp, ok := structured.(accommodationSearchResponse)
	if !ok {
		t.Fatalf("structured type = %T, want accommodationSearchResponse", structured)
	}
	for _, offer := range resp.Offers {
		if !offer.CriteriaMatched {
			t.Fatalf("matched offers list contained non-matching offer: %#v", offer)
		}
		if offer.PriceBasis == models.PriceBasisLeadIn ||
			offer.PriceConfidence == models.PriceConfidenceUnverified ||
			offer.InventoryCompleteness == models.RoomInventoryCompletenessPropertyLevelOnly {
			t.Fatalf("matched offers list contained lead-in/property-level offer: %#v", offer)
		}
	}

	t.Logf("LIVE_NO_KEY_ACCOMMODATION_PROBE=%s", marshalAccommodationProbeSummary(t, newAccommodationProbeSummary(args, resp)))
	if len(content) > 0 {
		t.Logf("LIVE_NO_KEY_ACCOMMODATION_TEXT=%s", content[0].Text)
	}
}

type accommodationProbeSummary struct {
	Case                    string                        `json:"case"`
	CheckIn                 string                        `json:"check_in"`
	CheckOut                string                        `json:"check_out"`
	Success                 bool                          `json:"success"`
	Count                   int                           `json:"count"`
	MatchingCount           int                           `json:"matching_count"`
	BookingReadyCount       int                           `json:"booking_ready_count"`
	FinalTripCostReadyCount int                           `json:"final_trip_cost_ready_count"`
	CandidateCount          int                           `json:"candidate_count"`
	TotalAvailable          int                           `json:"total_available,omitempty"`
	Offers                  []accommodationProbeOffer     `json:"offers,omitempty"`
	RejectedOffers          []accommodationProbeOffer     `json:"rejected_offers,omitempty"`
	Candidates              []accommodationProbeCandidate `json:"candidates,omitempty"`
	ProviderStatuses        []accommodationProbeProvider  `json:"provider_statuses,omitempty"`
	Warnings                []string                      `json:"warnings,omitempty"`
	Error                   string                        `json:"error,omitempty"`
}

type accommodationProbeOffer struct {
	PropertyName          string   `json:"property_name,omitempty"`
	RoomName              string   `json:"room_name,omitempty"`
	Provider              string   `json:"provider,omitempty"`
	TotalPrice            float64  `json:"total_price,omitempty"`
	NightlyPrice          float64  `json:"nightly_price,omitempty"`
	Currency              string   `json:"currency,omitempty"`
	PriceBasis            string   `json:"price_basis,omitempty"`
	PriceConfidence       string   `json:"price_confidence,omitempty"`
	InventoryCompleteness string   `json:"inventory_completeness,omitempty"`
	InventoryOptionCount  int      `json:"inventory_option_count,omitempty"`
	CriteriaMatched       bool     `json:"criteria_matched"`
	BookingReady          bool     `json:"booking_ready"`
	FinalTripCostReady    bool     `json:"final_trip_cost_ready"`
	MissingCriteria       []string `json:"missing_criteria,omitempty"`
	UnknownCriteria       []string `json:"unknown_criteria,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

type accommodationProbeCandidate struct {
	Name                   string   `json:"name,omitempty"`
	LeadInPrice            float64  `json:"lead_in_price,omitempty"`
	Currency               string   `json:"currency,omitempty"`
	PriceBasis             string   `json:"price_basis,omitempty"`
	PriceConfidence        string   `json:"price_confidence,omitempty"`
	OfferCount             int      `json:"offer_count"`
	MatchingOfferCount     int      `json:"matching_offer_count"`
	BookingReadyOfferCount int      `json:"booking_ready_offer_count"`
	DetailErrors           []string `json:"detail_errors,omitempty"`
}

type accommodationProbeProvider struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
	Results int    `json:"results,omitempty"`
	Error   string `json:"error,omitempty"`
	FixHint string `json:"fix_hint,omitempty"`
}

func newAccommodationProbeSummary(args map[string]any, resp accommodationSearchResponse) accommodationProbeSummary {
	return accommodationProbeSummary{
		Case:                    "paris_family_entire_apartment_kitchen_wifi_washer_free_cancel_no_serpapi",
		CheckIn:                 argString(args, "check_in"),
		CheckOut:                argString(args, "check_out"),
		Success:                 resp.Success,
		Count:                   resp.Count,
		MatchingCount:           resp.MatchingCount,
		BookingReadyCount:       resp.BookingReadyCount,
		FinalTripCostReadyCount: resp.FinalTripCostReadyCount,
		CandidateCount:          resp.CandidateCount,
		TotalAvailable:          resp.TotalAvailable,
		Offers:                  summarizeAccommodationProbeOffers(resp.Offers, 6),
		RejectedOffers:          summarizeAccommodationProbeOffers(resp.RejectedOffers, 6),
		Candidates:              summarizeAccommodationProbeCandidates(resp.Candidates, 4),
		ProviderStatuses:        summarizeAccommodationProbeProviders(resp.ProviderStatuses),
		Warnings:                append([]string(nil), resp.Warnings...),
		Error:                   resp.Error,
	}
}

func summarizeAccommodationProbeOffers(offers []models.AccommodationOffer, limit int) []accommodationProbeOffer {
	if len(offers) > limit {
		offers = offers[:limit]
	}
	out := make([]accommodationProbeOffer, 0, len(offers))
	for _, offer := range offers {
		out = append(out, accommodationProbeOffer{
			PropertyName:          offer.PropertyName,
			RoomName:              offer.RoomName,
			Provider:              offer.Provider,
			TotalPrice:            offer.TotalPrice,
			NightlyPrice:          offer.NightlyPrice,
			Currency:              offer.Currency,
			PriceBasis:            offer.PriceBasis,
			PriceConfidence:       offer.PriceConfidence,
			InventoryCompleteness: offer.InventoryCompleteness,
			InventoryOptionCount:  len(offer.InventoryOptions),
			CriteriaMatched:       offer.CriteriaMatched,
			BookingReady:          offer.BookingReadyStatus,
			FinalTripCostReady:    offer.FinalTripCostReadyStatus,
			MissingCriteria:       append([]string(nil), offer.MissingCriteria...),
			UnknownCriteria:       append([]string(nil), offer.UnknownCriteria...),
			Warnings:              append([]string(nil), offer.Warnings...),
		})
	}
	return out
}

func summarizeAccommodationProbeCandidates(candidates []accommodationCandidate, limit int) []accommodationProbeCandidate {
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]accommodationProbeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, accommodationProbeCandidate{
			Name:                   candidate.Name,
			LeadInPrice:            candidate.LeadInPrice,
			Currency:               candidate.Currency,
			PriceBasis:             candidate.PriceBasis,
			PriceConfidence:        candidate.PriceConfidence,
			OfferCount:             candidate.OfferCount,
			MatchingOfferCount:     candidate.MatchingOfferCount,
			BookingReadyOfferCount: candidate.BookingReadyOfferCount,
			DetailErrors:           accommodationDetailErrorStrings(candidate.DetailErrors),
		})
	}
	return out
}

func summarizeAccommodationProbeProviders(statuses []models.ProviderStatus) []accommodationProbeProvider {
	out := make([]accommodationProbeProvider, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, accommodationProbeProvider{
			ID:      status.ID,
			Name:    status.Name,
			Status:  status.Status,
			Results: status.Results,
			Error:   status.Error,
			FixHint: status.FixHint,
		})
	}
	return out
}

func marshalAccommodationProbeSummary(t *testing.T, summary accommodationProbeSummary) string {
	t.Helper()
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("marshal accommodation probe summary: %v", err)
	}
	return string(data)
}
