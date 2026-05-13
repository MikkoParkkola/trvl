package providers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// --- cookies.go ---

func TestWithElicit_GetElicit(t *testing.T) {
	ctx := context.Background()

	// No elicit set.
	fn := getElicit(ctx)
	if fn != nil {
		t.Error("expected nil elicit on fresh context")
	}

	// Set elicit.
	called := false
	elicitFn := func(msg string) (bool, error) {
		called = true
		return true, nil
	}
	ctx2 := WithElicit(ctx, elicitFn)
	fn2 := getElicit(ctx2)
	if fn2 == nil {
		t.Fatal("expected non-nil elicit after WithElicit")
	}
	ok, err := fn2("test")
	if !called || !ok || err != nil {
		t.Errorf("elicit function not called correctly: called=%v ok=%v err=%v", called, ok, err)
	}
}

func TestGetElicit_NilContext(t *testing.T) {
	var ctx context.Context //nolint:SA1012 // intentional nil ctx for edge-case test
	fn := getElicit(ctx)
	if fn != nil {
		t.Error("expected nil for nil context")
	}
}

// --- mapping.go ---

func TestBookingBedType(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{1, "single bed"},
		{2, "double bed"},
		{3, "bunk bed"},
		{4, "futon"},
		{5, "sofa bed"},
		{6, "king bed"},
		{7, "queen bed"},
		{0, "bed"},  // default
		{99, "bed"}, // unknown
	}
	for _, tt := range tests {
		got := bookingBedType(tt.code)
		if got != tt.want {
			t.Errorf("bookingBedType(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestStripNonNumeric(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"€ 61", "61"},
		{"$100.50", "100.50"},
		{"price: -15.5", "-15.5"},
		{"no numbers here", ""},
		{"", ""},
		{"42", "42"},
		{"1,234.56", "1234.56"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripNonNumeric(tt.input)
			if got != tt.want {
				t.Errorf("stripNonNumeric(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- enrichment.go ---

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello <b>world</b>", "hello world"},
		{"line1<br>line2", "line1 line2"},
		{"line1<br/>line2", "line1 line2"},
		{"line1<BR />line2", "line1 line2"},
		{"<p>paragraph</p>", "paragraph"},
		{"no tags here", "no tags here"},
		{"", ""},
		{"multi  spaces", "multi spaces"},
		{"<a href='x'>link</a>", "link"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractAggregateRating(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		obj := map[string]any{
			"aggregateRating": map[string]any{
				"ratingValue": 4.5,
				"reviewCount": 100,
			},
		}
		rating, count := extractAggregateRating(obj)
		if rating != 4.5 {
			t.Errorf("rating = %v, want 4.5", rating)
		}
		if count != 100 {
			t.Errorf("count = %d, want 100", count)
		}
	})

	t.Run("no_rating", func(t *testing.T) {
		obj := map[string]any{"name": "Hotel"}
		rating, count := extractAggregateRating(obj)
		if rating != 0 || count != 0 {
			t.Errorf("expected (0, 0), got (%v, %d)", rating, count)
		}
	})

	t.Run("wrong_type", func(t *testing.T) {
		obj := map[string]any{
			"aggregateRating": "not a map",
		}
		rating, count := extractAggregateRating(obj)
		if rating != 0 || count != 0 {
			t.Errorf("expected (0, 0), got (%v, %d)", rating, count)
		}
	})
}

// --- registry.go ---

func TestNewRegistry_DefaultPath(t *testing.T) {
	// NewRegistry reads ~/.trvl/providers/ -- should succeed on any system.
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}
	if reg == nil {
		t.Fatal("registry should not be nil")
	}
	// May or may not have providers, but should not panic.
	_ = reg.List()
}

func TestNewRegistry_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt failed: %v", err)
	}
	if reg == nil {
		t.Fatal("registry should not be nil")
	}
	list := reg.List()
	if len(list) != 0 {
		t.Errorf("expected 0 providers, got %d", len(list))
	}
}

func TestNewRegistry_WithProviderFile(t *testing.T) {
	dir := t.TempDir()
	config := `{
		"id": "test_provider",
		"name": "Test Provider",
		"type": "hotel",
		"base_url": "https://example.com",
		"search_path": "/search"
	}`
	err := os.WriteFile(filepath.Join(dir, "test.json"), []byte(config), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt failed: %v", err)
	}

	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(list))
	}
	if list[0].ID != "test_provider" {
		t.Errorf("ID = %q, want test_provider", list[0].ID)
	}
}

func TestNewRegistry_Reload(t *testing.T) {
	dir := t.TempDir()
	config := `{
		"id": "p1",
		"name": "Provider 1",
		"type": "hotel",
		"base_url": "https://example.com"
	}`
	err := os.WriteFile(filepath.Join(dir, "p1.json"), []byte(config), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Add a second file after initial load.
	config2 := `{
		"id": "p2",
		"name": "Provider 2",
		"type": "hotel",
		"base_url": "https://example2.com"
	}`
	err = os.WriteFile(filepath.Join(dir, "p2.json"), []byte(config2), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := reg.Reload("p2")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("reloaded config should not be nil")
	}
	if cfg.ID != "p2" {
		t.Errorf("reloaded ID = %q, want p2", cfg.ID)
	}

	list := reg.List()
	if len(list) != 2 {
		t.Errorf("expected 2 providers after reload, got %d", len(list))
	}
}
