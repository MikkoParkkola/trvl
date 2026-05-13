package trip

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/visa"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

// ViabilityInput configures a trip viability assessment.
type ViabilityInput struct {
	Origin      string // IATA code (e.g. "HEL")
	Destination string // IATA code or city name (e.g. "BCN")
	DepartDate  string // YYYY-MM-DD
	ReturnDate  string // YYYY-MM-DD
	Guests      int
	Passport    string // ISO country code for visa check (e.g. "FI")
	Currency    string
}

// ViabilityCheck is one dimension of the viability assessment.
type ViabilityCheck struct {
	Dimension string  `json:"dimension"` // "flights", "hotels", "visa", "weather", "total_cost"
	Status    string  `json:"status"`    // "ok", "warning", "blocker"
	Summary   string  `json:"summary"`
	Cost      float64 `json:"cost,omitempty"`
	Currency  string  `json:"currency,omitempty"`
}

// ViabilityResult is the response for a trip viability assessment.
type ViabilityResult struct {
	Success   bool             `json:"success"`
	Verdict   string           `json:"verdict"` // "GO", "WAIT", "NO_GO"
	Reason    string           `json:"reason"`  // one-line explanation
	Checks    []ViabilityCheck `json:"checks"`
	TotalCost float64          `json:"total_cost,omitempty"`
	Currency  string           `json:"currency,omitempty"`
	Nights    int              `json:"nights,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// AssessTrip evaluates whether a trip is viable by checking flights, hotels,
// visa, and weather in parallel. Returns a GO/WAIT/NO-GO verdict.
func AssessTrip(ctx context.Context, input ViabilityInput) (*ViabilityResult, error) {
	if input.Origin == "" || input.Destination == "" {
		return nil, fmt.Errorf("origin and destination are required")
	}
	if input.DepartDate == "" || input.ReturnDate == "" {
		return nil, fmt.Errorf("depart_date and return_date are required")
	}
	if input.Guests <= 0 {
		input.Guests = 1
	}

	var (
		costResult    *TripCostResult
		costErr       error
		visaResult    visa.Result
		weatherResult *weather.WeatherResult
		weatherErr    error
		wg            sync.WaitGroup
	)

	// 1. Trip cost (flights + hotel -- already parallelized internally).
	wg.Add(1)
	go func() {
		defer wg.Done()
		costResult, costErr = CalculateTripCost(ctx, TripCostInput{
			Origin:      input.Origin,
			Destination: input.Destination,
			DepartDate:  input.DepartDate,
			ReturnDate:  input.ReturnDate,
			Guests:      input.Guests,
			Currency:    input.Currency,
		})
	}()

	// 2. Visa check (static dataset, no API call).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if input.Passport != "" {
			destCountry := resolveDestinationCountry(input.Destination)
			if destCountry != "" {
				visaResult = visa.Lookup(input.Passport, destCountry)
			}
		}
	}()

	// 3. Weather forecast (Open-Meteo API).
	wg.Add(1)
	go func() {
		defer wg.Done()
		destCity := models.ResolveLocationName(input.Destination)
		weatherResult, weatherErr = weather.GetForecast(ctx, destCity, input.DepartDate, input.ReturnDate)
	}()

	wg.Wait()

	checks, hasBlocker, hasWarning := buildViabilityChecks(costResult, costErr, visaResult, input.Passport, weatherResult, weatherErr)

	verdict, reason := determineVerdict(checks, hasBlocker, hasWarning)

	totalCost := 0.0
	currency := ""
	nights := 0
	if costResult != nil {
		totalCost = costResult.Total
		currency = costResult.Currency
		nights = costResult.Nights
	}

	return &ViabilityResult{
		Success:   true,
		Verdict:   verdict,
		Reason:    reason,
		Checks:    checks,
		TotalCost: totalCost,
		Currency:  currency,
		Nights:    nights,
	}, nil
}

// buildViabilityChecks assembles the check results from the parallel lookups.
func buildViabilityChecks(
	costResult *TripCostResult, costErr error,
	visaResult visa.Result, passport string,
	weatherResult *weather.WeatherResult, weatherErr error,
) ([]ViabilityCheck, bool, bool) {
	var checks []ViabilityCheck
	hasBlocker := false
	hasWarning := false

	// Flight + hotel checks.
	if costErr != nil || costResult == nil || !costResult.Success {
		errMsg := "could not find flights"
		if costErr != nil {
			errMsg = costErr.Error()
		} else if costResult != nil && costResult.Error != "" {
			errMsg = costResult.Error
		}
		checks = append(checks, ViabilityCheck{
			Dimension: "flights",
			Status:    "warning",
			Summary:   errMsg,
		})
		hasWarning = true
	} else {
		flightCheck := ViabilityCheck{
			Dimension: "flights",
			Status:    "ok",
			Currency:  costResult.Flights.Currency,
		}
		outbound := costResult.Flights.Outbound
		ret := costResult.Flights.Return
		if outbound > 0 && ret > 0 {
			flightCheck.Cost = outbound + ret
			flightCheck.Summary = fmt.Sprintf("%.0f %s per person (%.0f out + %.0f return)",
				outbound+ret, costResult.Flights.Currency, outbound, ret)
		} else if outbound > 0 {
			flightCheck.Cost = outbound
			flightCheck.Summary = fmt.Sprintf("%.0f %s outbound only", outbound, costResult.Flights.Currency)
			flightCheck.Status = "warning"
			hasWarning = true
		} else if ret > 0 {
			flightCheck.Cost = ret
			flightCheck.Summary = fmt.Sprintf("%.0f %s return only", ret, costResult.Flights.Currency)
			flightCheck.Status = "warning"
			hasWarning = true
		} else {
			flightCheck.Summary = "no flight prices found"
			flightCheck.Status = "warning"
			hasWarning = true
		}
		checks = append(checks, flightCheck)

		// Hotel check.
		hotelCheck := ViabilityCheck{
			Dimension: "hotels",
			Currency:  costResult.Hotels.Currency,
		}
		if costResult.Hotels.PerNight > 0 {
			hotelCheck.Status = "ok"
			hotelCheck.Cost = costResult.Hotels.PerNight
			hotelCheck.Summary = fmt.Sprintf("%.0f %s/night (%s) — %.0f %s total",
				costResult.Hotels.PerNight, costResult.Hotels.Currency,
				costResult.Hotels.Name, costResult.Hotels.Total, costResult.Hotels.Currency)
		} else {
			hotelCheck.Status = "warning"
			hotelCheck.Summary = "no hotel prices found"
			hasWarning = true
		}
		checks = append(checks, hotelCheck)
	}

	// Visa check.
	if passport != "" {
		visaCheck := buildVisaCheck(visaResult)
		switch visaCheck.Status {
		case "blocker":
			hasBlocker = true
		case "warning":
			hasWarning = true
		}
		checks = append(checks, visaCheck)
	}

	// Weather check.
	if weatherErr == nil && weatherResult != nil && weatherResult.Success && len(weatherResult.Forecasts) > 0 {
		wc := buildWeatherCheck(weatherResult.Forecasts)
		if wc.Status == "warning" {
			hasWarning = true
		}
		checks = append(checks, wc)
	}

	// Total cost check.
	if costResult != nil && costResult.Total > 0 {
		checks = append(checks, ViabilityCheck{
			Dimension: "total_cost",
			Status:    "ok",
			Summary:   fmt.Sprintf("%.0f %s total (%.0f per person, %.0f per day)", costResult.Total, costResult.Currency, costResult.PerPerson, costResult.PerDay),
			Cost:      costResult.Total,
			Currency:  costResult.Currency,
		})
	}

	return checks, hasBlocker, hasWarning
}

// buildVisaCheck creates a ViabilityCheck from a visa lookup result.
func buildVisaCheck(visaResult visa.Result) ViabilityCheck {
	check := ViabilityCheck{Dimension: "visa"}
	if !visaResult.Success {
		check.Status = "warning"
		check.Summary = "could not determine visa requirements"
		return check
	}

	req := visaResult.Requirement
	switch req.Status {
	case "visa-free", "freedom-of-movement":
		check.Status = "ok"
		check.Summary = fmt.Sprintf("%s (max %s)", req.Status, req.MaxStay)
	case "visa-on-arrival", "e-visa":
		check.Status = "warning"
		check.Summary = fmt.Sprintf("%s — apply before travel", req.Status)
		if req.Notes != "" {
			check.Summary += ". " + req.Notes
		}
	case "visa-required":
		check.Status = "blocker"
		check.Summary = "visa required — check processing times before booking"
		if req.Notes != "" {
			check.Summary += ". " + req.Notes
		}
	default:
		check.Status = "warning"
		check.Summary = req.Status
	}
	return check
}

// buildWeatherCheck creates a ViabilityCheck from weather forecast data.
func buildWeatherCheck(forecasts []weather.Forecast) ViabilityCheck {
	check := ViabilityCheck{Dimension: "weather"}

	totalMax := 0.0
	totalMin := 0.0
	rainDays := 0
	for _, f := range forecasts {
		totalMax += f.TempMax
		totalMin += f.TempMin
		if f.Precipitation > 5 {
			rainDays++
		}
	}
	n := float64(len(forecasts))
	avgMax := totalMax / n
	avgMin := totalMin / n

	check.Summary = fmt.Sprintf("%.0f\u2013%.0f\u00b0C", avgMin, avgMax)
	if rainDays > 0 {
		check.Summary += fmt.Sprintf(", %d rainy day(s) of %d", rainDays, len(forecasts))
	}

	if rainDays > len(forecasts)/2 {
		check.Status = "warning"
		check.Summary += " — mostly rain expected"
	} else {
		check.Status = "ok"
	}
	return check
}

// determineVerdict computes the overall GO/WAIT/NO_GO verdict from checks.
func determineVerdict(checks []ViabilityCheck, hasBlocker, hasWarning bool) (string, string) {
	if hasBlocker {
		for _, c := range checks {
			if c.Status == "blocker" {
				return "NO_GO", c.Dimension + ": " + c.Summary
			}
		}
		return "NO_GO", "blocker detected"
	}
	if hasWarning {
		var warnings []string
		for _, c := range checks {
			if c.Status == "warning" {
				warnings = append(warnings, c.Dimension)
			}
		}
		return "WAIT", "Issues with: " + strings.Join(warnings, ", ")
	}
	return "GO", "All checks passed"
}

// resolveDestinationCountry maps an IATA airport code to its ISO 3166-1
// alpha-2 country code. Falls back to empty string for unknown codes.
func resolveDestinationCountry(dest string) string {
	dest = strings.ToUpper(strings.TrimSpace(dest))

	// If it's already a 2-letter country code, return it directly.
	if len(dest) == 2 {
		return dest
	}

	if cc, ok := airportCountries[dest]; ok {
		return cc
	}
	return ""
}

// airportCountries maps IATA airport codes to ISO 3166-1 alpha-2 country codes.
// Covers the same airports as models.AirportNames.
var airportCountries = map[string]string{
	// Europe
	"HEL": "FI", "LHR": "GB", "LGW": "GB", "STN": "GB", "LTN": "GB",
	"SEN": "GB", "BRS": "GB", "CDG": "FR", "ORY": "FR", "AMS": "NL",
	"FRA": "DE", "MUC": "DE", "BER": "DE", "DUS": "DE", "HAM": "DE",
	"MAD": "ES", "BCN": "ES", "AGP": "ES", "PMI": "ES", "ALC": "ES",
	"IBZ": "ES", "VLC": "ES", "SVQ": "ES", "FCO": "IT", "MXP": "IT",
	"NAP": "IT", "VCE": "IT", "LIN": "IT", "BGY": "IT", "BLQ": "IT",
	"OLB": "IT", "CTA": "IT", "PSA": "IT", "ZRH": "CH", "GVA": "CH",
	"VIE": "AT", "BRU": "BE", "CPH": "DK", "OSL": "NO", "ARN": "SE",
	"GOT": "SE", "DUB": "IE", "LIS": "PT", "OPO": "PT", "ATH": "GR",
	"GDN": "PL", "WRO": "PL", "KTW": "PL", "FAO": "PT", "SKG": "GR",
	"WAW": "PL", "KRK": "PL", "PRG": "CZ", "BUD": "HU", "OTP": "RO",
	"SOF": "BG", "IST": "TR", "SAW": "TR", "AYT": "TR", "ZAG": "HR",
	"BEG": "RS", "TLL": "EE", "RIX": "LV", "VNO": "LT", "KEF": "IS",
	"EDI": "GB", "MAN": "GB", "BHX": "GB", "NCE": "FR", "LYS": "FR",
	"TLS": "FR", "MRS": "FR", "DBV": "HR", "SPU": "HR", "TIV": "ME",
	"CFU": "GR", "HER": "GR", "RHO": "GR", "JTR": "GR", "TFS": "ES",
	"LPA": "ES", "ACE": "ES", "FUE": "ES",
	// North America
	"JFK": "US", "EWR": "US", "LGA": "US", "LAX": "US", "SFO": "US",
	"ORD": "US", "MDW": "US", "ATL": "US", "DFW": "US", "DEN": "US",
	"SEA": "US", "MIA": "US", "FLL": "US", "MCO": "US", "TPA": "US",
	"BOS": "US", "IAD": "US", "DCA": "US", "PHL": "US", "MSP": "US",
	"DTW": "US", "CLT": "US", "PHX": "US", "SAN": "US", "IAH": "US",
	"AUS": "US", "SLC": "US", "PDX": "US", "BNA": "US", "RDU": "US",
	"HNL": "US", "OGG": "US", "YYZ": "CA", "YVR": "CA", "YUL": "CA",
	"YYC": "CA", "YOW": "CA", "MEX": "MX", "CUN": "MX", "SJD": "MX",
	"GDL": "MX",
	// Asia
	"NRT": "JP", "HND": "JP", "KIX": "JP", "ICN": "KR", "PEK": "CN",
	"PKX": "CN", "PVG": "CN", "HKG": "HK", "TPE": "TW", "SIN": "SG",
	"BKK": "TH", "DMK": "TH", "KUL": "MY", "CGK": "ID", "MNL": "PH",
	"SGN": "VN", "HAN": "VN", "DEL": "IN", "BOM": "IN", "BLR": "IN",
	"MAA": "IN", "CCU": "IN", "CMB": "LK", "KTM": "NP", "DPS": "ID",
	"REP": "KH", "RGN": "MM", "PNH": "KH",
	// Middle East
	"DXB": "AE", "AUH": "AE", "DOH": "QA", "RUH": "SA", "JED": "SA",
	"TLV": "IL", "AMM": "JO", "BAH": "BH", "MCT": "OM", "KWI": "KW",
	// Africa
	"JNB": "ZA", "CPT": "ZA", "NBO": "KE", "CAI": "EG", "CMN": "MA",
	"RAK": "MA", "ADD": "ET", "LOS": "NG", "ACC": "GH", "DSS": "SN",
	"TUN": "TN",
	// Oceania
	"SYD": "AU", "MEL": "AU", "BNE": "AU", "PER": "AU", "AKL": "NZ",
	"CHC": "NZ", "WLG": "NZ", "NAN": "FJ", "PPT": "PF",
	// South America
	"GRU": "BR", "GIG": "BR", "EZE": "AR", "SCL": "CL", "BOG": "CO",
	"LIM": "PE", "UIO": "EC", "CCS": "VE", "MVD": "UY", "PTY": "PA",
	"SJO": "CR", "HAV": "CU", "SDQ": "DO", "MBJ": "JM",
}
