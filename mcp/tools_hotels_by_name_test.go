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

func stubSearchHotelsByName(t *testing.T, fn func(context.Context, string, string, string, string, string) ([]models.HotelResult, error)) {
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
	stubSearchHotelsByName(t, func(_ context.Context, name, _, _, _, _ string) ([]models.HotelResult, error) {
		return nil, errors.Join(
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
	stubSearchHotelsByName(t, func(context.Context, string, string, string, string, string) ([]models.HotelResult, error) {
		return nil, boom
	})
	if _, _, err := handleSearchHotelByName(context.Background(), byNameArgs(), nil, nil, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the provider error passed through", err)
	}
}
