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
			v, err := Memo(ctx, "HEL|BCN|2026-12-01", func(context.Context) (int, error) {
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
	if _, _ = Memo(ctx, "HEL|BCN|2026-12-02", func(context.Context) (int, error) { calls.Add(1); return 7, nil }); calls.Load() != 2 {
		t.Errorf("a different key did not run: %d calls", calls.Load())
	}
}

// The memo keeps an error too, and belongs to its pass: a new pass, or a
// context outside any pass, runs fn again.
func TestMemo_ScopedToItsPass(t *testing.T) {
	boom := errors.New("refused")
	var calls int
	fn := func(context.Context) (string, error) { calls++; return "", boom }

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

// Suppressing inside an active pass (a round-trip leg search inside a savings
// pass) keeps that pass's memo.
func TestSuppressSavingsPass_KeepsActivePass(t *testing.T) {
	pass := StartSavingsPass(context.Background())
	calls := 0
	fn := func(context.Context) (int, error) { calls++; return 1, nil }
	_, _ = Memo(pass, "leg", fn)
	_, _ = Memo(SuppressSavingsPass(pass), "leg", fn)
	if calls != 1 {
		t.Errorf("fn ran %d times, want 1: suppressing dropped the pass memo", calls)
	}
	if !SavingsPassSuppressed(SuppressSavingsPass(pass)) {
		t.Error("a suppressed pass context is not marked")
	}
}

// A caller that gives up returns its own context error, but the shared call
// runs on under the pass, and later callers get its real answer.
func TestMemo_CallerCancellationDoesNotPoison(t *testing.T) {
	pass := StartSavingsPass(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	fn := func(ctx context.Context) (int, error) {
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 42, nil
	}

	caller, cancel := context.WithCancel(pass)
	done := make(chan error, 1)
	go func() {
		_, err := Memo(caller, "k", fn)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller err = %v, want context.Canceled", err)
	}
	close(release)

	v, err := Memo(pass, "k", fn)
	if err != nil || v != 42 {
		t.Errorf("later caller got %d, %v; want 42 from the shared call, not the cancellation", v, err)
	}
}
