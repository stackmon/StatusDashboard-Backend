package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testClientID      = "status-dashboard"
	testKeyID         = "test-key"
	testRolesClaim    = "urn:zitadel:iam:org:project:roles"
	testUsernameClaim = "preferred_username"
	testOrgID         = "390700708019568682"
)

// testIDP is a local OpenID Connect server publishing one RSA key.
type testIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newTestIDP(t *testing.T, withKey bool) *testIDP {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	keys := []oidctest.PublicKey{}
	if withKey {
		keys = append(keys, oidctest.PublicKey{
			PublicKey: key.Public(),
			KeyID:     testKeyID,
			Algorithm: oidc.RS256,
		})
	}

	server := &oidctest.Server{PublicKeys: keys}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	server.SetIssuer(srv.URL)

	return &testIDP{server: srv, key: key}
}

// token signs the default claims, optionally mutated by the caller.
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

func newTestProvider(t *testing.T, idp *testIDP, roleNames ...string) *Provider {
	t.Helper()

	provider, err := NewProvider(context.Background(), ProviderConfig{
		Issuer:        idp.server.URL,
		ClientID:      testClientID,
		RolesClaim:    testRolesClaim,
		RoleNames:     roleNames,
		UsernameClaim: testUsernameClaim,
	})
	require.NoError(t, err)

	return provider
}

func TestProviderVerify(t *testing.T) {
	t.Parallel()

	idp := newTestIDP(t, true)
	provider := newTestProvider(t, idp, "sd_creators", "sd_operators", "sd_admins")

	t.Run("roles claim keyed by role name", func(t *testing.T) {
		t.Parallel()

		token := idp.token(t, func(claims map[string]any) {
			claims[testUsernameClaim] = "alice"
			claims[testRolesClaim] = map[string]any{
				"sd_admins":    map[string]any{testOrgID: "zitadel.otc-service.com"},
				"sd_creators":  map[string]any{testOrgID: "zitadel.otc-service.com"},
				"unknown_role": map[string]any{testOrgID: "zitadel.otc-service.com"},
			}
		})

		claims, err := provider.Verify(context.Background(), token)

		require.NoError(t, err)
		assert.Equal(t, "user-1", claims.Subject)
		assert.Equal(t, "alice", claims.Username)
		assert.Equal(t, ProviderZitadel, claims.Provider)
		assert.Equal(t, []string{"sd_admins", "sd_creators"}, claims.Roles,
			"only configured role names are extracted, sorted and deduplicated")
	})

	t.Run("roles claim keyed by organisation id", func(t *testing.T) {
		t.Parallel()

		token := idp.token(t, func(claims map[string]any) {
			claims[testRolesClaim] = map[string]any{
				testOrgID: map[string]any{"sd_operators": "zitadel.otc-service.com"},
			}
		})

		claims, err := provider.Verify(context.Background(), token)

		require.NoError(t, err)
		assert.Equal(t, []string{"sd_operators"}, claims.Roles)
	})

	t.Run("roles claim as string array", func(t *testing.T) {
		t.Parallel()

		token := idp.token(t, func(claims map[string]any) {
			claims[testRolesClaim] = []any{"sd_creators", "sd_admins", "argocd-admin"}
		})

		claims, err := provider.Verify(context.Background(), token)

		require.NoError(t, err)
		assert.Equal(t, []string{"sd_admins", "sd_creators"}, claims.Roles)
	})

	t.Run("no roles claim", func(t *testing.T) {
		t.Parallel()

		claims, err := provider.Verify(context.Background(), idp.token(t, nil))

		require.NoError(t, err)
		assert.Empty(t, claims.Roles)
		assert.Empty(t, claims.Username)
	})
}

