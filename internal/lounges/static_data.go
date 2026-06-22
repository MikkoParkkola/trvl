package lounges

// staticData holds curated lounge data for the top-30 hub airports.
// Cards use the same name conventions as preferences.LoungeCards so
// AnnotateAccess can match them without fuzzy logic.
//
// Sources: Priority Pass directory, LoungeKey directory, airport operator
// websites, and published lounge reviews (2024–2025 data).
//
// The underlying data is sharded across sibling files (static_data_*.go) to
// keep each file small; staticData is assembled from those regional maps at
// package init so the exact same final data set is preserved.
var staticData = func() map[string][]staticLounge {
	merged := make(map[string][]staticLounge)
	for _, shard := range []map[string][]staticLounge{
		staticDataEurope,
		staticDataMideastAsia,
		staticDataAsiaPacAmericas,
	} {
		for code, lounges := range shard {
			merged[code] = lounges
		}
	}
	return merged
}()
