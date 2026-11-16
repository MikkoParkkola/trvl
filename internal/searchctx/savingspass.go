package searchctx

import (
	"context"
	"sync"
)

// A savings pass is the travel-hacks engine running against a finished flight
// or ground search. Its detectors run flight and ground searches of their own;
// those must never start a second pass (unbounded fan-out, and every nested
// search unfiltered), so flights and ground share this one marker.

// savingsPassKey marks a context in which no new savings pass may start. Its
// value is the running pass, or nil when the context is only suppressed (for
// example a round-trip leg search outside any pass).
type savingsPassKey struct{}

type savingsPass struct {
	ctx     context.Context // the pass's own context: shared calls run under it
	mu      sync.Mutex
	entries map[string]*memoEntry
}

type memoEntry struct {
	done chan struct{} // closed once val and err are set
	val  any
	err  error
}

// StartSavingsPass marks ctx as inside a savings pass and gives the pass a
// fresh memo. Shared calls run under the returned context, so start the pass
// on a context that already carries the pass deadline. Callers check
// SavingsPassSuppressed first: a pass never starts inside another.
func StartSavingsPass(ctx context.Context) context.Context {
	pass := &savingsPass{entries: map[string]*memoEntry{}}
	pass.ctx = context.WithValue(ctx, savingsPassKey{}, pass)
	return pass.ctx
}

// SuppressSavingsPass marks ctx so no savings pass starts in it. Inside a
// running pass it changes nothing: the pass already suppresses, and its memo
// stays in reach.
func SuppressSavingsPass(ctx context.Context) context.Context {
	if SavingsPassSuppressed(ctx) {
		return ctx
	}
	return context.WithValue(ctx, savingsPassKey{}, (*savingsPass)(nil))
}

// SavingsPassSuppressed reports whether ctx is inside a savings pass, or was
// marked so none starts.
func SavingsPassSuppressed(ctx context.Context) bool {
	_, ok := ctx.Value(savingsPassKey{}).(*savingsPass)
	return ok
}

// InSavingsPass reports whether ctx carries a running pass (and its memo).
func InSavingsPass(ctx context.Context) bool {
	pass, _ := ctx.Value(savingsPassKey{}).(*savingsPass)
	return pass != nil
}

// Memo runs fn once per key within the savings pass carried by ctx and hands
// every caller that one result, error included. The call runs under the pass
// context, so a caller that gives up (its own ctx done) returns its context
// error without cancelling the call or poisoning the result for the others.
// Outside a pass it just calls fn(ctx).
func Memo[T any](ctx context.Context, key string, fn func(context.Context) (T, error)) (T, error) {
	pass, _ := ctx.Value(savingsPassKey{}).(*savingsPass)
	if pass == nil {
		return fn(ctx)
	}
	pass.mu.Lock()
	e, ok := pass.entries[key]
	if !ok {
		e = &memoEntry{done: make(chan struct{})}
		pass.entries[key] = e
		go func() {
			defer close(e.done)
			e.val, e.err = fn(pass.ctx)
		}()
	}
	pass.mu.Unlock()
	select {
	case <-e.done:
		v, _ := e.val.(T)
		return v, e.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}
