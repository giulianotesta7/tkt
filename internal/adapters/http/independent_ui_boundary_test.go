package httpadapter

import (
	"github.com/giulianotesta7/tkt/internal/domain"
	"html"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// S3/S8/S9/S12: a mixed selection must not reactivate an ordinary inactive
// account, or partially approve/reject the valid waiting entry before it.
func TestIndependentUIMixedInactiveBoundary(t *testing.T) {
	h := newHarness(t)
	waiting := seedWaitingAccount(t, h, "independent-ui", "waiting-gap@example.com", "Waiting Gap")
	inactive := seedUserRole(t, h.store, "Ordinary Inactive", "inactive-gap@example.com", domain.RoleUser)
	inactive.Active = false
	if err := h.store.UserStore().Update(t.Context(), inactive); err != nil {
		t.Fatal(err)
	}
	waitingID := strconv.FormatInt(waiting.ID, 10)
	inactiveID := strconv.FormatInt(inactive.ID, 10)
	for _, action := range []string{"approve", "reject"} {
		before := independentUIFileHash(t, h)
		rec := h.postForm(t, "/users/pending/"+action, url.Values{"user_ids": {waitingID, inactiveID}, "role": {"agent"}}, true)
		reason := (&domain.NotFoundError{Kind: "user", ID: inactive.ID}).Error()
		if rec.Code != 404 || rec.Header().Get("HX-Retarget") != "#users-root" || rec.Header().Get("HX-Reswap") != "outerHTML" || rec.Header().Get("Vary") != "HX-Request" || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: %d %v %s", action, rec.Code, rec.Header(), rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `<div class="users-error" role="alert">`+html.EscapeString(reason)+`</div>`) {
			t.Fatalf("%s wrong exact refusal %q", action, rec.Body.String())
		}
		if rec.Header().Get("X-Save-Feedback") != "" || rec.Header().Get("HX-Trigger-After-Swap") != "" {
			t.Fatal("refusal announced success")
		}
		if after := independentUIFileHash(t, h); after != before {
			t.Fatalf("%s refusal wrote DB/WAL", action)
		}
		for _, filter := range []string{"pending", "deactivated"} {
			body := h.get(t, "/users?status="+filter, true).Body.String()
			if filter == "pending" && (!strings.Contains(body, waiting.Email) || strings.Contains(body, inactive.Email)) {
				t.Fatal("mixed selection changed pending list")
			}
			if filter == "deactivated" && (!strings.Contains(body, inactive.Email) || strings.Contains(body, waiting.Email)) {
				t.Fatal("mixed selection changed deactivated list")
			}
		}
	}
}
