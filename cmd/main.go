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

// shutdownTimeout bounds the in-flight request drain after SIGTERM.
const shutdownTimeout = 15 * time.Second

// taskStopTimeout bounds the wait for in-flight scheduled tasks and the
// notification worker during shutdown.
const taskStopTimeout = 30 * time.Second

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

	ch := checker.New(s.DB, logger, s.Publisher())

	sched := scheduler.New(s.DB, logger)
	sched.Register("scan", 2*time.Minute, scheduler.KeyScan, func(ctx context.Context) error {
		ch.Check(ctx)
		s.Publisher().Notify()
		return nil
	})
	if w := s.Worker(); w != nil {
		sched.Register("notify_sweep", 5*time.Minute, scheduler.KeyNotifySweep, w.Drain)
		sched.Register("retention", 24*time.Hour, scheduler.KeyRetention, w.RunRetention)
	}

	ctx, done := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer done()

	go func() {
		if err = s.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("app is failed to run", zap.Error(err))
		}
	}()

	sched.Run(ctx)

	var workerDone chan struct{}
	if w := s.Worker(); w != nil {
		workerDone = make(chan struct{})
		go func() {
			w.Run(ctx)
			close(workerDone)
		}()
	}

	<-ctx.Done()
	s.Log.Info("shutdown app")

	// The signal context is already cancelled, so the shutdown needs its own
	// deadline to drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Stop the scheduler before the pool is closed: a scan round holds a
	// dedicated connection, so the pool must stay open until in-flight tasks
	// finish.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), taskStopTimeout)
	defer stopCancel()
	sched.Stop(stopCtx)

	// The worker's context is the signal context, already cancelled; wait for
	// its in-flight drain to finish before the pool is closed.
	if workerDone != nil {
		select {
		case <-workerDone:
		case <-stopCtx.Done():
			logger.Warn("timed out waiting for the notification worker to stop")
		}
	}

	if err = s.Shutdown(shutdownCtx); err != nil {
		logger.Error("app shutdown failed", zap.Error(err))
	}

	logger.Info("app exited")
}
