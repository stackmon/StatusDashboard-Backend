package conf

import (
	"errors"
	"fmt"
	"net/mail"
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
	DefaultMetricsPort     = "9090"
	DefaultOpenAPISpecPath = "openapi.yaml"

	// MaxPortNumber is the highest valid TCP port.
	MaxPortNumber = 65535
)

// Notification delivery defaults, applied when notifications are enabled.
const (
	DefaultSMTPTimeout     = "30s"
	DefaultLeaseTimeout    = "60s"
	DefaultMaxAttempts     = "5"
	DefaultBackoffInterval = "5m"
)

// Static proxy defaults. The byte budget keeps a wide margin below the memory
// limit of the deployment that runs this process.
const (
	DefaultStaticCacheTTL      = "5m"
	DefaultStaticCacheMaxBytes = "67108864"
)

type Config struct {
	// Single-word fields below carry no envconfig tag on purpose: envconfig falls back
	// to the bare tag name when the prefixed variable is unset, so a tag of "HOSTNAME"
	// would inherit the container's $HOSTNAME. Field names yield the same SD_* keys.

	// DB connection uri
	// format is `postgresql://user:pass@host:port/db_name`
	DB string
	// Cache connection uri
	// It can be redis format or internal
	Cache string `envconfig:"CACHE"`
	OIDC  OIDC   `envconfig:"OIDC"`
	// Log level for verbosity
	LogLevel string `envconfig:"LOG_LEVEL"`
	// App port
	Port string `envconfig:"PORT"`
	// MetricsPort serves /metrics on its own listener so the queue telemetry is not
	// reachable from the public API port.
	MetricsPort string `envconfig:"METRICS_PORT"`
	// Web URL for the app, used to build maintenance deep links in notifications.
	// Example: https://web.example.com
	WebURL string `envconfig:"WEB_URL"`
	// OpenAPISpecPath is the filesystem path to the OpenAPI spec served at
	// /openapi.json. Defaults to "openapi.yaml" (resolved relative to the
	// process working directory, matching the container's WORKDIR layout).
	// Override via SD_OPENAPI_SPEC_PATH for tests or non-standard deployments.
	OpenAPISpecPath string `envconfig:"OPENAPI_SPEC_PATH"`
	// RBAC configuration
	RBAC RBACConfig `envconfig:"RBAC"`
	// SMTP transport settings for outgoing mail
	SMTP SMTPConfig `envconfig:"SMTP"`
	// Notifications feature settings
	Notifications NotificationsConfig `envconfig:"NOTIFICATIONS"`
	// Static site proxy and cache
	Static Static `envconfig:"STATIC"`
}

// SMTPConfig holds the direct OTC SMTP transport settings.
//
// No envconfig tags: a tag like "USER" makes envconfig fall back to the shell's $USER
// when SD_SMTP_USER is unset. Field names yield the same keys without that fallback.
type SMTPConfig struct {
	Host     string
	Port     string
	From     string
	User     string
	Password string
	TLS      bool
	// Timeout is a Go duration string (e.g. "30s") for the SMTP connect/send.
	Timeout string
}

// NotificationsConfig holds the maintenance email notification settings.
type NotificationsConfig struct {
	// Enabled is the master on/off switch. Untagged for the same reason as SMTPConfig.
	Enabled bool
	// LeaseTimeout is a Go duration string; must exceed the SMTP timeout.
	LeaseTimeout string `envconfig:"LEASE_TIMEOUT"`
	// MaxAttempts is the finite retry limit before a row is marked failed.
	MaxAttempts string `envconfig:"MAX_ATTEMPTS"`
	// BackoffInterval is the base delay (Go duration string) for retry backoff.
	BackoffInterval string `envconfig:"BACKOFF_INTERVAL"`
	// SmodEmail is the fixed SMOD team review recipient.
	SmodEmail string `envconfig:"SMOD_EMAIL"`
	// EmailsOperators is the review recipient list for the Operator role.
	EmailsOperators string `envconfig:"EMAILS_OPERATORS"`
	// EmailsAdmins is the review recipient list for the Admin role.
	EmailsAdmins string `envconfig:"EMAILS_ADMINS"`
	// AllowedDomains restricts the user-supplied contact_email to these domains,
	// comma-separated. Empty means any domain is accepted.
	AllowedDomains string `envconfig:"ALLOWED_DOMAINS"`
	// ExcludedEmails never receive notifications, comma-separated. Applied to every
	// recipient so an exclusion cannot be bypassed via contact_email.
	ExcludedEmails string `envconfig:"EXCLUDED_EMAILS"`
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

	if err = c.validateMetricsPort(p); err != nil {
		return err
	}

	if c.OIDC.Issuer == "" || c.OIDC.ClientID == "" {
		return fmt.Errorf("SD_OIDC_ISSUER and SD_OIDC_CLIENT_ID are required")
	}

	if rbacErr := c.RBAC.Validate(); rbacErr != nil {
		return rbacErr
	}

	if notifErr := c.validateNotifications(); notifErr != nil {
		return notifErr
	}

	if staticErr := c.Static.Validate(); staticErr != nil {
		return staticErr
	}

	return nil
}

