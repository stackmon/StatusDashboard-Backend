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

	t.Run("merges untagged SMTP fields by field name", func(t *testing.T) {
		c := &Config{}
		env := map[string]string{
			"SD_SMTP_HOST":    "smtp.local",
			"SD_SMTP_USER":    "mailer",
			"SD_SMTP_TIMEOUT": "15s",
		}
		err := mergeConfigs(env, c, "SD")
		require.NoError(t, err)
		assert.Equal(t, "smtp.local", c.SMTP.Host)
		assert.Equal(t, "mailer", c.SMTP.User)
		assert.Equal(t, "15s", c.SMTP.Timeout)
	})
}

// TestLoadConf_IgnoresBareEnvNames guards against envconfig's fallback to the bare tag
// name: a tag of "USER" would otherwise inherit the shell's $USER and enable SMTP AUTH
// against a server that offers none, and "HOSTNAME" would adopt the container's name.
func TestLoadConf_IgnoresBareEnvNames(t *testing.T) {
	// The untagged SMTP fields must not fall back to the shell's $USER / $PASSWORD,
	// which would enable SMTP AUTH against a relay that offers none.
	t.Setenv("USER", "shell-user")
	t.Setenv("PASSWORD", "shell-password")
	t.Setenv("SD_OIDC_ISSUER", "https://zitadel.example.com")
	t.Setenv("SD_OIDC_CLIENT_ID", "status-dashboard")
	t.Setenv("SD_RBAC_ROLES_ADMINS", "sd_admins")
	t.Setenv("SD_SMTP_HOST", "127.0.0.1")

	c, err := LoadConf()
	require.NoError(t, err)

	assert.Empty(t, c.SMTP.User)
	assert.Empty(t, c.SMTP.Password)
	assert.Equal(t, "127.0.0.1", c.SMTP.Host)
}

func TestLoadConf_PrefixedNamesStillApply(t *testing.T) {
	t.Setenv("HOSTNAME", "pod-7f9c8d4b6-xk2wl")
	t.Setenv("SD_PORT", "9000")
	t.Setenv("SD_DB", "postgresql://localhost/sd")
	t.Setenv("SD_CACHE", "internal")
	t.Setenv("SD_OIDC_ISSUER", "https://zitadel.example.com")
	t.Setenv("SD_OIDC_CLIENT_ID", "status-dashboard")
	t.Setenv("SD_RBAC_ROLES_ADMINS", "sd_admins")
	t.Setenv("SD_WEB_URL", "https://web.example.com")

	c, err := LoadConf()
	require.NoError(t, err)

	assert.Equal(t, "https://web.example.com", c.WebURL)
	assert.Equal(t, "9000", c.Port)
	assert.Equal(t, "postgresql://localhost/sd", c.DB)
	assert.Equal(t, "internal", c.Cache)
}

func baseNotifConfig() Config {
	return Config{
		Port:        "8000",
		MetricsPort: DefaultMetricsPort,
		OIDC:        OIDC{Issuer: "https://zitadel.example.com", ClientID: "status-dashboard"},
		RBAC:        RBACConfig{Admins: "sd_admins"},
	}
}

func TestValidateNotifications(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(c *Config)
		expectErr bool
		errSubstr string
	}{
		{
			name:      "disabled skips all notification checks",
			mutate:    func(c *Config) { c.Notifications.Enabled = false },
			expectErr: false,
		},
		{
			name: "enabled with full valid config passes",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled:         true,
					LeaseTimeout:    "60s",
					MaxAttempts:     "5",
					BackoffInterval: "5m",
					SmodEmail:       "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: false,
		},
		{
			name: "enabled without SMTP host fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{Enabled: true, SmodEmail: "support@com.com"}
				c.SMTP = SMTPConfig{Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "SD_SMTP_HOST",
		},
		{
			name: "enabled without any review address fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m"}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "at least one review address",
		},
		{
			name: "malformed smtp from fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m", SmodEmail: "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "not-an-address", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "SD_SMTP_FROM",
		},
		{
			name: "smtp port out of range fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m", SmodEmail: "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "70000", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "SD_SMTP_PORT",
		},
		{
			name: "malformed review address fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m",
					EmailsOperators: "ops@com.com, broken at com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "SD_NOTIFICATIONS_EMAILS_OPERATORS",
		},
		{
			name: "multi-address review lists pass",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m",
					SmodEmail:       "support@com.com",
					EmailsOperators: "ops1@com.com, ops2@com.com",
					EmailsAdmins:    "admin@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: false,
		},
		{
			name: "lease timeout not greater than smtp timeout fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "30s", MaxAttempts: "5", BackoffInterval: "5m", SmodEmail: "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "must be greater than",
		},
		{
			name: "invalid smtp timeout fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m", SmodEmail: "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "notaduration"}
			},
			expectErr: true,
			errSubstr: "SD_SMTP_TIMEOUT",
		},
		{
			name: "non-positive max attempts fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "0", BackoffInterval: "5m", SmodEmail: "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "SD_NOTIFICATIONS_MAX_ATTEMPTS",
		},
		{
			name: "invalid backoff interval fails",
			mutate: func(c *Config) {
				c.Notifications = NotificationsConfig{
					Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "bad", SmodEmail: "support@com.com",
				}
				c.SMTP = SMTPConfig{Host: "smtp.otc", Port: "587", From: "sd@com.com", Timeout: "30s"}
			},
			expectErr: true,
			errSubstr: "SD_NOTIFICATIONS_BACKOFF_INTERVAL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseNotifConfig()
			tc.mutate(&cfg)
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

func TestFillDefaults_Notifications(t *testing.T) {
	t.Run("fills notification timing defaults when enabled", func(t *testing.T) {
		c := &Config{Notifications: NotificationsConfig{Enabled: true}}
		c.FillDefaults()

		assert.Equal(t, DefaultSMTPTimeout, c.SMTP.Timeout)
		assert.Equal(t, DefaultLeaseTimeout, c.Notifications.LeaseTimeout)
		assert.Equal(t, DefaultMaxAttempts, c.Notifications.MaxAttempts)
		assert.Equal(t, DefaultBackoffInterval, c.Notifications.BackoffInterval)
	})

	t.Run("leaves notification timing empty when disabled", func(t *testing.T) {
		c := &Config{Notifications: NotificationsConfig{Enabled: false}}
		c.FillDefaults()

		assert.Empty(t, c.SMTP.Timeout)
		assert.Empty(t, c.Notifications.LeaseTimeout)
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
