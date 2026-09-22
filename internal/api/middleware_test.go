package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/stackmon/otc-status-dashboard/internal/api/auth"
	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
)

const (
	testClientID      = "status-dashboard"
	testKeyID         = "test-key"
	testUsernameClaim = "preferred_username"
	testRolesClaim    = "urn:zitadel:iam:org:project:roles"
	testOrgID         = "123456789012345678"
)

// testIDP is a local OpenID Connect server publishing one RSA key.
type testIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newTestIDP(t *testing.T) *testIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	idpServer := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: key.Public(),
		KeyID:     testKeyID,
		Algorithm: oidc.RS256,
	}}}
	server := httptest.NewServer(idpServer)
	t.Cleanup(server.Close)
	idpServer.SetIssuer(server.URL)

	return &testIDP{server: server, key: key}
}

// token signs an access token whose claims are the defaults, mutated by the
// caller when a variant is needed.
func (idp *testIDP) token(t *testing.T, mutate func(claims map[string]any)) string {
	t.Helper()

	claims := map[string]any{
		"iss": idp.server.URL,
		"aud": testClientID,
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}

	rawClaims, err := json.Marshal(claims)
	require.NoError(t, err)

	return oidctest.SignIDToken(idp.key, testKeyID, oidc.RS256, string(rawClaims))
}

// newIDPProvider builds a provider that verifies tokens from the local test identity provider.
func newIDPProvider(t *testing.T, idp *testIDP, roleNames ...string) *auth.Provider {
	t.Helper()

	provider, err := auth.NewProvider(context.Background(), auth.ProviderConfig{
		Issuer:        idp.server.URL,
		ClientID:      testClientID,
		RolesClaim:    testRolesClaim,
		RoleNames:     roleNames,
		UsernameClaim: testUsernameClaim,
	})
	require.NoError(t, err)

	return provider
}

// rolePtr lets the RBAC tables distinguish "no role expected" (nil) from the
// zero role.
func rolePtr(role rbac.Role) *rbac.Role {
	return &role
}

func performRequestWithAuth(mw gin.HandlerFunc, authHeader string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Use(mw)
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	return w
}

