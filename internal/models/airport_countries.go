package models

// AirportCountry maps an IATA airport code to its ISO 3166-1 alpha-2 country
// code; "" when the code is not in the table.
func AirportCountry(code string) string {
	return airportCountries[code]
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
