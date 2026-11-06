package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Categories screen over the folder tree (issue #484, S6, S12-S15). These
// run through the real harness: routes, services, SQLite and templates.

func folderCount(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM folders`)
}

func folderParent(t *testing.T, h *harness, id int64) int64 {
	t.Helper()
	parent, _ := scanOneNullableInt(t, h.rawDB(t), `SELECT parent_id FROM folders WHERE id = ?`, id)
	return parent
}

// rowOrder returns the ids of the given kind ("folder" or "category") in the
// order the table renders them.
func rowOrder(body, kind string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<li class="catalog-tree-node catalog-tree-`+kind+`"[^>]*data-catalog-id="(\d+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestCatalogMoveDestinationsSurviveFiltering(t *testing.T) {
	h := newHarness(t)
	parent := seedFolder(t, h, nil, "Unmatched parent")
	child := seedFolder(t, h, &parent, "Unmatched child")
	body := h.get(t, "/categories?q=not-matching", false).Body.String()
	for _, want := range []string{`data-move-folder-id="` + id(parent) + `"`, `data-move-folder-id="` + id(child) + `"`, "Unmatched parent › Unmatched child"} {
		if !strings.Contains(body, want) {
			t.Errorf("filtered catalog missing move destination %q", want)
		}
	}
}

func TestCatalogInlineTreeAccessibility(t *testing.T) {
	h := newHarness(t)
	folder := seedFolder(t, h, nil, "Accessible folder")
	body := h.get(t, "/categories", false).Body.String()
	for _, want := range []string{`role="tree"`, `role="treeitem"`, `aria-level="1"`, `aria-selected="false"`, `data-rename-url="/categories/folders/` + id(folder) + `/rename"`, `id="catalog-live"`} {
		if !strings.Contains(body, want) {
			t.Errorf("inline tree missing %q", want)
		}
	}
}

func TestCatalogIsTreeWithoutLayoutSwitch(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/categories", false).Body.String()
	if !strings.Contains(body, `class="catalog-tree catalog-folder-tree"`) || strings.Contains(body, `action="/categories/layout"`) {
		t.Error("categories must show the tree without a layout switch")
	}
	rec := h.postForm(t, "/categories/layout", url.Values{"layout": {"tree"}}, false)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("removed layout route returned %d, want 404 or 405", rec.Code)
	}
}

func TestCatalogFoldersCreateRenameAndRefuseClashes(t *testing.T) {
	h := newHarness(t)
	before := folderCount(t, h)
	created := h.postForm(t, "/categories/folders", url.Values{"name": {"  IT services "}, "description": {"Descripción <strong>literal</strong>"}}, true)
	if created.Code != http.StatusOK || created.Header().Get("HX-Retarget") != "#categories-background" {
		t.Fatalf("create = %d %q %s", created.Code, created.Header().Get("HX-Retarget"), created.Body.String())
	}
	it := scanOneInt(t, h.rawDB(t), `SELECT id FROM folders WHERE name = 'IT services'`)
	catalog := h.get(t, "/categories", false)
	if catalog.Code != http.StatusOK {
		t.Fatalf("catalog = %d", catalog.Code)
	}
	rootRow := regexp.MustCompile(`(?s)<li class="catalog-tree-node catalog-tree-folder"[^>]*data-catalog-id="` + id(it) + `".*?</div>`).FindString(catalog.Body.String())
	if !strings.Contains(rootRow, `<span class="catalog-node-sub">Descripción &lt;strong&gt;literal&lt;/strong&gt;</span>`) || strings.Contains(rootRow, `<strong>literal</strong>`) {
		t.Errorf("root folder description must be escaped in its own sub-line: %s", rootRow)
	}
	sub := h.postForm(t, "/categories/folders", url.Values{"name": {"Hardware"}, "folder_id": {id(it)}, "folder": {id(it)}}, false)
	wantRedirect(t, sub, http.StatusSeeOther, "/categories?folder="+id(it))
	hw := scanOneInt(t, h.rawDB(t), `SELECT id FROM folders WHERE name = 'Hardware'`)
	if folderParent(t, h, hw) != it {
		t.Errorf("Hardware parent = %d, want %d", folderParent(t, h, hw), it)
	}

	for name, form := range map[string]url.Values{
		"blank name":                  {"name": {"   "}},
		"sibling clash, other case":   {"name": {"HARDWARE"}, "folder_id": {id(it)}},
		"top-level clash, other case": {"name": {"it SERVICES"}},
	} {
		rec := h.postForm(t, "/categories/folders", form, true)
		body := rec.Body.String()
		if rec.Header().Get("HX-Retarget") != "#category-drawer-host" || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `id="category-name" name="name" type="text" value="`+form.Get("name")+`"`) {
			t.Errorf("%s: want the drawer with the error and the typed name, got %d %q", name, rec.Code, rec.Header().Get("HX-Retarget"))
		}
	}
	if got := folderCount(t, h); got != before+2 {
		t.Errorf("folders = %d, want %d: a refused create wrote a row", got, before+2)
	}
	// The same name is fine under another parent.
	if rec := h.postForm(t, "/categories/folders", url.Values{"name": {"Hardware"}}, false); rec.Code != http.StatusSeeOther {
		t.Errorf("same name at the top level = %d", rec.Code)
	}

	renamed := h.postForm(t, "/categories/folders/"+id(hw)+"/edit", url.Values{"name": {"Equipment"}, "description": {"Docks"}}, true)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", renamed.Code, renamed.Body.String())
	}
	if got := scanOneString(t, h.rawDB(t), `SELECT name FROM folders WHERE id = ?`, hw); got != "Equipment" {
		t.Errorf("renamed = %q", got)
	}
	if folderParent(t, h, hw) != it {
		t.Errorf("a rename moved the folder")
	}
}

func TestCatalogFoldersMoveRefusesCyclesAndDeleteRefusesNonEmpty(t *testing.T) {
	h := newHarness(t)
	it := seedFolder(t, h, nil, "IT services")
	hw := seedFolder(t, h, &it, "Hardware")
	laptops := seedFolder(t, h, &hw, "Laptops")
	other := seedFolder(t, h, nil, "Other")

	// The move drawer never offers the folder itself or anything below it.
	drawer := h.get(t, "/categories/folders/"+id(it)+"/move", true).Body.String()
	for _, hidden := range []int64{it, hw, laptops} {
		if strings.Contains(drawer, `<option value="`+id(hidden)+`"`) {
			t.Errorf("the move drawer offers folder %d, which is the folder or below it", hidden)
		}
	}
	if !strings.Contains(drawer, `<option value="`+id(other)+`">Other</option>`) {
		t.Errorf("the move drawer must offer Other by its path")
	}

	for _, target := range []int64{it, hw, laptops} {
		rec := h.postForm(t, "/categories/folders/"+id(it)+"/move", url.Values{"folder_id": {id(target)}, "name": {"IT services"}}, true)
		if !strings.Contains(rec.Body.String(), "a folder can&#39;t move into itself or one of its subfolders") {
			t.Errorf("move into %d: %d %s", target, rec.Code, rec.Body.String())
		}
		if folderParent(t, h, it) != 0 {
			t.Fatalf("a refused move changed the parent")
		}
	}
	moved := h.postForm(t, "/categories/folders/"+id(hw)+"/move", url.Values{"folder_id": {id(other)}}, false)
	wantRedirect(t, moved, http.StatusSeeOther, "/categories")
	if folderParent(t, h, hw) != other {
		t.Errorf("legal move left parent %d", folderParent(t, h, hw))
	}

	refused := h.postForm(t, "/categories/folders/"+id(other)+"/delete", url.Values{"folder": {id(other)}}, true)
	if refused.Header().Get("HX-Retarget") != "#categories-background" || !strings.Contains(refused.Body.String(), "This folder isn&#39;t empty. Move or delete its folders and categories first.") {
		t.Errorf("non-empty delete = %d %s", refused.Code, refused.Body.String())
	}
	if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM folders WHERE id = ?`, other); n != 1 {
		t.Errorf("the refused delete removed the folder")
	}
	deleted := h.postForm(t, "/categories/folders/"+id(laptops)+"/delete", url.Values{"folder": {id(laptops)}}, false)
	// Deleting the selected folder returns to the top level.
	wantRedirect(t, deleted, http.StatusSeeOther, "/categories")
	if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM folders WHERE id = ?`, laptops); n != 0 {
		t.Errorf("the empty folder was not deleted")
	}
}

func TestCatalogCategoriesLiveInFolders(t *testing.T) {
	h := newHarness(t)
	it := seedFolder(t, h, nil, "IT services")
	other := seedFolder(t, h, nil, "Other")

	drawer := h.get(t, "/categories/new?folder_id="+id(it), true).Body.String()
	for _, want := range []string{
		`<option value="` + id(it) + `" selected>IT services</option>`,
	} {
		if !strings.Contains(drawer, want) {
			t.Errorf("new category drawer missing %q", want)
		}
	}
	if strings.Contains(drawer, `name="department_id"`) || strings.Contains(drawer, `name="desk_id"`) {
		t.Errorf("the category drawer still asks for a department or desk")
	}

	rec := h.postForm(t, "/categories", url.Values{"name": {"Password reset"}, "folder_id": {id(it)}, "folder": {id(it)}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/categories?folder="+id(it))
	cat := scanOneInt(t, h.rawDB(t), `SELECT id FROM categories WHERE name = 'Password reset'`)
	if folder, _ := scanOneNullableInt(t, h.rawDB(t), `SELECT folder_id FROM categories WHERE id = ?`, cat); folder != it {
		t.Errorf("category folder = %d, want %d", folder, it)
	}
	h.publishWorkflow(t, cat, simpleManualDef())
	version := scanOneInt(t, h.rawDB(t), `SELECT current_version_id FROM category_workflows WHERE category_id = ?`, cat)

	edit := h.get(t, "/categories/"+id(cat)+"/edit", true).Body.String()
	if !strings.Contains(edit, `<option value="`+id(it)+`" selected>`) {
		t.Errorf("the edit drawer must preselect the category's folder")
	}

	moved := h.postForm(t, "/categories/"+id(cat)+"/move", url.Values{"folder_id": {id(other)}}, false)
	wantRedirect(t, moved, http.StatusSeeOther, "/categories")
	if folder, _ := scanOneNullableInt(t, h.rawDB(t), `SELECT folder_id FROM categories WHERE id = ?`, cat); folder != other {
		t.Errorf("moved category folder = %d", folder)
	}
	if after := scanOneInt(t, h.rawDB(t), `SELECT current_version_id FROM category_workflows WHERE category_id = ?`, cat); after != version {
		t.Errorf("moving a category changed its published workflow")
	}
	if rec := h.postForm(t, "/categories/"+id(cat)+"/move", url.Values{"folder_id": {""}}, false); rec.Code != http.StatusSeeOther {
		t.Errorf("move to the top level = %d", rec.Code)
	}
	if _, set := scanOneNullableInt(t, h.rawDB(t), `SELECT folder_id FROM categories WHERE id = ?`, cat); set {
		t.Errorf("category not at the top level")
	}
}

func TestCatalogOrderAttentionAndSearch(t *testing.T) {
	h := newHarness(t)
	general := deskIDNamed(t, h, "General")
	it := seedFolder(t, h, nil, "it services")
	hw := seedFolder(t, h, &it, "Hardware")
	access := seedFolder(t, h, &it, "Access")
	benefits := seedFolder(t, h, nil, "Benefits")
	h.staff(t, general)
	repair, _ := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Laptop repair", "", "", &hw)
	h.publishWorkflow(t, repair.ID, simpleManualDef())
	h.seedTicket(t, "Open repair", func(in *application.CreateTicketInput) { in.CategoryID = repair.ID })
	zed, _ := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Zed request", "", "", &it)
	h.publishWorkflow(t, zed.ID, simpleManualDef())
	h.categories.CreateInFolderFor(t.Context(), *h.admin, "Alpha request", "", "", &it)

	// Folders first, then categories, each alphabetical case-insensitively.
	body := h.get(t, "/categories?folder="+id(it), false).Body.String()
	if got := rowOrder(body, "folder"); strings.Join(got, ",") != id(benefits)+","+id(it)+","+id(access)+","+id(hw) {
		t.Errorf("folder order = %v", got)
	}
	cats := rowOrder(body, "category")
	if len(cats) < 2 || strings.Index(body, ">Alpha request<") > strings.Index(body, ">Zed request<") || strings.Index(body, `data-catalog-id="`+id(hw)+`"`) > strings.Index(body, ">Alpha request<") {
		t.Errorf("rows are not folders-then-categories alphabetical: %v", cats)
	}

	// Only categories count: Alpha has no published workflow. Empty folders
	// and folders without published descendants do not count.
	quiet := seedFolder(t, h, nil, "Quiet")
	seedFolder(t, h, &quiet, "Quiet child")
	top := h.get(t, "/categories", false).Body.String()
	folderRow := regexp.MustCompile(`(?s)<li class="catalog-tree-node catalog-tree-folder"[^>]*data-catalog-id="` + id(access) + `".*?</div>`).FindString(top)
	if strings.Contains(folderRow, `catalog-node-sub`) || strings.Contains(folderRow, `catalog-badge`) {
		t.Errorf("empty folder must have no sub-line or badge: %s", folderRow)
	}
	for _, gone := range []string{`>0 open<`, `>1 open<`, `>1 folder`, `>2 folders`, `>2 categories`, ` · by Admin`, ` · edited by Admin`} {
		if strings.Contains(top, gone) {
			t.Errorf("catalog rows still show %q", gone)
		}
	}
	for _, line := range regexp.MustCompile(`(?s)<div class="catalog-tree-row catalog-tree-row-folder">.*?</div>`).FindAllString(top, -1) {
		if strings.Contains(line, `catalog-node-sub`) {
			t.Errorf("a shallow folder must have no sub-line: %s", line)
		}
	}
	if !strings.Contains(top, "Needs attention · 1") {
		t.Errorf("attention chip missing or wrong: %s", regexp.MustCompile(`Needs attention · \d+`).FindString(top))
	}
	attention := h.get(t, "/categories?attention=1", false).Body.String()
	for _, want := range []string{`data-catalog-id="` + id(it) + `"`, ">Alpha request<"} {
		if !strings.Contains(attention, want) {
			t.Errorf("attention results missing %q", want)
		}
	}
	for _, healthy := range []string{">Laptop repair<", ">Zed request<"} {
		if strings.Contains(attention, healthy) {
			t.Errorf("a healthy category %s is listed under Needs attention", healthy)
		}
	}
	folderIDs := "," + strings.Join(rowOrder(attention, "folder"), ",") + ","
	for _, absent := range []int64{access, benefits, quiet, hw} {
		if strings.Contains(folderIDs, ","+id(absent)+",") {
			t.Errorf("attention includes unrelated folder %d", absent)
		}
	}

	// Search by a folder name reaches the categories below it, with their path.
	search := h.get(t, "/categories?q=hardware", false).Body.String()
	if !strings.Contains(search, "<mark>Hardware</mark>") || !strings.Contains(search, ">Laptop repair<") {
		t.Errorf("folder-name search must list the folder and the category below it with its path")
	}
	if strings.Contains(search, ">Zed request<") {
		t.Errorf("search listed a category that does not match")
	}
	if !strings.Contains(h.get(t, "/categories?q=nothing-here", false).Body.String(), "No folders or categories match your search.") {
		t.Errorf("an empty search must say so")
	}
	if rec := h.get(t, "/categories?folder=abc", false); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid folder state = %d, want 422", rec.Code)
	}
	if rec := h.postForm(t, "/categories/folders", url.Values{"name": {"Never"}, "folder": {"-3"}}, false); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a mutation with an invalid return state = %d, want 422", rec.Code)
	}
	if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM folders WHERE name = 'Never'`); n != 0 {
		t.Errorf("a refused mutation wrote a folder")
	}
}

