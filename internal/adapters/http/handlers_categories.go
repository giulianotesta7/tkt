package httpadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// CategoryHandlers serve the Categories screen (issue #484): a folder tree of
// unlimited depth holding categories in one tree. Folders are created, renamed, moved and deleted here; a
// category is created in a folder and moves between folders without touching
// its workflow or its tickets. Its workflow steps decide which desk handles it
// (issue #491); desks are managed on /desks.
type CategoryHandlers struct {
	categories *application.CategoryService
	workflows  *application.WorkflowService
	renderer   *Renderer
	catalog    *application.CatalogService
	folders    *application.FolderService
	appSet     application.SettingsStore
}

// NewCategoryHandlers wires category routes without workflow projections. It
// remains available for focused legacy handler tests.
func NewCategoryHandlers(categories *application.CategoryService, renderer *Renderer) *CategoryHandlers {
	return &CategoryHandlers{categories: categories, renderer: renderer}
}

// NewCategoryHandlersWithWorkflows adds derived workflow badges to the category
// index used by the production and integration composition roots.
func NewCategoryHandlersWithWorkflows(categories *application.CategoryService, workflows *application.WorkflowService, renderer *Renderer, catalogs ...*application.CatalogService) *CategoryHandlers {
	h := &CategoryHandlers{categories: categories, workflows: workflows, renderer: renderer}
	if len(catalogs) > 0 {
		h.catalog = catalogs[0]
	}
	return h
}

// WithSettings supplies the instance settings the catalog reads. The only
// setting it needs is whether SLA is enabled at all: with SLA off, a row shows no
// SLA state instead of claiming "Default SLA" for a feature nobody turned on.
func (h *CategoryHandlers) WithSettings(settings application.SettingsStore) *CategoryHandlers {
	h.appSet = settings
	return h
}

// WithFolders supplies the folder tree (issue #484).
func (h *CategoryHandlers) WithFolders(folders *application.FolderService) *CategoryHandlers {
	h.folders = folders
	return h
}

// Register mounts the category and folder routes.
func (h *CategoryHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /categories", h.index)
	mux.HandleFunc("GET /categories/new", h.newForm)
	mux.HandleFunc("POST /categories", h.create)
	mux.HandleFunc("GET /categories/{id}/edit", h.editForm)
	mux.HandleFunc("POST /categories/{id}/edit", h.update)
	mux.HandleFunc("POST /categories/{id}/rename", h.renameCategory)
	mux.HandleFunc("GET /categories/{id}/details", h.categoryDetails)
	mux.HandleFunc("POST /categories/{id}/delete", h.delete)
	mux.HandleFunc("GET /categories/{id}/move", h.moveCategoryForm)
	mux.HandleFunc("POST /categories/{id}/move", h.moveCategory)
	mux.HandleFunc("GET /categories/folders/new", h.newFolderForm)
	mux.HandleFunc("POST /categories/folders", h.createFolder)
	mux.HandleFunc("GET /categories/folders/{id}/edit", h.editFolderForm)
	mux.HandleFunc("POST /categories/folders/{id}/edit", h.updateFolder)
	mux.HandleFunc("POST /categories/folders/{id}/rename", h.renameFolder)
	mux.HandleFunc("GET /categories/folders/{id}/details", h.folderDetails)
	mux.HandleFunc("GET /categories/folders/{id}/move", h.moveFolderForm)
	mux.HandleFunc("POST /categories/folders/{id}/move", h.moveFolder)
	mux.HandleFunc("POST /categories/folders/{id}/delete", h.deleteFolder)
}

// categoriesIndexData is the Categories screen payload.
type categoriesIndexData struct {
	pageData
	Error          string
	State          categoryViewState
	View           catalogView
	MoveFolders    []folderOption
	SearchQuery    string
	Attention      bool
	SearchClearURL string
	AttentionURL   string
	Drawer         *categoryDrawerData
}

