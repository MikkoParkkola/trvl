package mcp

import (
	"strings"
	"testing"
)

// TestPlanTripOutputSchema_DescribesWithheldTotals pins the MIK-8138 contract
// for structured clients: an incomplete summary is announced next to
// grand_total, so a withheld 0 is never read as a free trip.
func TestPlanTripOutputSchema_DescribesWithheldTotals(t *testing.T) {
	schema, ok := planTripOutputSchema().(map[string]interface{})
	if !ok {
		t.Fatal("plan_trip output schema is not an object")
	}
	summary := schema["properties"].(map[string]interface{})["summary"].(map[string]interface{})
	props := summary["properties"].(map[string]interface{})
	for _, field := range []string{"incomplete", "unconverted", "grand_total"} {
		if _, ok := props[field]; !ok {
			t.Errorf("summary schema lacks %q", field)
		}
	}
	desc, _ := props["incomplete"].(map[string]interface{})["description"].(string)
	if !strings.Contains(desc, "withheld") {
		t.Errorf("incomplete description = %q, want it to say the totals are withheld", desc)
	}
	if !strings.Contains(planTripTool().Description, "withholds grand_total") {
		t.Errorf("plan_trip description does not mention withheld totals: %q", planTripTool().Description)
	}
}
