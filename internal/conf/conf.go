package conf

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
)

const osPref = "SD"
const DevelopMode = "devel"

const (
	DefaultPort            = "8000"
	DefaultOpenAPISpecPath = "openapi.yaml"

	// DefaultRolesClaim is the Zitadel claim carrying the project roles of the
	// authenticated subject.
	DefaultRolesClaim = "urn:zitadel:iam:org:project:roles"

	// MinSecretKeyLength is the minimum required length for the HMAC secret key.
	// HMAC-SHA256 requires at least 32 bytes for cryptographic strength.
	MinSecretKeyLength = 32
)

type Config struct {
	// DB connection uri
	// format is `postgresql://user:pass@host:port/db_name`
	DB string `envconfig:"DB"`
	// Cache connection uri
	// It can be redis format or internal
	Cache string `envconfig:"CACHE"`
	// OIDC settings of the external identity provider (Zitadel)
	OIDC *OIDC `envconfig:"OIDC"`
	// Log level for verbosity
	LogLevel string `envconfig:"LOG_LEVEL"`
	// App port
	Port string `envconfig:"PORT"`
	// Secret key for local HMAC authentication (dev, tests, service-to-service)
	SecretKeyV1 string `envconfig:"SECRET_KEY"`
	// OpenAPISpecPath is the filesystem path to the OpenAPI spec served at
	// /openapi.json. Defaults to "openapi.yaml" (resolved relative to the
	// process working directory, matching the container's WORKDIR layout).
	// Override via SD_OPENAPI_SPEC_PATH for tests or non-standard deployments.
	OpenAPISpecPath string `envconfig:"OPENAPI_SPEC_PATH"`
	// RBAC configuration
	RBAC RBACConfig `envconfig:"RBAC"`
}

type RBACConfig struct {
	// Creators role name
	Creators string `envconfig:"ROLES_CREATORS"`
	// Operators role name
	Operators string `envconfig:"ROLES_OPERATORS"`
	// Admins role name (mandatory)
	Admins string `envconfig:"ROLES_ADMINS"`

	// Deprecated: pre-Zitadel group names, read for one release to keep
	// existing deployments running. Use the ROLES_* variables above.
	GroupsCreators  string `envconfig:"GROUPS_CREATORS"`
	GroupsOperators string `envconfig:"GROUPS_OPERATORS"`
	GroupsAdmins    string `envconfig:"GROUPS_ADMINS"`
}

// OIDC configures the external identity provider. ClientID is the audience the
// resource server accepts: every token must be issued to that client.
type OIDC struct {
	Issuer        string `envconfig:"ISSUER"`
	ClientID      string `envconfig:"CLIENT_ID"`
	RolesClaim    string `envconfig:"ROLES_CLAIM"`
	UsernameClaim string `envconfig:"USERNAME_CLAIM"`
}

func (c *Config) Validate() error {
	p, err := strconv.Atoi(c.Port)
	if err != nil {
		return fmt.Errorf("wrong SD_PORT format, should be a number in range 1025:50000")
	}
	if p < 1024 || p > 50000 {
		return fmt.Errorf("wrong port for http server")
	}

	if provErr := c.validateProviders(); provErr != nil {
		return provErr
	}

	if rbacErr := c.RBAC.Validate(); rbacErr != nil {
		return rbacErr
	}

	return nil
}

// validateProviders ensures at least one authentication provider is configured.
func (c *Config) validateProviders() error {
	hasIssuer := c.OIDC != nil && c.OIDC.Issuer != ""
	hasClientID := c.OIDC != nil && c.OIDC.ClientID != ""

	if hasIssuer != hasClientID {
		return fmt.Errorf("SD_OIDC_ISSUER and SD_OIDC_CLIENT_ID must be configured together")
	}

	hasOIDC := hasIssuer && hasClientID
	hasLocal := c.SecretKeyV1 != ""

	if !hasOIDC && !hasLocal {
		return fmt.Errorf("at least one authentication provider must be configured: " +
			"set SD_OIDC_ISSUER with SD_OIDC_CLIENT_ID for Zitadel or SD_SECRET_KEY for local HMAC")
	}

	if hasLocal && len(c.SecretKeyV1) < MinSecretKeyLength {
		return fmt.Errorf("SD_SECRET_KEY must be at least %d characters for HMAC-SHA256 security", MinSecretKeyLength)
	}

	return nil
}

func (r *RBACConfig) Validate() error {
	if r.Admins == "" {
		return fmt.Errorf("SD_RBAC_ROLES_ADMINS is required")
	}
	return nil
}

// applyLegacyRoleNames copies the deprecated SD_RBAC_GROUPS_* values into the
// role name fields so that deployments keeping the pre-Zitadel variables keep
// working. Explicit SD_RBAC_ROLES_* values always win.
func (r *RBACConfig) applyLegacyRoleNames() {
	if r.Creators == "" {
		r.Creators = r.GroupsCreators
	}

	if r.Operators == "" {
		r.Operators = r.GroupsOperators
	}

	if r.Admins == "" {
		r.Admins = r.GroupsAdmins
	}
}

