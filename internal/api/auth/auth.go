package auth

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Authentication paths, reported as idp_type in the audit log.
const (
	ProviderZitadel   = "zitadel"
	ProviderLocalHMAC = "local_hmac"
)

// Claims of local HMAC tokens. They are read only by the transitional HMAC
// branch and disappear with it.
const (
	hmacUsernameClaim = "preferred_username"
	hmacRolesClaim    = "groups"
)

// Claims is the verified identity of the caller.
type Claims struct {
	// Subject is the stable identifier of the caller: the OIDC subject for
	// Zitadel tokens, the preferred_username for local HMAC tokens.
	Subject string
	// Username is a human readable name for audit logging, empty when unknown.
	Username string
	// Roles are the role names used for RBAC resolution.
	Roles []string
	// Provider is the authentication path that verified the token.
	Provider string
}

// Authenticator verifies bearer tokens: OIDC tokens issued by the external
// identity provider and, during the migration period, local HMAC tokens.
type Authenticator struct {
	provider *Provider
	hmacKey  []byte
}

// NewAuthenticator creates an authenticator from the configured providers. Both
// are optional, but a token is only accepted when the matching provider is
// configured.
func NewAuthenticator(provider *Provider, hmacSecretKey string) *Authenticator {
	return &Authenticator{
		provider: provider,
		hmacKey:  []byte(hmacSecretKey),
	}
}

// Verify returns the identity carried by the raw token. The token header selects
// the provider: HMAC algorithms go to the local branch, everything else must be
// verifiable against the OIDC provider.
func (a *Authenticator) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	method, err := signingMethod(rawToken)
	if err != nil {
		return nil, err
	}

	if _, isHMAC := method.(*jwt.SigningMethodHMAC); isHMAC {
		return a.verifyHMAC(rawToken)
	}

	if a.provider == nil {
		return nil, fmt.Errorf("%w: token signed with %s is not accepted", ErrNoProviderConfigured, method.Alg())
	}

	return a.provider.Verify(ctx, rawToken)
}

// verifyHMAC handles locally signed tokens. It intentionally keeps the original
// behaviour of the transitional HMAC branch: no issuer or audience check, and
// the identity comes from preferred_username with roles from groups.
func (a *Authenticator) verifyHMAC(rawToken string) (*Claims, error) {
	if len(a.hmacKey) == 0 {
		return nil, fmt.Errorf("%w: HMAC tokens are rejected, SD_SECRET_KEY is not configured",
			ErrNoProviderConfigured)
	}

	claims := jwt.MapClaims{}
	keyFunc := func(*jwt.Token) (any, error) { return a.hmacKey, nil }

	_, err := jwt.ParseWithClaims(rawToken, claims, keyFunc,
		jwt.WithValidMethods([]string{
			jwt.SigningMethodHS256.Alg(),
			jwt.SigningMethodHS384.Alg(),
			jwt.SigningMethodHS512.Alg(),
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	username, ok := claims[hmacUsernameClaim].(string)
	if !ok || username == "" {
		return nil, fmt.Errorf("%w: %s claim is missing", ErrTokenInvalid, hmacUsernameClaim)
	}

	rawRoles, ok := claims[hmacRolesClaim].([]any)
	if !ok {
		return nil, fmt.Errorf("%w: %s claim is not an array", ErrTokenInvalid, hmacRolesClaim)
	}

	roles := make([]string, 0, len(rawRoles))
	for _, role := range rawRoles {
		name, isString := role.(string)
		if !isString {
			return nil, fmt.Errorf("%w: %s claim contains a non-string value", ErrTokenInvalid, hmacRolesClaim)
		}

		roles = append(roles, name)
	}

	return &Claims{
		Subject:  username,
		Username: username,
		Roles:    roles,
		Provider: ProviderLocalHMAC,
	}, nil
}

// signingMethod reads the signing algorithm from the token header without
// validating the token.
func signingMethod(rawToken string) (jwt.SigningMethod, error) {
	token, _, err := jwt.NewParser().ParseUnverified(rawToken, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read token header: %w", ErrTokenInvalid, err)
	}

	if token.Method == nil {
		return nil, fmt.Errorf("%w: unsupported signing algorithm", ErrTokenInvalid)
	}

	return token.Method, nil
}
