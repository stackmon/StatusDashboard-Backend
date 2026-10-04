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

	if err = s.Shutdown(ctx); err != nil {
		logger.Fatal("app shutdown failed", zap.Error(err))
	}

	ch.Shutdown()

	logger.Info("app exited")
}
