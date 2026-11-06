package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// The requester catalog over folders (issue #484, S7, S9, S11): the two-step
// catalog shows one folder level at a time with a breadcrumb, hides folders
// that offer nothing, skips pass-through folders, and flattens search into
// categories with their path; the single-page picker groups by folder path.

// requesterCatalogFixture is the artboard's catalog:
//
//	IT services › Hardware › Laptops       Laptop repair (published)
//	IT services › Software › Licenses › Design tools   Figma seat (published)
//	IT services › Access                   VPN access (not published)
//	Facilities › Office                    Desk booking (not published)
//	(top level)                            General question (published)
type requesterCatalogFixture struct {
	it, hardware, laptops, software, licenses, design, access, facilities int64
	repair, figma, vpn, general                                           int64
}

func newRequesterCatalog(t *testing.T, h *harness) requesterCatalogFixture {
	t.Helper()
	if _, err := h.rawDB(t).Exec(`DELETE FROM folders WHERE parent_id IS NOT NULL; DELETE FROM folders`); err != nil {
		t.Fatal(err)
	}
	var f requesterCatalogFixture
	f.it = seedFolder(t, h, nil, "IT services")
	f.hardware = seedFolder(t, h, &f.it, "Hardware")
	f.laptops = seedFolder(t, h, &f.hardware, "Laptops")
	f.software = seedFolder(t, h, &f.it, "Software")
	f.licenses = seedFolder(t, h, &f.software, "Licenses")
	f.design = seedFolder(t, h, &f.licenses, "Design tools")
	f.access = seedFolder(t, h, &f.it, "Access")
	f.facilities = seedFolder(t, h, nil, "Facilities")
	office := seedFolder(t, h, &f.facilities, "Office")
	desk := h.seededDesk(t)
	h.staff(t, desk)
	create := func(name string, folder *int64, publish bool) int64 {
		c, err := h.categories.CreateInFolderFor(t.Context(), *h.admin, name, name+" help", "", folder)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if publish {
			h.publishWorkflow(t, c.ID, simpleManualDef())
		}
		return c.ID
	}
	f.repair = create("Laptop repair", &f.laptops, true)
	f.figma = create("Figma seat", &f.design, true)
	f.vpn = create("VPN access", &f.access, false)
	create("Desk booking", &office, false)
	f.general = create("General question", nil, true)
	// The harness's Bugs category is published at the top level too; it stays.
	return f
}

