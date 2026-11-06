package domain

import (
	"reflect"
	"testing"
)

func folderID(v int64) *int64 { return &v }

// sampleTree is the artboard's catalog: IT services › Hardware › Laptops and
// Peripherals, IT services › Software › Licenses › Design tools (a single-child
// chain), People & HR › Benefits, and Facilities › Office.
func sampleTree() CatalogTree {
	return NewCatalogTree([]Folder{
		{ID: 1, Name: "IT services"},
		{ID: 2, ParentID: folderID(1), Name: "Hardware"},
		{ID: 3, ParentID: folderID(2), Name: "Laptops"},
		{ID: 4, ParentID: folderID(2), Name: "Peripherals"},
		{ID: 5, ParentID: folderID(1), Name: "Software"},
		{ID: 6, ParentID: folderID(5), Name: "Licenses"},
		{ID: 7, ParentID: folderID(6), Name: "Design tools"},
		{ID: 8, Name: "People & HR"},
		{ID: 9, ParentID: folderID(8), Name: "Benefits"},
		{ID: 10, Name: "facilities"},
		{ID: 11, ParentID: folderID(10), Name: "Office"},
		{ID: 12, ParentID: folderID(1), Name: "access & accounts"},
	})
}

func names(folders []Folder) []string {
	var out []string
	for _, f := range folders {
		out = append(out, f.Name)
	}
	return out
}

func TestCatalogTreeOrderAndPaths(t *testing.T) {
	tree := sampleTree()
	if got := names(tree.Children(nil)); !reflect.DeepEqual(got, []string{"facilities", "IT services", "People & HR"}) {
		t.Errorf("top level = %q", got)
	}
	if got := names(tree.Children(folderID(1))); !reflect.DeepEqual(got, []string{"access & accounts", "Hardware", "Software"}) {
		t.Errorf("IT services children = %q", got)
	}
	if got := tree.PathNames(folderID(7)); !reflect.DeepEqual(got, []string{"IT services", "Software", "Licenses", "Design tools"}) {
		t.Errorf("path = %q", got)
	}
	if got := tree.PathNames(nil); len(got) != 0 {
		t.Errorf("top-level path = %q, want empty", got)
	}
}

func TestCatalogTreeVisibilityAndLanding(t *testing.T) {
	tree := sampleTree()
	// Offered (published) categories sit in Laptops, Design tools and People & HR.
	visible := tree.VisibleFolders([]*int64{folderID(3), folderID(7), folderID(8)})
	want := map[int64]bool{1: true, 2: true, 3: true, 5: true, 6: true, 7: true, 8: true}
	if !reflect.DeepEqual(visible, want) {
		t.Errorf("visible = %v, want %v", visible, want)
	}
	direct := func(id int64) bool { return id == 3 || id == 7 || id == 8 }

	for _, tc := range []struct {
		name     string
		open     int64
		wantLand int64
	}{
		{"single-child chain lands on its deepest folder", 5, 7},
		{"Hardware has one visible child, Laptops", 2, 3},
		{"a folder with direct offers stays", 8, 8},
		{"a folder with two visible children stays", 1, 1},
	} {
		if got := tree.Land(tc.open, visible, direct); got != tc.wantLand {
			t.Errorf("%s: Land(%d) = %d, want %d", tc.name, tc.open, got, tc.wantLand)
		}
	}
}
