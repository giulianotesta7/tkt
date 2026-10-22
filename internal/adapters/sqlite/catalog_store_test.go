package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestCatalogStorePreservesHierarchyAndAssociations(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	desks := store.DeskStore()
	operations := &domain.Department{Name: "Operations", Description: "Operations", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, operations); err != nil {
		t.Fatal(err)
	}
	second := &domain.Department{Name: "Second", Description: "Second", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, second); err != nil {
		t.Fatal(err)
	}
	firstDesk := &domain.Desk{Name: "Support", CreatedAt: testClock, DepartmentID: &operations.ID}
	if err := desks.Create(ctx, firstDesk); err != nil {
		t.Fatal(err)
	}
	secondDesk := &domain.Desk{Name: "Support 2", CreatedAt: testClock, DepartmentID: &second.ID}
	if err := desks.Create(ctx, secondDesk); err != nil {
		t.Fatal(err)
	}
	category := &domain.Category{Name: "Requests", Description: "Requests", DeskID: firstDesk.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, category); err != nil {
		t.Fatal(err)
	}
	departments, err := catalog.ListDepartments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, department := range departments {
		if department.ID == operations.ID && (department.DeskCount != 1 || department.CategoryCount != 1) {
			t.Fatalf("operations counts = desks %d/categories %d", department.DeskCount, department.CategoryCount)
		}
	}
	listed, err := catalog.ListCatalogCategories(ctx, firstDesk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != category.ID || listed[0].DepartmentID != operations.ID || listed[0].DeskName != "Support" {
		t.Fatalf("listed category = %+v", listed)
	}
	searched, err := catalog.SearchCatalog(ctx, "requests")
	if err != nil || len(searched) != 1 || searched[0].ID != category.ID {
		t.Fatalf("search result = %+v, err=%v", searched, err)
	}
	if err := catalog.MoveCategory(ctx, category.ID, secondDesk.ID); err != nil {
		t.Fatal(err)
	}
	moved, err := store.CategoryStore().GetByID(ctx, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.DeskID != secondDesk.ID {
		t.Fatalf("moved category desk = %d, want %d", moved.DeskID, secondDesk.ID)
	}
}

func TestCatalogStoreListsLegacyDesksAsUnassigned(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	result, err := store.db.ExecContext(ctx, `INSERT INTO desks(name, description, department_id, created_at) VALUES(?, ?, NULL, ?)`, "Legacy desk", "Legacy description", formatTime(testClock))
	if err != nil {
		t.Fatalf("insert legacy desk: %v", err)
	}
	deskID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("legacy desk id: %v", err)
	}
	category := &domain.Category{Name: "Legacy category", Description: "Legacy category description", DeskID: deskID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, category); err != nil {
		t.Fatalf("create legacy category: %v", err)
	}
	desks, err := store.CatalogStore().ListDesks(ctx, 0)
	if err != nil {
		t.Fatalf("list unassigned desks: %v", err)
	}
	if len(desks) != 1 || desks[0].ID != deskID || desks[0].Description != "Legacy description" || desks[0].DepartmentID != 0 || desks[0].DepartmentName != "Unassigned" || desks[0].CategoryCount != 1 {
		t.Fatalf("unassigned desks = %+v, want legacy desk and category", desks)
	}
}

func TestCatalogStoreRejectsDuplicateAndBrokenReferences(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	department := &domain.Department{Name: "Operations", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, department); err != nil {
		t.Fatal(err)
	}
	first := &domain.Desk{Name: "Support", CreatedAt: testClock, DepartmentID: &department.ID}
	if err := store.DeskStore().Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	duplicate := &domain.Desk{Name: first.Name, CreatedAt: testClock, DepartmentID: &department.ID}
	if err := store.DeskStore().Create(ctx, duplicate); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate desk error = %v", err)
	}
	missingDepartment := int64(999999)
	if err := store.DeskStore().Create(ctx, &domain.Desk{Name: "Missing", CreatedAt: testClock, DepartmentID: &missingDepartment}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing department error = %v", err)
	}
	if err := catalog.MoveCategory(ctx, 999999, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing category error = %v", err)
	}
	if err := catalog.MoveCategory(ctx, 999999, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing desk error = %v", err)
	}
	if err := catalog.DeleteDepartment(ctx, department.ID); !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("referenced department error = %v", err)
	}
}

