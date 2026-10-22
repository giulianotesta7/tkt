package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The catalog tree view model (issue #387, T1/S1) is where the page's reads
// become rows: order, counts, health, search and attention filtering, the legacy
// selection mark and the SLA state. These tests exercise it directly, so a
// failure names the rule that broke instead of a substring of rendered HTML; the
// markup contract is pinned separately once the template exists.

func treeCategory(id int64, deskID int64, name string) domain.Category {
	return domain.Category{ID: id, Name: name, DeskID: deskID}
}

func treeDesk(id, departmentID int64, name string, agents, open int) domain.CatalogDesk {
	return domain.CatalogDesk{
		Desk:         domain.Desk{ID: id, Name: name},
		DepartmentID: departmentID,
		AgentCount:   agents,
		OpenTickets:  open,
	}
}

func treeDepartment(id int64, name string) domain.CatalogDepartment {
	return domain.CatalogDepartment{Department: domain.Department{ID: id, Name: name}}
}

func publishedState() catalogCategoryState {
	return catalogCategoryState{
		Badge:     application.MsgRef("categories.badge.published", application.Text("1")),
		Tone:      "ok",
		Published: true,
	}
}

func draftState() catalogCategoryState {
	return catalogCategoryState{Badge: application.MsgRef("categories.badge.draft_only"), Tone: "neutral"}
}

// baseTreeInput is the same fixture the rendered goldens use: two real
// departments, a desk each, and one category per desk.
func baseTreeInput() catalogTreeInput {
	return catalogTreeInput{
		Locale:      "en",
		Departments: []domain.CatalogDepartment{treeDepartment(2, "Support"), treeDepartment(1, "Technology")},
		Desks:       []domain.CatalogDesk{treeDesk(1, 1, "Service desk", 3, 3), treeDesk(3, 2, "Helpdesk", 0, 1)},
		Categories:  []domain.Category{treeCategory(2, 1, "Support"), treeCategory(1, 1, "Bugs"), treeCategory(3, 3, "Returns")},
		States:      map[int64]catalogCategoryState{1: publishedState(), 2: draftState(), 3: publishedState()},
		OpenTickets: map[int64]int{1: 3, 2: 1, 3: 0},
		SLADefaults: map[int64]bool{1: true},
		SLAEnabled:  true,
	}
}

// S1 order: real departments by name, desks and categories by name inside their
// parent. The projection returns the departments in insertion order, so the order
// here can only come from the tree itself.
func TestCatalogTreeOrdersDepartmentsDesksAndCategories(t *testing.T) {
	tree := buildCatalogTree(baseTreeInput())
	if len(tree.Departments) != 2 {
		t.Fatalf("departments = %d, want 2: %+v", len(tree.Departments), tree.Departments)
	}
	if tree.Departments[0].Name.Plain != "Support" || tree.Departments[1].Name.Plain != "Technology" {
		t.Fatalf("department order = %q, %q, want Support then Technology",
			tree.Departments[0].Name.Plain, tree.Departments[1].Name.Plain)
	}
	technology := tree.Departments[1]
	if len(technology.Desks) != 1 || technology.DeskCount != 1 || technology.AgentCount != 3 {
		t.Fatalf("technology = %+v, want one desk with three agents", technology)
	}
	var names []string
	for _, category := range technology.Desks[0].Categories {
		names = append(names, category.Name.Plain)
	}
	if strings.Join(names, ",") != "Bugs,Support" {
		t.Fatalf("category order = %v, want Bugs then Support", names)
	}
}

// S1 keeps the virtual Unassigned group last: desks whose stored department is
// NULL are presented there, and it is the only group with no actions of its own.
func TestCatalogTreePlacesUnassignedLast(t *testing.T) {
	in := baseTreeInput()
	in.Desks = append(in.Desks, treeDesk(9, 0, "Legacy", 1, 0))
	in.Categories = append(in.Categories, treeCategory(9, 9, "Other"))
	in.States[9] = publishedState()
	tree := buildCatalogTree(in)
	last := tree.Departments[len(tree.Departments)-1]
	if !last.Virtual || last.Name.Plain != "Unassigned" {
		t.Fatalf("last department = %+v, want the virtual Unassigned group", last)
	}
	if last.DeskCount != 1 || len(last.Desks) != 1 || last.Desks[0].Name.Plain != "Legacy" {
		t.Fatalf("unassigned group = %+v, want the legacy desk", last)
	}
	if last.Description != "Desks without a department" {
		t.Errorf("unassigned description = %q, want the catalogued one", last.Description)
	}
}

// S3: a query keeps matching nodes and their ancestors, ancestors stay
// expanded, and the matched run of the name is marked for <mark>.
func TestCatalogTreeSearchKeepsAncestorsAndMarksMatches(t *testing.T) {
	in := baseTreeInput()
	in.Query = "bugs"
	tree := buildCatalogTree(in)
	if len(tree.Departments) != 1 {
		t.Fatalf("departments = %d, want only the ancestor of the match: %+v", len(tree.Departments), tree.Departments)
	}
	department := tree.Departments[0]
	if department.Name.Matched {
		t.Error("the ancestor's own name must not be marked: only the node that matched is highlighted")
	}
	if len(department.Desks) != 1 {
		t.Fatalf("desks = %d, want the ancestor desk: %+v", len(department.Desks), department.Desks)
	}
	desk := department.Desks[0]
	if len(desk.Categories) != 1 || desk.Categories[0].Name.Plain != "Bugs" {
		t.Fatalf("categories = %+v, want only Bugs", desk.Categories)
	}
	parts := desk.Categories[0].Name.Parts
	if len(parts) != 1 || !parts[0].Match || parts[0].Text != "Bugs" {
		t.Fatalf("name parts = %+v, want the whole name marked", parts)
	}

	// A case-insensitive partial match marks only the matched run.
	partial := newCatalogName("Connectivity VPN", "vpn")
	if len(partial.Parts) != 2 || partial.Parts[0].Text != "Connectivity " || partial.Parts[0].Match {
		t.Fatalf("parts = %+v, want the literal prefix first", partial.Parts)
	}
	if !partial.Parts[1].Match || partial.Parts[1].Text != "VPN" {
		t.Fatalf("parts = %+v, want the matched run preserved as written", partial.Parts)
	}

	// A department match keeps the whole subtree under it.
	in = baseTreeInput()
	in.Query = "technology"
	tree = buildCatalogTree(in)
	if len(tree.Departments) != 1 || tree.Departments[0].Name.Plain != "Technology" {
		t.Fatalf("departments = %+v, want the matching department", tree.Departments)
	}
	// A matching department keeps its whole subtree: the department is the node
	// that matched, so nothing inside it is filtered away.
	if len(tree.Departments[0].Desks) != 1 || len(tree.Departments[0].Desks[0].Categories) != 2 {
		t.Fatalf("a matching department must keep its descendants: %+v", tree.Departments[0])
	}

	// No match at all is an empty tree, never a full one.
	in.Query = "nothing-matches-this"
	if tree = buildCatalogTree(in); len(tree.Departments) != 0 {
		t.Fatalf("departments = %+v, want none", tree.Departments)
	}
}

// S4: the chip counts every issue, and the filter keeps the nodes with an issue
// plus the ancestors that contain them. A desk with no agents makes its
// categories issues too, because nothing there can move a ticket.
func TestCatalogTreeAttentionCountsAndFilters(t *testing.T) {
	in := baseTreeInput()
	tree := buildCatalogTree(in)
	// Support has no agents (1) and its Returns category is published, so the desk
	// alone is the issue; Service desk has agents but one draft category (1).
	if tree.AttentionCount != 2 {
		t.Fatalf("attention count = %d, want desks without agents plus categories without a published workflow = 2", tree.AttentionCount)
	}

	in.Attention = true
	tree = buildCatalogTree(in)
	if tree.AttentionCount != 2 {
		t.Errorf("filtered attention count = %d, want the unfiltered count so the chip states the real size", tree.AttentionCount)
	}
	if len(tree.Departments) != 2 {
		t.Fatalf("departments = %+v, want both, because each holds one issue", tree.Departments)
	}
	support := tree.Departments[0]
	if len(support.Desks) != 1 || support.Desks[0].Name.Plain != "Helpdesk" {
		t.Fatalf("support desks = %+v, want only the desk without agents", support.Desks)
	}
	if len(support.Desks[0].Categories) != 1 || support.Desks[0].Categories[0].Name.Plain != "Returns" {
		t.Fatalf("a desk without agents keeps its categories: %+v", support.Desks[0].Categories)
	}
	technology := tree.Departments[1]
	if len(technology.Desks[0].Categories) != 1 || technology.Desks[0].Categories[0].Name.Plain != "Support" {
		t.Fatalf("technology categories = %+v, want only the unpublished one", technology.Desks[0].Categories)
	}

	// Composed with a query: the attention filter still applies.
	in.Query = "returns"
	tree = buildCatalogTree(in)
	if len(tree.Departments) != 1 || tree.Departments[0].Name.Plain != "Support" {
		t.Fatalf("departments = %+v, want the one that holds the only match", tree.Departments)
	}
	if len(tree.Departments[0].Desks[0].Categories) != 1 {
		t.Fatalf("composed filters kept %+v, want the matching issue only", tree.Departments[0].Desks)
	}
}

// S1: SLA state appears only when the instance has SLA enabled, and it reads
// Default or Custom — never one target, because four priorities make a single
// number ambiguous.
func TestCatalogTreeSLAStateOnlyWhenEnabled(t *testing.T) {
	tree := buildCatalogTree(baseTreeInput())
	if got := tree.Departments[1].Desks[0].Categories[0].SLAKey; got != "categories.tree.sla_default" {
		t.Errorf("defaulted category SLA key = %q", got)
	}
	if got := tree.Departments[1].Desks[0].Categories[1].SLAKey; got != "categories.tree.sla_custom" {
		t.Errorf("custom category SLA key = %q", got)
	}

	in := baseTreeInput()
	in.SLAEnabled = false
	tree = buildCatalogTree(in)
	for _, department := range tree.Departments {
		for _, desk := range department.Desks {
			for _, category := range desk.Categories {
				if category.SLAKey != "" {
					t.Fatalf("category %q rendered SLA %q with SLA disabled", category.Name.Plain, category.SLAKey)
				}
			}
		}
	}
}

// S6: the legacy view/department/desk params no longer filter. They mark the
// matching row, so old links and mutation redirects still land somewhere.
func TestCatalogTreeLegacySelectionMarksInsteadOfFiltering(t *testing.T) {
	in := baseTreeInput()
	in.DepartmentID = 2
	in.DeskID = 3
	tree := buildCatalogTree(in)
	if len(tree.Departments) != 2 || len(tree.Departments[0].Desks) != 1 {
		t.Fatalf("the selection must not filter the tree: %+v", tree.Departments)
	}
	support := tree.Departments[0]
	if !support.Current {
		t.Error("the selected department must be marked current")
	}
	if !support.Desks[0].Current {
		t.Error("the selected desk must be marked current")
	}
	if tree.Departments[1].Current || tree.Departments[1].Desks[0].Current {
		t.Error("an unselected row must not be marked current")
	}
}

// S1/S8 at the handler seam: the index data is assembled from the bounded
// projections and carries per-row health, not just names.
func TestCategoryIndexDataBuildsTheTreeFromTheBoundedProjections(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Onboarding", "New hire setup", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	handler := NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).WithSettings(h.store.SettingsStore())

	req := httptest.NewRequest(http.MethodGet, "/categories", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyUser{}, h.admin))
	data, err := handler.categoryIndexData(req, "")
	if err != nil {
		t.Fatalf("index data: %v", err)
	}
	var operations *catalogTreeDepartment
	for i := range data.Tree.Departments {
		if data.Tree.Departments[i].ID == department.ID {
			operations = &data.Tree.Departments[i]
		}
	}
	if operations == nil {
		t.Fatalf("departments = %+v, want Operations", data.Tree.Departments)
	}
	// The desk was staffed by nobody: the row must say so instead of showing a
	// misleading zero that looks like a count.
	if len(operations.Desks) != 1 || operations.Desks[0].AgentCount != 0 || !operations.Desks[0].NeedsAttention {
		t.Fatalf("operations desks = %+v, want one desk flagged for missing agents", operations.Desks)
	}
	row := operations.Desks[0].Categories[0]
	if !row.NeedsAttention {
		t.Error("a category with no published workflow must be flagged")
	}
	if row.Workflow.Empty() {
		t.Error("every category row carries a rendered workflow state")
	}
	// SLA is off on a fresh instance, so no row claims an SLA state.
	if row.SLAKey != "" {
		t.Errorf("SLA key = %q, want none while the instance has SLA disabled", row.SLAKey)
	}
	_ = category
}
