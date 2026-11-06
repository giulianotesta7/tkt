package sqlite

import (
	"context"
	"reflect"
	"testing"
)

// Migration 0045 tests (catalog folders — issue #484, S5, S17): departments
// leave the schema once nothing reads them, and the empty "General › General"
// folders 0043 copied from the seeded department and desk are removed — only
// when empty, so an instance that put anything in them keeps them.

func openPre0045(t *testing.T) *Store {
	t.Helper()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(context.Background(), s.db, migrationsBefore(t, "0045_drop_departments.sql", false)); err != nil {
		t.Fatalf("migrate before 0045: %v", err)
	}
	return s
}

func upgradeTo0045(t *testing.T, s *Store) {
	t.Helper()
	if err := migrate(context.Background(), s.db, migrationsBefore(t, "0045_drop_departments.sql", true)); err != nil {
		t.Fatalf("upgrade through 0045: %v", err)
	}
}

func folderPaths(t *testing.T, s *Store) []string {
	t.Helper()
	var out []string
	for _, r := range folderTree(t, s) {
		out = append(out, r.Path)
	}
	return out
}

func TestMigration0045Shape(t *testing.T) {
	s := newTestDB(t)
	for _, gone := range []string{"departments", "idx_desks_department", "folder_seed_copies"} {
		assertSchemaObjectAbsent(t, s, gone)
	}
	if columnExists(t, s, "desks", "department_id") {
		t.Errorf("desks.department_id still exists")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version = 45`); n != 1 {
		t.Errorf("schema_migrations version 45 rows = %d, want 1", n)
	}
	// A fresh install starts with no folders: the seeded General › General pair
	// was empty, so it is gone. The General desk stays: it is a team.
	if got := folderPaths(t, s); len(got) != 0 {
		t.Errorf("fresh install folders = %q, want none", got)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM desks WHERE name = 'General'`); n != 1 {
		t.Errorf("the General desk must stay, rows = %d", n)
	}
}

func TestMigration0045KeepsGeneralFoldersThatHoldSomething(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup string
		want  []string
	}{
		{
			name:  "a category in General › General keeps both",
			setup: `INSERT INTO categories (name, desk_id, folder_id, created_at) SELECT 'Printers', id, (SELECT f.id FROM folders f JOIN folders p ON p.id = f.parent_id WHERE f.name = 'General' AND p.name = 'General'), '2025-01-01T00:00:00Z' FROM desks WHERE name = 'General'`,
			want:  []string{"General", "General / General"},
		},
		{
			name:  "a category in the top General keeps it and drops the empty child",
			setup: `INSERT INTO categories (name, desk_id, folder_id, created_at) SELECT 'Printers', id, (SELECT id FROM folders WHERE name = 'General' AND parent_id IS NULL), '2025-01-01T00:00:00Z' FROM desks WHERE name = 'General'`,
			want:  []string{"General"},
		},
		{
			name:  "another subfolder keeps the top General and drops the empty child",
			setup: `INSERT INTO folders (parent_id, name, name_key, created_at) SELECT id, 'Hardware', 'hardware', '2025-01-01T00:00:00Z' FROM folders WHERE name = 'General' AND parent_id IS NULL`,
			want:  []string{"General", "General / Hardware"},
		},
		{
			name:  "a subfolder inside General › General keeps both",
			setup: `INSERT INTO folders (parent_id, name, name_key, created_at) SELECT f.id, 'Deep', 'deep', '2025-01-01T00:00:00Z' FROM folders f JOIN folders p ON p.id = f.parent_id WHERE f.name = 'General' AND p.name = 'General'`,
			want:  []string{"General", "General / General", "General / General / Deep"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openPre0045(t)
			mustExec(t, s, tc.setup)
			categories := countRows(t, s, `SELECT COUNT(*) FROM categories`)
			upgradeTo0045(t, s)
			if got := folderPaths(t, s); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("folders = %q, want %q", got, tc.want)
			}
			if after := countRows(t, s, `SELECT COUNT(*) FROM categories`); after != categories {
				t.Errorf("categories %d -> %d", categories, after)
			}
		})
	}
}

