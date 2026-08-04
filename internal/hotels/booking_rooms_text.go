package hotels

// Room-description text extractors, moved verbatim out of booking_rooms.go at
// the seam that file already drew ("--- Description text extractors ---") once
// it passed 800 lines. Everything here is pure string parsing over already
// fetched text: no HTTP, no JSON-LD shape knowledge, no Booking.com specifics
// beyond vocabulary. That is the boundary worth keeping -- booking_rooms.go
// owns fetching and structured-data parsing, this file owns reading prose.

import (
	"regexp"
	"strconv"
	"strings"
)

// --- Description text extractors ---

// bedTypePatterns maps keywords in room names/descriptions to standardized
// bed type strings.
var bedTypeKeywords = []struct {
	keyword string
	bedType string
}{
	// Explicit bed descriptions (most specific first).
	{"king bed", "1 king bed"},
	{"queen bed", "1 queen bed"},
	{"double bed", "1 double bed"},
	{"twin bed", "2 twin beds"},
	{"single bed", "1 single bed"},
	{"bunk bed", "bunk beds"},
	{"sofa bed", "sofa bed"},
	{"king-size", "1 king bed"},
	{"queen-size", "1 queen bed"},
	{"2 single", "2 single beds"},
	{"2 twin", "2 twin beds"},
	{"1 double", "1 double bed"},
	{"1 king", "1 king bed"},
	{"1 queen", "1 queen bed"},
	// Room type names that imply bed type.
	{"double room", "1 double bed"},
	{"twin room", "2 twin beds"},
	{"single room", "1 single bed"},
	{"king suite", "1 king bed"},
	{"king room", "1 king bed"},
	{"queen suite", "1 queen bed"},
	{"queen room", "1 queen bed"},
}

// extractBedType identifies bed type from a room name or description string.
func extractBedType(text string) string {
	lower := strings.ToLower(text)
	for _, bt := range bedTypeKeywords {
		if strings.Contains(lower, bt.keyword) {
			return bt.bedType
		}
	}
	return ""
}

// sizePattern matches room size in square meters, e.g. "35 m²", "28m2", "40 sqm".
var sizePattern = regexp.MustCompile(`(\d+)\s*(?:m²|m2|sqm|sq\.?\s*m)`)

// extractSizeM2 extracts room size in square meters from a description.
func extractSizeM2(text string) float64 {
	m := sizePattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return f
}

// guestPattern matches max guest counts, e.g. "max 4 guests", "sleeps 6",
// "for 2 adults", "accommodates 3".
var guestPattern = regexp.MustCompile(`(?i)(?:max(?:imum)?|sleeps|for|accommodates|up to)\s+(\d+)\s*(?:guests?|adults?|people|persons?)`)

// extractMaxGuests extracts the maximum guest count from a description.
func extractMaxGuests(text string) int {
	m := guestPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 || n > 20 {
		return 0
	}
	return n
}

// roomAmenityKeywords are amenities that can be detected from room
// names and descriptions (not property-level amenities).
var roomAmenityKeywords = []string{
	"balcony", "terrace", "sea view", "ocean view", "mountain view",
	"garden view", "pool view", "city view", "lake view", "river view",
	"minibar", "kitchenette", "kitchen", "air conditioning",
	"bathtub", "jacuzzi", "hot tub", "sauna", "private pool",
	"fireplace", "washing machine", "dishwasher", "oven",
	"coffee machine", "espresso", "microwave", "refrigerator",
	"soundproofing", "blackout curtains", "safe", "desk",
	"private bathroom", "shared bathroom", "en-suite",
	"free wifi", "flat-screen tv", "satellite tv",
	"breakfast included", "all inclusive",
	"parking", "rooftop", "patio", "courtyard",
}

// extractRoomAmenities detects room-level amenities from a text string.
func extractRoomAmenities(text string) []string {
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	var amenities []string
	for _, kw := range roomAmenityKeywords {
		if strings.Contains(lower, kw) {
			amenities = append(amenities, titleCase(kw))
		}
	}
	return amenities
}

func extractCancellationMetadata(text string) (string, *bool, *bool) {
	lower := normalizeRoomMetadataText(text)
	if lower == "" {
		return "", nil, nil
	}
	if strings.Contains(lower, "non-refundable") ||
		strings.Contains(lower, "nonrefundable") ||
		strings.Contains(lower, "no refund") {
		return "non_refundable", boolValue(false), boolValue(false)
	}
	if strings.Contains(lower, "free cancellation") ||
		strings.Contains(lower, "cancel free") ||
		strings.Contains(lower, "free to cancel") {
		return "free_cancellation", boolValue(true), boolValue(true)
	}
	if strings.Contains(lower, "refundable") ||
		strings.Contains(lower, "flexible cancellation") {
		return "refundable", boolValue(true), nil
	}
	return "", nil, nil
}

func extractBoardMetadata(text string) (string, *bool) {
	lower := normalizeRoomMetadataText(text)
	if lower == "" {
		return "", nil
	}
	if strings.Contains(lower, "all inclusive") {
		return "all_inclusive", boolValue(true)
	}
	if strings.Contains(lower, "full board") {
		return "full_board", boolValue(true)
	}
	if strings.Contains(lower, "half board") {
		return "half_board", boolValue(true)
	}
	if strings.Contains(lower, "room only") ||
		strings.Contains(lower, "without breakfast") ||
		strings.Contains(lower, "breakfast not included") ||
		strings.Contains(lower, "no breakfast") ||
		strings.Contains(lower, "no meals") {
		return "room_only", boolValue(false)
	}
	if strings.Contains(lower, "breakfast included") ||
		strings.Contains(lower, "included breakfast") ||
		strings.Contains(lower, "free breakfast") ||
		strings.Contains(lower, "with breakfast") {
		return "breakfast_included", boolValue(true)
	}
	return "", nil
}

func extractTaxesFeesIncluded(text string) *bool {
	lower := normalizeRoomMetadataText(text)
	if lower == "" {
		return nil
	}
	if strings.Contains(lower, "taxes and fees not included") ||
		strings.Contains(lower, "taxes not included") ||
		strings.Contains(lower, "fees not included") ||
		strings.Contains(lower, "excluding taxes") ||
		strings.Contains(lower, "excludes taxes") ||
		strings.Contains(lower, "does not include taxes") {
		return boolValue(false)
	}
	if strings.Contains(lower, "taxes and fees included") ||
		strings.Contains(lower, "taxes included") ||
		strings.Contains(lower, "fees included") ||
		strings.Contains(lower, "including taxes") ||
		strings.Contains(lower, "includes taxes") {
		return boolValue(true)
	}
	return nil
}

func normalizeRoomMetadataText(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	lower = strings.ReplaceAll(lower, "non refundable", "non-refundable")
	return lower
}

func boolValue(v bool) *bool {
	return &v
}

// titleCase capitalizes the first letter of each word in s.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// mergeStringSlices combines two string slices, deduplicating by lowercase.
func mergeStringSlices(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[strings.ToLower(s)] = true
	}
	merged := make([]string, len(a))
	copy(merged, a)
	for _, s := range b {
		if !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			merged = append(merged, s)
		}
	}
	return merged
}
