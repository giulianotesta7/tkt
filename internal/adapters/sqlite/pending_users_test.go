package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #419, S3: the Pending tab lists the accounts waiting for approval and
// approves them in bulk. Approval is ONE immediate transaction — pending clears
// and active is set together, the chosen role is granted, and the role audit
// rides along — so no account is ever left half approved.

func newPendingUserTestStore(t *testing.T) (*Store, application.PendingUserStore) {
	t.Helper()
	s := newTestDB(t)
	return s, newPendingUserStore(s.db)
}

// seedApprovalActor inserts the administrator whose id the role audit records
// as the approving actor (role_changes.actor_user_id references users).
func seedApprovalActor(t *testing.T, s *Store) int64 {
	t.Helper()
	actor := &domain.User{Name: "Ada", Email: "ada@example.com", PasswordHash: "h", Role: domain.RoleAdmin, Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(context.Background(), actor); err != nil {
		t.Fatalf("seed approval actor: %v", err)
	}
	return actor.ID
}

// seedPendingIdentity attaches one provider identity to an account through the
// real identity port, so the fixture is exactly the row a company sign-in
// stores (issue #419, S7).
func seedPendingIdentity(t *testing.T, s *Store, userID int64, provider application.CompanyProvider) {
	t.Helper()
	identity := &application.CompanyIdentity{
		UserID: userID, Provider: provider,
		Issuer: "https://issuer.example.com", Subject: fmt.Sprintf("subject-%d-%s", userID, provider),
		Email: "identity@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}
	if err := s.CompanyIdentityStore().LinkIdentity(context.Background(), identity); err != nil {
		t.Fatalf("seed %s identity for user %d: %v", provider, userID, err)
	}
}

// Issue #419, S3/S12: the Sign-in column's facts come from the stored
// identities joined to the accounts that wait for approval. One row per
// identity, ordered by account then provider; an account with no identity has
// no row, an ordinary account's identity is never listed, and approving the
// account takes it and its rows out of the pending read while the identity
// itself stays stored.
func TestPendingUserStoreAnswersTheStoredSignInMethods(t *testing.T) {
	s, pending := newPendingUserTestStore(t)
	ctx := context.Background()
	googleOnly := seedPendingUser(t, s, "Wanda", "wanda@example.com")
	both := seedPendingUser(t, s, "Vic", "vic@example.com")
	none := seedPendingUser(t, s, "Nora", "nora@example.com")
	settled := seedApprovalActor(t, s)
	seedPendingIdentity(t, s, googleOnly, application.ProviderGoogle)
	seedPendingIdentity(t, s, both, application.ProviderMicrosoft)
	seedPendingIdentity(t, s, both, application.ProviderGoogle)
	seedPendingIdentity(t, s, settled, application.ProviderMicrosoft)

	rows, err := pending.ListPendingSignIn(ctx)
	if err != nil {
		t.Fatalf("ListPendingSignIn: %v", err)
	}
	want := []application.PendingSignIn{
		{UserID: googleOnly, Provider: application.ProviderGoogle},
		{UserID: both, Provider: application.ProviderGoogle},
		{UserID: both, Provider: application.ProviderMicrosoft},
	}
	if len(rows) != len(want) {
		t.Fatalf("ListPendingSignIn = %+v, want %+v", rows, want)
	}
	for i, expected := range want {
		if rows[i] != expected {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], expected)
		}
	}
	for _, row := range rows {
		if row.UserID == settled {
			t.Errorf("an ordinary account's identity must never reach the pending read: %+v", row)
		}
		if row.UserID == none {
			t.Errorf("an account with no stored identity must answer no row: %+v", row)
		}
	}

	// Approval takes the account out of the pending read; the identity itself is
	// historical and stays stored.
	if _, err := pending.ApprovePending(ctx, []application.PendingApproval{{ID: googleOnly, Role: domain.RoleUser}}, settled, testClock); err != nil {
		t.Fatalf("ApprovePending: %v", err)
	}
	after, err := pending.ListPendingSignIn(ctx)
	if err != nil {
		t.Fatalf("ListPendingSignIn after approval: %v", err)
	}
	for _, row := range after {
		if row.UserID == googleOnly {
			t.Errorf("an approved account must leave the pending sign-in read: %+v", after)
		}
	}
	if got := countRows(t, s, `SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, googleOnly); got != 1 {
		t.Errorf("the approved account's identity rows = %d, want the stored identity kept", got)
	}
}

func TestPendingUserStoreListsAndApproves(t *testing.T) {
	s, pending := newPendingUserTestStore(t)
	ctx := context.Background()
	actorID := seedApprovalActor(t, s)
	wanda := seedPendingUser(t, s, "Wanda", "wanda@example.com")
	vic := seedPendingUser(t, s, "Vic", "vic@example.com")

	waiting, err := pending.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(waiting) != 2 || waiting[0].ID != wanda || waiting[1].ID != vic {
		t.Fatalf("ListPending = %+v, want both accounts ordered by id", waiting)
	}
	for _, u := range waiting {
		if !u.Pending || u.Active {
			t.Errorf("listed account %s = %+v, want pending and inactive", u.Email, u)
		}
	}

	approved, err := pending.ApprovePending(ctx, []application.PendingApproval{{ID: wanda, Role: domain.RoleAgent}}, actorID, testClock)
	if err != nil {
		t.Fatalf("ApprovePending: %v", err)
	}
	if len(approved) != 1 {
		t.Fatalf("ApprovePending returned %d accounts, want 1", len(approved))
	}
	got := approved[0]
	if got.ID != wanda || got.Name != "Wanda" || got.Email != "wanda@example.com" || got.Role != domain.RoleAgent || !got.Active || got.Pending {
		t.Fatalf("approved account = %+v, want the activated agent", got)
	}
	stored, err := s.UserStore().GetByID(ctx, wanda)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Active || stored.Pending || stored.Role != domain.RoleAgent {
		t.Fatalf("stored account = %+v, want active agent with no pending flag", stored)
	}
	if stored.CreatedAt != testClock {
		t.Errorf("created_at = %v, want the sign-in instant %v", stored.CreatedAt, testClock)
	}

	var fromRole, toRole, reason string
	var auditActor int64
	var createdAt string
	if err := s.db.QueryRow(`SELECT from_role, to_role, actor_user_id, reason, created_at FROM role_changes WHERE user_id = ?`, wanda).
		Scan(&fromRole, &toRole, &auditActor, &reason, &createdAt); err != nil {
		t.Fatalf("role audit: %v", err)
	}
	if fromRole != string(domain.RoleUser) || toRole != string(domain.RoleAgent) || auditActor != actorID {
		t.Errorf("role audit = %s→%s by %d, want user→agent by %d", fromRole, toRole, auditActor, actorID)
	}
	if reason != pendingApprovalReason || createdAt != formatTime(testClock) {
		t.Errorf("role audit reason/instant = %q/%q, want %q/%q", reason, createdAt, pendingApprovalReason, formatTime(testClock))
	}

	// The approved account is a managed user now; the other one still waits.
	waiting, err = pending.ListPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].ID != vic {
		t.Fatalf("ListPending after approval = %+v, want only the second account", waiting)
	}
	managed, err := s.UserStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 2 {
		t.Fatalf("managed list = %+v, want the approving actor and the approved account", managed)
	}
	for _, u := range managed {
		if u.ID == vic {
			t.Fatalf("account %d still waits for approval and must not be a managed user", vic)
		}
	}

	// The same approval is stale now: it changes nothing and audits nothing new.
	if _, err := pending.ApprovePending(ctx, []application.PendingApproval{{ID: wanda, Role: domain.RoleAdmin}}, actorID, testClock); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second approval = %v, want ErrNotFound", err)
	}
	stored, err = s.UserStore().GetByID(ctx, wanda)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Role != domain.RoleAgent {
		t.Fatalf("role after the stale approval = %q, want it unchanged", stored.Role)
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM role_changes WHERE user_id = ?`, wanda).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("role audits = %d, want 1", audits)
	}
}

