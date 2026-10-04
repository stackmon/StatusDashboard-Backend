package checker

import (
	"context"
	"errors"
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

// Check runs one full scan and returns the combined error of its two halves. It
// is the body of the scheduler's scan task, which holds the advisory lock for the
// whole round. Cancellation is observed throughout the round, so a caller must
// not close the pool while Check is running; a round aborted mid-scan leaves the
// remaining events to the next tick.
func (ch *Checker) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var (
		wg      sync.WaitGroup
		mntErr  error
		infoErr error
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ch.CheckMaintenance(ctx); err != nil {
			ch.log.Error("error to check maintenances", zap.Error(err))
			mntErr = err
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ch.CheckInfoEvents(ctx); err != nil {
			ch.log.Error("error to check info events", zap.Error(err))
			infoErr = err
		}
	}()

	wg.Wait()
	return errors.Join(mntErr, infoErr)
}
