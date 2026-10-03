package db

import (
	"fmt"
	"time"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

type Component struct {
	ID         uint            `json:"id"`
	Name       string          `json:"name,omitempty"`
	Attrs      []ComponentAttr `json:"attributes,omitempty"`
	Incidents  []*Incident     `json:"incidents,omitempty"`
	CreatedAt  *time.Time      `json:"-"`
	ModifiedAt *time.Time      `json:"-"`
	DeletedAt  *time.Time      `json:"-"`
}

func (c *Component) PrintAttrs() string {
	var category, region, compType string
	for _, a := range c.Attrs {
		switch a.Name {
		case "category":
			category = a.Value
		case regionAttrName:
			region = a.Value
		case "type":
			compType = a.Value
		}
	}
	return fmt.Sprintf("%s (%s, %s, %s)", c.Name, category, region, compType)
}

func (c *Component) Region() string {
	var region string
	for _, a := range c.Attrs {
		if a.Name == regionAttrName {
			region = a.Value
		}
	}

	return region
}

func (c *Component) Type() string {
	var cType string
	for _, a := range c.Attrs {
		if a.Name == "type" {
			cType = a.Value
		}
	}

	return cType
}

const regionAttrName = "region"

type ComponentAttr struct {
	ID          uint   `json:"-"`
	ComponentID uint   `json:"-"`
	Name        string `json:"name"`
	Value       string `json:"value"`
}

// Incident is a db table representation.
type Incident struct {
	ID           uint             `json:"id"`
	Text         *string          `json:"text"`
	Description  *string          `json:"description"`
	StartDate    *time.Time       `json:"start_date"`
	EndDate      *time.Time       `json:"end_date"`
	Impact       *int             `json:"impact"`
	Statuses     []IncidentStatus `json:"updates"`
	Status       event.Status     `json:"status"`
	System       bool             `json:"system"`
	Type         string           `json:"type"`
	Components   []Component      `json:"components"`
	CreatedAt    *time.Time       `json:"created_at,omitempty"`
	ModifiedAt   *time.Time       `json:"modified_at,omitempty"`
	DeletedAt    *time.Time       `json:"deleted_at,omitempty"`
	CreatedBy    *string          `json:"created_by,omitempty"`
	ContactEmail *string          `json:"contact_email,omitempty"`
	Version      *int             `json:"version,omitempty"`
}

func (in *Incident) Link() string {
	return fmt.Sprintf("<a href='/incidents/%d'>%s</a>", in.ID, *in.Text)
}

// IncidentStatus is a db table representation.
type IncidentStatus struct {
	ID         uint         `json:"-"`
	IncidentID uint         `json:"-"`
	Status     event.Status `json:"status"`
	Text       string       `json:"text"`
	Timestamp  time.Time    `json:"timestamp"`
	CreatedAt  *time.Time   `json:"created_at,omitempty"`
	ModifiedAt *time.Time   `json:"modified_at,omitempty"`
	DeletedAt  *time.Time   `json:"deleted_at,omitempty"`
	CreatedBy  *string      `json:"created_by,omitempty"`
	ModifiedBy *string      `json:"modified_by,omitempty"`
}

// NotificationOutbox stores one email task per recipient.
type NotificationOutbox struct {
	ID            uint           `json:"id"`
	Kind          string         `json:"kind"`
	IncidentID    uint           `json:"incident_id"`
	Recipient     string         `json:"recipient"`
	Payload       map[string]any `json:"payload"`
	ChangeID      string         `json:"change_id"`
	DedupKey      string         `json:"dedup_key"`
	Status        string         `json:"status"`
	Attempts      int            `json:"attempts"`
	NextAttemptAt *time.Time     `json:"next_attempt_at"`
	LockedBy      *string        `json:"locked_by"`
	LockedAt      *time.Time     `json:"locked_at"`
	LastError     *string        `json:"last_error"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}
