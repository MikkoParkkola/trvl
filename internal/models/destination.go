package models

// DestinationInfo aggregates travel intelligence for a city or region.
type DestinationInfo struct {
	Location string       `json:"location"`
	Country  CountryInfo  `json:"country"`
	Weather  WeatherInfo  `json:"weather"`
	Holidays []Holiday    `json:"holidays,omitempty"`
	Safety   SafetyInfo   `json:"safety"`
	Currency CurrencyInfo `json:"currency"`
	Timezone string       `json:"timezone"`
}

// CountryInfo holds basic facts about a country.
type CountryInfo struct {
	Name       string   `json:"name"`
	Code       string   `json:"code"` // ISO 3166-1 alpha-2
	Capital    string   `json:"capital"`
	Languages  []string `json:"languages"`
	Currencies []string `json:"currencies"` // currency codes
	Region     string   `json:"region"`
}

// WeatherInfo holds current and forecast weather data.
type WeatherInfo struct {
	Current  WeatherDay   `json:"current,omitempty"`
	Forecast []WeatherDay `json:"forecast,omitempty"`
}

// WeatherDay represents weather for a single day.
type WeatherDay struct {
	Date          string  `json:"date"`
	TempHigh      float64 `json:"temp_high_c"`
	TempLow       float64 `json:"temp_low_c"`
	Precipitation float64 `json:"precipitation_mm"`
	Description   string  `json:"description"`
}

// Holiday represents a public or bank holiday.
type Holiday struct {
	Date string `json:"date"`
	Name string `json:"name"`
	Type string `json:"type"` // public, bank, etc.
}

// SafetyInfo holds travel advisory information.
type SafetyInfo struct {
	Level       float64 `json:"level"`    // 1-5 scale
	Advisory    string  `json:"advisory"` // e.g. "exercise normal caution"
	Source      string  `json:"source"`
	LastUpdated string  `json:"last_updated"`
}

// CurrencyInfo holds exchange rate data for the destination.
type CurrencyInfo struct {
	LocalCurrency string  `json:"local_currency"`
	ExchangeRate  float64 `json:"exchange_rate"` // vs EUR
	BaseCurrency  string  `json:"base_currency"` // EUR
}

// WikivoyageGuide holds a structured travel guide from Wikivoyage.
type WikivoyageGuide struct {
	Location string            `json:"location"`
	Summary  string            `json:"summary"`
	Sections map[string]string `json:"sections"` // "See" -> "...", "Eat" -> "..."
	URL      string            `json:"url"`
}

// NearbyPOI represents a point of interest near a location.
type NearbyPOI struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Distance int     `json:"distance_m"`
	Cuisine  string  `json:"cuisine,omitempty"`
	Hours    string  `json:"opening_hours,omitempty"`
	Phone    string  `json:"phone,omitempty"`
	Website  string  `json:"website,omitempty"`
}

// Event represents a local event during travel dates.
type Event struct {
	Name       string `json:"name"`
	Date       string `json:"date"`
	Time       string `json:"time,omitempty"`
	Venue      string `json:"venue"`
	Type       string `json:"type"`
	URL        string `json:"url,omitempty"`
	PriceRange string `json:"price_range,omitempty"`
}

// RatedPlace represents a venue with user ratings from Foursquare.
type RatedPlace struct {
	Name       string  `json:"name"`
	Rating     float64 `json:"rating"`
	Category   string  `json:"category"`
	Cuisine    string  `json:"cuisine,omitempty"`
	PriceLevel int     `json:"price_level"`
	Distance   int     `json:"distance_m"`
	Address    string  `json:"address"`
	Tip        string  `json:"tip,omitempty"`
}

// Attraction represents a tourist attraction from OpenTripMap.
type Attraction struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Description  string `json:"description,omitempty"`
	WikipediaURL string `json:"wikipedia_url,omitempty"`
	Distance     int    `json:"distance_m"`
}
