package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NotificationOutbox maps the `notification_outbox` table.
//
// Introduced by migration 000008 (not yet applied to production, which is at
// version 7). The DDL below matches that migration exactly, including the
// partial indexes.
type NotificationOutbox struct {
	ent.Schema
}

func (NotificationOutbox) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "notification_outbox"},
	}
}

func (NotificationOutbox) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			SchemaType(map[string]string{"postgres": "serial"}),
		field.String("kind").NotEmpty().
			SchemaType(map[string]string{"postgres": "varchar(64)"}),
		field.Int("incident_id").
			SchemaType(map[string]string{"postgres": "integer"}),
		field.String("recipient").NotEmpty().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
		field.JSON("payload", map[string]any{}).
			SchemaType(map[string]string{"postgres": "jsonb"}),
		field.String("change_id").NotEmpty().
			SchemaType(map[string]string{"postgres": "uuid"}),
		field.String("dedup_key").NotEmpty().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
		field.String("status").Default("pending").
			SchemaType(map[string]string{"postgres": "varchar(20)"}),
		field.Int("attempts").Default(0).
			SchemaType(map[string]string{"postgres": "integer"}),
		field.Time("next_attempt_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamptz"}),
		field.String("locked_by").Optional().
			SchemaType(map[string]string{"postgres": "varchar(255)"}),
		field.Time("locked_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamptz"}),
		field.Text("last_error").Optional(),
		field.Time("created_at").
			Annotations(entsql.DefaultExpr("NOW()")).
			SchemaType(map[string]string{"postgres": "timestamptz"}),
		field.Time("updated_at").
			Annotations(entsql.DefaultExpr("NOW()")).
			SchemaType(map[string]string{"postgres": "timestamptz"}),
	}
}

func (NotificationOutbox) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).
			Ref("notifications").
			Field("incident_id").
			Unique().
			Required(),
	}
}

func (NotificationOutbox) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("dedup_key").
			Unique().
			StorageKey("idx_outbox_dedup"),
		index.Fields("next_attempt_at").
			StorageKey("idx_outbox_dispatch").
			Annotations(entsql.IndexWhere("status = 'pending'")),
		index.Fields("locked_at").
			StorageKey("idx_outbox_stale_processing").
			Annotations(entsql.IndexWhere("status = 'processing'")),
		index.Fields("updated_at").
			StorageKey("idx_outbox_retention").
			Annotations(entsql.IndexWhere("status = 'sent'")),
	}
}
