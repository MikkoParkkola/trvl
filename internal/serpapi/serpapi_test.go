package serpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchHotels_HTTP200(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "test_key" {
			t.Errorf("api_key = %q, want test_key", r.URL.Query().Get("api_key"))
		}
		if r.URL.Query().Get("engine") != "google_hotels" {
			t.Errorf("engine = %q, want google_hotels", r.URL.Query().Get("engine"))
		}
		resp := Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Properties: []Hotel{{
				Name:         "Test Hotel",
				RatePerNight: Rate{Extracted: 99, Lowest: "$99"},
				TotalRate:    Rate{Extracted: 693, Lowest: "$693"},
			}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	// Override URL temporarily
	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	result, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-08", "EUR")
	if err != nil {
		t.Fatalf("SearchHotels failed: %v", err)
	}
	if len(result.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(result.Properties))
	}
	h := result.Properties[0]
	if h.Name != "Test Hotel" {
		t.Errorf("Name = %q, want Test Hotel", h.Name)
	}
	if h.PricePerNight() != 99 {
		t.Errorf("PricePerNight = %.0f, want 99", h.PricePerNight())
	}
	if h.TotalPrice() != 693 {
		t.Errorf("TotalPrice = %.0f, want 693", h.TotalPrice())
	}
}

func TestSearchHotelsVerifiedFetchesPropertyDetailsAndPromotesProviderTotal(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	var listCalls, detailCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("property_token") == "" {
			listCalls++
			assertQuery(t, q.Get("adults"), "2", "adults")
			assertQuery(t, q.Get("gl"), "us", "gl")
			if err := json.NewEncoder(w).Encode(Response{
				SearchMetadata: struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}{Status: "Success"},
				Properties: []Hotel{{
					Name:          "Sorriso Thermae",
					PropertyToken: "sorriso-token",
					RatePerNight:  Rate{Extracted: 156, Lowest: "€156"},
					TotalRate:     Rate{Extracted: 779, Lowest: "€779"},
					Prices:        nil,
				}},
			}); err != nil {
				t.Errorf("encode list response: %v", err)
			}
			return
		}

		detailCalls++
		assertQuery(t, q.Get("property_token"), "sorriso-token", "property_token")
		if err := json.NewEncoder(w).Encode(propertyDetailsResponse{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Hotel: Hotel{
				Name:          "Sorriso Thermae",
				PropertyToken: "sorriso-token",
				Prices: []PriceOption{{
					Source:       "Booking.com",
					RatePerNight: Rate{Extracted: 220, Lowest: "€220"},
					TotalRate:    Rate{Extracted: 1102, Lowest: "€1,102"},
				}},
			},
		}); err != nil {
			t.Errorf("encode detail response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	result, err := SearchHotelsVerified(context.Background(), SearchOptions{
		Query:      "Ischia",
		CheckIn:    "2026-07-30",
		CheckOut:   "2026-08-04",
		Currency:   "EUR",
		Adults:     2,
		GL:         "us",
		MaxDetails: 8,
	})
	if err != nil {
		t.Fatalf("SearchHotelsVerified failed: %v", err)
	}
	if listCalls != 1 || detailCalls != 1 {
		t.Fatalf("calls = list %d detail %d, want 1/1", listCalls, detailCalls)
	}
	if len(result.Properties) != 1 {
		t.Fatalf("properties = %d, want 1", len(result.Properties))
	}
	hotel := result.Properties[0]
	if hotel.TotalPrice() != 1102 {
		t.Fatalf("verified total = %.0f, want 1102", hotel.TotalPrice())
	}
	if hotel.ListTotalRate == nil || hotel.ListTotalRate.Extracted != 779 {
		t.Fatalf("list total = %#v, want preserved 779", hotel.ListTotalRate)
	}
	if len(hotel.Prices) != 1 || hotel.Prices[0].Source != "Booking.com" {
		t.Fatalf("prices = %#v, want Booking.com detail price", hotel.Prices)
	}
	if hotel.PriceVerification == nil || hotel.PriceVerification.Status != "detail_verified" {
		t.Fatalf("verification = %#v, want detail_verified", hotel.PriceVerification)
	}
	if hotel.PriceVerification.VerifiedTotal != 1102 || hotel.PriceVerification.ListTotal != 779 {
		t.Fatalf("verification totals = %#v, want list 779 verified 1102", hotel.PriceVerification)
	}
	if !containsString(hotel.PriceVerification.Warnings, "detail_price_higher_than_list") {
		t.Fatalf("verification warnings = %v, want detail_price_higher_than_list", hotel.PriceVerification.Warnings)
	}
}

