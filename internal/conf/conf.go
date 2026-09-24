package conf

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

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

// Static proxy defaults. The byte budget keeps a wide margin below the memory
// limit of the deployment that runs this process.
const (
	DefaultStaticCacheTTL      = "5m"
	DefaultStaticCacheMaxBytes = "67108864"
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
	// Static site proxy and cache
	Static Static `envconfig:"STATIC"`
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

// Static configures the proxy that serves the status dashboard static site from
// the OBS website endpoints for every path the API does not own.
type Static struct {
	// Origins are the OBS website endpoints, primary first, each one a bare
	// hostname without scheme or path. An empty list disables the proxy.
	Origins string `envconfig:"ORIGINS"`
	// CacheTTL applies when an origin response carries no usable Cache-Control.
	CacheTTL string `envconfig:"CACHE_TTL"`
	// CacheMaxBytes bounds the total size of the in-memory response cache.
	CacheMaxBytes string `envconfig:"CACHE_MAX_BYTES"`
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

	if staticErr := c.Static.Validate(); staticErr != nil {
		return staticErr
	}

	return nil
}

func (r *RBACConfig) Validate() error {
	if r.Admins == "" {
		return fmt.Errorf("SD_RBAC_ROLES_ADMINS is required")
	}
	return nil
}

// Validate rejects a static proxy configuration the proxy cannot use.
func (s *Static) Validate() error {
	origins, err := s.OriginList()
	if err != nil {
		return err
	}

	// A proxy without origins is disabled, so the cache settings are unused.
	if len(origins) == 0 {
		return nil
	}

	if _, err = s.TTL(); err != nil {
		return err
	}

	_, err = s.MaxBytes()

	return err
}

// OriginList returns the OBS website endpoints in failover order. The list is
// empty when the static proxy is disabled.
func (s *Static) OriginList() ([]string, error) {
	fields := strings.Split(s.Origins, ",")

	origins := make([]string, 0, len(fields))

	for _, field := range fields {
		origin := strings.TrimSpace(field)
		if origin == "" {
			continue
		}

		if strings.ContainsAny(origin, "/?#@: \t") {
			return nil, fmt.Errorf(
				"wrong SD_STATIC_ORIGINS entry %q, expected a bare hostname without scheme, path or port",
				origin)
		}

		origins = append(origins, origin)
	}

	return origins, nil
}

// TTL is the cache lifetime applied to responses that carry no Cache-Control.
func (s *Static) TTL() (time.Duration, error) {
	ttl, err := time.ParseDuration(s.CacheTTL)
	if err != nil {
		return 0, fmt.Errorf("wrong SD_STATIC_CACHE_TTL format, expected a Go duration such as 5m: %w", err)
	}

	if ttl <= 0 {
		return 0, fmt.Errorf("wrong SD_STATIC_CACHE_TTL value, expected a positive duration")
	}

	return ttl, nil
}

// MaxBytes is the size of the whole response cache.
func (s *Static) MaxBytes() (int64, error) {
	maxBytes, err := strconv.ParseInt(s.CacheMaxBytes, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("wrong SD_STATIC_CACHE_MAX_BYTES format, expected a number of bytes: %w", err)
	}

	if maxBytes <= 0 {
		return 0, fmt.Errorf("wrong SD_STATIC_CACHE_MAX_BYTES value, expected a positive byte budget")
	}

	return maxBytes, nil
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

	if c.Static.CacheTTL == "" {
		c.Static.CacheTTL = DefaultStaticCacheTTL
	}

	if c.Static.CacheMaxBytes == "" {
		c.Static.CacheMaxBytes = DefaultStaticCacheMaxBytes
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

	logger.Info("Static site configuration",
		zap.String("origins", c.Static.Origins),
		zap.String("cache_ttl", c.Static.CacheTTL),
		zap.String("cache_max_bytes", c.Static.CacheMaxBytes),
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
