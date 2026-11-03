package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type RolePermission struct{ ent.Schema }

func (RolePermission) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "role_permissions"}}
}
func (RolePermission) Fields() []ent.Field {
	return []ent.Field{field.Int64("role_id"), field.Int64("permission_id")}
}
func (RolePermission) Edges() []ent.Edge {
	return []ent.Edge{edge.To("role", Role.Type).Field("role_id").Unique().Required(), edge.To("permission", Permission.Type).Field("permission_id").Unique().Required()}
}
func (RolePermission) Indexes() []ent.Index {
	return []ent.Index{index.Fields("role_id", "permission_id").Unique(), index.Fields("permission_id")}
}
