// Package preferences manages user preferences stored in ~/.trvl/preferences.json.
// Preferences are optional — if the file is missing, Default() is returned and
// no existing behaviour changes.
package preferences

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Preferences holds all personal travel preferences for the user.
type Preferences struct {
	// Identity
	HomeAirports []string `json:"home_airports"` // e.g. ["HEL", "AMS"]
	HomeCities   []string `json:"home_cities"`   // e.g. ["Helsinki", "Amsterdam"]

	// Travel style
	CarryOnOnly  bool `json:"carry_on_only"` // affects route hacks
	PreferDirect bool `json:"prefer_direct"` // flight stops preference

	// Accommodation
	NoDormitories  bool    `json:"no_dormitories"`   // exclude hostels with shared rooms
	EnSuiteOnly    bool    `json:"ensuite_only"`     // require own bathroom
	FastWifiNeeded bool    `json:"fast_wifi_needed"` // co-working capable
	MinHotelStars  int     `json:"min_hotel_stars"`  // 0 = any
	MinHotelRating float64 `json:"min_hotel_rating"` // 0-10 scale, e.g. 8.0

	// Preferred districts/neighborhoods per city.
	// e.g. {"Prague": ["Prague 1", "Prague 2"], "Helsinki": ["Kallio", "Punavuori"]}
	PreferredDistricts map[string][]string `json:"preferred_districts,omitempty"`

	// Currency & locale
	DisplayCurrency string `json:"display_currency"` // "EUR"
	Locale          string `json:"locale"`           // "en-FI"

	// Loyalty programmes
	LoyaltyAirlines       []string              `json:"loyalty_airlines,omitempty"`        // IATA codes, e.g. ["KL", "AY"]
	LoyaltyHotels         []string              `json:"loyalty_hotels,omitempty"`          // e.g. ["Marriott Bonvoy", "IHG"]
	FrequentFlyerPrograms []FrequentFlyerStatus `json:"frequent_flyer_programs,omitempty"` // alliance status tiers
	LoungeCards           []string              `json:"lounge_cards,omitempty"`            // e.g. ["Priority Pass", "Diners Club"]

	// Travel style (extended)
	DefaultCompanions int      `json:"default_companions"`          // 0 = solo, 1 = couple, 2+ = family/group
	TripTypes         []string `json:"trip_types,omitempty"`        // "city_break", "beach", "adventure", "business", "remote_work"
	SeatPreference    string   `json:"seat_preference"`             // "window", "aisle", "no_preference"

	// Budget
	BudgetPerNightMin float64 `json:"budget_per_night_min"` // min acceptable hotel price (filters too-cheap-to-trust)
	BudgetPerNightMax float64 `json:"budget_per_night_max"` // max hotel price per night
	BudgetFlightMax   float64 `json:"budget_flight_max"`    // max one-way flight price
	DealTolerance     string  `json:"deal_tolerance"`       // "price", "comfort", "balanced"

	// Flight preferences
	FlightTimeEarliest string `json:"flight_time_earliest"` // "06:00" — won't take flights before this
	FlightTimeLatest   string `json:"flight_time_latest"`   // "23:00" — won't take flights after this
	RedEyeOK           bool   `json:"red_eye_ok"`           // overnight flights acceptable?

	// Identity
	Nationality string   `json:"nationality"`          // ISO 3166-1 alpha-2 (e.g. "FI") — for visa warnings
	Languages   []string `json:"languages,omitempty"`   // spoken languages (e.g. ["en", "fi", "sv"])

	// Context (free-text, not filtered but used for personalization)
	PreviousTrips       []string `json:"previous_trips,omitempty"`       // cities/countries visited
	BucketList          []string `json:"bucket_list,omitempty"`          // dream destinations
	ActivityPreferences []string `json:"activity_preferences,omitempty"` // "museums", "nightlife", "food", "nature", etc.
	DietaryNeeds        []string `json:"dietary_needs,omitempty"`        // "vegetarian", "halal", "gluten_free", etc.
	Notes               string   `json:"notes,omitempty"`                // free-text for anything else

	// Family members for booking on behalf of
	FamilyMembers []FamilyMember `json:"family_members,omitempty"`

	// Multi-airport expansion: airports close enough to home to consider as
	// origin alternatives. Keyed by home airport IATA code.
	// e.g. {"AMS":["EIN"],"HEL":["TKU","TMP","TLL","ARN"]}
	NearbyAirports map[string][]string `json:"nearby_airports,omitempty"`

	// EarlyConnectionFloor is the earliest acceptable departure time (HH:MM)
	// after an overnight layover (≥8h). Default "10:00" per Mikko's travel
	// mental model ("unhurried wake + breakfast" rule). Per-user configurable.
	EarlyConnectionFloor string `json:"early_connection_floor,omitempty"`

	// AamuyoFloor is the deprecated alias for EarlyConnectionFloor, kept for
	// backwards compatibility with profiles written before the rename.
	//
	// Deprecated: Use EarlyConnectionFloor instead.
	AamuyoFloor string `json:"aamuyo_floor,omitempty"`

	// AirportAffinity tracks how often each origin airport has won a search.
	// Key is the IATA code; value is the win count (capped at 100).
	// Built up automatically by RecordWinningOrigin when the user omits the
	// origin argument and lets trvl pick defaults. Empty by default; existing
	// preferences files without this field deserialise to a nil map (back-compat
	// guaranteed by omitempty).
	//
	// Decay is not implemented in v1. When affinity diverges from current
	// travel patterns (e.g. after a city move), the user can clear this field
	// from preferences.json or set individual values to 0.
	AirportAffinity map[string]int `json:"airport_affinity,omitempty"`
}