func TestMigration0045UpgradeKeepsDesksAndTheirPeople(t *testing.T) {
	s := openPre0045(t)
	mustExec(t, s, `INSERT INTO departments (id, name, description, created_at) VALUES (50, 'IT services', '', '2025-01-01T00:00:00Z');
		INSERT INTO desks (id, name, description, department_id, created_at) VALUES (60, 'Service desk', 'Hardware team', 50, '2025-01-02T00:00:00Z')`)
	agent := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	mustExec(t, s, `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (60, ?, '2025-01-03T00:00:00Z')`, agent)
	mustExec(t, s, `INSERT INTO categories (id, name, desk_id, created_at) VALUES (70, 'Laptop repair', 60, '2025-01-04T00:00:00Z')`)
	suggest := countRows(t, s, `SELECT COUNT(*) FROM category_suggest_fts`)

	upgradeTo0045(t, s)

	var name, description, created string
	if err := s.db.QueryRow(`SELECT name, description, created_at FROM desks WHERE id = 60`).Scan(&name, &description, &created); err != nil {
		t.Fatalf("desk 60: %v", err)
	}
	if name != "Service desk" || description != "Hardware team" || created != "2025-01-02T00:00:00Z" {
		t.Errorf("desk 60 = %q %q %q", name, description, created)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM desk_members WHERE desk_id = 60`); n != 1 {
		t.Errorf("desk members = %d, want 1", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM categories WHERE id = 70 AND desk_id = 60`); n != 1 {
		t.Errorf("the category lost its desk")
	}
	if after := countRows(t, s, `SELECT COUNT(*) FROM category_suggest_fts`); after != suggest {
		t.Errorf("suggestion index rows %d -> %d", suggest, after)
	}
	// Desk writes still work without the column.
	mustExec(t, s, `UPDATE desks SET name = 'Hardware desk' WHERE id = 60`)
	mustExec(t, s, `INSERT INTO desks (name, description, created_at) VALUES ('Identity', '', '2025-02-01T00:00:00Z')`)
}

// S17 removes the folders 0043 COPIED from the seeded pair, never a folder an
// administrator created that merely shares the name.
func TestMigration0045KeepsUserCreatedGeneralNamesakes(t *testing.T) {
	const userTime = "2026-01-01T00:00:00Z"
	t.Run("a General child under a user-created General", func(t *testing.T) {
		s := openPre0045(t)
		// The administrator renamed the copied root, then built their own
		// General › {General, Hardware}, with the seeded description on purpose.
		mustExec(t, s, `UPDATE folders SET name = 'Original', name_key = 'original' WHERE parent_id IS NULL AND name = 'General'`)
		mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, description, created_at) VALUES (NULL, 'General', 'general', 'General ticket requests', ?)`, userTime)
		mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, created_at) SELECT id, 'General', 'general', ? FROM folders WHERE parent_id IS NULL AND name = 'General'`, userTime)
		mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, created_at) SELECT id, 'Hardware', 'hardware', ? FROM folders WHERE parent_id IS NULL AND name = 'General'`, userTime)

		upgradeTo0045(t, s)

		// The renamed copy is the administrator's now and stays; its empty
		// seeded child is still the copy S17 removes.
		want := []string{"General", "General / General", "General / Hardware", "Original"}
		if got := folderPaths(t, s); !reflect.DeepEqual(got, want) {
			t.Errorf("folders = %q, want %q", got, want)
		}
	})
	t.Run("an empty user-created top-level General", func(t *testing.T) {
		s := openPre0045(t)
		// The administrator deleted the seeded pair and created their own.
		mustExec(t, s, `DELETE FROM folders WHERE parent_id IS NOT NULL`)
		mustExec(t, s, `DELETE FROM folders`)
		mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, description, created_at) VALUES (NULL, 'General', 'general', 'General ticket requests', ?)`, userTime)

		upgradeTo0045(t, s)

		if got := folderPaths(t, s); !reflect.DeepEqual(got, []string{"General"}) {
			t.Errorf("folders = %q, want the administrator's General", got)
		}
	})
}

// Even a folder that copies every stored value of the seed — name, place,
// description and created_at, which only a hand-edited database can do — is
// not a seed copy: 0045 removes only the folders 0043 recorded by id.
func TestMigration0045KeepsNamesakeWithMatchingMetadata(t *testing.T) {
	s := openPre0045(t)
	mustExec(t, s, `DELETE FROM folders WHERE parent_id IS NOT NULL`)
	mustExec(t, s, `DELETE FROM folders`)
	mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, description, created_at)
		SELECT NULL, 'General', 'general', description, created_at FROM departments WHERE name = 'General'`)
	mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, description, created_at)
		SELECT f.id, 'General', 'general', ds.description, ds.created_at
		  FROM folders f, desks ds WHERE f.parent_id IS NULL AND lower(ds.name) = 'general'`)

	upgradeTo0045(t, s)

	if got := folderPaths(t, s); !reflect.DeepEqual(got, []string{"General", "General / General"}) {
		t.Errorf("folders = %q, want the administrator's General › General", got)
	}
}
