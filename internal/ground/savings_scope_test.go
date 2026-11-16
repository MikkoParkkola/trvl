package ground

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
)

// An include filter scopes the whole search: the savings pass, whose
// detectors search flights and every ground provider, does not run.
func TestAttachGroundHackSaving_SkippedUnderIncludeFilter(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return nil
	})
	opts := SearchOptions{Providers: []string{"european_sleeper"}}
	attachGroundHackSaving(context.Background(), okGroundResult(), "Amsterdam", "Berlin", "2026-12-01", opts)
	if called {
		t.Error("savings pass ran under an include provider filter")
	}
}

// A ground savings pass never starts inside another one, such as a flight
// search's pass whose detectors run ground searches.
func TestAttachGroundHackSaving_NotInsideAnotherPass(t *testing.T) {
	called := false
	withStubComposer(t, func(context.Context, HackComposeRequest) *models.HackSaving {
		called = true
		return nil
	})
	inFlightPass := searchctx.StartSavingsPass(context.Background())
	attachGroundHackSaving(inFlightPass, okGroundResult(), "Helsinki", "Tallinn", "2026-12-01", SearchOptions{})
	if called {
		t.Error("ground savings pass started inside another savings pass")
	}
}

// The pass carries the shared marker, so its detectors' own flight and ground
// searches start no pass of their own.
func TestAttachGroundHackSaving_MarksItsPass(t *testing.T) {
	marked := false
	withStubComposer(t, func(ctx context.Context, _ HackComposeRequest) *models.HackSaving {
		marked = searchctx.SavingsPassSuppressed(ctx)
		return nil
	})
	attachGroundHackSaving(context.Background(), okGroundResult(), "Helsinki", "Tallinn", "2026-12-01", SearchOptions{})
	if !marked {
		t.Error("the savings pass context does not carry the shared marker")
	}
}
