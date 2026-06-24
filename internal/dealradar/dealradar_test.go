package dealradar

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/los"
)

func TestFromFlips_OnlySurfacesGenuineSavings(t *testing.T) {
	flips := []los.Flip{
		{ // genuine absolute saving
			Kind:              los.FlipExtendForSavings,
			BaselineNights:    5,
			BaselineTotal:     500,
			AlternativeNights: 7,
			AlternativeTotal:  420,
			Reason:            "stay 7 nights instead of 5 to save 80.00",
		},
		{ // better nightly rate but costs more overall — skipped
			Kind:              los.FlipExtendBetterRate,
			BaselineNights:    5,
			BaselineTotal:     500,
			AlternativeNights: 8,
			AlternativeTotal:  640,
			Reason:            "longer total but lower per-night rate",
		},
	}
	items := FromFlips("EUR", flips)
	if len(items) != 1 {
		t.Fatalf("expected 1 surfaced item, got %d", len(items))
	}
	if items[0].Savings != 80 {
		t.Errorf("savings = %.2f, want 80", items[0].Savings)
	}
	if items[0].Source != "los" {
		t.Errorf("source = %q, want los", items[0].Source)
	}
}

func TestFromFlips_DefaultsCurrency(t *testing.T) {
	items := FromFlips("", []los.Flip{{BaselineTotal: 100, AlternativeTotal: 90, BaselineNights: 3, AlternativeNights: 4}})
	if len(items) != 1 || items[0].Currency != "EUR" {
		t.Fatalf("expected EUR default currency, got %+v", items)
	}
}

func TestBuildDigest_DeterministicOrdering(t *testing.T) {
	a := []Item{
		{Source: "points", Title: "Award seat", Savings: 50, Currency: "EUR"},
		{Source: "los", Title: "Flip B", Savings: 30, Currency: "EUR"},
	}
	b := []Item{
		{Source: "los", Title: "Flip A", Savings: 80, Currency: "EUR"},
		{Source: "los", Title: "Flip C", Savings: 30, Currency: "EUR"},
	}
	// Build twice with inputs in different order; bodies must be identical.
	d1 := BuildDigest(a, b)
	d2 := BuildDigest(b, a)
	if d1.Render() != d2.Render() {
		t.Fatalf("render not deterministic across input order:\n--- d1 ---\n%s\n--- d2 ---\n%s", d1.Render(), d2.Render())
	}
	// Within los, highest savings first; ties broken by title.
	order := []string{}
	for _, it := range d1.Items {
		order = append(order, it.Source+"/"+it.Title)
	}
	want := []string{"los/Flip A", "los/Flip B", "los/Flip C", "points/Award seat"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

func TestRender_StableBodyNoTimestamps(t *testing.T) {
	d := BuildDigest([]Item{
		{Source: "los", Title: "Length-of-stay flip: 5→7 nights", Detail: "stay 7 nights instead of 5 to save 80.00", Savings: 80, Currency: "EUR"},
	})
	body := d.Render()
	want := "trvl deal-radar\n===============\n\n" +
		"1. [los] Length-of-stay flip: 5→7 nights\n" +
		"   save 80.00 EUR\n" +
		"   stay 7 nights instead of 5 to save 80.00\n\n" +
		"Total potential savings: 80.00 EUR across 1 deals.\n"
	if body != want {
		t.Fatalf("body mismatch:\n--- got ---\n%q\n--- want ---\n%q", body, want)
	}
}

func TestRender_EmptyDigest(t *testing.T) {
	d := BuildDigest()
	if !d.Empty() {
		t.Fatal("expected empty digest")
	}
	if !strings.Contains(d.Render(), "No rate-flips") {
		t.Errorf("empty body should explain no deals, got %q", d.Render())
	}
	if !strings.Contains(d.Subject(), "no flips") {
		t.Errorf("empty subject unexpected: %q", d.Subject())
	}
}

func TestSubject_NonEmpty(t *testing.T) {
	d := BuildDigest([]Item{{Source: "los", Title: "x", Savings: 80, Currency: "EUR"}})
	s := d.Subject()
	if !strings.Contains(s, "1 deals") || !strings.Contains(s, "80") {
		t.Errorf("subject = %q, want deal count and savings", s)
	}
}
