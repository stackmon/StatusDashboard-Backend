package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"

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

	ch, err := checker.New(c, logger)
	if err != nil {
		logger.Error("fail to init checker", zap.Error(err))
	}

	// Wire the checker's publisher to the app's single delivery worker so
	// checker-driven transitions wake it immediately (same shared queue).
	if ch != nil {
		// NotifyFunc is nil when notifications are disabled; leave the publisher
		// unwired rather than installing a nil callback.
		if notify := s.NotifyFunc(); notify != nil {
			ch.Publisher().SetNotify(notify)
		}
	}

	stopCh := make(chan struct{})

	ctx, done := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer done()

	go func() {
		if err = s.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("app is failed to run", zap.Error(err))
		}
	}()

	go func() {
		ch.Run(stopCh)
	}()

	<-ctx.Done()
	s.Log.Info("shutdown app")

	if err = s.Shutdown(ctx); err != nil {
		logger.Fatal("app shutdown failed", zap.Error(err))
	}

	if err = ch.Shutdown(stopCh); err != nil {
		logger.Fatal("checker shutdown failed", zap.Error(err))
	}

	logger.Info("app exited")
}
