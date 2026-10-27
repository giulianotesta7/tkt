package httpadapter

// The pending-account notice in the bell and the full feed (issue #419, S3,
// S11, S12, S13). The notice itself is stored by the sign-in transaction
// (T5c/T5d); this slice renders it from the PERSISTED facts: the account the
// notice names, its stored provider identity, and a stable target in Users.
//
// Every test drives the real routes over the real file-backed database, the
// real templates, and the real company sign-in flow (the only fake is the
// provider, because this layer never opens a socket). A waiting account is
// arranged exactly as production creates it: a first company sign-in whose
// configured policy says "pending".
//
// Two rules decide what a row may say, and both are asserted here:
//
//   - the account facts come from the stored account and identities, never
//     from a guessed method or a fabricated ticket, and
//   - an account's name, address and provider are shown ONLY to a reader that
//     still holds the pending-management capability, so a downgraded recipient
//     keeps the notice without keeping the personal data it carries.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// pendingNoticeIssuer is the Google issuer the fake provider answers. It is a
// stored identity fact that must never reach a screen.
const pendingNoticeIssuer = "https://accounts.google.com"

// pendingNoticeAccount arranges ONE account that waits for approval through the
// public company sign-in flow, so the notice the administrators receive is
// exactly the one production stores, and returns the created account.
func pendingNoticeAccount(t *testing.T, h *harness, subject, email, name string) *domain.User {
	t.Helper()
	companySignInStore(t, h, companySignInIn(application.NewAccountsPending))
	h.companyProvider.claims = application.CompanyIdentityClaims{
		Issuer:        pendingNoticeIssuer,
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		HostedDomain:  "example.com",
		Name:          name,
	}
	cookie, _ := companyStart(t, h, "google")
	if rec := companyCallback(t, h, "google", cookie.Value, cookie.Value); rec.Code != http.StatusOK {
		t.Fatalf("pending sign-in for %q = %d, want 200: %s", email, rec.Code, rec.Body.String())
	}
	_, account, err := h.store.CompanyIdentityStore().ResolveIdentity(context.Background(), application.ProviderGoogle, pendingNoticeIssuer, subject)
	if err != nil {
		t.Fatalf("resolve the pending account %q: %v", email, err)
	}
	if !account.Pending || account.Active {
		t.Fatalf("arranged account %q active=%v pending=%v, want an inactive pending account", email, account.Active, account.Pending)
	}
	return account
}

// notificationCountUnread reads the bell fragment as one user and reports
// whether it still carries a badge.
func notificationCountUnread(t *testing.T, h *harness, sessionID string) bool {
	t.Helper()
	return strings.Contains(notificationCountAs(t, h, sessionID), "bell-badge")
}

// mustHaveNoStrayWhitespace applies the repository's rendered-markup rule to a
// body: no line is whitespace-only and no line ends in a space or a tab. The
// account rows own their own template branch, so they are checked here as well
// as in the amendment suite's ticket fixture.
func mustHaveNoStrayWhitespace(t *testing.T, body string) {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("line %d ends in whitespace: %q", i+1, line)
		}
		if line != "" && strings.TrimSpace(line) == "" {
			t.Errorf("line %d is whitespace-only: %q", i+1, line)
		}
	}
}