// categoryDrawerData is the drawer of the Categories screen and of the desks
// page: a category, a folder, a move, or (on /desks) a desk.
type categoryDrawerData struct {
	Kind        string // "category", "folder", "move" or "desk"
	ID          int64
	Name        string
	Description string
	// Keywords is the suggestion keywords field the category drawer alone
	// renders (issue #416, S4).
	Keywords string
	// FolderID is the category's folder, the new folder's parent, or the move
	// destination; 0 is the top level.
	FolderID int64
	// DeskID is the desk the /desks drawer edits; categories name no desk.
	DeskID  int64
	Folders []folderOption
	// MoveKind names what a move drawer moves: "folder" or "category".
	MoveKind  string
	Members   []domain.User
	Eligible  []domain.User
	ActionURL string
	CloseURL  string
	State     categoryViewState
	Error     string
	// HasServerError keeps the drawer dirty after a refused submission, so the
	// typed values are not dropped by a close without confirmation.
	HasServerError bool
	InvalidName    bool
	InvalidFolder  bool
	ReadError      error
	// Standalone marks the desk drawer as the /desks page opens it: member
	// forms keep the desks page's context.
	Standalone bool
}

// cannotRunLabel is the ONE place the "cannot run" fact is put into words. The
// admin's categories screen and the requester's ticket picker both need it, and
// two compositions of the same fact would drift into two vocabularies for one
// state — the thing WorkflowSummary exists to prevent.
// categoryBadge is the one line a category row shows for its workflow state.
//
// It is composed here, from facts, rather than in the store, so the wording is a
// presentation concern; and it is the only composition, so no surface invents a
// competing vocabulary. Before this, the store returned one of three words and
// "published with edits not yet live" rendered as the same "Draft" as "never
// published" — two different governance states behind one word.
func categoryBadge(s application.WorkflowSummary) application.Msg {
	switch {
	case s.Version == 0 && !s.HasDraft:
		return application.MsgRef("categories.badge.not_configured")
	case s.Version == 0:
		return application.MsgRef("categories.badge.draft_only")
	case !s.CannotRun.Empty():
		// Published, and unable to move a ticket. Without this the category looked
		// exactly like a healthy one while every ticket filed against it failed.
		// The reason is a catalogue reference (or data), composed as a nested
		// sentence so it follows the instance language.
		return application.MsgRef("categories.badge.cannot_run", application.Nested(s.CannotRun))
	case s.PendingSteps == 1:
		return application.MsgRef("categories.badge.published_one_change", application.Text(strconv.Itoa(s.Version)))
	case s.PendingSteps > 1:
		return application.MsgRef("categories.badge.published_many_changes",
			application.Text(strconv.Itoa(s.Version)), application.Text(strconv.Itoa(s.PendingSteps)))
	default:
		return application.MsgRef("categories.badge.published", application.Text(strconv.Itoa(s.Version)))
	}
}

func setCategoriesVary(w http.ResponseWriter) {
	w.Header().Set("Vary", "HX-Request")
}

func categoryID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}

// optionalFolderID reads a folder select or link: empty or 0 is the top level
// (nil).
func optionalFolderID(raw string) (*int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, &domain.ValidationError{Field: "folder_id", Message: "invalid identifier", Key: "error.invalid_identifier"}
	}
	return &id, nil
}

