package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func newComponentsRouter(t *testing.T) (*gin.Engine, *testIDP) {
	t.Helper()

	idp := newTestIDP(t)

	database, err := db.New(&conf.Config{DB: unreachableDSN})
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	cfg := &conf.Config{
		Port:            "8000",
		LogLevel:        conf.DevelopMode,
		OpenAPISpecPath: writeSpecFile(t, staticTestSpec),
		OIDC:            conf.OIDC{Issuer: idp.server.URL, ClientID: testClientID},
		RBAC: conf.RBACConfig{
			Admins:    "sd_admins",
			Creators:  "sd_creators",
			Reporters: "sd_reporters",
		},
	}

	router, err := New(cfg, zaptest.NewLogger(t), database)
	require.NoError(t, err)

	return router.Router(), idp
}

func roleToken(t *testing.T, idp *testIDP, roleNames ...string) string {
	t.Helper()

	roles := make(map[string]any, len(roleNames))
	for _, name := range roleNames {
		roles[name] = map[string]any{testOrgID: "zitadel.example.com"}
	}

	return "Bearer " + idp.token(t, func(claims map[string]any) {
		claims[testRolesClaim] = roles
	})
}

func TestPostComponentsRequiresCreatorRole(t *testing.T) {
	router, idp := newComponentsRouter(t)

	tests := []struct {
		name     string
		roles    []string
		expected int
	}{
		{name: "no application role", expected: http.StatusForbidden},
		{name: "reporter is read-only for the catalogue", roles: []string{"sd_reporters"}, expected: http.StatusForbidden},
		// The handler rejects the empty body, which proves the request passed the
		// middleware chain without reaching the database.
		{name: "creator", roles: []string{"sd_creators"}, expected: http.StatusBadRequest},
		{name: "admin", roles: []string{"sd_admins"}, expected: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v2/components", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", roleToken(t, idp, tc.roles...))

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expected, w.Code)
		})
	}
}