// FamilyMember represents a person the user may book travel for.
type FamilyMember struct {
	Name         string `json:"name"`
	Relationship string `json:"relationship"` // "father", "spouse", etc.
	Notes        string `json:"notes"`        // free-form preferences
}

// FrequentFlyerStatus records a user's loyalty tier in an airline alliance
// or with a specific carrier.
//
// Alliance is the alliance name: "oneworld", "skyteam", or "star_alliance"
// (case-insensitive). Tier is the status level within that alliance, e.g.
// "ruby", "sapphire", "emerald" (Oneworld), "elite", "elite_plus" (SkyTeam),
// "silver", "gold" (Star Alliance).
//
// AirlineCode is optional: if set (IATA code, e.g. "BA", "AY") the status is
// treated as belonging to that specific carrier for benefit look-up purposes
// when the flight's airline matches exactly.
type FrequentFlyerStatus struct {
	Alliance     string `json:"alliance"`                // "oneworld", "skyteam", "star_alliance"
	Tier         string `json:"tier"`                    // tier name, e.g. "gold", "sapphire"
	AirlineCode  string `json:"airline_code,omitempty"`  // optional specific IATA carrier code
	MilesBalance int    `json:"miles_balance,omitempty"` // current miles/points balance
	ProgramName  string `json:"program_name,omitempty"`  // e.g. "Flying Blue", "Royal Plus"
}

// defaultPath returns the canonical preferences file path (~/.trvl/preferences.json).
func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".trvl", "preferences.json"), nil
}

// Default returns sensible zero-value preferences.
// These are used when no preferences file exists.
func Default() *Preferences {
	return &Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
	}
}

// Load reads preferences from ~/.trvl/preferences.json.
// If the file does not exist, Default() is returned with no error.
func Load() (*Preferences, error) {
	path, err := defaultPath()
	if err != nil {
		return Default(), nil
	}
	return LoadFrom(path)
}

