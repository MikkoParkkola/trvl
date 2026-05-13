package expenses

// MIK-3088 (partial): tests for the per-traveller expense reconciliation.

import (
	"math"
	"strings"
	"testing"
)

func TestReconcile_EqualSplitTwoTravellers(t *testing.T) {
	got := Reconcile([]Booking{
		{ID: "b1", Category: "hotel", Currency: "EUR", Amount: 200, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}, {Traveller: "bob"}}},
	})
	if got.Total != 200 {
		t.Errorf("Total=%.2f, want 200", got.Total)
	}
	if len(got.PerTraveller) != 2 {
		t.Fatalf("got %d travellers, want 2", len(got.PerTraveller))
	}
	for _, b := range got.PerTraveller {
		want := 100.0
		if math.Abs(b.Owed-want) > 0.01 {
			t.Errorf("%s owed=%.2f, want 100", b.Traveller, b.Owed)
		}
	}
	if len(got.Transfers) != 1 {
		t.Fatalf("got %d transfers, want 1", len(got.Transfers))
	}
	tr := got.Transfers[0]
	if tr.From != "bob" || tr.To != "alice" || math.Abs(tr.Amount-100) > 0.01 {
		t.Errorf("transfer=%+v, want bob->alice 100", tr)
	}
}

func TestReconcile_WeightedSplit(t *testing.T) {
	// 300 EUR; alice claims 2 weights, bob claims 1.
	got := Reconcile([]Booking{
		{Currency: "EUR", Amount: 300, Payer: "alice", Split: []ShareEntry{
			{Traveller: "alice", Weight: 2},
			{Traveller: "bob", Weight: 1},
		}},
	})
	wantOwed := map[string]float64{"alice": 200, "bob": 100}
	for _, b := range got.PerTraveller {
		if math.Abs(b.Owed-wantOwed[b.Traveller]) > 0.01 {
			t.Errorf("%s owed=%.2f, want %.2f", b.Traveller, b.Owed, wantOwed[b.Traveller])
		}
	}
	if len(got.Transfers) != 1 {
		t.Fatalf("got %d transfers, want 1", len(got.Transfers))
	}
	if got.Transfers[0].Amount != 100 {
		t.Errorf("transfer amount=%.2f, want 100", got.Transfers[0].Amount)
	}
}

func TestReconcile_AlreadyBalancedNoTransfers(t *testing.T) {
	got := Reconcile([]Booking{
		{Currency: "EUR", Amount: 100, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}, {Traveller: "bob"}}},
		{Currency: "EUR", Amount: 100, Payer: "bob", Split: []ShareEntry{{Traveller: "alice"}, {Traveller: "bob"}}},
	})
	if len(got.Transfers) != 0 {
		t.Errorf("balanced trip should yield no transfers, got %v", got.Transfers)
	}
}

func TestReconcile_MinimumFlowThreeTravellers(t *testing.T) {
	// alice pays 300 hotel split 3 ways; bob pays 60 dinner split 3 ways.
	// Each traveller owes 100 + 20 = 120.
	//   alice: paid 300, owes 120, net +180 (creditor)
	//   bob:   paid 60,  owes 120, net  -60 (debtor)
	//   charlie: paid 0, owes 120, net -120 (debtor)
	// Two debtors → expect two transfers, both flowing to alice.
	got := Reconcile([]Booking{
		{Category: "hotel", Currency: "EUR", Amount: 300, Payer: "alice", Split: []ShareEntry{
			{Traveller: "alice"}, {Traveller: "bob"}, {Traveller: "charlie"},
		}},
		{Category: "dining", Currency: "EUR", Amount: 60, Payer: "bob", Split: []ShareEntry{
			{Traveller: "alice"}, {Traveller: "bob"}, {Traveller: "charlie"},
		}},
	})
	if len(got.Transfers) != 2 {
		t.Errorf("got %d transfers, want 2 (bob and charlie owe alice)", len(got.Transfers))
	}
	// Every transfer must flow to alice.
	for _, tr := range got.Transfers {
		if tr.To != "alice" {
			t.Errorf("transfer %+v: To=%q, want alice", tr, tr.To)
		}
	}
	// Sum of incoming-to-alice must equal alice's +180 net.
	var aliceIn float64
	for _, tr := range got.Transfers {
		if tr.To == "alice" {
			aliceIn += tr.Amount
		}
	}
	if math.Abs(aliceIn-180) > 0.01 {
		t.Errorf("transfers to alice sum to %.2f, want 180", aliceIn)
	}
}

