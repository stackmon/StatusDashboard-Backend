package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IncidentStatus maps the `incident_status` table.
//
// Note: production has NO foreign key on incident_status.incident_id, so
// incident_id is kept as a plain column (no edge) to match the production DDL.
type IncidentStatus struct {
	ent.Schema
}

func (IncidentStatus) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "incident_status"},
	}
}

func (IncidentStatus) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			SchemaType(map[string]string{"postgres": "serial"}),
		field.Int("incident_id").Optional().
			SchemaType(map[string]string{"postgres": "integer"}),
		field.Time("timestamp").
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.String("text").NotEmpty(),
		field.String("status").NotEmpty(),
		field.Time("created_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("modified_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("deleted_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.String("created_by").Optional().Nillable().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
		field.String("modified_by").Optional().Nillable().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
	}
}

func (IncidentStatus) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("id").
			StorageKey("ix_incident_status_id"),
		index.Fields("incident_id").
			StorageKey("ix_incident_status_incident_id"),
		index.Fields("incident_id", "timestamp").
			StorageKey("idx_incident_status_incident_id_timestamp"),
	}
}
