package checker

import (
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

const defaultPeriod = time.Minute * 2

type Checker struct {
	db       *db.DB
	log      *zap.Logger
	notifier *notification.Publisher
	// lastIDs are the earliest planned or in_progress maintenance/info events ID.
	lastMntID  uint
	lastInfoID uint
}

func New(c *conf.Config, log *zap.Logger) (*Checker, error) {
	dbNew, err := db.New(c)
	if err != nil {
		return nil, err
	}
	ncfg, err := notification.ConfigFromConf(c)
	if err != nil {
		return nil, err
	}
	return &Checker{db: dbNew, log: log, notifier: notification.NewPublisher(ncfg, dbNew)}, nil
}

func (ch *Checker) Check() {
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

func (ch *Checker) Run(done chan struct{}) {
	ch.log.Info("checker is started")
	ticker := time.NewTicker(defaultPeriod)
	defer ticker.Stop()

	for { //nolint:nolintlint
		select {
		case <-done:
			return
		case <-ticker.C:
			ch.Check()
		}
	}
}

func (ch *Checker) Shutdown(done chan struct{}) error {
	ch.log.Info("start to shutdown checker")
	done <- struct{}{}
	close(done)
	return ch.db.Close()
}

// Close releases the checker's database pool without going through the Run loop.
func (ch *Checker) Close() error {
	return ch.db.Close()
}

// Publisher returns the checker's notification publisher so the delivery worker's
// Notify can be wired in during app startup.
func (ch *Checker) Publisher() *notification.Publisher {
	return ch.notifier
}