func TestSearchHotelsVerifiedPopulatesPricesFromFeaturedOnlyDetail(t *testing.T) {
	// Roberto Reale's symptom: the detail endpoint returned provider rows only
	// under featured_prices (prices was null). The verified total promoted
	// correctly, but the inspectable Prices array stayed null, so the result
	// looked unverified even though it was detail-checked. After the fix the
	// verified provider breakdown is mirrored into Prices.
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("property_token") == "" {
			if err := json.NewEncoder(w).Encode(Response{
				SearchMetadata: struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}{Status: "Success"},
				Properties: []Hotel{{
					Name:          "Sorriso Thermae",
					PropertyToken: "sorriso-token",
					RatePerNight:  Rate{Extracted: 156, Lowest: "€156"},
					TotalRate:     Rate{Extracted: 779, Lowest: "€779"},
					Prices:        nil,
				}},
			}); err != nil {
				t.Errorf("encode list response: %v", err)
			}
			return
		}
		if err := json.NewEncoder(w).Encode(propertyDetailsResponse{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Hotel: Hotel{
				Name:          "Sorriso Thermae",
				PropertyToken: "sorriso-token",
				Prices:        nil,
				FeaturedPrices: []PriceOption{{
					Source:       "Booking.com",
					RatePerNight: Rate{Extracted: 205, Lowest: "€205"},
					TotalRate:    Rate{Extracted: 1024, Lowest: "€1,024"},
				}},
			},
		}); err != nil {
			t.Errorf("encode detail response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	result, err := SearchHotelsVerified(context.Background(), SearchOptions{
		Query:      "Ischia",
		CheckIn:    "2026-07-30",
		CheckOut:   "2026-08-04",
		Currency:   "EUR",
		Adults:     2,
		MaxDetails: 8,
	})
	if err != nil {
		t.Fatalf("SearchHotelsVerified failed: %v", err)
	}
	hotel := result.Properties[0]
	if hotel.TotalPrice() != 1024 {
		t.Fatalf("verified total = %.0f, want 1024", hotel.TotalPrice())
	}
	if len(hotel.Prices) != 1 || hotel.Prices[0].Source != "Booking.com" {
		t.Fatalf("prices = %#v, want Booking.com provider row mirrored from featured_prices", hotel.Prices)
	}
	if hotel.PriceVerification == nil || hotel.PriceVerification.Status != "detail_verified" {
		t.Fatalf("verification = %#v, want detail_verified", hotel.PriceVerification)
	}
}

func TestSearchHotelsVerifiedMarksPropertiesBeyondDetailLimit(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("property_token") == "" {
			if err := json.NewEncoder(w).Encode(Response{
				SearchMetadata: struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}{Status: "Success"},
				Properties: []Hotel{
					{
						Name:          "Checked Hotel",
						PropertyToken: "checked-token",
						TotalRate:     Rate{Extracted: 100},
					},
					{
						Name:          "Limit Hotel",
						PropertyToken: "limit-token",
						TotalRate:     Rate{Extracted: 200},
					},
				},
			}); err != nil {
				t.Errorf("encode list response: %v", err)
			}
			return
		}
		if err := json.NewEncoder(w).Encode(propertyDetailsResponse{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Hotel: Hotel{
				Name: "Checked Hotel",
				Prices: []PriceOption{{
					Source:    "Provider",
					TotalRate: Rate{Extracted: 120},
				}},
			},
		}); err != nil {
			t.Errorf("encode detail response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	result, err := SearchHotelsVerified(context.Background(), SearchOptions{
		Query:      "Ischia",
		CheckIn:    "2026-07-30",
		CheckOut:   "2026-08-04",
		Currency:   "EUR",
		MaxDetails: 1,
	})
	if err != nil {
		t.Fatalf("SearchHotelsVerified failed: %v", err)
	}
	if got := result.Properties[0].PriceVerification.Status; got != "detail_verified" {
		t.Fatalf("first status = %q, want detail_verified", got)
	}
	verification := result.Properties[1].PriceVerification
	if verification == nil {
		t.Fatal("second property should have detail-limit verification marker")
	}
	if verification.Status != "detail_not_checked_limit" {
		t.Fatalf("second status = %q, want detail_not_checked_limit", verification.Status)
	}
	if verification.ListTotal != 200 {
		t.Fatalf("second list total = %.0f, want 200", verification.ListTotal)
	}
}

