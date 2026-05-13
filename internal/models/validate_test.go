package models

import (
	"strings"
	"testing"
	"time"
)

// --- ValidateIATA ---

func TestValidateIATA_Valid(t *testing.T) {
	valid := []string{"HEL", "NRT", "LAX", "JFK", "CDG", "SIN"}
	for _, code := range valid {
		if err := ValidateIATA(code); err != nil {
			t.Errorf("ValidateIATA(%q) returned error: %v", code, err)
		}
	}
}

func TestValidateIATA_Lowercase(t *testing.T) {
	err := ValidateIATA("hel")
	if err == nil {
		t.Error("expected error for lowercase IATA code")
	}
}

func TestValidateIATA_MixedCase(t *testing.T) {
	err := ValidateIATA("Hel")
	if err == nil {
		t.Error("expected error for mixed case IATA code")
	}
}

func TestValidateIATA_TooShort(t *testing.T) {
	err := ValidateIATA("HE")
	if err == nil {
		t.Error("expected error for 2-letter code")
	}
}

func TestValidateIATA_TooLong(t *testing.T) {
	err := ValidateIATA("HELS")
	if err == nil {
		t.Error("expected error for 4-letter code")
	}
}

func TestValidateIATA_Numbers(t *testing.T) {
	err := ValidateIATA("H3L")
	if err == nil {
		t.Error("expected error for code containing numbers")
	}
}

func TestValidateIATA_AllNumbers(t *testing.T) {
	err := ValidateIATA("123")
	if err == nil {
		t.Error("expected error for all-numeric code")
	}
}

func TestValidateIATA_Empty(t *testing.T) {
	err := ValidateIATA("")
	if err == nil {
		t.Error("expected error for empty code")
	}
}

func TestValidateIATA_SpecialChars(t *testing.T) {
	tests := []string{"H-L", "H.L", "H L", "H@L", "H!L", "H#L"}
	for _, code := range tests {
		err := ValidateIATA(code)
		if err == nil {
			t.Errorf("expected error for code %q with special chars", code)
		}
	}
}

func TestValidateIATA_ErrorMessage(t *testing.T) {
	err := ValidateIATA("bad")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid IATA code") {
		t.Errorf("error message = %q, want to contain 'invalid IATA code'", err.Error())
	}
}

// --- ValidateDate ---

func TestValidateDate_ValidFuture(t *testing.T) {
	futureDate := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	if err := ValidateDate(futureDate); err != nil {
		t.Errorf("ValidateDate(%q) returned error: %v", futureDate, err)
	}
}

func TestValidateDate_Today(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	// Today should be valid (it is not before today).
	if err := ValidateDate(today); err != nil {
		t.Errorf("ValidateDate(%q) for today returned error: %v", today, err)
	}
}

func TestValidateDate_PastDate(t *testing.T) {
	// Use -2 days to avoid midnight boundary issues with UTC truncation
	past := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	err := ValidateDate(past)
	if err == nil {
		t.Errorf("expected error for past date %q", past)
	}
	if err != nil && !strings.Contains(err.Error(), "in the past") {
		t.Errorf("error message = %q, want to contain 'in the past'", err.Error())
	}
}

func TestValidateDate_InvalidFormat(t *testing.T) {
	tests := []string{
		"15-06-2026", // DD-MM-YYYY
		"06/15/2026", // MM/DD/YYYY
		"2026/06/15", // wrong separator
		"20260615",   // no separators
		"2026-6-15",  // single-digit month (still valid Go format but test)
		"not-a-date", // gibberish
	}
	for _, d := range tests {
		err := ValidateDate(d)
		if err == nil {
			t.Errorf("expected error for invalid date format %q", d)
		}
	}
}

func TestValidateDate_Empty(t *testing.T) {
	err := ValidateDate("")
	if err == nil {
		t.Error("expected error for empty date")
	}
}

func TestValidateDate_ErrorMessageFormat(t *testing.T) {
	err := ValidateDate("not-valid")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid date") {
		t.Errorf("error message = %q, want to contain 'invalid date'", err.Error())
	}
}

// --- ValidateDateRange ---

func TestValidateDateRange_Valid(t *testing.T) {
	err := ValidateDateRange("2026-06-15", "2026-06-22")
	if err != nil {
		t.Errorf("ValidateDateRange returned error: %v", err)
	}
}

func TestValidateDateRange_SameDay(t *testing.T) {
	err := ValidateDateRange("2026-06-15", "2026-06-15")
	if err != nil {
		t.Errorf("ValidateDateRange same day returned error: %v", err)
	}
}

func TestValidateDateRange_FromAfterTo(t *testing.T) {
	err := ValidateDateRange("2026-06-22", "2026-06-15")
	if err == nil {
		t.Error("expected error when from > to")
	}
	if !strings.Contains(err.Error(), "before start date") {
		t.Errorf("error message = %q, want to contain 'before start date'", err.Error())
	}
}

func TestValidateDateRange_InvalidFromDate(t *testing.T) {
	err := ValidateDateRange("bad-date", "2026-06-22")
	if err == nil {
		t.Error("expected error for invalid from date")
	}
	if !strings.Contains(err.Error(), "start date") {
		t.Errorf("error message = %q, want to contain 'start date'", err.Error())
	}
}

func TestValidateDateRange_InvalidToDate(t *testing.T) {
	err := ValidateDateRange("2026-06-15", "bad-date")
	if err == nil {
		t.Error("expected error for invalid to date")
	}
	if !strings.Contains(err.Error(), "end date") {
		t.Errorf("error message = %q, want to contain 'end date'", err.Error())
	}
}

func TestValidateDateRange_BothInvalid(t *testing.T) {
	err := ValidateDateRange("bad", "also-bad")
	if err == nil {
		t.Error("expected error for both invalid dates")
	}
	// Should fail on the first (from) date.
	if !strings.Contains(err.Error(), "start date") {
		t.Errorf("error message = %q, want to contain 'start date'", err.Error())
	}
}

func TestValidateDateRange_WideRange(t *testing.T) {
	// Year-spanning range should be fine.
	err := ValidateDateRange("2026-01-01", "2027-12-31")
	if err != nil {
		t.Errorf("ValidateDateRange year-spanning returned error: %v", err)
	}
}
