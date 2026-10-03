package tests

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// openRawDB opens a raw database/sql handle on the test database for seeding and
// verification that the facade does not expose.
func openRawDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = sqlDB.Close() })

	return sqlDB
}

const outboxColumns = "id, kind, incident_id, recipient, payload, change_id, dedup_key, " +
	"status, attempts, next_attempt_at, locked_by, locked_at, last_error, created_at, updated_at"

func scanOutboxRows(t *testing.T, rows *sql.Rows) []db.NotificationOutbox {
	t.Helper()
	defer rows.Close()

	var out []db.NotificationOutbox
	for rows.Next() {
		var (
			row         db.NotificationOutbox
			payload     []byte
			nextAttempt sql.NullTime
			lockedBy    sql.NullString
			lockedAt    sql.NullTime
			lastError   sql.NullString
		)
		require.NoError(t, rows.Scan(
			&row.ID, &row.Kind, &row.IncidentID, &row.Recipient, &payload, &row.ChangeID,
			&row.DedupKey, &row.Status, &row.Attempts, &nextAttempt, &lockedBy, &lockedAt,
			&lastError, &row.CreatedAt, &row.UpdatedAt,
		))
		if payload != nil {
			require.NoError(t, json.Unmarshal(payload, &row.Payload))
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
	require.NoError(t, rows.Err())
	return out
}

func queryOutbox(t *testing.T, sqlDB *sql.DB, where string, args ...any) []db.NotificationOutbox {
	t.Helper()

	query := "SELECT " + outboxColumns + " FROM notification_outbox WHERE " + where
	rows, err := sqlDB.Query(query, args...)
	require.NoError(t, err)
	return scanOutboxRows(t, rows)
}

// tableCount counts rows in a named table. table and where are trusted literals
// supplied by the tests.
func tableCount(t *testing.T, sqlDB *sql.DB, table, where string, args ...any) int64 {
	t.Helper()

	var n int64
	require.NoError(t, sqlDB.QueryRow("SELECT count(*) FROM "+table+" WHERE "+where, args...).Scan(&n))
	return n
}

// insertIncidentStatus seeds one incident_status row and returns its id.
func insertIncidentStatus(t *testing.T, sqlDB *sql.DB, incidentID uint, status, text string) uint {
	t.Helper()

	var id uint
	require.NoError(t, sqlDB.QueryRow(
		"INSERT INTO incident_status (incident_id, status, text, timestamp) VALUES ($1, $2, $3, now()) RETURNING id",
		incidentID, status, text).Scan(&id))
	return id
}

func outboxCount(t *testing.T, sqlDB *sql.DB, incidentID int) int64 {
	t.Helper()

	var n int64
	require.NoError(t, sqlDB.
		QueryRow("SELECT count(*) FROM notification_outbox WHERE incident_id = $1", incidentID).
		Scan(&n))
	return n
}

func outboxNotSentCount(t *testing.T, sqlDB *sql.DB, incidentID int) int64 {
	t.Helper()

	var n int64
	require.NoError(t, sqlDB.
		QueryRow("SELECT count(*) FROM notification_outbox WHERE incident_id = $1 AND status <> $2",
			incidentID, db.NotificationStatusSent).
		Scan(&n))
	return n
}

func outboxRecipients(t *testing.T, sqlDB *sql.DB, incidentID int) []string {
	t.Helper()
	return recipientsOf(queryOutbox(t, sqlDB, "incident_id = $1", incidentID))
}

func outboxRecipientsByKind(t *testing.T, sqlDB *sql.DB, incidentID int, kind string) []string {
	t.Helper()
	return recipientsOf(queryOutbox(t, sqlDB, "incident_id = $1 AND kind = $2", incidentID, kind))
}

func recipientsOf(rows []db.NotificationOutbox) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Recipient)
	}
	return out
}

func fetchRow(t *testing.T, sqlDB *sql.DB, id uint) db.NotificationOutbox {
	t.Helper()

	rows := queryOutbox(t, sqlDB, "id = $1", id)
	require.Len(t, rows, 1)
	return rows[0]
}

func fetchByDedup(t *testing.T, sqlDB *sql.DB, dedup string) db.NotificationOutbox {
	t.Helper()

	rows := queryOutbox(t, sqlDB, "dedup_key = $1", dedup)
	require.Len(t, rows, 1)
	return rows[0]
}

// updateOutbox changes columns on the single row matched by whereCol without
// touching updated_at, so tests can craft ages and states.
func updateOutbox(t *testing.T, sqlDB *sql.DB, cols map[string]any, whereCol string, whereVal any) {
	t.Helper()

	keys := make([]string, 0, len(cols))
	for k := range cols {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	set := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)+1)
	for i, k := range keys {
		set = append(set, fmt.Sprintf("%s = $%d", k, i+1))
		args = append(args, cols[k])
	}
	args = append(args, whereVal)

	query := "UPDATE notification_outbox SET " + strings.Join(set, ", ") +
		fmt.Sprintf(" WHERE %s = $%d", whereCol, len(keys)+1)
	_, err := sqlDB.Exec(query, args...)
	require.NoError(t, err)
}

// setOutbox mutates an outbox row by dedup key.
func setOutbox(t *testing.T, sqlDB *sql.DB, dedup string, cols map[string]any) {
	t.Helper()

	if len(cols) == 0 {
		return
	}
	updateOutbox(t, sqlDB, cols, "dedup_key", dedup)
}
