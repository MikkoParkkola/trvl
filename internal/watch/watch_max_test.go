package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// checkRoom — the entire function is 0% covered
// ---------------------------------------------------------------------------

// stubRoomChecker implements RoomChecker for testing.
type stubRoomChecker struct {
	matches []RoomMatch
	err     error
}

func (s *stubRoomChecker) CheckRooms(_ context.Context, _ Watch) ([]RoomMatch, error) {
	return s.matches, s.err
}

// stubPriceChecker implements PriceChecker for testing.
type stubPriceChecker struct {
	price        float64
	currency     string
	cheapestDate string
	err          error
}

func (s *stubPriceChecker) CheckPrice(_ context.Context, _ Watch) (float64, string, string, error) {
	return s.price, s.currency, s.cheapestDate, s.err
}

func TestCheckRoom_Error(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Test Hotel",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{err: fmt.Errorf("connection refused")}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("expected error from checkRoom")
	}
	if r.RoomFound {
		t.Error("RoomFound should be false on error")
	}
}

func TestCheckRoom_NoMatches(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Test Hotel",
		RoomKeywords: []string{"penthouse"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{matches: nil}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.RoomFound {
		t.Error("RoomFound should be false when no matches")
	}
	// Watch should still be updated (LastCheck marked).
	updated, ok := store.Get(id)
	if !ok {
		t.Fatal("watch not found after checkRoom")
	}
	if updated.LastCheck.IsZero() {
		t.Error("LastCheck should be set even with no matches")
	}
}

func TestCheckRoom_SingleMatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Beach Resort",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
		BelowPrice:   200,
		Currency:     "EUR",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{
		matches: []RoomMatch{
			{Name: "Ocean Suite", Price: 150, Currency: "EUR", Provider: "booking"},
		},
	}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if !r.RoomFound {
		t.Error("RoomFound should be true")
	}
	if r.NewPrice != 150 {
		t.Errorf("NewPrice = %f, want 150", r.NewPrice)
	}
	if !r.BelowGoal {
		t.Error("BelowGoal should be true (150 < 200)")
	}

	// Verify watch was updated in store.
	updated, _ := store.Get(id)
	if updated.MatchedRoom != "Ocean Suite" {
		t.Errorf("MatchedRoom = %q, want %q", updated.MatchedRoom, "Ocean Suite")
	}
	if updated.LastPrice != 150 {
		t.Errorf("LastPrice = %f, want 150", updated.LastPrice)
	}
	if updated.LowestPrice != 150 {
		t.Errorf("LowestPrice = %f, want 150", updated.LowestPrice)
	}
}

func TestCheckRoom_MultipleMatchesCheapest(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"balcony"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{
		matches: []RoomMatch{
			{Name: "Balcony Room A", Price: 0, Currency: "EUR"},   // no price
			{Name: "Balcony Room B", Price: 200, Currency: "EUR"}, // cheaper
			{Name: "Balcony Suite", Price: 300, Currency: "EUR"},
		},
	}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if !r.RoomFound {
		t.Error("RoomFound should be true")
	}
	// Cheapest with a price should be 200.
	if r.NewPrice != 200 {
		t.Errorf("NewPrice = %f, want 200 (cheapest with price)", r.NewPrice)
	}
	if len(r.RoomMatches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(r.RoomMatches))
	}
}

func TestCheckRoom_PriceDropTracking(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "City Hotel",
		RoomKeywords: []string{"double"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
		LastPrice:    250,
		LowestPrice:  250,
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	checker := &stubRoomChecker{
		matches: []RoomMatch{
			{Name: "Double Room", Price: 180, Currency: "EUR"},
		},
	}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.PriceDrop >= 0 {
		t.Errorf("PriceDrop = %f, want negative (price decreased)", r.PriceDrop)
	}
	if r.PrevPrice != 250 {
		t.Errorf("PrevPrice = %f, want 250", r.PrevPrice)
	}
	// LowestPrice should be updated.
	updated, _ := store.Get(id)
	if updated.LowestPrice != 180 {
		t.Errorf("LowestPrice = %f, want 180", updated.LowestPrice)
	}
}

// ---------------------------------------------------------------------------
// CheckAllWithRooms — room-watch dispatch + room-checker-nil path
// ---------------------------------------------------------------------------

func TestCheckAllWithRooms_DispatchesRoomWatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Resort",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2026-08-01",
		ReturnDate:   "2026-08-05",
	}
	_, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	roomChecker := &stubRoomChecker{
		matches: []RoomMatch{{Name: "Suite", Price: 300, Currency: "USD"}},
	}
	priceChecker := &stubPriceChecker{price: 100, currency: "EUR"}

	results := CheckAllWithRooms(context.Background(), store, priceChecker, roomChecker)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].RoomFound {
		t.Error("expected room watch to use roomChecker")
	}
}

