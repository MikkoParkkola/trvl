package models

import (
	"testing"
	"time"
)

// Booking searches cannot produce actionable inventory for a stay wholly in
// the past. ValidateDate already rejects this input; ValidateDateRange must
// preserve the same contract for commands that accept a check-in/check-out pair.
func TestValidateDateRangeRejectsPastStay(t *testing.T) {
	checkIn := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	checkOut := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	if err := ValidateDateRange(checkIn, checkOut); err == nil {
		t.Fatalf("ValidateDateRange(%q, %q) accepted a stay wholly in the past", checkIn, checkOut)
	}
}
