package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

// fixtureDir is the inboxparser test-fixture directory, reached from cmd/trvl.
const fixtureDir = "../../internal/inboxparser/testdata"

func fixturePath(name string) string { return filepath.Join(fixtureDir, name) }

// --- import-inbox: pure core ---

func TestCollectInbox_ParsesFixturesIntoBookings(t *testing.T) {
	paths := []string{
		fixturePath("klm.eml"),
		fixturePath("booking.eml"),
		fixturePath("airbnb.eml"),
		fixturePath("negative.eml"),
	}

	res := collectInbox(trips.Trip{Name: "x", Status: "planning"}, paths, os.ReadFile)

	if res.Summary.Parsed != 3 {
		t.Errorf("Parsed = %d, want 3", res.Summary.Parsed)
	}
	if res.Summary.Unrecognised != 1 {
		t.Errorf("Unrecognised = %d, want 1 (negative.eml)", res.Summary.Unrecognised)
	}
	if res.Summary.BookingsAdded != 3 {
		t.Errorf("BookingsAdded = %d, want 3", res.Summary.BookingsAdded)
	}
	if len(res.Trip.Bookings) != 3 {
		t.Fatalf("Bookings = %d, want 3", len(res.Trip.Bookings))
	}

	refs := map[string]bool{}
	for _, b := range res.Trip.Bookings {
		refs[b.Reference] = true
	}
	for _, want := range []string{"ABC123", "1234567890", "HMABCDEF"} {
		if !refs[want] {
			t.Errorf("expected booking reference %q to be present, have %v", want, refs)
		}
	}
}

func TestCollectInbox_Idempotent(t *testing.T) {
	paths := []string{fixturePath("klm.eml")}

	first := collectInbox(trips.Trip{Name: "x", Status: "planning"}, paths, os.ReadFile)
	if first.Summary.BookingsAdded != 1 {
		t.Fatalf("first BookingsAdded = %d, want 1", first.Summary.BookingsAdded)
	}

	// Re-ingesting the same mail into the already-populated trip must add nothing.
	second := collectInbox(first.Trip, paths, os.ReadFile)
	if second.Summary.BookingsAdded != 0 {
		t.Errorf("second BookingsAdded = %d, want 0 (idempotent)", second.Summary.BookingsAdded)
	}
	if len(second.Trip.Bookings) != 1 {
		t.Errorf("Bookings after re-ingest = %d, want 1", len(second.Trip.Bookings))
	}
}

func TestCollectInbox_FailSoftOnReadError(t *testing.T) {
	paths := []string{"missing-a.eml", fixturePath("klm.eml"), "missing-b.eml"}

	reader := func(p string) ([]byte, error) {
		if strings.HasPrefix(filepath.Base(p), "missing") {
			return nil, errors.New("no such file")
		}
		return os.ReadFile(p)
	}

	res := collectInbox(trips.Trip{Name: "x", Status: "planning"}, paths, reader)

	if len(res.Skipped) != 2 {
		t.Errorf("Skipped = %d, want 2", len(res.Skipped))
	}
	if len(res.Read) != 1 {
		t.Errorf("Read = %d, want 1", len(res.Read))
	}
	if res.Summary.Parsed != 1 {
		t.Errorf("Parsed = %d, want 1 (the readable fixture)", res.Summary.Parsed)
	}
}

// --- import-inbox: path expansion ---

func TestExpandEmlPaths_DirAndFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.eml"), "x")
	mustWrite(t, filepath.Join(dir, "b.EML"), "x") // case-insensitive ext
	mustWrite(t, filepath.Join(dir, "skip.txt"), "x")

	explicit := filepath.Join(dir, "skip.txt") // explicit file kept despite ext
	got, err := expandEmlPaths([]string{dir, explicit})
	if err != nil {
		t.Fatalf("expandEmlPaths: %v", err)
	}

	// a.eml + b.EML from dir, plus explicit skip.txt = 3, sorted, de-duped.
	if len(got) != 3 {
		t.Fatalf("got %d paths, want 3: %v", len(got), got)
	}
	for _, want := range []string{"a.eml", "b.EML", "skip.txt"} {
		if !containsBase(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

func TestExpandEmlPaths_MissingArgErrors(t *testing.T) {
	if _, err := expandEmlPaths([]string{"definitely-not-here-xyz"}); err == nil {
		t.Error("expected error for missing path")
	}
}

// --- import-inbox: command wiring + persistence ---

func TestImportInboxCmd_SavePersistsTrip(t *testing.T) {
	setTestHome(t, t.TempDir())

	cmd := importInboxCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{fixturePath("klm.eml"), fixturePath("booking.eml"), "--save", "Krakow"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("loadTripStore: %v", err)
	}
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("stored trips = %d, want 1", len(list))
	}
	if got := len(list[0].Bookings); got != 2 {
		t.Errorf("persisted bookings = %d, want 2", got)
	}
	if !strings.Contains(out.String(), "Bookings added:  2") {
		t.Errorf("summary missing booking count, got:\n%s", out.String())
	}
}

func TestImportInboxCmd_NoEmlFound(t *testing.T) {
	setTestHome(t, t.TempDir())
	emptyDir := t.TempDir()

	cmd := importInboxCmd()
	cmd.SetArgs([]string{emptyDir})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error when no .eml files found")
	}
}

func TestImportInboxCmd_SaveAndTripIDConflict(t *testing.T) {
	setTestHome(t, t.TempDir())

	cmd := importInboxCmd()
	cmd.SetArgs([]string{fixturePath("klm.eml"), "--save", "x", "--trip-id", "y"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error when both --save and --trip-id are set")
	}
}

// --- trip-id merge + json format ---

func TestImportInboxCmd_TripIDMergesIntoExisting(t *testing.T) {
	setTestHome(t, t.TempDir())

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("loadTripStore: %v", err)
	}
	id, err := store.Add(trips.Trip{Name: "Existing", Status: "planning"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	cmd := importInboxCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{fixturePath("klm.eml"), "--trip-id", id})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	reloaded, err := loadTripStore()
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, err := reloaded.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Bookings) != 1 {
		t.Errorf("merged bookings = %d, want 1", len(got.Bookings))
	}
	if !strings.Contains(out.String(), "Saved trip: "+id) {
		t.Errorf("expected saved-trip line for %s, got:\n%s", id, out.String())
	}
}

func TestImportInboxCmd_JSONFormat(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Cleanup(func() { format = "" })
	format = "json"

	cmd := importInboxCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{fixturePath("klm.eml")})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "\"summary\"") {
		t.Errorf("JSON output missing summary key:\n%s", out.String())
	}
}

// --- helpers ---

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func containsBase(paths []string, base string) bool {
	for _, p := range paths {
		if filepath.Base(p) == base {
			return true
		}
	}
	return false
}

// bindPersistentFormat is intentionally omitted: the package-level `format`
// var defaults to the table path, which these tests assert against.
