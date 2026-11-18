package hotels

import "strings"

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