func folderValue(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func (h *CategoryHandlers) index(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	// Folder navigation (sidebar, breadcrumb, folder rows) swaps the screen
	// region in place, so that request answers the region alone.
	if r.Header.Get("HX-Request") != "" && r.Header.Get("HX-Target") == "categories-background" {
		data, err := h.indexData(r, state, "")
		if err != nil {
			http.Error(w, mapErrorMsg(r, err), statusFor(err))
			return
		}
		h.renderer.Render(w, r, "categories_index", "categories_background", data, http.StatusOK)
		return
	}
	h.renderIndex(w, r, state, "", nil, http.StatusOK)
}

func (h *CategoryHandlers) indexData(r *http.Request, state categoryViewState, message string) (categoriesIndexData, error) {
	data := categoriesIndexData{
		pageData:    pageDataFrom(r, "categories"),
		Error:       message,
		State:       state,
		SearchQuery: state.Query,
		Attention:   state.Attention,
	}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	data.SearchClearURL = categoryStatePath(categoryViewState{Folder: state.Folder, Attention: state.Attention})
	data.AttentionURL = categoryStatePath(categoryViewState{Folder: state.Folder, Query: state.Query, Attention: !state.Attention})

	categories, err := h.categories.List(r.Context())
	if err != nil {
		return data, err
	}
	in := catalogViewInput{
		Categories:  categories,
		Places:      make(map[int64]*int64, len(categories)),
		States:      make(map[int64]catalogCategoryState, len(categories)),
		SLADefaults: map[int64]bool{},
		Query:       state.Query,
		Attention:   state.Attention,
	}
	for _, c := range categories {
		in.Places[c.ID] = c.FolderID
	}
	// The badge and tone come from the same workflow summary.
	if h.workflows != nil {
		actor := *userFromContext(r.Context())
		summaries, err := h.workflows.ListSummaries(r.Context(), actor)
		if err != nil {
			return data, err
		}
		for _, summary := range summaries {
			in.States[summary.CategoryID] = catalogCategoryStateOf(summary)
		}
	}
	if h.catalog != nil {
		var err error
		if in.SLAEnabled, err = h.slaEnabled(r); err != nil {
			return data, err
		}
		if in.SLAEnabled {
			defaults, err := h.catalog.ListDefaultSLACategories(r.Context())
			if err != nil {
				return data, err
			}
			for _, id := range defaults {
				in.SLADefaults[id] = true
			}
		}
	}
	if h.folders != nil {
		tree, err := h.folders.Tree(r.Context())
		if err != nil {
			return data, err
		}
		in.Folders = tree.All()
		data.MoveFolders = catalogFolderOptions(in.Folders, 0)
	}
	data.View = buildCatalogView(in)
	return data, nil
}

// slaEnabled reports whether the instance has per-category SLA switched on. A
// handler composed without the settings port (focused legacy tests) answers
// false rather than guessing, so no row claims an SLA state nobody configured.
func (h *CategoryHandlers) slaEnabled(r *http.Request) (bool, error) {
	if h.appSet == nil {
		return false, nil
	}
	return h.appSet.GetSLAEnabled(r.Context())
}

func (h *CategoryHandlers) renderIndex(w http.ResponseWriter, r *http.Request, state categoryViewState, message string, drawer *categoryDrawerData, status int) {
	data, err := h.indexData(r, state, message)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	data.Drawer = drawer
	h.renderer.Render(w, r, "categories_index", "", data, status)
}

// renderBackground answers an HTMX request with the refreshed screen region.
func (h *CategoryHandlers) renderBackground(w http.ResponseWriter, r *http.Request, state categoryViewState, message string, status int) {
	data, err := h.indexData(r, state, message)
	if err != nil {
		h.renderRefreshError(w, r, state)
		return
	}
	setCategoriesVary(w)
	w.Header().Set("HX-Retarget", "#categories-background")
	w.Header().Set("HX-Reswap", "outerHTML")
	w.Header().Set("HX-Push-Url", categoryStatePath(state))
	h.renderer.Render(w, r, "categories_index", "categories_background", data, status)
}

// renderRefreshError reports a committed mutation separately from a failed
// post-commit refresh. It answers a successful swap, so HTMX does not treat the
// response as a failed mutation and retry it.
func (h *CategoryHandlers) renderRefreshError(w http.ResponseWriter, r *http.Request, state categoryViewState) {
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, categoryStatePath(state))
		return
	}
	data := categoriesIndexData{pageData: pageDataFrom(r, "categories"), Error: i18n.T(requestLocale(r), "categories.refresh_error"), State: state}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	w.Header().Set("HX-Retarget", "#categories-background")
	w.Header().Set("HX-Reswap", "outerHTML")
	w.Header().Set("HX-Push-Url", categoryStatePath(state))
	w.Header().Set("HX-Trigger-After-Swap", "categories:saved")
	h.renderer.Render(w, r, "categories_index", "categories_background", data, http.StatusOK)
}