// TestPendingAccountNoticeRendersTheRealAccountRow is the S3/S12 bell row: the
// notice about a waiting account names that account, states the provider its
// stored identity carries, and opens Users > Pending. It carries no ticket —
// no fabricated number, no ticket-degrade label — and no identity secret.
func TestPendingAccountNoticeRendersTheRealAccountRow(t *testing.T) {
	h := newHarness(t)
	account := pendingNoticeAccount(t, h, "notice-subject-ana", "ana.perez@example.com", "Ana Pérez")

	if !notificationCountUnread(t, h, h.adminSession.ID) {
		t.Fatalf("the pending-account notice is not in the administrator's bell")
	}
	rec := notificationPanelAs(t, h, h.adminSession.ID, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /notifications = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	mustHaveNoStrayWhitespace(t, body)
	mustContain(t, body,
		"Ana Pérez is waiting for approval",
		"Ana Pérez (ana.perez@example.com) signed in with Google and is waiting for a role.",
		"Open Users › Pending",
		`name="target" value="users_pending"`,
	)
	// The row is about the ACCOUNT: it holds no ticket, so neither the ticket
	// number nor the "no longer available" degrade (nor a generic ticket
	// sentence) may stand in for a subject.
	for _, unwanted := range []string{
		"TKT-0",
		"Ticket no longer available",
		"Something happened on this ticket",
		"Automatic",
		pendingNoticeIssuer,
		"notice-subject-ana",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the pending-account row carries %q: %.2000s", unwanted, body)
		}
	}

	// Opening the row marks it read and lands on the page that acts on it.
	id := notificationRowIDs(t, body)[0]
	read := notificationRequestAs(t, h, h.adminSession.ID, http.MethodPost, "/notifications/"+id+"/read", "target=users_pending")
	wantRedirect(t, read, http.StatusSeeOther, "/users?status=pending")
	if notificationCountUnread(t, h, h.adminSession.ID) {
		t.Errorf("the badge survived reading the pending-account notice")
	}
	pendingPage := h.get(t, "/users?status=pending", false)
	if pendingPage.Code != http.StatusOK || !strings.Contains(pendingPage.Body.String(), "ana.perez@example.com") {
		t.Fatalf("the target page of the notice = %d, want the waiting account: %.2000s", pendingPage.Code, pendingPage.Body.String())
	}

	// Reading the notice is a read: it neither approves the account it
	// announces nor deletes the notice.
	stored, err := h.store.UserStore().GetByID(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("read the announced account: %v", err)
	}
	if !stored.Pending || stored.Active {
		t.Errorf("reading the notice changed the announced account: active=%v pending=%v", stored.Active, stored.Pending)
	}
	if reopened := notificationPanelAs(t, h, h.adminSession.ID, false).Body.String(); !strings.Contains(reopened, "Ana Pérez is waiting for approval") {
		t.Errorf("the notice was removed by being read: %.2000s", reopened)
	}
}

// TestPendingAccountNoticeFollowsTheInstanceLanguage proves the row is
// catalogue copy: the same stored notice reads in Spanish (neutral tú) with the
// provider labels the rest of the interface uses.
func TestPendingAccountNoticeFollowsTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set instance language: %v", err)
	}
	pendingNoticeAccount(t, h, "notice-subject-bruno", "bruno.salas@example.com", "Bruno Salas")

	body := notificationPanelAs(t, h, h.adminSession.ID, false).Body.String()
	mustContain(t, body,
		"Bruno Salas está pendiente de aprobación",
		"Bruno Salas (bruno.salas@example.com) inició sesión con Google y está esperando un rol.",
		"Abrir Usuarios › Pendientes",
	)
	if strings.Contains(body, "is waiting for a role") {
		t.Errorf("the Spanish panel rendered the English sentence: %.2000s", body)
	}
}

// TestPendingAccountNoticeStopsClaimingApprovalAfterApproval covers the notice
// that outlives its account's state: once the account is approved the row must
// stop saying it waits for a role, because that would be false.
func TestPendingAccountNoticeStopsClaimingApprovalAfterApproval(t *testing.T) {
	h := newHarness(t)
	account := pendingNoticeAccount(t, h, "notice-subject-carla", "carla.ferreyra@example.com", "Carla Ferreyra")

	approved := h.postForm(t, "/users/pending/approve", url.Values{
		"role":     {"agent"},
		"user_ids": {strconv.FormatInt(account.ID, 10)},
	}, false)
	wantRedirect(t, approved, http.StatusSeeOther, "/users?status=pending")

	body := notificationPanelAs(t, h, h.adminSession.ID, false).Body.String()
	mustContain(t, body,
		"Carla Ferreyra requested access",
		"Carla Ferreyra (carla.ferreyra@example.com) signed in with Google and requested access. The account is no longer waiting for approval.",
		"Open Users › Pending",
	)
	for _, unwanted := range []string{"is waiting for approval", "is waiting for a role", "TKT-0"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("a resolved notice still claims %q: %.2000s", unwanted, body)
		}
	}
}

