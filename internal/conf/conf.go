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
)

type Config struct {
	// DB connection uri
	// format is `postgresql://user:pass@host:port/db_name`
	DB string `envconfig:"DB"`
	// Cache connection uri
	// It can be redis format or internal
	Cache string `envconfig:"CACHE"`
	OIDC  OIDC   `envconfig:"OIDC"`
	// Log level for verbosity
	LogLevel string `envconfig:"LOG_LEVEL"`
	// App port
	Port string `envconfig:"PORT"`
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
	// Reporters role name. Machine principals mapped here may only create
	// system incidents via POST /v2/events.
	Reporters string `envconfig:"ROLES_REPORTERS"`
}

// OIDC configures the external identity provider (Zitadel).
type OIDC struct {
	Issuer        string `envconfig:"ISSUER"`
	ClientID      string `envconfig:"CLIENT_ID"`
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

	if c.OIDC.Issuer == "" || c.OIDC.ClientID == "" {
		return fmt.Errorf("SD_OIDC_ISSUER and SD_OIDC_CLIENT_ID are required")
	}

	if rbacErr := c.RBAC.Validate(); rbacErr != nil {
		return rbacErr
	}

	return nil
}

func (r *RBACConfig) Validate() error {
	if r.Admins == "" {
		return fmt.Errorf("SD_RBAC_ROLES_ADMINS is required")
	}
	return nil
}

func (c *Config) FillDefaults() {
	if c.LogLevel == "" {
		c.LogLevel = DevelopMode
	}

	if c.Port == "" {
		c.Port = DefaultPort
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
func mergeConfigs(env map[string]string, obj any, prefix string) error {
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

		// Handle embedded struct (e.g., RBACConfig or OIDC)
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
		zap.String("issuer", c.OIDC.Issuer),
		zap.String("client_id", c.OIDC.ClientID),
		zap.String("username_claim", c.OIDC.UsernameClaim),
		zap.String("creators_role", c.RBAC.Creators),
		zap.String("operators_role", c.RBAC.Operators),
		zap.String("admins_role", c.RBAC.Admins),
		zap.String("reporters_role", c.RBAC.Reporters),
	)

	logger.Info("Storage and logging configuration",
		zap.String("db", sanitizeDBString(c.DB)),
		// zap.String("cache", c.Cache),
		zap.String("log_level", c.LogLevel),
		zap.String("openapi_spec_path", c.OpenAPISpecPath),
	)
}
