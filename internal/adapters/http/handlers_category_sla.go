package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// CategorySLAHandlers own the per-category SLA configuration screen (issue
// #211): GET /categories/{id}/sla renders the category's materialized
// 4-priority x 2-milestone matrix, POST /categories/{id}/sla replaces it
// through SLAService.SetCategoryTargets. Both routes are gated on
// CapManageCategories (admin+) at the HTTP boundary; the application
// service re-enforces the same capability before mutating anything.
type CategorySLAHandlers struct {
	categories *application.CategoryService
	sla        *application.SLAService
	slaStore   application.SLAStore
	renderer   *Renderer
}

// NewCategorySLAHandlers wires the category SLA routes against the category
// and SLA use cases and the SLA store port the materialized matrix is read
// through (the application service exposes no read use case for it).
func NewCategorySLAHandlers(categories *application.CategoryService, sla *application.SLAService, slaStore application.SLAStore, renderer *Renderer) *CategorySLAHandlers {
	return &CategorySLAHandlers{categories: categories, sla: sla, slaStore: slaStore, renderer: renderer}
}

// Register mounts the category SLA routes.
func (h *CategorySLAHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /categories/{id}/sla", h.get)
	mux.HandleFunc("POST /categories/{id}/sla", h.post)
}

// slaTargetUnits is one milestone's target decomposed into the h/m/s units
// the shared SLA target grid renders. The decomposition round-trips the
// submitted form exactly, so a rejected post re-renders what the
// administrator actually typed.
type slaTargetUnits struct {
	Hours   int
	Minutes int
	Seconds int
}

// slaPriorityRow is one priority's row of the shared SLA target grid.
type slaPriorityRow struct {
	Priority      domain.Priority
	Label         string
	FirstResponse slaTargetUnits
	Resolve       slaTargetUnits
}

// slaGridData is the payload of the shared sla_target_grid partial.
type slaGridData struct {
	Rows []slaPriorityRow
}

// slaGridPriorities fixes the shared grid's display order: the store lists
// the matrix critical > high > medium > low, and the parsed post must build
// the same order.
var slaGridPriorities = []domain.Priority{
	domain.PriorityCritical,
	domain.PriorityHigh,
	domain.PriorityMedium,
	domain.PriorityLow,
}

// slaPolicyRows decomposes each policy's second counts into the grid's
// h/m/s units. A priority without a row renders as a zero-filled row, so a
// matrix the application service would reject as incomplete still renders
// every field the administrator needs to fill in.
func slaPolicyRows(policies []domain.SLAPolicy) []slaPriorityRow {
	byPriority := make(map[domain.Priority]domain.SLAPolicy, len(policies))
	for _, p := range policies {
		byPriority[p.Priority] = p
	}
	rows := make([]slaPriorityRow, 0, len(slaGridPriorities))
	for _, priority := range slaGridPriorities {
		p := byPriority[priority]
		rows = append(rows, slaPriorityRow{
			Priority:      priority,
			Label:         humanizeLabel(priority),
			FirstResponse: slaUnits(p.FirstResponseSeconds),
			Resolve:       slaUnits(p.ResolveSeconds),
		})
	}
	return rows
}

// slaUnits decomposes a target in seconds into h/m/s. The three units keep
// one shared sign, so the h*3600 + m*60 + s reconstruction is exact for
// negative submissions too.
func slaUnits(total int) slaTargetUnits {
	negative := total < 0
	if negative {
		total = -total
	}
	units := slaTargetUnits{Hours: total / 3600, Minutes: total % 3600 / 60, Seconds: total % 60}
	if negative {
		units.Hours, units.Minutes, units.Seconds = -units.Hours, -units.Minutes, -units.Seconds
	}
	return units
}

// parseSLATargets reconstructs the four-priority matrix from the shared
// grid's field names (first_response_h_<priority>, ...). A blank or
// non-numeric unit counts as 0, so the application service's own validation
// (all four priorities present, every target at least 60 seconds) produces
// the rejection rather than the parser.
func parseSLATargets(r *http.Request) []domain.SLAPolicy {
	policies := make([]domain.SLAPolicy, 0, len(slaGridPriorities))
	for _, priority := range slaGridPriorities {
		policies = append(policies, domain.SLAPolicy{
			Priority:             priority,
			FirstResponseSeconds: slaUnitSeconds(r, "first_response", priority),
			ResolveSeconds:       slaUnitSeconds(r, "resolve", priority),
		})
	}
	return policies
}

// slaUnitSeconds reconstructs one milestone's target as h*3600 + m*60 + s.
func slaUnitSeconds(r *http.Request, milestone string, priority domain.Priority) int {
	return 3600*slaUnit(r, milestone, "h", priority) +
		60*slaUnit(r, milestone, "m", priority) +
		slaUnit(r, milestone, "s", priority)
}

// slaUnit reads one h/m/s unit of the grid, 0 when absent or non-numeric.
func slaUnit(r *http.Request, milestone, unit string, priority domain.Priority) int {
	return slaFormInt(r, milestone+"_"+unit+"_"+string(priority))
}

// categorySLAData is the category SLA screen payload; Error carries a
// rejected-save message (422/403 re-render).
type categorySLAData struct {
	pageData
	CategoryID   int64
	CategoryName string
	Error        string
	Grid         slaGridData
}

func (h *CategorySLAHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	categoryID, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	category, err := h.categories.GetByID(r.Context(), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	policies, err := h.slaStore.ListByCategory(r.Context(), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	h.render(w, r, category, slaPolicyRows(policies), "", http.StatusOK)
}

func (h *CategorySLAHandlers) post(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	categoryID, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	category, err := h.categories.GetByID(r.Context(), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	// Parse the submitted matrix BEFORE loading anything for the error
	// path: a rejected save echoes the SUBMITTED values, not the stored
	// ones, so one bad number does not cost the administrator 24 fields.
	policies := parseSLATargets(r)
	if err := h.sla.SetCategoryTargets(r.Context(), *userFromContext(r.Context()), categoryID, policies); err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.render(w, r, category, slaPolicyRows(policies), msg, status)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, categorySLAPath(categoryID))
}

// render draws the category SLA screen with the given rows and error.
func (h *CategorySLAHandlers) render(w http.ResponseWriter, r *http.Request, category *domain.Category, rows []slaPriorityRow, errorMessage string, status int) {
	data := categorySLAData{
		pageData:     pageDataFrom(r, "categories"),
		CategoryID:   category.ID,
		CategoryName: category.Name,
		Error:        errorMessage,
		Grid:         slaGridData{Rows: rows},
	}
	data.PageFoundationAssets = true
	h.renderer.Render(w, r, "category_sla", "", data, status)
}

// categorySLAPath is the canonical location of the category SLA screen.
func categorySLAPath(categoryID int64) string {
	return "/categories/" + strconv.FormatInt(categoryID, 10) + "/sla"
}
