package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/app"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
)

// baseAppConfig is a fully-formed config (app.New does not call FillDefaults) with
// notifications off. The OpenAPI spec path is resolved from the tests/ working dir.
func baseAppConfig() *conf.Config {
	return &conf.Config{
		DB:              databaseURL,
		Port:            "8000",
		WebURL:          "https://status.example.com",
		OIDC:            conf.OIDC{Issuer: testIDP.server.URL, ClientID: testClientID},
		OpenAPISpecPath: "../openapi.yaml",
		RBAC:            conf.RBACConfig{Creators: creatorRole, Operators: operatorRole, Admins: adminRole},
	}
}

func TestApp_BootsWithNotificationsDisabled(t *testing.T) {
	s, err := app.New(baseAppConfig(), zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.DB.Close() })

	assert.Nil(t, s.NotifyFunc(), "no worker wake-up when notifications are disabled")
}

func TestApp_BootsWithNotificationsEnabled(t *testing.T) {
	cfg := baseAppConfig()
	cfg.SMTP = conf.SMTPConfig{Host: "smtp", Port: "587", From: "sd@com.com", Timeout: "30s"}
	cfg.Notifications = conf.NotificationsConfig{
		Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m",
		SmodEmail: "smod@com.com", EmailsOperators: "ops@com.com", EmailsAdmins: "admin@com.com",
	}

	s, err := app.New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.DB.Close() })

	assert.NotNil(t, s.NotifyFunc(), "worker wired on the shared pool when enabled")
}
