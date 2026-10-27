package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// A first company sign-in that creates a pending account announces it (issue
// #419, S3/S9/S11). The application picks the recipients — active administrators
// and root, nobody else — and the store writes those notices in the account's own
// transaction. These tests run the real service over the real driver, so they
// pin what the in-memory fakes cannot: the notices commit with the account and
// its identity, a rejected notice rolls all three back, the announcement carries
// no ticket and owes no email, and a replay announces nothing new.

// pendingCompanyConfig is the stored configuration of these tests: Google on
// inside one domain, with every new account pending (S3).
type pendingCompanyConfig struct{}

func (pendingCompanyConfig) Get(context.Context) (application.CompanySignInSettings, error) {
	return application.CompanySignInSettings{
		Address: "https://tkt.example.com", NewAccounts: application.NewAccountsPending,
		Google: application.GoogleSignIn{Enabled: true, ClientID: "client", SealedSecret: "secret", Domains: []string{"example.com"}},
	}, nil
}

func (pendingCompanyConfig) ClientSecret(context.Context, application.CompanyProvider) (string, error) {
	return "secret", nil
}

// seedCompanyAccount seeds one account with an explicit role and activation
// (test arrange), so a test can build the recipient roster it needs.
func seedCompanyAccount(t *testing.T, s *Store, name, email string, role domain.Role, active bool) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO users (name, email, password_hash, role, active, pending, created_at) VALUES (?, ?, '', ?, ?, 0, ?)`,
		name, email, string(role), active, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed %s account %s: %v", role, email, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed %s account %s: last insert id: %v", role, email, err)
	}
	return id
}

// companySignIn runs one whole authorization: start it and answer the callback
// with the provider's claims.
func companySignIn(t *testing.T, svc *application.CompanySignInService) application.CompanySignInOutcome {
	t.Helper()
	ctx := context.Background()
	start, err := svc.Begin(ctx, application.ProviderGoogle)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	out, err := svc.Complete(ctx, application.CompanySignInCallback{
		Provider: application.ProviderGoogle, CookieState: start.State, QueryState: start.State, Code: "auth-code",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return out
}

// countTableRows answers how many rows the given table holds.
func countTableRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPendingCompanyCreationStoresOneNoticePerActiveManager proves the whole
// stored outcome of a first pending sign-in: the account waits, gets no session,
// keeps its provider identity, and is announced once to every active
// administrator and root — and to nobody else. The notice carries the account as
// its subject, no ticket, no actor, and the injected instant, and it owes no
// email delivery row.
func TestPendingCompanyCreationStoresOneNoticePerActiveManager(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	root := seedCompanyAccount(t, s, "Rita", "rita@example.com", domain.RoleRoot, true)
	admin := seedCompanyAccount(t, s, "Ada", "ada@example.com", domain.RoleAdmin, true)
	seedCompanyAccount(t, s, "Abe", "abe@example.com", domain.RoleAgent, true)
	seedCompanyAccount(t, s, "Uma", "uma@example.com", domain.RoleUser, true)
	seedCompanyAccount(t, s, "Iris", "iris@example.com", domain.RoleAdmin, false)
	seedPendingUser(t, s, "Pia", "pia@example.com")

	svc := application.NewCompanySignInService(pendingCompanyConfig{}, newCompanyIdentityStore(s.db),
		&repeatSignInProvider{email: "ana@example.com"}, newSessionStore(s.db), nil, repeatSignInClock{})

	out := companySignIn(t, svc)
	if out.Kind != application.CompanySignInPending || out.User == nil {
		t.Fatalf("outcome = %q/%q, want the pending account", out.Kind, out.Reason)
	}
	if out.Session != nil {
		t.Errorf("pending outcome carried session %q, want none (S3)", out.Session.ID)
	}
	if !out.User.Pending || out.User.Active || out.User.Role != domain.RoleUser {
		t.Errorf("created account active/pending/role = %v/%v/%q, want false/true/user",
			out.User.Active, out.User.Pending, out.User.Role)
	}
	if sessions := countTableRows(t, s, "sessions"); sessions != 0 {
		t.Errorf("sessions = %d, want 0 for a pending account", sessions)
	}
	if identities := countTableRows(t, s, "user_identities"); identities != 1 {
		t.Errorf("identities = %d, want the one linked to the created account", identities)
	}

	// One notice per active administrator and root, in the order the roster is
	// read (by id), and nothing for the agent, the requester, the deactivated
	// administrator, or the account that waits.
	recipients := map[int64]domain.Notification{}
	for _, user := range []int64{root, admin} {
		rows, err := s.NotificationStore().ListForUser(ctx, user, 10)
		if err != nil {
			t.Fatalf("ListForUser(%d): %v", user, err)
		}
		if len(rows) != 1 {
			t.Fatalf("notifications of %d = %d, want 1", user, len(rows))
		}
		recipients[user] = rows[0]
	}
	if total := countTableRows(t, s, "notifications"); total != 2 {
		t.Errorf("stored notifications = %d, want exactly the two announcements", total)
	}
	for user, n := range recipients {
		if n.UserID != user {
			t.Errorf("notice recipient = %d, want %d", n.UserID, user)
		}
		if n.Kind != domain.NotificationCompanyAccountPending {
			t.Errorf("notice kind = %q, want %q", n.Kind, domain.NotificationCompanyAccountPending)
		}
		if n.TicketID != 0 {
			t.Errorf("notice ticket = %d, want 0 (an account announcement names no ticket)", n.TicketID)
		}
		if n.PendingUserID == nil || *n.PendingUserID != out.User.ID {
			t.Errorf("notice subject = %v, want the created account %d", n.PendingUserID, out.User.ID)
		}
		if n.ActorUserID != nil || n.FromValue != nil || n.ToValue != nil || n.CommentID != nil || n.StepIndex != nil {
			t.Errorf("notice carries an actor or ticket facts: %+v", n)
		}
		if !n.CreatedAt.Equal(testClock) {
			t.Errorf("notice created at = %v, want the injected instant %v", n.CreatedAt, testClock)
		}
		if n.ReadAt != nil {
			t.Errorf("fresh notice read_at = %v, want nil", *n.ReadAt)
		}
	}

	// The stored row names the account and stores no ticket at all, and the
	// announcement never reaches the email sender.
	var (
		ticketID  *int64
		pendingID *int64
	)
	if err := s.db.QueryRowContext(ctx, `SELECT ticket_id, pending_user_id FROM notifications WHERE user_id = ?`, admin).
		Scan(&ticketID, &pendingID); err != nil {
		t.Fatalf("read the stored notice: %v", err)
	}
	if ticketID != nil {
		t.Errorf("stored ticket_id = %d, want NULL", *ticketID)
	}
	if pendingID == nil || *pendingID != out.User.ID {
		t.Errorf("stored pending_user_id = %v, want %d", pendingID, out.User.ID)
	}
	if deliveries := countTableRows(t, s, "notification_emails"); deliveries != 0 {
		t.Errorf("notification email rows = %d, want 0 (no ticket copy exists for an account)", deliveries)
	}

	// A replay of the same sign-in announces nothing new: the account already
	// exists, so the flow never reaches the creation that raises the batch.
	accounts := countTableRows(t, s, "users")
	if again := companySignIn(t, svc); again.Kind != application.CompanySignInPending {
		t.Fatalf("replay = %q/%q, want pending", again.Kind, again.Reason)
	}
	if total := countTableRows(t, s, "notifications"); total != 2 {
		t.Errorf("notifications after the replay = %d, want the 2 from the creation", total)
	}
	if users := countTableRows(t, s, "users"); users != accounts {
		t.Errorf("accounts after the replay = %d, want %d", users, accounts)
	}
	if sessions := countTableRows(t, s, "sessions"); sessions != 0 {
		t.Errorf("sessions after the replay = %d, want 0", sessions)
	}
}

// TestPendingCompanyCreationWithoutAnActiveManagerStoresNoNotice proves a
// missing recipient is not a missing account: with nobody able to approve, the
// account and its identity are stored, the batch is empty, and the flow answers
// the pending screen.
func TestPendingCompanyCreationWithoutAnActiveManagerStoresNoNotice(t *testing.T) {
	s := newTestDB(t)
	seedCompanyAccount(t, s, "Abe", "abe@example.com", domain.RoleAgent, true)
	svc := application.NewCompanySignInService(pendingCompanyConfig{}, newCompanyIdentityStore(s.db),
		&repeatSignInProvider{email: "ana@example.com"}, newSessionStore(s.db), nil, repeatSignInClock{})

	out := companySignIn(t, svc)
	if out.Kind != application.CompanySignInPending || out.User == nil {
		t.Fatalf("outcome = %q/%q, want the pending account", out.Kind, out.Reason)
	}
	if notifications := countTableRows(t, s, "notifications"); notifications != 0 {
		t.Errorf("notifications = %d, want none (an agent cannot approve)", notifications)
	}
	if identities := countTableRows(t, s, "user_identities"); identities != 1 {
		t.Errorf("identities = %d, want 1", identities)
	}
}

// TestRequesterCompanyCreationStoresNoNotice is the preserved behavior of the
// other policy: with new accounts created as requesters and an active
// administrator sitting in the roster, the first sign-in creates and signs the
// account in, stores the identity and the session, and announces nothing.
func TestRequesterCompanyCreationStoresNoNotice(t *testing.T) {
	s := newTestDB(t)
	seedCompanyAccount(t, s, "Ada", "ada@example.com", domain.RoleAdmin, true)
	svc := application.NewCompanySignInService(repeatSignInConfig{}, newCompanyIdentityStore(s.db),
		&repeatSignInProvider{email: "ana@example.com"}, newSessionStore(s.db), nil, repeatSignInClock{})

	out := companySignIn(t, svc)
	if out.Kind != application.CompanySignInSignedIn || out.Session == nil {
		t.Fatalf("outcome = %q/%q, want signed_in with a session", out.Kind, out.Reason)
	}
	if notifications := countTableRows(t, s, "notifications"); notifications != 0 {
		t.Errorf("notifications = %d, want none (a requester account waits for nobody)", notifications)
	}
	if sessions := countTableRows(t, s, "sessions"); sessions != 1 {
		t.Errorf("sessions = %d, want the one issued", sessions)
	}
}

// TestPendingCompanyCreationRollsBackAccountIdentityAndNotices proves the three
// writes are ONE transaction: a notice the append rejects leaves no account, no
// identity, and no notice behind, and consumes no account id.
func TestPendingCompanyCreationRollsBackAccountIdentityAndNotices(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	admin := seedCompanyAccount(t, s, "Ada", "ada@example.com", domain.RoleAdmin, true)
	store := newCompanyIdentityStore(s.db)

	create := func(notices []domain.Notification) error {
		user := domain.User{Name: "Ana", Email: "ana@example.com", Role: domain.RoleUser, Pending: true, CreatedAt: testClock}
		identity := &application.CompanyIdentity{
			Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
			Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
		}
		return store.CreateUserWithIdentity(ctx, &user, identity, notices)
	}

	cases := []struct {
		name    string
		notices []domain.Notification
	}{
		{
			name: "a ticket kind is not an account announcement",
			notices: []domain.Notification{{
				UserID: admin, TicketID: 7, Kind: domain.NotificationAssigned, CreatedAt: testClock,
			}},
		},
		{
			name:    "an announcement without its instant",
			notices: []domain.Notification{{UserID: admin, Kind: domain.NotificationCompanyAccountPending}},
		},
		{
			name:    "an announcement without a recipient",
			notices: []domain.Notification{{Kind: domain.NotificationCompanyAccountPending, CreatedAt: testClock}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := create(tc.notices)
			var invariant *domain.NotificationInvariantError
			if !errors.As(err, &invariant) {
				t.Fatalf("CreateUserWithIdentity = %v, want a *domain.NotificationInvariantError", err)
			}
			// Nothing of the rejected creation survived: no account, no identity,
			// no notice, no delivery row, and no session.
			if users := countTableRows(t, s, "users"); users != 1 {
				t.Errorf("accounts = %d, want only the seeded administrator", users)
			}
			for _, table := range []string{"user_identities", "notifications", "notification_emails", "sessions"} {
				if rows := countTableRows(t, s, table); rows != 0 {
					t.Errorf("%s = %d, want 0 after the rollback", table, rows)
				}
			}
		})
	}

	// The id the rolled-back insert would have taken is free again: the next
	// creation follows the seeded administrator directly.
	user := domain.User{Name: "Ana", Email: "ana@example.com", Role: domain.RoleUser, Pending: true, CreatedAt: testClock}
	identity := &application.CompanyIdentity{
		Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}
	subject := admin
	if err := store.CreateUserWithIdentity(ctx, &user, identity, []domain.Notification{{
		UserID: admin, Kind: domain.NotificationCompanyAccountPending, CreatedAt: testClock.Add(time.Minute),
	}}); err != nil {
		t.Fatalf("CreateUserWithIdentity after the rollbacks: %v", err)
	}
	if user.ID != subject+1 {
		t.Errorf("created account id = %d, want %d (a rolled-back insert consumes no id)", user.ID, subject+1)
	}
	if notifications := countTableRows(t, s, "notifications"); notifications != 1 {
		t.Errorf("notifications = %d, want the one stored with the account", notifications)
	}
}
