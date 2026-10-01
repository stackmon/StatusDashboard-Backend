package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func TestRender_SubjectAndBodyFromPayload(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	row := db.NotificationOutbox{
		Payload: map[string]any{
			"incident_id": "42",
			"title":       "DB upgrade",
			"old_status":  "pending_review",
			"new_status":  "reviewed",
			"actor":       "admin-user",
			"changed_at":  "2026-08-26T10:00:00Z",
			"link":        "https://status.example.com/incidents/42",
		},
	}

	email, err := r.Render(row)
	require.NoError(t, err)

	assert.Equal(t, "[Maintenance] DB upgrade — reviewed", email.Subject)
	assert.Contains(t, email.Body, "event #42")
	assert.Contains(t, email.Body, "pending_review -> reviewed")
	assert.Contains(t, email.Body, "admin-user")
	assert.Contains(t, email.Body, "https://status.example.com/incidents/42")
}

func TestRender_OmitsArrowWhenNoOldStatus(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	row := db.NotificationOutbox{
		Payload: map[string]any{
			"title":      "New maintenance",
			"new_status": "pending_review",
		},
	}

	email, err := r.Render(row)
	require.NoError(t, err)
	assert.Contains(t, email.Body, "Status: pending_review")
	assert.NotContains(t, email.Body, "->", "no arrow without old status")
}

func TestRender_CreationReadsAsScheduledNotAsChange(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	render := func(newStatus string) Email {
		t.Helper()
		email, rerr := r.Render(db.NotificationOutbox{
			Payload: map[string]any{
				"title": "DB upgrade", "new_status": newStatus, "actor": "admin-user",
			},
		})
		require.NoError(t, rerr)
		return email
	}

	t.Run("operator or admin creates a planned window", func(t *testing.T) {
		email := render("planned")
		assert.Equal(t, "[Maintenance] DB upgrade — scheduled", email.Subject)
		assert.Contains(t, email.Body, "has been scheduled")
		assert.Contains(t, email.Body, "Created by: admin-user")
		assert.NotContains(t, email.Body, "changed status")
	})

	t.Run("creator submits for review", func(t *testing.T) {
		email := render("pending_review")
		assert.Equal(t, "[Maintenance] DB upgrade — awaiting review", email.Subject)
		assert.Contains(t, email.Body, "has been submitted for review")
		assert.NotContains(t, email.Body, "changed status")
	})

	t.Run("later transition still reads as a change", func(t *testing.T) {
		email, rerr := r.Render(db.NotificationOutbox{
			Payload: map[string]any{
				"title": "DB upgrade", "old_status": "planned",
				"new_status": "in_progress", "actor": "checker",
			},
		})
		require.NoError(t, rerr)
		assert.Equal(t, "[Maintenance] DB upgrade — in_progress", email.Subject)
		assert.Contains(t, email.Body, "changed status")
		assert.Contains(t, email.Body, "Changed by: checker")
	})
}
