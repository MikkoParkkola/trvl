package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Regression coverage for the v1.21.0 rooms report: an OTA fallback candidate
// must not be accepted merely because both names contain "Hotel" or "Mare".
func TestFindBestNameMatchRejectsDifferentPropertiesSharingGenericWords(t *testing.T) {
	const query = "Hotel Continental Mare, Ischia Italy"

	tests := []struct {
		name      string
		candidate string
	}{
		{name: "shared mare token", candidate: "Hotel Mare Blu Terme"},
		{name: "generic hotel token only", candidate: "Hotel Residence San Angelo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findBestNameMatch([]models.HotelResult{{
				Name:    tt.candidate,
				HotelID: "agoda:unrelated-property",
			}}, query)
			if got != nil {
				t.Fatalf("matched unrelated property %q for query %q", got.Name, query)
			}
		})
	}
}
