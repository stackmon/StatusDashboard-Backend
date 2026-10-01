package tests

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/api/auth"
)

const (
	testClientID = "status-dashboard"
	testKeyID    = "test-key"
	// testRolesClaim is the project roles claim Zitadel emits, nesting the
	// organisation id below the role name.
	testRolesClaim = "urn:zitadel:iam:org:project:roles"
	testOrgID      = "123456789012345678"
	// testUsernameClaim is the claim Zitadel fills for human users; machine accounts have none.
	testUsernameClaim = "preferred_username"
)

// localIDP is a local OpenID Connect server publishing the key that signs the test tokens.
type localIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

// testIDP is created before the package level tokens signed with it and is closed by TestMain.
var testIDP = newLocalIDP()

func newLocalIDP() *localIDP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("failed to generate identity provider key: %v", err))
	}

	idpServer := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: key.Public(),
		KeyID:     testKeyID,
		Algorithm: oidc.RS256,
	}}}
	server := httptest.NewServer(idpServer)
	idpServer.SetIssuer(server.URL)

	return &localIDP{server: server, key: key}
}

func (idp *localIDP) provider(t *testing.T, roleNames ...string) *auth.Provider {
	t.Helper()

	provider, err := auth.NewProvider(context.Background(), auth.ProviderConfig{
		Issuer:        idp.server.URL,
		ClientID:      testClientID,
		RoleNames:     roleNames,
		UsernameClaim: testUsernameClaim,
	})
	require.NoError(t, err)

	return provider
}

// tokenClaims returns the claims Zitadel emits for a user holding the given project roles.
func tokenClaims(userID string, roles ...string) map[string]any {
	rolesClaim := make(map[string]any, len(roles))
	for _, role := range roles {
		rolesClaim[role] = map[string]string{testOrgID: "otc"}
	}

	return map[string]any{
		"iss":             testIDP.server.URL,
		"aud":             testClientID,
		"sub":             userID,
		"exp":             time.Now().Add(time.Hour).Unix(),
		"iat":             time.Now().Unix(),
		testUsernameClaim: userID,
		testRolesClaim:    rolesClaim,
	}
}

func tokenForRole(userID string, roles ...string) string {
	return signToken(testIDP.key, tokenClaims(userID, roles...))
}

// signToken signs claims with the given key; the rejection tests pass a key the
// provider does not publish.
func signToken(key *rsa.PrivateKey, claims map[string]any) string {
	raw, err := json.Marshal(claims)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal token claims: %v", err))
	}

	return oidctest.SignIDToken(key, testKeyID, oidc.RS256, string(raw))
}
