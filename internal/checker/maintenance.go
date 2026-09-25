package checker

import (
	"fmt"
	"slices"
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
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
	if ch.lastMntID == 0 {
		ch.log.Info("no last completed maintenance, starting from the beginning")
	}

	maintenances, err := ch.db.GetMaintenances(ch.lastMntID)
	if err != nil {
		return err
	}

	var activeMaintenances []uint
	for _, mn := range maintenances {
		// Draft maintenances are not processed by the checker — they await
		// manual approval (reviewed) or rejection (cancelled) via the API.
		if mn.Status == event.MaintenancePendingReview {
			continue
		}

		if processErr := ch.processMaintenance(mn, &activeMaintenances); processErr != nil {
			ch.log.Error("failed to process maintenance",
				zap.Uint("mntID", mn.ID), zap.Error(processErr))
			continue
		}
	}

	if len(activeMaintenances) == 0 {
		for _, mn := range maintenances {
			if mn.ID > ch.lastMntID {
				ch.lastMntID = mn.ID
			}
		}
		ch.log.Debug(
			"there are no actual maintenances, set the last ID to the last one",
			zap.Uint("lastMntID", ch.lastMntID),
		)
	} else {
		ch.lastMntID = slices.Min(activeMaintenances)
		ch.log.Debug(
			"set the last ID to the earliest planned or in_progress maintenance",
			zap.Uint("lastMntID", ch.lastMntID),
		)
	}

	ch.log.Info("finished checking maintenances")

	return nil
}

func (ch *Checker) processMaintenance(mn *db.Incident, activeMaintenances *[]uint) error {
	// Refetch immediately before the read-modify-write. The bulk
	// GetMaintenances above is N items old by the time we reach item N;
	// using its preloaded state for the version check races concurrent
	// API edits. A single fresh read shrinks the race window from
	// "duration of the whole tick" to "one DB round-trip", which makes
	// ErrVersionConflict effectively unreachable without a retry loop.
	mn, err := ch.db.GetIncident(int(mn.ID))
	if err != nil {
		return fmt.Errorf("refetch maintenance %d: %w", mn.ID, err)
	}

	actualStatus := ch.evaluateAndFixMntStatus(mn)

	if mn.Status != actualStatus {
		mn.Status = actualStatus
		if modErr := ch.db.ModifyIncident(mn); modErr != nil {
			return fmt.Errorf("update maintenance %d: %w", mn.ID, modErr)
		}
	}

	trackActiveMaintenance(actualStatus, mn.ID, activeMaintenances)
	return nil
}

func (ch *Checker) evaluateAndFixMntStatus(mn *db.Incident) event.Status {
	sHistory := calculateMntStatusHistory(mn)
	actualStatus := calculateCurrentMntStatus(sHistory, mn)
	ch.fixMntMissedStatuses(actualStatus, sHistory, mn)
	return actualStatus
}

func trackActiveMaintenance(status event.Status, id uint, activeMaintenances *[]uint) {
	if status == event.MaintenancePlanned || status == event.MaintenanceInProgress {
		*activeMaintenances = append(*activeMaintenances, id)
	}
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
