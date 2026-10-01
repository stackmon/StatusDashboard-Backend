package db

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
)

// rowExists checks whether a row with the same dedup key already exists.
func (db *DB) rowExists(tx *gorm.DB, dedupKey string) (bool, error) {
	q := db.g
	if tx != nil {
		q = tx
	}

	var row NotificationOutbox
	if err := q.Select("id").Where("dedup_key = ?", dedupKey).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Enqueue inserts one outbox row for a single recipient.
// The row must be written in the same DB transaction as the business change.
func (db *DB) Enqueue(ctx context.Context, tx *gorm.DB, row NotificationOutbox) error {
	if row.DedupKey == "" {
		return errors.New("notification: dedup_key is required")
	}

	return db.execWithTx(ctx, tx, func(gtx *gorm.DB) error {
		exists, err := db.rowExists(gtx, row.DedupKey)
		if err != nil {
			return err
		}
		if exists {
			return ErrNotificationDuplicate
		}
		return gtx.Create(&row).Error
	})
}

// ClaimPending claims a batch of due rows for processing.
// It must use `FOR UPDATE SKIP LOCKED` semantics and mark rows as processing,
// increment attempts, and store lease metadata.
func (db *DB) ClaimPending(
	ctx context.Context, tx *gorm.DB, limit int, leaseOwner string, _ time.Duration,
) ([]NotificationOutbox, error) {
	var rows []NotificationOutbox

	return rows, db.execWithTx(ctx, tx, func(gtx *gorm.DB) error {
		if err := gtx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", NotificationStatusPending).
			Where("next_attempt_at IS NULL OR next_attempt_at <= ?", time.Now().UTC()).
			Order("id ASC").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for i := range rows {
			rows[i].Status = NotificationStatusProcessing
			rows[i].Attempts++
			rows[i].LockedBy = &leaseOwner
			rows[i].LockedAt = &now
			if err := gtx.Model(&rows[i]).Updates(map[string]any{
				"status":    rows[i].Status,
				"attempts":  rows[i].Attempts,
				"locked_by": rows[i].LockedBy,
				"locked_at": rows[i].LockedAt,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkSent marks a row as sent and clears the active lease.
func (db *DB) MarkSent(ctx context.Context, tx *gorm.DB, id uint) error {
	return db.execWithTx(ctx, tx, func(gtx *gorm.DB) error {
		res := gtx.Model(&NotificationOutbox{}).
			Where("id = ?", id).Updates(map[string]any{
			"status":     NotificationStatusSent,
			"locked_by":  nil,
			"locked_at":  nil,
			"last_error": nil,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotificationNotFound
		}
		return nil
	})
}

// getRowByID loads a single notification row by primary key.
func (db *DB) getRowByID(tx *gorm.DB, id uint) (*NotificationOutbox, error) {
	q := db.g
	if tx != nil {
		q = tx
	}

	var row NotificationOutbox
	if err := q.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotificationNotFound
		}
		return nil, err
	}
	return &row, nil
}

// MarkFailed marks a row as failed or pending with retry metadata.
// If retries remain, keep status='pending' and set next_attempt_at.
// Otherwise set status='failed' and last_error.
func (db *DB) MarkFailed(
	ctx context.Context, tx *gorm.DB, id uint, errText string, maxAttempts int, backoff func(attempts int) time.Time,
) error {
	return db.execWithTx(ctx, tx, func(gtx *gorm.DB) error {
		row, err := db.getRowByID(gtx, id)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"last_error": errText,
			"locked_by":  nil,
			"locked_at":  nil,
		}

		if row.Attempts >= maxAttempts {
			updates["status"] = NotificationStatusFailed
			updates["next_attempt_at"] = nil
		} else {
			updates["status"] = NotificationStatusPending
			updates["next_attempt_at"] = backoff(row.Attempts)
		}

		return gtx.Model(&NotificationOutbox{}).Where("id = ?", id).Updates(updates).Error
	})
}

// MarkFailedTerminal fails a row outright, ignoring the remaining attempts. Used for
// rejections the server will repeat on every retry, such as an unknown recipient.
func (db *DB) MarkFailedTerminal(ctx context.Context, tx *gorm.DB, id uint, errText string) error {
	return db.execWithTx(ctx, tx, func(gtx *gorm.DB) error {
		res := gtx.Model(&NotificationOutbox{}).
			Where("id = ?", id).Updates(map[string]any{
			"status":          NotificationStatusFailed,
			"last_error":      errText,
			"next_attempt_at": nil,
			"locked_by":       nil,
			"locked_at":       nil,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotificationNotFound
		}
		return nil
	})
}

// RecoverStaleProcessing returns stale processing rows back to pending,
// or marks them failed if they exhausted all attempts.
func (db *DB) RecoverStaleProcessing(
	ctx context.Context, tx *gorm.DB, leaseTimeout time.Duration, maxAttempts int,
) ([]NotificationOutbox, error) {
	var rows []NotificationOutbox

	return rows, db.execWithTx(ctx, tx, func(gtx *gorm.DB) error {
		cutoff := time.Now().UTC().Add(-leaseTimeout)
		now := time.Now().UTC()
		if err := gtx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", NotificationStatusProcessing).
			Where("locked_at < ?", cutoff).
			Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			if rows[i].Attempts >= maxAttempts {
				rows[i].Status = NotificationStatusFailed
				rows[i].NextAttemptAt = nil
			} else {
				rows[i].Status = NotificationStatusPending
				rows[i].NextAttemptAt = &now
			}
			rows[i].LockedBy = nil
			rows[i].LockedAt = nil

			if err := gtx.Model(&rows[i]).
				Where("status = ?", NotificationStatusProcessing).
				Where("locked_at < ?", cutoff).
				Updates(map[string]any{
					"status":          rows[i].Status,
					"next_attempt_at": rows[i].NextAttemptAt,
					"locked_by":       rows[i].LockedBy,
					"locked_at":       rows[i].LockedAt,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// execWithTx runs the callback in a transaction if tx is nil; otherwise it uses the provided tx.
func (db *DB) execWithTx(ctx context.Context, tx *gorm.DB, fn func(*gorm.DB) error) error {
	if tx != nil {
		return fn(tx)
	}

	return db.g.WithContext(ctx).Transaction(func(gtx *gorm.DB) error {
		return fn(gtx)
	})
}
