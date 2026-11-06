package domain

import (
	"sort"
	"strings"
)

// CatalogTree answers the structural questions every catalog surface asks
// about the folder tree (issue #484): a folder's path, its children in catalog
// order, its descendants, which folders a requester can see, and where opening
// a folder lands. It is built once per request from the full folder list; the
// database already guarantees the list is a tree (no cycles, every parent
// present), and a malformed list degrades to fewer results, never a loop.
type CatalogTree struct {
	byID     map[int64]Folder
	children map[int64][]Folder // key 0 holds the top level
}

// NewCatalogTree indexes folders by id and by parent. Children are kept in
// catalog order: name case-insensitively, then id (S12).
func NewCatalogTree(folders []Folder) CatalogTree {
	t := CatalogTree{byID: make(map[int64]Folder, len(folders)), children: map[int64][]Folder{}}
	for _, f := range folders {
		t.byID[f.ID] = f
	}
	for _, f := range folders {
		parent := parentKey(f.ParentID)
		if parent != 0 {
			if _, ok := t.byID[parent]; !ok {
				continue
			}
		}
		t.children[parent] = append(t.children[parent], f)
	}
	for key := range t.children {
		list := t.children[key]
		sort.SliceStable(list, func(i, j int) bool { return CatalogLess(list[i].Name, list[i].ID, list[j].Name, list[j].ID) })
	}
	return t
}

// CatalogLess is the catalog's total order for siblings of one kind: name
// case-insensitively, then id.
func CatalogLess(nameA string, idA int64, nameB string, idB int64) bool {
	a, b := strings.ToLower(nameA), strings.ToLower(nameB)
	if a != b {
		return a < b
	}
	return idA < idB
}

func parentKey(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// All returns every folder of the tree, in no particular order.
func (t CatalogTree) All() []Folder {
	out := make([]Folder, 0, len(t.byID))
	for _, f := range t.byID {
		out = append(out, f)
	}
	return out
}

// Folder returns the folder with id.
func (t CatalogTree) Folder(id int64) (Folder, bool) {
	f, ok := t.byID[id]
	return f, ok
}

// Children returns the folders directly inside parent (nil = top level), in
// catalog order.
func (t CatalogTree) Children(parent *int64) []Folder {
	return t.children[parentKey(parent)]
}

// Path returns the folders from the top level down to id, both included. A
// nil id (top level) or an unknown id returns nil.
func (t CatalogTree) Path(id *int64) []Folder {
	if id == nil {
		return nil
	}
	var out []Folder
	seen := map[int64]bool{}
	for current, ok := t.byID[*id]; ok && !seen[current.ID]; current, ok = t.byID[parentKey(current.ParentID)] {
		seen[current.ID] = true
		out = append([]Folder{current}, out...)
		if current.ParentID == nil {
			break
		}
	}
	return out
}

// PathNames is Path as names, the form search results and suggestions show.
func (t CatalogTree) PathNames(id *int64) []string {
	path := t.Path(id)
	out := make([]string, 0, len(path))
	for _, f := range path {
		out = append(out, f.Name)
	}
	return out
}

// VisibleFolders is the set of folders a requester sees (S7): every folder on
// the path of a folder that directly holds an offered category. A folder whose
// whole subtree offers nothing is absent.
func (t CatalogTree) VisibleFolders(offeredIn []*int64) map[int64]bool {
	visible := map[int64]bool{}
	for _, folder := range offeredIn {
		for _, f := range t.Path(folder) {
			visible[f.ID] = true
		}
	}
	return visible
}

// Land is where opening folder id takes a requester (S9): while the folder
// offers no category directly and exactly one visible folder sits inside it,
// the requester lands on that child instead. directOffers reports whether a
// folder directly holds an offered category.
func (t CatalogTree) Land(id int64, visible map[int64]bool, directOffers func(int64) bool) int64 {
	seen := map[int64]bool{}
	for !seen[id] {
		seen[id] = true
		if directOffers(id) {
			return id
		}
		var only int64
		count := 0
		for _, child := range t.children[id] {
			if visible[child.ID] {
				only = child.ID
				count++
			}
		}
		if count != 1 {
			return id
		}
		id = only
	}
	return id
}
