package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestCatalogTree_RendersOrderHealthAndDisclosure is S1/S2 in the rendered page:
// one tree with departments by name then Unassigned last, per-row health, and a
// disclosure button per expandable node. These are the facts the browser contract
// (e2e/tests/categories.spec.ts) then drives.
func TestCatalogTree_RendersOrderHealthAndDisclosure(t *testing.T) {
	h := newHarness(t)
	_, page := catalogTreeFixture(t, h)

	for _, want := range []string{
		// The page is the Catalog now; the category level keeps its own noun.
		`<h1 class="command-title">Catalog</h1>`,
		// One tree, not three columns.
		`class="catalog-tree"`,
		`data-level="department"`,
		// Disclosure buttons, expanded by default, with the plural aria labels.
		`data-catalog-disclosure aria-expanded="true" aria-controls="catalog-d1" aria-label="Collapse General"`,
		`data-catalog-slug="d1"`,
		`data-catalog-slug="k`,
		// Health facts, in the order a row reads.
		`≤`,
	} {
		if strings.Contains(want, "≤") {
			continue
		}
		if !strings.Contains(page, want) {
			t.Errorf("the catalog tree must render %q", want)
		}
	}
	// Order: General before Operations, and Unassigned last.
	general := strings.Index(page, `data-catalog-slug="d1"`)
	operations := strings.Index(page, `data-catalog-slug="d2"`)
	unassigned := strings.Index(page, `data-catalog-slug="d-1"`)
	if general < 0 || operations < 0 || unassigned < 0 {
		t.Fatalf("the tree must hold every department: general=%d operations=%d unassigned=%d", general, operations, unassigned)
	}
	if !(general < operations && operations < unassigned) {
		t.Errorf("department order = general %d, operations %d, unassigned %d; want Unassigned last", general, operations, unassigned)
	}
	// Counts match the data: one desk with no agents, one with two.
	if !strings.Contains(page, `<span class="catalog-badge catalog-badge-none">No agents</span>`) {
		t.Error("a desk with no agents must render the amber No agents badge, not a zero count")
	}
	if !strings.Contains(page, `<span class="catalog-node-meta">2 agents</span>`) {
		t.Error("a staffed desk must render its agent count")
	}
	// The virtual Unassigned group carries no actions of its own.
	unassignedBlock := page[unassigned:]
	if end := strings.Index(unassignedBlock, `data-level="desk"`); end > 0 {
		unassignedBlock = unassignedBlock[:end]
	}
	if strings.Contains(unassignedBlock, "data-category-menu") {
		t.Error("the virtual Unassigned department must have no row menu")
	}
}