// validateMetricsPort keeps the metrics listener on its own port; sharing apiPort
// would put the queue telemetry back on the public API. An empty value is left to
// FillDefaults, which LoadConf runs before validating.
func (c *Config) validateMetricsPort(apiPort int) error {
	if c.MetricsPort == "" {
		return nil
	}

	p, err := strconv.Atoi(c.MetricsPort)
	if err != nil || p < 1024 || p > MaxPortNumber {
		return fmt.Errorf("wrong SD_METRICS_PORT format, should be a number in range 1024:%d", MaxPortNumber)
	}
	if p == apiPort {
		return fmt.Errorf("SD_METRICS_PORT must differ from SD_PORT")
	}

	return nil
}

// validateNotifications enforces SMTP and review-audience requirements when
// notifications are enabled. When disabled, the feature stays inert and no
// notification settings are required.
func (c *Config) validateNotifications() error {
	if !c.Notifications.Enabled {
		return nil
	}

	if err := c.validateSMTP(); err != nil {
		return err
	}

	if err := c.validateReviewAudience(); err != nil {
		return err
	}

	smtpTimeout, err := time.ParseDuration(c.SMTP.Timeout)
	if err != nil {
		return fmt.Errorf("invalid SD_SMTP_TIMEOUT: %w", err)
	}

	leaseTimeout, err := time.ParseDuration(c.Notifications.LeaseTimeout)
	if err != nil {
		return fmt.Errorf("invalid SD_NOTIFICATIONS_LEASE_TIMEOUT: %w", err)
	}

	if leaseTimeout <= smtpTimeout {
		return fmt.Errorf("SD_NOTIFICATIONS_LEASE_TIMEOUT (%s) must be greater than SD_SMTP_TIMEOUT (%s)",
			leaseTimeout, smtpTimeout)
	}

	attempts, err := strconv.Atoi(c.Notifications.MaxAttempts)
	if err != nil || attempts < 1 {
		return fmt.Errorf("SD_NOTIFICATIONS_MAX_ATTEMPTS must be a positive integer")
	}

	if _, parseErr := time.ParseDuration(c.Notifications.BackoffInterval); parseErr != nil {
		return fmt.Errorf("invalid SD_NOTIFICATIONS_BACKOFF_INTERVAL: %w", parseErr)
	}

	return nil
}

// validateSMTP checks the transport settings. The sender address is parsed here
// because a malformed From is rejected by the relay on every single message.
func (c *Config) validateSMTP() error {
	if c.SMTP.Host == "" || c.SMTP.Port == "" || c.SMTP.From == "" {
		return fmt.Errorf("notifications enabled: SD_SMTP_HOST, SD_SMTP_PORT and SD_SMTP_FROM are required")
	}

	port, err := strconv.Atoi(c.SMTP.Port)
	if err != nil || port < 1 || port > MaxPortNumber {
		return fmt.Errorf("SD_SMTP_PORT must be a number in range 1:%d", MaxPortNumber)
	}

	if _, err = mail.ParseAddress(c.SMTP.From); err != nil {
		return fmt.Errorf("invalid SD_SMTP_FROM %q: %w", c.SMTP.From, err)
	}

	return nil
}

