package main

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func TestProvidersCmd_NonNil(t *testing.T) {
	cmd := providersCmd()
	if cmd == nil {
		t.Fatal("providersCmd() returned nil")
	}
}

func TestProvidersCmd_Use(t *testing.T) {
	cmd := providersCmd()
	if cmd.Use != "providers" {
		t.Errorf("providersCmd Use = %q, want providers", cmd.Use)
	}
}

func TestProvidersCmd_Alias(t *testing.T) {
	cmd := providersCmd()
	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "provider" {
		t.Errorf("providersCmd aliases = %v, want [provider]", cmd.Aliases)
	}
}

func TestProvidersCmd_HasSubcommands(t *testing.T) {
	cmd := providersCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	for _, want := range []string{"list", "enable", "disable", "reset", "status"} {
		if !names[want] {
			t.Errorf("providersCmd missing subcommand %q", want)
		}
	}
}

func TestProvidersListCmd_EmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	reg, err := providers.NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	configs := reg.List()
	if len(configs) != 0 {
		t.Errorf("expected 0 providers, got %d", len(configs))
	}
}

func TestProvidersListCmd_WithProviders(t *testing.T) {
	dir := t.TempDir()
	reg, err := providers.NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	_ = reg.Save(&providers.ProviderConfig{
		ID:       "kiwi",
		Name:     "Kiwi.com",
		Category: "flights",
		Endpoint: "https://api.tequila.kiwi.com",
		Consent:  &providers.ConsentRecord{Granted: true},
	})
	_ = reg.Save(&providers.ProviderConfig{
		ID:       "booking",
		Name:     "Booking.com",
		Category: "hotels",
		Endpoint: "https://api.booking.com",
		Consent:  &providers.ConsentRecord{Granted: true},
	})

	configs := reg.List()
	if len(configs) != 2 {
		t.Errorf("expected 2 providers, got %d", len(configs))
	}
}

func TestProviderConfig_EndpointDomain(t *testing.T) {
	cfg := providers.ProviderConfig{Endpoint: "https://api.tequila.kiwi.com/v1"}
	got := cfg.EndpointDomain()
	if got != "api.tequila.kiwi.com" {
		t.Errorf("EndpointDomain = %q, want api.tequila.kiwi.com", got)
	}
}

func TestProviderConfig_Status(t *testing.T) {
	cfg := providers.ProviderConfig{}
	if cfg.Status() != "new" {
		t.Errorf("Status (no data) = %q, want new", cfg.Status())
	}
}