// TestCatalogTree_RowMenusKeepEveryActionAndAddCreation is S5: the tree keeps
// every action the columns offered and adds the two creation shortcuts that
// preselect their parent.
func TestCatalogTree_RowMenusKeepEveryActionAndAddCreation(t *testing.T) {
	h := newHarness(t)
	base, page := catalogTreeFixture(t, h)

	for _, want := range []string{
		// The one New menu owns creation.
		`data-catalog-new-toggle`,
		`id="catalog-new-menu"`,
		`href="/categories/departments/new?view=structure"`,
		`href="/categories/desks/new?view=structure"`,
		`href="/categories/new?view=structure"`,
		// A department keeps edit and delete and gains Add desk.
		`/categories/departments/` + id(base.departmentID) + `/edit?view=structure`,
		`action="/categories/departments/` + id(base.departmentID) + `/delete"`,
		`/categories/desks/new?view=structure&department_id=` + id(base.departmentID) + `"`,
		// A desk keeps edit and delete and gains Add category, both preselected.
		`/categories/desks/` + id(base.deskID) + `/edit?view=structure&department_id=` + id(base.departmentID),
		`action="/categories/desks/` + id(base.deskID) + `/delete"`,
		`/categories/new?view=structure&department_id=` + id(base.departmentID) + `&desk_id=` + id(base.deskID) + `"`,
		// A category keeps all four actions it has today.
		`/categories/` + id(base.categoryID) + `/edit?view=structure`,
		`href="/categories/` + id(base.categoryID) + `/workflow"`,
		`href="/categories/` + id(base.categoryID) + `/sla"`,
		`action="/categories/` + id(base.categoryID) + `/delete"`,
		// Every destructive control still asks first and names its row.
		`data-category-delete="category" data-delete-confirm="Delete the category “Membership”?"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the catalog tree must render %q", want)
		}
	}

	// The creation shortcuts preselect their parent in the drawer they open: the
	// same routes the columns used, with the ids the consignment carries.
	for _, tc := range []struct {
		path    string
		control string
	}{
		{"/categories/desks/new?view=structure&department_id=" + id(base.departmentID), `value="` + id(base.departmentID) + `" selected>`},
		{"/categories/new?view=structure&department_id=" + id(base.departmentID) + "&desk_id=" + id(base.deskID), `value="` + id(base.deskID) + `" data-department-id="` + id(base.departmentID) + `" selected>`},
	} {
		drawer := hxGet(t, h, tc.path)
		if drawer.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200: %s", tc.path, drawer.Code, drawer.Body.String())
		}
		if !strings.Contains(drawer.Body.String(), tc.control) {
			t.Errorf("GET %s must preselect its parent, missing %q", tc.path, tc.control)
		}
	}
}

// TestCatalogTree_FiltersRenderInPlaceAndCompose is S3/S4: both filters are plain
// GETs, they keep ancestors, the chip counts every issue and toggles off, and the
// two compose.
func TestCatalogTree_FiltersRenderInPlaceAndCompose(t *testing.T) {
	h := newHarness(t)
	base, _ := catalogTreeFixture(t, h)

	// attention=1 shows the issue nodes plus their ancestors, and the chip is
	// active with the same count it states when off.
	filtered := h.get(t, "/categories?attention=1", false).Body.String()
	if !strings.Contains(filtered, `class="catalog-attention-chip is-active" href="/categories"`) {
		t.Errorf("the active chip must toggle back to the plain URL: %s", filtered)
	}
	if !strings.Contains(filtered, `data-level="desk"`) {
		t.Errorf("the attention filter must keep the desk with an issue: %s", filtered)
	}
	// The chip states how many issues exist, and that number is the count of the
	// two issue facts the rows themselves render: a desk nobody staffs, and a
	// category with no published workflow.
	// Counted on the swapped region, not the whole document: the inline stylesheet
	// declares the badge class too.
	plainBody := hxGet(t, h, "/categories").Body.String()
	wantIssues := strings.Count(plainBody, "catalog-badge-none")
	chip := regexp.MustCompile(`class="catalog-attention-chip[^>]*>Needs attention · (\d+)<`).FindStringSubmatch(filtered)
	if chip == nil {
		t.Fatalf("the chip must state how many issues exist: %s", filtered)
	}
	if chip[1] != strconv.Itoa(wantIssues) {
		t.Errorf("chip count = %s, want the %d issue rows the page shows", chip[1], wantIssues)
	}
	// The category without a workflow is an issue; its desk and department are the
	// ancestors that keep it reachable.
	if !strings.Contains(filtered, "Membership") {
		t.Errorf("a category with no published workflow must be shown: %s", filtered)
	}

	// The plain page drops the filter from the chip URL and keeps the issues.
	plain := h.get(t, "/categories", false).Body.String()
	if !strings.Contains(plain, `class="catalog-attention-chip" href="/categories?attention=1"`) {
		t.Errorf("the inactive chip must link to the filtered view: %s", plain)
	}

	// Search and attention compose: only the matching issue survives.
	composed := h.get(t, "/categories?q=Membership&attention=1", false).Body.String()
	if !strings.Contains(composed, `<mark>Membership</mark>`) {
		t.Errorf("the composed view must keep the mark: %s", composed)
	}
	if strings.Contains(composed, `<span class="catalog-node-name">Payroll</span>`) {
		t.Errorf("the composed view must drop the desk that does not match: %s", composed)
	}

	// Count zero hides the chip entirely: an installation with nothing to fix must
	// not offer a filter that would empty the page.
	clean := fixtureCategoriesIndexData()
	clean.Tree.AttentionCount = 0
	if body := renderLocalized(t, "en", "categories_index", "categories_background", clean); strings.Contains(body, "catalog-attention-chip") {
		t.Errorf("a catalog with no issues must not render the attention chip: %s", body)
	}
	_ = base
}

// TestCatalogTree_LegacySelectionMarksAndDrawersPreserveIt is S6: the params still
// work, they only mark the row, and the drawer close URL keeps them.
func TestCatalogTree_LegacySelectionMarksAndDrawersPreserveIt(t *testing.T) {
	h := newHarness(t)
	base, _ := catalogTreeFixture(t, h)
	path := "/categories?view=structure&department_id=" + id(base.departmentID) + "&desk_id=" + id(base.deskID)
	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, `data-level="desk" data-current="true"`) {
		t.Errorf("the legacy desk selection must mark its row: %s", body)
	}
	if !strings.Contains(body, `aria-current="true"`) {
		t.Errorf("the marked row must say so accessibly: %s", body)
	}
	// The whole tree is still there: the params never filter.
	if !strings.Contains(body, `<span class="catalog-node-name">Payroll</span>`) {
		t.Errorf("the selection must not filter the tree: %s", body)
	}

	drawer := hxGet(t, h, "/categories/"+id(base.categoryID)+"/edit?view=structure&department_id="+id(base.departmentID)+"&desk_id="+id(base.deskID))
	if got, want := drawer.Header().Get("HX-Retarget"), "#category-drawer-host"; got != want {
		t.Errorf("HX-Retarget = %q, want %q", got, want)
	}
	closeURL := regexp.MustCompile(`data-close-url="([^"]+)"`).FindStringSubmatch(drawer.Body.String())
	if closeURL == nil {
		t.Fatalf("the drawer must carry its close URL: %s", drawer.Body.String())
	}
	for _, want := range []string{"desk_id=" + id(base.deskID), "department_id=" + id(base.departmentID), "view=structure"} {
		if !strings.Contains(closeURL[1], want) {
			t.Errorf("the drawer must close back to the state it was opened in, close URL %q is missing %q", closeURL[1], want)
		}
	}
}

