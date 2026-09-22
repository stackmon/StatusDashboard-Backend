package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHMACSecret = "test-secret-key-for-unit-tests!!"

func signHMAC(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(method, claims)
	signed, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	return signed
}

func validHMACClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"preferred_username": "alice",
		"groups":             []any{"sd_creators", "argocd-admin"},
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
}

func TestAuthenticatorVerifyHMAC(t *testing.T) {
	t.Parallel()

	authn := NewAuthenticator(nil, testHMACSecret)

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()

		claims, err := authn.Verify(context.Background(),
			signHMAC(t, testHMACSecret, jwt.SigningMethodHS256, validHMACClaims()))

		require.NoError(t, err)
		assert.Equal(t, "alice", claims.Subject)
		assert.Equal(t, "alice", claims.Username)
		assert.Equal(t, []string{"sd_creators", "argocd-admin"}, claims.Roles)
		assert.Equal(t, ProviderLocalHMAC, claims.Provider)
	})

	t.Run("hmac token without configured secret", func(t *testing.T) {
		t.Parallel()

		noSecret := NewAuthenticator(nil, "")
		_, err := noSecret.Verify(context.Background(),
			signHMAC(t, testHMACSecret, jwt.SigningMethodHS256, validHMACClaims()))

		assert.ErrorIs(t, err, ErrNoProviderConfigured)
	})

	t.Run("wrong secret", func(t *testing.T) {
		t.Parallel()

		_, err := authn.Verify(context.Background(),
			signHMAC(t, "another-secret", jwt.SigningMethodHS256, validHMACClaims()))

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()

		claims := validHMACClaims()
		claims["exp"] = time.Now().Add(-time.Hour).Unix()

		_, err := authn.Verify(context.Background(),
			signHMAC(t, testHMACSecret, jwt.SigningMethodHS256, claims))

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("missing preferred_username", func(t *testing.T) {
		t.Parallel()

		claims := validHMACClaims()
		delete(claims, "preferred_username")

		_, err := authn.Verify(context.Background(),
			signHMAC(t, testHMACSecret, jwt.SigningMethodHS256, claims))

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("groups is not an array", func(t *testing.T) {
		t.Parallel()

		claims := validHMACClaims()
		claims["groups"] = "sd_creators"

		_, err := authn.Verify(context.Background(),
			signHMAC(t, testHMACSecret, jwt.SigningMethodHS256, claims))

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("groups contains a non-string value", func(t *testing.T) {
		t.Parallel()

		claims := validHMACClaims()
		claims["groups"] = []any{"sd_creators", 42}

		_, err := authn.Verify(context.Background(),
			signHMAC(t, testHMACSecret, jwt.SigningMethodHS256, claims))

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("tampered payload", func(t *testing.T) {
		t.Parallel()

		_, err := authn.Verify(context.Background(), "eyJhbGciOiJIUzI1NiJ9.not-a-payload.sig")

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})
}

func TestAuthenticatorVerifyOIDC(t *testing.T) {
	t.Parallel()

	t.Run("asymmetric token without configured provider", func(t *testing.T) {
		t.Parallel()

		idp := newTestIDP(t, true)
		authn := NewAuthenticator(nil, testHMACSecret)

		_, err := authn.Verify(context.Background(), idp.token(t, nil))

		assert.ErrorIs(t, err, ErrNoProviderConfigured)
	})

	t.Run("asymmetric token is delegated to the provider", func(t *testing.T) {
		t.Parallel()

		idp := newTestIDP(t, true)
		provider := newTestProvider(t, idp, "sd_admins")
		authn := NewAuthenticator(provider, testHMACSecret)

		claims, err := authn.Verify(context.Background(), idp.token(t, func(claims map[string]any) {
			claims[testRolesClaim] = map[string]any{"sd_admins": map[string]any{testOrgID: "otc"}}
		}))

		require.NoError(t, err)
		assert.Equal(t, ProviderZitadel, claims.Provider)
		assert.Equal(t, []string{"sd_admins"}, claims.Roles)
	})

	t.Run("malformed token", func(t *testing.T) {
		t.Parallel()

		authn := NewAuthenticator(nil, testHMACSecret)

		_, err := authn.Verify(context.Background(), "garbage")

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})

	t.Run("alg none token", func(t *testing.T) {
		t.Parallel()

		token := jwt.NewWithClaims(jwt.SigningMethodNone, validHMACClaims())
		unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		t.Run("is rejected without a provider", func(t *testing.T) {
			t.Parallel()

			authn := NewAuthenticator(nil, testHMACSecret)

			_, verifyErr := authn.Verify(context.Background(), unsigned)

			assert.ErrorIs(t, verifyErr, ErrNoProviderConfigured)
		})

		t.Run("is rejected by the provider", func(t *testing.T) {
			t.Parallel()

			idp := newTestIDP(t, true)
			authn := NewAuthenticator(newTestProvider(t, idp), testHMACSecret)

			_, verifyErr := authn.Verify(context.Background(), unsigned)

			assert.ErrorIs(t, verifyErr, ErrTokenInvalid)
		})
	})
}

func TestSigningMethod(t *testing.T) {
	t.Parallel()

	t.Run("hmac", func(t *testing.T) {
		t.Parallel()

		method, err := signingMethod(signHMAC(t, testHMACSecret, jwt.SigningMethodHS384, validHMACClaims()))

		require.NoError(t, err)
		assert.Equal(t, jwt.SigningMethodHS384.Alg(), method.Alg())
	})

	t.Run("rsa", func(t *testing.T) {
		t.Parallel()

		idp := newTestIDP(t, true)

		method, err := signingMethod(idp.token(t, nil))

		require.NoError(t, err)
		assert.Equal(t, "RS256", method.Alg())
	})

	t.Run("malformed", func(t *testing.T) {
		t.Parallel()

		_, err := signingMethod("not-a-jwt")

		assert.ErrorIs(t, err, ErrTokenInvalid)
	})
}
