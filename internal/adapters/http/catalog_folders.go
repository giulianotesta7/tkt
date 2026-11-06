package httpadapter

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Categories screen (issue #484): a folder tree of unlimited depth holding
// categories, rendered as one indented tree.

// catalogTreeIndentCap is the last level that adds indentation in the Tree
// layout; deeper rows keep that indent and show their level instead, so a deep
// branch stays readable.
const catalogTreeIndentCap = 4

// Needs attention reasons, as catalogue keys.
const catalogIssueCannotRun = "categories.attention.cannot_run"

// catalogNamePart is one run of a node's name: either literal text or a search
// match. The template wraps the matched runs in <mark> and escapes the rest, so
// the highlight never needs the raw query written into the page as HTML.
type catalogNamePart struct {
	Text  string
	Match bool
}

// catalogName is a node's name split for highlighting, plus the plain text the
// accessible labels ("Collapse <name>", the row's menu label) use.
type catalogName struct {
	Plain   string
	Parts   []catalogNamePart
	Matched bool
}

// catalogCategoryState is one category's workflow fact as the row shows it: the
// composed badge (wording), the tone (colour) and whether a version is live.
// All three come from the same WorkflowSummary, so a row cannot say "published"
// in one column and "no workflow" in another.
type catalogCategoryState struct {
	Badge     application.Msg
	Tone      string
	Published bool
	CannotRun bool // published, but its workflow cannot move a ticket
}

// catalogCategoryStateOf maps one workflow summary to the row's facts. A
// published definition that cannot move a ticket keeps today's wording and takes
// the warning tone, which is the anomaly the picker also refuses to hide.
func catalogCategoryStateOf(summary application.WorkflowSummary) catalogCategoryState {
	state := catalogCategoryState{
		Badge:     categoryBadge(summary),
		Published: summary.Version > 0,
		CannotRun: summary.Version > 0 && !summary.CannotRun.Empty(),
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

// catalogFolderRow is one folder with what both layouts show about it.
type catalogFolderRow struct {
	ID          int64
	Slug        string // the id the browser remembers a collapsed folder by ("f12")
	Name        catalogName
	Description string
	ParentID    int64 // 0 at the top level
	Depth       int   // 1 at the top level
	// DeepLevel marks a row past catalogTreeIndentCap: the Tree layout keeps the
	// capped indent and shows the level instead.
	DeepLevel  bool
	PathLabel  string // the ancestors, "IT services › Hardware"; empty at the top
	Folders    []catalogFolderRow
	Categories []catalogCategoryRow
}

// catalogCategoryRow is one category with its workflow. The workflow's steps
// decide which desk handles it, so the row names no desk of its own.
type catalogCategoryRow struct {
	ID           int64
	Name         catalogName
	Description  string
	FolderID     int64 // 0 at the top level
	PathLabel    string
	Depth        int
	DeepLevel    bool
	Workflow     application.Msg
	WorkflowTone string
	// SLAKey is the catalogue key for the SLA state, empty when the instance has
	// SLA disabled — the row then shows no SLA at all.
	SLAKey   string
	IssueKey string
}

// catalogView is everything the Categories screen renders.
type catalogView struct {
	// Folders and Categories are the top level of the Tree layout, already
	// filtered by search and Needs attention (matches plus their ancestors).
	Folders        []catalogFolderRow
	Categories     []catalogCategoryRow
	AttentionCount int
	Empty          bool
}

// catalogViewInput is everything the view needs from the page's reads.
type catalogViewInput struct {
	Folders     []domain.Folder
	Places      map[int64]*int64
	Categories  []domain.Category
	States      map[int64]catalogCategoryState
	SLADefaults map[int64]bool
	SLAEnabled  bool
	Query       string
	Attention   bool
}

func buildCatalogView(in catalogViewInput) catalogView {
	tree := domain.NewCatalogTree(in.Folders)
	query := strings.TrimSpace(in.Query)

	byFolder := map[int64][]domain.Category{}
	for _, c := range in.Categories {
		folder := int64(0)
		if place := in.Places[c.ID]; place != nil {
			if _, ok := tree.Folder(*place); ok {
				folder = *place
			}
		}
		byFolder[folder] = append(byFolder[folder], c)
	}
	for key := range byFolder {
		list := byFolder[key]
		sort.SliceStable(list, func(i, j int) bool {
			return domain.CatalogLess(list[i].Name, list[i].ID, list[j].Name, list[j].ID)
		})
	}

	pathLabel := func(folder int64) string {
		if folder == 0 {
			return ""
		}
		return strings.Join(tree.PathNames(&folder), " › ")
	}
	categoryRow := func(c domain.Category, folder int64, depth int) catalogCategoryRow {
		state := in.States[c.ID]
		if state.Badge.Empty() {
			state.Badge = application.MsgRef("categories.badge.not_configured")
			state.Tone = "none"
		}
		row := catalogCategoryRow{
			ID: c.ID, Name: newCatalogName(c.Name, query), Description: c.Description,
			FolderID: folder, PathLabel: pathLabel(folder), Depth: depth, DeepLevel: depth > catalogTreeIndentCap,
			Workflow: state.Badge, WorkflowTone: state.Tone,
			SLAKey: catalogSLAKey(in.SLAEnabled, in.SLADefaults[c.ID]),
		}
		// Desk responsibility lives in the workflow (issue #491): a published
		// workflow that cannot move a ticket needs attention, whatever desk
		// the category used to name.
		switch {
		case !state.Published:
			row.IssueKey = "categories.attention.no_workflow"
		case state.CannotRun:
			row.IssueKey = catalogIssueCannotRun
		}
		return row
	}

	// build renders one folder and everything below it, unfiltered.
	var build func(f domain.Folder, depth int) catalogFolderRow
	build = func(f domain.Folder, depth int) catalogFolderRow {
		parent := int64(0)
		if f.ParentID != nil {
			parent = *f.ParentID
		}
		row := catalogFolderRow{
			ID: f.ID, Slug: "f" + strconv.FormatInt(f.ID, 10), Name: newCatalogName(f.Name, query),
			Description: f.Description, ParentID: parent, Depth: depth, DeepLevel: depth > catalogTreeIndentCap,
			PathLabel: pathLabel(parent),
		}
		id := f.ID
		for _, child := range tree.Children(&id) {
			row.Folders = append(row.Folders, build(child, depth+1))
		}
		for _, c := range byFolder[f.ID] {
			row.Categories = append(row.Categories, categoryRow(c, f.ID, depth+1))
		}
		return row
	}

	view := catalogView{}
	var all []catalogFolderRow
	for _, f := range tree.Children(nil) {
		all = append(all, build(f, 1))
	}
	var topCategories []catalogCategoryRow
	for _, c := range byFolder[0] {
		topCategories = append(topCategories, categoryRow(c, 0, 1))
	}

	// Needs attention counts before any filter, so the chip states how much
	// work exists even while the page shows part of it.
	var count func(rows []catalogFolderRow)
	count = func(rows []catalogFolderRow) {
		for _, f := range rows {
			for _, c := range f.Categories {
				if c.IssueKey != "" {
					view.AttentionCount++
				}
			}
			count(f.Folders)
		}
	}
	count(all)
	for _, c := range topCategories {
		if c.IssueKey != "" {
			view.AttentionCount++
		}
	}

	view.Folders, view.Categories = filterCatalog(all, topCategories, query, in.Attention)
	view.Empty = len(view.Folders) == 0 && len(view.Categories) == 0

	return view
}

// filterCatalog keeps the folders and categories the search and Needs
// attention select, plus every ancestor a kept row needs to stay reachable. A
// category matches through its own name or the name of any folder above it.
func filterCatalog(folders []catalogFolderRow, categories []catalogCategoryRow, query string, attention bool) ([]catalogFolderRow, []catalogCategoryRow) {
	if query == "" && !attention {
		return folders, categories
	}
	keepCategory := func(c catalogCategoryRow, ancestorMatched bool) bool {
		if attention && c.IssueKey == "" {
			return false
		}
		return query == "" || c.Name.Matched || ancestorMatched
	}
	var walk func(f catalogFolderRow, ancestorMatched bool) (catalogFolderRow, bool)
	walk = func(f catalogFolderRow, ancestorMatched bool) (catalogFolderRow, bool) {
		matched := ancestorMatched || f.Name.Matched
		kept := f
		kept.Folders, kept.Categories = nil, nil
		for _, child := range f.Folders {
			if row, ok := walk(child, matched); ok {
				kept.Folders = append(kept.Folders, row)
			}
		}
		for _, c := range f.Categories {
			if keepCategory(c, matched) {
				kept.Categories = append(kept.Categories, c)
			}
		}
		self := !attention && (query == "" || f.Name.Matched)
		return kept, self || len(kept.Folders) > 0 || len(kept.Categories) > 0
	}
	var outFolders []catalogFolderRow
	for _, f := range folders {
		if row, ok := walk(f, false); ok {
			outFolders = append(outFolders, row)
		}
	}
	var outCategories []catalogCategoryRow
	for _, c := range categories {
		if keepCategory(c, false) {
			outCategories = append(outCategories, c)
		}
	}
	return outFolders, outCategories
}

// folderOption is one choice of a folder select: the full path as the label.
type folderOption struct {
	ID       int64
	ParentID int64
	Label    string
}

// catalogFolderOptions lists every folder depth first in catalog order, each
// labelled with its full path, leaving out exclude and everything below it (a
// folder cannot move into itself or one of its subfolders).
func catalogFolderOptions(folders []domain.Folder, exclude int64) []folderOption {
	tree := domain.NewCatalogTree(folders)
	var out []folderOption
	var walk func(parent *int64)
	walk = func(parent *int64) {
		for _, f := range tree.Children(parent) {
			if f.ID == exclude {
				continue
			}
			id := f.ID
			out = append(out, folderOption{ID: f.ID, ParentID: folderValue(f.ParentID), Label: strings.Join(tree.PathNames(&id), " › ")})
			walk(&id)
		}
	}
	walk(nil)
	return out
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
		out.Matched = strings.Contains(lower, needle)
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

// catalogSLAKey is the SLA state's catalogue key, or empty when the instance has
// SLA disabled, in which case the row shows no SLA at all.
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

// categoryViewState is the Categories screen's URL state: the selected folder
// of the Folders layout plus the two filters.
type categoryViewState struct {
	Folder    int64
	Query     string
	Attention bool
}

// categoryStatePath is the URL of one Categories view.
func categoryStatePath(state categoryViewState) string {
	values := url.Values{}
	if state.Folder != 0 {
		values.Set("folder", strconv.FormatInt(state.Folder, 10))
	}
	if state.Query != "" {
		values.Set("q", state.Query)
	}
	if state.Attention {
		values.Set("attention", "1")
	}
	if len(values) == 0 {
		return "/categories"
	}
	return "/categories?" + values.Encode()
}

// categoryStateFrom reads the state from a query or a submitted form. A folder
// id that is not a positive integer is refused, so a mutation never commits
// before its redirect target is known to be valid.
func categoryStateFrom(values url.Values) (categoryViewState, error) {
	state := categoryViewState{Query: strings.TrimSpace(values.Get("q")), Attention: values.Get("attention") == "1"}
	if raw := values.Get("folder"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return categoryViewState{}, &domain.ValidationError{Field: "folder", Message: "invalid identifier", Key: "error.invalid_identifier"}
		}
		state.Folder = id
	}
	return state, nil
}

// catalogNodeContext and catalogCategoryContext carry a row together with the
// view state, so the recursive tree templates can build every link and form.
type catalogNodeContext struct {
	Row   catalogFolderRow
	State categoryViewState
}

type catalogCategoryContext struct {
	Row   catalogCategoryRow
	State categoryViewState
}
