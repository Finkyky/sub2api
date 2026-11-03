package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

type Role struct{ ent.Schema }

func (Role) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "roles"}}
}
func (Role) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (Role) Fields() []ent.Field {
	return []ent.Field{field.String("code").MaxLen(100).NotEmpty().Unique(), field.String("name").MaxLen(150).NotEmpty(), field.String("description").Optional().Nillable(), field.Bool("is_system").Default(false), field.Bool("is_super_admin").Default(false)}
}
func (Role) Edges() []ent.Edge {
	return []ent.Edge{edge.To("permissions", Permission.Type).Through("role_permissions", RolePermission.Type), edge.From("users", User.Type).Ref("roles").Through("user_roles", UserRole.Type)}
}
func (Role) Indexes() []ent.Index {
	return []ent.Index{index.Fields("is_system"), index.Fields("is_super_admin")}
}
