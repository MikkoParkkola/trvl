package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func stubSearchHotelsByName(t *testing.T, fn func(context.Context, string, string, string, string, string) ([]models.HotelResult, []models.ProviderStatus, error)) {
	t.Helper()
	orig := searchHotelsByNameFunc
	searchHotelsByNameFunc = fn
	t.Cleanup(func() { searchHotelsByNameFunc = orig })
}

func byNameArgs() map[string]any {
	return map[string]any{
		"name": "Palais Nonexistent", "location": "Vienna",
		"check_in": "2099-06-15", "check_out": "2099-06-18",
	}
}

// TestHandleSearchHotelByName_NoMatchIsEmptySuccessWithNotice: "no hotel by
// that name" is an answer, not a tool failure. The handler returns an empty
// success whose notice names the property and carries the Google error that
// came back with the miss.
func TestHandleSearchHotelByName_NoMatchIsEmptySuccessWithNotice(t *testing.T) {
	stubSearchHotelsByName(t, func(_ context.Context, name, _, _, _, _ string) ([]models.HotelResult, []models.ProviderStatus, error) {
		return nil, nil, errors.Join(
			fmt.Errorf("%w: %q", hotels.ErrNoNameMatch, name),
			errors.New("hotel search request: request blocked (403)"),
		)
	})

	_, structured, err := handleSearchHotelByName(context.Background(), byNameArgs(), nil, nil, nil)
	if err != nil {
		t.Fatalf("no-match returned a tool error %q, want an empty success", err)
	}
	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Success bool              `json:"success"`
		Count   int               `json:"count"`
		Hotels  []json.RawMessage `json:"hotels"`
		Notice  string            `json:"notice"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if !resp.Success || resp.Count != 0 || len(resp.Hotels) != 0 {
		t.Fatalf("response = %s, want success with zero hotels", raw)
	}
	if !strings.Contains(string(raw), `"hotels":[]`) {
		t.Errorf("response = %s, want an empty hotels array, not null", raw)
	}
	if !strings.Contains(resp.Notice, "Palais Nonexistent") {
		t.Errorf("notice = %q, want it to name the requested property", resp.Notice)
	}
	if !strings.Contains(resp.Notice, "request blocked (403)") {
		t.Errorf("notice = %q, want the Google error text", resp.Notice)
	}
}

// TestHandleSearchHotelByName_OtherErrorsStayErrors guards the boundary: only
// the no-match sentinel becomes an empty success.
func TestHandleSearchHotelByName_OtherErrorsStayErrors(t *testing.T) {
	boom := errors.New("no results from any provider")
	stubSearchHotelsByName(t, func(context.Context, string, string, string, string, string) ([]models.HotelResult, []models.ProviderStatus, error) {
		return nil, nil, boom
	})
	if _, _, err := handleSearchHotelByName(context.Background(), byNameArgs(), nil, nil, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the provider error passed through", err)
	}
}

// TestHandleSearchHotelByName_NoMatchWithoutGoogleError: Google answered
// fine and simply had no such property. Still an empty success, and the
// notice names the property without inventing a provider failure.
func TestHandleSearchHotelByName_NoMatchWithoutGoogleError(t *testing.T) {
	stubSearchHotelsByName(t, func(_ context.Context, name, _, _, _, _ string) ([]models.HotelResult, []models.ProviderStatus, error) {
		return nil, nil, fmt.Errorf("%w: %q", hotels.ErrNoNameMatch, name)
	})

	_, structured, err := handleSearchHotelByName(context.Background(), byNameArgs(), nil, nil, nil)
	if err != nil {
		t.Fatalf("no-match returned a tool error %q, want an empty success", err)
	}
	raw, _ := json.Marshal(structured)
	var resp struct {
		Success bool   `json:"success"`
		Count   int    `json:"count"`
		Notice  string `json:"notice"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if !resp.Success || resp.Count != 0 || !strings.Contains(resp.Notice, "Palais Nonexistent") {
		t.Fatalf("response = %s, want an empty success whose notice names the property", raw)
	}
}

// TestSearchHotelByNameTool_DeclaresItsExtraFields: the response carries
// search_name and notice, so the tool's output schema must declare them.
func TestSearchHotelByNameTool_DeclaresItsExtraFields(t *testing.T) {
	schema, ok := searchHotelByNameTool().OutputSchema.(map[string]interface{})
	if !ok {
		t.Fatalf("output schema is %T, want an object schema", searchHotelByNameTool().OutputSchema)
	}
	props, _ := schema["properties"].(map[string]interface{})
	for _, field := range []string{"search_name", "notice"} {
		if _, ok := props[field]; !ok {
			t.Errorf("output schema lacks %q", field)
		}
	}
	if _, ok := hotelSearchOutputSchema().(map[string]interface{})["properties"].(map[string]interface{})["notice"]; ok {
		t.Errorf("the shared search_hotels schema gained notice; declare it on search_hotel_by_name only")
	}
}

// TestHandleSearchHotelByName_ReportsCompleteness: the by-name lookup carries
// its provider statuses and the completeness they add up to, on a hit and on
// a miss. A miss while Google failed must not read as a complete answer.
func TestHandleSearchHotelByName_ReportsCompleteness(t *testing.T) {
	statuses := []models.ProviderStatus{
		{ID: "google_hotels", Status: models.StatusFailed, Error: "request blocked (403)"},
		{ID: "booking", Status: models.StatusCheckedHit},
	}
	cases := map[string][]models.HotelResult{
		"hit":  {{Name: "Hotel Sacher Vienna", HotelID: "ChIJsacher"}},
		"miss": nil,
	}
	for label, found := range cases {
		stubSearchHotelsByName(t, func(_ context.Context, name, _, _, _, _ string) ([]models.HotelResult, []models.ProviderStatus, error) {
			if found == nil {
				return nil, statuses, fmt.Errorf("%w: %q", hotels.ErrNoNameMatch, name)
			}
			return found, statuses, nil
		})
		_, structured, err := handleSearchHotelByName(context.Background(), byNameArgs(), nil, nil, nil)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		raw, _ := json.Marshal(structured)
		var resp struct {
			ProviderStatuses []models.ProviderStatus `json:"provider_statuses"`
			Completeness     models.Completeness     `json:"completeness"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("%s: decode %s: %v", label, raw, err)
		}
		if len(resp.ProviderStatuses) != 2 {
			t.Errorf("%s: provider_statuses = %+v, want both statuses", label, resp.ProviderStatuses)
		}
		if resp.Completeness.State != models.CompletenessPartial || resp.Completeness.Queried != 2 {
			t.Errorf("%s: completeness = %+v, want partial with 2 queried", label, resp.Completeness)
		}
	}
}
