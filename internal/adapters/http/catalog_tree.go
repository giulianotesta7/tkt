package httpadapter

import (
	"sort"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// catalogNamePart is one run of a node's name: either literal text or a search
// match. The template wraps the matched runs in <mark> and escapes the rest, so
// the highlight never needs the raw query written into the page as HTML.
type catalogNamePart struct {
	Text  string
	Match bool
}

// catalogName is a catalog node's name split for highlighting, plus the plain
// text the accessible labels ("Collapse <name>", the row's menu label) use.
type catalogName struct {
	Plain   string
	Parts   []catalogNamePart
	Matched bool
}

// catalogTreeCategory is one category row: the category level of the tree.
type catalogTreeCategory struct {
	ID       int64
	Name     catalogName
	Workflow application.Msg
	// WorkflowTone is the row's state tone: published, draft-only, missing or
	// unable to run. It only names a class; the wording stays in the catalogue.
	WorkflowTone string
	// SLAKey is the catalogue key for the SLA state, empty when the instance has
	// SLA disabled — the row then shows no SLA at all.
	SLAKey string
	// NeedsAttention is true when the category has no published workflow. A
	// category whose desk cannot run it is flagged at the desk level instead,
	// because the missing capability belongs to the desk.
	NeedsAttention bool
	OpenTickets    int
	Current        bool
	// DepartmentValue and DeskID are the row's own location, so its edit and
	// delete requests resubmit exactly the state the row shows instead of a
	// "selected" department the tree no longer has.
	DepartmentValue string
	DeskID          int64
}

// catalogTreeDesk is one desk row, with its categories.
type catalogTreeDesk struct {
	ID int64
	// Slug is the id the browser remembers a collapsed desk by ("k7").
	Slug       string
	Name       catalogName
	AgentCount int
	// NeedsAttention is true when nobody can take this desk's tickets, or when
	// one of its categories has no published workflow.
	NeedsAttention bool
	OpenTickets    int
	Categories     []catalogTreeCategory
	Current        bool
	// DepartmentValue is the department this desk resubmits in its own requests:
	// the real id, or "unassigned" for a legacy desk with no department.
	DepartmentValue string
}

// catalogTreeDepartment is one department row, with its desks.
type catalogTreeDepartment struct {
	ID int64
	// Slug is the id the browser remembers a collapsed department by ("d12").
	Slug        string
	Name        catalogName
	Description string
	DeskCount   int
	AgentCount  int
	// Virtual marks the presentation-only Unassigned group: desks whose stored
	// department is NULL. It is never a Department row and carries no actions.
	Virtual   bool
	Desks     []catalogTreeDesk
	Current   bool
	DeskMeta  application.Msg
	AgentMeta application.Msg
}

// catalogTree is the whole administration surface: one expandable tree of
// departments, desks and categories, plus the count the Needs attention chip
// shows. The count is computed BEFORE the filter, so the chip states how much
// work exists even while the page shows only part of it.
type catalogTree struct {
	Departments    []catalogTreeDepartment
	AttentionCount int
}

// catalogTreeInput is everything the tree needs from the page's reads. It is a
// struct rather than a long parameter list because the handler assembles it from
// four different ports.
type catalogTreeInput struct {
	Locale       string
	Departments  []domain.CatalogDepartment
	Desks        []domain.CatalogDesk
	Categories   []domain.Category
	States       map[int64]catalogCategoryState
	OpenTickets  map[int64]int
	SLADefaults  map[int64]bool
	SLAEnabled   bool
	Query        string
	Attention    bool
	DepartmentID int64
	DeskID       int64
}

// catalogCategoryState is one category's workflow fact as the row shows it: the
// composed badge (wording), the tone (colour) and whether a version is live.
// All three come from the same WorkflowSummary, so a row cannot say "published"
// in one column and "no workflow" in another.
type catalogCategoryState struct {
	Badge     application.Msg
	Tone      string
	Published bool
}

// catalogCategoryStateOf maps one workflow summary to the row's facts. A
// published definition that cannot move a ticket keeps today's wording and takes
// the warning tone, which is the anomaly the picker also refuses to hide.
func catalogCategoryStateOf(summary application.WorkflowSummary, attribution application.WorkflowAttribution) catalogCategoryState {
	state := catalogCategoryState{
		Badge:     categoryStatusLine(summary, attribution),
		Published: summary.Version > 0,
	}
	switch {
	case summary.Version == 0 && !summary.HasDraft:
		state.Tone = "none"
	case summary.Version == 0:
		state.Tone = "neutral"
	case !summary.CannotRun.Empty():
		state.Tone = "warn"
	default:
		state.Tone = "ok"
	}
	return state
}

// buildCatalogTree turns the page's reads into the tree the template renders.
//
// Order is part of the contract (S1): real departments by name, then the virtual
// Unassigned group when it holds desks; desks and categories by name inside
// their parent. Filters (S3 search, S4 attention) keep matching nodes plus their
// ancestors, so a match is never orphaned from the level that contains it.
func buildCatalogTree(in catalogTreeInput) catalogTree {
	unassignedName := i18n.T(in.Locale, "categories.unassigned.name")
	unassignedDescription := i18n.T(in.Locale, "categories.unassigned.description")

	byDesk := make(map[int64][]domain.Category, len(in.Categories))
	for _, category := range in.Categories {
		byDesk[category.DeskID] = append(byDesk[category.DeskID], category)
	}
	for deskID := range byDesk {
		categories := byDesk[deskID]
		sort.SliceStable(categories, func(i, j int) bool {
			return catalogSortKey(categories[i].Name, categories[i].ID) < catalogSortKey(categories[j].Name, categories[j].ID)
		})
		byDesk[deskID] = categories
	}

	byDepartment := make(map[int64][]domain.CatalogDesk, len(in.Departments)+1)
	for _, desk := range in.Desks {
		byDepartment[desk.DepartmentID] = append(byDepartment[desk.DepartmentID], desk)
	}
	for departmentID := range byDepartment {
		desks := byDepartment[departmentID]
		sort.SliceStable(desks, func(i, j int) bool {
			return catalogSortKey(desks[i].Name, desks[i].ID) < catalogSortKey(desks[j].Name, desks[j].ID)
		})
		byDepartment[departmentID] = desks
	}

	tree := catalogTree{}
	appendDepartment := func(id int64, name, description string, virtual bool) {
		desks := byDepartment[id]
		// A real department is always shown, even with no desks: it is where the
		// next desk goes, and its own actions must stay reachable. The virtual
		// group exists only while it holds desks.
		if len(desks) == 0 && virtual {
			return
		}
		// The virtual group IS the -1 selection: its row id, slug and selection
		// mark all use the sentinel the URLs carry, so a reader (or a journey) that
		// resolves the row's id never has to know the storage key is 0.
		rowID := id
		if virtual {
			rowID = unassignedDepartmentID
		}
		department := catalogTreeDepartment{
			ID:          rowID,
			Slug:        "d" + strconv.FormatInt(rowID, 10),
			Name:        newCatalogName(name, in.Query),
			Description: description,
			DeskCount:   len(desks),
			Virtual:     virtual,
			Current:     in.DepartmentID == rowID,
		}
		for _, desk := range desks {
			row := catalogTreeDesk{
				ID:              desk.ID,
				Slug:            "k" + strconv.FormatInt(desk.ID, 10),
				Name:            newCatalogName(desk.Name, in.Query),
				AgentCount:      desk.AgentCount,
				OpenTickets:     desk.OpenTickets,
				Current:         in.DeskID == desk.ID,
				DepartmentValue: departmentValue(id, virtual),
			}
			for _, category := range byDesk[desk.ID] {
				state := in.States[category.ID]
				if state.Badge.Empty() {
					state.Badge = application.MsgRef("categories.badge.not_configured")
					state.Tone = "none"
				}
				row.Categories = append(row.Categories, catalogTreeCategory{
					ID:              category.ID,
					Name:            newCatalogName(category.Name, in.Query),
					Workflow:        state.Badge,
					WorkflowTone:    state.Tone,
					SLAKey:          catalogSLAKey(in.SLAEnabled, in.SLADefaults[category.ID]),
					NeedsAttention:  !state.Published,
					OpenTickets:     in.OpenTickets[category.ID],
					Current:         in.DeskID == desk.ID,
					DepartmentValue: departmentValue(id, virtual),
					DeskID:          desk.ID,
				})
			}
			row.NeedsAttention = desk.AgentCount == 0
			for _, category := range row.Categories {
				if category.NeedsAttention {
					row.NeedsAttention = true
					break
				}
			}
			department.AgentCount += desk.AgentCount
			department.Desks = append(department.Desks, row)
		}
		tree.Departments = append(tree.Departments, department)
	}

	departments := append([]domain.CatalogDepartment(nil), in.Departments...)
	sort.SliceStable(departments, func(i, j int) bool {
		return catalogSortKey(departments[i].Name, departments[i].ID) < catalogSortKey(departments[j].Name, departments[j].ID)
	})
	for _, department := range departments {
		appendDepartment(department.ID, department.Name, department.Description, false)
	}
	appendDepartment(0, unassignedName, unassignedDescription, true)

	for _, department := range tree.Departments {
		for _, desk := range department.Desks {
			if desk.AgentCount == 0 {
				tree.AttentionCount++
			}
			for _, category := range desk.Categories {
				if category.NeedsAttention {
					tree.AttentionCount++
				}
			}
		}
	}

	return tree.filter(in.Query, in.Attention)
}

// filter keeps the nodes the current query and the Needs attention chip select,
// plus every ancestor a kept node needs to stay reachable.
func (tree catalogTree) filter(query string, attention bool) catalogTree {
	if query == "" && !attention {
		return tree
	}
	filtered := catalogTree{AttentionCount: tree.AttentionCount}
	for _, department := range tree.Departments {
		kept := catalogTreeDepartment{
			ID: department.ID, Slug: department.Slug, Name: department.Name,
			Description: department.Description, Virtual: department.Virtual, Current: department.Current,
		}
		for _, desk := range department.Desks {
			keptDesk := catalogTreeDesk{
				ID: desk.ID, Slug: desk.Slug, Name: desk.Name, AgentCount: desk.AgentCount,
				NeedsAttention: desk.NeedsAttention, OpenTickets: desk.OpenTickets, Current: desk.Current,
				DepartmentValue: desk.DepartmentValue,
			}
			for _, category := range desk.Categories {
				// A desk nobody can act on is the issue, and it makes every one of
				// its categories part of the answer: hiding them would leave a row
				// that states a problem and shows nothing behind it.
				if attention && !category.NeedsAttention && desk.AgentCount > 0 {
					continue
				}
				if query != "" && !category.Name.Matched && !desk.Name.Matched && !department.Name.Matched {
					continue
				}
				keptDesk.Categories = append(keptDesk.Categories, category)
			}
			deskKept := len(keptDesk.Categories) > 0 ||
				(!attention || desk.NeedsAttention) &&
					(query == "" || desk.Name.Matched || department.Name.Matched)
			if !deskKept {
				continue
			}
			// DeskCount and AgentCount stay the group's real totals: a filtered row
			// still states how big the department IS, and only what it lists shrinks.
			kept.Desks = append(kept.Desks, keptDesk)
		}
		// A department survives as an ancestor of a kept child, and — without the
		// attention filter — when its own name matched, so a department with no
		// desks is still findable by search.
		keepDepartment := len(kept.Desks) > 0 || (query != "" && !attention && department.Name.Matched)
		if keepDepartment {
			filtered.Departments = append(filtered.Departments, kept)
		}
	}
	return filtered
}

// newCatalogName splits name around every case-insensitive occurrence of query.
// A name whose lowercasing changes its byte length (a handful of non-ASCII
// runes) is returned unhighlighted rather than sliced at a wrong offset.
func newCatalogName(name, query string) catalogName {
	out := catalogName{Plain: name}
	query = strings.TrimSpace(query)
	if query == "" {
		out.Parts = []catalogNamePart{{Text: name}}
		return out
	}
	lower := strings.ToLower(name)
	needle := strings.ToLower(query)
	if needle == "" || len(lower) != len(name) {
		out.Parts = []catalogNamePart{{Text: name}}
		return out
	}
	cursor := 0
	for {
		index := strings.Index(lower[cursor:], needle)
		if index < 0 {
			break
		}
		start := cursor + index
		end := start + len(needle)
		if start > cursor {
			out.Parts = append(out.Parts, catalogNamePart{Text: name[cursor:start]})
		}
		out.Parts = append(out.Parts, catalogNamePart{Text: name[start:end], Match: true})
		out.Matched = true
		cursor = end
	}
	if len(out.Parts) == 0 {
		out.Parts = []catalogNamePart{{Text: name}}
		return out
	}
	if cursor < len(name) {
		out.Parts = append(out.Parts, catalogNamePart{Text: name[cursor:]})
	}
	return out
}

// catalogSortKey orders rows by name, then by id so the order is total and a
// projection that returns rows in any order still renders the same tree.
func catalogSortKey(name string, id int64) string {
	return strings.ToLower(name) + "\x00" + strconv.FormatInt(id, 20)
}

// catalogSLAKey is the SLA state's catalogue key, or empty when the instance has
// SLA disabled, in which case the row shows no SLA at all (S1).
func catalogSLAKey(enabled, defaults bool) string {
	switch {
	case !enabled:
		return ""
	case defaults:
		return "categories.tree.sla_default"
	default:
		return "categories.tree.sla_custom"
	}
}

// departmentValue is the department_id a row's own requests carry: the real id,
// or the "unassigned" sentinel the drawer and the mutation state accept for the
// presentation-only group.
func departmentValue(id int64, virtual bool) string {
	if virtual {
		return categoryDepartmentValue(unassignedDepartmentID)
	}
	return categoryDepartmentValue(id)
}
