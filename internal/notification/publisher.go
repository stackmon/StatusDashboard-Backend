package notification

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// Publisher records notification intent: it turns a maintenance Change into outbox
// rows and enqueues them within the caller's transaction, so the email tasks commit
// together with the business change (architecture §3).
type Publisher struct {
	enabled        bool
	resolver       *Resolver
	db             *db.DB
	allowedDomains []string
	// notify wakes the delivery worker after a change commits (hot path). Optional.
	notify func()
}

// NewPublisher builds a Publisher from the parsed config. When cfg.Enabled is false
// the publisher is inert and PublishTx is a no-op.
func NewPublisher(cfg Config, database *db.DB) *Publisher {
	return &Publisher{
		enabled:        cfg.Enabled,
		resolver:       NewResolver(cfg),
		db:             database,
		allowedDomains: cfg.AllowedDomains,
	}
}

// AllowsDomain reports whether a user-supplied address may receive notifications.
// It permits everything when the feature is off or no allow-list is configured, so
// existing installations keep working unchanged.
func (p *Publisher) AllowsDomain(email string) bool {
	if p == nil || !p.enabled {
		return true
	}

	return allowedDomain(p.allowedDomains, email)
}

// AllowedDomains lists the configured domains, for error messages.
func (p *Publisher) AllowedDomains() []string {
	if p == nil {
		return nil
	}

	return p.allowedDomains
}

// SetNotify wires the post-commit wake-up callback (typically Worker.Notify).
func (p *Publisher) SetNotify(fn func()) {
	if p != nil {
		p.notify = fn
	}
}

// Notify signals the delivery worker that new rows may be due. Call it after the
// business transaction commits. Nil-safe and a no-op when disabled or unwired.
func (p *Publisher) Notify() {
	if p == nil || !p.enabled || p.notify == nil {
		return
	}
	p.notify()
}

// Enabled reports whether notifications should be published. It is nil-safe so
// handlers can hold a nil *Publisher when the feature is off.
func (p *Publisher) Enabled() bool {
	return p != nil && p.enabled
}

// PublishTx enqueues one outbox row per recipient for the change, using tx so the
// rows share the business transaction. It is a no-op when disabled or when the
// change resolves to no recipients.
func (p *Publisher) PublishTx(ctx context.Context, tx *gorm.DB, ch Change) error {
	if !p.Enabled() {
		return nil
	}
	rows := p.resolver.BuildRows(ch)
	for i := range rows {
		// A duplicate means this transition was already notified; skip it rather than
		// failing the business transaction that carries the change.
		if err := p.db.Enqueue(ctx, tx, rows[i]); err != nil && !errors.Is(err, db.ErrNotificationDuplicate) {
			return err
		}
	}
	return nil
}