func TestReconcile_CategoryRollup(t *testing.T) {
	got := Reconcile([]Booking{
		{Category: "hotel", Currency: "EUR", Amount: 200, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}},
		{Category: "flight", Currency: "EUR", Amount: 150, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}},
		{Category: "hotel", Currency: "EUR", Amount: 100, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}},
	})
	want := map[string]float64{"hotel": 300, "flight": 150}
	for _, c := range got.ByCategory {
		if math.Abs(c.Total-want[c.Category]) > 0.01 {
			t.Errorf("category %s = %.2f, want %.2f", c.Category, c.Total, want[c.Category])
		}
	}
	// Sorted by Total descending → hotel first.
	if got.ByCategory[0].Category != "hotel" {
		t.Errorf("first category=%s, want hotel", got.ByCategory[0].Category)
	}
}

func TestReconcile_NonPositiveAmountSkipped(t *testing.T) {
	got := Reconcile([]Booking{
		{Currency: "EUR", Amount: 0, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}},
		{Currency: "EUR", Amount: -50, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}},
	})
	if got.Total != 0 {
		t.Errorf("Total=%.2f, want 0 (all skipped)", got.Total)
	}
}

func TestReconcile_EmptyPayerOrSplitSkipped(t *testing.T) {
	got := Reconcile([]Booking{
		{Currency: "EUR", Amount: 100, Payer: "", Split: []ShareEntry{{Traveller: "alice"}}},
		{Currency: "EUR", Amount: 100, Payer: "alice", Split: nil},
	})
	if got.Total != 0 {
		t.Errorf("Total=%.2f, want 0", got.Total)
	}
}

func TestReconcile_DefaultWeightWhenZero(t *testing.T) {
	got := Reconcile([]Booking{
		{Currency: "EUR", Amount: 100, Payer: "alice", Split: []ShareEntry{
			{Traveller: "alice", Weight: 0},
			{Traveller: "bob", Weight: 0},
		}},
	})
	for _, b := range got.PerTraveller {
		if math.Abs(b.Owed-50) > 0.01 {
			t.Errorf("%s owed=%.2f, want 50 (zero weight defaults to 1)", b.Traveller, b.Owed)
		}
	}
}

func TestReconcile_ToleranceCollapsesPennyResidue(t *testing.T) {
	// 100 / 3 = 33.3333... per traveller. Settlement should collapse
	// the cents residue into the existing transfers without producing
	// a 0.00 ghost transfer.
	got := Reconcile([]Booking{
		{Currency: "EUR", Amount: 100, Payer: "alice", Split: []ShareEntry{
			{Traveller: "alice"}, {Traveller: "bob"}, {Traveller: "charlie"},
		}},
	})
	for _, tr := range got.Transfers {
		if tr.Amount < 0.01 {
			t.Errorf("emitted near-zero transfer: %+v", tr)
		}
	}
}

func TestRender_HappyPath(t *testing.T) {
	got := Reconcile([]Booking{
		{Category: "hotel", Currency: "EUR", Amount: 200, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}, {Traveller: "bob"}}},
	})
	rendered := Render(got)
	for _, want := range []string{"Trip total", "alice", "bob", "hotel"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("Render output missing %q; got: %s", want, rendered)
		}
	}
}

func TestRender_EmptySettlement(t *testing.T) {
	if got := Render(Settlement{}); !strings.Contains(got, "no bookings") {
		t.Errorf("empty settlement render=%q, want 'no bookings' message", got)
	}
}

func TestReconcile_PicksFirstNonEmptyCurrency(t *testing.T) {
	got := Reconcile([]Booking{
		{Amount: 100, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}},                  // empty currency
		{Currency: "USD", Amount: 100, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}}, // first non-empty
		{Currency: "EUR", Amount: 100, Payer: "alice", Split: []ShareEntry{{Traveller: "alice"}}}, // ignored
	})
	if got.Currency != "USD" {
		t.Errorf("Currency=%q, want USD", got.Currency)
	}
}
