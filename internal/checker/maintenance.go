package checker

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

type MntStatusHistory struct {
	hasReviewed   bool
	hasPlanned    bool
	hasInProgress bool
	hasCompleted  bool
	hasCancelled  bool
}

func (st *MntStatusHistory) hasStatus(status event.Status) bool {
	switch status {
	case event.MaintenanceReviewed:
		return st.hasReviewed
	case event.MaintenancePlanned:
		return st.hasPlanned
	case event.MaintenanceInProgress:
		return st.hasInProgress
	case event.MaintenanceCompleted:
		return st.hasCompleted
	case event.MaintenanceCancelled:
		return st.hasCancelled
	default:
		return false
	}
}

func (st *MntStatusHistory) setStatus(status event.Status) {
	switch status {
	case event.MaintenanceReviewed:
		st.hasReviewed = true
	case event.MaintenancePlanned:
		st.hasPlanned = true
	case event.MaintenanceInProgress:
		st.hasInProgress = true
	case event.MaintenanceCompleted:
		st.hasCompleted = true
	case event.MaintenanceCancelled:
		st.hasCancelled = true
	default:
	}
}

func (ch *Checker) CheckMaintenance() error {
	ch.log.Info("check maintenances statuses")

	maintenances, err := ch.db.GetMaintenances()
	if err != nil {
		return err
	}

	for _, mn := range maintenances {
		// Draft maintenances are not processed by the checker — they await
		// manual approval (reviewed) or rejection (cancelled) via the API.
		if mn.Status == event.MaintenancePendingReview {
			continue
		}

		if processErr := ch.processMaintenance(mn); processErr != nil {
			ch.log.Error("failed to process maintenance",
				zap.Uint("mntID", mn.ID), zap.Error(processErr))
			continue
		}
	}

	ch.log.Info("finished checking maintenances")

	return nil
}

// needsRefetch reports whether the batch-loaded state already agrees with the
// status the checker would compute. When it does, the event is steady-state and
// the per-event refetch can be skipped; when it does not, a fresh read is needed
// before the read-modify-write.
func needsRefetch(mn *db.Incident) bool {
	return mn.Status != calculateCurrentMntStatus(calculateMntStatusHistory(mn), mn)
}

func (ch *Checker) processMaintenance(mn *db.Incident) error {
	// Decide from the batch-loaded state whether the status will change. Only
	// then refetch: a fresh read immediately before the read-modify-write
	// shrinks the version-conflict window, and the write is the only place the
	// backfilled statuses persist. Steady-state events (no change) skip the
	// refetch, but every event is still scanned so manual DB edits are caught.
	if !needsRefetch(mn) {
		return nil
	}

	fresh, err := ch.db.GetIncident(int(mn.ID))
	if err != nil {
		return fmt.Errorf("refetch maintenance %d: %w", mn.ID, err)
	}

	actualStatus := ch.evaluateAndFixMntStatus(fresh)
	if fresh.Status == actualStatus {
		return nil
	}

	oldStatus := fresh.Status
	fresh.Status = actualStatus
	// The modify + enqueue share one transaction: on a version conflict the
	// whole thing rolls back and no notification is published.
	txErr := ch.db.WithTx(context.Background(), func(tx *db.Tx) error {
		if modErr := ch.db.ModifyIncidentTx(tx, fresh); modErr != nil {
			return modErr
		}
		return ch.notifier.PublishTx(context.Background(), tx, notification.Change{
			IncidentID:   fresh.ID,
			Title:        strDeref(fresh.Text),
			OldStatus:    oldStatus,
			NewStatus:    fresh.Status,
			ContactEmail: strDeref(fresh.ContactEmail),
			Actor:        notification.ActorChecker,
		})
	})
	if txErr != nil {
		return fmt.Errorf("update maintenance %d: %w", mn.ID, txErr)
	}
	ch.notifier.Notify() // wake the worker after the commit

	return nil
}

// strDeref returns the pointed-to string, or "" for a nil pointer.
func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (ch *Checker) evaluateAndFixMntStatus(mn *db.Incident) event.Status {
	sHistory := calculateMntStatusHistory(mn)
	actualStatus := calculateCurrentMntStatus(sHistory, mn)
	ch.fixMntMissedStatuses(actualStatus, sHistory, mn)
	return actualStatus
}

