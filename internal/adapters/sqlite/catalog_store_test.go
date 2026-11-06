package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestCatalogStoreSearchCatalogMatchesNameDescriptionNotDesk(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	support := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, support); err != nil {
		t.Fatal(err)
	}
	platform := &domain.Desk{Name: "Platform", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, platform); err != nil {
		t.Fatal(err)
	}
	// Desk names are never matched: a category names no desk.
	requests := &domain.Category{Name: "Requests", Description: "General asks", CreatedAt: testClock}
	createCategory(t, store, requests)
	deploys := &domain.Category{Name: "Deploys", Description: "Release trains", CreatedAt: testClock}
	createCategory(t, store, deploys)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{"Deploys", "Requests"}},
		{"REQUESTS", []string{"Requests"}},
		{"release", []string{"Deploys"}},
		{"platform", nil},
		{"support", nil},
		{"nothing", nil},
	} {
		got, err := store.CatalogStore().SearchCatalog(ctx, tc.query)
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		var names []string
		for _, c := range got {
			names = append(names, c.Name)
			if c.ID == requests.ID && c.Description != "General asks" {
				t.Fatalf("search %q description = %q, want General asks", tc.query, c.Description)
			}
		}
		if strings.Join(names, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("search %q = %v, want %v", tc.query, names, tc.want)
		}
	}
}

func TestCatalogStoreRejectsDuplicateDeskNames(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	first := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	duplicate := &domain.Desk{Name: first.Name, CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, duplicate); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate desk error = %v", err)
	}
}

// Desk category counts describe current published workflow targets (#491),
// including published workflows whose desk has lost its staffing.
func TestCatalogStoreOfferedCategoryCount(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	desk := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, desk); err != nil {
		t.Fatal(err)
	}
	// The desk is staffed first so the assignment workflow passes the publish
	// gate; staffing is then removed to reach the published-but-unrunnable
	// state, which the picker still lists with its reason.
	agent := seedUserRaw(t, store, "Support Agent", "support-agent@tkt.test", "agent")
	if _, err := store.db.ExecContext(ctx, `INSERT INTO desk_members(desk_id, user_id, created_at) VALUES(?, ?, ?)`, desk.ID, agent, formatTime(testClock)); err != nil {
		t.Fatalf("staff desk: %v", err)
	}
	published := &domain.Category{Name: "Requests", Description: "Requests", CreatedAt: testClock}
	createCategory(t, store, published)
	deskCounts := func() (total, offered int) {
		t.Helper()
		desks, err := catalog.(application.CatalogTreeStore).ListTreeDesks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range desks {
			if d.ID == desk.ID {
				return d.CategoryCount, d.OfferedCategoryCount
			}
		}
		t.Fatalf("desk %d missing from %+v", desk.ID, desks)
		return 0, 0
	}

	// An unpublished category does not route to any desk.
	if total, offered := deskCounts(); total != 0 || offered != 0 {
		t.Fatalf("unpublished desk counts = total %d/offered %d, want 0/0", total, offered)
	}

	// Publishing the category routes it to exactly one desk.
	canon := mustCanon(t, domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim},
	}})
	if _, issues, err := store.WorkflowStore().Publish(ctx, published.ID, canon, nil); err != nil || len(issues) != 0 {
		t.Fatalf("publish = %v, issues %v", err, issues)
	}
	if total, offered := deskCounts(); total != 1 || offered != 1 {
		t.Fatalf("published desk counts = total %d/offered %d, want 1/1", total, offered)
	}

	// Removing the desk's members makes the published category unrunnable while
	// it stays published. The picker still lists it (marked with its reason), so
	// the offered count must not drop it.
	if _, err := store.db.ExecContext(ctx, `DELETE FROM desk_members WHERE desk_id=?`, desk.ID); err != nil {
		t.Fatalf("empty the desk: %v", err)
	}
	if total, offered := deskCounts(); total != 1 || offered != 1 {
		t.Fatalf("unrunnable desk counts = total %d/offered %d, want 1/1", total, offered)
	}

	// A draft-only category changes neither count.
	draftOnly := &domain.Category{Name: "Drafts", Description: "Drafts", CreatedAt: testClock}
	createCategory(t, store, draftOnly)
	if err := store.WorkflowStore().UpsertDraft(ctx, draftOnly.ID, mustCanon(t, domain.WorkflowDefinition{{
		Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "draft only"},
	}})); err != nil {
		t.Fatalf("upsert draft: %v", err)
	}
	if total, offered := deskCounts(); total != 1 || offered != 1 {
		t.Fatalf("draft-only desk counts = total %d/offered %d, want 1/1", total, offered)
	}
}

