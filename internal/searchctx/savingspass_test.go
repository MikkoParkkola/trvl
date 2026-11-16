package searchctx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSavingsPass_MarksAndSuppresses(t *testing.T) {
	bg := context.Background()
	if SavingsPassSuppressed(bg) {
		t.Fatal("a plain context is marked")
	}
	if !SavingsPassSuppressed(StartSavingsPass(bg)) {
		t.Error("a started pass does not suppress a nested one")
	}
	if !SavingsPassSuppressed(SuppressSavingsPass(bg)) {
		t.Error("SuppressSavingsPass does not mark the context")
	}
}

// One query runs once per pass, however many detectors ask for it at once;
// every caller gets the one answer.
func TestMemo_RunsEachKeyOncePerPass(t *testing.T) {
	ctx := StartSavingsPass(context.Background())
	var calls atomic.Int32
	var wg sync.WaitGroup
	results := make([]int, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := Memo(ctx, "HEL|BCN|2026-12-01", func() (int, error) {
				calls.Add(1)
				return 42, nil
			})
			if err != nil {
				t.Errorf("memo error: %v", err)
			}
			results[i] = v
		}(i)
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Errorf("fn ran %d times, want 1", n)
	}
	for i, v := range results {
		if v != 42 {
			t.Errorf("caller %d got %d, want 42", i, v)
		}
	}
	if _, _ = Memo(ctx, "HEL|BCN|2026-12-02", func() (int, error) { calls.Add(1); return 7, nil }); calls.Load() != 2 {
		t.Errorf("a different key did not run: %d calls", calls.Load())
	}
}

// The memo keeps an error too, and belongs to its pass: a new pass, or a
// context outside any pass, runs fn again.
func TestMemo_ScopedToItsPass(t *testing.T) {
	boom := errors.New("refused")
	var calls int
	fn := func() (string, error) { calls++; return "", boom }

	pass := StartSavingsPass(context.Background())
	for range 2 {
		if _, err := Memo(pass, "k", fn); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the memoized error", err)
		}
	}
	if calls != 1 {
		t.Errorf("fn ran %d times in one pass, want 1", calls)
	}
	if _, _ = Memo(StartSavingsPass(context.Background()), "k", fn); calls != 2 {
		t.Errorf("a new pass reused the old memo: %d calls", calls)
	}
	if _, _ = Memo(SuppressSavingsPass(context.Background()), "k", fn); calls != 3 {
		t.Errorf("a suppressed context without a pass memoized: %d calls", calls)
	}
	if _, _ = Memo(context.Background(), "k", fn); calls != 4 {
		t.Errorf("a plain context memoized: %d calls", calls)
	}
}