// afterMutation answers a successful write: the refreshed region for HTMX
// (which closes the drawer), a redirect otherwise.
func (h *CategoryHandlers) afterMutation(w http.ResponseWriter, r *http.Request, state categoryViewState) {
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, categoryStatePath(state))
		return
	}
	w.Header().Set("HX-Trigger-After-Swap", "categories:saved")
	h.renderBackground(w, r, state, "", http.StatusOK)
}

// mutationState validates the view a mutation returns to BEFORE the write, so
// a malformed state is refused with nothing stored.
func (h *CategoryHandlers) mutationState(w http.ResponseWriter, r *http.Request) (categoryViewState, bool) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return categoryViewState{}, false
	}
	state, err := categoryStateFrom(r.Form)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return categoryViewState{}, false
	}
	return state, true
}

// refuseInPlace answers a refused delete or move with the screen and the
// message, keeping the view the request came from.
func (h *CategoryHandlers) refuseInPlace(w http.ResponseWriter, r *http.Request, state categoryViewState, message string, status int) {
	if r.Header.Get("HX-Request") == "" {
		h.renderIndex(w, r, state, message, nil, status)
		return
	}
	h.renderBackground(w, r, state, message, status)
}

// drawer builds a drawer with the folder choices it may need.
func (h *CategoryHandlers) drawer(r *http.Request, kind string, state categoryViewState, exclude int64) *categoryDrawerData {
	d := &categoryDrawerData{Kind: kind, State: state, CloseURL: categoryStatePath(state)}
	if h.folders != nil {
		tree, err := h.folders.Tree(r.Context())
		if err != nil {
			d.ReadError = err
			return d
		}
		d.Folders = catalogFolderOptions(tree.All(), exclude)
	}
	return d
}

func (h *CategoryHandlers) renderDrawer(w http.ResponseWriter, r *http.Request, d *categoryDrawerData, status int) {
	setCategoriesVary(w)
	if d.ReadError != nil {
		http.Error(w, mapErrorMsg(r, d.ReadError), statusFor(d.ReadError))
		return
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Retarget", "#category-drawer-host")
		w.Header().Set("HX-Reswap", "outerHTML")
		h.renderer.Render(w, r, "categories_index", "category_drawer", d, status)
		return
	}
	h.renderIndex(w, r, d.State, "", d, status)
}

// formError re-renders a drawer with what was submitted and the refusal.
func (h *CategoryHandlers) formError(w http.ResponseWriter, r *http.Request, d *categoryDrawerData, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	d.Error, d.HasServerError = msg, true
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		switch validation.Field {
		case "name":
			d.InvalidName = true
		case "folder_id":
			d.InvalidFolder = true
		}
	case errors.Is(err, domain.ErrDuplicate):
		d.InvalidName = true
	case errors.Is(err, domain.ErrFolderCycle), errors.Is(err, domain.ErrNotFound):
		d.InvalidFolder = true
	}
	h.renderDrawer(w, r, d, status)
}

