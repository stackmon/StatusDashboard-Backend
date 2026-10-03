package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Incident maps the `incident` table.
type Incident struct {
	ent.Schema
}

func (Incident) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "incident"},
	}
}

func (Incident) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			SchemaType(map[string]string{"postgres": "serial"}),
		field.String("text").NotEmpty(),
		field.String("description").Optional().Nillable().
			SchemaType(map[string]string{"postgres": "varchar(1500)"}),
		field.Time("start_date").
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("end_date").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Int("impact").
			SchemaType(map[string]string{"postgres": "smallint"}),
		field.Bool("system").Default(false),
		field.Enum("type").
			Values("incident", "info", "maintenance").
			SchemaType(map[string]string{"postgres": "varchar"}),
		field.String("status").Optional().
			SchemaType(map[string]string{"postgres": "varchar(50)"}),
		field.Time("created_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("modified_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("deleted_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.String("created_by").Optional().Nillable().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
		field.String("contact_email").Optional().Nillable().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
		field.Int("version").Default(1).
			SchemaType(map[string]string{"postgres": "integer"}),
	}
}

func (Incident) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("components", Component.Type).
			StorageKey(
				edge.Table("incident_component_relation"),
				edge.Columns("incident_id", "component_id"),
				edge.Symbols(
					"incident_component_relation_incident_id_fkey",
					"incident_component_relation_component_id_fkey",
				),
			),
		edge.To("notifications", NotificationOutbox.Type).
			StorageKey(edge.Symbol("notification_outbox_incident_id_fkey")),
	}
}

func (Incident) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("id").
			StorageKey("ix_incident_id"),
	}
}
