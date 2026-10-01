package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stackmon/otc-status-dashboard/internal/api"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

const (
	readHeaderTimeout = 3 * time.Second
	// notificationStaleThreshold flags a processing row as stuck in the /metrics gauge.
	notificationStaleThreshold = 2 * time.Minute
)

type App struct {
	// Configuration
	conf *conf.Config
	// Router
	api *api.API
	// zap logger
	Log *zap.Logger
	// db connection
	DB *db.DB
	// http server
	srv *http.Server
	// metrics server, listening on its own port (nil when notifications are disabled)
	metricsSrv *http.Server
	// notification delivery worker (nil when notifications are disabled)
	worker       *notification.Worker
	workerCancel context.CancelFunc
}

func New(c *conf.Config, log *zap.Logger) (*App, error) {
	dbNew, err := db.New(c)
	if err != nil {
		return nil, err
	}

	apiNew, err := api.New(c, log, dbNew)
	if err != nil {
		return nil, err
	}

	// Build the delivery worker on the app's shared pool — it must NOT open its own.
	// Budget PostgreSQL connections as max_open_conns_per_pod * number_of_pods.
	worker, metricsHandler, err := buildWorker(c, log, dbNew, apiNew)
	if err != nil {
		return nil, err
	}

	s := &http.Server{
		Addr:              fmt.Sprintf(":%s", c.Port),
		Handler:           apiNew.Router(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	a := &App{api: apiNew, Log: log, conf: c, DB: dbNew, srv: s, worker: worker}

	if metricsHandler != nil {
		a.metricsSrv = &http.Server{
			Addr:              fmt.Sprintf(":%s", c.MetricsPort),
			Handler:           metricsHandler,
			ReadHeaderTimeout: readHeaderTimeout,
		}
	}

	return a, nil
}

// buildWorker constructs the delivery worker and the handler for the private metrics
// listener, and wires the API publisher's hot-path signal to the worker. Both results
// are nil when notifications are disabled.
func buildWorker(
	c *conf.Config, log *zap.Logger, dbNew *db.DB, apiNew *api.API,
) (*notification.Worker, http.Handler, error) {
	ncfg, err := notification.ConfigFromConf(c)
	if err != nil {
		return nil, nil, err
	}
	if !ncfg.Enabled {
		return nil, nil, nil
	}

	if err = dbNew.EnsureNotificationSchema(); err != nil {
		return nil, nil, err
	}

	sender, err := notification.NewSMTPSender(ncfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build smtp sender: %w", err)
	}

	// Dedicated registry so /metrics exposes just the notification signals.
	reg := prometheus.NewRegistry()
	metrics := notification.NewMetrics()
	metrics.MustRegister(reg)
	reg.MustRegister(notification.NewStatsCollector(dbNew, notificationStaleThreshold))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	worker, err := notification.NewWorker(ncfg, dbNew, sender, log, metrics)
	if err != nil {
		return nil, nil, fmt.Errorf("build notification worker: %w", err)
	}

	apiNew.Publisher().SetNotify(worker.Notify)
	return worker, mux, nil
}

// NotifyFunc returns the worker's wake-up callback, or nil when notifications are
// disabled. Used to wire the checker's publisher to the same worker.
func (a *App) NotifyFunc() func() {
	if a.worker == nil {
		return nil
	}
	return a.worker.Notify
}

func (a *App) Run() error {
	if a.worker != nil {
		var ctx context.Context
		ctx, a.workerCancel = context.WithCancel(context.Background())
		go a.worker.Run(ctx)
	}
	if a.metricsSrv != nil {
		go func() {
			a.Log.Info("metrics server started", zap.String("addr", a.metricsSrv.Addr))
			if err := a.metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.Log.Error("metrics server failed", zap.Error(err))
			}
		}()
	}
	return a.srv.ListenAndServe()
}

func (a *App) Shutdown(ctx context.Context) error {
	if a.workerCancel != nil {
		a.workerCancel()
	}
	if a.metricsSrv != nil {
		if err := a.metricsSrv.Shutdown(ctx); err != nil {
			a.Log.Error("metrics server shutdown", zap.Error(err))
		}
	}
	// TODO: add a proper shutdown for a database
	return a.srv.Shutdown(ctx)
}
