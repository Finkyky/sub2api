package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type UserRole struct{ ent.Schema }

func (UserRole) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "user_roles"}}
}
func (UserRole) Fields() []ent.Field {
	return []ent.Field{field.Int64("user_id"), field.Int64("role_id")}
}
func (UserRole) Edges() []ent.Edge {
	return []ent.Edge{edge.To("user", User.Type).Field("user_id").Unique().Required(), edge.To("role", Role.Type).Field("role_id").Unique().Required()}
}
func (UserRole) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "role_id").Unique(), index.Fields("role_id")}
}
