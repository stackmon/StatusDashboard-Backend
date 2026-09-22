package tests

import (
	"net/http"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToken_InvalidSignature verifies that a JWT signed with a wrong secret
// is rejected with 401 on both POST and PATCH endpoints.
func TestToken_InvalidSignature(t *testing.T) {
	r := initTestsWithHMAC(t)
	truncateIncidents(t)

	wrongSecretToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"preferred_username": "user-a",
		"groups":             []interface{}{creatorRole},
	})
	invalidToken, err := wrongSecretToken.SignedString([]byte("wrong-secret"))
	require.NoError(t, err)

	t.Run("POST returns 401", func(t *testing.T) {
		w, _ := createEvent(t, r, maintenanceData(), invalidToken)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("PATCH returns 401", func(t *testing.T) {
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, "pending_review", intPtr(eventVersion(inc)), invalidToken, http.StatusUnauthorized)
	})
}

// TestToken_InvalidGroupsClaim verifies that a JWT with groups as a string
// (instead of array) is rejected with 401.
func TestToken_InvalidGroupsClaim(t *testing.T) {
	r := initTestsWithHMAC(t)
	truncateIncidents(t)

	invalidClaimsToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"preferred_username": "user-a",
		"groups":             "sd_creators", // string instead of []interface{}
	})
	tokenStr, err := invalidClaimsToken.SignedString([]byte(testHMACSecret))
	require.NoError(t, err)

	w, _ := createEvent(t, r, maintenanceData(), tokenStr)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestToken_ValidClaimsSucceeds verifies that a properly signed JWT with
// correct claims structure is accepted.
func TestToken_ValidClaimsSucceeds(t *testing.T) {
	r := initTestsWithHMAC(t)
	truncateIncidents(t)

	w, resp := createEvent(t, r, maintenanceData(), creatorTokenA)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, resp)
}