// inlineWrite owns the inline response contract while the existing drawer paths
// retain their historical behaviour.
func (h *CategoryHandlers) inlineWrite(w http.ResponseWriter, r *http.Request, kind, action string) {
	state, err := categoryStateFrom(r.Form)
	if err != nil {
		h.inlineRefusal(w, r, err, kind, false)
		return
	}
	ctx := r.Context()
	actor := *userFromContext(ctx)
	id := parseID(r.PathValue("id"))
	name := ""
	var oldParent, parent *int64
	var tree domain.CatalogTree
	if h.folders != nil {
		tree, err = h.folders.Tree(ctx)
	}
	if err == nil && action != "created" {
		if id == 0 {
			err = &domain.ValidationError{Field: "", Message: "invalid identifier", Key: "error.invalid_identifier"}
		} else if kind == "category" {
			var c *domain.Category
			c, err = h.categories.GetByID(ctx, id)
			if err == nil {
				name, oldParent = c.Name, c.FolderID
			}
		} else {
			f, found := tree.Folder(id)
			if !found {
				err = &domain.NotFoundError{Kind: "folder", ID: id}
			} else {
				name, oldParent = f.Name, f.ParentID
			}
		}
	}
	if err == nil && (action == "created" || action == "moved" || (action == "saved" && kind == "category")) {
		parent, err = optionalFolderID(r.Form.Get("folder_id"))
	}
	if err == nil {
		switch action {
		case "created":
			if kind == "folder" {
				var f *domain.Folder
				f, err = h.folders.CreateFor(ctx, actor, parent, r.Form.Get("name"), r.Form.Get("description"))
				if err == nil {
					id, name = f.ID, f.Name
				}
			} else {
				var c *domain.Category
				c, err = h.categories.CreateInFolderFor(ctx, actor, r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords"), parent)
				if err == nil {
					id, name = c.ID, c.Name
				}
			}
		case "saved":
			if kind == "folder" {
				var f *domain.Folder
				f, err = h.folders.UpdateFor(ctx, actor, id, r.Form.Get("name"), r.Form.Get("description"))
				if err == nil {
					name = f.Name
				}
			} else {
				var c *domain.Category
				c, err = h.categories.GetByID(ctx, id)
				if err == nil {
					c.Name, c.Description, c.Keywords, c.FolderID = r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords"), parent
					if h.catalog != nil {
						err = h.catalog.UpdateCategoryFor(ctx, actor, c)
					} else {
						_, err = h.categories.RenameFor(ctx, actor, id, c.Name)
					}
					name = strings.TrimSpace(c.Name)
				}
			}
		case "renamed":
			if kind == "folder" {
				f, _ := tree.Folder(id)
				var updated *domain.Folder
				updated, err = h.folders.UpdateFor(ctx, actor, id, r.Form.Get("name"), f.Description)
				if err == nil {
					name = updated.Name
				}
			} else {
				var updated *domain.Category
				updated, err = h.categories.RenameFor(ctx, actor, id, r.Form.Get("name"))
				if err == nil {
					name = updated.Name
				}
			}
		case "moved":
			if kind == "folder" {
				err = h.folders.MoveFor(ctx, actor, id, parent)
			} else {
				err = h.folders.PlaceCategoryFor(ctx, actor, id, parent)
			}
		case "deleted":
			if kind == "folder" {
				err = h.folders.DeleteFor(ctx, actor, id)
			} else {
				err = h.categories.DeleteFor(ctx, actor, id)
			}
		}
	}
	if err != nil {
		h.inlineRefusal(w, r, err, kind, kind == "folder" && action == "deleted")
		return
	}
	if action == "renamed" && r.Form.Get("inline") != "1" {
		redirect(w, r, "/categories")
		return
	}
	tr := i18n.New(requestLocale(r))
	event := map[string]any{"kind": kind, "id": id, "name": name}
	if action == "created" {
		event["folderID"] = folderValue(parent)
		if kind == "folder" {
			event["message"] = tr.Tf("categories.inline.created_folder", name)
		} else {
			event["message"] = tr.Tf("categories.inline.created_category", name)
		}
	} else if action == "moved" {
		path := strings.Join(tree.PathNames(parent), " › ")
		event["folderID"], event["fromFolderID"], event["path"] = folderValue(parent), folderValue(oldParent), path
		if path == "" {
			event["message"] = tr.Tf("categories.inline.moved_top", name)
		} else {
			event["message"] = tr.Tf("categories.inline.moved", name, path)
		}
	} else if action == "renamed" {
		event["message"] = tr.Tf("categories.inline.renamed", name)
	} else if action == "saved" {
		event["message"] = tr.Tf("categories.inline.saved", name)
	} else {
		event["message"] = tr.Tf("categories.inline.deleted", name)
	}
	data, err := h.indexData(r, state, "")
	if err != nil {
		h.renderRefreshError(w, r, state)
		return
	}
	trigger, _ := json.Marshal(map[string]any{"catalog:" + action: event})
	// HTTP header bytes are decoded as Latin-1 by browsers. JSON escapes keep
	// this catalog metadata ASCII while preserving the decoded event values.
	var header strings.Builder
	for _, r := range string(trigger) {
		if r < 0x80 {
			header.WriteRune(r)
		} else {
			for _, unit := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&header, `\u%04x`, unit)
			}
		}
	}
	w.Header().Set("HX-Trigger", header.String())
	h.renderer.RenderFragment(w, r, "categories_background", data, http.StatusOK)
}

