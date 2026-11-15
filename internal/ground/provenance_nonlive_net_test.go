package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestNonLiveProducer_EckeroLineTimetable(t *testing.T) {
	withEckeroServer(t, eckeroDefaultHome, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	routes, err := SearchEckeroLine(context.Background(), "Helsinki", "Tallinn", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchEckeroLine: %v", err)
	}
	assertNeverLiveThenClassified(t, "eckeroline timetable", routes)
}

// withDFDSAvailability serves body with status from the DFDS availability API.
func withDFDSAvailability(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	origClient, origLimiter := dfdsClient, dfdsLimiter
	dfdsClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() {
		srv.Close()
		dfdsClient, dfdsLimiter = origClient, origLimiter
	})
}

const dfdsAvailableJSON = `{"dates":{"fromDate":"2026-04-07","toDate":"2026-12-30"},"defaultDate":"2026-04-07","disabledDates":[],"offerDates":[]}`

func TestNonLiveProducer_DFDSTable(t *testing.T) {
	withDFDSAvailability(t, http.StatusOK, dfdsAvailableJSON)
	routes, err := SearchDFDS(context.Background(), "Amsterdam", "Newcastle", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchDFDS: %v", err)
	}
	assertNeverLiveThenClassified(t, "dfds", routes)
}

func TestNonLiveProducer_Finnlines(t *testing.T) {
	charge := 4500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var resp finnlinesGraphQLResponse
		resp.Data.ListTimeTableAvailability = []finnlinesTimetableEntry{{
			SailingCode: "FI001", DepartureDate: "2026-07-01", DepartureTime: "17:00",
			ArrivalDate: "2026-07-02", ArrivalTime: "09:30", DeparturePort: "FIHEL", ArrivalPort: "DETRV",
			IsAvailable: true, ShipName: "FINNSTAR", CrossingTime: "29:30", ChargeTotal: &charge,
		}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	origClient, origLimiter := finnlinesClient, finnlinesLimiter
	finnlinesClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { finnlinesClient, finnlinesLimiter = origClient, origLimiter })
	routes, err := SearchFinnlines(context.Background(), "Helsinki", "Travemünde", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchFinnlines: %v", err)
	}
	assertNeverLiveThenClassified(t, "finnlines", routes)
}

// TestNonLiveProducer_TransitousSchedule: Transitous has schedules but no
// fares, so its routes are unavailable after the merge pass.
func TestNonLiveProducer_TransitousSchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(transitousResponse{
			From: transitousPlace{Name: "Helsinki"},
			To:   transitousPlace{Name: "Tampere"},
			Itineraries: []transitousItinerary{{
				Duration: 5400, StartTime: "2026-07-01T08:00:00+03:00", EndTime: "2026-07-01T09:30:00+03:00",
				Legs: []transitousLeg{{
					Mode: "RAIL", Duration: 5400,
					From:      transitousPlace{Name: "Helsinki Railway Station"},
					To:        transitousPlace{Name: "Tampere Railway Station"},
					StartTime: "2026-07-01T08:00:00+03:00", EndTime: "2026-07-01T09:30:00+03:00",
					Route: &transitousRoute{Agency: "VR", ShortName: "IC 123"},
				}},
			}},
		})
	}))
	defer srv.Close()
	origClient, origLimiter := transitousClient, transitousLimiter
	transitousClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	transitousLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { transitousClient, transitousLimiter = origClient, origLimiter })
	routes, err := SearchTransitous(context.Background(), 60.17, 24.94, 61.50, 23.77, "2026-07-01")
	if err != nil {
		t.Fatalf("SearchTransitous: %v", err)
	}
	assertNeverLiveThenClassified(t, "transitous", routes)
}

// TestNonLiveProducer_DistribusionLowestPrice: Distribusion reports the
// connection's lowest price, a range low.
func TestNonLiveProducer_DistribusionLowestPrice(t *testing.T) {
	payload, err := json.Marshal(distribusionJSONAPI{
		Data: []distribusionData{{
			ID: "conn-1", Type: "connection",
			Attributes: distribusionConnectionAttrs{
				DepartureTime: "2026-06-01T07:00:00", ArrivalTime: "2026-06-01T09:00:00", DurationInMinutes: 120,
				LowestPrice: 1500, Currency: "EUR", TrafficType: "bus", MarketingCarrierCode: "LUXEX",
				DepartureStationCode: "FIHELS", ArrivalStationCode: "EETLLS", Available: true,
			},
		}},
		Included: []distribusionIncluded{
			{ID: "FIHELS", Type: "station", Attributes: distribusionIncludedAttrs{Name: "Helsinki Kamppi", City: "Helsinki"}},
			{ID: "EETLLS", Type: "station", Attributes: distribusionIncludedAttrs{Name: "Tallinn Bus Station", City: "Tallinn"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv := mockDistribusionServer(t, string(payload), http.StatusOK)
	defer srv.Close()
	t.Setenv("DISTRIBUSION_API_KEY", "mock-api-key")
	origClient, origLimiter := distribusionHTTPClient, distribusionLimiter
	distribusionHTTPClient = &http.Client{Timeout: 5 * time.Second, Transport: &mockDistribusionTransport{real: srv.URL, original: distribusionAPIBase}}
	distribusionLimiter = rate.NewLimiter(rate.Inf, 1)
	t.Cleanup(func() { distribusionHTTPClient, distribusionLimiter = origClient, origLimiter })
	routes, err := SearchDistribusion(context.Background(), "Helsinki", "Tallinn", "2026-06-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDistribusion: %v", err)
	}
	assertNeverLiveThenClassified(t, "distribusion", routes)
}