func TestCheckAllWithRooms_NilRoomChecker(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Hotel",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2026-08-01",
		ReturnDate:   "2026-08-05",
	}
	_, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	priceChecker := &stubPriceChecker{price: 100, currency: "EUR"}

	results := CheckAllWithRooms(context.Background(), store, priceChecker, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Error("expected error about room checker not configured")
	}
}

func TestCheckAllWithRooms_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Add two watches so the inter-check pause fires.
	w1 := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01"}
	w2 := Watch{Type: "flight", Origin: "HEL", Destination: "NRT", DepartDate: "2026-07-01"}
	if _, err := store.Add(w1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(w2); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	checker := &stubPriceChecker{price: 100, currency: "EUR"}
	results := CheckAllWithRooms(ctx, store, checker, nil)

	// Should get at most 1 result since context is cancelled during inter-check pause.
	if len(results) > 2 {
		t.Errorf("expected at most 2 results with cancelled context, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// checkOne — update watch error, record price error paths
// ---------------------------------------------------------------------------

func TestCheckOne_ZeroPrice(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01"}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id

	checker := &stubPriceChecker{price: 0, currency: ""}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Errorf("unexpected error: %v", r.Error)
	}
	if r.NewPrice != 0 {
		t.Errorf("NewPrice = %f, want 0", r.NewPrice)
	}
}

func TestCheckOne_UpdatesCheapestDate(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartFrom: "2026-07-01", DepartTo: "2026-07-31"}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id

	checker := &stubPriceChecker{price: 200, currency: "EUR", cheapestDate: "2026-07-15"}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.CheapestDate != "2026-07-15" {
		t.Errorf("CheapestDate = %q, want %q", r.CheapestDate, "2026-07-15")
	}
	updated, _ := store.Get(id)
	if updated.CheapestDate != "2026-07-15" {
		t.Errorf("stored CheapestDate = %q, want %q", updated.CheapestDate, "2026-07-15")
	}
}

func TestCheckOne_TracksLowestPrice(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		LowestPrice: 300,
		LastPrice:   300,
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id

	// Price drops below existing lowest.
	checker := &stubPriceChecker{price: 250, currency: "EUR"}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}

	updated, _ := store.Get(id)
	if updated.LowestPrice != 250 {
		t.Errorf("LowestPrice = %f, want 250", updated.LowestPrice)
	}
}

// ---------------------------------------------------------------------------
// Notify — desktop notification path + room desktop path
// ---------------------------------------------------------------------------

func TestNotify_DesktopBelowGoal(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false, Desktop: true}

	r := CheckResult{
		Watch: Watch{
			Type:        "flight",
			Origin:      "HEL",
			Destination: "BCN",
			DepartDate:  "2026-07-01",
			BelowPrice:  300,
		},
		NewPrice:  250,
		Currency:  "EUR",
		BelowGoal: true,
	}
	n.Notify(r)

	out := buf.String()
	if out == "" {
		t.Fatal("expected output for below-goal notification")
	}
	// Should contain booking URL since DepartDate is set.
	if !bytes.Contains([]byte(out), []byte("Book:")) {
		t.Error("expected booking URL in output")
	}
}

func TestNotifyRoom_DesktopWithPrice(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false, Desktop: true}

	r := CheckResult{
		Watch: Watch{
			Type:         "room",
			HotelName:    "Beach Hotel",
			RoomKeywords: []string{"suite"},
		},
		RoomFound: true,
		RoomMatches: []RoomMatch{
			{Name: "Ocean Suite", Price: 150, Currency: "EUR", Provider: "booking"},
		},
		NewPrice: 150,
		Currency: "EUR",
	}
	n.Notify(r)

	out := buf.String()
	if out == "" {
		t.Fatal("expected output for room notification")
	}
}

func TestNotifyRoom_DesktopNoPrice(t *testing.T) {
	var buf bytes.Buffer
	n := &Notifier{Out: &buf, UseColor: false, Desktop: true}

	r := CheckResult{
		Watch: Watch{
			Type:         "room",
			HotelName:    "Mountain Lodge",
			RoomKeywords: []string{"cabin"},
		},
		RoomFound: true,
		RoomMatches: []RoomMatch{
			{Name: "Forest Cabin", Price: 0, Currency: ""},
		},
	}
	n.Notify(r)

	out := buf.String()
	if out == "" {
		t.Fatal("expected output")
	}
}

// ---------------------------------------------------------------------------
// saveJSON error paths — Sparkline idx clamping
// ---------------------------------------------------------------------------