func (h *CategoryHandlers) inlineRefusal(w http.ResponseWriter, r *http.Request, err error, kind string, folderDelete bool) {
	status, message := mapErrorFor(r, err)
	if folderDelete && errors.Is(err, domain.ErrReferenced) {
		message = i18n.T(requestLocale(r), "categories.folder_not_empty")
	}
	field := ""
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		if validation.Field == "name" || validation.Field == "folder_id" {
			field = validation.Field
		}
		if field == "name" {
			if kind == "folder" {
				message = i18n.T(requestLocale(r), "categories.inline.refusal_name_folder")
			} else {
				message = i18n.T(requestLocale(r), "categories.inline.refusal_name_category")
			}
		}
	case errors.Is(err, domain.ErrDuplicate):
		field = "name"
		if kind == "folder" {
			message = i18n.T(requestLocale(r), "categories.inline.refusal_duplicate_folder")
		} else {
			message = i18n.T(requestLocale(r), "categories.inline.refusal_duplicate_category")
		}
	case errors.Is(err, domain.ErrFolderCycle):
		field = "folder_id"
		message = i18n.T(requestLocale(r), "categories.inline.refusal_cycle")
	case errors.Is(err, domain.ErrNotFound):
		message = i18n.T(requestLocale(r), "categories.inline.refusal_not_found")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": message, "field": field}})
}

func (h *CategoryHandlers) renameCategory(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	_ = r.ParseMultipartForm(32 << 20)
	h.inlineWrite(w, r, "category", "renamed")
}

func (h *CategoryHandlers) renameFolder(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	_ = r.ParseMultipartForm(32 << 20)
	h.inlineWrite(w, r, "folder", "renamed")
}

type catalogDetailsData struct {
	Kind, Name, Description, Keywords string
	ID, FolderID                      int64
	NotEmpty                          bool
}

func (h *CategoryHandlers) categoryDetails(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	c, err := h.categories.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.drawer(r, "category", categoryViewState{}, 0)
	if d.ReadError != nil {
		http.Error(w, mapErrorMsg(r, d.ReadError), statusFor(d.ReadError))
		return
	}
	data := catalogDetailsData{Kind: "category", ID: id, Name: c.Name, Description: c.Description, Keywords: c.Keywords, FolderID: folderValue(c.FolderID)}
	h.renderer.RenderFragment(w, r, "catalog_details", data, http.StatusOK)
}

