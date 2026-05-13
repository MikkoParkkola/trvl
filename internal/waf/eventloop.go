package waf

import (
	"container/heap"
	"context"
	"sync"
	"time"

	"github.com/grafana/sobek"
)

// job is a callable scheduled on the loop. Microtasks carry when = zero time
// and are always dispatched before any delayed macrotask.
type job struct {
	id       int64
	when     time.Time
	interval time.Duration // >0 ⇒ re-arm after firing
	cb       sobek.Callable
	cancel   bool
}

// timerHeap orders macrotasks by their firing time. Safe to reuse stdlib heap.
type timerHeap []*job

func (h timerHeap) Len() int            { return len(h) }
func (h timerHeap) Less(i, j int) bool  { return h[i].when.Before(h[j].when) }
func (h timerHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x interface{}) { *h = append(*h, x.(*job)) }
func (h *timerHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// eventLoop is a minimal microtask + timer scheduler for sobek. It is NOT
// goroutine-safe for concurrent VM access: every job must run on the single
// OS goroutine that owns the Runtime. Async bridges (fetch) deposit their
// callbacks through enqueueFromGo, which synchronises via a channel drained
// from the loop goroutine.
type eventLoop struct {
	vm         *sobek.Runtime
	microtasks []func() error
	timers     timerHeap
	nextID     int64
	pending    int // in-flight host-side async operations (e.g. fetch)
	stopped    bool

	mu sync.Mutex
	in chan func() error // inbound jobs from Go goroutines
}

func newEventLoop(vm *sobek.Runtime) *eventLoop {
	return &eventLoop{vm: vm, in: make(chan func() error, 128)}
}

// scheduleMicrotask enqueues a Go-side callback to run as the next microtask.
func (l *eventLoop) scheduleMicrotask(fn func() error) {
	l.microtasks = append(l.microtasks, fn)
}

// scheduleTimer registers a JS callback to fire after d (or repeatedly at
// interval d if repeat=true). Returns the timer id used by clearTimeout.
func (l *eventLoop) scheduleTimer(cb sobek.Callable, d time.Duration, repeat bool) int64 {
	l.nextID++
	j := &job{id: l.nextID, when: time.Now().Add(d), cb: cb}
	if repeat {
		j.interval = d
	}
	heap.Push(&l.timers, j)
	return j.id
}

// clearTimer marks a scheduled timer as cancelled. It is lazily removed the
// next time the heap top references it.
func (l *eventLoop) clearTimer(id int64) {
	for _, t := range l.timers {
		if t.id == id {
			t.cancel = true
		}
	}
}

// trackPending is used by host bridges (fetch) to tell the loop "I have an
// async operation outstanding, do not exit yet". delta is +1 to start, -1 to
// complete.
func (l *eventLoop) trackPending(delta int) {
	l.pending += delta
	if l.pending < 0 {
		l.pending = 0
	}
}

// enqueueFromGo lets background goroutines push a VM-touching callback back
// onto the loop. The caller must bump pending before starting work and the
// callback should decrement it.
func (l *eventLoop) enqueueFromGo(fn func() error) {
	l.mu.Lock()
	stopped := l.stopped
	l.mu.Unlock()
	if stopped {
		return
	}
	l.in <- fn
}

// run drives the loop until one of:
//   - done() returns true (e.g. promise settled),
//   - ctx is cancelled / deadline hit,
//   - microtask+timer+pending queues are all empty.
//
// The return is the first error produced by any job; subsequent errors are
// dropped to keep the shutdown path simple.
func (l *eventLoop) run(ctx context.Context, done func() bool) error {
	defer func() {
		l.mu.Lock()
		l.stopped = true
		l.mu.Unlock()
	}()

	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for {
		if done != nil && done() {
			return firstErr
		}
		if ctx.Err() != nil {
			l.vm.Interrupt("waf: event loop deadline exceeded")
			return ctx.Err()
		}

		// 1. Drain microtasks fully before touching macrotasks.
		if len(l.microtasks) > 0 {
			mt := l.microtasks
			l.microtasks = nil
			for _, fn := range mt {
				record(fn())
				if done != nil && done() {
					return firstErr
				}
			}
			continue
		}

		// 2. Drain inbound jobs from Go (non-blocking).
		select {
		case fn := <-l.in:
			record(fn())
			continue
		default:
		}

		// 3. Fire any due timers.
		if n := l.fireDueTimers(record); n > 0 {
			continue
		}

		// 4. Nothing runnable — figure out how long to wait.
		if len(l.timers) == 0 && l.pending == 0 && len(l.microtasks) == 0 {
			return firstErr
		}

		var wait = 50 * time.Millisecond
		if len(l.timers) > 0 {
			d := time.Until(l.timers[0].when)
			if d < wait {
				wait = d
			}
			if wait < 0 {
				wait = 0
			}
		}

		// Block until something arrives or the next timer is due.
		select {
		case <-ctx.Done():
			l.vm.Interrupt("waf: event loop deadline exceeded")
			return ctx.Err()
		case fn := <-l.in:
			record(fn())
		case <-time.After(wait):
		}
	}
}

// fireDueTimers pops every timer whose deadline has passed, invokes it, and
// re-arms intervals. Returns the number of timers fired (cancelled timers are
// silently discarded and do not count).
func (l *eventLoop) fireDueTimers(record func(error)) int {
	fired := 0
	now := time.Now()
	for len(l.timers) > 0 {
		top := l.timers[0]
		if top.cancel {
			heap.Pop(&l.timers)
			continue
		}
		if top.when.After(now) {
			break
		}
		heap.Pop(&l.timers)
		_, err := top.cb(sobek.Undefined())
		record(err)
		fired++
		if top.interval > 0 && !top.cancel {
			top.when = now.Add(top.interval)
			heap.Push(&l.timers, top)
		}
	}
	return fired
}
