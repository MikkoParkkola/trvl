package destinations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// mockAPIs starts HTTP test servers that return canned responses for all
// external APIs used by the destinations package. It returns a cleanup function
// that resets the package-level URL constants.
func mockAPIs(t *testing.T) func() {
	t.Helper()

	// --- Nominatim mock ---
	nominatim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]nominatimResult{{
			Lat:         "35.6762",
			Lon:         "139.6503",
			DisplayName: "Tokyo, Japan",
			Address:     map[string]string{"country_code": "jp"},
		}})
	}))

	// --- Open-Meteo mock ---
	meteo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openMeteoResponse{Timezone: "Asia/Tokyo"}
		resp.Daily.Time = []string{"2026-06-15", "2026-06-16"}
		resp.Daily.TemperatureMax = []float64{28.5, 30.1}
		resp.Daily.TemperatureMin = []float64{20.1, 21.3}
		resp.Daily.PrecipitationSum = []float64{0.0, 5.2}
		resp.Daily.WeatherCode = []int{0, 61}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	// --- REST Countries mock ---
	countries := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]restCountryResponse{{
			Name: struct {
				Common string `json:"common"`
			}{Common: "Japan"},
			CCA2:      "JP",
			Capital:   []string{"Tokyo"},
			Languages: map[string]string{"jpn": "Japanese"},
			Currencies: map[string]struct {
				Name   string `json:"name"`
				Symbol string `json:"symbol"`
			}{"JPY": {Name: "Japanese yen", Symbol: "\u00a5"}},
			Region:    "Asia",
			Timezones: []string{"UTC+09:00"},
		}})
	}))

	// --- Nager.Date mock ---
	nager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]nagerHoliday{
			{Date: "2026-06-15", Name: "Test Holiday", Types: []string{"Public"}},
			{Date: "2026-06-20", Name: "Outside Range", Types: []string{"Public"}},
		})
	}))

	// --- travel-advisory.info mock ---
	advisory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"api_status": map[string]interface{}{
				"request": map[string]interface{}{"item": "JP"},
			},
			"data": map[string]interface{}{
				"JP": map[string]interface{}{
					"name":      "Japan",
					"continent": "AS",
					"advisory": map[string]interface{}{
						"score":   1.5,
						"message": "",
						"updated": "2026-03-01",
						"source":  "travel-advisory.info",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	// --- ExchangeRate mock ---
	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(exchangeRateResponse{
			Base: "EUR",
			Rates: map[string]float64{
				"JPY": 162.5,
				"USD": 1.08,
			},
		})
	}))

	// Override package-level constants by swapping the test server URLs
	// into unexported vars (we use a test-only setter pattern).
	origNominatim := setTestNominatimURL(nominatim.URL)
	origMeteo := setTestOpenMeteoURL(meteo.URL)
	origCountries := setTestRestCountriesURL(countries.URL + "/")
	origNager := setTestNagerDateURL(nager.URL)
	origAdvisory := setTestTravelAdvisoryURL(advisory.URL)
	origExchange := setTestExchangeRateURL(exchange.URL)

	// Clear all caches for test isolation.
	clearAllCaches()

	return func() {
		nominatim.Close()
		meteo.Close()
		countries.Close()
		nager.Close()
		advisory.Close()
		exchange.Close()

		setTestNominatimURL(origNominatim)
		setTestOpenMeteoURL(origMeteo)
		setTestRestCountriesURL(origCountries)
		setTestNagerDateURL(origNager)
		setTestTravelAdvisoryURL(origAdvisory)
		setTestExchangeRateURL(origExchange)
	}
}

func TestGetDestinationInfo(t *testing.T) {
	cleanup := mockAPIs(t)
	defer cleanup()

	ctx := context.Background()
	dates := models.DateRange{CheckIn: "2026-06-15", CheckOut: "2026-06-18"}

	info, err := GetDestinationInfo(ctx, "Tokyo", dates)
	if err != nil {
		t.Fatalf("GetDestinationInfo: %v", err)
	}

	// Verify location.
	if !strings.Contains(info.Location, "Tokyo") {
		t.Errorf("Location = %q, want to contain 'Tokyo'", info.Location)
	}

	// Verify country.
	if info.Country.Name != "Japan" {
		t.Errorf("Country.Name = %q, want 'Japan'", info.Country.Name)
	}
	if info.Country.Code != "JP" {
		t.Errorf("Country.Code = %q, want 'JP'", info.Country.Code)
	}

	// Verify weather.
	if len(info.Weather.Forecast) != 2 {
		t.Errorf("Weather.Forecast count = %d, want 2", len(info.Weather.Forecast))
	}
	if info.Weather.Current.TempHigh != 28.5 {
		t.Errorf("Weather.Current.TempHigh = %.1f, want 28.5", info.Weather.Current.TempHigh)
	}

	// Verify holidays (only one within the date range).
	if len(info.Holidays) != 1 {
		t.Errorf("Holidays count = %d, want 1", len(info.Holidays))
	} else if info.Holidays[0].Name != "Test Holiday" {
		t.Errorf("Holidays[0].Name = %q, want 'Test Holiday'", info.Holidays[0].Name)
	}

	// Verify safety.
	if info.Safety.Level != 1.5 {
		t.Errorf("Safety.Level = %.1f, want 1.5", info.Safety.Level)
	}
	if info.Safety.Advisory != "Exercise normal caution" {
		t.Errorf("Safety.Advisory = %q, want 'Exercise normal caution'", info.Safety.Advisory)
	}

	// Verify currency.
	if info.Currency.LocalCurrency != "JPY" {
		t.Errorf("Currency.LocalCurrency = %q, want 'JPY'", info.Currency.LocalCurrency)
	}
	if info.Currency.ExchangeRate < 100 || info.Currency.ExchangeRate > 250 {
		t.Errorf("Currency.ExchangeRate = %.1f, expected between 100-250 JPY/EUR", info.Currency.ExchangeRate)
	}

	// Verify timezone.
	if info.Timezone != "Asia/Tokyo" {
		t.Errorf("Timezone = %q, want 'Asia/Tokyo'", info.Timezone)
	}
}

