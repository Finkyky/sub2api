package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/migrate"
	"github.com/Wei-Shaw/sub2api/migrations"
)

func TestRBACMigrationMatchesEntJoinPrimaryKeys(t *testing.T) {
	source, err := migrations.FS.ReadFile("250_add_rbac_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(source)
	for _, tc := range []struct {
		table   string
		primary string
		columns int
		pair    string
	}{
		{"role_permissions", migrate.RolePermissionsTable.PrimaryKey[0].Name, len(migrate.RolePermissionsColumns), "UNIQUE (role_id, permission_id)"},
		{"user_roles", migrate.UserRolesTable.PrimaryKey[0].Name, len(migrate.UserRolesColumns), "UNIQUE (user_id, role_id)"},
	} {
		start := strings.Index(sql, "CREATE TABLE IF NOT EXISTS "+tc.table+" (")
		if start < 0 {
			t.Fatalf("missing %s", tc.table)
		}
		end := strings.Index(sql[start:], ");")
		if end < 0 {
			t.Fatalf("unterminated %s", tc.table)
		}
		definition := sql[start : start+end]
		if tc.primary != "id" || tc.columns != 3 {
			t.Fatalf("unexpected generated Ent shape for %s: primary=%s columns=%d", tc.table, tc.primary, tc.columns)
		}
		if !strings.Contains(definition, "id BIGSERIAL PRIMARY KEY") {
			t.Errorf("%s migration is missing Ent's generated id primary key", tc.table)
		}
		if !strings.Contains(definition, tc.pair) {
			t.Errorf("%s is missing unique assignment pair", tc.table)
		}
	}
}