func calculateMntStatusHistory(mn *db.Incident) *MntStatusHistory {
	sHistory := &MntStatusHistory{}
	for _, st := range mn.Statuses {
		if st.Status == event.MaintenanceReviewed {
			sHistory.hasReviewed = true
		}
		if st.Status == event.MaintenancePlanned {
			sHistory.hasPlanned = true
		}
		if st.Status == event.MaintenanceInProgress {
			sHistory.hasInProgress = true
		}
		if st.Status == event.MaintenanceCompleted {
			sHistory.hasCompleted = true
		}
		if st.Status == event.MaintenanceCancelled {
			sHistory.hasCancelled = true
		}
	}

	return sHistory
}

func calculateCurrentMntStatus(sHistory *MntStatusHistory, mn *db.Incident) event.Status {
	if sHistory.hasCancelled {
		return event.MaintenanceCancelled
	}

	// If current status is "reviewed", transition to "planned" (checker auto-approval)
	if mn.Status == event.MaintenanceReviewed {
		return event.MaintenancePlanned
	}

	now := time.Now().UTC()

	// calculate the mn current status
	if mn.StartDate.After(now) {
		return event.MaintenancePlanned
	}

	// An open-ended maintenance (end_date empty) is still running, matching how
	// info events are handled. Without the nil check the dereference panics.
	if mn.StartDate.Before(now) && (mn.EndDate == nil || mn.EndDate.After(now)) {
		return event.MaintenanceInProgress
	}

	return event.MaintenanceCompleted
}

func (ch *Checker) fixMntMissedStatuses(status event.Status, sHistory *MntStatusHistory, mnt *db.Incident) {
	ch.log.Info(
		"start to fix missed statuses for the maintenance",
		zap.String("targetStatus", string(status)), zap.Uint("mntID", mnt.ID),
	)

	var statusText string
	var statusTimestamp time.Time

	switch status {
	case event.MaintenancePlanned:
		ch.log.Info("fixing the planned status for the maintenance", zap.Uint("mntID", mnt.ID))
		if sHistory.hasStatus(status) {
			ch.log.Info("the maintenance is already has planned status", zap.Uint("mntID", mnt.ID))
			return
		}
		statusText = event.MaintenancePlannedStatusText()
		statusTimestamp = *mnt.StartDate

	case event.MaintenanceInProgress:
		ch.log.Info("fixing the active status for the maintenance", zap.Uint("mntID", mnt.ID))
		ch.fixMntMissedStatuses(event.MaintenancePlanned, sHistory, mnt)
		if sHistory.hasStatus(status) {
			ch.log.Info("the maintenance is already has active status", zap.Uint("mntID", mnt.ID))
			return
		}
		statusText = event.MaintenanceInProgressStatusText()
		statusTimestamp = *mnt.StartDate

	case event.MaintenanceCompleted:
		ch.log.Info("fixing the completed status for the maintenance", zap.Uint("mntID", mnt.ID))
		ch.fixMntMissedStatuses(event.MaintenanceInProgress, sHistory, mnt)
		if sHistory.hasStatus(status) {
			ch.log.Info("the maintenance is already has completed status", zap.Uint("mntID", mnt.ID))
			return
		}
		statusText = event.MaintenanceCompletedStatusText()
		statusTimestamp = *mnt.EndDate
	case event.MaintenanceCancelled:
		ch.log.Info("fixing the cancelled status for the maintenance", zap.Uint("mntID", mnt.ID))
		// Only backfill planned if the event progressed past pending_review.
		// Cancelling directly from pending_review must not fabricate a planned entry.
		if sHistory.hasReviewed || sHistory.hasPlanned {
			ch.fixMntMissedStatuses(event.MaintenancePlanned, sHistory, mnt)
		}
		ch.log.Info("maintenance cancelled — skipping further status backfill", zap.Uint("mntID", mnt.ID))
		return
	default:
		return
	}

	mnt.Statuses = append(mnt.Statuses, db.IncidentStatus{
		IncidentID: mnt.ID,
		Status:     status,
		Text:       statusText,
		Timestamp:  statusTimestamp,
	})
	sHistory.setStatus(status)
	ch.log.Info("the status was added", zap.String("status", string(status)), zap.Uint("mntID", mnt.ID))
}
