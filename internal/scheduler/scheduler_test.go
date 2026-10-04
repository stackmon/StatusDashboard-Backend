package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// fakeLocker records lock acquisitions and can simulate a busy lock.
type fakeLocker struct {
	mu   sync.Mutex
	keys []int64
	busy bool
	fn   func(ctx context.Context) error
}

func (f *fakeLocker) WithAdvisoryLock(ctx context.Context, key int64, fn func(context.Context) error) error {
	f.mu.Lock()
	f.keys = append(f.keys, key)
	busy := f.busy
	f.mu.Unlock()
	if busy {
		return db.ErrLockBusy
	}
	return fn(ctx)
}

func (f *fakeLocker) lockCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}

func TestScheduler_RunsTaskOnInterval(t *testing.T) {
	locker := &fakeLocker{fn: func(context.Context) error { return nil }}
	s := New(locker, zap.NewNop())
	s.Register("t", 10*time.Millisecond, KeyScan, locker.fn)

	ctx, cancel := context.WithCancel(context.Background())
	s.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for locker.lockCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	_ = s.Stop(context.Background())

	assert.GreaterOrEqual(t, locker.lockCount(), 1, "the task must run at least once")
}

func TestScheduler_SkipsRoundWhenLockBusy(t *testing.T) {
	var calls int
	locker := &fakeLocker{
		busy: true,
		fn: func(context.Context) error {
			calls++
			return nil
		},
	}
	s := New(locker, zap.NewNop())
	s.Register("t", 10*time.Millisecond, KeyScan, locker.fn)

	ctx, cancel := context.WithCancel(context.Background())
	s.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for locker.lockCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	_ = s.Stop(context.Background())

	assert.GreaterOrEqual(t, locker.lockCount(), 1, "the lock must be attempted")
	assert.Equal(t, 0, calls, "a busy lock must skip the round without running the task")
}

func TestScheduler_StopWaitsForInFlightTask(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	locker := &fakeLocker{fn: func(context.Context) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}}
	s := New(locker, zap.NewNop())
	s.Register("t", 10*time.Millisecond, KeyScan, locker.fn)

	ctx, cancel := context.WithCancel(context.Background())
	s.Run(ctx)

	<-started // the task is now in flight

	stopped := make(chan struct{})
	go func() {
		_ = s.Stop(context.Background())
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop must not return while the task is still running")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop must return once the in-flight task finishes")
	}
	cancel()
}

func TestScheduler_KeepsRunningAfterTaskError(t *testing.T) {
	var calls atomic.Int64
	locker := &fakeLocker{fn: func(context.Context) error {
		calls.Add(1)
		return errors.New("boom")
	}}
	s := New(locker, zap.NewNop())
	s.Register("t", 10*time.Millisecond, KeyScan, locker.fn)

	ctx, cancel := context.WithCancel(context.Background())
	s.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	_ = s.Stop(context.Background())

	assert.GreaterOrEqual(t, calls.Load(), int64(2), "a failing task must not stop the schedule")
}
