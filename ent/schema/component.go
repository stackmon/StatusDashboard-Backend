package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Component maps the `component` table.
type Component struct {
	ent.Schema
}

func (Component) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "component"},
	}
}

func (Component) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			SchemaType(map[string]string{"postgres": "serial"}),
		field.String("name").NotEmpty(),
		field.Time("created_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("modified_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
		field.Time("deleted_at").Optional().
			SchemaType(map[string]string{"postgres": "timestamp"}),
	}
}

func (Component) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("attributes", ComponentAttr.Type).
			StorageKey(edge.Symbol("component_attribute_component_id_fkey")).
			Annotations(entsql.OnDelete(entsql.NoAction)),
		edge.From("incidents", Incident.Type).
			Ref("components"),
	}
}

func (Component) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("id").
			StorageKey("ix_component_id"),
	}
}