func TestAuthenticationMW_Zitadel(t *testing.T) {
	logger := zaptest.NewLogger(t)
	idp := newTestIDP(t)
	mw := AuthenticationMW(newIDPProvider(t, idp, "sd_creators", "sd_operators", "sd_admins"), logger)

	t.Run("valid token stores subject and roles", func(t *testing.T) {
		token := idp.token(t, func(claims map[string]any) {
			claims[testUsernameClaim] = "alice"
			claims[testRolesClaim] = map[string]any{"sd_admins": map[string]any{testOrgID: "otc"}}
		})

		var gotUserID any
		var gotRoles any

		router := gin.New()
		router.Use(mw)
		router.GET("/test", func(c *gin.Context) {
			gotUserID, _ = c.Get(v2.UserIDContextKey)
			gotRoles, _ = c.Get(v2.UserIDRolesContextKey)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "user-1", gotUserID, "the subject is the identity")
		assert.Equal(t, []string{"sd_admins"}, gotRoles)
	})

	t.Run("unknown role names yield no roles", func(t *testing.T) {
		token := idp.token(t, func(claims map[string]any) {
			claims[testRolesClaim] = map[string]any{"sd_readers": map[string]any{testOrgID: "otc"}}
		})

		var gotRoles any

		router := gin.New()
		router.Use(mw)
		router.GET("/test", func(c *gin.Context) {
			gotRoles, _ = c.Get(v2.UserIDRolesContextKey)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, []string{}, gotRoles)
	})

	t.Run("token from another issuer returns 401", func(t *testing.T) {
		foreign := newTestIDP(t)
		w := performRequestWithAuth(mw, "Bearer "+foreign.token(t, nil))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("token for another audience returns 401", func(t *testing.T) {
		token := idp.token(t, func(claims map[string]any) { claims["aud"] = "another-client" })
		w := performRequestWithAuth(mw, "Bearer "+token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("expired token returns 401", func(t *testing.T) {
		token := idp.token(t, func(claims map[string]any) {
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		})
		w := performRequestWithAuth(mw, "Bearer "+token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("token signed by an unknown key returns 401", func(t *testing.T) {
		other := newTestIDP(t)
		rawClaims, err := json.Marshal(map[string]any{
			"iss": idp.server.URL,
			"aud": testClientID,
			"sub": "user-1",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		require.NoError(t, err)

		token := oidctest.SignIDToken(other.key, testKeyID, oidc.RS256, string(rawClaims))

		w := performRequestWithAuth(mw, "Bearer "+token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("token without a subject returns 401", func(t *testing.T) {
		token := idp.token(t, func(claims map[string]any) { delete(claims, "sub") })
		w := performRequestWithAuth(mw, "Bearer "+token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("missing authorization header returns 401", func(t *testing.T) {
		w := performRequestWithAuth(mw, "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("garbage token returns 401", func(t *testing.T) {
		w := performRequestWithAuth(mw, "Bearer not-a-token")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("unsigned token returns 401", func(t *testing.T) {
		unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
			"iss": idp.server.URL,
			"aud": testClientID,
			"sub": "user-1",
			"exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		w := performRequestWithAuth(mw, "Bearer "+unsigned)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestRBACAuthorizationMW(t *testing.T) {
	logger := zaptest.NewLogger(t)
	rbacService := rbac.New(rbac.Config{Creators: "sd_creators", Operators: "sd_operators", Admins: "sd_admins", Reporters: "sd_reporters"})

	tests := []struct {
		name           string
		roles          []string
		setRoles       bool
		expectedStatus int
		expectedRole   *rbac.Role
	}{
		{
			name:           "Creator role is allowed",
			roles:          []string{"sd_creators"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Creator),
		},
		{
			name:           "Operator role is allowed",
			roles:          []string{"sd_operators"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Operator),
		},
		{
			name:           "Admin role is allowed",
			roles:          []string{"sd_admins"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Admin),
		},
		{
			name:           "Reporter role is allowed",
			roles:          []string{"sd_reporters"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Reporter),
		},
		{
			name:           "Role with leading slash is normalized",
			roles:          []string{"/sd_creators"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Creator),
		},
		{
			name:           "Missing roles in context return 401",
			setRoles:       false,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Empty roles return 403",
			roles:          []string{},
			setRoles:       true,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Unrecognized roles return 403",
			roles:          []string{"random_role", "other_role"},
			setRoles:       true,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var role any

			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(v2.UserIDContextKey, "user-1")
				if tt.setRoles {
					c.Set(v2.UserIDRolesContextKey, tt.roles)
				}
				c.Next()
			})
			router.Use(RBACAuthorizationMW(rbacService, logger))
			router.GET("/test", func(c *gin.Context) {
				role, _ = c.Get(v2.RoleContextKey)
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedRole != nil {
				assert.Equal(t, *tt.expectedRole, role)
			} else {
				assert.Nil(t, role, "denied requests must not receive a role")
			}
		})
	}

	t.Run("Roles of an unexpected type return 401", func(t *testing.T) {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(v2.UserIDRolesContextKey, "sd_admins")
			c.Next()
		})
		router.Use(RBACAuthorizationMW(rbacService, logger))
		router.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestDenyReporterScopeMW(t *testing.T) {
	logger := zaptest.NewLogger(t)
	rbacService := rbac.New(rbac.Config{
		Creators:  "sd_creators",
		Operators: "sd_operators",
		Admins:    "sd_admins",
		Reporters: "sd_reporters",
	})

	tests := []struct {
		name           string
		roles          []string
		setRoles       bool
		expectedStatus int
	}{
		{
			name:           "Reporter role is denied",
			roles:          []string{"sd_reporters"},
			setRoles:       true,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Creator role passes through",
			roles:          []string{"sd_creators"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Admin role passes through",
			roles:          []string{"sd_admins"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Unmapped route roles keep their previous access",
			roles:          []string{"sd_readers"},
			setRoles:       true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Anonymous request passes through",
			setRoles:       false,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tt.setRoles {
					c.Set(v2.UserIDContextKey, "user-1")
					c.Set(v2.UserIDRolesContextKey, tt.roles)
				}
				c.Next()
			})
			router.Use(DenyReporterScopeMW(rbacService, logger))
			router.POST("/test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}

	t.Run("Disabled reporter configuration never denies", func(t *testing.T) {
		plainService := rbac.New(rbac.Config{Creators: "sd_creators"})

		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(v2.UserIDRolesContextKey, []string{"sd_reporters"})
			c.Next()
		})
		router.Use(DenyReporterScopeMW(plainService, logger))
		router.POST("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestMiddleware_ZitadelTokenAuthorizesRBAC(t *testing.T) {
	logger := zaptest.NewLogger(t)
	idp := newTestIDP(t)
	rbacService := rbac.New(rbac.Config{Creators: "sd_creators", Operators: "sd_operators", Admins: "sd_admins", Reporters: "sd_reporters"})
	authn := newIDPProvider(t, idp, rbacService.RoleNames()...)

	tests := []struct {
		name           string
		roles          map[string]any
		expectedStatus int
		expectedRole   *rbac.Role
	}{
		{
			name:           "project role resolves to Operator",
			roles:          map[string]any{"sd_operators": map[string]any{testOrgID: "otc"}},
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Operator),
		},
		{
			name:           "unknown project role is forbidden",
			roles:          map[string]any{"sd_readers": map[string]any{testOrgID: "otc"}},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "reporter project role resolves to Reporter",
			roles:          map[string]any{"sd_reporters": map[string]any{testOrgID: "otc"}},
			expectedStatus: http.StatusOK,
			expectedRole:   rolePtr(rbac.Reporter),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := idp.token(t, func(claims map[string]any) { claims[testRolesClaim] = tt.roles })

			var role any

			router := gin.New()
			router.Use(AuthenticationMW(authn, logger))
			router.Use(RBACAuthorizationMW(rbacService, logger))
			router.GET("/test", func(c *gin.Context) {
				role, _ = c.Get(v2.RoleContextKey)
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedRole != nil {
				assert.Equal(t, *tt.expectedRole, role)
			} else {
				assert.Nil(t, role, "denied requests must not receive a role")
			}
		})
	}
}

func TestSetJWTClaims(t *testing.T) {
	logger := zaptest.NewLogger(t)
	idp := newTestIDP(t)
	provider := newIDPProvider(t, idp, "sd_creators")

	t.Run("no authorization header passes anonymously", func(t *testing.T) {
		var exists bool

		router := gin.New()
		router.Use(SetJWTClaims(provider, logger))
		router.GET("/test", func(c *gin.Context) {
			_, exists = c.Get(v2.UserIDContextKey)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.False(t, exists, "anonymous requests carry no identity")
	})

	t.Run("valid Zitadel token sets the identity", func(t *testing.T) {
		token := idp.token(t, func(claims map[string]any) {
			claims[testRolesClaim] = map[string]any{"sd_creators": map[string]any{testOrgID: "otc"}}
		})

		var gotUserID any
		var gotRoles any

		router := gin.New()
		router.Use(SetJWTClaims(provider, logger))
		router.GET("/test", func(c *gin.Context) {
			gotUserID, _ = c.Get(v2.UserIDContextKey)
			gotRoles, _ = c.Get(v2.UserIDRolesContextKey)
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "user-1", gotUserID)
		assert.Equal(t, []string{"sd_creators"}, gotRoles)
	})

	t.Run("invalid token returns 401", func(t *testing.T) {
		w := performRequestWithAuth(SetJWTClaims(provider, logger), "Bearer not-a-token")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestCheckEventExistenceMW(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("invalid eventID returns 400", func(t *testing.T) {
		router := gin.New()
		// Pass nil db - we won't reach the DB call because binding fails
		router.Use(CheckEventExistenceMW(nil, logger))
		router.GET("/events/:eventID", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/events/not-a-number", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestErrorHandle(t *testing.T) {
	t.Run("no errors passes through", func(t *testing.T) {
		router := gin.New()
		router.Use(ErrorHandle())
		router.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("4xx error is passed through", func(t *testing.T) {
		router := gin.New()
		router.Use(ErrorHandle())
		router.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusBadRequest)
			_ = c.Error(fmt.Errorf("bad input"))
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "bad input")
	})

	t.Run("5xx error is masked", func(t *testing.T) {
		router := gin.New()
		router.Use(ErrorHandle())
		router.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusInternalServerError)
			_ = c.Error(fmt.Errorf("database connection lost"))
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NotContains(t, w.Body.String(), "database connection lost")
	})
}

func TestAuthAudit_DoesNotPanic(t *testing.T) {
	logger := zaptest.NewLogger(t)

	assert.NotPanics(t, func() {
		authAudit(logger, "token_validation", "success", auth.ProviderZitadel, "user1", "")
	})
	assert.NotPanics(t, func() {
		authAudit(logger, "token_validation", "failure", "", "", "parse_error")
	})
	assert.NotPanics(t, func() {
		authAudit(logger, "authorization", "denied", "", "user2", "no_matching_rbac_role")
	})
}
