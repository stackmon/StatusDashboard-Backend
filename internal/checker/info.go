package checker

import (
	"time"

	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

type InfoStatusHistory struct {
	hasPlanned   bool
	hasActive    bool
	hasCompleted bool
	hasCancelled bool
}

func (st *InfoStatusHistory) hasStatus(status event.Status) bool {
	switch status {
	case event.InfoPlanned:
		return st.hasPlanned
	case event.InfoActive:
		return st.hasActive
	case event.InfoCompleted:
		return st.hasCompleted
	case event.InfoCancelled:
		return st.hasCancelled
	default:
		return false
	}
}

func (st *InfoStatusHistory) setStatus(status event.Status) {
	switch status {
	case event.InfoPlanned:
		st.hasPlanned = true
	case event.InfoActive:
		st.hasActive = true
	case event.InfoCompleted:
		st.hasCompleted = true
	case event.InfoCancelled:
		st.hasCancelled = true
	default:
	}
}

func (ch *Checker) CheckInfoEvents() error {
	ch.log.Info("check info event statuses")

	infos, err := ch.db.GetInfoEvents()
	if err != nil {
		return err
	}

	for _, info := range infos {
		sHistory := calculateInfoStatusHistory(info)
		actualStatus := calculateCurrentInfoStatus(sHistory, info)

		switch actualStatus {
		case event.InfoPlanned:
			ch.fixInfoMissedStatuses(event.InfoPlanned, sHistory, info)
		case event.InfoActive:
			ch.fixInfoMissedStatuses(event.InfoActive, sHistory, info)
		case event.InfoCompleted:
			ch.fixInfoMissedStatuses(event.InfoCompleted, sHistory, info)
		case event.InfoCancelled:
			ch.fixInfoMissedStatuses(event.InfoCancelled, sHistory, info)
		default:
		}

		// Only update the incident if the status has actually changed
		if info.Status != actualStatus {
			info.Status = actualStatus
			err = ch.db.ModifyIncident(info)
			if err != nil {
				return err
			}
		}
	}

	ch.log.Info("finished checking info events")

	return nil
}

func calculateInfoStatusHistory(mn *db.Incident) *InfoStatusHistory {
	sHistory := &InfoStatusHistory{}
	for _, st := range mn.Statuses {
		if st.Status == event.InfoPlanned {
			sHistory.hasPlanned = true
		}
		if st.Status == event.InfoActive {
			sHistory.hasActive = true
		}
		if st.Status == event.InfoCompleted {
			sHistory.hasCompleted = true
		}
		if st.Status == event.InfoCancelled {
			sHistory.hasCancelled = true
		}
	}

	return sHistory
}

func calculateCurrentInfoStatus(sHistory *InfoStatusHistory, info *db.Incident) event.Status {
	if sHistory.hasCancelled {
		return event.InfoCancelled
	}

	now := time.Now().UTC()

	// calculate the info current status
	if info.StartDate.After(now) {
		return event.InfoPlanned
	}

	if info.StartDate.Before(now) && (info.EndDate == nil || info.EndDate.After(now)) {
		return event.InfoActive
	}

	return event.InfoCompleted
}

func (ch *Checker) fixInfoMissedStatuses(status event.Status, sHistory *InfoStatusHistory, info *db.Incident) {
	ch.log.Info(
		"start to fix missed statuses for the info event",
		zap.String("targetStatus", string(status)), zap.Uint("infoID", info.ID),
	)

	var statusText string
	var statusTimestamp time.Time

	switch status {
	case event.InfoPlanned:
		ch.log.Info("fixing the planned status for the info event", zap.Uint("infoID", info.ID))
		if sHistory.hasStatus(status) {
			ch.log.Info("the info event is already has planned status", zap.Uint("infoID", info.ID))
			return
		}
		statusText = event.InfoPlannedStatusText()
		statusTimestamp = *info.StartDate

	case event.InfoActive:
		ch.log.Info("fixing the active status for the info event", zap.Uint("infoID", info.ID))
		ch.fixInfoMissedStatuses(event.InfoPlanned, sHistory, info)
		if sHistory.hasStatus(status) {
			ch.log.Info("the info event is already has active status", zap.Uint("infoID", info.ID))
			return
		}
		statusText = event.InfoActiveStatusText()
		statusTimestamp = *info.StartDate

	case event.InfoCompleted:
		ch.log.Info("fixing the completed status for the info event", zap.Uint("infoID", info.ID))
		ch.fixInfoMissedStatuses(event.InfoActive, sHistory, info)
		if sHistory.hasStatus(status) {
			ch.log.Info("the info event is already has completed status", zap.Uint("infoID", info.ID))
			return
		}
		statusText = event.InfoCompletedStatusText()
		statusTimestamp = *info.EndDate
	case event.InfoCancelled:
		ch.log.Info("fixing the cancelled status for the info event", zap.Uint("infoID", info.ID))
		ch.fixInfoMissedStatuses(event.InfoPlanned, sHistory, info)
		ch.log.Info("the info event is already has cancelled status", zap.Uint("infoID", info.ID))
		return
	default:
		return
	}

	info.Statuses = append(info.Statuses, db.IncidentStatus{
		IncidentID: info.ID,
		Status:     status,
		Text:       statusText,
		Timestamp:  statusTimestamp,
	})
	sHistory.setStatus(status)
	ch.log.Info("the status was added", zap.String("status", string(status)), zap.Uint("infoID", info.ID))
}
