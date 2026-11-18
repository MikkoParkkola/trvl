package models

// CityAliases maps common English city names to the form that Google
// Hotels actually resolves correctly. Without this, "Prague" returns zero
// results while "Praha" works fine.
var CityAliases = map[string]string{
	"prague":     "Praha",
	"munich":     "München",
	"vienna":     "Wien",
	"cologne":    "Köln",
	"copenhagen": "København",
	"warsaw":     "Warszawa",
	"bucharest":  "București",
	"gothenburg": "Göteborg",
	"nuremberg":  "Nürnberg",
}