func TestProviderVerifyRejectsBadTokens(t *testing.T) {
	t.Parallel()

	idp := newTestIDP(t, true)
	provider := newTestProvider(t, idp, "sd_admins")

	testCases := []struct {
		name   string
		mutate func(claims map[string]any)
	}{
		{
			name: "foreign issuer",
			mutate: func(claims map[string]any) {
				claims["iss"] = "https://another-idp.example.com"
			},
		},
		{
			name: "foreign audience",
			mutate: func(claims map[string]any) {
				claims["aud"] = "another-client"
			},
		},
		{
			name: "expired token",
			mutate: func(claims map[string]any) {
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			},
		},
		{
			name: "missing subject",
			mutate: func(claims map[string]any) {
				delete(claims, "sub")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := provider.Verify(context.Background(), idp.token(t, testCase.mutate))

			assert.ErrorIs(t, err, ErrTokenInvalid)
		})
	}

	t.Run("unsigned token with alg none", func(t *testing.T) {
		t.Parallel()

		unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
			"iss": idp.server.URL,
			"aud": testClientID,
			"sub": "user-1",
			"exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		_, verifyErr := provider.Verify(context.Background(), unsigned)

		assert.ErrorIs(t, verifyErr, ErrTokenInvalid)
	})

	t.Run("garbage token", func(t *testing.T) {
		t.Parallel()

		_, err := provider.Verify(context.Background(), "not-a-token")

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("unknown key id", func(t *testing.T) {
		t.Parallel()

		foreign := newTestIDP(t, true)
		token := foreign.token(t, nil)

		_, err := provider.Verify(context.Background(), token)

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})
}

func TestNewProviderDiscoveryErrors(t *testing.T) {
	t.Parallel()

	t.Run("unreachable issuer", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err := NewProvider(ctx, ProviderConfig{
			Issuer:   "http://127.0.0.1:1",
			ClientID: testClientID,
		})

		assert.ErrorContains(t, err, "oidc discovery")
	})

	t.Run("empty key set", func(t *testing.T) {
		t.Parallel()

		idp := newTestIDP(t, false)

		_, err := NewProvider(context.Background(), ProviderConfig{
			Issuer:   idp.server.URL,
			ClientID: testClientID,
		})

		assert.ErrorContains(t, err, "key set does not contain any key")
	})
}

func TestExtractRoleNames(t *testing.T) {
	t.Parallel()

	known := roleNameSet([]string{"sd_creators", "sd_operators", "sd_admins"})

	testCases := []struct {
		name     string
		value    any
		expected []string
	}{
		{name: "nil claim", value: nil, expected: []string{}},
		{
			name:     "role to organisation mapping",
			value:    map[string]any{"sd_admins": map[string]any{testOrgID: "otc"}},
			expected: []string{"sd_admins"},
		},
		{
			name:     "organisation to role mapping",
			value:    map[string]any{testOrgID: map[string]any{"sd_operators": "otc"}},
			expected: []string{"sd_operators"},
		},
		{
			name:     "multiple organisations",
			value:    map[string]any{"sd_creators": map[string]any{"1": "a", "2": "b"}},
			expected: []string{"sd_creators"},
		},
		{
			name:     "unknown names are ignored",
			value:    map[string]any{"sd_readers": map[string]any{"1": "a"}},
			expected: []string{},
		},
		{
			name:     "string array",
			value:    []any{"sd_admins", "nested", 42},
			expected: []string{"sd_admins"},
		},
		{
			name:     "plain string",
			value:    "sd_operators",
			expected: []string{"sd_operators"},
		},
		{
			name:     "unexpected type",
			value:    42,
			expected: []string{},
		},
		{
			name: "deeply nested value beyond the depth limit",
			value: map[string]any{
				"1": map[string]any{
					"2": map[string]any{
						"3": map[string]any{"4": "sd_admins"},
					},
				},
			},
			expected: []string{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.expected, extractRoleNames(testCase.value, known))
		})
	}

	t.Run("no known role names", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, extractRoleNames(map[string]any{"sd_admins": "x"}, roleNameSet(nil)))
	})
}

func TestRoleNameSet(t *testing.T) {
	t.Parallel()

	set := roleNameSet([]string{" sd_admins ", "", "sd_creators"})

	assert.Len(t, set, 2)
	assert.Contains(t, set, "sd_admins")
	assert.Contains(t, set, "sd_creators")
}

func TestStringClaim(t *testing.T) {
	t.Parallel()

	payload := map[string]any{"preferred_username": "alice", "email": 42}

	assert.Equal(t, "alice", stringClaim(payload, "preferred_username"))
	assert.Empty(t, stringClaim(payload, "email"))
	assert.Empty(t, stringClaim(payload, "missing"))
	assert.Empty(t, stringClaim(payload, ""))
}