// The catalog tree (issue #387, S8) renders every desk with two facts no
// existing projection carries — how many agents can act on its tickets and how
// much work is open in it — and ONE read has to answer both for every desk.
//
// RED: the projection does not exist yet.
func TestCatalogStoreListTreeDesksCountsAgentsAndOpenTickets(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	support := &domain.Desk{Name: "Support", Description: "First line", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, support); err != nil {
		t.Fatalf("create support desk: %v", err)
	}
	platform := &domain.Desk{Name: "Platform", Description: "Infrastructure", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, platform); err != nil {
		t.Fatalf("create platform desk: %v", err)
	}
	// Agent counting is the workflow publish gate's predicate: active, and agent
	// or above. An inactive member is still a member of the desk but can never
	// act, so it must not be counted. (The membership triggers already refuse to
	// add a plain requester, so inactivity is the state worth proving here.)
	activeAgent := seedUserRole(t, store, "Ana Agent", "ana@tree.test", true, domain.RoleAgent)
	activeAdmin := seedUserRole(t, store, "Ada Admin", "ada@tree.test", true, domain.RoleAdmin)
	inactiveAgent := seedUserRole(t, store, "Gone Agent", "gone@tree.test", false, domain.RoleAgent)
	for _, userID := range []int64{activeAgent, activeAdmin, inactiveAgent} {
		if err := store.DeskStore().AddMember(ctx, support.ID, userID, testClock); err != nil {
			t.Fatalf("add member %d: %v", userID, err)
		}
	}

	supportCategory := &domain.Category{Name: "VPN", Description: "Remote access", CreatedAt: testClock}
	createCategory(t, store, supportCategory)
	platformCategory := &domain.Category{Name: "Deploys", CreatedAt: testClock}
	createCategory(t, store, platformCategory)
	// "Open" is the same predicate the workflow summary uses: everything not
	// closed and not cancelled. A resolved ticket is still work somebody owes.
	seedSearchTicket(t, store, 1, "VPN down", "", domain.StateNew, domain.PriorityHigh, supportCategory.ID)
	seedSearchTicket(t, store, 2, "VPN flaky", "", domain.StateInProgress, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 3, "VPN resolved", "", domain.StateResolved, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 4, "VPN closed", "", domain.StateClosed, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 5, "VPN cancelled", "", domain.StateCancelled, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 6, "Deploy failed", "", domain.StateNew, domain.PriorityHigh, platformCategory.ID)
	// Preserve the open population, now with explicit responsibility evidence.
	for _, id := range []int64{1, 2, 3, 4, 5} {
		appendMetricAssignment(t, store, id, &support.ID, testClock)
	}
	appendMetricAssignment(t, store, 6, &platform.ID, testClock)
	seedPublished(t, store, supportCategory.ID, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: support.ID, Strategy: domain.StrategyClaim}}})

	tree, ok := catalog.(application.CatalogTreeStore)
	if !ok {
		t.Fatal("the SQLite catalog store must implement the tree projection")
	}
	desks, err := tree.ListTreeDesks(ctx)
	if err != nil {
		t.Fatalf("list tree desks: %v", err)
	}
	// Migration 0011 seeds the compatibility "General" desk, so the projection
	// answers the whole installation: that desk plus the two arranged here,
	// ordered by name.
	if len(desks) != 3 || desks[0].Name != "General" || desks[1].Name != "Platform" || desks[2].Name != "Support" {
		t.Fatalf("tree desks = %d, want every desk in one read: %+v", len(desks), desks)
	}
	byID := make(map[int64]domain.CatalogDesk, len(desks))
	for _, desk := range desks {
		byID[desk.ID] = desk
	}
	if got := byID[support.ID]; got.AgentCount != 2 || got.OpenTickets != 3 || got.CategoryCount != 1 || got.Description != "First line" {
		t.Errorf("support = agents %d/open %d/categories %d/description %q, want 2/3/1/First line",
			got.AgentCount, got.OpenTickets, got.CategoryCount, got.Description)
	}
	if got := byID[platform.ID]; got.AgentCount != 0 || got.OpenTickets != 1 {
		t.Errorf("platform = agents %d/open %d, want 0/1", got.AgentCount, got.OpenTickets)
	}
}

// The tree shows SLA state as Default or Custom (issue #387, S1/S8): the only
// fact it needs is whether a category's materialized matrix still equals the
// instance defaults. Comparing four priorities per category in the browser
// would cost a read per row; this projection answers it for the whole page.
//
// RED: the projection does not exist yet.
func TestCatalogStoreListDefaultSLACategoriesSeparatesCustomTargets(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	desk := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := store.DeskStore().Create(ctx, desk); err != nil {
		t.Fatalf("create desk: %v", err)
	}
	// Migration 0016 materializes the defaults onto every new category, so this
	// one matches out of the box.
	defaulted := &domain.Category{Name: "VPN", CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, defaulted); err != nil {
		t.Fatalf("create defaulted category: %v", err)
	}
	tuned := &domain.Category{Name: "Onboarding", CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, tuned); err != nil {
		t.Fatalf("create tuned category: %v", err)
	}
	if err := store.SLAStore().UpsertCategoryTarget(ctx, tuned.ID, domain.SLAPolicy{
		Priority: domain.PriorityCritical, FirstResponseSeconds: 900, ResolveSeconds: 7200,
	}); err != nil {
		t.Fatalf("tune category target: %v", err)
	}

	tree, ok := catalog.(application.CatalogTreeStore)
	if !ok {
		t.Fatal("the SQLite catalog store must implement the tree projection")
	}
	matches, err := tree.ListDefaultSLACategories(ctx)
	if err != nil {
		t.Fatalf("list default sla categories: %v", err)
	}
	if len(matches) != 1 || matches[0] != defaulted.ID {
		t.Fatalf("default-SLA categories = %v, want only %d", matches, defaulted.ID)
	}

	// A category created while the defaults were different keeps ITS matrix: the
	// comparison reads the current defaults, so changing them after creation
	// turns a defaulted category into a custom one without touching its rows.
	if _, err := store.db.ExecContext(ctx, `UPDATE sla_defaults SET first_response_seconds=1200 WHERE priority='high'`); err != nil {
		t.Fatalf("move the defaults: %v", err)
	}
	matches, err = tree.ListDefaultSLACategories(ctx)
	if err != nil {
		t.Fatalf("list default sla categories after the defaults moved: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("default-SLA categories = %v, want none once the defaults moved", matches)
	}
}