// LoadFrom reads preferences from an explicit file path.
// If the file does not exist, Default() is returned with no error.
func LoadFrom(path string) (*Preferences, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read preferences: %w", err)
	}
	if len(data) == 0 {
		return Default(), nil
	}

	p := Default()
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parse preferences: %w", err)
	}

	// Migrate: rating scale changed from 0-5 to 0-10 in v0.6.5.
	// If the value looks like the old 0-5 scale, double it.
	if p.MinHotelRating > 0 && p.MinHotelRating <= 5 {
		p.MinHotelRating *= 2
	}

	// Default-populate NearbyAirports when not set. These defaults match
	// Mikko's two-base lifestyle (AMS primary, HEL secondary) from the
	// travel mental model document.
	if len(p.NearbyAirports) == 0 {
		p.NearbyAirports = defaultNearbyAirports()
	}

	// Migrate legacy AamuyoFloor → EarlyConnectionFloor, default when neither set.
	if p.EarlyConnectionFloor == "" && p.AamuyoFloor != "" {
		p.EarlyConnectionFloor = p.AamuyoFloor
	}
	if p.EarlyConnectionFloor == "" {
		p.EarlyConnectionFloor = "10:00"
	}
	// Keep AamuyoFloor in sync for legacy readers.
	if p.AamuyoFloor == "" {
		p.AamuyoFloor = p.EarlyConnectionFloor
	}

	return p, nil
}

// Save writes preferences to ~/.trvl/preferences.json atomically.
func Save(p *Preferences) error {
	path, err := defaultPath()
	if err != nil {
		return err
	}
	return SaveTo(path, p)
}

// SaveTo writes preferences to an explicit file path atomically.
func SaveTo(path string, p *Preferences) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create preferences dir: %w", err)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode preferences: %w", err)
	}

	// Atomic write: write to temp file, rename.
	// Use a cryptographically random suffix and O_CREATE|O_EXCL at mode 0600
	// so the file is never readable by other users even momentarily (avoids
	// the TOCTOU window between CreateTemp and Chmod).
	dir := filepath.Dir(path)
	rndBytes := make([]byte, 8)
	if _, err := rand.Read(rndBytes); err != nil {
		return fmt.Errorf("generate temp name: %w", err)
	}
	tmpPath := filepath.Join(dir, filepath.Base(path)+".tmp-"+hex.EncodeToString(rndBytes))
	//nolint:gosec // mode 0600 is intentional — preferences file must be owner-only
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	cleanup = false
	return nil
}

// HomeAirport returns the first configured home airport, or "" if none.
func (p *Preferences) HomeAirport() string {
	if len(p.HomeAirports) > 0 {
		return p.HomeAirports[0]
	}
	return ""
}

// DistrictsFor returns the preferred districts for the given city (case-insensitive).
func (p *Preferences) DistrictsFor(city string) []string {
	if p.PreferredDistricts == nil {
		return nil
	}
	// Exact match first.
	if d, ok := p.PreferredDistricts[city]; ok {
		return d
	}
	// Case-insensitive fallback.
	cityLower := lowerStr(city)
	for k, v := range p.PreferredDistricts {
		if lowerStr(k) == cityLower {
			return v
		}
	}
	return nil
}

