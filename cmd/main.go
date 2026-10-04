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

	ch := checker.New(s.DB, logger, s.Publisher())

	ctx, done := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer done()

	go func() {
		if err = s.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("app is failed to run", zap.Error(err))
		}
	}()

	go ch.Run()

	<-ctx.Done()
	s.Log.Info("shutdown app")

	// Stop the checker before the pool is closed: Check runs synchronously, so
	// this waits for an in-flight scan to finish before App.Shutdown closes the
	// database pool.
	ch.Shutdown()

	// The signal context is already cancelled, so the shutdown needs its own
	// deadline to drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err = s.Shutdown(shutdownCtx); err != nil {
		logger.Error("app shutdown failed", zap.Error(err))
	}

	logger.Info("app exited")
}
