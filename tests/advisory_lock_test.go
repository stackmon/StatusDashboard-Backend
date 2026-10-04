package tests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func TestWithAdvisoryLock_MutualExclusion(t *testing.T) {
	d, _ := newNotifDB(t)

	err := d.WithAdvisoryLock(context.Background(), 9001, func(context.Context) error {
		err := d.WithAdvisoryLock(context.Background(), 9001, func(context.Context) error {
			return nil
		})
		assert.ErrorIs(t, err, db.ErrLockBusy, "same session cannot re-acquire the lock")
		return nil
	})
	require.NoError(t, err)
}

func TestWithAdvisoryLock_ConcurrentHolders(t *testing.T) {
	d, _ := newNotifDB(t)

	const key = int64(9001)
	const workers = 4

	var mu sync.Mutex
	running := 0
	maxRunning := 0

	run := func() error {
		return d.WithAdvisoryLock(context.Background(), key, func(context.Context) error {
			mu.Lock()
			running++
			if running > maxRunning {
				maxRunning = running
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			running--
			mu.Unlock()
			return nil
		})
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				err := run()
				if err != nil && !errors.Is(err, db.ErrLockBusy) {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, maxRunning, "the lock must be held by at most one goroutine at a time")
}

func TestWithAdvisoryLock_DistinctKeys(t *testing.T) {
	d, _ := newNotifDB(t)

	err := d.WithAdvisoryLock(context.Background(), 9001, func(context.Context) error {
		return d.WithAdvisoryLock(context.Background(), 9002, func(context.Context) error {
			return nil
		})
	})
	require.NoError(t, err, "different keys must not block each other")
}

func TestWithAdvisoryLock_ReleasedAfterCancelledFn(t *testing.T) {
	d, _ := newNotifDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	err := d.WithAdvisoryLock(ctx, 9001, func(context.Context) error {
		cancel()
		return context.Canceled
	})
	assert.ErrorIs(t, err, context.Canceled)

	// The lock must have been released despite the cancelled context.
	err = d.WithAdvisoryLock(context.Background(), 9001, func(context.Context) error {
		return nil
	})
	require.NoError(t, err, "lock must be released even when the context was cancelled")
}
