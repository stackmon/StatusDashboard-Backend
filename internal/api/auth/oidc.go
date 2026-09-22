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

// Error values returned while authenticating a request.
var (
	// ErrTokenInvalid covers any token that cannot be trusted: bad signature,
	// unexpected issuer or audience, expired, or missing mandatory claims.
	ErrTokenInvalid = errors.New("token is invalid")
	// ErrNoProviderConfigured is returned when no verifier can handle the token.
	ErrNoProviderConfigured = errors.New("no authentication provider is configured for this token")
)

// maxRolesClaimDepth limits the recursion into the roles claim. Zitadel nests
// role names at most two levels deep.
const maxRolesClaimDepth = 3

// ProviderConfig describes the identity provider that issues access tokens.
type ProviderConfig struct {
	// Issuer is the OIDC issuer URL, e.g. https://zitadel.example.com.
	Issuer string
	// ClientID is the audience the resource server accepts. Tokens issued to
	// any other audience are rejected.
	ClientID string
	// RolesClaim is the claim carrying the project roles. An empty value
	// disables role extraction.
	RolesClaim string
	// RoleNames are the role names the resource server knows about. Claims
	// carrying any other name (organisation and project IDs, organisation
	// names) are ignored.
	RoleNames []string
	// UsernameClaim is an optional claim carrying a human readable user name
	// used for audit logging. It never replaces the subject as identity.
	UsernameClaim string
}

// Provider verifies tokens issued by an OIDC identity provider (Zitadel) and
// extracts the identity and project roles from them.
type Provider struct {
	verifier      *oidc.IDTokenVerifier
	rolesClaim    string
	roleNames     map[string]struct{}
	usernameClaim string
}

// NewProvider discovers the provider metadata and checks that it publishes a
// usable JWKS. Failures are returned to the caller so a misconfigured
// deployment refuses to start instead of rejecting every request at runtime.
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
		Roles:    p.roles(payload[p.rolesClaim]),
		Provider: ProviderZitadel,
	}, nil
}

// roles returns the known role names carried by the roles claim, sorted.
func (p *Provider) roles(value any) []string {
	return extractRoleNames(value, p.roleNames)
}

// jwksURI reads the JWKS location from the provider metadata.
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
// reported at startup. The verifier keeps caching keys by kid afterwards and
// refetches them on rotation.
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

// stringClaim returns the string value of the given claim, empty when the claim
// is absent or of another type.
func stringClaim(payload map[string]any, claim string) string {
	if claim == "" {
		return ""
	}

	value, _ := payload[claim].(string)
	return value
}

// extractRoleNames collects the known role names from the roles claim. Zitadel
// emits project roles either as {role: {orgID: orgName}} or as {orgID: {role:
// orgName}}, so the claim is walked and every key or string value matching a
// known role name is kept; organisation and project IDs, organisation names and
// any other unknown value are ignored.
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

// roleNameSet builds the lookup set of known role names.
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
