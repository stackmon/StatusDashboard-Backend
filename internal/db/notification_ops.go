package db

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent/notificationoutbox"
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
    COALESCE(SUM(CASE WHEN status = 'processing' AND locked_at < $1 THEN 1 ELSE 0 END), 0)  AS stale_processing,
    COALESCE(SUM(CASE WHEN status = 'pending' AND next_attempt_at > now() THEN 1 ELSE 0 END), 0) AS retry_backlog,
    COALESCE(EXTRACT(EPOCH FROM now() -
        MIN(CASE WHEN status IN ('pending', 'processing') THEN created_at END)), 0)        AS oldest_pending_age_seconds
FROM notification_outbox`

	if err := db.sql.QueryRowContext(ctx, query, cutoff).Scan(
		&stats.Pending,
		&stats.Processing,
		&stats.Sent,
		&stats.Failed,
		&stats.StaleProcessing,
		&stats.RetryBacklog,
		&stats.OldestPendingAgeSeconds,
	); err != nil {
		return nil, err
	}
	return &stats, nil
}

// ListNotificationsByStatus returns the most recently updated rows in the given status.
// Rows stuck in pending with a rising attempt count are the usual symptom of a
// misconfigured relay, so every status must be reachable, not just failed.
func (db *DB) ListNotificationsByStatus(ctx context.Context, status string, limit int) ([]NotificationOutbox, error) {
	// Ent drops Limit(0) while SQL LIMIT 0 selects nothing; keep the SQL semantics
	// so a zero limit returns no rows and a negative one means "no limit".
	if limit == 0 {
		return []NotificationOutbox{}, nil
	}

	query := db.e.NotificationOutbox.Query().
		Where(notificationoutbox.StatusEQ(status)).
		Order(notificationoutbox.ByUpdatedAt(entsql.OrderDesc()))
	if limit > 0 {
		query = query.Limit(limit)
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]NotificationOutbox, 0, len(rows))
	for _, row := range rows {
		out = append(out, notificationOutboxFromEnt(row))
	}
	return out, nil
}

// EnsureNotificationSchema reports whether the outbox table exists. Migrations are
// applied out of band, so without this check a stale database would let the app start
// and only fail on the first maintenance change.
func (db *DB) EnsureNotificationSchema() error {
	var count int
	if err := db.sql.QueryRowContext(
		context.Background(),
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = CURRENT_SCHEMA() AND table_name = 'notification_outbox'
		   AND table_type = 'BASE TABLE'`,
	).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrNotificationSchemaMissing
	}

	return nil
}

// RedriveFailed resets failed rows back to pending for another delivery cycle,
// clearing attempts, error and lease. With no ids it re-drives every failed row.
func (db *DB) RedriveFailed(ctx context.Context, ids ...uint) (int64, error) {
	update := db.e.NotificationOutbox.Update().
		Where(notificationoutbox.StatusEQ(NotificationStatusFailed))
	if len(ids) > 0 {
		intIDs := make([]int, 0, len(ids))
		for _, id := range ids {
			intIDs = append(intIDs, int(id))
		}
		update.Where(notificationoutbox.IDIn(intIDs...))
	}

	now := time.Now().UTC()
	affected, err := update.
		SetStatus(NotificationStatusPending).
		SetAttempts(0).
		SetNextAttemptAt(now).
		ClearLastError().
		ClearLockedBy().
		ClearLockedAt().
		SetUpdatedAt(now).
		Save(ctx)
	return int64(affected), err
}

// DeleteSentBefore removes delivered rows older than the cutoff in batches
// (retention). Failed rows are kept for audit and re-drive.
func (db *DB) DeleteSentBefore(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	const deleteBatch = `DELETE FROM notification_outbox WHERE id IN (
        SELECT id FROM notification_outbox
        WHERE status = $1 AND updated_at < $2
        ORDER BY id LIMIT $3)`

	var total int64
	for {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}

		res, err := db.sql.ExecContext(ctx, deleteBatch, NotificationStatusSent, before, batchSize)
		if err != nil {
			return total, err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += removed
		if removed < int64(batchSize) {
			return total, nil
		}
	}
}
