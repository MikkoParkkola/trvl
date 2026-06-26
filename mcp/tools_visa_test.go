package mcp

import (
	"context"
	"testing"
)

func TestCheckVisaTool_Definition(t *testing.T) {
	t.Parallel()
	tool := checkVisaTool()
	if tool.Name != "check_visa" {
		t.Errorf("Name = %q, want check_visa", tool.Name)
	}
	// Dual-mode tool: passport/destination are not hard-required at the schema
	// level so list_countries can run without them. The lookup-path requirement
	// is enforced in the handler instead. All three inputs must be advertised.
	for _, key := range []string{"passport", "destination", "list_countries"} {
		if _, ok := tool.InputSchema.Properties[key]; !ok {
			t.Errorf("missing input property %q", key)
		}
	}
}

func TestHandleCheckVisa_Success(t *testing.T) {
	t.Parallel()
	content, structured, err := handleCheckVisa(context.Background(), map[string]any{
		"passport":    "IT",
		"destination": "JP",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	if structured == nil {
		t.Fatal("expected structured result")
	}
}

func TestHandleCheckVisa_MissingArgsErrors(t *testing.T) {
	t.Parallel()
	if _, _, err := handleCheckVisa(context.Background(), map[string]any{}, nil, nil, nil); err == nil {
		t.Fatal("expected error when passport and destination are absent")
	}
	if _, _, err := handleCheckVisa(context.Background(), map[string]any{"passport": "FI"}, nil, nil, nil); err == nil {
		t.Fatal("expected error when destination is absent")
	}
}

func TestHandleCheckVisa_ListCountries(t *testing.T) {
	t.Parallel()
	content, structured, err := handleCheckVisa(context.Background(), map[string]any{
		"list_countries": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	list, ok := structured.(visaCountryList)
	if !ok {
		t.Fatalf("structured result = %T, want visaCountryList", structured)
	}
	if !list.Success {
		t.Error("expected success=true")
	}
	if len(list.Countries) == 0 {
		t.Fatal("expected a non-empty country list")
	}
	for i, c := range list.Countries {
		if c.Code == "" || c.Name == "" {
			t.Fatalf("country[%d] = %+v, want non-empty code and name", i, c)
		}
	}
}

func TestHandleCheckVisa_ListCountriesIgnoresPassportDestination(t *testing.T) {
	t.Parallel()
	// list_countries takes precedence; missing passport/destination must not error.
	_, structured, err := handleCheckVisa(context.Background(), map[string]any{
		"list_countries": true,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := structured.(visaCountryList); !ok {
		t.Fatalf("structured result = %T, want visaCountryList", structured)
	}
}
