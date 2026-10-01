package notification

// Package notification builds and delivers maintenance email notifications.
//
// It is the "notification core" from docs/notifications/architecture.md: it turns
// a maintenance change into one outbox row per recipient (resolver), renders those
// rows into emails (renderer) and sends them over SMTP (sender). Delivery timing,
// claiming and retries live in the storage layer and the worker.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// ActorChecker is the actor recorded for automatic checker transitions.
const ActorChecker = "checker"

// maxBackoff caps the exponential retry delay (architecture §5).
const maxBackoff = 2 * time.Hour

// backoffJitter spreads retries by up to ±20%. Rows usually fail together (one relay
// outage), so without it every retry would hit the recovering server at once.
const backoffJitter = 0.2

// Config is the parsed, ready-to-use notification configuration.
// It is derived from conf.Config once at startup so the hot path never re-parses
// durations, integers or recipient lists.
type Config struct {
	Enabled bool

	From     string
	Host     string
	Port     int
	User     string
	Password string
	TLS      bool
	Timeout  time.Duration

	LeaseTimeout time.Duration
	MaxAttempts  int
	BackoffBase  time.Duration

	ReviewSMOD      string
	ReviewOperators []string
	ReviewAdmins    []string

	// AllowedDomains limits the user-supplied contact_email. Empty allows any domain.
	AllowedDomains []string
	// ExcludedEmails are dropped from every recipient list.
	ExcludedEmails []string

	// BaseURL is the web origin used to build maintenance deep links.
	BaseURL string
}

// ConfigFromConf parses and validates the notification settings from conf.Config.
// conf.Validate has already guaranteed the raw values are well-formed when enabled,
// so parsing here cannot fail for an enabled feature; errors are still surfaced.
func ConfigFromConf(c *conf.Config) (Config, error) {
	out := Config{
		Enabled:         c.Notifications.Enabled,
		From:            c.SMTP.From,
		Host:            c.SMTP.Host,
		User:            c.SMTP.User,
		Password:        c.SMTP.Password,
		TLS:             c.SMTP.TLS,
		ReviewSMOD:      strings.TrimSpace(c.Notifications.SmodEmail),
		ReviewOperators: splitEmails(c.Notifications.EmailsOperators),
		ReviewAdmins:    splitEmails(c.Notifications.EmailsAdmins),
		AllowedDomains:  splitEmails(c.Notifications.AllowedDomains),
		ExcludedEmails:  splitEmails(c.Notifications.ExcludedEmails),
		BaseURL:         strings.TrimRight(c.WebURL, "/"),
	}

	if !c.Notifications.Enabled {
		return out, nil
	}

	port, err := strconv.Atoi(c.SMTP.Port)
	if err != nil {
		return Config{}, fmt.Errorf("invalid SD_SMTP_PORT: %w", err)
	}
	out.Port = port

	if out.Timeout, err = time.ParseDuration(c.SMTP.Timeout); err != nil {
		return Config{}, fmt.Errorf("invalid SD_SMTP_TIMEOUT: %w", err)
	}
	if out.LeaseTimeout, err = time.ParseDuration(c.Notifications.LeaseTimeout); err != nil {
		return Config{}, fmt.Errorf("invalid SD_NOTIFICATIONS_LEASE_TIMEOUT: %w", err)
	}
	if out.BackoffBase, err = time.ParseDuration(c.Notifications.BackoffInterval); err != nil {
		return Config{}, fmt.Errorf("invalid SD_NOTIFICATIONS_BACKOFF_INTERVAL: %w", err)
	}
	if out.MaxAttempts, err = strconv.Atoi(c.Notifications.MaxAttempts); err != nil {
		return Config{}, fmt.Errorf("invalid SD_NOTIFICATIONS_MAX_ATTEMPTS: %w", err)
	}

	return out, nil
}

// KindForStatus maps a resulting maintenance status to a notification kind
// (architecture §1 recipient table).
func KindForStatus(status event.Status) string {
	switch status {
	case event.MaintenancePendingReview:
		return db.NotificationKindPendingReview
	case event.MaintenanceReviewed:
		return db.NotificationKindReviewed
	default:
		return db.NotificationKindStatusChanged
	}
}

// isReviewStatus reports whether the status still needs a human decision and thus
// notifies the review audience in addition to the creator.
func isReviewStatus(status event.Status) bool {
	return status == event.MaintenancePendingReview || status == event.MaintenanceReviewed
}

// Backoff returns a retry-time function for db.MarkFailed: attempt n becomes
// eligible again after base*2^(n-1), capped at maxBackoff and spread by jitter
// (architecture §5).
func Backoff(base time.Duration) func(attempts int) time.Time {
	return func(attempts int) time.Time {
		delay := base
		for i := 1; i < attempts; i++ {
			delay *= 2
			if delay >= maxBackoff {
				delay = maxBackoff
				break
			}
		}
		if delay > maxBackoff {
			delay = maxBackoff
		}

		return time.Now().UTC().Add(withJitter(delay))
	}
}

// withJitter shifts d by a random factor within ±backoffJitter.
func withJitter(d time.Duration) time.Duration {
	spread := (rand.Float64()*2 - 1) * backoffJitter //nolint:gosec // scheduling spread, not security

	return time.Duration(float64(d) * (1 + spread))
}

// splitEmails parses a comma-separated recipient list into normalized addresses.
func splitEmails(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if e := normalizeEmail(p); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// normalizeEmail trims surrounding space and lowercases an address for dedup.
func normalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

// allowedDomain reports whether a user-supplied address may receive notifications.
// An empty allow-list permits every domain. Only the creator address is subject to
// it; the review audience comes from trusted configuration.
func allowedDomain(allowed []string, email string) bool {
	if len(allowed) == 0 {
		return true
	}

	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}

	return slices.Contains(allowed, normalizeEmail(email[at+1:]))
}
