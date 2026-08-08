package httpadapter

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Golden-file harness (D7): frozen fixtures render deterministically (fixed
// clock, fixed order) and are compared against testdata/*.golden. Run with
// -update to regenerate, then rerun WITHOUT -update to prove stability.
var update = flag.Bool("update", false, "update golden files")

// goldenFile compares got against testdata/<name>.golden; -update writes it.
func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with go test -run TestGolden -update)", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// TestGoldenFullPage freezes the full-page render path (shell + content)
// with a frozen fixture timestamp (D7: the render path never calls
// time.Now()).
func TestGoldenFullPage(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)

	r.Render(rec, req, "fixture", "", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	goldenFile(t, "render_full_page", rec.Body.String())
}

// TestGoldenFragment freezes the HX fragment render path.
func TestGoldenFragment(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	req.Header.Set("HX-Request", "true")

	r.Render(rec, req, "fixture", "fixture_fragment", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	goldenFile(t, "render_fragment", rec.Body.String())
}