// validateReviewAudience requires at least one review address and rejects malformed
// ones: unlike contact_email these come from the operator, so a typo would silently
// break every review notification.
func (c *Config) validateReviewAudience() error {
	if c.Notifications.SmodEmail == "" &&
		c.Notifications.EmailsOperators == "" &&
		c.Notifications.EmailsAdmins == "" {
		return fmt.Errorf("notifications enabled: at least one review address must be set " +
			"(SD_NOTIFICATIONS_SMOD_EMAIL, SD_NOTIFICATIONS_EMAILS_OPERATORS or SD_NOTIFICATIONS_EMAILS_ADMINS)")
	}

	lists := map[string]string{
		"SD_NOTIFICATIONS_SMOD_EMAIL":       c.Notifications.SmodEmail,
		"SD_NOTIFICATIONS_EMAILS_OPERATORS": c.Notifications.EmailsOperators,
		"SD_NOTIFICATIONS_EMAILS_ADMINS":    c.Notifications.EmailsAdmins,
		"SD_NOTIFICATIONS_EXCLUDED_EMAILS":  c.Notifications.ExcludedEmails,
	}
	for envName, raw := range lists {
		if err := validateEmailList(envName, raw); err != nil {
			return err
		}
	}

	return validateDomainList("SD_NOTIFICATIONS_ALLOWED_DOMAINS", c.Notifications.AllowedDomains)
}

// validateDomainList checks a comma-separated domain list. Entries are bare domains,
// so a stray "@" usually means a full address was pasted in by mistake.
func validateDomainList(envName, raw string) error {
	for _, part := range strings.Split(raw, ",") {
		domain := strings.TrimSpace(part)
		if domain == "" {
			continue
		}
		if strings.ContainsAny(domain, "@ ") || !strings.Contains(domain, ".") {
			return fmt.Errorf("%s contains an invalid domain %q, expected e.g. \"example.com\"", envName, domain)
		}
	}

	return nil
}

// validateEmailList parses a comma-separated recipient list, mirroring how
// notification.splitEmails will later read it.
func validateEmailList(envName, raw string) error {
	for _, part := range strings.Split(raw, ",") {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("%s contains an invalid address %q: %w", envName, addr, err)
		}
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

	if c.MetricsPort == "" {
		c.MetricsPort = DefaultMetricsPort
	}

	if c.OpenAPISpecPath == "" {
		c.OpenAPISpecPath = DefaultOpenAPISpecPath
	}

	if c.Notifications.Enabled {
		if c.SMTP.Timeout == "" {
			c.SMTP.Timeout = DefaultSMTPTimeout
		}
		if c.Notifications.LeaseTimeout == "" {
			c.Notifications.LeaseTimeout = DefaultLeaseTimeout
		}
		if c.Notifications.MaxAttempts == "" {
			c.Notifications.MaxAttempts = DefaultMaxAttempts
		}
		if c.Notifications.BackoffInterval == "" {
			c.Notifications.BackoffInterval = DefaultBackoffInterval
		}
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

// envKeyPart mirrors envconfig's key derivation: the tag when present, the field
// name otherwise. Untagged fields are how we avoid envconfig's bare-name fallback.
func envKeyPart(field reflect.StructField) string {
	if tag := field.Tag.Get(envConfigTag); tag != "" {
		return tag
	}

	return field.Name
}

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
			confPrefix := fmt.Sprintf("%s_%s", prefix, envKeyPart(field))
			err := mergeConfigs(env, value.Addr().Interface(), confPrefix)
			if err != nil {
				return err
			}

			continue
		}

		if value.IsZero() && value.IsValid() && value.CanSet() {
			mapKey := strings.ToUpper(fmt.Sprintf("%s_%s", prefix, envKeyPart(field)))

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

// maskSecret hides a secret value in logs, keeping an empty value empty.
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

	logger.Info("Notifications configuration",
		zap.Bool("enabled", c.Notifications.Enabled),
		zap.String("smtp_host", c.SMTP.Host),
		zap.String("smtp_port", c.SMTP.Port),
		zap.String("smtp_from", c.SMTP.From),
		zap.String("smtp_user", c.SMTP.User),
		zap.String("smtp_password", maskSecret(c.SMTP.Password)),
		zap.Bool("smtp_tls", c.SMTP.TLS),
	)
}
