package tests

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToken_ForeignSignature verifies that a token signed by a key the identity
// provider does not publish is rejected with 401 on POST and PATCH.
func TestToken_ForeignSignature(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	foreignKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	foreignToken := signToken(foreignKey, tokenClaims("user-a", creatorRole))

	t.Run("POST returns 401", func(t *testing.T) {
		w, _ := createEvent(t, r, maintenanceData(), foreignToken)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("PATCH returns 401", func(t *testing.T) {
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, "pending_review", intPtr(eventVersion(inc)), foreignToken, http.StatusUnauthorized)
	})
}

// TestToken_MalformedRolesClaim verifies that a token whose roles claim has an
// unexpected shape carries no roles, so write access is refused with 403.
func TestToken_MalformedRolesClaim(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	claims := tokenClaims("user-a")
	claims[testRolesClaim] = 42

	w, _ := createEvent(t, r, maintenanceData(), signToken(testIDP.key, claims))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestToken_ValidClaimsSucceeds verifies that a properly signed token with the
// expected claims is accepted.
func TestToken_ValidClaimsSucceeds(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	w, resp := createEvent(t, r, maintenanceData(), creatorTokenA)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, resp)
}