func TestSearchHotelsVerifiedUsesLocalCacheForDuplicateListAndDetailRequests(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	t.Setenv(serpapiCacheDirEnv, t.TempDir())
	var listCalls, detailCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("property_token") == "" {
			listCalls++
			if err := json.NewEncoder(w).Encode(Response{
				SearchMetadata: struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}{Status: "Success"},
				Properties: []Hotel{{
					Name:          "Budget Hotel",
					PropertyToken: "budget-token",
					RatePerNight:  Rate{Extracted: 100},
					TotalRate:     Rate{Extracted: 500},
				}},
			}); err != nil {
				t.Errorf("encode list response: %v", err)
			}
			return
		}

		detailCalls++
		if err := json.NewEncoder(w).Encode(propertyDetailsResponse{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Hotel: Hotel{
				Name: "Budget Hotel",
				Prices: []PriceOption{{
					Source:    "Direct",
					TotalRate: Rate{Extracted: 520},
				}},
			},
		}); err != nil {
			t.Errorf("encode detail response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	opts := SearchOptions{
		Query:      "Ischia",
		CheckIn:    "2026-07-30",
		CheckOut:   "2026-08-04",
		Currency:   "EUR",
		MaxDetails: 1,
	}
	for i := 0; i < 2; i++ {
		result, err := SearchHotelsVerified(context.Background(), opts)
		if err != nil {
			t.Fatalf("SearchHotelsVerified call %d failed: %v", i+1, err)
		}
		if got := result.Properties[0].TotalPrice(); got != 520 {
			t.Fatalf("call %d total = %.0f, want cached verified 520", i+1, got)
		}
	}
	if listCalls != 1 || detailCalls != 1 {
		t.Fatalf("network calls = list %d detail %d, want 1/1 with local cache", listCalls, detailCalls)
	}
}

