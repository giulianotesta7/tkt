package httpadapter

import (
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestTicketsIndexRoleSearchSyncContract proves the issue #122 role-search
// sync server contract: agent and user ticket screens render the
// hx-preserved debounced live-search form seeded from the URL q (so a
// non-JS or restored hit on /tickets?q=... shows the synced value), while
// admin/root keep the server-rendered filter bar with Apply/Clear and never
// receive the live-search form. Asset gating itself is proven in
// tickets_static_test.go.
func TestTicketsIndexRoleSearchSyncContract(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct {
		name         string
		role         domain.Role
		wantLiveForm bool
	}{
		{name: "agent", role: domain.RoleAgent, wantLiveForm: true},
		{name: "user", role: domain.RoleUser, wantLiveForm: true},
		{name: "admin", role: domain.RoleAdmin},
		{name: "root", role: domain.RoleRoot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The harness already seeds its own admin/root actors, so these
			// per-role fixtures use distinct names/emails.
			actor := seedUserRole(t, h.store, tc.name+"-probe", tc.name+"-probe@tkt.test", tc.role)
			session := seedSession(t, h.store, actor.ID)
			rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets?q=printer", map[string]string{
				"Cookie": sessionCookie + "=" + session.ID,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()

			gotLiveForm := strings.Contains(body, `hx-trigger="input changed delay:300ms"`)
			if gotLiveForm != tc.wantLiveForm {
				t.Fatalf("%s live-search form = %t, want %t", tc.name, gotLiveForm, tc.wantLiveForm)
			}
			if !tc.wantLiveForm {
				for _, keep := range []string{`type="submit">Apply`, `aria-label="Clear filters"`, `name="state"`} {
					if !strings.Contains(body, keep) {
						t.Errorf("%s view must keep the filter bar control %q", tc.name, keep)
					}
				}
				return
			}

			for _, want := range []string{
				`hx-preserve="true"`,
				`hx-get="/tickets"`,
				`hx-target="#tickets-screen"`,
				`hx-swap="outerHTML"`,
				`hx-push-url="true"`,
				`value="printer"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s live search omits %q", tc.name, want)
				}
			}
			for _, absent := range []string{`type="submit">Apply`, `aria-label="Clear filters"`} {
				if strings.Contains(body, absent) {
					t.Errorf("%s role view must not render %q", tc.name, absent)
				}
			}
		})
	}
}