// TestCatalogTreeLeavesDeskResponsibilityToWorkflows pins issue #491 S1 on the
// tree: the workflow's steps decide which desk handles a category, so a row
// neither shows the category's own desk nor raises attention because that desk
// has no agents. The workflow facts keep their warnings.
func TestCatalogTreeLeavesDeskResponsibilityToWorkflows(t *testing.T) {
	h := newHarness(t)
	// An unstaffed desk that no category can name any more (#491).
	h.desks.Create(t.Context(), *h.admin, "Legacy owners")
	support, _ := h.desks.Create(t.Context(), *h.admin, "Support")
	infra, _ := h.desks.Create(t.Context(), *h.admin, "Infra")
	owned, _ := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Legacy owned", "", "", nil)
	h.publishWorkflow(t, owned.ID, assignToDeskDef(support.ID))
	h.categories.CreateInFolderFor(t.Context(), *h.admin, "Never published", "", "", nil)
	broken, _ := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Broken route", "", "", nil)
	h.publishWorkflow(t, broken.ID, assignToDeskDef(infra.ID))
	if err := h.desks.RemoveMember(t.Context(), *h.admin, infra.ID, h.admin.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	body := h.get(t, "/categories", false).Body.String()
	row := regexp.MustCompile(`(?s)<li class="catalog-tree-node catalog-tree-category" data-catalog-id="` + id(owned.ID) + `".*?</li>`).FindString(body)
	if row == "" {
		t.Fatalf("the Legacy owned row is missing")
	}
	for _, gone := range []string{"catalog-desk-chip", "Legacy owners", "is-unstaffed", "Desk has no agents"} {
		if strings.Contains(row, gone) {
			t.Errorf("a category row must not show its own desk, found %q in %s", gone, row)
		}
	}
	if strings.Contains(body, "catalog-desk-chip") {
		t.Errorf("no category row may carry a desk chip")
	}
	if !strings.Contains(body, "Can&#39;t run · Infra has no active members") {
		t.Errorf("the workflow's own readiness warning must still render")
	}

	// The category with no published workflow needs attention, and so does
	// Broken route: its published workflow can't run, which the workflow (not
	// the category's own desk) now says. The harness's Bugs and Legacy owned run.
	if got := regexp.MustCompile(`Needs attention · \d+`).FindString(body); got != "Needs attention · 2" {
		t.Errorf("attention chip = %q, want %q", got, "Needs attention · 2")
	}
	attention := h.get(t, "/categories?attention=1", false).Body.String()
	if !strings.Contains(attention, ">Never published<") {
		t.Errorf("a category with no published workflow must still need attention")
	}
	if !strings.Contains(attention, ">Broken route<") {
		t.Errorf("a category whose published workflow can't run must need attention")
	}
	if strings.Contains(attention, ">Legacy owned<") {
		t.Errorf("a runnable category must not need attention because its own desk has no agents")
	}
}

func TestCatalogTreeLayoutCapsTheIndentPastLevelFour(t *testing.T) {
	h := newHarness(t)
	var parent *int64
	for _, name := range []string{"L1", "L2", "L3", "L4", "L5", "L6"} {
		folder := seedFolder(t, h, parent, name)
		parent = &folder
	}
	body := h.get(t, "/categories", false).Body.String()
	if !strings.Contains(body, `<span class="catalog-level">Level 5</span> L5`) || !strings.Contains(body, `<span class="catalog-level">Level 6</span> L6`) {
		t.Errorf("levels past 4 must show their level")
	}
	if strings.Contains(body, `<span class="catalog-level">Level 4</span>`) {
		t.Errorf("level 4 is still indented and must not show a level")
	}
	if !strings.Contains(body, `<span class="catalog-node-sub">L1 › L2 › L3 › L4</span>`) {
		t.Errorf("a deep row must show its path")
	}

	deep := *parent
	for _, description := range []string{"Descripción <b>literal</b>", ""} {
		updated := h.postForm(t, "/categories/folders/"+id(deep)+"/edit", url.Values{"name": {"L6"}, "description": {description}}, true)
		if updated.Code != http.StatusOK {
			t.Fatalf("update deep folder = %d %s", updated.Code, updated.Body.String())
		}
		catalog := h.get(t, "/categories", false)
		if catalog.Code != http.StatusOK {
			t.Fatalf("catalog = %d", catalog.Code)
		}
		line := regexp.MustCompile(`(?s)<li class="catalog-tree-node catalog-tree-folder"[^>]*data-catalog-id="` + id(deep) + `".*?</div>`).FindString(catalog.Body.String())
		want := `<span class="catalog-node-sub">L1 › L2 › L3 › L4 › L5`
		if description != "" {
			want += ` · Descripción &lt;b&gt;literal&lt;/b&gt;`
		}
		want += `</span>`
		if !strings.Contains(line, want) || strings.Contains(line, `<b>literal</b>`) {
			t.Errorf("deep folder sub-line for %q: %s", description, line)
		}
	}
}

func TestCatalogDrawersVaryAndRefuseAgents(t *testing.T) {
	h := newHarness(t)
	folder := seedFolder(t, h, nil, "IT services")
	for _, path := range []string{"/categories/new", "/categories/folders/new", "/categories/folders/" + id(folder) + "/edit", "/categories/folders/" + id(folder) + "/move"} {
		rec := hxGet(t, h, path)
		if rec.Code != http.StatusOK || rec.Header().Get("Vary") != "HX-Request" || rec.Header().Get("HX-Retarget") != "#category-drawer-host" {
			t.Errorf("GET %s = %d vary %q retarget %q", path, rec.Code, rec.Header().Get("Vary"), rec.Header().Get("HX-Retarget"))
		}
	}
	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	session := seedSession(t, h.store, agent.ID)
	before := folderCount(t, h)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/categories/folders/new", nil),
		httptest.NewRequest(http.MethodPost, "/categories/folders", strings.NewReader("name=Sneaky")),
		httptest.NewRequest(http.MethodPost, "/categories/folders/"+id(folder)+"/delete", nil),
	} {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", sessionCookie+"="+session.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as agent = %d, want 403", req.Method, req.URL.Path, rec.Code)
		}
	}
	if got := folderCount(t, h); got != before {
		t.Errorf("an agent changed folders")
	}
}

