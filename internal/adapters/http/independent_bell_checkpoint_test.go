package httpadapter

// Independent bounded NEW Bell regressions (issue #419, T5m.5: S3, S9, S11,
// S12, S13; owner authorization L155).
//
// The independent probe artifacts for this slice were lost before they could be
// imported into the project (L154), and the owner authorized reconstructing them
// as NEW tests (L155, verbatim: "Reconstruirlos como tests nuevos — recomendado").
// Nothing here reproduces the missing probe: this file is written against the
// RULES the slice owes, not against remembered bytes. No body, hash or runtime
// result below is claimed to be the old one; the old five Bell tests and the
// original probe evidence keep their own, historical attribution.
//
// This is the checkpoint half. Three rules are settled here, each through the
// public routes over the real file-backed database, the real templates, the real
// pending-account transaction and the real notification store (only the identity
// provider is faked, because this layer never opens a socket):
//
//   - the account row renders the STORED account facts, escaped, in both of the
//     panel's representations, and never repeats a provider identity;
//   - a reader who is not the addressee cannot read the row at all, and the
//     refusal is a plain 404 that writes nothing; and
//   - a read is a read: clearing one recipient's badge leaves every other
//     recipient's notice unread and never moves the announced account out of
//     its waiting state.

import (
	"context"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

const (
	// independentBellName is the stored account name this probe arranges: it
	// carries markup and both quote characters, so a row that interpolated the
	// stored value into markup would be visible instead of merely suspicious.
	independentBellName = `Ana <b>Pérez</b> & "Co"`
	// independentBellEmail is the stored address the notice names. It is shown to
	// the addressee on purpose — the notice is about this account.
	independentBellEmail = "ana.checkpoint@example.com"
	// independentBellSubject is the provider subject stored with the identity. It
	// is an internal identifier: no representation may repeat it.
	independentBellSubject = "independent-bell-subject-7f2"
	// independentBellIssuer is the issuer the fake provider answers.
	independentBellIssuer = "https://accounts.google.com"
)

// TestIndependentBellCheckpoint is the S3/S12 checkpoint: the bell's account row
// renders the stored account escaped in the full page AND in the drawer
// fragment; a foreign reader gets the exact 404 without a byte written; and a
// recipient-scoped read leaves the other recipients' badge and the announced
// account untouched.
func TestIndependentBellCheckpoint(t *testing.T) {
	h := newHarness(t)
	// The announcement reaches every active account that may approve a waiting
	// one, so a second administrator receives its OWN row for the same account.
	mira := seedUserRole(t, h.store, "Mira Checkpoint", "mira-checkpoint@tkt.test", domain.RoleAdmin)
	miraSession := seedSession(t, h.store, mira.ID).ID
	// An agent can open tickets but cannot manage waiting accounts, so it is not
	// an addressee of this notice at all.
	intruder := seedUserRole(t, h.store, "Intruder Agent", "intruder-checkpoint@tkt.test", domain.RoleAgent)
	intruderSession := seedSession(t, h.store, intruder.ID).ID

	// The waiting account is arranged exactly as production creates it: a first
	// company sign-in under a "pending" new-account policy.
	account := seedWaitingAccount(t, h, independentBellSubject, independentBellEmail, independentBellName)

	escapedName := html.EscapeString(independentBellName)
	title := escapedName + " is waiting for approval"
	sentence := escapedName + " (" + independentBellEmail + ") signed in with Google and is waiting for a role."

	// --- the full page ------------------------------------------------------
	full := notificationPanelAs(t, h, h.adminSession.ID, false)
	if full.Code != http.StatusOK {
		t.Fatalf("GET /notifications = %d, want 200: %s", full.Code, full.Body.String())
	}
	fullBody := full.Body.String()
	if !strings.Contains(fullBody, "<!DOCTYPE") {
		t.Errorf("the non-HX panel is not a full document: %.600s", fullBody)
	}
	mustContain(t, fullBody, title, sentence, `name="target" value="users_pending"`)
	for _, raw := range []string{independentBellName, independentBellSubject, independentBellIssuer} {
		if strings.Contains(fullBody, raw) {
			t.Errorf("the full panel repeats the unescaped value %q: %.2000s", raw, fullBody)
		}
	}

	// --- the drawer fragment ------------------------------------------------
	fragment := notificationPanelAs(t, h, h.adminSession.ID, true)
	if fragment.Code != http.StatusOK {
		t.Fatalf("GET /notifications (HX) = %d, want 200: %s", fragment.Code, fragment.Body.String())
	}
	fragmentBody := fragment.Body.String()
	if strings.Contains(fragmentBody, "<!DOCTYPE") {
		t.Errorf("the HX panel rendered a document instead of the drawer fragment: %.600s", fragmentBody)
	}
	if got := fragment.Header().Get("Vary"); got != "HX-Request" {
		t.Errorf("HX panel Vary = %q, want HX-Request", got)
	}
	if got := fragment.Header().Get("HX-Trigger-After-Swap"); got != "notifications:changed" {
		t.Errorf("HX panel HX-Trigger-After-Swap = %q, want notifications:changed", got)
	}
	mustContain(t, fragmentBody, title, sentence)
	for _, raw := range []string{independentBellName, independentBellSubject, independentBellIssuer} {
		if strings.Contains(fragmentBody, raw) {
			t.Errorf("the drawer fragment repeats the unescaped value %q: %.2000s", raw, fragmentBody)
		}
	}

	// The addressee's own row is unread, and the first row it offers is the
	// account notice (nothing else happened in this instance yet).
	if body := notificationCountAs(t, h, h.adminSession.ID); !strings.Contains(body, "bell-badge") {
		t.Fatalf("the addressee has no unread badge for its own notice: %s", body)
	}
	ids := notificationRowIDs(t, fullBody)
	if len(ids) == 0 {
		t.Fatalf("the panel offers no row to open: %.2000s", fullBody)
	}
	noticeID := ids[0]

	// --- a foreign reader cannot read the row -------------------------------
	before := independentUIFileHash(t, h)
	foreign := notificationRequestAs(t, h, intruderSession, http.MethodPost, "/notifications/"+noticeID+"/read", "target=users_pending")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign read of a notice = %d, want 404: %s", foreign.Code, foreign.Body.String())
	}
	if got := foreign.Body.String(); got != "notification not found\n" {
		t.Errorf("foreign read body = %q, want %q", got, "notification not found\n")
	}
	if got := foreign.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("foreign read Content-Type = %q", got)
	}
	if got := foreign.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("foreign read X-Content-Type-Options = %q, want nosniff", got)
	}
	if after := independentUIFileHash(t, h); after != before {
		t.Errorf("a refused foreign read changed DB/WAL")
	}

	// --- a read is recipient-scoped ----------------------------------------
	readAll := notificationRequestAs(t, h, miraSession, http.MethodPost, "/notifications/read-all", "")
	wantRedirect(t, readAll, http.StatusSeeOther, "/notifications")
	if body := notificationCountAs(t, h, miraSession); strings.Contains(body, "bell-badge") {
		t.Errorf("read-all left its own recipient's badge standing: %s", body)
	}
	if body := notificationCountAs(t, h, h.adminSession.ID); !strings.Contains(body, "bell-badge") {
		t.Errorf("one recipient's read-all cleared another recipient's unread notice: %s", body)
	}
	adminAfter := notificationPanelAs(t, h, h.adminSession.ID, false)
	if adminAfter.Code != http.StatusOK {
		t.Fatalf("GET /notifications after another recipient's read-all = %d, want 200", adminAfter.Code)
	}
	mustContain(t, adminAfter.Body.String(), title, sentence, `class="notification-unread"`)

	// --- reading a notice never decides for the account it announces -------
	stored, err := h.store.UserStore().GetByID(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("read the announced account: %v", err)
	}
	if !stored.Pending || stored.Active {
		t.Errorf("the announced account moved out of its waiting state: active=%v pending=%v", stored.Active, stored.Pending)
	}
	if stored.Name != independentBellName || stored.Email != independentBellEmail {
		t.Errorf("the announced account lost its stored facts: %q <%s>", stored.Name, stored.Email)
	}
}
