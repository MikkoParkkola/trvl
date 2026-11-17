package hotels

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// ErrNoNameMatch means the providers answered but no candidate's name matched
// the requested property. Callers show an empty result with a notice instead
// of presenting unrelated hotels as matches.
var ErrNoNameMatch = errors.New("no hotel matched the requested name")

// SearchHotelsByName searches for hotels matching a specific property name across
// all providers (Google Hotels, Trivago, and any configured external providers).
//
// Strategy: use the full "name + location" string as the search query so that
// Google Hotels and other providers return algorithm-ranked results for that
// specific property, then filter client-side to keep only results whose name
// fuzzy-matches the search name. All providers run in parallel.
//
// Parameters:
//   - name: property name to search for (e.g. "CORU House Prague")
//   - location: city/area context (e.g. "Prague") — appended to name for the
//     area search when not already present in the name
//   - checkIn, checkOut: dates in YYYY-MM-DD format
//   - currency: 3-letter ISO code (e.g. "USD", "EUR"); defaults to "USD"
//
// The provider statuses come back with the matches and with a name miss, so a
// caller can tell a complete "not found" from one where providers failed.
func SearchHotelsByName(ctx context.Context, name, location, checkIn, checkOut, currency string) ([]models.HotelResult, []models.ProviderStatus, error) {
	if name == "" {
		return nil, nil, fmt.Errorf("hotel name is required")
	}
	if checkIn == "" || checkOut == "" {
		return nil, nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if currency == "" {
		currency = "USD"
	}

	// Build the search query: use "name, location" so providers treat the name
	// as a disambiguation hint while the location anchors geocoding. If the
	// name already contains the location (case-insensitive), skip appending.
	query := buildNameQuery(name, location)

	opts := HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   2,
		Currency: currency,
		// Single page is enough — we only need enough results to find the named
		// property, and using MaxPages=1 avoids unnecessary rate-limit pressure.
		MaxPages: 1,
	}

	// Run Google Hotels + Trivago + external providers in parallel.
	// Google Hotels is always primary; others are non-fatal.
	type providerResult struct {
		hotels   []models.HotelResult
		statuses []models.ProviderStatus
	}

	resultCh := make(chan providerResult, 3)
	var wg sync.WaitGroup
	// googleErr is written by the Google goroutine and read after wg.Wait.
	var googleErr error

	// Google Hotels — use the full "name + location" query so the algorithm
	// surfaces the specific property.
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := SearchHotels(ctx, query, opts)
		if err != nil {
			slog.Warn("search_by_name: google hotels failed", "error", logredact.Err(err))
			googleErr = fmt.Errorf("google hotels: %w", err)
			resultCh <- providerResult{statuses: []models.ProviderStatus{
				hotelProviderStatusFromError("google_hotels", "Google Hotels", err),
			}}
			return
		}
		// When another provider returned hotels, SearchHotels succeeds and
		// keeps a Google failure only as a provider status.
		if err := googleStatusErr(res); err != nil {
			googleErr = err
		}
		resultCh <- providerResult{hotels: res.Hotels, statuses: res.ProviderStatuses}
	}()

	// Trivago — location-only query (Trivago's API resolves by area, name
	// search is not supported server-side).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if location == "" {
			return
		}
		res, err := SearchTrivago(ctx, location, opts)
		if err != nil {
			slog.Warn("search_by_name: trivago failed", "error", logredact.Err(err))
			resultCh <- providerResult{statuses: []models.ProviderStatus{
				hotelProviderStatusFromError("trivago", "Trivago", err),
			}}
			return
		}
		resultCh <- providerResult{hotels: res, statuses: []models.ProviderStatus{
			hotelProviderStatusFromResults("trivago", "Trivago", len(res)),
		}}
	}()

	// External providers (Booking, Airbnb, Hostelworld, …).
	if eprt := getExternalProviderRuntime(); eprt != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			searchLoc := location
			if searchLoc == "" {
				searchLoc = name
			}
			lat, lon, err := ResolveLocation(ctx, searchLoc)
			if err != nil {
				slog.Warn("search_by_name: geocode failed", "error", logredact.Err(err))
				return
			}
			res, statuses, err := eprt.SearchHotels(ctx, query, lat, lon, checkIn, checkOut, currency, 2, nil)
			if err != nil {
				slog.Warn("search_by_name: external providers failed", "error", logredact.Err(err))
				resultCh <- providerResult{statuses: statuses}
				return
			}
			resultCh <- providerResult{hotels: res, statuses: statuses}
		}()
	}

	wg.Wait()
	close(resultCh)

	// Collect and merge all provider results.
	var batches [][]models.HotelResult
	var statuses []models.ProviderStatus
	for pr := range resultCh {
		if pr.hotels != nil {
			batches = append(batches, pr.hotels)
		}
		statuses = append(statuses, pr.statuses...)
	}
	// The Google search already runs Trivago and the external providers, so
	// the same provider can report twice; keep its best answer.
	statuses = coalesceProviderStatuses(statuses)
	slices.SortFunc(statuses, func(a, b models.ProviderStatus) int { return strings.Compare(a.ID, b.ID) })

	// Normalize before merge so headline adoption is cross-currency safe.
	// Use provided currency (already defaulted to USD by caller) or first observed.
	normTarget := strings.TrimSpace(currency)
	if normTarget == "" {
		for _, b := range batches {
			for _, h := range b {
				if c := strings.TrimSpace(h.Currency); c != "" {
					normTarget = c
					break
				}
			}
			if normTarget != "" {
				break
			}
		}
	}
	if normTarget != "" {
		conv := hotelCurrencyConverter()
		for _, b := range batches {
			normalizeHotelCurrencies(ctx, b, normTarget, conv)
		}
	}

	merged := models.MergeHotelResults(batches...)
	// The merge finalizes without a requested currency; finalize again so the
	// headline is the selected quote converted into the caller's currency.
	models.FinalizeHotelPriceTrust(merged, currency, time.Now(), trustConverter(ctx))
	ensureComparableInTargetCurrency(merged, currency)
	// Zero candidates is a failed or empty search, not a name miss: nothing
	// was matched against, so report the provider outcome instead.
	if len(merged) == 0 {
		return nil, statuses, errors.Join(fmt.Errorf("no results from any provider for %q", name), googleErr)
	}

	// Filter to keep only hotels whose name fuzzy-matches the search name; on a
	// miss, retry once without a trailing location word.
	// Distance only chooses among full-name matches; a stripped-name retry
	// must also sit inside the city radius.
	matched := preferWithinRadius(filterByNameMatch(withoutOtherTowns(merged, name, location), name), name, location)
	if len(matched) == 0 {
		if stripped, eligible, ok := retryWithoutTrailingWord(name, location, merged); ok {
			city := retryCity(name, location)
			matched = filterByNameMatch(withinRadius(withoutOtherTowns(eligible, stripped, city), city), stripped)
		}
	}

	// No match is an answer, not a reason to present unrelated hotels as the
	// requested property. A Google failure travels with it.
	if len(matched) == 0 {
		return nil, statuses, errors.Join(
			fmt.Errorf("%w: %q among %d candidates", ErrNoNameMatch, name, len(merged)),
			googleErr,
		)
	}
	return matched, statuses, nil
}