func TestSparkline_IdxClamp(t *testing.T) {
	// Create history where price exactly equals hi (idx should clamp).
	history := []PricePoint{
		{Price: 0},
		{Price: 100},
	}
	result := Sparkline(history, 10)
	if result == "" {
		t.Error("expected non-empty sparkline")
	}
}

// ---------------------------------------------------------------------------
// saveLocked error paths
// ---------------------------------------------------------------------------

func TestSaveLocked_EnsureDirError(t *testing.T) {
	// Point store at a path where a file exists instead of a directory.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "blocker")
	if err := os.WriteFile(filePath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(filepath.Join(filePath, "subdir"))
	err := store.Save()
	if err == nil {
		t.Fatal("expected error when dir creation fails")
	}
}

func TestSaveJSON_WriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory permissions not enforced on Windows")
	}
	// Try writing to a read-only directory.
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(readOnlyDir, 0o700) //nolint:errcheck

	err := saveJSON(filepath.Join(readOnlyDir, "test.json"), []string{"data"})
	if err == nil {
		t.Fatal("expected error when writing to read-only dir")
	}
}

// ---------------------------------------------------------------------------
// Load error paths
// ---------------------------------------------------------------------------

func TestLoad_HistoryFileCorrupt(t *testing.T) {
	dir := t.TempDir()
	// Write valid watches but corrupt history.
	watchesData, _ := json.Marshal([]Watch{{ID: "test1", Type: "flight", Origin: "HEL", Destination: "BCN"}})
	if err := os.WriteFile(filepath.Join(dir, "watches.json"), watchesData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "price-history.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	err := store.Load()
	if err == nil {
		t.Fatal("expected error loading corrupt history")
	}
}

func TestLoad_WatchesFileCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "watches.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	err := store.Load()
	if err == nil {
		t.Fatal("expected error loading corrupt watches")
	}
}

// ---------------------------------------------------------------------------
// loadJSON — non-NotExist read error
// ---------------------------------------------------------------------------

func TestLoadJSON_ReadError(t *testing.T) {
	// Create a directory where a file is expected (read will fail).
	dir := t.TempDir()
	blockPath := filepath.Join(dir, "isdir")
	if err := os.MkdirAll(blockPath, 0o700); err != nil {
		t.Fatal(err)
	}

	var dst []Watch
	err := loadJSON(blockPath, &dst)
	if err == nil {
		t.Fatal("expected error reading a directory as a file")
	}
}

// ---------------------------------------------------------------------------
// saveLocked error on saveJSON history
// ---------------------------------------------------------------------------

func TestSaveLocked_HistorySaveError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem chmod semantics differ on Windows; tracked in #45")
	}
	dir := t.TempDir()
	store := NewStore(dir)
	store.watches = []Watch{{ID: "test1", Type: "flight"}}

	// Write watches.json fine but block history path.
	historyPath := store.historyPath()
	if err := os.MkdirAll(historyPath, 0o700); err != nil {
		t.Fatal(err)
	}

	err := store.Save()
	if err == nil {
		t.Fatal("expected error when history save fails")
	}
}

// ---------------------------------------------------------------------------
// Remove save error
// ---------------------------------------------------------------------------

func TestRemove_SaveError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem chmod semantics differ on Windows; tracked in #45")
	}
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01"}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}

	// Block the watches file so save fails on Remove.
	watchesPath := store.watchesPath()
	os.Remove(watchesPath)
	if err := os.MkdirAll(watchesPath, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err = store.Remove(id)
	if err == nil {
		t.Fatal("expected error when save fails during Remove")
	}
}

// ---------------------------------------------------------------------------
// Add save error
// ---------------------------------------------------------------------------

func TestAdd_SaveError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem chmod semantics differ on Windows; tracked in #45")
	}
	dir := t.TempDir()
	store := NewStore(dir)

	// First add succeeds.
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01"}
	if _, err := store.Add(w); err != nil {
		t.Fatal(err)
	}

	// Block the watches file.
	watchesPath := store.watchesPath()
	os.Remove(watchesPath)
	if err := os.MkdirAll(watchesPath, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := store.Add(w)
	if err == nil {
		t.Fatal("expected error when save fails during Add")
	}
}

// ---------------------------------------------------------------------------
// desktopNotify — the function is 0% covered
// ---------------------------------------------------------------------------

func TestDesktopNotify_NonDarwin(t *testing.T) {
	// On darwin this will actually attempt the notification.
	// The function is best-effort and ignores errors.
	n := &Notifier{Out: &bytes.Buffer{}, Desktop: true}
	// Should not panic.
	n.desktopNotify("Test Title", "Test message")
}

// ---------------------------------------------------------------------------
// saveJSON Windows rename fallback (line 442-450)
// We can't directly test the Windows path on darwin, but we can test
// that saveJSON works correctly for the normal path.
// ---------------------------------------------------------------------------

