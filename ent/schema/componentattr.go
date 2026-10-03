package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ComponentAttr maps the `component_attribute` table.
type ComponentAttr struct {
	ent.Schema
}

func (ComponentAttr) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "component_attribute"},
	}
}

func (ComponentAttr) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").
			SchemaType(map[string]string{"postgres": "serial"}),
		field.Int("component_id").Optional().
			SchemaType(map[string]string{"postgres": "integer"}),
		field.String("name").NotEmpty(),
		field.String("value").NotEmpty(),
	}
}

func (ComponentAttr) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("component", Component.Type).
			Ref("attributes").
			Field("component_id").
			Unique(),
	}
}

func (ComponentAttr) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("id").
			StorageKey("ix_component_attribute_id"),
		index.Fields("component_id").
			StorageKey("ix_component_attribute_component_id"),
		index.Fields("component_id", "name").
			Unique().
			StorageKey("unique_component_attribute"),
	}
}
