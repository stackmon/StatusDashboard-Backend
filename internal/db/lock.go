package db

import (
	"context"
)

// WithAdvisoryLock runs fn while holding a non-blocking session-level advisory
// lock on key. The lock is acquired and released on the same dedicated
// connection: pg_advisory_lock is session-scoped, so releasing it from a
// different pooled connection would leave the lock held until that connection
// is closed.
//
// The lock is released even when ctx is cancelled, and the connection is
// returned to the pool afterwards. If another session already holds the lock,
// ErrLockBusy is returned without waiting.
func (db *DB) WithAdvisoryLock(ctx context.Context, key int64, fn func(context.Context) error) error {
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	var got bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		return err
	}
	if !got {
		return ErrLockBusy
	}

	defer func() {
		// Unlock must run even after ctx is cancelled, otherwise the lock
		// stays held for the lifetime of the connection.
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", key)
	}()

	return fn(ctx)
}