// TestCatalogStoreOfferedCategoryCount is the #248 boundary: CategoryCount is the
// TOTAL the admin structure lists (published and draft alike), while
// OfferedCategoryCount is the number of the desk's or department's categories
// that carry a published version — exactly the set the requester picker lists.
// A desk could read "3" over two rows because the shared count included a
// category the picker never offers.
func TestCatalogStoreOfferedCategoryCount(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	department := &domain.Department{Name: "Operations", Description: "Operations", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, department); err != nil {
		t.Fatal(err)
	}
	desk := &domain.Desk{Name: "Support", CreatedAt: testClock, DepartmentID: &department.ID}
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
	published := &domain.Category{Name: "Requests", Description: "Requests", DeskID: desk.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, published); err != nil {
		t.Fatal(err)
	}
	deskCounts := func() (total, offered int) {
		t.Helper()
		desks, err := catalog.ListDesks(ctx, department.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(desks) != 1 {
			t.Fatalf("desks = %+v", desks)
		}
		return desks[0].CategoryCount, desks[0].OfferedCategoryCount
	}
	departmentCounts := func() (total, offered int) {
		t.Helper()
		departments, err := catalog.ListDepartments(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range departments {
			if d.ID == department.ID {
				return d.CategoryCount, d.OfferedCategoryCount
			}
		}
		t.Fatalf("department %d missing from %+v", department.ID, departments)
		return 0, 0
	}

	// One unpublished category: the admin total sees it, the picker offers none.
	if total, offered := deskCounts(); total != 1 || offered != 0 {
		t.Fatalf("unpublished desk counts = total %d/offered %d, want 1/0", total, offered)
	}
	if total, offered := departmentCounts(); total != 1 || offered != 0 {
		t.Fatalf("unpublished department counts = total %d/offered %d, want 1/0", total, offered)
	}

	// Publishing the category offers exactly one, and the total is unchanged.
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
	if total, offered := departmentCounts(); total != 1 || offered != 1 {
		t.Fatalf("published department counts = total %d/offered %d, want 1/1", total, offered)
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
	if total, offered := departmentCounts(); total != 1 || offered != 1 {
		t.Fatalf("unrunnable department counts = total %d/offered %d, want 1/1", total, offered)
	}

	// A draft-only category has a category_workflows row but no current version:
	// it must move the total without moving the offered count. This is the row
	// the LEFT JOIN could wrongly admit if the predicate checked for the row
	// instead of the pointer.
	draftOnly := &domain.Category{Name: "Drafts", Description: "Drafts", DeskID: desk.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, draftOnly); err != nil {
		t.Fatal(err)
	}
	if err := store.WorkflowStore().UpsertDraft(ctx, draftOnly.ID, mustCanon(t, domain.WorkflowDefinition{{
		Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "draft only"},
	}})); err != nil {
		t.Fatalf("upsert draft: %v", err)
	}
	if total, offered := deskCounts(); total != 2 || offered != 1 {
		t.Fatalf("draft-only desk counts = total %d/offered %d, want 2/1", total, offered)
	}
	if total, offered := departmentCounts(); total != 2 || offered != 1 {
		t.Fatalf("draft-only department counts = total %d/offered %d, want 2/1", total, offered)
	}
}

// The catalog tree (issue #387, S8) renders every desk with two facts no
// existing projection carries — how many agents can act on its tickets and how
// much work is open in it — and ONE read has to answer both for every desk.
// Before this, the index fanned out one ListDesks per department and still had
// no agent or open-ticket count at all.
//
// RED: the projection does not exist yet.
func TestCatalogStoreListTreeDesksCountsAgentsAndOpenTickets(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	operations := &domain.Department{Name: "Operations", Description: "Ops", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, operations); err != nil {
		t.Fatalf("create department: %v", err)
	}
	support := &domain.Desk{Name: "Support", Description: "First line", CreatedAt: testClock, DepartmentID: &operations.ID}
	if err := store.DeskStore().Create(ctx, support); err != nil {
		t.Fatalf("create support desk: %v", err)
	}
	platform := &domain.Desk{Name: "Platform", Description: "Infrastructure", CreatedAt: testClock, DepartmentID: &operations.ID}
	if err := store.DeskStore().Create(ctx, platform); err != nil {
		t.Fatalf("create platform desk: %v", err)
	}
	// A legacy desk keeps a NULL department and must still be projected, with
	// DepartmentID 0, so the handler can place it in the virtual Unassigned
	// group instead of losing it.
	legacy, err := store.db.ExecContext(ctx, `INSERT INTO desks(name, description, department_id, created_at) VALUES(?, ?, NULL, ?)`, "Legacy", "", formatTime(testClock))
	if err != nil {
		t.Fatalf("insert legacy desk: %v", err)
	}
	legacyID, err := legacy.LastInsertId()
	if err != nil {
		t.Fatalf("legacy desk id: %v", err)
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

	supportCategory := &domain.Category{Name: "VPN", Description: "Remote access", DeskID: support.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, supportCategory); err != nil {
		t.Fatalf("create support category: %v", err)
	}
	platformCategory := &domain.Category{Name: "Deploys", DeskID: platform.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, platformCategory); err != nil {
		t.Fatalf("create platform category: %v", err)
	}
	// "Open" is the same predicate the workflow summary uses: everything not
	// closed and not cancelled. A resolved ticket is still work somebody owes.
	seedSearchTicket(t, store, 1, "VPN down", "", domain.StateNew, domain.PriorityHigh, supportCategory.ID)
	seedSearchTicket(t, store, 2, "VPN flaky", "", domain.StateInProgress, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 3, "VPN resolved", "", domain.StateResolved, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 4, "VPN closed", "", domain.StateClosed, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 5, "VPN cancelled", "", domain.StateCancelled, domain.PriorityLow, supportCategory.ID)
	seedSearchTicket(t, store, 6, "Deploy failed", "", domain.StateNew, domain.PriorityHigh, platformCategory.ID)

	tree, ok := catalog.(application.CatalogTreeStore)
	if !ok {
		t.Fatal("the SQLite catalog store must implement the tree projection")
	}
	desks, err := tree.ListTreeDesks(ctx)
	if err != nil {
		t.Fatalf("list tree desks: %v", err)
	}
	// Migration 0011 seeds the compatibility "General" desk, so the projection
	// answers the whole installation: that desk plus the three arranged here.
	if len(desks) != 4 {
		t.Fatalf("tree desks = %d, want every desk in one read: %+v", len(desks), desks)
	}
	byID := make(map[int64]domain.CatalogDesk, len(desks))
	for _, desk := range desks {
		byID[desk.ID] = desk
	}
	if got := byID[support.ID]; got.AgentCount != 2 || got.OpenTickets != 3 || got.CategoryCount != 1 || got.DepartmentID != operations.ID {
		t.Errorf("support = agents %d/open %d/categories %d/department %d, want 2/3/1/%d",
			got.AgentCount, got.OpenTickets, got.CategoryCount, got.DepartmentID, operations.ID)
	}
	if got := byID[platform.ID]; got.AgentCount != 0 || got.OpenTickets != 1 {
		t.Errorf("platform = agents %d/open %d, want 0/1", got.AgentCount, got.OpenTickets)
	}
	if got := byID[legacyID]; got.AgentCount != 0 || got.OpenTickets != 0 || got.DepartmentID != 0 || got.DepartmentName != "Unassigned" {
		t.Errorf("legacy = %+v, want the virtual unassigned shape", got)
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
	department := &domain.Department{Name: "Operations", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, department); err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk := &domain.Desk{Name: "Support", CreatedAt: testClock, DepartmentID: &department.ID}
	if err := store.DeskStore().Create(ctx, desk); err != nil {
		t.Fatalf("create desk: %v", err)
	}
	// Migration 0016 materializes the defaults onto every new category, so this
	// one matches out of the box.
	defaulted := &domain.Category{Name: "VPN", DeskID: desk.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, defaulted); err != nil {
		t.Fatalf("create defaulted category: %v", err)
	}
	tuned := &domain.Category{Name: "Onboarding", DeskID: desk.ID, CreatedAt: testClock}
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
