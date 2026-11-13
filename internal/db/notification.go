package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent"
	"github.com/stackmon/otc-status-dashboard/ent/notificationoutbox"
	"github.com/stackmon/otc-status-dashboard/ent/predicate"
)

const (
	NotificationStatusPending    = "pending"
	NotificationStatusProcessing = "processing"
	NotificationStatusSent       = "sent"
	NotificationStatusFailed     = "failed"

	NotificationKindPendingReview = "pending_review"
	NotificationKindReviewed      = "reviewed"
	NotificationKindStatusChanged = "status_changed"
)

var (
	ErrNotificationDuplicate = errors.New("notification: duplicate outbox row")
	ErrNotificationNotFound  = errors.New("notification: not found")
	// ErrNotificationLeaseLost reports a finalize that matched no row because the
	// lease has moved on. The caller must not retry: the row is not its own anymore.
	ErrNotificationLeaseLost = errors.New("notification: lease lost")
)

// outboxColumns lists every outbox column in the order selectOutboxRows expects.
const outboxColumns = `id, kind, incident_id, recipient, payload, change_id, dedup_key, status, ` +
	`attempts, next_attempt_at, locked_by, locked_at, last_error, created_at, updated_at`

// rowExists checks whether a row with the same dedup key already exists.
func (db *DB) rowExists(ctx context.Context, c *ent.Client, dedupKey string) (bool, error) {
	return c.NotificationOutbox.Query().
		Where(notificationoutbox.DedupKeyEQ(dedupKey)).
		Exist(ctx)
}

// Enqueue inserts one outbox row for a single recipient.
// The row must be written in the same DB transaction as the business change.
func (db *DB) Enqueue(ctx context.Context, tx *Tx, row NotificationOutbox) error {
	if row.DedupKey == "" {
		return errors.New("notification: dedup_key is required")
	}

	return db.execWithTx(ctx, tx, func(c *ent.Client, _ entsql.ExecQuerier) error {
		exists, err := db.rowExists(ctx, c, row.DedupKey)
		if err != nil {
			return err
		}
		if exists {
			return ErrNotificationDuplicate
		}

		if _, err = newOutboxCreate(c, row).Save(ctx); err != nil {
			if ent.IsConstraintError(err) {
				return ErrNotificationDuplicate
			}
			return err
		}
		return nil
	})
}

func newOutboxCreate(c *ent.Client, row NotificationOutbox) *ent.NotificationOutboxCreate {
	create := c.NotificationOutbox.Create().
		SetKind(row.Kind).
		SetIncidentID(int(row.IncidentID)).
		SetRecipient(row.Recipient).
		SetPayload(row.Payload).
		SetChangeID(row.ChangeID).
		SetDedupKey(row.DedupKey)
	if row.Status != "" {
		create.SetStatus(row.Status)
	}
	if row.Attempts != 0 {
		create.SetAttempts(row.Attempts)
	}
	if row.NextAttemptAt != nil {
		create.SetNextAttemptAt(*row.NextAttemptAt)
	}
	if row.LockedBy != nil {
		create.SetLockedBy(*row.LockedBy)
	}
	if row.LockedAt != nil {
		create.SetLockedAt(*row.LockedAt)
	}
	if row.LastError != nil {
		create.SetLastError(*row.LastError)
	}
	if !row.CreatedAt.IsZero() {
		create.SetCreatedAt(row.CreatedAt)
	}
	if !row.UpdatedAt.IsZero() {
		create.SetUpdatedAt(row.UpdatedAt)
	}
	return create
}

