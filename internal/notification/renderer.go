package notification

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

//go:embed templates/subject.tmpl templates/body.tmpl
var templateFS embed.FS

// Email is a rendered message ready to send.
type Email struct {
	Subject string
	Body    string
}

// templateData is the strongly-typed view a template renders against, extracted
// from the outbox row's string payload.
type templateData struct {
	IncidentID string
	Title      string
	OldStatus  string
	NewStatus  string
	Actor      string
	ChangedAt  string
	Link       string
	// State names the subject line state, and Headline the opening sentence. Both are
	// derived here so the templates stay free of status logic.
	State    string
	Headline string
}

// Renderer turns an outbox row into an Email using the embedded templates.
type Renderer struct {
	subject *template.Template
	body    *template.Template
}

// NewRenderer parses the embedded templates once. It fails fast on invalid
// templates so a bad template never reaches the delivery worker.
func NewRenderer() (*Renderer, error) {
	subject, err := template.New("subject.tmpl").ParseFS(templateFS, "templates/subject.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse subject template: %w", err)
	}
	body, err := template.New("body.tmpl").ParseFS(templateFS, "templates/body.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse body template: %w", err)
	}
	return &Renderer{subject: subject, body: body}, nil
}

// Render produces the subject and body for one outbox row from its payload.
func (r *Renderer) Render(row db.NotificationOutbox) (Email, error) {
	oldStatus := payloadString(row.Payload, "old_status")
	newStatus := payloadString(row.Payload, "new_status")
	state, headline := describe(oldStatus, newStatus)

	data := templateData{
		IncidentID: payloadString(row.Payload, "incident_id"),
		Title:      payloadString(row.Payload, "title"),
		OldStatus:  oldStatus,
		NewStatus:  newStatus,
		Actor:      payloadString(row.Payload, "actor"),
		ChangedAt:  payloadString(row.Payload, "changed_at"),
		Link:       payloadString(row.Payload, "link"),
		State:      state,
		Headline:   headline,
	}

	var subject bytes.Buffer
	if err := r.subject.Execute(&subject, data); err != nil {
		return Email{}, fmt.Errorf("render subject: %w", err)
	}
	var body bytes.Buffer
	if err := r.body.Execute(&body, data); err != nil {
		return Email{}, fmt.Errorf("render body: %w", err)
	}

	return Email{
		Subject: strings.TrimSpace(subject.String()),
		Body:    body.String(),
	}, nil
}

// payloadString reads a string field from the JSONB payload, tolerating a missing
// key or a non-string value (returns "").
func payloadString(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}

// describe words the notification: the subject-line state and the opening sentence.
// An empty old status means the maintenance was just created, which must not read as a
// status change.
func describe(oldStatus, newStatus string) (string, string) {
	if oldStatus != "" {
		return newStatus, "changed status"
	}

	if event.Status(newStatus) == event.MaintenancePendingReview {
		return "awaiting review", "has been submitted for review"
	}

	return "scheduled", "has been scheduled"
}
