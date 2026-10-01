package db

import (
	"context"
	"time"
)

// NotificationStats is a snapshot of the outbox queue for the ops interface.
type NotificationStats struct {
	Pending                 int64   `json:"pending"`
	Processing              int64   `json:"processing"`
	Sent                    int64   `json:"sent"`
	Failed                  int64   `json:"failed"`
	StaleProcessing         int64   `json:"stale_processing"`
	RetryBacklog            int64   `json:"retry_backlog"`
	OldestPendingAgeSeconds float64 `json:"oldest_pending_age_seconds"`
}

// GetNotificationStats returns queue depth and health counters in one query.
// staleThreshold marks processing rows whose lease is older than it as stuck.
func (db *DB) GetNotificationStats(ctx context.Context, staleThreshold time.Duration) (*NotificationStats, error) {
	cutoff := time.Now().UTC().Add(-staleThreshold)

	var stats NotificationStats
	query := `
SELECT
    COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0)                       AS pending,
    COALESCE(SUM(CASE WHEN status = 'processing' THEN 1 ELSE 0 END), 0)                    AS processing,
    COALESCE(SUM(CASE WHEN status = 'sent' THEN 1 ELSE 0 END), 0)                          AS sent,
    COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)                        AS failed,
    COALESCE(SUM(CASE WHEN status = 'processing' AND locked_at < ? THEN 1 ELSE 0 END), 0)  AS stale_processing,
    COALESCE(SUM(CASE WHEN status = 'pending' AND next_attempt_at > now() THEN 1 ELSE 0 END), 0) AS retry_backlog,
    COALESCE(EXTRACT(EPOCH FROM now() -
        MIN(CASE WHEN status IN ('pending', 'processing') THEN created_at END)), 0)        AS oldest_pending_age_seconds
FROM notification_outbox`

	if err := db.g.WithContext(ctx).Raw(query, cutoff).Scan(&stats).Error; err != nil {
		return nil, err
	}
	return &stats, nil
}

// ListNotificationsByStatus returns the most recently updated rows in the given status.
// Rows stuck in pending with a rising attempt count are the usual symptom of a
// misconfigured relay, so every status must be reachable, not just failed.
func (db *DB) ListNotificationsByStatus(ctx context.Context, status string, limit int) ([]NotificationOutbox, error) {
	var rows []NotificationOutbox
	err := db.g.WithContext(ctx).
		Where("status = ?", status).
		Order("updated_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// EnsureNotificationSchema reports whether the outbox table exists. Migrations are
// applied out of band, so without this check a stale database would let the app start
// and only fail on the first maintenance change.
func (db *DB) EnsureNotificationSchema() error {
	if !db.g.Migrator().HasTable(&NotificationOutbox{}) {
		return ErrNotificationSchemaMissing
	}

	return nil
}

// RedriveFailed resets failed rows back to pending for another delivery cycle,
// clearing attempts, error and lease. With no ids it re-drives every failed row.
func (db *DB) RedriveFailed(ctx context.Context, ids ...uint) (int64, error) {
	q := db.g.WithContext(ctx).Model(&NotificationOutbox{}).Where("status = ?", NotificationStatusFailed)
	if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}

	res := q.Updates(map[string]any{
		"status":          NotificationStatusPending,
		"attempts":        0,
		"next_attempt_at": time.Now().UTC(),
		"last_error":      nil,
		"locked_by":       nil,
		"locked_at":       nil,
	})
	return res.RowsAffected, res.Error
}

// DeleteSentBefore removes delivered rows older than the cutoff in batches
// (retention). Failed rows are kept for audit and re-drive.
func (db *DB) DeleteSentBefore(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}

		res := db.g.WithContext(ctx).Exec(
			`DELETE FROM notification_outbox WHERE id IN (
                SELECT id FROM notification_outbox
                WHERE status = ? AND updated_at < ?
                ORDER BY id LIMIT ?)`,
			NotificationStatusSent, before, batchSize)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < int64(batchSize) {
			return total, nil
		}
	}
}
