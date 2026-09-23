package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestRBACConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    RBACConfig
		expectErr bool
		errSubstr string
	}{
		{
			name: "All roles configured",
			config: RBACConfig{
				Creators:  "sd_creators",
				Operators: "sd_operators",
				Admins:    "sd_admins",
			},
			expectErr: false,
		},
		{
			name: "Only Admins configured (minimum required)",
			config: RBACConfig{
				Admins: "sd_admins",
			},
			expectErr: false,
		},
		{
			name:      "Missing Admins fails validation",
			config:    RBACConfig{},
			expectErr: true,
			errSubstr: "SD_RBAC_ROLES_ADMINS",
		},
		{
			name: "Missing Admins but other roles set fails",
			config: RBACConfig{
				Creators:  "sd_creators",
				Operators: "sd_operators",
			},
			expectErr: true,
			errSubstr: "SD_RBAC_ROLES_ADMINS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConfig_Validate_PropagatesRBACError(t *testing.T) {
	cfg := &Config{
		Port: "8000",
		OIDC: OIDC{
			Issuer:   "https://zitadel.example.com",
			ClientID: "status-dashboard",
		},
		RBAC: RBACConfig{},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SD_RBAC_ROLES_ADMINS")
}

func TestConfig_Validate_RequiresOIDC(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		expectErr bool
		errSubstr string
	}{
		{
			name: "Issuer and client id pass",
			cfg: Config{
				Port: "8000",
				OIDC: OIDC{
					Issuer:   "https://zitadel.example.com",
					ClientID: "status-dashboard",
				},
				RBAC: RBACConfig{Admins: "sd_admins"},
			},
			expectErr: false,
		},
		{
			name: "No issuer and no client id fails",
			cfg: Config{
				Port: "8000",
				RBAC: RBACConfig{Admins: "sd_admins"},
			},
			expectErr: true,
			errSubstr: "SD_OIDC_ISSUER and SD_OIDC_CLIENT_ID",
		},
		{
			name: "Issuer without client id fails",
			cfg: Config{
				Port: "8000",
				OIDC: OIDC{
					Issuer: "https://zitadel.example.com",
				},
				RBAC: RBACConfig{Admins: "sd_admins"},
			},
			expectErr: true,
			errSubstr: "SD_OIDC_ISSUER and SD_OIDC_CLIENT_ID",
		},
		{
			name: "Client id without issuer fails",
			cfg: Config{
				Port: "8000",
				OIDC: OIDC{
					ClientID: "status-dashboard",
				},
				RBAC: RBACConfig{Admins: "sd_admins"},
			},
			expectErr: true,
			errSubstr: "SD_OIDC_ISSUER and SD_OIDC_CLIENT_ID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConfig_Validate_PortValidation(t *testing.T) {
	base := Config{
		OIDC: OIDC{
			Issuer:   "https://zitadel.example.com",
			ClientID: "status-dashboard",
		},
		RBAC: RBACConfig{Admins: "admins"},
	}

	tests := []struct {
		name      string
		port      string
		expectErr bool
		errSubstr string
	}{
		{name: "Valid port", port: "8000", expectErr: false},
		{name: "Non-numeric port", port: "abc", expectErr: true, errSubstr: "wrong SD_PORT format"},
		{name: "Port too low", port: "80", expectErr: true, errSubstr: "wrong port"},
		{name: "Port too high", port: "60000", expectErr: true, errSubstr: "wrong port"},
		{name: "Port at lower boundary", port: "1025", expectErr: false},
		{name: "Port at upper boundary", port: "50000", expectErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.Port = tc.port
			err := cfg.Validate()
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFillDefaults(t *testing.T) {
	t.Run("fills all empty fields", func(t *testing.T) {
		c := &Config{}
		c.FillDefaults()

		assert.Equal(t, DevelopMode, c.LogLevel)
		assert.Equal(t, DefaultPort, c.Port)
		assert.Equal(t, DefaultOpenAPISpecPath, c.OpenAPISpecPath)
	})

	t.Run("preserves existing values", func(t *testing.T) {
		c := &Config{
			LogLevel:        "info",
			Port:            "9090",
			OpenAPISpecPath: "custom.yaml",
		}
		c.FillDefaults()

		assert.Equal(t, "info", c.LogLevel)
		assert.Equal(t, "9090", c.Port)
		assert.Equal(t, "custom.yaml", c.OpenAPISpecPath)
	})

	t.Run("legacy group variables fill the role names", func(t *testing.T) {
		c := &Config{RBAC: RBACConfig{
			GroupsCreators:  "legacy_creators",
			GroupsOperators: "legacy_operators",
			GroupsAdmins:    "legacy_admins",
		}}
		c.FillDefaults()

		assert.Equal(t, "legacy_creators", c.RBAC.Creators)
		assert.Equal(t, "legacy_operators", c.RBAC.Operators)
		assert.Equal(t, "legacy_admins", c.RBAC.Admins)
		assert.True(t, c.RBAC.legacyRoleNamesUsed())
	})

	t.Run("role names win over legacy group variables", func(t *testing.T) {
		c := &Config{RBAC: RBACConfig{
			Creators:       "sd_creators",
			GroupsCreators: "legacy_creators",
			GroupsAdmins:   "legacy_admins",
		}}
		c.FillDefaults()

		assert.Equal(t, "sd_creators", c.RBAC.Creators)
		assert.Equal(t, "legacy_admins", c.RBAC.Admins)
	})
}

func TestSanitizeDBString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{name: "Empty string", input: "", expect: ""},
		{
			name:   "Full connection string strips credentials",
			input:  "postgresql://user:pass@localhost:5432/mydb",
			expect: "postgresql://localhost:5432/mydb",
		},
		{
			name:   "URL without credentials",
			input:  "postgresql://localhost:5432/mydb",
			expect: "postgresql://localhost:5432/mydb",
		},
		{
			name:   "Invalid URL returns raw string",
			input:  "://broken",
			expect: "://broken",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, sanitizeDBString(tc.input))
		})
	}
}

func TestMergeConfigs(t *testing.T) {
	t.Run("nil env map is no-op", func(t *testing.T) {
		c := &Config{}
		err := mergeConfigs(nil, c, "SD")
		require.NoError(t, err)
	})

	t.Run("non-pointer returns error", func(t *testing.T) {
		err := mergeConfigs(map[string]string{}, Config{}, "SD")
		require.ErrorIs(t, err, ErrInvalidDataMerge)
	})

	t.Run("pointer to non-struct returns error", func(t *testing.T) {
		s := "hello"
		err := mergeConfigs(map[string]string{}, &s, "SD")
		require.ErrorIs(t, err, ErrInvalidDataMerge)
	})

	t.Run("fills empty string fields from env map", func(t *testing.T) {
		c := &Config{}
		env := map[string]string{
			"SD_DB":        "postgresql://localhost/test",
			"SD_LOG_LEVEL": "info",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "postgresql://localhost/test", c.DB)
		assert.Equal(t, "info", c.LogLevel)
	})

	t.Run("does not overwrite existing values", func(t *testing.T) {
		c := &Config{DB: "existing"}
		env := map[string]string{
			"SD_DB": "overwritten",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "existing", c.DB)
	})

	t.Run("merges into embedded struct (RBACConfig)", func(t *testing.T) {
		c := &Config{}
		env := map[string]string{
			"SD_RBAC_ROLES_ADMINS":    "my-admins",
			"SD_RBAC_GROUPS_CREATORS": "legacy-creators",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "my-admins", c.RBAC.Admins)
		assert.Equal(t, "legacy-creators", c.RBAC.GroupsCreators)
	})

	t.Run("merges into embedded struct (OIDC)", func(t *testing.T) {
		c := &Config{}
		env := map[string]string{
			"SD_OIDC_ISSUER":    "https://zitadel.example.com",
			"SD_OIDC_CLIENT_ID": "status-dashboard",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "https://zitadel.example.com", c.OIDC.Issuer)
		assert.Equal(t, "status-dashboard", c.OIDC.ClientID)
	})
}

func TestConfig_Log(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("logs the OIDC and RBAC configuration", func(t *testing.T) {
		c := &Config{
			Port:     "8000",
			DB:       "postgresql://localhost:5432/db",
			LogLevel: "devel",
			RBAC:     RBACConfig{Admins: "admins"},
			OIDC: OIDC{
				Issuer:        "https://zitadel.example.com",
				ClientID:      "status-dashboard",
				UsernameClaim: "preferred_username",
			},
		}
		assert.NotPanics(t, func() { c.Log(logger) })
	})

	t.Run("logs the deprecated group variables", func(t *testing.T) {
		c := &Config{
			Port:     "8000",
			LogLevel: "devel",
			RBAC:     RBACConfig{GroupsAdmins: "legacy-admins"},
		}
		assert.NotPanics(t, func() { c.Log(logger) })
	})
}
