package httpadapter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/giulianotesta7/tkt/internal/domain"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// Public route refusals must not write, and keep the existing drawer target.
func independentUIFileHash(t *testing.T, h *harness) string {
	t.Helper()
	out := ""
	for _, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(h.dbPath + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out += fmt.Sprintf("%s:%x;", suffix, sha256.Sum256(b))
	}
	return out
}

func TestIndependentUIDrawerDifferential(t *testing.T) {
	old := fixedNow
	fixedNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	defer func() { fixedNow = old }()
	h := newHarness(t)
	u := seedUserRole(t, h.store, "Ordinary", "ordinary@example.com", domain.RoleUser)
	seedUserRole(t, h.store, "Duplicate", "duplicate@example.com", domain.RoleUser)
	path := "/users/" + strconv.FormatInt(u.ID, 10) + "/edit"
	for _, tc := range []struct {
		name, path, nameValue, email, role string
		code                               int
	}{
		{"peer grant", path, "Ordinary", u.Email, "admin", 403},
		{"missing", "/users/999999/edit", "Ordinary", u.Email, "user", 404},
		{"duplicate", path, "Ordinary", "duplicate@example.com", "user", 409},
		{"blank name", path, "", u.Email, "user", 422},
	} {
		before := independentUIFileHash(t, h)
		rec := h.postForm(t, tc.path, url.Values{"name": {tc.nameValue}, "email": {tc.email}, "role": {tc.role}, "active": {"true"}, "status": {"active"}}, true)
		if rec.Code != tc.code || rec.Header().Get("HX-Retarget") != "#users-drawer-host" || rec.Header().Get("HX-Reswap") != "outerHTML" {
			t.Fatalf("%s: %d %v %s", tc.name, rec.Code, rec.Header(), rec.Body.String())
		}
		if after := independentUIFileHash(t, h); after != before {
			t.Fatalf("%s wrote DB/WAL", tc.name)
		}
		b, err := json.Marshal(struct {
			Name    string
			Status  int
			Headers map[string][]string
			Body    string
		}{tc.name, rec.Code, rec.Header(), rec.Body.String()})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("OBS " + string(b))
	}
}