func lowerStr(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// FilterHotels applies preference-based post-filters to a hotel list.
//
// Applied filters:
//   - NoDormitories: removes properties whose name or description suggests shared rooms
//   - EnSuiteOnly: removes properties that appear to lack a private bathroom
//   - Preferred districts: deprioritises (moves to end) hotels not in preferred districts
//     for the given city when preferences exist for that city
//
// The function always returns a valid (possibly empty) slice and never mutates
// the input. It is a no-op when p is nil.
func FilterHotels(hotels []models.HotelResult, city string, p *Preferences) []models.HotelResult {
	if p == nil {
		return hotels
	}

	// Minimum review count threshold: when the user cares about quality
	// (MinHotelRating >= 8.0), a hotel with <20 reviews isn't enough data
	// to trust. This catches new listings and obscure guesthouses.
	minReviews := 0
	if p.MinHotelRating >= 8.0 {
		minReviews = 20
	}

	out := make([]models.HotelResult, 0, len(hotels))
	for _, h := range hotels {
		if p.NoDormitories && isDormitory(h) {
			continue
		}
		if p.EnSuiteOnly && lacksPrivateBathroom(h) {
			continue
		}
		// Drop low-review properties when quality matters.
		if minReviews > 0 && h.ReviewCount > 0 && h.ReviewCount < minReviews && !models.HasExternalProviderSource(h) {
			continue
		}
		// If the user wants min rating but the property has no reviews at all,
		// drop it — we can't verify quality.
		if p.MinHotelRating > 0 && h.ReviewCount == 0 && h.Rating == 0 && !models.HasExternalProviderSource(h) {
			continue
		}
		// Drop suspiciously cheap hotels when BudgetPerNightMin is set.
		if p.BudgetPerNightMin > 0 && h.Price > 0 && h.Price < p.BudgetPerNightMin {
			continue
		}
		out = append(out, h)
	}

	// Preferred districts: filter or prioritise by neighborhood.
	//
	// Behavior:
	//   - User-defined districts: strict filter (user chose these explicitly)
	//   - Curated defaults: prioritise matches to the front but don't
	//     filter out non-matches entirely, since hotel addresses are
	//     often empty or truncated in Google results and we would lose
	//     everything. Cheap trick: use negative keywords instead to drop
	//     obvious airport/suburb hotels.
	districts := p.DistrictsFor(city)
	if len(districts) > 0 {
		filtered, matched := filterByDistrict(out, districts)
		if matched {
			out = filtered
		}
	} else if defaults := DefaultDistrictsFor(city); len(defaults) > 0 {
		// Prioritise matches, then drop obvious airport/suburb patterns.
		out = prioritiseByDistrict(out, defaults)
		out = dropAirportAndSuburbHotels(out, city)
	}

	return out
}

// dormKeywords are substrings that indicate shared-room or sub-hotel
// accommodation. Includes generic terms, known hostel chains, and
// private-room / guesthouse patterns that typically come with a single room
// instead of a full hotel experience.
var dormKeywords = []string{
	// Generic terms
	"hostel", "dorm", "dormitory", "capsule", "pod hotel", "bunk",
	"youth hostel", "backpacker",
	// Room-listing patterns (private rooms in guesthouses, not real hotels)
	"rooms in ",
	"private room",
	"shared room",
	"- double room",
	"- single room",
	"- twin room",
	"- triple room",
	"double room in",
	"room in ",
	"rooming house",
	"guesthouse",
	"guest house",
	// Known hostel/hybrid chains (lowercase substring match)
	"st christopher", // St Christopher's Inn
	"generator ",     // Generator Hostels (trailing space to avoid "generator hotel" false positives)
	"meininger",      // MEININGER Hotels (hybrid hostel/hotel)
	"wombat",         // Wombats Hostels
	"clink",          // Clink Hostels
	"safestay",       // Safestay
	"yha ",           // YHA hostels
	"nomad cave",     // Nomad Cave (budget shared-room)
	"nomad city",     // Nomad City (budget shared-room)
	"a&o",            // A&O Hotels and Hostels
	"rygerfjord",     // Rygerfjord (Stockholm hostel boat)
	"citybox",        // Citybox (Nordic budget self-service, shared kitchens)
	"travelodge",     // Travelodge (debatable — UK budget chain, but avg < 4★ experience)
}

// subListingPattern matches hotel names that are actually sub-listings of a
// bigger property, such as:
//
//	"Main Square Rooms - Small Double Room"
//	"Fantastic Inn Kraków - Krasińskiego"     (multiple branches of one inn)
//	"Name - Economy Twin Room"
//
// These are usually guest rooms or apartments rather than real hotels, and
// they clog up results when sorting by price.
var subListingPattern = regexp.MustCompile(`(?i)\s-\s[^-]*\b(room|apartment|studio|suite|double|single|twin|triple|dorm)\b`)

// isDormitory returns true when the hotel name or amenities suggest shared sleeping.
func isDormitory(h models.HotelResult) bool {
	combined := strings.ToLower(h.Name + " " + h.Address)
	for _, kw := range dormKeywords {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	if subListingPattern.MatchString(h.Name) {
		return true
	}
	// Also check amenity strings.
	for _, a := range h.Amenities {
		aLow := strings.ToLower(a)
		if strings.Contains(aLow, "shared room") || strings.Contains(aLow, "dorm") {
			return true
		}
	}
	return false
}

// bathroomKeywords indicate that a hotel explicitly shares bathrooms.
var sharedBathroomKeywords = []string{
	"shared bathroom", "shared bath", "shared facilities",
	"communal bathroom", "common bathroom",
}

// lacksPrivateBathroom returns true when the hotel signals it does NOT have
// a private bathroom. If there is no evidence either way, returns false (keep
// the hotel — don't falsely exclude).
func lacksPrivateBathroom(h models.HotelResult) bool {
	combined := strings.ToLower(h.Name + " " + h.Address)
	for _, a := range h.Amenities {
		combined += " " + strings.ToLower(a)
	}

	for _, kw := range sharedBathroomKeywords {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// airportSuburbKeywords are substrings in hotel names/addresses that indicate
// an airport or distant-suburb location — exactly what leisure travelers
// do NOT want when visiting a city.
var airportSuburbKeywords = []string{
	"airport", "aéroport", "aeroport",
	// Known airport-area districts
	"villepinte", "roissy", "tremblay-en-france",
	"orly", "rungis", "massy",
	"stansted", "luton", "gatwick",
	"schiphol", "hoofddorp",
	"fiumicino",
	"vantaa airport",
	"barajas",
	"zaventem",
	"malpensa",
}

// dropAirportAndSuburbHotels removes hotels whose name contains obvious
// airport or distant-suburb markers. Conservative — only the most blatant
// cases are dropped to avoid false positives.
func dropAirportAndSuburbHotels(hotels []models.HotelResult, city string) []models.HotelResult {
	out := make([]models.HotelResult, 0, len(hotels))
	for _, h := range hotels {
		combined := strings.ToLower(h.Name + " " + h.Address)
		drop := false
		for _, kw := range airportSuburbKeywords {
			if strings.Contains(combined, kw) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, h)
		}
	}
	// Never return empty — if everything was dropped, return the original.
	if len(out) == 0 {
		return hotels
	}
	return out
}

// filterByDistrict keeps only hotels that match one of the given districts
// (by substring in name or address). Returns (filtered, anyMatched).
// anyMatched is true when at least one hotel survived the filter.
func filterByDistrict(hotels []models.HotelResult, districts []string) ([]models.HotelResult, bool) {
	out := make([]models.HotelResult, 0, len(hotels))
	for _, h := range hotels {
		combined := strings.ToLower(h.Name + " " + h.Address)
		for _, d := range districts {
			if strings.Contains(combined, strings.ToLower(d)) {
				out = append(out, h)
				break
			}
		}
	}
	return out, len(out) > 0
}

// affinityMaxScore is the ceiling on airport affinity scores. Prevents one
// runaway route from drowning all others if the user does the same search
// many times in a short period.
const affinityMaxScore = 100

// railFlyOrigins is the set of rail+fly airports reachable from AMS. When one
// of these wins a search, RecordWinningOrigin also adds it to NearbyAirports
// so future searches that start from AMS automatically include it.
var railFlyOrigins = map[string]bool{"ZYR": true, "ANR": true, "BRU": true}

// RecordWinningOrigin increments the affinity score for the given IATA code
// in the user's preferences and persists the change. The call is idempotent
// and safe to fire-and-forget (non-fatal callers can log the error).
//
// Side-effect: when iata is a rail+fly airport (ZYR/ANR/BRU) and AMS is a
// home airport, it is also added to NearbyAirports["AMS"] so that future
// home-fan expansions include it without needing affinity to reach threshold 3.
//
// Scores are capped at affinityMaxScore (100).
func RecordWinningOrigin(iata string) error {
	iata = strings.ToUpper(strings.TrimSpace(iata))
	if iata == "" {
		return nil
	}

	p, err := Load()
	if err != nil {
		return fmt.Errorf("load preferences for affinity update: %w", err)
	}

	// Initialise map on first write — safe even on a Default() prefs with nil map.
	if p.AirportAffinity == nil {
		p.AirportAffinity = make(map[string]int)
	}

	score := p.AirportAffinity[iata] + 1
	if score > affinityMaxScore {
		score = affinityMaxScore
	}
	p.AirportAffinity[iata] = score

	// If this is a rail+fly origin and AMS is a home airport, also register
	// it as a nearby airport so it participates in fan-out unconditionally.
	if railFlyOrigins[iata] {
		for _, home := range p.HomeAirports {
			if strings.ToUpper(strings.TrimSpace(home)) == "AMS" {
				if p.NearbyAirports == nil {
					p.NearbyAirports = make(map[string][]string)
				}
				already := false
				for _, nb := range p.NearbyAirports["AMS"] {
					if strings.ToUpper(strings.TrimSpace(nb)) == iata {
						already = true
						break
					}
				}
				if !already {
					p.NearbyAirports["AMS"] = append(p.NearbyAirports["AMS"], iata)
				}
				break
			}
		}
	}

	return Save(p)
}

// defaultNearbyAirports returns the built-in nearby-airport seed based on
// Mikko's two-base lifestyle (Amsterdam + Helsinki). Keys are home-airport
// IATA codes; values are airports close enough to justify searching.
//
// Source: travel_search_mental_model.md — MULTI-AIRPORT ORIGIN SPREAD section.
func defaultNearbyAirports() map[string][]string {
	return map[string][]string{
		"AMS": {"EIN"},
		"HEL": {"TKU", "TMP", "TLL", "ARN"},
	}
}

// NearbyAirportsFor returns the nearby airports for a given home airport.
// It also includes the home airport itself. The result is deduplicated.
// Returns a slice containing at minimum the home airport itself.
func (p *Preferences) NearbyAirportsFor(homeAirport string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(code string) {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code != "" && !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	add(homeAirport)
	for _, nb := range p.NearbyAirports[strings.ToUpper(strings.TrimSpace(homeAirport))] {
		add(nb)
	}
	return out
}

// ExpandHomeOrigins returns the full set of origin airports to search when
// --home-fan is enabled: all home airports plus their nearby airports.
// Duplicates are deduplicated.
func (p *Preferences) ExpandHomeOrigins() []string {
	seen := make(map[string]bool)
	var out []string
	add := func(code string) {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code != "" && !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	for _, home := range p.HomeAirports {
		add(home)
		for _, nb := range p.NearbyAirports[strings.ToUpper(strings.TrimSpace(home))] {
			add(nb)
		}
	}
	return out
}

// prioritiseByDistrict reorders hotels so those whose address contains one of
// the preferred district strings appear first. Order within each group is
// preserved.
func prioritiseByDistrict(hotels []models.HotelResult, districts []string) []models.HotelResult {
	var preferred, rest []models.HotelResult
	for _, h := range hotels {
		addrLow := strings.ToLower(h.Address)
		matched := false
		for _, d := range districts {
			if strings.Contains(addrLow, strings.ToLower(d)) {
				matched = true
				break
			}
		}
		if matched {
			preferred = append(preferred, h)
		} else {
			rest = append(rest, h)
		}
	}
	return append(preferred, rest...)
}