// buildNameQuery constructs the location query string used for the area search.
// If location is non-empty and not already contained in name, it appends
// ", location" so the search anchors to the right city.
func buildNameQuery(name, location string) string {
	if location == "" {
		return name
	}
	// If location words are already in the name, just use the name as-is.
	if strings.Contains(strings.ToLower(name), strings.ToLower(location)) {
		return name
	}
	return name + ", " + location
}

// filterByNameMatch keeps only hotels whose normalised name contains all
// significant words from the search name (length >= 3, case-insensitive,
// punctuation stripped). E.g. "CORU House" matches "CORU House Prague -
// Design Hotel" but not "Prague Hilton".
func filterByNameMatch(hotels []models.HotelResult, searchName string) []models.HotelResult {
	searchWords := canonicalCityWords(normalizeWords(searchName))
	if len(searchWords) == 0 {
		return hotels
	}

	var out []models.HotelResult
	for _, h := range hotels {
		hotelWords := normalizeWordsSet(h.Name)
		if allWordsPresent(searchWords, hotelWords) {
			out = append(out, h)
		}
	}
	return out
}

// retryWithoutTrailingWord decides whether a name lookup that matched nothing
// may try once more without its last word, as in "ARTIST Boutique Hotel
// Vienna". The word may go when it equals the location argument (every
// candidate stays eligible) or appears in a candidate's address (only those
// candidates stay eligible; HotelResult has no city field). At least two
// words must remain with a location set, three without, so the retry cannot
// collapse to a single generic word.
func retryWithoutTrailingWord(name, location string, candidates []models.HotelResult) (string, []models.HotelResult, bool) {
	words := strings.Fields(name)
	if len(words) < 2 {
		return "", nil, false
	}
	last := hotelNameWords(words[len(words)-1])
	if len(last) != 1 {
		return "", nil, false
	}
	stripped := strings.Join(words[:len(words)-1], " ")
	// Count the words the matcher compares, and require one that identifies
	// the property: "a&o Hotel" or "Hotel -" would otherwise match any hotel.
	minRemaining := 3
	if strings.TrimSpace(location) != "" {
		minRemaining = 2
	}
	if len(normalizeWords(stripped)) < minRemaining || len(meaningfulHotelNameWords(stripped)) == 0 {
		return "", nil, false
	}
	if sameCityWord(last[0], location) {
		return stripped, candidates, true
	}
	var eligible []models.HotelResult
	for _, c := range candidates {
		for _, w := range hotelNameWords(c.Address) {
			if sameCityWord(w, last[0]) {
				eligible = append(eligible, c)
				break
			}
		}
	}
	return stripped, eligible, len(eligible) > 0
}