// TestDowngradedRecipientLosesThePendingAccountDetails is the confidentiality
// half of S3/S12: the notice stays addressed to the person it was sent to, but
// the account's name, address and provider are only theirs to read while they
// still hold the pending-management capability. A downgrade therefore removes
// the personal data, not the row.
func TestDowngradedRecipientLosesThePendingAccountDetails(t *testing.T) {
	h := newHarness(t)
	mira := seedUserRole(t, h.store, "Mira Admin", "mira@tkt.test", domain.RoleAdmin)
	miraSession := seedSession(t, h.store, mira.ID).ID
	// Removing an administrator is root's decision (role-authorization), so the
	// downgrade below is performed by root exactly as production performs it.
	root := seedUserRole(t, h.store, "Root Notice", "root-notice@tkt.test", domain.RoleRoot)
	pendingNoticeAccount(t, h, "notice-subject-diego", "diego.ramos@example.com", "Diego Ramos")

	authorized := notificationPanelAs(t, h, miraSession, false).Body.String()
	mustContain(t, authorized, "Diego Ramos (diego.ramos@example.com) signed in with Google and is waiting for a role.")

	// Root downgrades the colleague who received the notice. The stored row is
	// untouched; only the reader's capability changed.
	if _, err := h.users.ChangeRole(context.Background(), *root, mira.ID, domain.RoleAgent); err != nil {
		t.Fatalf("downgrade the notice recipient: %v", err)
	}

	rec := notificationPanelAs(t, h, miraSession, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /notifications after the downgrade = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	mustHaveNoStrayWhitespace(t, body)
	mustContain(t, body, "A company account requested access")
	for _, leaked := range []string{
		"Diego Ramos",
		"diego.ramos@example.com",
		"Google",
		"Open Users › Pending",
		`name="target"`,
		pendingNoticeIssuer,
		"notice-subject-diego",
	} {
		if strings.Contains(body, leaked) {
			t.Errorf("a downgraded recipient still learns %q: %.2000s", leaked, body)
		}
	}

	// The row remains theirs to read, and reading it is still a stable outcome:
	// it marks read and returns to the panel instead of offering a page the
	// reader can no longer open.
	if !notificationCountUnread(t, h, miraSession) {
		t.Fatalf("the downgraded recipient lost the notice itself")
	}
	id := notificationRowIDs(t, body)[0]
	read := notificationRequestAs(t, h, miraSession, http.MethodPost, "/notifications/"+id+"/read", "")
	wantRedirect(t, read, http.StatusSeeOther, "/notifications")
	if notificationCountUnread(t, h, miraSession) {
		t.Errorf("the badge survived reading the restricted notice")
	}
}

// TestPendingAccountNoticeRendersBesideTicketRows is the mixed feed: an account
// notice and a ticket notification in the same panel keep their own shapes, so
// adding the account row never rewrote what a ticket row says.
func TestPendingAccountNoticeRendersBesideTicketRows(t *testing.T) {
	h := newHarness(t)
	mira := seedUserRole(t, h.store, "Mira Admin", "mira@tkt.test", domain.RoleAdmin)
	ticket := h.seedTicket(t, "Printer on the third floor", nil)
	h.assignTicket(t, ticket.ID, mira.ID)
	pendingNoticeAccount(t, h, "notice-subject-elena", "elena.vidal@example.com", "Elena Vidal")
	session := seedSession(t, h.store, mira.ID).ID

	body := notificationPanelAs(t, h, session, false).Body.String()
	mustContain(t, body,
		"TKT-1 · Printer on the third floor",
		"Assigned to you",
		"Elena Vidal is waiting for approval",
		"Elena Vidal (elena.vidal@example.com) signed in with Google and is waiting for a role.",
		"Open Users › Pending",
	)
	if strings.Contains(body, "TKT-0") || strings.Contains(body, "Ticket no longer available") {
		t.Errorf("a ticket row lost its subject in the mixed feed: %.2000s", body)
	}
	if ids := notificationRowIDs(t, body); len(ids) != 2 {
		t.Errorf("mixed feed row ids = %v, want the ticket row and the account row", ids)
	}
}
