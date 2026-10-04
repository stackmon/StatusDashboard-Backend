package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// Advisory-lock keys for the scheduled tasks. They live in the SD3 reserved
// range 9000-9099 and are not shared with any other service.
const (
	KeyScan        int64 = 9001
	KeyNotifySweep int64 = 9002
	KeyRetention   int64 = 9003
)

// locker is the narrow db surface the scheduler needs; *db.DB satisfies it.
type locker interface {
	WithAdvisoryLock(ctx context.Context, key int64, fn func(context.Context) error) error
}

type Task struct {
	Name     string
	Interval time.Duration
	Key      int64
	Fn       func(ctx context.Context) error
}

type Scheduler struct {
	tasks  []Task
	locker locker
	log    *zap.Logger
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(l locker, log *zap.Logger) *Scheduler {
	return &Scheduler{locker: l, log: log}
}

func (s *Scheduler) Register(name string, interval time.Duration, key int64, fn func(ctx context.Context) error) {
	s.tasks = append(s.tasks, Task{Name: name, Interval: interval, Key: key, Fn: fn})
}

// Run starts one goroutine per registered task. It returns immediately.
func (s *Scheduler) Run(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, t := range s.tasks {
		s.wg.Add(1)
		go func(t Task) {
			defer s.wg.Done()
			s.runTask(ctx, t)
		}(t)
	}
}

func (s *Scheduler) runTask(ctx context.Context, t Task) {
	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The lock is taken once here, around the whole round; task
			// bodies must not take it again.
			err := s.locker.WithAdvisoryLock(ctx, t.Key, t.Fn)
			if errors.Is(err, db.ErrLockBusy) {
				s.log.Debug("another replica holds the lock, skipping this round", zap.String("task", t.Name))
				continue
			}
			if err != nil && ctx.Err() == nil {
				s.log.Error("task failed", zap.String("task", t.Name), zap.Error(err))
			}
		}
	}
}

// Stop cancels the schedule and waits for in-flight tasks to return, bounded
// by ctx.
func (s *Scheduler) Stop(ctx context.Context) {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn("timed out waiting for scheduled tasks to finish")
	}
}
