package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

const staticTestSpec = `openapi: 3.0.0
info:
  title: status dashboard
  version: "1"
paths: {}
`

// newStaticRouter builds the router the way the process does, so the catch-all
// under test is the one a deployment gets.
func newStaticRouter(t *testing.T, staticCfg conf.Static) *gin.Engine {
	t.Helper()

	idp := newTestIDP(t)

	database, _, err := db.NewWithMock()
	require.NoError(t, err)

	cfg := &conf.Config{
		Port:            "8000",
		LogLevel:        conf.DevelopMode,
		OpenAPISpecPath: writeSpecFile(t, staticTestSpec),
		OIDC:            conf.OIDC{Issuer: idp.server.URL, ClientID: testClientID},
		RBAC:            conf.RBACConfig{Admins: "sd_admins"},
		Static:          staticCfg,
	}

	router, err := New(cfg, zaptest.NewLogger(t), database)
	require.NoError(t, err)

	return router.Router()
}

func TestCatchAllWithoutStaticOrigins(t *testing.T) {
	router := newStaticRouter(t, conf.Static{})

	t.Run("unknown paths keep the plain 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.JSONEq(t, `{"errMsg":"page not found"}`, w.Body.String())
		assert.Empty(t, w.Header().Get("X-Cache"))
	})

	t.Run("registered routes still answer", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "status dashboard")
	})
}

func TestNewRejectsUnusableStaticConfiguration(t *testing.T) {
	idp := newTestIDP(t)

	database, _, err := db.NewWithMock()
	require.NoError(t, err)

	cfg := &conf.Config{
		Port:            "8000",
		LogLevel:        conf.DevelopMode,
		OpenAPISpecPath: writeSpecFile(t, staticTestSpec),
		OIDC:            conf.OIDC{Issuer: idp.server.URL, ClientID: testClientID},
		RBAC:            conf.RBACConfig{Admins: "sd_admins"},
		Static:          conf.Static{Origins: "https://bucket.example.com", CacheTTL: "5m", CacheMaxBytes: "1024"},
	}

	router, err := New(cfg, zaptest.NewLogger(t), database)
	require.Error(t, err)
	assert.Nil(t, router)
	assert.Contains(t, err.Error(), "init static site proxy")
}
