package route

import (
	"math"
	"sort"
	"strings"
)

// Hub is a transfer city that connects multiple transport modes.
type Hub struct {
	City     string   // Canonical city name
	Airports []string // IATA codes
	Lat      float64  // Latitude
	Lon      float64  // Longitude
	HasFerry bool     // true if the city has ferry terminal connections
}

// hubs is the static list of European transfer hub cities.
// Each hub appears in 2+ provider station databases and has an airport.
var hubs = []Hub{
	{City: "Vienna", Airports: []string{"VIE"}, Lat: 48.2082, Lon: 16.3738},
	{City: "Berlin", Airports: []string{"BER"}, Lat: 52.5200, Lon: 13.4050},
	{City: "Munich", Airports: []string{"MUC"}, Lat: 48.1351, Lon: 11.5820},
	{City: "Frankfurt", Airports: []string{"FRA"}, Lat: 50.1109, Lon: 8.6821},
	{City: "Prague", Airports: []string{"PRG"}, Lat: 50.0755, Lon: 14.4378},
	{City: "Budapest", Airports: []string{"BUD"}, Lat: 47.4979, Lon: 19.0402},
	{City: "Amsterdam", Airports: []string{"AMS"}, Lat: 52.3676, Lon: 4.9041},
	{City: "Brussels", Airports: []string{"BRU"}, Lat: 50.8503, Lon: 4.3517},
	{City: "Paris", Airports: []string{"CDG", "ORY"}, Lat: 48.8566, Lon: 2.3522},
	{City: "Zurich", Airports: []string{"ZRH"}, Lat: 47.3769, Lon: 8.5417},
	{City: "Copenhagen", Airports: []string{"CPH"}, Lat: 55.6761, Lon: 12.5683, HasFerry: true}, // DFDS
	{City: "Warsaw", Airports: []string{"WAW"}, Lat: 52.2297, Lon: 21.0122},
	{City: "Zagreb", Airports: []string{"ZAG"}, Lat: 45.8150, Lon: 15.9819},
	{City: "Helsinki", Airports: []string{"HEL"}, Lat: 60.1699, Lon: 24.9384, HasFerry: true},   // Tallink, Viking, Eckerö
	{City: "Tallinn", Airports: []string{"TLL"}, Lat: 59.4370, Lon: 24.7536, HasFerry: true},    // Tallink, Viking, Eckerö
	{City: "Stockholm", Airports: []string{"ARN"}, Lat: 59.3293, Lon: 18.0686, HasFerry: true},  // Tallink, Viking, Stena
	{City: "Gothenburg", Airports: []string{"GOT"}, Lat: 57.7089, Lon: 11.9746, HasFerry: true}, // Stena
	{City: "Riga", Airports: []string{"RIX"}, Lat: 56.9496, Lon: 24.1052, HasFerry: true},       // Tallink
	{City: "Milan", Airports: []string{"MXP", "LIN"}, Lat: 45.4642, Lon: 9.1900},
	{City: "Rome", Airports: []string{"FCO"}, Lat: 41.9028, Lon: 12.4964},
	{City: "Barcelona", Airports: []string{"BCN"}, Lat: 41.3874, Lon: 2.1686},
	{City: "Madrid", Airports: []string{"MAD"}, Lat: 40.4168, Lon: -3.7038},
	{City: "London", Airports: []string{"LHR", "LGW", "STN"}, Lat: 51.5074, Lon: -0.1278},
	{City: "Dubrovnik", Airports: []string{"DBV"}, Lat: 42.6507, Lon: 18.0944},
	{City: "Split", Airports: []string{"SPU"}, Lat: 43.5081, Lon: 16.4402},
	{City: "Athens", Airports: []string{"ATH"}, Lat: 37.9838, Lon: 23.7275},
	{City: "Lisbon", Airports: []string{"LIS"}, Lat: 38.7223, Lon: -9.1393},
}

// hubIndex maps lowercase city name to hub index for fast lookup.
var hubIndex map[string]int

func init() {
	hubIndex = make(map[string]int, len(hubs))
	for i, h := range hubs {
		hubIndex[strings.ToLower(h.City)] = i
	}
}

// LookupHub finds a hub by city name (case-insensitive).
func LookupHub(city string) (Hub, bool) {
	idx, ok := hubIndex[strings.ToLower(strings.TrimSpace(city))]
	if !ok {
		return Hub{}, false
	}
	return hubs[idx], true
}

// CityForAirport returns the hub city name for an IATA code, or the code itself.
func CityForAirport(iata string) string {
	upper := strings.ToUpper(strings.TrimSpace(iata))
	for _, h := range hubs {
		for _, a := range h.Airports {
			if a == upper {
				return h.City
			}
		}
	}
	return upper
}

// AirportForCity returns the primary IATA code for a city, or empty string.
func AirportForCity(city string) string {
	h, ok := LookupHub(city)
	if !ok {
		return ""
	}
	if len(h.Airports) > 0 {
		return h.Airports[0]
	}
	return ""
}

// haversineKm computes great-circle distance in km.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// CandidateHubs returns hubs that are geographically reasonable for a route,
// sorted by detour ratio (closest to the direct path first).
// A hub is included if dist(origin, hub) + dist(hub, dest) < factor * dist(origin, dest).
func CandidateHubs(origin, dest Hub, factor float64) []Hub {
	if factor <= 0 {
		factor = 2.0
	}
	direct := haversineKm(origin.Lat, origin.Lon, dest.Lat, dest.Lon)
	if direct < 1 {
		return nil
	}

	type hubDetour struct {
		hub    Hub
		detour float64
	}
	var candidates []hubDetour
	for _, h := range hubs {
		if strings.EqualFold(h.City, origin.City) || strings.EqualFold(h.City, dest.City) {
			continue
		}
		detour := haversineKm(origin.Lat, origin.Lon, h.Lat, h.Lon) +
			haversineKm(h.Lat, h.Lon, dest.Lat, dest.Lon)
		if detour < factor*direct {
			candidates = append(candidates, hubDetour{hub: h, detour: detour})
		}
	}

	// Sort by detour distance — closest to direct path first.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].detour < candidates[j].detour
	})

	result := make([]Hub, len(candidates))
	for i, c := range candidates {
		result[i] = c.hub
	}
	return result
}