func (r *RBACConfig) legacyRoleNamesUsed() bool {
	return r.GroupsCreators != "" || r.GroupsOperators != "" || r.GroupsAdmins != ""
}

func (c *Config) FillDefaults() {
	if c.LogLevel == "" {
		c.LogLevel = DevelopMode
	}

	if c.Port == "" {
		c.Port = DefaultPort
	}

	c.RBAC.applyLegacyRoleNames()

	if c.OIDC != nil && c.OIDC.RolesClaim == "" {
		c.OIDC.RolesClaim = DefaultRolesClaim
	}

	if c.OpenAPISpecPath == "" {
		c.OpenAPISpecPath = DefaultOpenAPISpecPath
	}
}

// LoadConf loads configuration from .env file and environment.
// Env variables are preferred.
func LoadConf() (*Config, error) {
	var envMap map[string]string
	envMap, _ = godotenv.Read()

	var c Config
	err := envconfig.Process(osPref, &c)
	if err != nil {
		return nil, err
	}

	if err = mergeConfigs(envMap, &c, osPref); err != nil {
		return nil, err
	}

	c.FillDefaults()

	if err = c.Validate(); err != nil {
		return nil, err
	}

	return &c, nil
}

var ErrInvalidDataMerge = errors.New("could not merge config, the obj must be a point to a struct")

const envConfigTag = "envconfig"

// mergeConfigs allow to merge config params from env variables and .env file.
// It checks the Config struct and if the value is missing, it set up the value from .env file.
func mergeConfigs(env map[string]string, obj any, prefix string) error { //nolint:gocognit
	if env == nil {
		return nil
	}

	v := reflect.ValueOf(obj)

	if v.Kind() != reflect.Ptr {
		return ErrInvalidDataMerge
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return ErrInvalidDataMerge
	}

	t := v.Type()

	// Iterate through the fields
	for i := 0; i < v.NumField(); i++ { //nolint:intrange
		field := t.Field(i)
		value := v.Field(i)

		// Handle pointer to struct (e.g., *OIDC)
		if value.Kind() == reflect.Ptr && value.Elem().Kind() == reflect.Struct {
			envValueTag := field.Tag.Get(envConfigTag)
			confPrefix := fmt.Sprintf("%s_%s", prefix, envValueTag)
			err := mergeConfigs(env, value.Interface(), confPrefix)
			if err != nil {
				return err
			}

			continue
		}

		// Handle embedded struct (e.g., RBACConfig)
		// For struct values (not pointers), we need to pass a pointer
		if value.Kind() == reflect.Struct {
			envValueTag := field.Tag.Get(envConfigTag)
			confPrefix := fmt.Sprintf("%s_%s", prefix, envValueTag)
			err := mergeConfigs(env, value.Addr().Interface(), confPrefix)
			if err != nil {
				return err
			}

			continue
		}

		if value.IsZero() && value.IsValid() && value.CanSet() {
			envValueTag := field.Tag.Get(envConfigTag)
			mapKey := strings.ToUpper(fmt.Sprintf("%s_%s", prefix, envValueTag))

			switch value.Kind() {
			case reflect.String:
				value.SetString(env[mapKey])
			case reflect.Bool:
				if env[mapKey] == "true" {
					value.SetBool(true)
				}
			default:
				return fmt.Errorf("unsupported type for config field %s", field.Name)
			}
		}
	}

	return nil
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return "<hidden>"
}

func sanitizeDBString(dbURL string) string {
	if dbURL == "" {
		return ""
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return dbURL
	}
	return fmt.Sprintf("%s://%s%s",
		u.Scheme,
		u.Host,
		u.Path,
	)
}

func (c *Config) Log(logger *zap.Logger) {
	logger.Info("Application starting with the following configuration:")

	logger.Info("Endpoint configuration",
		zap.String("port", c.Port),
	)

	logger.Info("Authentication configuration",
		zap.Bool("oidc_configured", c.OIDC != nil && c.OIDC.Issuer != ""),
		zap.Bool("local_hmac_configured", c.SecretKeyV1 != ""),
		zap.String("creators_role", c.RBAC.Creators),
		zap.String("operators_role", c.RBAC.Operators),
		zap.String("admins_role", c.RBAC.Admins),
		zap.String("secret_key_v1", maskSecret(c.SecretKeyV1)),
	)

	logger.Info("Storage and logging configuration",
		zap.String("db", sanitizeDBString(c.DB)),
		// zap.String("cache", c.Cache),
		zap.String("log_level", c.LogLevel),
		zap.String("openapi_spec_path", c.OpenAPISpecPath),
	)

	if c.RBAC.legacyRoleNamesUsed() {
		logger.Warn("SD_RBAC_GROUPS_* variables are deprecated, use SD_RBAC_ROLES_* instead",
			zap.String("deprecated_admins", c.RBAC.GroupsAdmins))
	}

	if c.OIDC != nil {
		logger.Info("OIDC configuration",
			zap.String("issuer", c.OIDC.Issuer),
			zap.String("client_id", c.OIDC.ClientID),
			zap.String("roles_claim", c.OIDC.RolesClaim),
			zap.String("username_claim", c.OIDC.UsernameClaim),
		)
	}
}
