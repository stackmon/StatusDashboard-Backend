package checker

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

const defaultPeriod = time.Minute * 2

// scanLockKey guards the full scan across replicas. It lives in the SD3
// reserved advisory-lock range 9000-9099; it will move to internal/scheduler
// when the unified scheduler lands.
const scanLockKey int64 = 9001

type Checker struct {
	db       *db.DB
	log      *zap.Logger
	notifier *notification.Publisher
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

// New builds a Checker on the app's shared database pool and notification
// publisher. It owns neither: the pool and the publisher are closed and
// wired by the app.
func New(database *db.DB, log *zap.Logger, notifier *notification.Publisher) *Checker {
	return &Checker{db: database, log: log, notifier: notifier, done: make(chan struct{})}
}

func (ch *Checker) Check() {
	// One lock per round so only one replica scans at a time; the scan is
	// idempotent, so a skipped round costs nothing.
	err := ch.db.WithAdvisoryLock(context.Background(), scanLockKey, func(context.Context) error {
		ch.runScan()
		return nil
	})
	if errors.Is(err, db.ErrLockBusy) {
		ch.log.Debug("another replica holds the scan lock, skipping this round")
		return
	}
	if err != nil {
		ch.log.Error("failed to acquire the scan lock", zap.Error(err))
	}
}

func (ch *Checker) runScan() {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		err := ch.CheckMaintenance()
		if err != nil {
			ch.log.Error("error to check maintenances", zap.Error(err))
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		err := ch.CheckInfoEvents()
		if err != nil {
			ch.log.Error("error to check info events", zap.Error(err))
		}
		wg.Done()
	}()

	wg.Wait()
}

func (ch *Checker) Run() {
	ch.log.Info("checker is started")
	ctx, cancel := context.WithCancel(context.Background())
	ch.mu.Lock()
	ch.cancel = cancel
	ch.mu.Unlock()
	defer cancel()

	ticker := time.NewTicker(defaultPeriod)
	defer ticker.Stop()

	for { //nolint:nolintlint
		select {
		case <-ctx.Done():
			close(ch.done)
			return
		case <-ticker.C:
			ch.Check()
		}
	}
}

// Shutdown stops the Run loop and waits for it to exit. It is safe to call
// multiple times and without Run having started.
func (ch *Checker) Shutdown() {
	ch.log.Info("start to shutdown checker")
	ch.mu.Lock()
	cancel := ch.cancel
	ch.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-ch.done
}
