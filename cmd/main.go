package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/app"
	"github.com/stackmon/otc-status-dashboard/internal/checker"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/scheduler"
)

const (
	// shutdownTimeout bounds the in-flight request drain after SIGTERM.
	shutdownTimeout = 15 * time.Second
	// taskStopTimeout bounds the wait for in-flight scheduled tasks and the
	// notification worker during shutdown.
	taskStopTimeout = 30 * time.Second

	scanInterval      = time.Minute * 2
	sweepInterval     = time.Minute * 5
	retentionInterval = time.Hour * 24
)

func main() {
	c, err := conf.LoadConf()
	if err != nil {
		log.Fatalf("failed to parse configuration: %s", err.Error())
	}

	logger := conf.NewLogger(c.LogLevel)
	c.Log(logger)

	s, err := app.New(c, logger)
	if err != nil {
		logger.Fatal("fail to init app", zap.Error(err))
	}

	ctx, done := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer done()

	sched := newScheduler(s, logger)
	go runServer(s, logger)
	sched.Run(ctx)
	workerDone := startWorker(s, ctx)

	<-ctx.Done()
	s.Log.Info("shutdown app")
	shutdown(s, sched, logger, workerDone)
	logger.Info("app exited")
}

// newScheduler registers every periodic task. The advisory lock that keeps a task
// single-replica is taken by the scheduler, not by the task body.
func newScheduler(s *app.App, logger *zap.Logger) *scheduler.Scheduler {
	ch := checker.New(s.DB, logger, s.Publisher())

	sched := scheduler.New(s.DB, logger)
	sched.Register("scan", scanInterval, scheduler.KeyScan, func(ctx context.Context) error {
		if err := ch.Check(ctx); err != nil {
			return err
		}
		s.Publisher().Notify()
		return nil
	})
	if w := s.Worker(); w != nil {
		sched.Register("notify_sweep", sweepInterval, scheduler.KeyNotifySweep, w.Drain)
		sched.Register("retention", retentionInterval, scheduler.KeyRetention, w.RunRetention)
	}
	return sched
}

func runServer(s *app.App, logger *zap.Logger) {
	if err := s.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("app is failed to run", zap.Error(err))
	}
}

// startWorker runs the notification worker on ctx and returns a channel closed
// when it has stopped, or nil when notifications are disabled.
func startWorker(s *app.App, ctx context.Context) chan struct{} {
	w := s.Worker()
	if w == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	return done
}

func shutdown(s *app.App, sched *scheduler.Scheduler, logger *zap.Logger, workerDone chan struct{}) {
	// The signal context is already cancelled, so the shutdown needs its own
	// deadline to drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), taskStopTimeout)
	defer stopCancel()

	// A scan round holds a dedicated connection, so the pool must outlive the
	// scheduled work.
	schedErr := sched.Stop(stopCtx)
	workerErr := waitWorker(stopCtx, workerDone, logger)

	if err := s.Shutdown(shutdownCtx); err != nil {
		logger.Error("app shutdown failed", zap.Error(err))
	}

	// Never close the pool while in-flight work may still be running.
	if schedErr != nil || workerErr != nil {
		logger.Error("in-flight work did not stop before the deadline; leaving the database pool open",
			zap.Error(errors.Join(schedErr, workerErr)))
		return
	}
	if err := s.DB.Close(); err != nil {
		logger.Error("database close failed", zap.Error(err))
	}
}

func waitWorker(ctx context.Context, workerDone chan struct{}, logger *zap.Logger) error {
	if workerDone == nil {
		return nil
	}
	select {
	case <-workerDone:
		return nil
	case <-ctx.Done():
		logger.Warn("timed out waiting for the notification worker to stop")
		return ctx.Err()
	}
}