// ClaimPending claims a batch of due rows for processing.
// It must use `FOR UPDATE SKIP LOCKED` semantics and mark rows as processing,
// increment attempts, and store lease metadata.
func (db *DB) ClaimPending(
	ctx context.Context, tx *Tx, limit int, leaseOwner string, _ time.Duration,
) ([]NotificationOutbox, error) {
	var rows []NotificationOutbox

	err := db.execWithTx(ctx, tx, func(_ *ent.Client, raw entsql.ExecQuerier) error {
		claimed, err := selectOutboxRows(ctx, raw,
			`SELECT `+outboxColumns+` FROM notification_outbox
			 WHERE status = $1 AND (next_attempt_at IS NULL OR next_attempt_at <= $2)
			 ORDER BY id ASC LIMIT $3 FOR UPDATE SKIP LOCKED`,
			NotificationStatusPending, time.Now().UTC(), limit)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		for i := range claimed {
			claimed[i].Status = NotificationStatusProcessing
			claimed[i].Attempts++
			claimed[i].LockedBy = &leaseOwner
			claimed[i].LockedAt = &now

			if _, err = raw.ExecContext(ctx,
				`UPDATE notification_outbox
				 SET status = $1, attempts = $2, locked_by = $3, locked_at = $4, updated_at = $5
				 WHERE id = $6`,
				claimed[i].Status, claimed[i].Attempts, claimed[i].LockedBy, claimed[i].LockedAt,
				now, claimed[i].ID); err != nil {
				return err
			}
		}

		rows = claimed
		return nil
	})
	return rows, err
}

// leasedWhere restricts an update to a row that is still processing under the given
// claim. Attempts doubles as a monotonic lease epoch: ClaimPending increments it on
// every claim, so a recovered row re-claimed by any worker, including this one, no
// longer matches the old lease.
func leasedWhere(lease Lease) predicate.NotificationOutbox {
	return notificationoutbox.And(
		notificationoutbox.IDEQ(int(lease.ID)),
		notificationoutbox.StatusEQ(NotificationStatusProcessing),
		notificationoutbox.LockedByEQ(lease.Owner),
		notificationoutbox.AttemptsEQ(lease.Attempts),
	)
}

// finalizeResult maps an update that matched no row to the reason it missed.
func finalizeResult(ctx context.Context, c *ent.Client, id uint, affected int) error {
	if affected > 0 {
		return nil
	}

	exists, err := c.NotificationOutbox.Query().
		Where(notificationoutbox.IDEQ(int(id))).
		Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return ErrNotificationLeaseLost
	}

	return ErrNotificationNotFound
}

// MarkSent marks the leased row as sent and clears the lease.
func (db *DB) MarkSent(ctx context.Context, tx *Tx, lease Lease) error {
	return db.execWithTx(ctx, tx, func(c *ent.Client, _ entsql.ExecQuerier) error {
		affected, err := c.NotificationOutbox.Update().
			Where(leasedWhere(lease)).
			SetStatus(NotificationStatusSent).
			ClearLockedBy().
			ClearLockedAt().
			ClearLastError().
			SetUpdatedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			return err
		}

		return finalizeResult(ctx, c, lease.ID, affected)
	})
}

// MarkFailed marks a row as failed or pending with retry metadata.
// If retries remain, keep status='pending' and set next_attempt_at.
// Otherwise set status='failed' and last_error.
func (db *DB) MarkFailed(
	ctx context.Context, tx *Tx, lease Lease, errText string, maxAttempts int,
	backoff func(attempts int) time.Time,
) error {
	return db.execWithTx(ctx, tx, func(c *ent.Client, _ entsql.ExecQuerier) error {
		update := c.NotificationOutbox.Update().
			Where(leasedWhere(lease)).
			SetLastError(errText).
			ClearLockedBy().
			ClearLockedAt().
			SetUpdatedAt(time.Now().UTC())

		if lease.Attempts >= maxAttempts {
			update.SetStatus(NotificationStatusFailed).ClearNextAttemptAt()
		} else {
			update.SetStatus(NotificationStatusPending).SetNextAttemptAt(backoff(lease.Attempts))
		}

		affected, err := update.Save(ctx)
		if err != nil {
			return err
		}

		return finalizeResult(ctx, c, lease.ID, affected)
	})
}