// TestPendingUserStoreApprovalNeverGrantsRoot proves approval cannot create or
// promote a root (issue #419, S9): root is refused and nothing is written.
func TestPendingUserStoreApprovalNeverGrantsRoot(t *testing.T) {
	s, pending := newPendingUserTestStore(t)
	ctx := context.Background()
	actorID := seedApprovalActor(t, s)
	wanda := seedPendingUser(t, s, "Wanda", "wanda@example.com")

	_, err := pending.ApprovePending(ctx, []application.PendingApproval{{ID: wanda, Role: domain.RoleRoot}}, actorID, testClock)
	var protected *domain.RootProtectedError
	if !errors.As(err, &protected) {
		t.Fatalf("root approval = %v, want RootProtectedError", err)
	}
	assertPendingUnchanged(t, s, wanda)
}

// TestPendingUserStoreApprovalIsAllOrNothing proves a bulk approval either
// applies every entry or writes nothing: a stale, repeated, invalid, or empty
// batch leaves every account waiting and audits no role change (issue #419, S3).
func TestPendingUserStoreApprovalIsAllOrNothing(t *testing.T) {
	cases := []struct {
		name  string
		build func(first, second, settled int64) []application.PendingApproval
		check func(t *testing.T, err error)
	}{
		{
			name: "unknown id",
			build: func(first, _, _ int64) []application.PendingApproval {
				return []application.PendingApproval{{ID: first, Role: domain.RoleAgent}, {ID: 9999, Role: domain.RoleAgent}}
			},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("error = %v, want ErrNotFound", err)
				}
			},
		},
		{
			name: "already approved",
			build: func(first, _, settled int64) []application.PendingApproval {
				return []application.PendingApproval{{ID: first, Role: domain.RoleAgent}, {ID: settled, Role: domain.RoleAgent}}
			},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("error = %v, want ErrNotFound", err)
				}
			},
		},
		{
			name: "repeated id",
			build: func(first, _, _ int64) []application.PendingApproval {
				return []application.PendingApproval{{ID: first, Role: domain.RoleAgent}, {ID: first, Role: domain.RoleAgent}}
			},
			check: func(t *testing.T, err error) {
				var validation *domain.ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("error = %v, want ValidationError", err)
				}
			},
		},
		{
			name: "invalid role",
			build: func(first, _, _ int64) []application.PendingApproval {
				return []application.PendingApproval{{ID: first, Role: domain.Role("superuser")}}
			},
			check: func(t *testing.T, err error) {
				var validation *domain.ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("error = %v, want ValidationError", err)
				}
			},
		},
		{
			name: "empty selection",
			build: func(_, _, _ int64) []application.PendingApproval {
				return nil
			},
			check: func(t *testing.T, err error) {
				var validation *domain.ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("error = %v, want ValidationError", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, pending := newPendingUserTestStore(t)
			ctx := context.Background()
			actorID := seedApprovalActor(t, s)
			first := seedPendingUser(t, s, "Wanda", "wanda@example.com")
			second := seedPendingUser(t, s, "Vic", "vic@example.com")
			settled := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Role: domain.RoleUser, Active: true, CreatedAt: testClock}
			if err := s.UserStore().Create(ctx, settled); err != nil {
				t.Fatal(err)
			}

			_, err := pending.ApprovePending(ctx, tc.build(first, second, settled.ID), actorID, testClock)
			if err == nil {
				t.Fatal("ApprovePending must refuse the batch")
			}
			tc.check(t, err)
			assertPendingUnchanged(t, s, first)
			assertPendingUnchanged(t, s, second)
			kept, err := s.UserStore().GetByID(ctx, settled.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !kept.Active || kept.Pending || kept.Role != domain.RoleUser {
				t.Fatalf("settled account = %+v, want it untouched", kept)
			}
			var audits int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM role_changes`).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			if audits != 0 {
				t.Fatalf("role audits = %d, want 0: a refused batch grants nothing", audits)
			}
		})
	}
}

// assertPendingUnchanged proves the named account still waits for approval.
func assertPendingUnchanged(t *testing.T, s *Store, id int64) {
	t.Helper()
	u, err := s.UserStore().GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("pending account %d disappeared: %v", id, err)
	}
	if !u.Pending || u.Active || u.Role != domain.RoleUser {
		t.Fatalf("pending account %d after a refused approval = %+v, want it unchanged", id, u)
	}
}

// TestUserCreateNeverStoresAPendingAccount proves S10's store-side fact: the
// ordinary creation path — the one a CSV import uses — stores no pending
// account, even if a caller sets the flag. A pending account comes only from the
// company sign-in store, which writes it with its provider identity.
func TestUserCreateNeverStoresAPendingAccount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	u := &domain.User{Name: "Imported", Email: "imported@example.com", PasswordHash: "h", Role: domain.RoleUser, Active: true, Pending: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}

	stored, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Pending {
		t.Fatalf("created account = %+v, want no pending flag", stored)
	}
	waiting, err := s.PendingUserStore().ListPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 0 {
		t.Fatalf("pending list = %+v, want nothing from ordinary creation", waiting)
	}
	managed, err := s.UserStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 || managed[0].ID != u.ID {
		t.Fatalf("managed list = %+v, want the created account", managed)
	}
}