func TestSearchHotelsWithOptionsNoCacheBypassesLocalCache(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	t.Setenv(serpapiCacheDirEnv, t.TempDir())
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assertQuery(t, r.URL.Query().Get("no_cache"), "true", "no_cache")
		if err := json.NewEncoder(w).Encode(Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	opts := SearchOptions{
		Query:    "Ischia",
		CheckIn:  "2026-07-30",
		CheckOut: "2026-08-04",
		Currency: "EUR",
		NoCache:  true,
	}
	for i := 0; i < 2; i++ {
		if _, err := SearchHotelsWithOptions(context.Background(), opts); err != nil {
			t.Fatalf("SearchHotelsWithOptions call %d failed: %v", i+1, err)
		}
	}
	if calls != 2 {
		t.Fatalf("network calls = %d, want 2 when no_cache bypasses local cache", calls)
	}
}

func TestSearchHotelsWithOptionsPassesAccommodationCriteria(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assertQuery(t, q.Get("adults"), "4", "adults")
		assertQuery(t, q.Get("children"), "2", "children")
		assertQuery(t, q.Get("children_ages"), "7,9", "children_ages")
		assertQuery(t, q.Get("sort_by"), "3", "sort_by")
		assertQuery(t, q.Get("min_price"), "100", "min_price")
		assertQuery(t, q.Get("max_price"), "300.5", "max_price")
		assertQuery(t, q.Get("property_types"), "17,21", "property_types")
		assertQuery(t, q.Get("amenities"), "35,9", "amenities")
		assertQuery(t, q.Get("rating"), "8", "rating")
		assertQuery(t, q.Get("brands"), "33,67", "brands")
		assertQuery(t, q.Get("hotel_class"), "4,5", "hotel_class")
		assertQuery(t, q.Get("free_cancellation"), "true", "free_cancellation")
		assertQuery(t, q.Get("special_offers"), "true", "special_offers")
		assertQuery(t, q.Get("eco_certified"), "true", "eco_certified")
		assertQuery(t, q.Get("vacation_rentals"), "true", "vacation_rentals")
		assertQuery(t, q.Get("bedrooms"), "2", "bedrooms")
		assertQuery(t, q.Get("bathrooms"), "1", "bathrooms")
		assertQuery(t, q.Get("next_page_token"), "next-token", "next_page_token")
		assertQuery(t, q.Get("no_cache"), "true", "no_cache")
		if err := json.NewEncoder(w).Encode(Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	_, err := SearchHotelsWithOptions(context.Background(), SearchOptions{
		Query:            "Lisbon apartments",
		CheckIn:          "2026-08-10",
		CheckOut:         "2026-08-17",
		Currency:         "EUR",
		Adults:           4,
		Children:         2,
		ChildrenAges:     []int{7, 9},
		SortBy:           "price",
		MinPrice:         100,
		MaxPrice:         300.5,
		PropertyTypes:    []string{"17", "21"},
		Amenities:        []string{"35", "9"},
		Rating:           "8",
		Brands:           []string{"33", "67"},
		HotelClasses:     []int{4, 5},
		FreeCancellation: true,
		SpecialOffers:    true,
		EcoCertified:     true,
		VacationRentals:  true,
		MinBedrooms:      2,
		MinBathrooms:     1,
		NextPageToken:    "next-token",
		NoCache:          true,
	})
	if err != nil {
		t.Fatalf("SearchHotelsWithOptions failed: %v", err)
	}
}

func TestSearchHotelsWithOptionsRejectsMismatchedChildrenAges(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	_, err := SearchHotelsWithOptions(context.Background(), SearchOptions{
		Query:        "Lisbon apartments",
		CheckIn:      "2026-08-10",
		CheckOut:     "2026-08-17",
		Currency:     "EUR",
		Adults:       2,
		Children:     3,
		ChildrenAges: []int{7, 9},
	})
	if err == nil {
		t.Fatal("expected mismatched children error")
	}
}

func TestGoogleMapsDataCID(t *testing.T) {
	got, err := GoogleMapsDataCID("0x133b6ab82c204df7:0x437369f021e5e869")
	if err != nil {
		t.Fatalf("GoogleMapsDataCID failed: %v", err)
	}
	if got != "4860344902944680041" {
		t.Fatalf("data_cid = %q, want 4860344902944680041", got)
	}

	got, err = GoogleMapsDataCID("4860344902944680041")
	if err != nil {
		t.Fatalf("GoogleMapsDataCID decimal failed: %v", err)
	}
	if got != "4860344902944680041" {
		t.Fatalf("decimal data_cid = %q, want unchanged", got)
	}
}

func TestResolveGoogleMapsPlaceUsesDataCID(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assertQuery(t, q.Get("engine"), "google_maps", "engine")
		assertQuery(t, q.Get("type"), "place", "type")
		assertQuery(t, q.Get("data_cid"), "4860344902944680041", "data_cid")
		assertQuery(t, q.Get("api_key"), "test_key", "api_key")
		if err := json.NewEncoder(w).Encode(mapsPlaceResponse{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			PlaceResult: MapsPlace{
				Title:   "Hotel Continental Mare",
				Address: "Via Baldassarre Cossa, 25, 80074 Ischia NA, Italy",
				DataID:  "0x133b6ab82c204df7:0x437369f021e5e869",
				DataCID: "4860344902944680041",
			},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	place, err := ResolveGoogleMapsPlace(context.Background(), "0x133b6ab82c204df7:0x437369f021e5e869")
	if err != nil {
		t.Fatalf("ResolveGoogleMapsPlace failed: %v", err)
	}
	if place.Title != "Hotel Continental Mare" {
		t.Fatalf("title = %q, want Hotel Continental Mare", place.Title)
	}
}

func TestResolveGoogleMapsPlaceRejectsInvalidID(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	if _, err := ResolveGoogleMapsPlace(context.Background(), "not-a-google-id"); err == nil {
		t.Fatal("expected invalid google hotel ID error")
	}
}

func TestSearchHotels_HTTPError(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	_, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-02", "EUR")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestSearchHotels_MissingKey(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "")
	_, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-02", "EUR")
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestSearchHotels_ErrorStatus(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Error"},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	_, err := SearchHotels(context.Background(), "Test", "2026-01-01", "2026-01-02", "EUR")
	if err == nil {
		t.Fatal("expected error for Error status")
	}
}

func assertQuery(t *testing.T, got, want, name string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s query = %q, want %q", name, got, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestSearchHotelsWithOptions_PaginationToken pins the page-2 pagination
// contract used to page google_hotels past page 1 until a named property is
// found: a prior response's next_page_token is forwarded as a query param on
// the follow-up request, and the serpapi_pagination.next_page_token field of
// the response is parsed back so the caller can keep paging. Fails-before /
// passes-after the SerpapiPagination struct field + the next_page_token query
// wiring.
func TestSearchHotelsWithOptions_PaginationToken(t *testing.T) {
	t.Setenv("SERPAPI_KEY", "test_key")
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("next_page_token")
		resp := Response{
			SearchMetadata: struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}{Status: "Success"},
			Properties: []Hotel{{Name: "Paged Hotel"}},
		}
		resp.SerpapiPagination.NextPageToken = "NEXT_PAGE_3"
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	origSearch := searchURL
	searchURL = srv.URL + "/search"
	defer func() { searchURL = origSearch }()

	resp, err := SearchHotelsWithOptions(context.Background(), SearchOptions{
		Query:         "Test",
		CheckIn:       "2026-01-01",
		CheckOut:      "2026-01-08",
		Currency:      "EUR",
		NextPageToken: "PAGE_2",
	})
	if err != nil {
		t.Fatalf("SearchHotelsWithOptions failed: %v", err)
	}
	if gotToken != "PAGE_2" {
		t.Fatalf("outgoing next_page_token = %q, want PAGE_2 (token not forwarded for page-2 fetch)", gotToken)
	}
	if resp.SerpapiPagination.NextPageToken != "NEXT_PAGE_3" {
		t.Fatalf("parsed next_page_token = %q, want NEXT_PAGE_3 (pagination field not parsed)", resp.SerpapiPagination.NextPageToken)
	}
}