func TestGetDestinationInfo_GracefulDegradation(t *testing.T) {
	// Set up mocks but make the weather API return 500.
	nominatim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]nominatimResult{{
			Lat:         "48.8566",
			Lon:         "2.3522",
			DisplayName: "Paris, France",
			Address:     map[string]string{"country_code": "fr"},
		}})
	}))
	defer nominatim.Close()

	meteo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer meteo.Close()

	countries := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]restCountryResponse{{
			Name: struct {
				Common string `json:"common"`
			}{Common: "France"},
			CCA2:      "FR",
			Capital:   []string{"Paris"},
			Languages: map[string]string{"fra": "French"},
			Currencies: map[string]struct {
				Name   string `json:"name"`
				Symbol string `json:"symbol"`
			}{"EUR": {Name: "Euro", Symbol: "\u20ac"}},
			Region: "Europe",
		}})
	}))
	defer countries.Close()

	nager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]nagerHoliday{})
	}))
	defer nager.Close()

	advisory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"FR": map[string]interface{}{
					"advisory": map[string]interface{}{
						"score":   2.0,
						"updated": "2026-01-01",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer advisory.Close()

	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(exchangeRateResponse{
			Base:  "EUR",
			Rates: map[string]float64{"EUR": 1.0},
		})
	}))
	defer exchange.Close()

	origNominatim := setTestNominatimURL(nominatim.URL)
	origMeteo := setTestOpenMeteoURL(meteo.URL)
	origCountries := setTestRestCountriesURL(countries.URL + "/")
	origNager := setTestNagerDateURL(nager.URL)
	origAdvisory := setTestTravelAdvisoryURL(advisory.URL)
	origExchange := setTestExchangeRateURL(exchange.URL)
	clearAllCaches()
	defer func() {
		setTestNominatimURL(origNominatim)
		setTestOpenMeteoURL(origMeteo)
		setTestRestCountriesURL(origCountries)
		setTestNagerDateURL(origNager)
		setTestTravelAdvisoryURL(origAdvisory)
		setTestExchangeRateURL(origExchange)
	}()

	ctx := context.Background()
	dates := models.DateRange{CheckIn: "2026-07-01", CheckOut: "2026-07-05"}

	info, err := GetDestinationInfo(ctx, "Paris", dates)
	if err != nil {
		t.Fatalf("GetDestinationInfo should not fail when only weather fails: %v", err)
	}

	// Weather should be empty (API returned 500).
	if len(info.Weather.Forecast) != 0 {
		t.Errorf("Weather.Forecast should be empty when API fails, got %d items", len(info.Weather.Forecast))
	}

	// Country should still work.
	if info.Country.Name != "France" {
		t.Errorf("Country.Name = %q, want 'France'", info.Country.Name)
	}

	// Safety should still work.
	if info.Safety.Level != 2.0 {
		t.Errorf("Safety.Level = %.1f, want 2.0", info.Safety.Level)
	}
}

func TestWeatherCodeDescription(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "Clear sky"},
		{1, "Mainly clear"},
		{2, "Partly cloudy"},
		{3, "Overcast"},
		{45, "Fog"},
		{61, "Rain"},
		{71, "Snow"},
		{95, "Thunderstorm"},
		{999, "Unknown"},
	}
	for _, tt := range tests {
		got := weatherCodeDescription(tt.code)
		if got != tt.want {
			t.Errorf("weatherCodeDescription(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestAdvisoryText(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{1.0, "Exercise normal caution"},
		{2.5, "Exercise normal caution"},
		{3.0, "Exercise increased caution"},
		{3.5, "Exercise increased caution"},
		{4.0, "Reconsider travel"},
		{4.5, "Do not travel"},
	}
	for _, tt := range tests {
		got := advisoryText(tt.score)
		if got != tt.want {
			t.Errorf("advisoryText(%.1f) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestFilterHolidays(t *testing.T) {
	holidays := []models.Holiday{
		{Date: "2026-06-10", Name: "Before"},
		{Date: "2026-06-15", Name: "Start"},
		{Date: "2026-06-17", Name: "Middle"},
		{Date: "2026-06-18", Name: "End"},
		{Date: "2026-06-25", Name: "After"},
	}

	filtered := filterHolidays(holidays, "2026-06-15", "2026-06-18")
	if len(filtered) != 3 {
		t.Errorf("filterHolidays: got %d, want 3", len(filtered))
	}

	// No filter.
	all := filterHolidays(holidays, "", "")
	if len(all) != 5 {
		t.Errorf("filterHolidays with no dates: got %d, want 5", len(all))
	}
}

func TestCaching(t *testing.T) {
	cleanup := mockAPIs(t)
	defer cleanup()

	ctx := context.Background()

	// First call populates cache.
	_, err := FetchSafety(ctx, "JP")
	if err != nil {
		t.Fatalf("first FetchSafety: %v", err)
	}

	// Second call should use cache (server could be down and it would still work).
	info, err := FetchSafety(ctx, "JP")
	if err != nil {
		t.Fatalf("second FetchSafety: %v", err)
	}
	if info.Level != 1.5 {
		t.Errorf("cached Safety.Level = %.1f, want 1.5", info.Level)
	}
}
