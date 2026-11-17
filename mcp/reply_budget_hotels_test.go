package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

var budgetHotelArgs = map[string]any{"location": "Vienna", "check_in": "2027-03-10", "check_out": "2027-03-11"}

// budgetHotel is a hotel row padded to roughly pad bytes, priced in EUR with
// a comparable price so the currency-aware lead-in selection applies.
func budgetHotel(i int, price, rating float64, pad int) models.HotelResult {
	return models.HotelResult{
		Name:            fmt.Sprintf("Hotel %03d", i),
		HotelID:         fmt.Sprintf("h%03d", i),
		Price:           price,
		ComparablePrice: price,
		Currency:        "EUR",
		Rating:          rating,
		Description:     strings.Repeat("d", pad),
	}
}

// budgetHotelFixture returns n hotels sorted cheapest first, rated 7.0, except
// the last (priciest) one, which is the highest rated: a naive tail cut would
// drop the hotel the summary names.
func budgetHotelFixture(n, pad int) []models.HotelResult {
	rows := make([]models.HotelResult, n)
	for i := range rows {
		rows[i] = budgetHotel(i, float64(40+i), 7.0, pad)
	}
	rows[n-1].Rating = 9.9
	return rows
}

func stubHotelSearch(t *testing.T, rows []models.HotelResult) {
	t.Helper()
	orig := searchHotelsFunc
	t.Cleanup(func() { searchHotelsFunc = orig })
	searchHotelsFunc = func(context.Context, string, hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		cp := slices.Clone(rows)
		return &models.HotelSearchResult{
			Success: true, Count: len(cp), Hotels: cp,
			ProviderStatuses: []models.ProviderStatus{{ID: "google_hotels", Name: "Google Hotels", Status: "ok", Results: len(cp)}},
		}, nil
	}
}

func mergeArgs(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestReplyBudgetSearchHotels(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(300, 900))
	for _, via := range []string{"search_hotels", "travel"} {
		t.Run(via, func(t *testing.T) {
			var got groundCall
			if via == "travel" {
				got = callGroundTool(t, "travel", map[string]any{"intent": "search_hotels", "params": budgetHotelArgs})
			} else {
				got = callGroundTool(t, "search_hotels", budgetHotelArgs)
			}
			if got.isError {
				t.Fatalf("isError: %s", got.errText)
			}
			assertUnderPromise(t, got)
			body := replyBody(t, got)
			names := rowNames(body, "hotels", "name")
			if len(names) == 0 || len(names) >= 300 {
				t.Fatalf("hotels shown = %d, want a cut between 1 and 299", len(names))
			}
			if c := bodyInt(body, "count"); c != len(names) {
				t.Errorf("count = %d, want the %d rows in this response", c, len(names))
			}
			if o := bodyInt(body, "budget_omitted"); o != 300-len(names) {
				t.Errorf("budget_omitted = %d, want %d", o, 300-len(names))
			}
			for _, want := range []string{"Hotel 000", "Hotel 299"} {
				if !slices.Contains(names, want) {
					t.Errorf("%s (named by the summary) was cut", want)
				}
			}
			if !strings.Contains(got.userText, fmt.Sprintf("Showing %d of 300 hotels", len(names))) || !strings.Contains(got.userText, "50 KB") {
				t.Errorf("summary should say Showing %d of 300 hotels and name the 50 KB budget:\n%s", len(names), got.userText)
			}
			if !strings.Contains(got.userText, "Found 300 hotels") {
				t.Errorf("summary should still describe all 300 found:\n%s", got.userText)
			}
			assertTextCopyMatches(t, got)
		})
	}
}

// TestReplyBudgetHotelsPinsCurrencyAwareLeadIn: under sort=rating the lead-in
// sits last, and a nominally cheaper unconverted foreign price must not take
// its pin.
func TestReplyBudgetHotelsPinsCurrencyAwareLeadIn(t *testing.T) {
	rows := make([]models.HotelResult, 300)
	for i := range rows {
		rows[i] = budgetHotel(i, 120, 9.9-float64(i)*0.01, 900)
	}
	rows[150].Currency, rows[150].Price, rows[150].ComparablePrice = "KWD", 5, 0
	rows[299].Price, rows[299].ComparablePrice = 25, 25
	stubHotelSearch(t, rows)
	got := callGroundTool(t, "search_hotels", mergeArgs(budgetHotelArgs, map[string]any{"sort": "rating"}))
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	names := rowNames(got.structured, "hotels", "name")
	if !slices.Contains(names, "Hotel 299") {
		t.Errorf("the EUR 25 lead-in hotel was cut; shown %d rows", len(names))
	}
	if !strings.Contains(got.userText, "Lowest lead-in: EUR25/night (Hotel 299)") {
		t.Errorf("summary lead-in should be the comparable minimum:\n%s", got.userText)
	}
}

func TestReplyBudgetHotelsUnderBudgetUnchanged(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(5, 200))
	got := callGroundTool(t, "search_hotels", budgetHotelArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	if _, ok := got.structured["budget_omitted"]; ok {
		t.Errorf("budget_omitted present on an under-budget reply")
	}
	if c := bodyInt(got.structured, "count"); c != 5 {
		t.Errorf("count = %d, want 5", c)
	}
	if strings.Contains(got.userText, "Showing") || strings.Contains(got.userText, "50 KB") {
		t.Errorf("under-budget summary changed:\n%s", got.userText)
	}
	// Every field of every row ships as the search returned it.
	raw, _ := json.Marshal(budgetHotelFixture(5, 200))
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.structured["hotels"], want) {
		t.Errorf("under-budget hotel rows differ from the search result")
	}
	assertTextCopyMatches(t, got)
}

// TestReplyBudgetHotelsFloor drives the cut to its floor: only the two pinned
// rows fit, and they are what ships.
func TestReplyBudgetHotelsFloor(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(300, 9_000))
	got := callGroundTool(t, "search_hotels", budgetHotelArgs)
	if got.isError {
		t.Fatalf("isError: %s", got.errText)
	}
	assertUnderPromise(t, got)
	names := rowNames(got.structured, "hotels", "name")
	if strings.Join(names, ",") != "Hotel 000,Hotel 299" {
		t.Errorf("floor kept %d rows, want only the two pinned hotels", len(names))
	}
	if o := bodyInt(got.structured, "budget_omitted"); o != 298 {
		t.Errorf("budget_omitted = %d, want 298", o)
	}
}

func TestReplyBudgetHotelsFloorOverBudget(t *testing.T) {
	stubHotelSearch(t, budgetHotelFixture(10, 30_000))
	assertBoundedFloorError(t, callGroundTool(t, "search_hotels", budgetHotelArgs))
}
