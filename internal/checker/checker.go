package checker

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

type Checker struct {
	db       *db.DB
	log      *zap.Logger
	notifier *notification.Publisher
}

// New builds a Checker on the app's shared database pool and notification
// publisher. It owns neither: the pool and the publisher are closed and
// wired by the app.
func New(database *db.DB, log *zap.Logger, notifier *notification.Publisher) *Checker {
	return &Checker{db: database, log: log, notifier: notifier}
}

// Check runs one full scan. It is the body of the scheduler's scan task,
// which holds the advisory lock for the whole round.
func (ch *Checker) Check(ctx context.Context) {
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
