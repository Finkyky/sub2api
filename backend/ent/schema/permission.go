package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

type Permission struct{ ent.Schema }

func (Permission) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "permissions"}}
}
func (Permission) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (Permission) Fields() []ent.Field {
	return []ent.Field{field.String("code").MaxLen(150).NotEmpty().Unique(), field.String("name").MaxLen(150).NotEmpty(), field.String("resource").MaxLen(100).NotEmpty(), field.String("action").MaxLen(100).NotEmpty(), field.String("description").Optional().Nillable()}
}
func (Permission) Edges() []ent.Edge {
	return []ent.Edge{edge.From("roles", Role.Type).Ref("permissions").Through("role_permissions", RolePermission.Type)}
}
