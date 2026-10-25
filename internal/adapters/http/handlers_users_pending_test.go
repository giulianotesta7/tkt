package httpadapter

// The Users > Pending tab (issue #419, S3, S8, S9, S10, S12, S13): the accounts
// a company sign-in created that wait for an administrator's approval, the bulk
// approval that turns a chosen set of them into managed users with ONE role, and
// the concealment that keeps a waiting account out of every ordinary management
// route.
//
// Every test drives the real routes through the middleware-wrapped mux over the
// real file-backed database and the real templates. A waiting account is
// arranged through the public company sign-in flow (start + callback against the
// fake provider) instead of a hand-built row, so the fixture is exactly the
// account production creates.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

const (
	// pendingIssuer is the Google issuer the fake provider answers.
	pendingIssuer = "https://accounts.google.com"
	// pendingApprovalReason is the role_changes reason the approval writes when
	// it grants a role the waiting account did not have.
	pendingApprovalReason = "pending approval"
)

// seedWaitingAccount arranges ONE account that waits for approval through the
// real company sign-in flow: the stored policy says a first sign-in is created
// pending, the fake provider answers verified claims for the address, and the
// public start + callback pair creates the account exactly as production does.
func seedWaitingAccount(t *testing.T, h *harness, subject, email, name string) *domain.User {
	t.Helper()
	companySignInStore(t, h, companySignInIn(application.NewAccountsPending))
	h.companyProvider.claims = application.CompanyIdentityClaims{
		Issuer:        pendingIssuer,
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
	_, account, err := h.store.CompanyIdentityStore().ResolveIdentity(context.Background(), application.ProviderGoogle, pendingIssuer, subject)
	if err != nil {
		t.Fatalf("resolve the pending account %q: %v", email, err)
	}
	if !account.Pending || account.Active {
		t.Fatalf("arranged account %q active=%v pending=%v, want an inactive pending account", email, account.Active, account.Pending)
	}
	return account
}

// seedActorWithSession inserts one active account with the given role and a live
// session, so a test can address a route as a non-admin actor.
func seedActorWithSession(t *testing.T, h *harness, name, email string, role domain.Role) string {
	t.Helper()
	actor := seedUserRole(t, h.store, name, email, role)
	return seedSession(t, h.store, actor.ID).ID
}

// asActor runs one authenticated GET as an explicit session cookie.
func asActor(h *harness, session, target string, hx bool) *actorResponse {
	headers := map[string]string{"Cookie": sessionCookie + "=" + session}
	if hx {
		headers["HX-Request"] = "true"
	}
	rec := doRequest(h.mux, h.mw, http.MethodGet, target, headers)
	return &actorResponse{Code: rec.Code, Body: rec.Body.String(), Header: rec.Header()}
}

// actorResponse is the observable half of one response: status, body and
// headers, which is what an assertion reads.
type actorResponse struct {
	Code   int
	Body   string
	Header http.Header
}

// pendingAccountSnapshot renders the stored state an approval could change, so a
// refused batch can prove it wrote nothing at all: every account row plus the
// role audit count.
func pendingAccountSnapshot(t *testing.T, h *harness) string {
	t.Helper()
	db := h.rawDB(t)
	rows, err := db.Query(`SELECT id, name, email, role, active, pending FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("read account snapshot: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, active, pending int64
		var name, email, role string
		if err := rows.Scan(&id, &name, &email, &role, &active, &pending); err != nil {
			t.Fatalf("scan account snapshot: %v", err)
		}
		lines = append(lines, strings.Join([]string{
			strconv.FormatInt(id, 10), name, email, role,
			strconv.FormatInt(active, 10), strconv.FormatInt(pending, 10),
		}, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read account snapshot: %v", err)
	}
	lines = append(lines, "role_changes="+strconv.FormatInt(scanOneInt(t, db, `SELECT COUNT(*) FROM role_changes`), 10))
	return strings.Join(lines, "\n")
}

// TestUsersPendingTabIsManagementOnly is S3 at the authorization boundary: the
// tab lists accounts waiting for approval to root and admin only. A role without
// user management is refused before anything is read, and the refusal carries no
// account.
func TestUsersPendingRejectPreservesIdentityAndBlocksSignInUntilReactivated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	waiting := seedWaitingAccount(t, h, "rejected-subject", "rejected@example.com", "Rejected Person")
	identity, _, err := h.store.CompanyIdentityStore().ResolveIdentity(ctx, application.ProviderGoogle, pendingIssuer, "rejected-subject")
	if err != nil {
		t.Fatal(err)
	}
	identityBefore := fmt.Sprintf("%+v", identity)
	unreadBefore, err := h.store.NotificationStore().UnreadCount(ctx, h.admin.ID)
	if err != nil || unreadBefore < 1 {
		t.Fatalf("initial unread=%d/%v", unreadBefore, err)
	}
	rec := h.postForm(t, "/users/pending/reject", url.Values{"user_ids": {strconv.FormatInt(waiting.ID, 10)}, "role": {"root"}}, true)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Retarget") != "#users-root" || rec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("Reject=%d/%v: %s", rec.Code, rec.Header(), rec.Body.String())
	}
	mustContain(t, rec.Body.String(), `class="users-pending-count">0</span>`)
	identity, stored, err := h.store.CompanyIdentityStore().ResolveIdentity(ctx, application.ProviderGoogle, pendingIssuer, "rejected-subject")
	if err != nil || stored.ID != waiting.ID || stored.Pending || stored.Active || stored.Role != domain.RoleUser || fmt.Sprintf("%+v", identity) != identityBefore {
		t.Fatalf("Reject lost account/identity or changed role: %v", err)
	}
	// Reject changes the queue count, not the recipient's unread-notification count.
	if unread, err := h.store.NotificationStore().UnreadCount(ctx, h.admin.ID); err != nil || unread != unreadBefore {
		t.Fatalf("unread=%d/%v, want %d", unread, err, unreadBefore)
	}
	deactivated := h.get(t, "/users?status=deactivated", false)
	mustContain(t, deactivated.Body.String(), waiting.Email)
	usersBefore, err := h.store.UserStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessions := storedSessionCount(t, h)
	for attempt := 0; attempt < 2; attempt++ {
		cookie, _ := companyStart(t, h, "google")
		refused := companyCallback(t, h, "google", cookie.Value, cookie.Value)
		if refused.Code != http.StatusForbidden {
			t.Fatalf("rejected sign-in=%d: %s", refused.Code, refused.Body.String())
		}
		mustContain(t, refused.Body.String(), i18n.T(i18n.Default, "auth.company.refused_deactivated"))
		if findCookie(refused, sessionCookie) != nil || storedSessionCount(t, h) != sessions {
			t.Fatal("rejected sign-in created a session")
		}
	}
	usersAfter, err := h.store.UserStore().List(ctx)
	if err != nil || fmt.Sprintf("%+v", usersAfter) != fmt.Sprintf("%+v", usersBefore) {
		t.Fatalf("rejected sign-in changed accounts: %v", err)
	}
	if pending, err := h.store.PendingUserStore().ListPending(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("rejected sign-in created a new pending account: %v/%v", pending, err)
	}
	identity, _, err = h.store.CompanyIdentityStore().ResolveIdentity(ctx, application.ProviderGoogle, pendingIssuer, "rejected-subject")
	if err != nil || fmt.Sprintf("%+v", identity) != identityBefore {
		t.Fatalf("rejected sign-in changed identity: %v", err)
	}
	edit := h.postForm(t, "/users/"+strconv.FormatInt(waiting.ID, 10)+"/edit", url.Values{"name": {waiting.Name}, "email": {waiting.Email}, "role": {"agent"}, "active": {"true"}, "status": {"active"}}, true)
	if edit.Code != http.StatusOK {
		t.Fatalf("reactivate/assign=%d: %s", edit.Code, edit.Body.String())
	}
	cookie, _ := companyStart(t, h, "google")
	wantRedirect(t, companyCallback(t, h, "google", cookie.Value, cookie.Value), http.StatusSeeOther, "/tickets")
	_, stored, err = h.store.CompanyIdentityStore().ResolveIdentity(ctx, application.ProviderGoogle, pendingIssuer, "rejected-subject")
	if err != nil || stored.ID != waiting.ID || !stored.Active || stored.Pending || stored.Role != domain.RoleAgent {
		t.Fatalf("reactivated identity/role=%v/%v", stored, err)
	}
}

func TestUsersPendingRejectRefusesInvalidBatchesAndUnauthorizedActors(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	waiting := seedWaitingAccount(t, h, "reject-invalid", "reject-invalid@example.com", "Still Waiting")
	id := strconv.FormatInt(waiting.ID, 10)
	for _, tc := range []struct {
		ids    []string
		status int
		key    string
	}{
		{nil, http.StatusUnprocessableEntity, "error.pending_rejection_selection_required"},
		{[]string{id, id}, http.StatusUnprocessableEntity, "error.pending_approval_duplicate_account"},
		{[]string{id, "999999"}, http.StatusNotFound, ""},
		{[]string{id, "0"}, http.StatusUnprocessableEntity, "error.invalid_user"},
		{[]string{id, "-1"}, http.StatusUnprocessableEntity, "error.invalid_user"},
	} {
		before, err := h.store.UserStore().GetByID(ctx, waiting.ID)
		if err != nil {
			t.Fatal(err)
		}
		beforeUser := *before
		rec := h.postForm(t, "/users/pending/reject", url.Values{"user_ids": tc.ids}, false)
		if rec.Code != tc.status {
			t.Fatalf("invalid Reject=%d want %d: %s", rec.Code, tc.status, rec.Body.String())
		}
		if tc.key != "" {
			mustContain(t, rec.Body.String(), i18n.T(i18n.Default, tc.key))
		}
		after, err := h.store.UserStore().GetByID(ctx, waiting.ID)
		if err != nil || fmt.Sprintf("%+v", *after) != fmt.Sprintf("%+v", beforeUser) {
			t.Fatalf("refusal changed user: %v", err)
		}
	}
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		session := seedActorWithSession(t, h, "Reject Actor", "reject-"+string(role)+"@example.com", role)
		rec := h.postFormAs(t, "/users/pending/reject", url.Values{"user_ids": {id}}, session)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Reject as %s=%d", role, rec.Code)
		}
	}
	rec := h.postFormAs(t, "/users/pending/reject", url.Values{"user_ids": {id}}, "")
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	stored, err := h.store.UserStore().GetByID(ctx, waiting.ID)
	if err != nil || !stored.Pending || stored.Active {
		t.Fatalf("unauthorized Reject changed user: %v", err)
	}
}

func TestUsersPendingTabIsManagementOnly(t *testing.T) {
	h := newHarness(t)
	waiting := seedWaitingAccount(t, h, "google-subject-1", "waiting@example.com", "Waiting Person")

	for _, tc := range []struct {
		name   string
		role   domain.Role
		seed   bool
		want   int
		allows bool
	}{
		{name: "root", role: domain.RoleRoot, seed: true, want: http.StatusOK, allows: true},
		{name: "admin", role: domain.RoleAdmin, seed: false, want: http.StatusOK, allows: true},
		{name: "agent", role: domain.RoleAgent, seed: true, want: http.StatusForbidden},
		{name: "user", role: domain.RoleUser, seed: true, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := h.adminSession.ID
			if tc.seed {
				session = seedActorWithSession(t, h, "Actor "+tc.name, "actor-"+tc.name+"@tkt.test", tc.role)
			}
			rec := asActor(h, session, "/users?status=pending", false)
			if rec.Code != tc.want {
				t.Fatalf("GET /users?status=pending as %s = %d, want %d: %s", tc.name, rec.Code, tc.want, rec.Body)
			}
			if !tc.allows {
				if strings.Contains(rec.Body, waiting.Email) || strings.Contains(rec.Body, waiting.Name) {
					t.Errorf("a refused Pending tab must not disclose the waiting account: %s", rec.Body)
				}
				mustContain(t, rec.Body, i18n.T(i18n.Default, "error.forbidden"))
				return
			}
			mustContain(t, rec.Body, waiting.Email, waiting.Name, i18n.T(i18n.Default, "users.filter.pending"))
		})
	}
}

// TestUsersPendingTabListsOnlyWaitingAccounts is S3/S10 at the surface: the tab
// lists the waiting accounts with the request instant, the ordinary list keeps
// them out, and the copy states the consequence — inactive, awaiting approval —
// instead of the "no role" claim the account never made.
func TestUsersPendingTabListsOnlyWaitingAccounts(t *testing.T) {
	h := newHarness(t)
	waiting := seedWaitingAccount(t, h, "google-subject-1", "waiting@example.com", "Waiting Person")
	ordinary := h.createUser(t, "Ana Torres", "ana@example.com", "s3cret-pass-12")

	pending := h.get(t, "/users?status=pending", false)
	if pending.Code != http.StatusOK {
		t.Fatalf("GET /users?status=pending = %d, want 200: %s", pending.Code, pending.Body.String())
	}
	body := pending.Body.String()
	mustContain(t, body,
		`data-users-status="pending"`,
		`data-users-list-url="/users?status=pending"`,
		`href="/users?status=pending"`,
		`action="/users/pending/approve"`,
		`hx-post="/users/pending/approve"`,
		waiting.Name, waiting.Email,
		`name="user_ids" value="`+strconv.FormatInt(waiting.ID, 10)+`"`,
		`name="role"`,
		i18n.T(i18n.Default, "users.status.pending"),
		i18n.T(i18n.Default, "users.pending.help"),
		i18n.T(i18n.Default, "users.pending.approve_as"),
		i18n.T(i18n.Default, "users.pending.approve"),
		i18n.T(i18n.Default, "users.pending.selected"),
		i18n.T(i18n.Default, "users.pending.col_requested"),
		i18n.T(i18n.Default, "common.clear"),
	)
	if strings.Contains(body, ordinary.Email) {
		t.Errorf("the Pending tab must never list an ordinary account: %s", body)
	}
	if strings.Contains(body, "no role") {
		t.Errorf("the Pending tab must state that access awaits approval, never that the account has no role: %s", body)
	}

	// The ordinary list keeps every waiting account out, while the tab counts it.
	all := h.get(t, "/users", false).Body.String()
	if strings.Contains(all, waiting.Email) || strings.Contains(all, waiting.Name) {
		t.Errorf("the ordinary Users list must not expose a waiting account: %s", all)
	}
	db := h.rawDB(t)
	ordinaryCount := scanOneInt(t, db, `SELECT COUNT(*) FROM users WHERE pending = 0`)
	activeCount := scanOneInt(t, db, `SELECT COUNT(*) FROM users WHERE pending = 0 AND active = 1`)
	mustContain(t, all,
		"All <span>"+strconv.FormatInt(ordinaryCount+1, 10)+"</span>",
		"Active <span>"+strconv.FormatInt(activeCount, 10)+"</span>",
		"Deactivated <span>0</span>",
		`Pending <span class="users-pending-count">1</span>`,
	)
	// The tab link is the stable address the notification bell targets.
	mustContain(t, all, `href="/users?status=pending"`, `hx-get="/users?status=pending"`)
}

// TestUsersPendingEditAnswersLikeAMissingAccount is the concealment rule: an
// ordinary GET /users/{id}/edit for an account that waits for approval answers
// exactly as an unknown id does — same status, same body, no drawer — for the
// plain and the HTMX request alike.
func TestUsersPendingEditAnswersLikeAMissingAccount(t *testing.T) {
	h := newHarness(t)
	waiting := seedWaitingAccount(t, h, "google-subject-1", "waiting@example.com", "Waiting Person")
	// A second, otherwise identical instance without the waiting account: the
	// same id is simply unknown there, which is the answer the concealment must
	// reproduce byte for byte.
	control := newHarness(t)
	target := "/users/" + strconv.FormatInt(waiting.ID, 10) + "/edit"

	for _, hx := range []bool{false, true} {
		got := h.get(t, target, hx)
		want := control.get(t, target, hx)
		if got.Code != http.StatusNotFound || want.Code != http.StatusNotFound {
			t.Fatalf("edit a waiting account (hx=%v) = %d and unknown id = %d, want 404/404", hx, got.Code, want.Code)
		}
		if got.Body.String() != want.Body.String() {
			t.Errorf("edit a waiting account (hx=%v) must answer exactly as an unknown id\n--- waiting ---\n%s\n--- unknown ---\n%s", hx, got.Body.String(), want.Body.String())
		}
		if strings.Contains(got.Body.String(), waiting.Email) || strings.Contains(got.Body.String(), waiting.Name) {
			t.Errorf("the concealed answer must carry no fact about the waiting account: %s", got.Body.String())
		}
		if got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
			t.Errorf("concealed content type = %q, want %q", got.Header().Get("Content-Type"), want.Header().Get("Content-Type"))
		}
	}

	// The ordinary write routes refuse it as well: only the Pending tab approves.
	before := pendingAccountSnapshot(t, h)
	rec := h.postForm(t, target, url.Values{
		"name": {"Hijacked Name"}, "email": {"waiting@example.com"}, "role": {"agent"}, "active": {"true"}, "status": {"all"},
	}, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /users/{pending}/edit = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if after := pendingAccountSnapshot(t, h); after != before {
		t.Errorf("an ordinary edit must not touch a waiting account\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestUsersPendingApprovalActivatesWithTheChosenRole is S3/S9: one decision
// approves the whole selected batch, and every approved account becomes an
// active managed user with the ONE role the batch chose — audited as a role
// grant, never as a privilege the approval invented.
func TestUsersPendingApprovalActivatesWithTheChosenRole(t *testing.T) {
	h := newHarness(t)
	first := seedWaitingAccount(t, h, "google-subject-1", "first@example.com", "First Waiting")
	second := seedWaitingAccount(t, h, "google-subject-2", "second@example.com", "Second Waiting")
	db := h.rawDB(t)

	rec := h.postForm(t, "/users/pending/approve", url.Values{
		"role":     {"agent"},
		"user_ids": {strconv.FormatInt(first.ID, 10), strconv.FormatInt(second.ID, 10)},
	}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users?status=pending")

	for _, id := range []int64{first.ID, second.ID} {
		stored, err := h.store.UserStore().GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("read approved account %d: %v", id, err)
		}
		if stored.Pending || !stored.Active || stored.Role != domain.RoleAgent {
			t.Errorf("approved account %d active=%v pending=%v role=%q, want an active non-pending agent", id, stored.Active, stored.Pending, stored.Role)
		}
		if got := scanOneString(t, db, `SELECT reason FROM role_changes WHERE user_id = ?`, id); got != pendingApprovalReason {
			t.Errorf("approved account %d role audit reason = %q, want %q", id, got, pendingApprovalReason)
		}
	}
	if got := scanOneInt(t, db, `SELECT COUNT(*) FROM users WHERE pending = 1`); got != 0 {
		t.Errorf("accounts still waiting = %d, want none after the approval", got)
	}

	// The approved accounts left the queue and joined ordinary management.
	pendingTab := h.get(t, "/users?status=pending", false).Body.String()
	mustContain(t, pendingTab, i18n.T(i18n.Default, "users.pending.empty"))
	if strings.Contains(pendingTab, first.Email) || strings.Contains(pendingTab, second.Email) {
		t.Errorf("an approved account must leave the Pending tab: %s", pendingTab)
	}
	all := h.get(t, "/users", false).Body.String()
	mustContain(t, all, first.Email, second.Email)
}

// TestUsersPendingApprovalRespectsTheGrantMatrix is S9 at the role choice: the
// bulk approval grants exactly what an ordinary edit could grant. Root may
// approve as admin, an admin may not, and nobody may approve as root.
func TestUsersPendingApprovalRespectsTheGrantMatrix(t *testing.T) {
	h := newHarness(t)
	rootSession := seedActorWithSession(t, h, "Root", "root@tkt.test", domain.RoleRoot)
	waiting := seedWaitingAccount(t, h, "google-subject-1", "waiting@example.com", "Waiting Person")
	rootTab := asActor(h, rootSession, "/users?status=pending", false)
	mustContain(t, rootTab.Body, `value="admin"`)

	adminTab := h.get(t, "/users?status=pending", false)
	if strings.Contains(adminTab.Body.String(), `value="admin"`) {
		t.Errorf("an admin must not be offered the admin role in the approval picker: %s", adminTab.Body.String())
	}

	form := url.Values{"role": {"admin"}, "user_ids": {strconv.FormatInt(waiting.ID, 10)}}
	before := pendingAccountSnapshot(t, h)
	refused := h.postForm(t, "/users/pending/approve", form, false)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("admin approving as admin = %d, want 403: %s", refused.Code, refused.Body.String())
	}
	// The refusal is the same sentence the edit drawer answers for a peer-admin
	// grant, because approval reuses the managed-user grant matrix.
	mustContain(t, refused.Body.String(), "admin accounts require root")
	if after := pendingAccountSnapshot(t, h); after != before {
		t.Errorf("a refused admin grant must write nothing\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}

	form.Set("role", "root")
	refusedRoot := h.postForm(t, "/users/pending/approve", form, false)
	if refusedRoot.Code != http.StatusForbidden {
		t.Fatalf("approving as root = %d, want 403: %s", refusedRoot.Code, refusedRoot.Body.String())
	}
	mustContain(t, refusedRoot.Body.String(), i18n.T(i18n.Default, "error.root_protected"))
	if after := pendingAccountSnapshot(t, h); after != before {
		t.Errorf("a refused root grant must write nothing\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}

	form.Set("role", "admin")
	approved := h.postFormAs(t, "/users/pending/approve", form, rootSession)
	wantRedirect(t, approved, http.StatusSeeOther, "/users?status=pending")
	stored, err := h.store.UserStore().GetByID(context.Background(), waiting.ID)
	if err != nil || stored.Pending || !stored.Active || stored.Role != domain.RoleAdmin {
		t.Fatalf("root approving as admin = %+v (err %v), want an active admin", stored, err)
	}
}

// TestUsersPendingApprovalRefusesBadBatchesWithoutWriting is the atomicity
// contract: an empty selection, a repeated account, a stale account, a mixed
// batch, an invalid role and an actor without user management are each refused
// with their exact localized answer, and the database is left untouched —
// including the valid entry of a mixed batch.
func TestUsersPendingApprovalRefusesBadBatchesWithoutWriting(t *testing.T) {
	h := newHarness(t)
	first := seedWaitingAccount(t, h, "google-subject-1", "first@example.com", "First Waiting")
	second := seedWaitingAccount(t, h, "google-subject-2", "second@example.com", "Second Waiting")
	settled := h.createUser(t, "Already Managed", "managed@example.com", "s3cret-pass-12")
	agentSession := seedActorWithSession(t, h, "Agent", "agent-actor@tkt.test", domain.RoleAgent)

	firstID := strconv.FormatInt(first.ID, 10)
	secondID := strconv.FormatInt(second.ID, 10)
	settledID := strconv.FormatInt(settled.ID, 10)

	for _, tc := range []struct {
		name    string
		session string
		form    url.Values
		want    int
		message string
	}{
		{
			name: "no selection", form: url.Values{"role": {"agent"}},
			want: http.StatusUnprocessableEntity, message: i18n.T(i18n.Default, "error.pending_approval_selection_required"),
		},
		{
			name: "repeated account", form: url.Values{"role": {"agent"}, "user_ids": {firstID, firstID}},
			want: http.StatusUnprocessableEntity, message: i18n.T(i18n.Default, "error.pending_approval_duplicate_account"),
		},
		{
			name: "stale account", form: url.Values{"role": {"agent"}, "user_ids": {settledID}},
			want: http.StatusNotFound, message: (&domain.NotFoundError{Kind: "user", ID: settled.ID}).Error(),
		},
		{
			name: "mixed batch", form: url.Values{"role": {"agent"}, "user_ids": {firstID, settledID}},
			want: http.StatusNotFound, message: (&domain.NotFoundError{Kind: "user", ID: settled.ID}).Error(),
		},
		{
			name: "invalid role", form: url.Values{"role": {"wizard"}, "user_ids": {secondID}},
			want: http.StatusUnprocessableEntity, message: i18n.T(i18n.Default, "error.invalid_role"),
		},
		{
			name: "no user management", session: agentSession, form: url.Values{"role": {"user"}, "user_ids": {secondID}},
			want: http.StatusForbidden, message: i18n.T(i18n.Default, "error.forbidden"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := pendingAccountSnapshot(t, h)
			var rec *httptest.ResponseRecorder
			if tc.session != "" {
				rec = h.postFormAs(t, "/users/pending/approve", tc.form, tc.session)
			} else {
				rec = h.postForm(t, "/users/pending/approve", tc.form, false)
			}
			if rec.Code != tc.want {
				t.Fatalf("POST /users/pending/approve (%s) = %d, want %d: %s", tc.name, rec.Code, tc.want, rec.Body.String())
			}
			mustContain(t, rec.Body.String(), tc.message)
			if after := pendingAccountSnapshot(t, h); after != before {
				t.Errorf("a refused batch must write nothing (%s)\n--- before ---\n%s\n--- after ---\n%s", tc.name, before, after)
			}
		})
	}
}

// TestUsersPendingApprovalHTMXContract pins the HTMX half of the bulk form: the
// success replaces the whole Users screen and reports the save, and a refusal
// keeps the same target so the specific localized reason reaches the page.
func TestUsersPendingApprovalHTMXContract(t *testing.T) {
	h := newHarness(t)
	waiting := seedWaitingAccount(t, h, "google-subject-1", "waiting@example.com", "Waiting Person")

	saved := h.postForm(t, "/users/pending/approve", url.Values{
		"role": {"user"}, "user_ids": {strconv.FormatInt(waiting.ID, 10)},
	}, true)
	if saved.Code != http.StatusOK {
		t.Fatalf("HX approval = %d, want 200: %s", saved.Code, saved.Body.String())
	}
	for key, want := range map[string]string{
		"HX-Retarget":           "#users-root",
		"HX-Reswap":             "outerHTML",
		"HX-Trigger-After-Swap": "users:saved",
		"Vary":                  "HX-Request",
		"Content-Type":          "text/html; charset=utf-8",
	} {
		if got := saved.Header().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(saved.Header().Get("X-Save-Feedback"), i18n.T(i18n.Default, "feedback.saved")) {
		t.Errorf("X-Save-Feedback = %q, want the saved outcome", saved.Header().Get("X-Save-Feedback"))
	}
	body := saved.Body.String()
	if strings.Contains(body, "<html") || strings.Count(body, `id="users-root"`) != 1 {
		t.Fatalf("HX approval must answer the users screen fragment only: %s", body)
	}
	mustContain(t, body, i18n.T(i18n.Default, "users.pending.empty"))

	// A refusal answers the specific reason at the same target.
	refused := h.postForm(t, "/users/pending/approve", url.Values{"role": {"agent"}}, true)
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("HX refusal = %d, want 422: %s", refused.Code, refused.Body.String())
	}
	if refused.Header().Get("HX-Retarget") != "#users-root" || refused.Header().Get("HX-Reswap") != "outerHTML" {
		t.Errorf("HX refusal swap = %q/%q, want #users-root/outerHTML", refused.Header().Get("HX-Retarget"), refused.Header().Get("HX-Reswap"))
	}
	mustContain(t, refused.Body.String(), i18n.T(i18n.Default, "error.pending_approval_selection_required"), `id="users-root"`)
}

// seedWaitingAccountWithProvider arranges one waiting account through the real
// company sign-in flow with an explicit subject, so several accounts can sign
// in with the same fake provider without colliding on the identity pair.
func seedWaitingAccountWithProvider(t *testing.T, h *harness, provider, subject, email, name string) *domain.User {
	t.Helper()
	companySignInStore(t, h, companySignInIn(application.NewAccountsPending))
	claims := application.CompanyIdentityClaims{
		Issuer:        "https://accounts.google.com",
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		HostedDomain:  "example.com",
		Name:          name,
	}
	if provider == "microsoft" {
		claims = application.CompanyIdentityClaims{
			Issuer:   "https://login.microsoftonline.com/" + companyTenant + "/v2.0",
			TenantID: companyTenant,
			Subject:  subject,
			Email:    email,
			Name:     name,
		}
	}
	h.companyProvider.claims = claims
	cookie, _ := companyStart(t, h, provider)
	if rec := companyCallback(t, h, provider, cookie.Value, cookie.Value); rec.Code != http.StatusOK {
		t.Fatalf("pending %s sign-in for %q = %d, want 200: %s", provider, email, rec.Code, rec.Body.String())
	}
	_, account, err := h.store.CompanyIdentityStore().ResolveIdentity(context.Background(), application.CompanyProvider(provider), claims.Issuer, subject)
	if err != nil {
		t.Fatalf("resolve the pending account %q: %v", email, err)
	}
	return account
}

// TestUsersPendingTabShowsTheStoredSignInMethod is S12's Sign-in column: each row
// states the provider the stored identity carries, the column renders only that
// closed label, and no identity detail (issuer, subject, address) reaches the
// page.
func TestUsersPendingTabShowsTheStoredSignInMethod(t *testing.T) {
	h := newHarness(t)
	google := seedWaitingAccountWithProvider(t, h, "google", "subject-google", "ana@example.com", "Ana Perez")
	microsoft := seedWaitingAccountWithProvider(t, h, "microsoft", "subject-microsoft", "bruno@example.com", "Bruno Salas")

	body := h.get(t, "/users?status=pending", false).Body.String()
	mustContain(t, body,
		i18n.T(i18n.Default, "users.pending.col_signin"),
		i18n.T(i18n.Default, "label.company_provider.google"),
		i18n.T(i18n.Default, "label.company_provider.microsoft"),
	)
	for _, row := range []struct {
		user           *domain.User
		want, unwanted string
	}{
		{google, "Google", "Microsoft"},
		{microsoft, "Microsoft", "Google"},
	} {
		cell := pendingSignInCell(t, body, row.user)
		if !strings.Contains(cell, row.want) || strings.Contains(cell, row.unwanted) {
			t.Errorf("sign-in cell for %q = %q, want %q and not %q", row.user.Email, cell, row.want, row.unwanted)
		}
	}
	for _, leak := range []string{"subject-google", "subject-microsoft", "accounts.google.com", "login.microsoftonline.com", companyTenant} {
		if strings.Contains(body, leak) {
			t.Errorf("the Pending tab must not publish the identity's own record, found %q", leak)
		}
	}
}

// pendingSignInCell isolates one row's Sign-in cell from the rendered screen, so
// an assertion about a provider cannot be satisfied by a neighbouring row.
func pendingSignInCell(t *testing.T, body string, user *domain.User) string {
	t.Helper()
	row := strings.Index(body, `data-user-email="`+user.Email+`"`)
	if row < 0 {
		t.Fatalf("the rendered Pending tab has no row for %q", user.Email)
	}
	cell := strings.Index(body[row:], `<td class="users-pending-signin">`)
	if cell < 0 {
		t.Fatalf("the row for %q has no Sign-in cell", user.Email)
	}
	start := row + cell
	end := strings.Index(body[start:], "</td>")
	if end < 0 {
		t.Fatalf("the Sign-in cell for %q is not terminated", user.Email)
	}
	return body[start : start+end]
}

// TestUsersPendingSignInStatesEveryStoredFact covers the two shapes a real
// account cannot always produce: several stored identities render in display
// order, and an account with no stored identity says so instead of guessing a
// provider from its address.
func TestUsersPendingSignInStatesEveryStoredFact(t *testing.T) {
	data := fixtureUsersPendingData()
	data.PendingRows[0].SignInLabels = []string{"label.company_provider.google", "label.company_provider.microsoft"}
	data.PendingRows[1].SignInLabels = nil
	data.PendingRows[1].SignInUnknown = true
	body := renderGolden(t, "users_index", "users_screen", data, true)
	mustContain(t, body,
		"Google · Microsoft",
		i18n.T(i18n.Default, "users.pending.signin_unknown"),
	)

	for _, tc := range []struct {
		provider application.CompanyProvider
		want     string
	}{
		{application.ProviderGoogle, "label.company_provider.google"},
		{application.ProviderMicrosoft, "label.company_provider.microsoft"},
		{application.CompanyProvider("github"), "label.company_provider.unknown"},
	} {
		if got := companyProviderLabel(tc.provider); got != tc.want {
			t.Errorf("companyProviderLabel(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

// TestUsersPendingRowMenuApprovesThatRow is S12's per-row control: the ⋯ trigger
// opens a real menu whose items approve THAT account with a role the actor may
// grant, reusing the same approval route and grant matrix as the bulk bar. There
// is no reject item: rejection uses the separate bulk action.
func TestUsersPendingRowMenuApprovesThatRow(t *testing.T) {
	h := newHarness(t)
	first := seedWaitingAccountWithProvider(t, h, "google", "subject-1", "first@example.com", "First Waiting")
	second := seedWaitingAccountWithProvider(t, h, "google", "subject-2", "second@example.com", "Second Waiting")
	db := h.rawDB(t)

	body := h.get(t, "/users?status=pending", false).Body.String()
	mustContain(t, body,
		`data-users-pending-menu`,
		`aria-haspopup="menu"`,
		`aria-controls="pending-menu-`+strconv.FormatInt(first.ID, 10)+`"`,
		`aria-expanded="false"`,
		`id="pending-menu-`+strconv.FormatInt(first.ID, 10)+`"`,
		`role="menu"`,
		`role="menuitem"`,
		`form="users-pending-form"`,
	)
	menu := pendingRowMenu(t, body, first)
	// One item per grantable role, each its own POST of this row and that role.
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		want := fmt.Sprintf(`name="role" value="%s"`, role)
		if !strings.Contains(menu, want) {
			t.Errorf("the row menu must offer %q: %s", want, menu)
		}
	}
	if strings.Contains(menu, `value="admin"`) {
		t.Errorf("an admin must not be offered the admin role in a row menu: %s", menu)
	}
	if strings.Contains(strings.ToLower(menu), "reject") {
		t.Errorf("the approval menu must not submit a bulk rejection")
	}
	mustContain(t, body, `formaction="/users/pending/reject"`, `hx-post="/users/pending/reject"`)

	// The menu's own submission approves exactly that row.
	rec := h.postForm(t, "/users/pending/approve", url.Values{
		"role": {"agent"}, "user_ids": {strconv.FormatInt(first.ID, 10)},
	}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/users?status=pending")
	stored, err := h.store.UserStore().GetByID(context.Background(), first.ID)
	if err != nil || stored.Pending || !stored.Active || stored.Role != domain.RoleAgent {
		t.Fatalf("approved row = %+v (err %v), want an active agent", stored, err)
	}
	untouched, err := h.store.UserStore().GetByID(context.Background(), second.ID)
	if err != nil || !untouched.Pending || untouched.Active {
		t.Fatalf("the other row = %+v (err %v), want it still waiting", untouched, err)
	}
	if got := scanOneInt(t, db, `SELECT COUNT(*) FROM role_changes WHERE user_id = ?`, first.ID); got != 1 {
		t.Errorf("role audits for the approved row = %d, want 1", got)
	}

	// Root may grant admin from the same menu; the matrix is the shared one.
	rootSession := seedActorWithSession(t, h, "Root", "root-menu@tkt.test", domain.RoleRoot)
	rootBody := asActor(h, rootSession, "/users?status=pending", false).Body
	rootMenu := pendingRowMenu(t, rootBody, second)
	if !strings.Contains(rootMenu, `name="role" value="admin"`) {
		t.Errorf("root must be offered the admin role in the row menu: %s", rootMenu)
	}
	if strings.Contains(rootMenu, `value="root"`) {
		t.Errorf("no actor may approve as root: %s", rootMenu)
	}
}

// pendingRowMenu isolates one row's menu panel from the rendered screen.
func pendingRowMenu(t *testing.T, body string, user *domain.User) string {
	t.Helper()
	marker := `id="pending-menu-` + strconv.FormatInt(user.ID, 10) + `"`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("the rendered Pending tab has no row menu for %q", user.Email)
	}
	end := strings.Index(body[start:], "</div>")
	if end < 0 {
		t.Fatalf("the row menu for %q is not terminated", user.Email)
	}
	return body[start : start+end]
}

// TestUsersPendingCountsAddUpWithMixedAccounts pins the count semantics with a
// mix of active, deactivated and waiting accounts, before and after an approval,
// and on every filter. The All count includes the waiting accounts — the selected
// design's 14 = 11 + 3 — while the ordinary All LIST keeps them out (S3: a
// waiting account belongs to the Pending tab only), so the count is deliberately
// not the row count of that list.
func TestUsersPendingCountsAddUpWithMixedAccounts(t *testing.T) {
	h := newHarness(t)
	active := h.createUser(t, "Ana Active", "ana@example.com", "s3cret-pass-12")
	deactivated := h.createUser(t, "Beto Off", "beto@example.com", "s3cret-pass-12")
	deactivated.Active = false
	if err := h.store.UserStore().Update(context.Background(), deactivated); err != nil {
		t.Fatal(err)
	}
	first := seedWaitingAccountWithProvider(t, h, "google", "subject-1", "first@example.com", "First Waiting")
	seedWaitingAccountWithProvider(t, h, "microsoft", "subject-2", "second@example.com", "Second Waiting")

	// Harness admin (active) + one active agent + one deactivated + two waiting.
	for _, tc := range []struct {
		status                               usersStatus
		all, activeN, deactivatedN, pendingN int
		rows                                 int
	}{
		{usersStatusAll, 5, 2, 1, 2, 3},
		{usersStatusActive, 5, 2, 1, 2, 2},
		{usersStatusDeactivated, 5, 2, 1, 2, 1},
		{usersStatusPending, 5, 2, 1, 2, 2},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			body := h.get(t, usersListURL(tc.status), false).Body.String()
			mustContain(t, body,
				`All <span>`+strconv.Itoa(tc.all)+`</span>`,
				`Active <span>`+strconv.Itoa(tc.activeN)+`</span>`,
				`Deactivated <span>`+strconv.Itoa(tc.deactivatedN)+`</span>`,
				`Pending <span class="users-pending-count">`+strconv.Itoa(tc.pendingN)+`</span>`,
			)
			if got := strings.Count(body, `<tr data-user-name=`); got != tc.rows {
				t.Errorf("%s rows = %d, want %d: %s", tc.status, got, tc.rows, first.Email)
			}
			if tc.status != usersStatusPending {
				for _, waiting := range []string{first.Email, "second@example.com"} {
					if strings.Contains(body, waiting) {
						t.Errorf("the %s list must keep %q in the Pending tab only", tc.status, waiting)
					}
				}
				return
			}
			if strings.Contains(body, active.Email) || strings.Contains(body, deactivated.Email) {
				t.Errorf("the Pending tab must list no ordinary account: %s", body)
			}
		})
	}

	// An approval moves one account from waiting to active and leaves All and
	// Deactivated alone.
	wantRedirect(t, h.postForm(t, "/users/pending/approve", url.Values{
		"role": {string(domain.RoleUser)}, "user_ids": {strconv.FormatInt(first.ID, 10)},
	}, false), http.StatusSeeOther, "/users?status=pending")
	after := h.get(t, "/users", false).Body.String()
	mustContain(t, after,
		`All <span>5</span>`,
		`Active <span>3</span>`,
		`Deactivated <span>1</span>`,
		`Pending <span class="users-pending-count">1</span>`,
	)
	mustContain(t, after, first.Email)
}

// TestUsersPendingReadFailureFailsClosed proves the composition's failure state:
// when the pending read cannot answer, the screen answers the generic 500 instead
// of rendering a list that silently looks empty, on both the ordinary address and
// the Pending tab.
func TestUsersPendingReadFailureFailsClosed(t *testing.T) {
	h := newHarness(t)
	seedWaitingAccount(t, h, "subject-1", "waiting@example.com", "Waiting Person")

	mux := http.NewServeMux()
	NewUserHandlers(h.users, h.renderer).
		WithPendingUsers(application.NewPendingUsersService(failingPendingStore{h.store.PendingUserStore()}, h.clock)).
		Register(mux)
	for _, target := range []string{"/users", "/users?status=pending"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("GET %s with an unavailable pending read = %d, want 500: %s", target, rec.Code, rec.Body.String())
		}
		mustContain(t, rec.Body.String(), i18n.T(i18n.Default, "error.internal_server"))
		if strings.Contains(rec.Body.String(), "waiting@example.com") || strings.Contains(rec.Body.String(), "users-root") {
			t.Errorf("a failed pending read must not render a partial screen: %s", rec.Body.String())
		}
	}
}

// failingPendingStore simulates the pending read seam failing (this layer's only
// allowed fake): everything else answers from the real store.
type failingPendingStore struct{ application.PendingUserStore }

func (f failingPendingStore) ListPending(context.Context) ([]domain.User, error) {
	return nil, errors.New("pending read unavailable")
}

// TestUsersPendingTabNeedsItsService proves the wiring contract: a composition
// without the pending service has no Pending tab, its address answers as an
// unmounted route, and the ordinary screen it does serve carries no pending
// affordance at all.
func TestUsersPendingTabNeedsItsService(t *testing.T) {
	h := newHarness(t)
	mux := http.NewServeMux()
	NewUserHandlers(h.users, h.renderer).Register(mux)

	request := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		return rec
	}
	if rec := request("/users?status=pending"); rec.Code != http.StatusNotFound {
		t.Fatalf("the pending address without the service = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	body := request("/users").Body.String()
	if strings.Contains(body, "/users?status=pending") || strings.Contains(body, "users-pending-count") {
		t.Errorf("an unwired composition must render no Pending tab: %s", body)
	}
	mustContain(t, body, `class="users-list"`, `id="users-new-launcher"`)
}

// TestUsersPendingTabSpeaksSpanish is S13 at the surface: the tab renders from
// the catalogue, so a Spanish instance reads neutral tú copy and never the
// English chrome.
func TestUsersPendingTabSpeaksSpanish(t *testing.T) {
	body := renderLocalized(t, i18n.Spanish, "users_index", "users_screen", fixtureUsersPendingData())
	for _, want := range []string{
		"Pendientes", "Pendiente", "Aprobar como", "Aprobar", "Limpiar",
		"seleccionadas", "Solicitado", "Selecciona todas las cuentas pendientes",
		"Selecciona Wanda Alvarez",
		"Estas cuentas están inactivas y esperan aprobación.",
		"Método de ingreso",
		"Acciones para Wanda Alvarez",
		"Aprobar como Usuario",
		"Aprobar como Agente",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Spanish Pending tab must render %q, got: %.1200s", want, body)
		}
	}
	for _, unwanted := range []string{"Approve as", ">Requested<", ">selected<", "Select all pending accounts", ">Sign-in<", "Actions for "} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the Spanish Pending tab must not render the English %q", unwanted)
		}
	}
}

// TestUsersPendingTabPreservesOrdinaryManagement is the preservation half: the
// routes that share the Users handler keep their behaviour with the Pending tab
// present — the ordinary list and its filters, the edit drawer, and the ordinary
// save path.
func TestUsersPendingTabPreservesOrdinaryManagement(t *testing.T) {
	h := newHarness(t)
	ana := h.createUser(t, "Ana Torres", "ana@example.com", "s3cret-pass-12")
	seedWaitingAccount(t, h, "google-subject-1", "waiting@example.com", "Waiting Person")
	db := h.rawDB(t)

	all := h.get(t, "/users", false)
	if all.Code != http.StatusOK || all.Header().Get("Vary") != "HX-Request" {
		t.Fatalf("ordinary list = %d/%q, want 200/HX-Request", all.Code, all.Header().Get("Vary"))
	}
	body := all.Body.String()
	mustContain(t, body,
		`class="users-status-tabs"`,
		`href="/users?status=active"`,
		`href="/users?status=deactivated"`,
		`class="users-list"`,
		"Created", // the ordinary table keeps its own columns
		ana.Email,
		`href="/users/new"`,
	)
	ordinary := scanOneInt(t, db, `SELECT COUNT(*) FROM users WHERE pending = 0`)
	// All counts every account, the waiting one included; the ordinary rows never
	// list it.
	mustContain(t, body, "All <span>"+strconv.FormatInt(ordinary+1, 10)+"</span>")
	active := h.get(t, "/users?status=active", true)
	if active.Code != http.StatusOK || strings.Contains(active.Body.String(), "waiting@example.com") {
		t.Fatalf("active filter must keep the waiting account out: %d/%s", active.Code, active.Body.String())
	}
	mustContain(t, active.Body.String(), ana.Email)

	// The edit drawer still opens and still saves for an ordinary account.
	path := "/users/" + strconv.FormatInt(ana.ID, 10) + "/edit"
	edit := h.get(t, path, false)
	if edit.Code != http.StatusOK {
		t.Fatalf("ordinary edit = %d, want 200: %s", edit.Code, edit.Body.String())
	}
	mustContain(t, edit.Body.String(), `id="users-drawer-host"`, `id="user-drawer-form"`, ana.Email)
	saved := h.postForm(t, path, url.Values{
		"name": {"Ana Updated"}, "email": {"ana@example.com"}, "role": {"agent"}, "active": {"true"}, "status": {"active"},
	}, true)
	if saved.Code != http.StatusOK || saved.Header().Get("HX-Retarget") != "#users-root" {
		t.Fatalf("ordinary save = %d/%q, want 200/#users-root: %s", saved.Code, saved.Header().Get("HX-Retarget"), saved.Body.String())
	}
	if got := scanOneString(t, db, `SELECT name FROM users WHERE id = ?`, ana.ID); got != "Ana Updated" {
		t.Errorf("ordinary save stored name %q, want %q", got, "Ana Updated")
	}
}

// failingUserListStore is the operational failure seam for the pending screen's
// read: every write goes to the real store, and List — the read the screen is
// rendered from — answers an error once armed.
type failingUserListStore struct {
	application.UserStore
	fail bool
}

func (f *failingUserListStore) List(ctx context.Context) ([]domain.User, error) {
	if f.fail {
		return nil, errors.New("user list unavailable")
	}
	return f.UserStore.List(ctx)
}

// pendingScreenWithFailingRead mounts the Pending routes over the real pending
// use case and a user service whose List fails, so a committed bulk action can be
// observed when the screen read that follows it cannot answer.
func pendingScreenWithFailingRead(t *testing.T, h *harness) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	NewUserHandlers(application.NewUserService(&failingUserListStore{UserStore: h.store.UserStore(), fail: true}, h.clock), h.renderer).
		WithPendingUsers(application.NewPendingUsersService(h.store.PendingUserStore(), h.clock)).
		Register(mux)
	return mux
}

// TestUsersPendingBulkActionsReportTheCommittedSaveWhenTheScreenReadFails is
// S3/S12 at the boundary the compensation exists for: the batch is durable before
// the screen is read, so a failed read may never answer an uncommitted failure.
// Both bulk actions answer a truthful success — 200, the Saved feedback on both
// delivery channels, HX-Refresh and no swap — and the stored effect is there;
// replaying the same ids answers the stale refusal the operator would see next.
func TestUsersPendingBulkActionsReportTheCommittedSaveWhenTheScreenReadFails(t *testing.T) {
	for _, tc := range []struct {
		action, status  string
		role            domain.Role
		active, pending bool
	}{
		{"approve", "active", domain.RoleAgent, true, false},
		{"reject", "deactivated", domain.RoleUser, false, false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			h := newHarness(t)
			email := tc.action + "-read-failure@example.com"
			waiting := seedWaitingAccount(t, h, tc.action+"-read-failure", email, "Read Failure")
			form := url.Values{"role": {"agent"}, "user_ids": {strconv.FormatInt(waiting.ID, 10)}}
			req := httptest.NewRequest(http.MethodPost, "/users/pending/"+tc.action, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
			req.Header.Set("HX-Request", "true")
			rec := httptest.NewRecorder()
			h.mw.Wrap(pendingScreenWithFailingRead(t, h)).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("committed %s answered %d, want 200: %s", tc.action, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"message":"`+saveFeedbackSaved+`"`) {
				t.Errorf("%s feedback = %q, want the Saved message", tc.action, got)
			}
			if rec.Header().Get("HX-Refresh") != "true" || rec.Header().Get("HX-Retarget") != "" || strings.Contains(rec.Body.String(), `id="users-root"`) {
				t.Errorf("%s fallback = refresh %q retarget %q body %q, want a reload without a swap",
					tc.action, rec.Header().Get("HX-Refresh"), rec.Header().Get("HX-Retarget"), rec.Body.String())
			}
			// The reload HX-Refresh asks for reads the flash record, so it is the
			// authenticated cookie with the lifetime a native save issues.
			cookie := findCookie(rec, saveFeedbackCookie)
			if cookie == nil || cookie.Value == "" || cookie.Path != "/" || !cookie.HttpOnly ||
				cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != int(saveFeedbackTTL.Seconds()) {
				t.Errorf("%s flash cookie = %#v, want the canonical saved record", tc.action, cookie)
			}
			if body := h.get(t, "/users?status=pending", true).Body.String(); strings.Contains(body, email) {
				t.Errorf("%s left the account in the pending queue", tc.action)
			}
			if body := h.get(t, "/users?status="+tc.status, true).Body.String(); !strings.Contains(body, email) {
				t.Errorf("%s account is not listed under %s", tc.action, tc.status)
			}
			stored, err := h.store.UserStore().GetByID(context.Background(), waiting.ID)
			if err != nil {
				t.Fatalf("%s stored account: %v", tc.action, err)
			}
			if stored.Role != tc.role || stored.Active != tc.active || stored.Pending != tc.pending {
				t.Errorf("%s stored role=%q active=%v pending=%v, want %q/%v/%v",
					tc.action, stored.Role, stored.Active, stored.Pending, tc.role, tc.active, tc.pending)
			}
			retry := h.postForm(t, "/users/pending/"+tc.action, form, true)
			if retry.Code != http.StatusNotFound || retry.Header().Get("HX-Retarget") != "#users-root" {
				t.Errorf("%s replay = %d/%q, want the stale 404 on the screen", tc.action, retry.Code, retry.Header().Get("HX-Retarget"))
			}
		})
	}
}

