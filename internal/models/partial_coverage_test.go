package models

import (
	"strings"
	"testing"
)

func TestPartialCoverageNote(t *testing.T) {
	ok := []ProviderStatus{{ID: "a", Name: "A", Status: StatusOK}}
	if got := PartialCoverageNote(ok); got != "" {
		t.Errorf("complete search: got %q, want no note", got)
	}
	if got := PartialCoverageNote(nil); got != "" {
		t.Errorf("unassessed search: got %q, want no note", got)
	}
	partial := append(ok, ProviderStatus{ID: "b", Name: "B", Status: StatusTimeout, Error: "deadline exceeded"})
	got := PartialCoverageNote(partial)
	if !strings.Contains(got, "Checked 1 of 2 providers") || !strings.Contains(got, "- B: ") || !strings.Contains(got, "deadline exceeded") {
		t.Errorf("partial search note = %q, want the count and the failed provider", got)
	}
}
