package ground

import (
	"context"
	"strings"
	"testing"
)

// --- generateCorrelationID ---

func TestGenerateCorrelationID_Format(t *testing.T) {
	id := generateCorrelationID()
	if id == "" {
		t.Fatal("expected non-empty correlation ID")
	}
	// Format: <hex>-<hex>-<hex>-<hex>-<hex>_<hex>-<hex>-<hex>-<hex>-<hex>
	// There should be exactly 4 dashes in the first half and 4 in the second,
	// separated by an underscore.
	parts := strings.Split(id, "_")
	if len(parts) != 2 {
		t.Errorf("expected 1 underscore in ID, got %q", id)
	}
}

func TestGenerateCorrelationID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		id := generateCorrelationID()
		if seen[id] {
			t.Errorf("duplicate correlation ID: %q", id)
		}
		seen[id] = true
	}
}

func TestGenerateCorrelationID_Hex(t *testing.T) {
	id := generateCorrelationID()
	// Strip underscore and dashes, result should be all hex digits.
	clean := strings.ReplaceAll(id, "-", "")
	clean = strings.ReplaceAll(clean, "_", "")
	for _, c := range clean {
		if ('0' > c || c > '9') && ('a' > c || c > 'f') {
			t.Errorf("non-hex character %q in ID %q", c, id)
		}
	}
}

// --- convertTaxiFare ---

func TestConvertTaxiFare_EURPassthrough(t *testing.T) {
	ctx := context.Background()
	low, high, cur := convertTaxiFare(ctx, 10.0, 20.0, "EUR")
	if cur != "EUR" {
		t.Errorf("expected EUR, got %q", cur)
	}
	if low != 10.0 || high != 20.0 {
		t.Errorf("expected (10, 20), got (%.2f, %.2f)", low, high)
	}
}

func TestConvertTaxiFare_EmptyCurrencyDefaultsToEUR(t *testing.T) {
	ctx := context.Background()
	low, high, cur := convertTaxiFare(ctx, 15.0, 25.0, "")
	if cur != "EUR" {
		t.Errorf("expected EUR for empty currency, got %q", cur)
	}
	if low != 15.0 || high != 25.0 {
		t.Errorf("expected (15, 25), got (%.2f, %.2f)", low, high)
	}
}

func TestConvertTaxiFare_WhitespaceCurrency(t *testing.T) {
	ctx := context.Background()
	low, high, cur := convertTaxiFare(ctx, 10.0, 20.0, "  EUR  ")
	if cur != "EUR" {
		t.Errorf("expected EUR with trimmed whitespace, got %q", cur)
	}
	if low != 10.0 || high != 20.0 {
		t.Errorf("expected (10, 20), got (%.2f, %.2f)", low, high)
	}
}

func TestConvertTaxiFare_LowNotGreaterThanHigh(t *testing.T) {
	ctx := context.Background()
	// When conversion fails (unknown currency), falls back to EUR values.
	low, high, _ := convertTaxiFare(ctx, 10.0, 20.0, "EUR")
	if low > high {
		t.Errorf("low (%.2f) > high (%.2f)", low, high)
	}
}
