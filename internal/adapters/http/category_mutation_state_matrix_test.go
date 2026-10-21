package httpadapter

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// catalogFingerprint renders every stored category, department and desk row,
// so a test can prove a refused request changed nothing at all.
func catalogFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"categories", "departments", "desks"} {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			fmt.Fprintf(&b, "%s:%v;", table, vals)
		}
		_ = rows.Close()
	}
	return b.String()
}

// Issue #345: all nine catalog mutations, HTMX or not, refuse a desk without
// its department before writing, with the same 422 the old post-write check
// answered.
func TestEveryCatalogMutationRefusesADeskWithoutItsDepartmentBeforeWriting(t *testing.T) {
	for _, htmx := range []bool{false, true} {
		h, handler, catalog := categoryMutationHarness(t)
		dep, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "")
		if err != nil {
			t.Fatalf("department: %v", err)
		}
		desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, dep.ID, "Service desk")
		if err != nil {
			t.Fatalf("desk: %v", err)
		}
		cat, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Cat", "", desk.ID)
		if err != nil {
			t.Fatalf("category: %v", err)
		}
		spare, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Spare", "")
		if err != nil {
			t.Fatalf("spare department: %v", err)
		}
		spareDesk, err := catalog.CreateDeskFor(t.Context(), *h.admin, spare.ID, "Spare desk")
		if err != nil {
			t.Fatalf("spare desk: %v", err)
		}
		id := func(n int64) string { return strconv.FormatInt(n, 10) }
		before := catalogFingerprint(t, h.rawDB(t))
		for _, m := range []struct {
			path string
			form url.Values
		}{
			{"/categories", url.Values{"name": {"Probe"}}},
			{"/categories/" + id(cat.ID) + "/edit", url.Values{"name": {"Renamed"}}},
			{"/categories/" + id(cat.ID) + "/delete", url.Values{}},
			{"/categories/departments", url.Values{"name": {"Finance"}}},
			{"/categories/departments/" + id(spare.ID) + "/edit", url.Values{"name": {"Finance"}}},
			{"/categories/departments/" + id(spare.ID) + "/delete", url.Values{}},
			{"/categories/desks", url.Values{"name": {"New desk"}}},
			{"/categories/desks/" + id(spareDesk.ID) + "/edit", url.Values{"name": {"Renamed desk"}}},
			{"/categories/desks/" + id(spareDesk.ID) + "/delete", url.Values{}},
		} {
			m.form.Set("view", "structure")
			m.form.Set("desk_id", id(desk.ID))
			req := httptest.NewRequest(http.MethodPost, m.path, strings.NewReader(m.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
			if htmx {
				req.Header.Set("HX-Request", "true")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s htmx=%v: status = %d, want 422", m.path, htmx, rec.Code)
			}
			if got, want := rec.Body.String(), "department is required when a desk is selected\n"; got != want {
				t.Errorf("%s htmx=%v: body = %q, want %q", m.path, htmx, got, want)
			}
		}
		if after := catalogFingerprint(t, h.rawDB(t)); after != before {
			t.Errorf("htmx=%v: a refused mutation changed stored rows\nbefore=%s\nafter=%s", htmx, before, after)
		}
	}
}