func TestRequesterCatalogShowsOneLevelAndHidesEmptyBranches(t *testing.T) {
	h := newHarness(t)
	f := newRequesterCatalog(t, h)
	setCreationMode(t, h, application.TicketCreationTwoStep)
	mux := createModeHarness(t, h)

	status, body := getPath(t, h, mux, "/tickets/new")
	if status != http.StatusOK {
		t.Fatalf("GET /tickets/new = %d", status)
	}
	for _, want := range []string{
		`href="/tickets/new?folder=` + id(f.it) + `"`,
		`<span class="create-card-name">IT services</span>`,
		`<span class="create-card-name">General question</span>`,
		`<span aria-current="page">All requests</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("top level missing %q", want)
		}
	}
	// Facilities offers no published category anywhere below it.
	if strings.Contains(body, ">Facilities<") {
		t.Errorf("a folder with nothing published is offered")
	}
	// Categories of deeper levels are not on the top level's view.
	level := body[strings.Index(body, "data-catalog-level"):]
	level = level[:strings.Index(level, "data-catalog-flat")]
	if strings.Contains(level, "Laptop repair") {
		t.Errorf("the top level lists a category from a deeper folder")
	}

	_, it := getPath(t, h, mux, "/tickets/new?folder="+id(f.it))
	for _, want := range []string{
		`<a href="/tickets/new">All requests</a>`,
		`<span aria-current="page">IT services</span>`,
		`<span class="create-card-name">Hardware</span>`,
		`<span class="create-card-name">Software</span>`,
		`href="/tickets/new" class="btn ghost create-catalog-back"`,
	} {
		if !strings.Contains(it, want) {
			t.Errorf("IT services level missing %q", want)
		}
	}
	if strings.Contains(it, `<span class="create-card-name">Access</span>`) {
		t.Errorf("Access offers only an unpublished category and must be hidden")
	}
}

func TestRequesterCatalogSkipsPassThroughFolders(t *testing.T) {
	h := newHarness(t)
	f := newRequesterCatalog(t, h)
	setCreationMode(t, h, application.TicketCreationTwoStep)
	mux := createModeHarness(t, h)

	_, it := getPath(t, h, mux, "/tickets/new?folder="+id(f.it))
	// Software holds only Licenses, which holds only Design tools: the card
	// opens Design tools directly. Hardware holds only Laptops (Peripherals is
	// absent here), so it opens Laptops.
	if !strings.Contains(it, `href="/tickets/new?folder=`+id(f.design)+`"`) {
		t.Errorf("Software must link straight to Design tools")
	}
	if !strings.Contains(it, `href="/tickets/new?folder=`+id(f.laptops)+`"`) {
		t.Errorf("Hardware must link straight to Laptops")
	}

	// Asking for a pass-through folder lands on its deepest single child too.
	_, design := getPath(t, h, mux, "/tickets/new?folder="+id(f.software))
	for _, want := range []string{
		`<a href="/tickets/new?folder=` + id(f.it) + `">IT services</a>`,
		`<a href="/tickets/new?folder=` + id(f.software) + `">Software</a>`,
		`<a href="/tickets/new?folder=` + id(f.licenses) + `">Licenses</a>`,
		`<span aria-current="page">Design tools</span>`,
		`href="/tickets/new?category_id=` + id(f.figma) + `"`,
		// Back skips the pass-through chain and returns to IT services.
		`href="/tickets/new?folder=` + id(f.it) + `" class="btn ghost create-catalog-back"`,
	} {
		if !strings.Contains(design, want) {
			t.Errorf("Design tools level missing %q", want)
		}
	}

	// A folder the requester cannot see, or a malformed one, shows the top level.
	for _, target := range []string{"/tickets/new?folder=" + id(f.facilities), "/tickets/new?folder=abc"} {
		status, body := getPath(t, h, mux, target)
		if status != http.StatusOK || !strings.Contains(body, `<span aria-current="page">All requests</span>`) {
			t.Errorf("GET %s = %d, want the top level", target, status)
		}
	}
}

func TestRequesterCatalogSearchListsCategoriesWithTheirPath(t *testing.T) {
	h := newHarness(t)
	f := newRequesterCatalog(t, h)
	setCreationMode(t, h, application.TicketCreationTwoStep)
	mux := createModeHarness(t, h)

	_, body := getPath(t, h, mux, "/tickets/new?q=licenses")
	flat := body[strings.Index(body, "data-catalog-flat"):]
	if !strings.Contains(flat, `href="/tickets/new?category_id=`+id(f.figma)+`"`) ||
		!strings.Contains(flat, "IT services › Software › Licenses › Design tools") {
		t.Errorf("a folder-name search must list Figma seat with its path")
	}
	if strings.Contains(flat, `href="/tickets/new?category_id=`+id(f.repair)+`" hidden`) == false && strings.Contains(flat, `category_id=`+id(f.repair)) {
		// Laptop repair is rendered for the live filter but hidden by the server.
		t.Errorf("a non-matching category is not hidden")
	}
	if !strings.Contains(body, `class="create-catalog-level" data-catalog-level hidden`) {
		t.Errorf("the folder level must be hidden while searching")
	}
	if strings.Contains(flat, "folder=") {
		t.Errorf("search results must be categories only, never folders")
	}
	if !strings.Contains(body, `data-catalog-no-match hidden`) {
		t.Errorf("a search with matches must keep the no-match message hidden")
	}
	// General is the legacy desk name, but only General question matches by
	// category name. The unrelated repair remains hidden on the native GET path.
	status, deskSearch := getPath(t, h, mux, "/tickets/new?q=General")
	if status != http.StatusOK || !strings.Contains(deskSearch, `href="/tickets/new?category_id=`+id(f.repair)+`" hidden`) {
		t.Error("desk-only matches must be hidden")
	}
	if strings.Contains(deskSearch, "Laptops · General") {
		t.Error("catalog captions must not present a category-owned desk")
	}
	_, none := getPath(t, h, mux, "/tickets/new?q=zzzz")
	if !strings.Contains(none, "No requests match your search") || strings.Contains(none, `data-catalog-no-match hidden`) {
		t.Errorf("an empty search must say so")
	}
}

func TestRequesterPickerGroupsByFolderPath(t *testing.T) {
	h := newHarness(t)
	f := newRequesterCatalog(t, h)
	mux := createModeHarness(t, h)

	_, body := getPath(t, h, mux, "/tickets/new")
	for _, want := range []string{
		`<optgroup label="IT services › Hardware › Laptops">`,
		`<optgroup label="IT services › Software › Licenses › Design tools">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("single-page picker missing group %q", want)
		}
	}
	if !strings.Contains(body, `data-picker-search="Search categories or folders"`) || !strings.Contains(body, `<select id="category_id" name="category_id"`) {
		t.Error("picker must retain its native select and describe desk-free search")
	}
	if strings.Contains(body, "VPN access") {
		t.Errorf("an unpublished category is offered")
	}

	setCreationMode(t, h, application.TicketCreationTwoStep)
	_, form := getPath(t, h, mux, "/tickets/new?category_id="+id(f.figma))
	if !strings.Contains(form, "IT services › Software › Licenses › Design tools › Figma seat") {
		t.Errorf("the fixed category must show its folder path")
	}
}
