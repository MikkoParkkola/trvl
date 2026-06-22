package providers

import (
	"strconv"
	"strings"
)

// normalizePrice converts price from fromCurrency to toCurrency using live
// ECB rates (via Frankfurter API, refreshed daily). Returns price unchanged
// when currencies match, either is empty, or no rate is available.
func normalizePrice(price float64, fromCurrency, toCurrency string) float64 {
	if fromCurrency == toCurrency || fromCurrency == "" || toCurrency == "" {
		return price
	}
	if r := defaultFXCache.getRate(fromCurrency, toCurrency); r > 0 {
		return price * r
	}
	return price
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err == nil {
			return f
		}
		// Try the first numeric token before falling back to full strip.
		// This handles composite strings like "4.84 (25)" where
		// stripNonNumeric would concatenate all digits into "4.8425".
		if first := firstNumericToken(n); first != "" {
			if f, err = strconv.ParseFloat(first, 64); err == nil {
				return f
			}
		}
		// Strip currency symbols and whitespace (e.g. "€ 61" -> "61").
		cleaned := stripNonNumeric(n)
		if cleaned != "" {
			f, _ = strconv.ParseFloat(cleaned, 64)
			return f
		}
		return 0
	default:
		return 0
	}
}

// stripNonNumeric removes everything except digits, '.', and '-' from s.
// Used to extract a numeric value from currency-formatted strings like "€ 61".
func stripNonNumeric(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// firstNumericToken extracts the first contiguous number (integer or decimal)
// from a string that may contain mixed text. It handles composite formats
// like "4.84 (25)" (returns "4.84") and "€ 204" (returns "204") by
// scanning for the first run of digits/dots/minus.
func firstNumericToken(s string) string {
	var b strings.Builder
	started := false
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' {
			b.WriteRune(r)
			started = true
		} else if r == ',' && started {
			// Thousands separator (e.g. "1,204") — skip the comma but
			// continue collecting digits so "€1,204" → "1204".
			continue
		} else if started {
			break // end of the first numeric run
		}
	}
	return b.String()
}

// lastIntToken extracts the last integer found in a string. Useful for
// parsing review counts from composite strings like "4.84 (25)" where the
// count appears at the end.
func lastIntToken(s string) string {
	var last string
	var current strings.Builder
	inNumber := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			current.WriteRune(r)
			inNumber = true
		} else {
			if inNumber {
				last = current.String()
				current.Reset()
				inNumber = false
			}
		}
	}
	if inNumber {
		last = current.String()
	}
	return last
}

// currencySymbols maps common single-character currency symbols to their
// ISO 4217 codes. Multi-character symbols (kr, zł, лв) are not included;
// those currencies use the 3-letter code path instead.
var currencySymbols = map[rune]string{
	'€': "EUR",
	'$': "USD",
	'£': "GBP",
	'¥': "JPY",
	'₩': "KRW",
	'₹': "INR",
	'₽': "RUB",
	'₺': "TRY",
	'₴': "UAH",
	'₿': "BTC",
	'฿': "THB",
	'₫': "VND",
	'₱': "PHP",
	'₡': "CRC",
	'₦': "NGN",
	'₪': "ILS",
	'₸': "KZT",
	'₾': "GEL",
	'₼': "AZN",
	'₵': "GHS",
	'₲': "PYG",
	'₮': "MNT",
	'₭': "LAK",
	'৳': "BDT",
}

// extractCurrencyCode extracts an ISO 4217 currency code from a price string.
// It handles two formats:
//   - 3-letter code prefix/suffix: "EUR 204", "204 USD"
//   - Currency symbol prefix: "€175", "£99", "$120"
//
// Returns the empty string when no currency can be determined.
func extractCurrencyCode(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Check for a 3-letter uppercase ISO code at the start or end.
	// Leading: "EUR 204", "USD204"
	if len(s) >= 3 {
		prefix := s[:3]
		if isUpperAlpha(prefix) {
			return prefix
		}
	}
	// Trailing: "204 EUR", "204EUR"
	if len(s) >= 3 {
		suffix := s[len(s)-3:]
		if isUpperAlpha(suffix) {
			return suffix
		}
	}

	// Check for currency symbol at start of string.
	for _, r := range s {
		if code, ok := currencySymbols[r]; ok {
			return code
		}
		// Only check the first non-space rune.
		if r != ' ' {
			break
		}
	}

	return ""
}

// isUpperAlpha reports whether s consists entirely of uppercase ASCII letters.
func isUpperAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return len(s) > 0
}
