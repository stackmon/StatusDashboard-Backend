package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
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

func TestConfig_Validate_PropagatesStaticError(t *testing.T) {
	cfg := &Config{
		Port: "8000",
		OIDC: OIDC{
			Issuer:   "https://zitadel.example.com",
			ClientID: "status-dashboard",
		},
		RBAC:   RBACConfig{Admins: "sd_admins"},
		Static: Static{Origins: "a.obs.example.com", CacheTTL: "5m", CacheMaxBytes: "0"},
	}

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SD_STATIC_CACHE_MAX_BYTES")
}

func TestConfig_Validate_AcceptsDefaultStaticSettings(t *testing.T) {
	cfg := &Config{
		Port: "8000",
		OIDC: OIDC{Issuer: "https://zitadel.example.com", ClientID: "status-dashboard"},
		RBAC: RBACConfig{Admins: "sd_admins"},
	}
	cfg.FillDefaults()

	require.NoError(t, cfg.Validate())
}

func TestStatic_Validate(t *testing.T) {
	valid := Static{CacheTTL: "5m", CacheMaxBytes: "67108864"}

	tests := []struct {
		name      string
		cfg       Static
		expectErr bool
		errSubstr string
	}{
		{
			name: "Proxy disabled without origins",
			cfg:  Static{},
		},
		{
			name: "Unused cache settings are ignored while the proxy is disabled",
			cfg:  Static{CacheTTL: "5 minutes", CacheMaxBytes: "0"},
		},
		{
			name: "Configured origins with usable cache settings",
			cfg:  valid,
		},
		{
			name: "Several origins in failover order",
			cfg:  Static{Origins: "a.obs.example.com,b.obs.example.com", CacheTTL: "1m", CacheMaxBytes: "1024"},
		},
		{
			name: "Empty entries between origins are ignored",
			cfg:  Static{Origins: ",a.obs.example.com, ,", CacheTTL: "1m", CacheMaxBytes: "1024"},
		},
		{
			name:      "Origin with a scheme",
			cfg:       Static{Origins: "https://a.obs.example.com", CacheTTL: "1m", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_ORIGINS",
		},
		{
			name:      "Origin with a path",
			cfg:       Static{Origins: "a.obs.example.com/index.html", CacheTTL: "1m", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_ORIGINS",
		},
		{
			name:      "Origin with a port",
			cfg:       Static{Origins: "a.obs.example.com:443", CacheTTL: "1m", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_ORIGINS",
		},
		{
			name:      "Malformed duration",
			cfg:       Static{Origins: "a.obs.example.com", CacheTTL: "5 minutes", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_TTL",
		},
		{
			name:      "Missing duration",
			cfg:       Static{Origins: "a.obs.example.com", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_TTL",
		},
		{
			name:      "Zero duration",
			cfg:       Static{Origins: "a.obs.example.com", CacheTTL: "0s", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_TTL",
		},
		{
			name:      "Negative duration",
			cfg:       Static{Origins: "a.obs.example.com", CacheTTL: "-5m", CacheMaxBytes: "1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_TTL",
		},
		{
			name:      "Byte budget with a unit",
			cfg:       Static{Origins: "a.obs.example.com", CacheTTL: "1m", CacheMaxBytes: "64MiB"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_MAX_BYTES",
		},
		{
			name:      "Zero byte budget",
			cfg:       Static{Origins: "a.obs.example.com", CacheTTL: "1m", CacheMaxBytes: "0"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_MAX_BYTES",
		},
		{
			name:      "Negative byte budget",
			cfg:       Static{Origins: "a.obs.example.com", CacheTTL: "1m", CacheMaxBytes: "-1024"},
			expectErr: true,
			errSubstr: "SD_STATIC_CACHE_MAX_BYTES",
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

func TestStatic_Accessors(t *testing.T) {
	cfg := Static{
		Origins:       " primary.obs.example.com , backup.obs.example.com ",
		CacheTTL:      "90s",
		CacheMaxBytes: "1048576",
	}

	origins, err := cfg.OriginList()
	require.NoError(t, err)
	assert.Equal(t, []string{"primary.obs.example.com", "backup.obs.example.com"}, origins)

	ttl, err := cfg.TTL()
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, ttl)

	maxBytes, err := cfg.MaxBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(1048576), maxBytes)
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
		assert.Equal(t, DefaultStaticCacheTTL, c.Static.CacheTTL)
		assert.Equal(t, DefaultStaticCacheMaxBytes, c.Static.CacheMaxBytes)
	})

	t.Run("preserves existing values", func(t *testing.T) {
		c := &Config{
			LogLevel:        "info",
			Port:            "9090",
			OpenAPISpecPath: "custom.yaml",
			RBAC:            RBACConfig{Creators: "sd_creators", Admins: "sd_admins"},
			Static:          Static{Origins: "a.obs.example.com", CacheTTL: "1m", CacheMaxBytes: "4096"},
		}
		c.FillDefaults()

		assert.Equal(t, "info", c.LogLevel)
		assert.Equal(t, "9090", c.Port)
		assert.Equal(t, "custom.yaml", c.OpenAPISpecPath)
		assert.Equal(t, "sd_creators", c.RBAC.Creators)
		assert.Equal(t, "sd_admins", c.RBAC.Admins)
		assert.Equal(t, "a.obs.example.com", c.Static.Origins)
		assert.Equal(t, "1m", c.Static.CacheTTL)
		assert.Equal(t, "4096", c.Static.CacheMaxBytes)
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
			"SD_RBAC_ROLES_ADMINS":   "my-admins",
			"SD_RBAC_ROLES_CREATORS": "my-creators",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "my-admins", c.RBAC.Admins)
		assert.Equal(t, "my-creators", c.RBAC.Creators)
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

	t.Run("merges into embedded struct (Static)", func(t *testing.T) {
		c := &Config{}
		env := map[string]string{
			"SD_STATIC_ORIGINS":         "a.obs.example.com",
			"SD_STATIC_CACHE_TTL":       "1m",
			"SD_STATIC_CACHE_MAX_BYTES": "4096",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "a.obs.example.com", c.Static.Origins)
		assert.Equal(t, "1m", c.Static.CacheTTL)
		assert.Equal(t, "4096", c.Static.CacheMaxBytes)
	})
}

func TestConfig_Log(t *testing.T) {
	t.Run("logs the OIDC and RBAC configuration", func(t *testing.T) {
		logger := zaptest.NewLogger(t)

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

	t.Run("logs every configured role name", func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)

		c := &Config{
			Port:     "8000",
			LogLevel: "devel",
			RBAC: RBACConfig{
				Creators:  "sd_creators",
				Operators: "sd_operators",
				Admins:    "sd_admins",
				Reporters: "sd_reporters",
			},
		}
		c.Log(zap.New(core))

		entries := logs.FilterMessage("Authentication configuration").All()
		require.Len(t, entries, 1)

		fields := entries[0].ContextMap()
		assert.Equal(t, "sd_creators", fields["creators_role"])
		assert.Equal(t, "sd_operators", fields["operators_role"])
		assert.Equal(t, "sd_admins", fields["admins_role"])
		assert.Equal(t, "sd_reporters", fields["reporters_role"])
	})

	t.Run("logs the static site configuration", func(t *testing.T) {
		core, logs := observer.New(zap.InfoLevel)

		c := &Config{
			Port:   "8000",
			Static: Static{Origins: "bucket.obs.example.com", CacheTTL: "5m", CacheMaxBytes: "67108864"},
		}
		c.Log(zap.New(core))

		entries := logs.FilterMessage("Static site configuration").All()
		require.Len(t, entries, 1)

		fields := entries[0].ContextMap()
		assert.Equal(t, "bucket.obs.example.com", fields["origins"])
		assert.Equal(t, "5m", fields["cache_ttl"])
		assert.Equal(t, "67108864", fields["cache_max_bytes"])
	})
}
