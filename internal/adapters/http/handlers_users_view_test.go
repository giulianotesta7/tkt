package httpadapter

import (
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestNormalizeUsersStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want usersStatus
	}{
		{"missing", "", usersStatusAll},
		{"all", "all", usersStatusAll},
		{"active", "active", usersStatusActive},
		{"deactivated", "deactivated", usersStatusDeactivated},
		{"unknown", "unknown", usersStatusAll},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeUsersStatus(tc.raw); got != tc.want {
				t.Fatalf("normalizeUsersStatus(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestBuildUsersIndexData(t *testing.T) {
	t0 := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	users := []domain.User{
		{ID: 2, Name: "Beto Ruiz", Email: "beto@example.com", Role: domain.RoleAgent, Active: false, CreatedAt: t0.Add(time.Hour)},
		{ID: 3, Name: "Mystery", Email: "mystery@example.com", Role: domain.Role("legacy"), Active: true, CreatedAt: t0},
		{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Role: domain.RoleUser, Active: true, CreatedAt: t0},
	}
	data := buildUsersIndexData(domain.User{Role: domain.RoleAdmin}, users, usersStatusActive)
	if data.Status != usersStatusActive || data.ListURL != "/users?status=active" {
		t.Fatalf("status/url = %q/%q", data.Status, data.ListURL)
	}
	if data.Counts != (usersCounts{All: 3, Active: 2, Deactivated: 1}) {
		t.Fatalf("counts = %+v", data.Counts)
	}
	if len(data.Rows) != 2 || data.Rows[0].ID != 3 || data.Rows[1].ID != 1 {
		t.Fatalf("rows did not preserve input order: %+v", data.Rows)
	}
	if row := data.Rows[0]; row.RoleLabel != "Unknown" || row.StatusLabel != "Active" || row.CanManage || row.LauncherID != "" {
		t.Fatalf("invalid row did not fail closed: %+v", row)
	}
	if row := data.Rows[1]; row.Initials != "AT" || row.CreatedAt != t0 || row.EditURL != "/users/1/edit" {
		t.Fatalf("row facts = %+v", row)
	}
}

func TestBuildUsersIndexDataEmptyMessages(t *testing.T) {
	active := []domain.User{{ID: 1, Name: "Ana", Role: domain.RoleUser, Active: true}}
	inactive := []domain.User{{ID: 2, Name: "Beto", Role: domain.RoleUser}}
	for _, tc := range []struct {
		name, want string
		users      []domain.User
		status     usersStatus
	}{
		{"all active", "", active, usersStatusAll},
		{"all deactivated", "", inactive, usersStatusAll},
		{"active match", "", active, usersStatusActive},
		{"deactivated empty", "No deactivated users.", active, usersStatusDeactivated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := buildUsersIndexData(domain.User{Role: domain.RoleRoot}, tc.users, tc.status)
			if data.EmptyMessage != tc.want {
				t.Fatalf("empty message = %q, want %q", data.EmptyMessage, tc.want)
			}
		})
	}
}

func TestUsersRoleOptions(t *testing.T) {
	for _, tc := range []struct {
		actor domain.Role
		want  []domain.Role
	}{
		{domain.RoleAdmin, []domain.Role{domain.RoleUser, domain.RoleAgent}},
		{domain.RoleRoot, []domain.Role{domain.RoleUser, domain.RoleAgent, domain.RoleAdmin}},
		{domain.Role("unknown"), nil},
	} {
		t.Run(string(tc.actor), func(t *testing.T) {
			options := usersRoleOptions(tc.actor)
			if len(options) != len(tc.want) {
				t.Fatalf("got %d options, want %d", len(options), len(tc.want))
			}
			for i, option := range options {
				if option.Value != tc.want[i] {
					t.Fatalf("option %d = %q, want %q", i, option.Value, tc.want[i])
				}
			}
		})
	}
	if got := roleDescription(domain.RoleAdmin); got != "Includes Agent access and user management. Only Root can grant this role." {
		t.Fatalf("admin description = %q", got)
	}
}
