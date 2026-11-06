package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// newFolderTestStore is a fully migrated store with an empty folder tree: the
// 0043 backfill's folders (from the seeded General department and desk) are
// removed so every test states its own tree.
func newFolderTestStore(t *testing.T) (*Store, *folderStore) {
	t.Helper()
	s := newTestDB(t)
	mustExec(t, s, `UPDATE categories SET folder_id = NULL; DELETE FROM folders WHERE parent_id IS NOT NULL; DELETE FROM folders`)
	return s, newFolderStore(s.db)
}

var folderTestNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func createFolder(t *testing.T, fs *folderStore, parent *int64, name string) int64 {
	t.Helper()
	f := &domain.Folder{ParentID: parent, Name: name, CreatedAt: folderTestNow}
	if err := fs.CreateFolder(context.Background(), f); err != nil {
		t.Fatalf("create folder %q: %v", name, err)
	}
	return f.ID
}

func folderNames(t *testing.T, fs *folderStore) []string {
	t.Helper()
	list, err := fs.ListFolders(context.Background())
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	var out []string
	for _, f := range list {
		out = append(out, f.Name)
	}
	return out
}

func TestFolderStoreCreateGetList(t *testing.T) {
	ctx := context.Background()
	_, fs := newFolderTestStore(t)

	it := createFolder(t, fs, nil, "IT services")
	createFolder(t, fs, nil, "facilities")
	hw := createFolder(t, fs, &it, "Hardware")

	got, err := fs.GetFolder(ctx, hw)
	if err != nil {
		t.Fatalf("get folder: %v", err)
	}
	want := &domain.Folder{ID: hw, ParentID: &it, Name: "Hardware", Description: "", CreatedAt: folderTestNow}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("get folder = %+v, want %+v", got, want)
	}
	// Name order is case-insensitive: "facilities" before "Hardware" before "IT services".
	if names := folderNames(t, fs); !reflect.DeepEqual(names, []string{"facilities", "Hardware", "IT services"}) {
		t.Errorf("list order = %q", names)
	}
	if _, err := fs.GetFolder(ctx, 9999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get missing folder: err = %v, want not found", err)
	}
}

func TestFolderStoreCreateRefusals(t *testing.T) {
	ctx := context.Background()
	_, fs := newFolderTestStore(t)
	it := createFolder(t, fs, nil, "IT services")
	createFolder(t, fs, &it, "Hardware")

	clash := &domain.Folder{ParentID: &it, Name: "HARDWARE", CreatedAt: folderTestNow}
	var dup *domain.DuplicateError
	if err := fs.CreateFolder(ctx, clash); !errors.As(err, &dup) || dup.Kind != "folder" {
		t.Errorf("sibling clash: err = %v, want a folder duplicate", err)
	}
	missing := int64(9999)
	if err := fs.CreateFolder(ctx, &domain.Folder{ParentID: &missing, Name: "Orphan", CreatedAt: folderTestNow}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing parent: err = %v, want not found", err)
	}
	if names := folderNames(t, fs); !reflect.DeepEqual(names, []string{"Hardware", "IT services"}) {
		t.Errorf("refused creates wrote something: %q", names)
	}
	// The same name is allowed under another parent.
	createFolder(t, fs, nil, "Hardware")
}