// MarkFailedTerminal fails a row outright, ignoring the remaining attempts. Used for
// rejections the server will repeat on every retry, such as an unknown recipient.
func (db *DB) MarkFailedTerminal(ctx context.Context, tx *Tx, lease Lease, errText string) error {
	return db.execWithTx(ctx, tx, func(c *ent.Client, _ entsql.ExecQuerier) error {
		affected, err := c.NotificationOutbox.Update().
			Where(leasedWhere(lease)).
			SetStatus(NotificationStatusFailed).
			SetLastError(errText).
			ClearNextAttemptAt().
			ClearLockedBy().
			ClearLockedAt().
			SetUpdatedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			return err
		}

		return finalizeResult(ctx, c, lease.ID, affected)
	})
}

// RecoverStaleProcessing returns stale processing rows back to pending,
// or marks them failed if they exhausted all attempts.
func (db *DB) RecoverStaleProcessing(
	ctx context.Context, tx *Tx, leaseTimeout time.Duration, maxAttempts int,
) ([]NotificationOutbox, error) {
	var rows []NotificationOutbox

	err := db.execWithTx(ctx, tx, func(_ *ent.Client, raw entsql.ExecQuerier) error {
		cutoff := time.Now().UTC().Add(-leaseTimeout)
		now := time.Now().UTC()

		stale, err := selectOutboxRows(ctx, raw,
			`SELECT `+outboxColumns+` FROM notification_outbox
			 WHERE status = $1 AND locked_at < $2
			 FOR UPDATE SKIP LOCKED`,
			NotificationStatusProcessing, cutoff)
		if err != nil {
			return err
		}

		for i := range stale {
			if stale[i].Attempts >= maxAttempts {
				stale[i].Status = NotificationStatusFailed
				stale[i].NextAttemptAt = nil
			} else {
				stale[i].Status = NotificationStatusPending
				stale[i].NextAttemptAt = &now
			}
			stale[i].LockedBy = nil
			stale[i].LockedAt = nil

			if _, err = raw.ExecContext(ctx,
				`UPDATE notification_outbox
				 SET status = $1, next_attempt_at = $2, locked_by = $3, locked_at = $4, updated_at = $5
				 WHERE id = $6 AND status = $7 AND locked_at < $8`,
				stale[i].Status, stale[i].NextAttemptAt, stale[i].LockedBy, stale[i].LockedAt,
				now, stale[i].ID, NotificationStatusProcessing, cutoff); err != nil {
				return err
			}
		}

		rows = stale
		return nil
	})
	return rows, err
}

// selectOutboxRows runs a hand-written statement and scans full outbox rows. The
// claim and recovery paths need row locks, which the Ent query builder cannot
// express, so the statements stay raw.
func selectOutboxRows(
	ctx context.Context, q entsql.ExecQuerier, query string, args ...any,
) ([]NotificationOutbox, error) {
	sqlRows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()

	var out []NotificationOutbox
	for sqlRows.Next() {
		var (
			row         NotificationOutbox
			payload     []byte
			nextAttempt sql.NullTime
			lockedBy    sql.NullString
			lockedAt    sql.NullTime
			lastError   sql.NullString
		)
		if err = sqlRows.Scan(
			&row.ID, &row.Kind, &row.IncidentID, &row.Recipient, &payload, &row.ChangeID,
			&row.DedupKey, &row.Status, &row.Attempts, &nextAttempt, &lockedBy, &lockedAt,
			&lastError, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}

		if payload != nil {
			if err = json.Unmarshal(payload, &row.Payload); err != nil {
				return nil, err
			}
		}
		if nextAttempt.Valid {
			row.NextAttemptAt = &nextAttempt.Time
		}
		if lockedBy.Valid {
			row.LockedBy = &lockedBy.String
		}
		if lockedAt.Valid {
			row.LockedAt = &lockedAt.Time
		}
		if lastError.Valid {
			row.LastError = &lastError.String
		}

		out = append(out, row)
	}

	return out, sqlRows.Err()
}