// TestCatalogFolderNavigationSwapsOnlyTheScreen: the sidebar, the breadcrumb
// and the folder rows navigate with HTMX into #categories-background, so that
// request answers the region alone, never the whole page inside itself.
func TestCatalogFolderNavigationSwapsOnlyTheScreen(t *testing.T) {
	h := newHarness(t)
	folder := seedFolder(t, h, nil, "IT services")
	req := httptest.NewRequest(http.MethodGet, "/categories?folder="+id(folder), nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "categories-background")
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(body, "<!DOCTYPE html>") || !strings.HasPrefix(strings.TrimSpace(body), `<div id="categories-background"`) {
		t.Fatalf("HTMX folder navigation must answer the screen region only, got %d: %.200s", rec.Code, body)
	}
	if !strings.Contains(body, `class="catalog-tree catalog-folder-tree"`) || !strings.Contains(body, "IT services") {
		t.Errorf("the region does not show the tree")
	}
	if rec.Header().Get("Vary") != "HX-Request" {
		t.Errorf("Vary = %q", rec.Header().Get("Vary"))
	}
}

// TestCatalogTopLevelCreationLinksOpenTheirDrawers: at the top level the header
// buttons carry folder 0, which means the top level, not an invalid folder.
func TestCatalogTopLevelCreationLinksOpenTheirDrawers(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/categories", false).Body.String()
	for _, path := range []string{"/categories/new?folder_id=0", "/categories/folders/new?parent_id=0"} {
		if !strings.Contains(body, `href="`+strings.ReplaceAll(path, "&", "&amp;")+`"`) {
			t.Fatalf("the header does not link %q", path)
		}
		rec := hxGet(t, h, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<option value="">Top level</option>`) {
			t.Errorf("GET %s = %d", path, rec.Code)
		}
	}
}
