// Package clock is a tiny injectable time source so timing-dependent code
// (watcher WaitStable, sync keepalive, download polling) is testable
// without real sleeps. Production uses System; tests use Fake.
package clock

import (
	"sync"
	"time"
)

// Clock abstracts the parts of time that conduit depends on.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
	After(d time.Duration) <-chan time.Time
}

// System is the real clock.
type System struct{}

func (System) Now() time.Time                         { return time.Now() }
func (System) Sleep(d time.Duration)                  { time.Sleep(d) }
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a test clock. Sleep returns immediately (advancing virtual time
// and recording the duration). After returns a channel the test fires
// explicitly via Fire, so loops never busy-spin.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	sleeps  []time.Duration
	pending []chan time.Time
}

// NewFake starts at an arbitrary fixed instant.
func NewFake() *Fake {
	return &Fake{now: time.Unix(1_700_000_000, 0)}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Sleep(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleeps = append(f.sleeps, d)
	f.now = f.now.Add(d)
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, ch)
	return ch
}

// Fire delivers a tick to the oldest pending After channel. Returns false
// if there is nothing waiting.
func (f *Fake) Fire() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		return false
	}
	ch := f.pending[0]
	f.pending = f.pending[1:]
	f.now = f.now.Add(time.Second)
	ch <- f.now
	return true
}

// Sleeps returns the durations passed to Sleep, in order.
func (f *Fake) Sleeps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.sleeps...)
}
