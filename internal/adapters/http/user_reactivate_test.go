package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestUserEditReactivateBranch proves the inactive-user edit form renders
// the "Reactivate user" action and that submitting it reactivates the
// account. S3.1 in the desks-ux-polish tasks requires both labels; the
// Deactivate branch was already covered by TestUserEditOwnsRoleStatusAndPasswordWorkflows.
func TestUserEditReactivateBranch(t *testing.T) {
	h := newHarness(t)
	member := seedUserRole(t, h.store, "Member", "member-reactivate@tkt.test", domain.RoleUser)

	// Deactivate through the managed-edit workflow.
	editPath := "/users/" + strconv.FormatInt(member.ID, 10) + "/edit"
	rec := h.postForm(t, editPath, url.Values{
		"name":   {"Member"},
		"email":  {"member-reactivate@tkt.test"},
		"role":   {"user"},
		"active": {"false"},
	}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users")
	stored, err := h.store.UserStore().GetByID(context.Background(), member.ID)
	if err != nil || stored.Active {
		t.Fatalf("setup: expected inactive user, got %+v err=%v", stored, err)
	}

	// Inactive target must render the Reactivate branch, never Deactivate.
	rec = h.get(t, editPath, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET edit = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Reactivate user") {
		t.Errorf("inactive edit form missing 'Reactivate user' label")
	}
	if strings.Contains(body, "Deactivate user") {
		t.Errorf("inactive edit form must not render 'Deactivate user'")
	}

	// Submit reactivation; account becomes active again.
	rec = h.postForm(t, editPath, url.Values{
		"name":   {"Member"},
		"email":  {"member-reactivate@tkt.test"},
		"role":   {"user"},
		"active": {"true"},
	}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users")
	stored, err = h.store.UserStore().GetByID(context.Background(), member.ID)
	if err != nil || !stored.Active {
		t.Fatalf("reactivate = %+v err=%v, want Active=true", stored, err)
	}
}