// assertSwappedScreenFragment pins a swapped body to the users_screen fragment
// the retarget names: the users-root element and none of the shell a whole
// document would carry, whose swap would nest a second shell in the page.
func assertSwappedScreenFragment(t *testing.T, label, body string) {
	t.Helper()
	if !strings.HasPrefix(strings.TrimLeft(body, "\n"), `<div id="users-root"`) {
		t.Errorf("%s body starts %q, want the users_screen fragment", label, body)
	}
	for _, shell := range []string{"<!DOCTYPE", "<html", "<body", `id="notification-bell"`, `class="topbar"`, `class="tabbar"`} {
		if strings.Contains(body, shell) {
			t.Errorf("%s body carries page shell %q", label, shell)
		}
	}
}

// TestUsersPendingRefusalRendersTheScreenFragment is S12 at the refusal contract:
// a refused batch answers #users-root with outerHTML, so its body is the same
// users_screen fragment the success path swaps, carrying the sentence that names
// the refusal — not a whole document.
func TestUsersPendingRefusalRendersTheScreenFragment(t *testing.T) {
	h := newHarness(t)
	waiting := seedWaitingAccount(t, h, "refusal-fragment", "refusal-fragment@example.com", "Refusal Fragment")
	refusal := h.postForm(t, "/users/pending/approve", url.Values{"role": {"agent"}}, true)
	if refusal.Code != http.StatusUnprocessableEntity || refusal.Header().Get("HX-Retarget") != "#users-root" || refusal.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("refusal = %d/%v, want a 422 swapped into the screen", refusal.Code, refusal.Header())
	}
	mustContain(t, refusal.Body.String(), `class="users-error" role="alert">`+"select at least one account to approve"+`</div>`)
	assertSwappedScreenFragment(t, "refusal", refusal.Body.String())
	success := h.postForm(t, "/users/pending/approve", url.Values{"role": {"agent"}, "user_ids": {strconv.FormatInt(waiting.ID, 10)}}, true)
	if success.Code != http.StatusOK {
		t.Fatalf("approval = %d, want 200: %s", success.Code, success.Body.String())
	}
	assertSwappedScreenFragment(t, "success", success.Body.String())
}

// TestUsersPendingSubmitUsesTheSharedBusyGuard pins the client half of the bulk
// contract (issue #419, S3/S12) beside its HTTP regressions: the Pending tab's
// form shares the drawer's in-flight guard, so a second submit while the first
// bulk POST is in flight is cancelled, and the guard is released when the swapped
// screen settles or the request fails. A non-idempotent batch can therefore never
// be posted twice by a double click.
func TestUsersPendingSubmitUsesTheSharedBusyGuard(t *testing.T) {
	h := newHarness(t)
	js := h.get(t, "/static/users.js", false).Body.String()
	for _, marker := range []string{
		`if (event.target.matches("#users-pending-form, .users-row-menu form")) {`,
		"      if (busy) {\n        event.preventDefault();\n        return;\n      }\n      busy = true;\n      pendingScreenSave = true;",
		"    if (pendingScreenSave) {\n      pendingScreenSave = false;\n      busy = false;",
		`event.detail?.elt?.matches("#users-pending-form, .users-row-menu form") && !event.detail?.successful`,
	} {
		if !strings.Contains(js, marker) {
			t.Errorf("users.js lacks the pending busy-guard marker %q", marker)
		}
	}
}