func (h *CategoryHandlers) folderDetails(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	id, ok := h.folderID(w, r)
	if !ok {
		return
	}
	tree, err := h.folders.Tree(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	f, found := tree.Folder(id)
	if !found {
		err = &domain.NotFoundError{Kind: "folder", ID: id}
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	categories, err := h.categories.List(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	notEmpty := len(tree.Children(&id)) > 0
	for _, c := range categories {
		if folderValue(c.FolderID) == id {
			notEmpty = true
			break
		}
	}
	data := catalogDetailsData{Kind: "folder", ID: id, Name: f.Name, Description: f.Description, NotEmpty: notEmpty}
	h.renderer.RenderFragment(w, r, "catalog_details", data, http.StatusOK)
}

// ---- categories ----

func (h *CategoryHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.drawer(r, "category", state, 0)
	d.ActionURL = "/categories"
	folder, err := optionalFolderID(r.URL.Query().Get("folder_id"))
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d.FolderID = folderValue(folder)
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *CategoryHandlers) categoryDrawerFromForm(r *http.Request, state categoryViewState, id int64) *categoryDrawerData {
	d := h.drawer(r, "category", state, 0)
	d.ID, d.Name, d.Description, d.Keywords = id, r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords")
	d.FolderID = parseID(r.Form.Get("folder_id"))
	d.ActionURL = "/categories"
	if id != 0 {
		d.ActionURL = "/categories/" + strconv.FormatInt(id, 10) + "/edit"
	}
	return d
}

func (h *CategoryHandlers) create(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "category", "created")
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	folder, err := optionalFolderID(r.Form.Get("folder_id"))
	if err == nil {
		_, err = h.categories.CreateInFolderFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords"), folder)
	}
	if err != nil {
		h.formError(w, r, h.categoryDrawerFromForm(r, state, 0), err)
		return
	}
	h.afterMutation(w, r, state)
}

func (h *CategoryHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	c, err := h.categories.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.drawer(r, "category", state, 0)
	d.ID, d.Name, d.Description, d.Keywords, d.FolderID = id, c.Name, c.Description, c.Keywords, folderValue(c.FolderID)
	d.ActionURL = "/categories/" + strconv.FormatInt(id, 10) + "/edit"
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *CategoryHandlers) update(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "category", "saved")
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	c, err := h.categories.GetByID(r.Context(), id)
	if err == nil {
		c.Name, c.Description, c.Keywords = r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords")
		if c.FolderID, err = optionalFolderID(r.Form.Get("folder_id")); err == nil {
			if h.catalog != nil {
				err = h.catalog.UpdateCategoryFor(r.Context(), *userFromContext(r.Context()), c)
			} else {
				_, err = h.categories.RenameFor(r.Context(), *userFromContext(r.Context()), id, c.Name)
			}
		}
	}
	if err != nil {
		h.formError(w, r, h.categoryDrawerFromForm(r, state, id), err)
		return
	}
	h.afterMutation(w, r, state)
}

func (h *CategoryHandlers) delete(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "category", "deleted")
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	if err := h.categories.DeleteFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.refuseInPlace(w, r, state, msg, status)
		return
	}
	h.afterMutation(w, r, state)
}

func (h *CategoryHandlers) moveCategoryForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	c, err := h.categories.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.drawer(r, "move", state, 0)
	d.MoveKind, d.ID, d.Name, d.FolderID = "category", id, c.Name, folderValue(c.FolderID)
	d.ActionURL = "/categories/" + strconv.FormatInt(id, 10) + "/move"
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *CategoryHandlers) moveCategory(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "category", "moved")
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	folder, err := optionalFolderID(r.Form.Get("folder_id"))
	if err == nil {
		err = h.folders.PlaceCategoryFor(r.Context(), *userFromContext(r.Context()), id, folder)
	}
	if err != nil {
		d := h.drawer(r, "move", state, 0)
		d.MoveKind, d.ID, d.Name, d.FolderID = "category", id, r.Form.Get("name"), parseID(r.Form.Get("folder_id"))
		d.ActionURL = "/categories/" + strconv.FormatInt(id, 10) + "/move"
		h.formError(w, r, d, err)
		return
	}
	h.afterMutation(w, r, state)
}

// ---- folders ----

func (h *CategoryHandlers) folderID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_identifier", http.StatusBadRequest)
	}
	return id, ok
}

func (h *CategoryHandlers) newFolderForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	parent, err := optionalFolderID(r.URL.Query().Get("parent_id"))
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.drawer(r, "folder", state, 0)
	d.FolderID = folderValue(parent)
	d.ActionURL = "/categories/folders"
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *CategoryHandlers) createFolder(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "folder", "created")
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	parent, err := optionalFolderID(r.Form.Get("folder_id"))
	if err == nil {
		_, err = h.folders.CreateFor(r.Context(), *userFromContext(r.Context()), parent, r.Form.Get("name"), r.Form.Get("description"))
	}
	if err != nil {
		d := h.drawer(r, "folder", state, 0)
		d.Name, d.Description, d.FolderID = r.Form.Get("name"), r.Form.Get("description"), parseID(r.Form.Get("folder_id"))
		d.ActionURL = "/categories/folders"
		h.formError(w, r, d, err)
		return
	}
	h.afterMutation(w, r, state)
}