// normalizeWords returns the hotelNameWords tokens of at least three letters.
func normalizeWords(s string) []string {
	var words []string
	for _, w := range hotelNameWords(s) {
		if len([]rune(w)) >= 3 {
			words = append(words, w)
		}
	}
	return words
}

// normalizeWordsSet returns a set of normalised words from the string, with
// alias-table city words in their shared form.
func normalizeWordsSet(s string) map[string]bool {
	words := canonicalCityWords(normalizeWords(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// allWordsPresent returns true if every word in needles appears in haystack.
func allWordsPresent(needles []string, haystack map[string]bool) bool {
	for _, w := range needles {
		if !haystack[w] {
			return false
		}
	}
	return true
}

// word(s)), search that area, then fuzzy-match the hotel name in results. If that
// fails we fall back to searching the full query as the location.
func SearchHotelByName(ctx context.Context, query string, checkIn, checkOut, currency string) (*models.HotelResult, error) {
	if query == "" {
		return nil, fmt.Errorf("hotel name query is required")
	}
	if checkIn == "" || checkOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if currency == "" {
		currency = "USD"
	}

	opts := HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   2,
		Currency: currency,
	}

	// Build search location candidates: prefer context after comma, then last
	// word. The query is a hotel query, so a candidate carrying the name is
	// searched as a property and never sent to city-scoped providers (#812).
	candidates := locationCandidates(query, LocationHotelQuery)

	var lastErr error
	// googleErr is the latest Google failure seen, which may exist only in a
	// search's provider statuses when another provider returned hotels.
	var googleErr error
	sawResults := false
	searchedProperty := map[string]bool{}
	for i, cand := range candidates {
		// Cooldown between candidates to avoid Google 429 rate limits.
		// Each SearchHotels call makes multiple page requests across sort
		// orders, so sequential calls without delay trigger rate limiting.
		if i > 0 {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		opts.Scope = cand.scope
		if cand.scope == SearchScopeProperty {
			searchedProperty[cand.query] = true
		}
		result, err := SearchHotels(ctx, cand.query, opts)
		if err != nil {
			lastErr = err
			continue
		}
		// The latest Google answer wins (nil when Google answered).
		googleErr = googleStatusErr(result)
		if len(result.Hotels) == 0 {
			continue
		}
		sawResults = true

		match, matchQuery := findBestNameMatch(result.Hotels, query), query
		if match == nil {
			// The text after the last comma, when present, is the location;
			// otherwise only a word found in a candidate's address may go.
			namePart, suffix := query, ""
			if idx := strings.LastIndex(query, ","); idx >= 0 {
				namePart = strings.TrimSpace(query[:idx])
				suffix = strings.TrimSpace(query[idx+1:])
			}
			if stripped, eligible, ok := retryWithoutTrailingWord(namePart, suffix, result.Hotels); ok {
				city := retryCity(namePart, suffix)
				matchQuery = stripped + ", " + city
				match = findBestNameMatch(withinRadius(eligible, city), matchQuery)
			}
		}
		if match != nil {
			// If the match has no Google place ID (none, or another provider's
			// id such as Booking's), ask Google for the property by the
			// original query: Google Hotels reads "Summer Shades Hotel,
			// Naoussa" as one hotel and returns it with its place ID. A query
			// already searched as a property would only repeat that answer;
			// building it from a candidate doubled the name (#812).
			if !hotelIDIsGooglePlaceID(match.HotelID) {
				if !searchedProperty[query] {
					opts.Scope = SearchScopeProperty
					targeted, tErr := SearchHotels(ctx, query, opts)
					if tErr != nil {
						googleErr = tErr
					} else {
						// The latest Google answer wins: one that came back
						// clears an earlier block or refusal.
						googleErr = googleStatusErr(targeted)
						// A Google-ID row must pass the same name and town rules
						// as the match it replaces.
						var google []models.HotelResult
						for _, h := range targeted.Hotels {
							if hotelIDIsGooglePlaceID(h.HotelID) {
								google = append(google, h)
							}
						}
						if again := findBestNameMatch(google, matchQuery); again != nil {
							return again, nil
						}
					}
				}
				// Google refused, so the missing place ID says nothing about
				// Google's index: report the refusal, not a match the rooms
				// lookup cannot use (#813).
				if errors.Is(googleErr, models.ErrRateLimited) {
					return nil, fmt.Errorf("found %q, but Google Hotels refused requests, so its Google place ID is unknown; retry once Google's cooldown ends: %w", match.Name, googleErr)
				}
			}
			return match, nil
		}

		// Area search succeeded but no name match — try next location candidate
		// instead of returning a random first result. Returning the wrong
		// hotel causes the caller to fetch room data for a completely
		// different property, which is worse than failing.
		continue
	}

	if lastErr != nil && !sawResults {
		return nil, fmt.Errorf("hotel name search: %w", lastErr)
	}
	// Google's refusal travels with a miss: the hotel may be the one Google
	// would have returned (#813).
	return nil, errors.Join(fmt.Errorf("%w: %q", ErrNoNameMatch, query), lastErr, googleErr)
}

// buildLocationCandidates generates location search strings from a hotel name query.
// E.g. "Hotel Lutetia, Paris" -> ["Paris", "Hotel Lutetia Paris", "Hotel Lutetia, Paris"]
// "Summer Shades hotel, Naoussa, Paros" -> ["Paros", "Naoussa, Paros", "Summer Shades hotel, Naoussa Paros", "Summer Shades hotel, Naoussa, Paros"]
func buildLocationCandidates(query string) []string {
	var candidates []string

	// Find all comma-separated parts and generate progressively broader
	// location candidates. For "name, area, city":
	//   ["city", "area, city", "name area city", "name, area, city"]
	parts := strings.Split(query, ",")
	if len(parts) >= 2 {
		// Add location suffixes: start from the rightmost part, build up
		for i := len(parts) - 1; i >= 1; i-- {
			loc := strings.TrimSpace(strings.Join(parts[i:], ","))
			if loc != "" {
				candidates = append(candidates, loc)
			}
		}

		// Also try "before after" as the full query (no comma).
		before := strings.TrimSpace(parts[0])
		after := strings.TrimSpace(parts[len(parts)-1])
		if before != "" && after != "" {
			candidates = append(candidates, before+" "+after)
		}
	}

	// Try the full query as location (works when it contains a city).
	candidates = append(candidates, query)

	return candidates
}

// findBestNameMatch searches hotels for the best fuzzy match to the query.
// The query may include location context after a comma (e.g. "Makis Place, Mykonos").
// Only the part before the comma is used for name matching, so location words
// don't cause false matches with unrelated hotels.
func findBestNameMatch(hotels []models.HotelResult, query string) *models.HotelResult {
	// Strip location context: only match against the part before the last comma.
	// City words compare through the alias table, and a candidate naming a
	// town other than the comma-suffix city is not the property.
	namePart, city := query, ""
	if idx := strings.LastIndex(query, ","); idx >= 0 {
		namePart = strings.TrimSpace(query[:idx])
		city = strings.TrimSpace(query[idx+1:])
	}
	queryWords := canonicalCityWords(meaningfulHotelNameWords(namePart))
	if len(queryWords) == 0 {
		return nil
	}
	queryNormalized := strings.Join(canonicalCityWords(hotelNameWords(namePart)), " ")
	conflicts := townConflict(namePart, city)
	beyond := beyondCityRadius(city)

	bestIdx := -1
	bestScore := 0
	bestIDRank := -1
	bestWithin := false

	for i := range hotels {
		if conflicts != nil && conflicts(hotels[i]) {
			continue
		}
		// Outside the city radius only a name adding nothing to the query
		// may match; distance then just ranks it below matches inside.
		within := beyond == nil || !beyond(hotels[i])
		if !within && addsWords(hotels[i].Name, namePart+" "+city) {
			continue
		}
		candidateWords := canonicalCityWords(hotelNameWords(hotels[i].Name))
		candidateSet := make(map[string]struct{}, len(candidateWords))
		for _, word := range candidateWords {
			candidateSet[word] = struct{}{}
		}

		score := 0
		candidateNormalized := strings.Join(candidateWords, " ")
		// A token-bounded phrase match scores highest. Padding prevents a short
		// property name such as "Mare" from matching "Rivamare".
		if strings.Contains(" "+candidateNormalized+" ", " "+queryNormalized+" ") {
			score = 100
		} else {
			// Every meaningful query word must be present as a complete token.
			// A single shared generic or brand fragment is not property identity.
			allPresent := true
			for _, w := range queryWords {
				if _, ok := candidateSet[w]; !ok {
					allPresent = false
					break
				}
			}
			if allPresent {
				score = len(queryWords) * 10
			}
		}

		if score == 0 {
			continue
		}

		// Tie-break by ID usability so selection is deterministic and the rooms
		// flow gets a resolvable target. The same query previously returned a
		// usable Google place ID or an empty-ID OTA entry depending on the
		// arbitrary order of equally-named results, intermittently aborting the
		// upstream rooms lookup ("found but has no Google ID"). Prefer, in order:
		// higher name score, then a genuine Google place ID over any other ID
		// over no ID at all (mirrors the #288 candidate-ranking doctrine), then a
		// stable lexical key so two equally-usable results resolve identically
		// regardless of input ordering.
		// A match inside the city radius beats any outside it; distance
		// never removes the only match.
		idRank := hotelIDRank(hotels[i].HotelID)
		if bestIdx == -1 || (within && !bestWithin) || (within == bestWithin && nameMatchBetter(
			score, idRank, hotels[i],
			bestScore, bestIDRank, hotels[bestIdx],
		)) {
			bestIdx, bestScore, bestIDRank, bestWithin = i, score, idRank, within
		}
	}

	if bestIdx == -1 {
		return nil
	}
	return &hotels[bestIdx]
}

var genericHotelNameWords = map[string]struct{}{
	"and":        {},
	"apartment":  {},
	"apartments": {},
	"hostel":     {},
	"hotel":      {},
	"hotels":     {},
	"inn":        {},
	"resort":     {},
	"resorts":    {},
	"the":        {},
}

// hotelNameWords lowercases a property name and splits punctuation-delimited
// names such as "Ritz-Carlton" into exact comparable tokens.
func hotelNameWords(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// meaningfulHotelNameWords removes category words that cannot establish
// property identity on their own.
func meaningfulHotelNameWords(name string) []string {
	words := hotelNameWords(name)
	filtered := words[:0]
	for _, word := range words {
		if len([]rune(word)) < 3 {
			continue
		}
		if _, generic := genericHotelNameWords[word]; generic {
			continue
		}
		filtered = append(filtered, word)
	}
	return filtered
}

// nameMatchBetter reports whether candidate (cScore, cIDRank, c) is a strictly
// better name-match selection than the incumbent (bScore, bIDRank, b). Ordering
// is total and order-independent: name score, then ID usability rank, then a
// lexical tie-break on HotelID and Name so the winner is deterministic.
func nameMatchBetter(
	cScore, cIDRank int, c models.HotelResult,
	bScore, bIDRank int, b models.HotelResult,
) bool {
	if cScore != bScore {
		return cScore > bScore
	}
	if cIDRank != bIDRank {
		return cIDRank > bIDRank
	}
	if c.HotelID != b.HotelID {
		return c.HotelID < b.HotelID
	}
	return c.Name < b.Name
}

// hotelIDRank ranks a HotelID by how usable it is as a Google rooms-RPC target:
// 2 = genuine Google place ID, 1 = some other non-empty ID, 0 = no ID at all.
// The rooms flow can only resolve Google place IDs, so a result carrying one
// must outrank an equally-named OTA/apartment entry whose ID (or absence of one)
// would dead-end the lookup.
func hotelIDRank(id string) int {
	switch {
	case hotelIDIsGooglePlaceID(id):
		return 2
	case strings.TrimSpace(id) != "":
		return 1
	default:
		return 0
	}
}

// hotelIDIsGooglePlaceID reports whether a HotelID is a genuine Google place ID
// (prefixed "/g/" or "ChIJ") that the rooms RPC can resolve. Mirrors
// looksLikeGoogleHotelID / accommodationHotelIDIsGooglePlaceID used elsewhere.
func hotelIDIsGooglePlaceID(id string) bool {
	return models.IsGoogleHotelID(id)
}

// enrichHotelAmenities fetches detail pages for the top N hotels to get full
// amenity lists. Runs up to 3 concurrent fetches. Hotels without a HotelID
// are skipped. Failures are silently ignored (search results still have
// partial amenities from the search page).
func enrichHotelAmenities(ctx context.Context, hotels []models.HotelResult, limit int) []models.HotelResult {
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	// Collect indices of hotels eligible for enrichment.
	var indices []int
	for i := range hotels {
		if hotels[i].HotelID != "" && len(indices) < limit {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return hotels
	}

	// Fetch detail pages in parallel with concurrency limit of 3.
	const concurrency = 3
	type result struct {
		index     int
		amenities []string
	}

	results := make(chan result, len(indices))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for _, idx := range indices {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			amenities, err := FetchHotelAmenities(ctx, hotels[i].HotelID)
			if err != nil || len(amenities) == 0 {
				return
			}
			results <- result{index: i, amenities: amenities}
		}(idx)
	}

	// Close results channel when all goroutines complete.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Apply enriched amenities back to hotels.
	for r := range results {
		hotels[r.index].Amenities = mergeAmenities(hotels[r.index].Amenities, r.amenities)
	}

	return hotels
}

// propertyTypeCode converts a human-readable property type string to the
// Google Hotels &ptype= parameter value. Returns "" if the type is unknown
// or empty (meaning: no filter applied).
//
// Known Google Hotels ptype values (reverse-engineered):
//
//	2 = hotel, 3 = apartment, 4 = hostel, 5 = resort, 7 = bnb, 8 = villa
func propertyTypeCode(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "hotel":
		return "2"
	case "apartment":
		return "3"
	case "hostel":
		return "4"
	case "resort":
		return "5"
	case "bnb", "bed_and_breakfast", "bed and breakfast":
		return "7"
	case "villa":
		return "8"
	default:
		return ""
	}
}

// mergeAmenities combines two amenity lists, deduplicating by lowercase name.
// The first list's items take priority in ordering.
// tagHotelSource stamps each hotel with a PriceSource for the given provider
// so that MergeHotelResults can track per-provider prices. Hotels that already
// carry Sources (e.g. from a previous enrichment pass) are left unchanged.
func tagHotelSource(hotels []models.HotelResult, provider string) []models.HotelResult {
	tagged := make([]models.HotelResult, len(hotels))
	copy(tagged, hotels)
	for i := range tagged {
		if len(tagged[i].Sources) == 0 {
			tagged[i].Sources = []models.PriceSource{{
				Provider:   provider,
				Price:      tagged[i].Price,
				Currency:   tagged[i].Currency,
				BookingURL: tagged[i].BookingURL,
			}}
		}
	}
	return tagged
}

func mergeAmenities(existing, additional []string) []string {
	seen := make(map[string]bool, len(existing)+len(additional))
	var merged []string

	for _, a := range existing {
		lower := strings.ToLower(a)
		if !seen[lower] {
			seen[lower] = true
			merged = append(merged, a)
		}
	}
	for _, a := range additional {
		lower := strings.ToLower(a)
		if !seen[lower] {
			seen[lower] = true
			merged = append(merged, a)
		}
	}

	return merged
}