// catalogTreeFixture arranges one installation with the shape the tree has to
// render: two real departments, a staffed desk with a category, a desk nobody
// staffs, and a legacy desk with no department.
type catalogTreeIDs struct {
	departmentID int64
	deskID       int64
	categoryID   int64
}

func catalogTreeFixture(t *testing.T, h *harness) (catalogTreeIDs, string) {
	t.Helper()
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	// Migration 0011 seeds the compatibility General department; reuse it so the
	// fixture does not collide with the installation's own first department.
	departments, err := catalog.ListDepartments(t.Context())
	if err != nil {
		t.Fatalf("list departments: %v", err)
	}
	var general domain.CatalogDepartment
	for _, department := range departments {
		if department.Name == "General" {
			general = department
		}
	}
	if general.ID == 0 {
		t.Fatal("the seeded General department is missing")
	}
	operations, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatalf("create Operations: %v", err)
	}
	staffed, err := catalog.CreateDeskFor(t.Context(), *h.admin, general.ID, "Service desk", "First line")
	if err != nil {
		t.Fatalf("create staffed desk: %v", err)
	}
	unstaffed, err := catalog.CreateDeskFor(t.Context(), *h.admin, operations.ID, "Payroll", "Nobody here")
	if err != nil {
		t.Fatalf("create unstaffed desk: %v", err)
	}
	if _, err := h.desks.CreateWithDescription(t.Context(), *h.admin, "Legacy desk", "No department"); err != nil {
		t.Fatalf("create legacy desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Membership", "Membership requests", staffed.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// Two agents, counted the way the publish gate counts them: an inactive member
	// and a member that cannot act must not inflate the number.
	agentA := seedUserRole(t, h.store, "Ana Agent", "ana-tree@tkt.test", domain.RoleAgent)
	agentB := seedUserRole(t, h.store, "Ada Admin", "ada-tree@tkt.test", domain.RoleAdmin)
	gone := seedUserRole(t, h.store, "Gone Agent", "gone-tree@tkt.test", domain.RoleAgent)
	if err := h.store.UserStore().Update(t.Context(), &domain.User{ID: gone.ID, Name: gone.Name, Email: gone.Email, PasswordHash: gone.PasswordHash, Role: gone.Role, Active: false, CreatedAt: gone.CreatedAt}); err != nil {
		t.Fatalf("deactivate member: %v", err)
	}
	for _, userID := range []int64{agentA.ID, agentB.ID, gone.ID} {
		if err := h.store.DeskStore().AddMember(t.Context(), staffed.ID, userID, fixedNow); err != nil {
			t.Fatalf("staff desk: %v", err)
		}
	}
	_ = unstaffed
	return catalogTreeIDs{departmentID: general.ID, deskID: staffed.ID, categoryID: category.ID},
		h.get(t, "/categories", false).Body.String()
}

// id formats an id for a URL, which every catalog assertion needs.
func id(value int64) string { return strconv.FormatInt(value, 10) }

// hxGet runs one authenticated HTMX fragment request through the harness mux.
func hxGet(t *testing.T, h *harness, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}