func TestFolderStoreUpdate(t *testing.T) {
	ctx := context.Background()
	_, fs := newFolderTestStore(t)
	it := createFolder(t, fs, nil, "IT services")
	createFolder(t, fs, nil, "Facilities")

	if err := fs.UpdateFolder(ctx, &domain.Folder{ID: it, Name: "Technology", Description: "Laptops and access"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := fs.GetFolder(ctx, it)
	if got.Name != "Technology" || got.Description != "Laptops and access" || got.ParentID != nil {
		t.Errorf("updated folder = %+v", got)
	}
	if err := fs.UpdateFolder(ctx, &domain.Folder{ID: it, Name: "facilities"}); !errors.Is(err, domain.ErrDuplicate) {
		t.Errorf("rename onto a sibling: err = %v, want duplicate", err)
	}
	if err := fs.UpdateFolder(ctx, &domain.Folder{ID: 9999, Name: "Ghost"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update missing: err = %v, want not found", err)
	}
}

func TestFolderStoreMove(t *testing.T) {
	ctx := context.Background()
	_, fs := newFolderTestStore(t)
	it := createFolder(t, fs, nil, "IT services")
	hw := createFolder(t, fs, &it, "Hardware")
	laptops := createFolder(t, fs, &hw, "Laptops")
	other := createFolder(t, fs, nil, "Other")
	createFolder(t, fs, &other, "Laptops")

	for _, target := range []int64{it, hw, laptops} {
		target := target
		err := fs.MoveFolder(ctx, it, &target)
		var cycle *domain.FolderCycleError
		if !errors.Is(err, domain.ErrFolderCycle) || !errors.As(err, &cycle) || cycle.FolderID != it || cycle.ParentID != target {
			t.Errorf("move IT services under %d: err = %v, want a folder cycle", target, err)
		}
		if err == nil || err.Error() != "a folder can't move into itself or one of its subfolders" {
			t.Errorf("cycle message = %v", err)
		}
		if got, _ := fs.GetFolder(ctx, it); got.ParentID != nil {
			t.Errorf("refused move changed the parent to %v", *got.ParentID)
		}
	}

	if err := fs.MoveFolder(ctx, laptops, &other); !errors.Is(err, domain.ErrDuplicate) {
		t.Errorf("move onto a sibling name: err = %v, want duplicate", err)
	}
	if got, _ := fs.GetFolder(ctx, laptops); got.ParentID == nil || *got.ParentID != hw {
		t.Errorf("refused move changed the parent: %+v", got)
	}

	if err := fs.MoveFolder(ctx, hw, &other); err != nil {
		t.Fatalf("legal move: %v", err)
	}
	if got, _ := fs.GetFolder(ctx, hw); got.ParentID == nil || *got.ParentID != other {
		t.Errorf("moved folder parent = %v, want %d", got.ParentID, other)
	}
	if err := fs.MoveFolder(ctx, hw, nil); err != nil {
		t.Fatalf("move to top level: %v", err)
	}
	if got, _ := fs.GetFolder(ctx, hw); got.ParentID != nil {
		t.Errorf("top-level move left parent %v", *got.ParentID)
	}
	missing := int64(9999)
	if err := fs.MoveFolder(ctx, hw, &missing); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("move under a missing parent: err = %v, want not found", err)
	}
	if err := fs.MoveFolder(ctx, missing, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("move a missing folder: err = %v, want not found", err)
	}
}

func TestFolderStoreDeleteAndPlacement(t *testing.T) {
	ctx := context.Background()
	s, fs := newFolderTestStore(t)
	it := createFolder(t, fs, nil, "IT services")
	hw := createFolder(t, fs, &it, "Hardware")
	empty := createFolder(t, fs, nil, "Empty")
	cat := seedCategory(t, s, "Laptop repair")
	other := seedCategory(t, s, "General question")

	if err := fs.PlaceCategory(ctx, cat, &hw); err != nil {
		t.Fatalf("place category: %v", err)
	}
	places, err := fs.CategoryFolders(ctx)
	if err != nil {
		t.Fatalf("category folders: %v", err)
	}
	if p := places[cat]; p == nil || *p != hw {
		t.Errorf("category %d folder = %v, want %d", cat, p, hw)
	}
	if p, ok := places[other]; !ok || p != nil {
		t.Errorf("category %d folder = %v (present %v), want top level", other, p, ok)
	}

	var ref *domain.ReferencedError
	if err := fs.DeleteFolder(ctx, it); !errors.As(err, &ref) || ref.Kind != "folder" {
		t.Errorf("delete a folder holding a folder: err = %v, want referenced", err)
	}
	if err := fs.DeleteFolder(ctx, hw); !errors.Is(err, domain.ErrReferenced) {
		t.Errorf("delete a folder holding a category: err = %v, want referenced", err)
	}
	if names := folderNames(t, fs); !reflect.DeepEqual(names, []string{"Empty", "Hardware", "IT services"}) {
		t.Errorf("refused deletes removed something: %q", names)
	}

	if err := fs.PlaceCategory(ctx, cat, nil); err != nil {
		t.Fatalf("place at top level: %v", err)
	}
	if err := fs.DeleteFolder(ctx, hw); err != nil {
		t.Fatalf("delete the emptied folder: %v", err)
	}
	if err := fs.DeleteFolder(ctx, empty); err != nil {
		t.Fatalf("delete empty folder: %v", err)
	}
	if err := fs.DeleteFolder(ctx, empty); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("delete twice: err = %v, want not found", err)
	}

	missing := int64(9999)
	if err := fs.PlaceCategory(ctx, cat, &missing); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("place in a missing folder: err = %v, want not found", err)
	}
	if err := fs.PlaceCategory(ctx, 9999, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("place a missing category: err = %v, want not found", err)
	}
}

func TestFolderStoreUnicodeSiblingDuplicate(t *testing.T) {
	ctx := context.Background()
	_, fs := newFolderTestStore(t)
	createFolder(t, fs, nil, "ÁREA")
	err := fs.CreateFolder(ctx, &domain.Folder{Name: "área", CreatedAt: folderTestNow})
	var dup *domain.DuplicateError
	if !errors.As(err, &dup) || dup.Kind != "folder" {
		t.Fatalf("Unicode sibling duplicate: err = %v, want folder DuplicateError", err)
	}
	if got := folderNames(t, fs); !reflect.DeepEqual(got, []string{"ÁREA"}) {
		t.Fatalf("refused create changed folders: %q", got)
	}
	other := createFolder(t, fs, nil, "Otra")
	if err := fs.UpdateFolder(ctx, &domain.Folder{ID: other, Name: "Área"}); !errors.Is(err, domain.ErrDuplicate) {
		t.Errorf("Unicode rename onto a sibling: err = %v, want duplicate", err)
	}
}

// TestFolderStoreGreekCaseSiblingDuplicate covers a case variant ToLower does
// not unify: Greek final sigma.
func TestFolderStoreGreekCaseSiblingDuplicate(t *testing.T) {
	ctx := context.Background()
	_, fs := newFolderTestStore(t)
	createFolder(t, fs, nil, "ΟΣ")

	err := fs.CreateFolder(ctx, &domain.Folder{Name: "ος", CreatedAt: folderTestNow})
	var dup *domain.DuplicateError
	if !errors.As(err, &dup) || dup.Kind != "folder" {
		t.Fatalf("Greek sibling duplicate: err = %v, want folder DuplicateError", err)
	}
	if got := folderNames(t, fs); !reflect.DeepEqual(got, []string{"ΟΣ"}) {
		t.Fatalf("refused create changed folders: %q", got)
	}
}
