package db

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

type Component struct {
	ID         uint            `json:"id"`
	Name       string          `json:"name,omitempty"`
	Attrs      []ComponentAttr `json:"attributes,omitempty"`
	Incidents  []*Incident     `json:"incidents,omitempty" gorm:"many2many:incident_component_relation"`
	CreatedAt  *time.Time      `json:"-"`
	ModifiedAt *time.Time      `json:"-"`
	DeletedAt  *time.Time      `json:"-"`
}

func (c *Component) TableName() string {
	return "component"
}

// BeforeCreate GORM hook to set created_at and modified_at.
func (c *Component) BeforeCreate(_ *gorm.DB) error {
	now := time.Now().UTC()
	if c.CreatedAt == nil {
		c.CreatedAt = &now
	}
	if c.ModifiedAt == nil {
		c.ModifiedAt = &now
	}
	return nil
}

// BeforeUpdate GORM hook to set modified_at.
func (c *Component) BeforeUpdate(_ *gorm.DB) error {
	now := time.Now().UTC()
	c.ModifiedAt = &now
	return nil
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

func (c *ComponentAttr) TableName() string {
	return "component_attribute"
}

// Incident is a db table representation.
type Incident struct {
	ID           uint             `json:"id"`
	Text         *string          `json:"text" gorm:"not null"`
	Description  *string          `json:"description" gorm:"type:varchar(1500)"`
	StartDate    *time.Time       `json:"start_date" gorm:"not null"`
	EndDate      *time.Time       `json:"end_date"`
	Impact       *int             `json:"impact" gorm:"not null"`
	Statuses     []IncidentStatus `json:"updates" gorm:"foreignKey:IncidentID"`
	Status       event.Status     `json:"status" gorm:"type:varchar(50)"`
	System       bool             `json:"system" gorm:"not null"`
	Type         string           `json:"type" gorm:"not null"`
	Components   []Component      `json:"components" gorm:"many2many:incident_component_relation"`
	CreatedAt    *time.Time       `json:"created_at,omitempty"`
	ModifiedAt   *time.Time       `json:"modified_at,omitempty"`
	DeletedAt    *time.Time       `json:"deleted_at,omitempty"`
	CreatedBy    *string          `json:"created_by,omitempty" gorm:"type:varchar(255)"`
	ContactEmail *string          `json:"contact_email,omitempty" gorm:"type:varchar(255)"`
	Version      *int             `json:"version,omitempty" gorm:"not null;default:1"`
}

func (in *Incident) TableName() string {
	return "incident"
}

func (in *Incident) Link() string {
	return fmt.Sprintf("<a href='/incidents/%d'>%s</a>", in.ID, *in.Text)
}

// BeforeSave GORM hook to set created_at and modified_at.
func (in *Incident) BeforeSave(_ *gorm.DB) error {
	now := time.Now().UTC()
	if in.CreatedAt == nil {
		in.CreatedAt = &now
	}
	if in.ModifiedAt == nil {
		in.ModifiedAt = &now
	}
	return nil
}

// BeforeUpdate GORM hook to set modified_at.
func (in *Incident) BeforeUpdate(_ *gorm.DB) error {
	now := time.Now().UTC()
	in.ModifiedAt = &now
	return nil
}

// IncidentStatus is a db table representation.
type IncidentStatus struct {
	ID         uint         `json:"-" gorm:"primaryKey;autoIncrement:true;"`
	IncidentID uint         `json:"-"`
	Status     event.Status `json:"status"`
	Text       string       `json:"text"`
	Timestamp  time.Time    `json:"timestamp"`
	CreatedAt  *time.Time   `json:"created_at,omitempty"`
	ModifiedAt *time.Time   `json:"modified_at,omitempty"`
	DeletedAt  *time.Time   `json:"deleted_at,omitempty"`
	CreatedBy  *string      `json:"created_by,omitempty" gorm:"type:varchar(255)"`
	ModifiedBy *string      `json:"modified_by,omitempty" gorm:"type:varchar(255)"`
}

func (is *IncidentStatus) TableName() string {
	return "incident_status"
}

// BeforeSave GORM hook to set created_at and modified_at.
func (is *IncidentStatus) BeforeSave(_ *gorm.DB) error {
	now := time.Now().UTC()
	if is.CreatedAt == nil {
		is.CreatedAt = &now
	}
	if is.ModifiedAt == nil {
		is.ModifiedAt = &now
	}
	return nil
}

// BeforeUpdate GORM hook to set modified_at.
func (is *IncidentStatus) BeforeUpdate(_ *gorm.DB) error {
	now := time.Now().UTC()
	is.ModifiedAt = &now
	return nil
}

// NotificationOutbox stores one email task per recipient.
type NotificationOutbox struct {
	ID            uint           `json:"id" gorm:"primaryKey;autoIncrement:true"`
	Kind          string         `json:"kind" gorm:"type:varchar(64);not null"`
	IncidentID    uint           `json:"incident_id" gorm:"not null"`
	Recipient     string         `json:"recipient" gorm:"type:varchar(255);not null"`
	Payload       map[string]any `json:"payload" gorm:"type:jsonb;not null;serializer:json"`
	ChangeID      string         `json:"change_id" gorm:"type:uuid;not null"`
	DedupKey      string         `json:"dedup_key" gorm:"column:dedup_key;type:varchar(255);not null;uniqueIndex"`
	Status        string         `json:"status" gorm:"type:varchar(20);not null;default:pending"`
	Attempts      int            `json:"attempts" gorm:"not null;default:0"`
	NextAttemptAt *time.Time     `json:"next_attempt_at" gorm:"type:timestamptz"`
	LockedBy      *string        `json:"locked_by" gorm:"type:varchar(255)"`
	LockedAt      *time.Time     `json:"locked_at" gorm:"type:timestamptz"`
	LastError     *string        `json:"last_error" gorm:"type:text"`
	CreatedAt     time.Time      `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     time.Time      `json:"updated_at" gorm:"autoUpdateTime"`
}

func (no *NotificationOutbox) TableName() string {
	return "notification_outbox"
}
