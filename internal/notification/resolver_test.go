package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func testResolver() *Resolver {
	return NewResolver(Config{
		ReviewSMOD:      "support@com.com",
		ReviewOperators: []string{"ops@com.com"},
		ReviewAdmins:    []string{"admin@com.com"},
		BaseURL:         "https://status.example.com",
	})
}

func TestRecipients_ReviewStatusesIncludeAudienceAndCreator(t *testing.T) {
	r := testResolver()

	for _, status := range []event.Status{event.MaintenancePendingReview, event.MaintenanceReviewed} {
		got := r.Recipients(status, "creator@com.com")
		assert.ElementsMatch(t,
			[]string{"support@com.com", "ops@com.com", "admin@com.com", "creator@com.com"},
			got, "status %s", status)
	}
}

func TestRecipients_ExcludedAddressesAreDropped(t *testing.T) {
	r := NewResolver(Config{
		ReviewSMOD:      "support@com.com",
		ReviewOperators: []string{"ops@com.com"},
		ReviewAdmins:    []string{"admin@com.com"},
		ExcludedEmails:  []string{"ops@com.com", "noreply@com.com"},
	})

	got := r.Recipients(event.MaintenancePendingReview, "creator@com.com")
	assert.ElementsMatch(t, []string{"support@com.com", "admin@com.com", "creator@com.com"}, got)

	// The exclusion must hold even when the address arrives as the creator contact.
	got = r.Recipients(event.MaintenancePlanned, "NoReply@COM.com")
	assert.Empty(t, got)
}

func TestRecipients_LifecycleStatusesCreatorOnly(t *testing.T) {
	r := testResolver()

	for _, status := range []event.Status{
		event.MaintenancePlanned, event.MaintenanceInProgress,
		event.MaintenanceCompleted, event.MaintenanceCancelled,
	} {
		got := r.Recipients(status, "creator@com.com")
		assert.Equal(t, []string{"creator@com.com"}, got, "status %s", status)
	}
}

func TestRecipients_NormalizesAndDeduplicates(t *testing.T) {
	r := NewResolver(Config{
		ReviewSMOD:      "Support@Com.com",
		ReviewOperators: []string{" ops@com.com "},
		ReviewAdmins:    []string{"support@com.com"}, // duplicate of SMOD after normalize
	})

	// Creator equals the operator address (different case) -> must appear once.
	got := r.Recipients(event.MaintenancePendingReview, "OPS@com.com")
	assert.Equal(t, []string{"support@com.com", "ops@com.com"}, got)
}

func TestRecipients_EmptyContactEmailForLifecycleYieldsNone(t *testing.T) {
	r := testResolver()
	assert.Empty(t, r.Recipients(event.MaintenancePlanned, ""))
}

func TestDedupKey(t *testing.T) {
	assert.Equal(t, "42:pending_review:>pending_review:creator@com.com",
		DedupKey(42, db.NotificationKindPendingReview, "", event.MaintenancePendingReview, "creator@com.com"))
}

func TestBuildRows_OneRowPerRecipientSharedChangeID(t *testing.T) {
	r := testResolver()
	ch := Change{
		IncidentID:   42,
		Title:        "DB upgrade",
		OldStatus:    "",
		NewStatus:    event.MaintenancePendingReview,
		ContactEmail: "creator@com.com",
		Actor:        "admin-user",
	}

	rows := r.BuildRows(ch)
	require.Len(t, rows, 4)

	changeID := rows[0].ChangeID
	require.NotEmpty(t, changeID)
	seenRecipients := make(map[string]struct{})
	for _, row := range rows {
		assert.Equal(t, changeID, row.ChangeID, "all rows share one change_id")
		assert.Equal(t, db.NotificationKindPendingReview, row.Kind)
		assert.Equal(t, uint(42), row.IncidentID)
		assert.Equal(t, db.NotificationStatusPending, row.Status)
		assert.Equal(t, DedupKey(42, row.Kind, "", event.MaintenancePendingReview, row.Recipient), row.DedupKey)
		assert.Equal(t, "42", row.Payload["incident_id"])
		assert.Equal(t, "DB upgrade", row.Payload["title"])
		assert.Equal(t, "https://status.example.com/incidents/42", row.Payload["link"])
		seenRecipients[row.Recipient] = struct{}{}
	}
	assert.Len(t, seenRecipients, 4, "recipients are unique")
}

func TestRecipients_ContactEmailDomainAllowList(t *testing.T) {
	r := NewResolver(Config{
		ReviewSMOD:     "support@com.com",
		AllowedDomains: []string{"example.com"},
	})

	t.Run("contact email outside the allow-list is dropped", func(t *testing.T) {
		assert.Empty(t, r.Recipients(event.MaintenancePlanned, "creator@gmail.com"))
	})

	t.Run("allowed contact email is kept", func(t *testing.T) {
		assert.Equal(t, []string{"creator@example.com"},
			r.Recipients(event.MaintenancePlanned, "creator@example.com"))
	})

	t.Run("review audience is not subject to the allow-list", func(t *testing.T) {
		assert.Equal(t, []string{"support@com.com"},
			r.Recipients(event.MaintenancePendingReview, "creator@gmail.com"))
	})

	t.Run("empty allow-list permits any domain", func(t *testing.T) {
		open := NewResolver(Config{})
		assert.Equal(t, []string{"creator@anywhere.org"},
			open.Recipients(event.MaintenancePlanned, "creator@anywhere.org"))
	})
}

func TestBuildRows_DedupKeyIsStableAcrossPublishes(t *testing.T) {
	r := testResolver()
	ch := Change{
		IncidentID:   42,
		OldStatus:    event.MaintenancePlanned,
		NewStatus:    event.MaintenanceInProgress,
		ContactEmail: "creator@com.com",
	}

	first := r.BuildRows(ch)
	second := r.BuildRows(ch)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, first[0].DedupKey, second[0].DedupKey,
		"a repeated publish of the same transition must reuse the dedup key")
}

func TestBuildRows_DedupKeyDistinguishesTransitions(t *testing.T) {
	r := testResolver()
	base := Change{IncidentID: 42, ContactEmail: "creator@com.com"}

	created := base
	created.OldStatus, created.NewStatus = "", event.MaintenancePlanned

	repeated := base
	repeated.OldStatus, repeated.NewStatus = event.MaintenanceCompleted, event.MaintenancePlanned

	assert.NotEqual(t, r.BuildRows(created)[0].DedupKey, r.BuildRows(repeated)[0].DedupKey,
		"a later transition into the same status is a different change")
}

func TestBuildRows_NoRecipientsReturnsNil(t *testing.T) {
	r := testResolver()
	rows := r.BuildRows(Change{
		IncidentID: 7,
		NewStatus:  event.MaintenancePlanned, // lifecycle -> creator only
		// no contact email
	})
	assert.Nil(t, rows)
}
