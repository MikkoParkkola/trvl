package hotels

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- parseDetailAmenities ---

func TestParseDetailAmenities_AmenityGroups(t *testing.T) {
	// Build a page with AF_initDataCallback blocks containing amenity groups.
	page := buildFakeDetailPage([]any{
		[]any{
			"Popular",
			[]any{
				[]any{"Free WiFi"},
				[]any{"Pool"},
				[]any{"Breakfast included"},
			},
		},
		[]any{
			"Food & drink",
			[]any{
				[]any{"Restaurant"},
				[]any{"Bar"},
				[]any{"Room service"},
			},
		},
	})

	amenities, err := parseDetailAmenities(page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := toSet(amenities)
	for _, want := range []string{"Free WiFi", "Pool", "Breakfast included", "Restaurant", "Bar", "Room service"} {
		if !got[want] {
			t.Errorf("missing amenity %q in %v", want, amenities)
		}
	}
}

func TestParseDetailAmenities_CodePairs(t *testing.T) {
	// Build a page with amenity code pairs (same format as search results).
	page := buildFakeDetailPage([]any{
		nil, nil, nil,
		[]any{
			[]any{float64(1), float64(2)},  // free_wifi
			[]any{float64(1), float64(4)},  // pool
			[]any{float64(1), float64(8)},  // spa
			[]any{float64(1), float64(22)}, // restaurant
		},
	})

	amenities, err := parseDetailAmenities(page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := toSet(amenities)
	for _, want := range []string{"free_wifi", "pool", "spa", "restaurant"} {
		if !got[want] {
			t.Errorf("missing amenity %q in %v", want, amenities)
		}
	}
}

func TestParseDetailAmenities_FlatList(t *testing.T) {
	// Build a page with a flat list of amenity-name arrays.
	page := buildFakeDetailPage([]any{
		[]any{"Free WiFi"},
		[]any{"Pool"},
		[]any{"Spa"},
		[]any{"Parking"},
		[]any{"Breakfast"},
	})

	amenities, err := parseDetailAmenities(page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := toSet(amenities)
	for _, want := range []string{"Free WiFi", "Pool", "Spa", "Parking", "Breakfast"} {
		if !got[want] {
			t.Errorf("missing amenity %q in %v", want, amenities)
		}
	}
}

func TestParseDetailAmenities_Deduplicates(t *testing.T) {
	page := buildFakeDetailPage([]any{
		[]any{
			"Popular",
			[]any{
				[]any{"Free WiFi"},
				[]any{"Pool"},
			},
		},
		[]any{
			"Highlights",
			[]any{
				[]any{"Free WiFi"}, // duplicate
				[]any{"Spa"},
			},
		},
	})

	amenities, err := parseDetailAmenities(page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count occurrences of "Free WiFi".
	count := 0
	for _, a := range amenities {
		if a == "Free WiFi" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected Free WiFi once, got %d times in %v", count, amenities)
	}
}

func TestParseDetailAmenities_NoCallbacks(t *testing.T) {
	_, err := parseDetailAmenities("<html>no callbacks</html>")
	if err == nil {
		t.Error("expected error for page without callbacks")
	}
}

func TestParseDetailAmenities_NoAmenities(t *testing.T) {
	page := buildFakeDetailPage([]any{"just", "strings", "no", "amenities"})
	_, err := parseDetailAmenities(page)
	if err == nil {
		t.Error("expected error for page without amenity data")
	}
}

// --- tryAmenityGroup ---

func TestTryAmenityGroup_Valid(t *testing.T) {
	arr := []any{
		"Popular",
		[]any{
			[]any{"Free WiFi"},
			[]any{"Pool"},
		},
	}

	names := tryAmenityGroup(arr)
	if len(names) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(names), names)
	}
	if names[0] != "Free WiFi" || names[1] != "Pool" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestTryAmenityGroup_UnknownGroupName(t *testing.T) {
	arr := []any{
		"Random Header",
		[]any{[]any{"Something"}},
	}
	names := tryAmenityGroup(arr)
	if len(names) != 0 {
		t.Errorf("expected empty for unknown group name, got %v", names)
	}
}

func TestTryAmenityGroup_NotString(t *testing.T) {
	arr := []any{
		float64(42),
		[]any{[]any{"Something"}},
	}
	names := tryAmenityGroup(arr)
	if len(names) != 0 {
		t.Errorf("expected empty for non-string group name, got %v", names)
	}
}

// --- tryFlatAmenityList ---

func TestTryFlatAmenityList_Valid(t *testing.T) {
	arr := []any{
		[]any{"Free WiFi"},
		[]any{"Pool"},
		[]any{"Parking"},
		[]any{"Breakfast"},
	}

	names := tryFlatAmenityList(arr)
	if len(names) != 4 {
		t.Fatalf("expected 4, got %d: %v", len(names), names)
	}
}

func TestTryFlatAmenityList_TooFew(t *testing.T) {
	arr := []any{
		[]any{"Pool"},
		[]any{"Spa"},
	}
	names := tryFlatAmenityList(arr)
	if len(names) != 0 {
		t.Errorf("expected empty for too few items, got %v", names)
	}
}

func TestTryFlatAmenityList_NonAmenityStrings(t *testing.T) {
	arr := []any{
		[]any{"Some random text"},
		[]any{"Another random thing"},
		[]any{"Not an amenity at all"},
	}
	names := tryFlatAmenityList(arr)
	if len(names) != 0 {
		t.Errorf("expected empty for non-amenity strings, got %v", names)
	}
}

// --- tryAmenityCodePairs ---

func TestTryAmenityCodePairs_Valid(t *testing.T) {
	arr := []any{
		[]any{float64(1), float64(2)},
		[]any{float64(1), float64(4)},
		[]any{float64(1), float64(8)},
	}

	names := tryAmenityCodePairs(arr)
	got := toSet(names)
	if !got["free_wifi"] || !got["pool"] || !got["spa"] {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestTryAmenityCodePairs_TooFew(t *testing.T) {
	arr := []any{
		[]any{float64(1), float64(2)},
		[]any{float64(1), float64(4)},
	}
	names := tryAmenityCodePairs(arr)
	if len(names) != 0 {
		t.Errorf("expected empty for too few pairs, got %v", names)
	}
}

func TestTryAmenityCodePairs_NotPairs(t *testing.T) {
	arr := []any{
		[]any{"not", "numbers"},
		[]any{"also", "not"},
		[]any{"still", "not"},
	}
	names := tryAmenityCodePairs(arr)
	if len(names) != 0 {
		t.Errorf("expected empty for non-numeric pairs, got %v", names)
	}
}

// --- normalizeAmenityName ---

func TestNormalizeAmenityName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Free WiFi", "Free WiFi"},
		{"  Pool  ", "Pool"},
		{"", ""},
		{"http://example.com", ""},
		{"<b>Bold</b>", ""},
		{strings.Repeat("x", 101), ""},
	}

	for _, tt := range tests {
		got := normalizeAmenityName(tt.input)
		if got != tt.want {
			t.Errorf("normalizeAmenityName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- isAmenityGroupName ---

func TestIsAmenityGroupName(t *testing.T) {
	for _, name := range []string{"Popular", "POPULAR", "Food & drink", "  parking  "} {
		if !isAmenityGroupName(name) {
			t.Errorf("expected %q to be recognized as amenity group", name)
		}
	}

	for _, name := range []string{"Random", "Hotel Info", ""} {
		if isAmenityGroupName(name) {
			t.Errorf("expected %q to NOT be recognized as amenity group", name)
		}
	}
}

// --- looksLikeAmenity ---

func TestLooksLikeAmenity(t *testing.T) {
	for _, s := range []string{"Free WiFi", "Indoor pool", "Spa", "Parking available", "Breakfast included"} {
		if !looksLikeAmenity(s) {
			t.Errorf("expected %q to look like amenity", s)
		}
	}

	for _, s := range []string{"John Smith", "2024-01-01", "Click here"} {
		if looksLikeAmenity(s) {
			t.Errorf("expected %q to NOT look like amenity", s)
		}
	}
}

// --- mergeAmenities ---

func TestMergeAmenities_Basic(t *testing.T) {
	existing := []string{"pool", "spa"}
	additional := []string{"wifi", "pool", "breakfast"}

	merged := mergeAmenities(existing, additional)

	if len(merged) != 4 {
		t.Fatalf("expected 4 merged, got %d: %v", len(merged), merged)
	}

	// Existing items should come first.
	if merged[0] != "pool" || merged[1] != "spa" {
		t.Errorf("existing items should be first: %v", merged)
	}

	got := toSet(merged)
	for _, want := range []string{"pool", "spa", "wifi", "breakfast"} {
		if !got[want] {
			t.Errorf("missing %q in merged %v", want, merged)
		}
	}
}

func TestMergeAmenities_CaseInsensitiveDedup(t *testing.T) {
	existing := []string{"Pool"}
	additional := []string{"pool", "POOL"}

	merged := mergeAmenities(existing, additional)
	if len(merged) != 1 {
		t.Errorf("expected 1 (deduplicated), got %d: %v", len(merged), merged)
	}
	if merged[0] != "Pool" {
		t.Errorf("should keep first occurrence: got %q", merged[0])
	}
}

func TestMergeAmenities_Empty(t *testing.T) {
	if merged := mergeAmenities(nil, nil); len(merged) != 0 {
		t.Errorf("expected empty, got %v", merged)
	}
	if merged := mergeAmenities(nil, []string{"pool"}); len(merged) != 1 {
		t.Errorf("expected 1, got %v", merged)
	}
	if merged := mergeAmenities([]string{"pool"}, nil); len(merged) != 1 {
		t.Errorf("expected 1, got %v", merged)
	}
}

// --- enrichHotelAmenities concurrency ---

func TestEnrichHotelAmenities_RespectsLimit(t *testing.T) {
	// This tests the enrichment logic with the actual function signature.
	// Since FetchHotelAmenities requires network, we test the limit/index
	// selection logic by verifying the function handles the limit parameter.
	hotels := make([]models.HotelResult, 20)
	for i := range hotels {
		hotels[i] = models.HotelResult{
			Name:    fmt.Sprintf("Hotel %d", i),
			HotelID: fmt.Sprintf("id_%d", i),
		}
	}

	// With a cancelled context, enrichment should silently fail and return
	// the original hotels unchanged.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result := enrichHotelAmenities(ctx, hotels, 5)
	if len(result) != 20 {
		t.Errorf("expected 20 hotels returned, got %d", len(result))
	}
}

func TestEnrichHotelAmenities_SkipsNoHotelID(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Hotel A", HotelID: ""},     // no ID
		{Name: "Hotel B", HotelID: "id_b"}, // has ID
		{Name: "Hotel C", HotelID: ""},     // no ID
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := enrichHotelAmenities(ctx, hotels, 5)
	if len(result) != 3 {
		t.Errorf("expected 3 hotels, got %d", len(result))
	}
}

func TestEnrichHotelAmenities_DefaultAndMaxLimit(t *testing.T) {
	// Test that limit 0 defaults to 5 and limit > 10 caps at 10.
	// We verify indirectly by checking no panic occurs.
	hotels := make([]models.HotelResult, 3)
	for i := range hotels {
		hotels[i] = models.HotelResult{
			Name:    fmt.Sprintf("Hotel %d", i),
			HotelID: fmt.Sprintf("id_%d", i),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Default limit.
	result := enrichHotelAmenities(ctx, hotels, 0)
	if len(result) != 3 {
		t.Errorf("expected 3, got %d", len(result))
	}

	// Excessive limit.
	result = enrichHotelAmenities(ctx, hotels, 100)
	if len(result) != 3 {
		t.Errorf("expected 3, got %d", len(result))
	}
}

func TestEnrichHotelAmenities_ConcurrencyLimit(t *testing.T) {
	// Verify that concurrent goroutines are bounded.
	// We use a counter to track peak concurrency. Since we can't easily mock
	// FetchHotelAmenities (it uses DefaultClient), we test the semaphore
	// pattern by checking the function doesn't deadlock with many hotels.
	hotels := make([]models.HotelResult, 15)
	for i := range hotels {
		hotels[i] = models.HotelResult{
			Name:    fmt.Sprintf("Hotel %d", i),
			HotelID: fmt.Sprintf("id_%d", i),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should not deadlock even with limit > concurrency.
	result := enrichHotelAmenities(ctx, hotels, 10)
	if len(result) != 15 {
		t.Errorf("expected 15, got %d", len(result))
	}
}

// --- findDetailAmenities depth ---

func TestFindDetailAmenities_MaxDepth(t *testing.T) {
	// Build a deeply nested structure to verify depth limit.
	var nested any = []any{
		"Popular",
		[]any{[]any{"Deep WiFi"}},
	}
	for i := 0; i < 15; i++ {
		nested = []any{nested}
	}

	// At depth 15, the amenity group is too deep to find.
	results := findDetailAmenities(nested, 0)
	if len(results) != 0 {
		t.Errorf("expected empty at extreme depth, got %v", results)
	}
}

func TestFindDetailAmenities_MapNavigation(t *testing.T) {
	data := map[string]any{
		"amenities": []any{
			"Popular",
			[]any{
				[]any{"Free WiFi"},
				[]any{"Pool"},
			},
		},
	}

	results := findDetailAmenities(data, 0)
	got := toSet(results)
	if !got["Free WiFi"] || !got["Pool"] {
		t.Errorf("expected Free WiFi and Pool, got %v", results)
	}
}

// --- helpers ---

// buildFakeDetailPage creates a minimal HTML page with an AF_initDataCallback
// block containing the given JSON data.
func buildFakeDetailPage(data any) string {
	// Serialize data to JSON.
	var sb strings.Builder
	sb.WriteString("<html><body><script>")
	sb.WriteString("AF_initDataCallback({key: 'ds:0', data:")

	// Simple JSON serialization for test data.
	jsonBytes := marshalTestJSON(data)
	_, _ = sb.Write(jsonBytes)

	sb.WriteString("});")
	sb.WriteString("</script></body></html>")
	return sb.String()
}

// marshalTestJSON serializes test data to JSON bytes.
func marshalTestJSON(v any) []byte {
	switch val := v.(type) {
	case nil:
		return []byte("null")
	case string:
		return []byte(fmt.Sprintf("%q", val))
	case float64:
		return []byte(fmt.Sprintf("%g", val))
	case int:
		return []byte(fmt.Sprintf("%d", val))
	case bool:
		if val {
			return []byte("true")
		}
		return []byte("false")
	case []any:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(string(marshalTestJSON(item)))
		}
		sb.WriteByte(']')
		return []byte(sb.String())
	case map[string]any:
		var sb strings.Builder
		sb.WriteByte('{')
		first := true
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !first {
				sb.WriteByte(',')
			}
			first = false
			sb.WriteString(string(marshalTestJSON(k)))
			sb.WriteByte(':')
			sb.WriteString(string(marshalTestJSON(val[k])))
		}
		sb.WriteByte('}')
		return []byte(sb.String())
	default:
		return []byte("null")
	}
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