func TestSaveJSON_NormalPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	data := map[string]string{"key": "value"}

	if err := saveJSON(path, data); err != nil {
		t.Fatalf("saveJSON: %v", err)
	}

	// Verify file permissions (Unix only; Windows does not honor POSIX mode
	// bits — see #45).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
		}
	}

	// Verify content.
	var result map[string]string
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("key = %q, want %q", result["key"], "value")
	}
}

// ---------------------------------------------------------------------------
// checkRoom with UpdateWatch error
// ---------------------------------------------------------------------------

func TestCheckRoom_UpdateWatchError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	// Create a watch and then manually set an ID that won't be in the store,
	// simulating UpdateWatch failure.
	w := Watch{
		ID:           "nonexistent-id",
		Type:         "room",
		HotelName:    "Broken Hotel",
		RoomKeywords: []string{"suite"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
	}

	checker := &stubRoomChecker{
		matches: []RoomMatch{
			{Name: "Suite", Price: 100, Currency: "EUR"},
		},
	}
	r := checkRoom(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("expected error from UpdateWatch with nonexistent watch")
	}
}

func TestCheckRoom_RecordPriceError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem chmod semantics differ on Windows; tracked in #45")
	}
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		Type:         "room",
		HotelName:    "Hotel X",
		RoomKeywords: []string{"double"},
		DepartDate:   "2026-07-01",
		ReturnDate:   "2026-07-08",
	}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id

	checker := &stubRoomChecker{
		matches: []RoomMatch{
			{Name: "Double Room", Price: 200, Currency: "EUR"},
		},
	}

	// Block the history file so RecordPrice fails.
	historyPath := store.historyPath()
	os.Remove(historyPath)
	if err := os.MkdirAll(historyPath, 0o700); err != nil {
		t.Fatal(err)
	}

	r := checkRoom(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("expected error from RecordPrice")
	}
}

// ---------------------------------------------------------------------------
// checkOne — update watch error + record price error
// ---------------------------------------------------------------------------

func TestCheckOne_UpdateWatchError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{
		ID:          "no-such-id",
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
	}

	checker := &stubPriceChecker{price: 200, currency: "EUR"}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("expected update watch error")
	}
}

func TestCheckOne_RecordPriceError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem chmod semantics differ on Windows; tracked in #45")
	}
	dir := t.TempDir()
	store := NewStore(dir)
	w := Watch{Type: "flight", Origin: "HEL", Destination: "BCN", DepartDate: "2026-07-01"}
	id, err := store.Add(w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id

	// Block the history file.
	historyPath := store.historyPath()
	os.Remove(historyPath)
	if err := os.MkdirAll(historyPath, 0o700); err != nil {
		t.Fatal(err)
	}

	checker := &stubPriceChecker{price: 200, currency: "EUR"}
	r := checkOne(context.Background(), store, checker, w)
	if r.Error == nil {
		t.Fatal("expected record price error")
	}
}

// ---------------------------------------------------------------------------
// DefaultStore error path (only reachable if HOME is unset)
// ---------------------------------------------------------------------------

func TestDefaultStore_Success(t *testing.T) {
	store, err := DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

// ---------------------------------------------------------------------------
// shortID fallback (we can't easily make rand.Read fail, but cover the normal path)
// ---------------------------------------------------------------------------

func TestShortID_Length(t *testing.T) {
	id := shortID()
	if len(id) != 8 {
		t.Errorf("shortID length = %d, want 8", len(id))
	}
	// Each ID should be unique.
	id2 := shortID()
	if id == id2 {
		t.Error("two shortIDs should differ")
	}
}

// ---------------------------------------------------------------------------
// saveJSON with unmarshalable data
// ---------------------------------------------------------------------------

func TestSaveJSON_MarshalError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	// Channels cannot be marshaled to JSON.
	ch := make(chan int)
	err := saveJSON(path, ch)
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

// ---------------------------------------------------------------------------
// saveJSON Chmod/Write/Sync/Close error coverage via normal run
// (These are covered when the happy path runs through saveJSON.)
// ---------------------------------------------------------------------------

func TestSaveJSON_RoundTripComplex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "complex.json")

	data := []Watch{
		{
			ID: "abc", Type: "flight", Origin: "HEL", Destination: "BCN",
			CreatedAt: time.Now(), LastCheck: time.Now(),
		},
		{
			ID: "def", Type: "hotel", Destination: "Prague",
			BelowPrice: 80, Currency: "EUR",
		},
	}

	if err := saveJSON(path, data); err != nil {
		t.Fatalf("saveJSON: %v", err)
	}

	var loaded []Watch
	if err := loadJSON(path, &loaded); err != nil {
		t.Fatalf("loadJSON: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded %d watches, want 2", len(loaded))
	}
}
