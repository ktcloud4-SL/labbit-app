// Package realtimetest는 realtime/terminal test가 공유하는 지원 코드다. production 코드는 이 package를 import하지 않는다.
package realtimetest

import (
	"sort"
	"sync"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// FakeClock은 Advance로만 시간이 흐르는 realtime.Clock이다. 60초 grace를 test에서 실제로 기다리지 않게 한다.
// timer callback은 실제 timer처럼 별도 goroutine에서 실행한다. 그래서 Advance를 호출한 test goroutine이 callback과 잠금 경쟁을 하지 않는다.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	seq    int
}

var _ realtime.Clock = (*FakeClock)(nil)

// NewFakeClock은 start 시각에서 시작하는 FakeClock을 만든다.
func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

type fakeTimer struct {
	clock *FakeClock
	at    time.Time
	seq   int
	f     func()
	done  bool
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) AfterFunc(d time.Duration, f func()) realtime.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &fakeTimer{clock: c, at: c.now.Add(d), seq: c.seq, f: f}
	c.timers = append(c.timers, t)
	return t
}

// Stop은 아직 실행되지 않은 timer를 취소한다. 이미 실행되었거나 취소되었다면 false다.
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}

// Advance는 시간을 d만큼 흐르게 하고 그 사이에 만료되는 timer를 만료 시각 순서대로 실행한다.
// callback은 별도 goroutine에서 시작한다. 실행이 끝나기를 기다리지 않으므로 호출자는 결과를 eventually로 확인한다.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.done && !t.at.After(c.now) {
			t.done = true
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].at.Equal(due[j].at) {
			return due[i].seq < due[j].seq
		}
		return due[i].at.Before(due[j].at)
	})
	c.mu.Unlock()

	for _, t := range due {
		go t.f()
	}
}

// Pending은 아직 실행되지 않았고 취소되지 않은 timer 수다.
func (c *FakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.done {
			n++
		}
	}
	return n
}
