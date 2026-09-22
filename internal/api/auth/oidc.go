package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// ErrTokenInvalid covers any token that cannot be trusted: bad signature,
// unexpected issuer or audience, expired, or missing mandatory claims.
var ErrTokenInvalid = errors.New("token is invalid")

// maxRolesClaimDepth limits the recursion into the roles claim. Zitadel nests
// role names at most two levels deep.
const maxRolesClaimDepth = 3

// ProviderConfig describes the identity provider that issues access tokens.
type ProviderConfig struct {
	Issuer string
	// ClientID is the audience every accepted token must be issued to.
	ClientID string
	// RolesClaim is the claim carrying the project roles; empty disables role extraction.
	RolesClaim string
	// RoleNames are the role names the resource server knows, all other names in the claim are ignored.
	RoleNames []string
	// UsernameClaim is an optional display name for audit logging, never the identity.
	UsernameClaim string
}

// Provider verifies tokens issued by Zitadel and extracts identity and project roles.
type Provider struct {
	verifier      *oidc.IDTokenVerifier
	rolesClaim    string
	roleNames     map[string]struct{}
	usernameClaim string
}

// NewProvider discovers the provider metadata and checks that it publishes a
// usable JWKS, so that a misconfigured deployment refuses to start instead of
// rejecting every request at runtime.
func NewProvider(ctx context.Context, cfg ProviderConfig) (*Provider, error) {
	oidcProvider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for issuer %s: %w", cfg.Issuer, err)
	}

	jwksURL, err := jwksURI(oidcProvider, cfg.Issuer)
	if err != nil {
		return nil, err
	}

	if err = checkKeySet(ctx, jwksURL); err != nil {
		return nil, fmt.Errorf("oidc key set at %s: %w", jwksURL, err)
	}

	return &Provider{
		verifier:      oidcProvider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		rolesClaim:    cfg.RolesClaim,
		roleNames:     roleNameSet(cfg.RoleNames),
		usernameClaim: cfg.UsernameClaim,
	}, nil
}

// Verify checks the signature, issuer, audience and expiry of the raw token and
// returns the identity it carries.
func (p *Provider) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	token, err := p.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	if token.Subject == "" {
		return nil, fmt.Errorf("%w: subject claim is missing", ErrTokenInvalid)
	}

	var payload map[string]any
	if err = token.Claims(&payload); err != nil {
		return nil, fmt.Errorf("%w: cannot decode claims: %w", ErrTokenInvalid, err)
	}

	return &Claims{
		Subject:  token.Subject,
		Username: stringClaim(payload, p.usernameClaim),
		Roles:    extractRoleNames(payload[p.rolesClaim], p.roleNames),
		Provider: ProviderZitadel,
	}, nil
}

func jwksURI(provider *oidc.Provider, issuer string) (string, error) {
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}

	if err := provider.Claims(&metadata); err != nil {
		return "", fmt.Errorf("oidc provider metadata: %w", err)
	}

	if metadata.JWKSURI == "" {
		return "", fmt.Errorf("oidc provider %s does not publish a jwks_uri", issuer)
	}

	return metadata.JWKSURI, nil
}

// checkKeySet prefetches the JWKS so that an unreachable or empty key set is
// reported at startup. The verifier caches keys by kid afterwards and refetches
// them on rotation.
func checkKeySet(ctx context.Context, jwksURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var keySet struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&keySet); err != nil {
		return fmt.Errorf("cannot decode key set: %w", err)
	}

	if len(keySet.Keys) == 0 {
		return errors.New("key set does not contain any key")
	}

	return nil
}

// stringClaim returns the string value of a claim, empty when it is absent or not a string.
func stringClaim(payload map[string]any, claim string) string {
	if claim == "" {
		return ""
	}

	value, _ := payload[claim].(string)
	return value
}

// extractRoleNames collects the known role names from the roles claim. Zitadel
// nests them as {role: {orgID: orgName}} or {orgID: {role: orgName}}, so keys and
// string values are both matched against the known names.
func extractRoleNames(value any, known map[string]struct{}) []string {
	if value == nil || len(known) == 0 {
		return []string{}
	}

	found := make(map[string]struct{})
	collectRoleNames(value, known, found, 0)

	roles := make([]string, 0, len(found))
	for name := range found {
		roles = append(roles, name)
	}
	sort.Strings(roles)

	return roles
}

func collectRoleNames(value any, known, found map[string]struct{}, depth int) {
	if depth > maxRolesClaimDepth {
		return
	}

	switch typed := value.(type) {
	case map[string]any:
		for name, nested := range typed {
			if _, ok := known[name]; ok {
				found[name] = struct{}{}
			}

			collectRoleNames(nested, known, found, depth+1)
		}
	case []any:
		for _, item := range typed {
			collectRoleNames(item, known, found, depth)
		}
	case string:
		if _, ok := known[typed]; ok {
			found[typed] = struct{}{}
		}
	}
}

func roleNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			set[name] = struct{}{}
		}
	}

	return set
}
