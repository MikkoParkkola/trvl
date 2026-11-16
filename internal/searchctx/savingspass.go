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
// value is the running pass's memo, or nil when the context is only
// suppressed (for example a round-trip leg search).
type savingsPassKey struct{}

type savingsPass struct {
	mu      sync.Mutex
	entries map[string]*memoEntry
}

type memoEntry struct {
	once sync.Once
	val  any
	err  error
}

// StartSavingsPass marks ctx as inside a savings pass and gives the pass a
// fresh memo. Callers check SavingsPassSuppressed first: a pass never starts
// inside another.
func StartSavingsPass(ctx context.Context) context.Context {
	return context.WithValue(ctx, savingsPassKey{}, &savingsPass{entries: map[string]*memoEntry{}})
}

// SuppressSavingsPass marks ctx so no savings pass starts in it, without a
// memo.
func SuppressSavingsPass(ctx context.Context) context.Context {
	return context.WithValue(ctx, savingsPassKey{}, (*savingsPass)(nil))
}

// SavingsPassSuppressed reports whether ctx is inside a savings pass, or was
// marked so none starts.
func SavingsPassSuppressed(ctx context.Context) bool {
	_, ok := ctx.Value(savingsPassKey{}).(*savingsPass)
	return ok
}

// Memo runs fn once per key within the savings pass carried by ctx and hands
// every caller that one result, error included. Outside a pass it just calls
// fn. Callers that ask concurrently wait for the first.
//
// ponytail: the first caller's context bounds the shared call; within one pass
// every detector shares the pass deadline, so that is the same budget.
func Memo[T any](ctx context.Context, key string, fn func() (T, error)) (T, error) {
	pass, _ := ctx.Value(savingsPassKey{}).(*savingsPass)
	if pass == nil {
		return fn()
	}
	pass.mu.Lock()
	e, ok := pass.entries[key]
	if !ok {
		e = &memoEntry{}
		pass.entries[key] = e
	}
	pass.mu.Unlock()
	e.once.Do(func() { e.val, e.err = fn() })
	v, _ := e.val.(T)
	return v, e.err
}
