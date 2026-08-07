package watch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestBoltMigrationBacksUpAndPreservesLegacyGeneration(t *testing.T) {
	dir := t.TempDir()
	legacyWatch := Watch{
		ID:          "legacy-1",
		Type:        "flight",
		Origin:      "HEL",
		Destination: "AMS",
		Currency:    "EUR",
		CreatedAt:   time.Now().Add(-time.Hour),
	}
	legacyPoints := make([]PricePoint, maxObservationsPerWatch+25)
	for i := range legacyPoints {
		legacyPoints[i] = PricePoint{
			WatchID:   legacyWatch.ID,
			Price:     float64(123 + i),
			Currency:  "EUR",
			Timestamp: time.Now().Add(time.Duration(i-maxObservationsPerWatch) * time.Minute),
		}
	}
	if err := saveJSON(filepath.Join(dir, "watches.json"), []Watch{legacyWatch}); err != nil {
		t.Fatal(err)
	}
	if err := saveJSON(filepath.Join(dir, "price-history.json"), legacyPoints); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPrice(legacyWatch.ID, 111, "EUR"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "watch.db")); err != nil {
		t.Fatalf("authoritative database not created: %v", err)
	}
	for _, name := range []string{"watches.json", "price-history.json"} {
		matches, err := filepath.Glob(filepath.Join(dir, name+".bak-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("%s backups = %d, want 1", name, len(matches))
		}
		if name == "price-history.json" {
			var backedUp []PricePoint
			if err := loadJSON(matches[0], &backedUp); err != nil {
				t.Fatal(err)
			}
			if len(backedUp) != len(legacyPoints) {
				t.Fatalf("legacy backup points = %d, want %d", len(backedUp), len(legacyPoints))
			}
		}
	}

	reloaded := NewStore(dir)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.History(legacyWatch.ID); len(got) != maxObservationsPerWatch || got[len(got)-1].Price != 111 {
		last := PricePoint{}
		if len(got) > 0 {
			last = got[len(got)-1]
		}
		t.Fatalf("migrated history count = %d, last = %#v", len(got), last)
	}
}

func TestBoltTransactionRollsBackWatchAndHistoryTogether(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected transaction failure")
	err := store.withBoltMutation(func(tx *bolt.Tx, watches *[]Watch) (bool, error) {
		*watches = append(*watches, Watch{ID: "partial", Type: "flight", Origin: "HEL", Destination: "AMS"})
		if _, err := appendHistoryPointTx(tx, PricePoint{WatchID: "partial", Price: 99, Currency: "EUR", Timestamp: time.Now()}); err != nil {
			return false, err
		}
		return true, injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("error = %v, want injected failure", err)
	}

	reloaded := NewStore(store.Dir())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 || len(reloaded.AllHistory()) != 0 {
		t.Fatalf("partial generation became visible: watches=%#v history=%#v", reloaded.List(), reloaded.AllHistory())
	}
}

func TestBoltReaderSeesOnlyCompleteGenerations(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	db, err := store.openBolt(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mutationReady := make(chan struct{})
	releaseMutation := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- db.Update(func(tx *bolt.Tx) error {
			watches := []Watch{{ID: "generation-2", Type: "flight", Origin: "HEL", Destination: "AMS"}}
			if err := putJSON(tx.Bucket(bucketMeta), keyWatches, watches); err != nil {
				return err
			}
			if _, err := appendHistoryPointTx(tx, PricePoint{WatchID: "generation-2", Price: 88, Currency: "EUR", Timestamp: time.Now()}); err != nil {
				return err
			}
			close(mutationReady)
			<-releaseMutation
			return nil
		})
	}()
	<-mutationReady

	watches, history, err := loadBoltState(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 0 || len(history) != 0 {
		t.Fatalf("reader observed an uncommitted partial generation: watches=%d history=%d", len(watches), len(history))
	}
	close(releaseMutation)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}

	watches, history, err = loadBoltState(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 || len(history) != 1 || watches[0].ID != history[0].WatchID {
		t.Fatalf("reader observed an incomplete committed generation: watches=%#v history=%#v", watches, history)
	}
}

func TestRecordPriceDoesNotDecodeRetainedHistory(t *testing.T) {
	store := NewStore(t.TempDir())
	store.watches = []Watch{{ID: "w1", Type: "flight", Origin: "HEL", Destination: "AMS", Currency: "EUR"}}
	store.history = make([]PricePoint, 5000)
	for i := range store.history {
		store.history[i] = PricePoint{WatchID: "w1", Price: float64(100 + i%20), Currency: "EUR", Timestamp: time.Now().Add(time.Duration(i) * time.Second)}
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	decoded := 0
	historyDecodeHook = func() { decoded++ }
	t.Cleanup(func() { historyDecodeHook = nil })
	if err := store.RecordPrice("w1", 95, "EUR"); err != nil {
		t.Fatal(err)
	}
	if decoded != 0 {
		t.Fatalf("hot write decoded %d retained points", decoded)
	}
	if got := store.History("w1"); len(got) != maxObservationsPerWatch {
		t.Fatalf("retained points = %d, want %d", len(got), maxObservationsPerWatch)
	}
}

func TestBoltSaveFailsClosedWhenHistoryRefreshFails(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.RecordPrice("watch-1", 100, "EUR"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.databasePath(), []byte("not a bbolt database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err == nil {
		t.Fatal("Save succeeded after the authoritative history database became unreadable")
	}
}