func (h *CategoryHandlers) editFolderForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := h.folderID(w, r)
	if !ok {
		return
	}
	tree, err := h.folders.Tree(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	f, found := tree.Folder(id)
	if !found {
		http.Error(w, mapErrorMsg(r, &domain.NotFoundError{Kind: "folder", ID: id}), http.StatusNotFound)
		return
	}
	d := h.drawer(r, "folder", state, 0)
	d.ID, d.Name, d.Description, d.FolderID = id, f.Name, f.Description, folderValue(f.ParentID)
	d.ActionURL = "/categories/folders/" + strconv.FormatInt(id, 10) + "/edit"
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *CategoryHandlers) updateFolder(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "folder", "saved")
		return
	}
	id, ok := h.folderID(w, r)
	if !ok {
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	if _, err := h.folders.UpdateFor(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		d := h.drawer(r, "folder", state, 0)
		d.ID, d.Name, d.Description = id, r.Form.Get("name"), r.Form.Get("description")
		d.ActionURL = "/categories/folders/" + strconv.FormatInt(id, 10) + "/edit"
		h.formError(w, r, d, err)
		return
	}
	h.afterMutation(w, r, state)
}

func (h *CategoryHandlers) moveFolderForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	state, err := categoryStateFrom(r.URL.Query())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := h.folderID(w, r)
	if !ok {
		return
	}
	tree, err := h.folders.Tree(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	f, found := tree.Folder(id)
	if !found {
		http.Error(w, mapErrorMsg(r, &domain.NotFoundError{Kind: "folder", ID: id}), http.StatusNotFound)
		return
	}
	// A folder cannot move into itself or below itself, so those destinations
	// are not offered; the store refuses them anyway.
	d := h.drawer(r, "move", state, id)
	d.MoveKind, d.ID, d.Name, d.FolderID = "folder", id, f.Name, folderValue(f.ParentID)
	d.ActionURL = "/categories/folders/" + strconv.FormatInt(id, 10) + "/move"
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *CategoryHandlers) moveFolder(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "folder", "moved")
		return
	}
	id, ok := h.folderID(w, r)
	if !ok {
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	parent, err := optionalFolderID(r.Form.Get("folder_id"))
	if err == nil {
		err = h.folders.MoveFor(r.Context(), *userFromContext(r.Context()), id, parent)
	}
	if err != nil {
		d := h.drawer(r, "move", state, id)
		d.MoveKind, d.ID, d.Name, d.FolderID = "folder", id, r.Form.Get("name"), parseID(r.Form.Get("folder_id"))
		d.ActionURL = "/categories/folders/" + strconv.FormatInt(id, 10) + "/move"
		h.formError(w, r, d, err)
		return
	}
	h.afterMutation(w, r, state)
}

// deleteFolder removes an empty folder. A folder that still holds a folder or a
// category is refused with a message that says what to do, and nothing changes.
func (h *CategoryHandlers) deleteFolder(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.folders == nil {
		return
	}
	if r.FormValue("inline") == "1" {
		h.inlineWrite(w, r, "folder", "deleted")
		return
	}
	id, ok := h.folderID(w, r)
	if !ok {
		return
	}
	state, ok := h.mutationState(w, r)
	if !ok {
		return
	}
	if err := h.folders.DeleteFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		status, msg := mapErrorFor(r, err)
		if errors.Is(err, domain.ErrReferenced) {
			msg = i18n.T(requestLocale(r), "categories.folder_not_empty")
		}
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.refuseInPlace(w, r, state, msg, status)
		return
	}
	// The deleted folder can no longer be the selected one.
	if state.Folder == id {
		state.Folder = 0
	}
	h.afterMutation(w, r, state)
}
