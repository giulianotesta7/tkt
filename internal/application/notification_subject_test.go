package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #419, S3/S12: the account a pending notice names is rendered from the
// stored facts, and the pending-management capability decides whether those
// facts travel at all. These tests read the application layer only — the read
// is a FACTS port, so the policy that gates it and the sentence it produces are
// the two things only this layer owns.

// fakeNotificationSubjectStore answers a fixed set of accounts and records the
// ids it was asked for, so a test can prove a reader without the capability
// never causes the read at all.
type fakeNotificationSubjectStore struct {
	subjects []application.NotificationAccountSubject
	err      error
	reads    [][]int64
}

func (s *fakeNotificationSubjectStore) ListNotificationSubjects(_ context.Context, userIDs []int64) ([]application.NotificationAccountSubject, error) {
	s.reads = append(s.reads, append([]int64(nil), userIDs...))
	if s.err != nil {
		return nil, s.err
	}
	wanted := make(map[int64]bool, len(userIDs))
	for _, id := range userIDs {
		wanted[id] = true
	}
	var out []application.NotificationAccountSubject
	for _, id := range userIDs {
		for _, subject := range s.subjects {
			if subject.UserID == id && wanted[id] {
				out = append(out, subject)
				wanted[id] = false
			}
		}
	}
	return out, nil
}

// pendingManager returns the actor that holds the pending-management capability
// and one that does not, so a test states the policy it is exercising.
func pendingManager(role domain.Role) domain.User {
	return domain.User{ID: 7, Name: "Actor", Email: "actor@tkt.test", Role: role, Active: true}
}

// subjectStoreWithOneAccount is the stored shape a company sign-in leaves: a
// waiting account holding two provider identities, stored in the reverse of
// their display order.
func subjectStoreWithOneAccount() *fakeNotificationSubjectStore {
	return &fakeNotificationSubjectStore{subjects: []application.NotificationAccountSubject{{
		UserID: 42, Name: "Ana Pérez", Email: "ana.perez@example.com", Pending: true,
		Providers: []application.CompanyProvider{application.ProviderMicrosoft, application.ProviderGoogle},
	}}}
}

// TestNotificationAccountsAnswersTheStoredFactsForAManager is the authorized
// half: the account's own name and address, its pending state, and its stored
// providers in display order — with an id that no longer exists reported as
// missing instead of dropped.
func TestNotificationAccountsAnswersTheStoredFactsForAManager(t *testing.T) {
	store := subjectStoreWithOneAccount()

	accounts, err := application.NotificationAccounts(context.Background(), pendingManager(domain.RoleAdmin), store, []int64{42, 99})
	if err != nil {
		t.Fatalf("NotificationAccounts: %v", err)
	}
	seen := accounts[42]
	if !seen.Visible || !seen.Found {
		t.Fatalf("account 42 visible=%v found=%v, want the stored account", seen.Visible, seen.Found)
	}
	if seen.Name != "Ana Pérez" || seen.Email != "ana.perez@example.com" || !seen.Pending {
		t.Errorf("account 42 = %+v, want the stored name, address and pending state", seen)
	}
	if len(seen.Providers) != 2 || seen.Providers[0] != application.ProviderGoogle || seen.Providers[1] != application.ProviderMicrosoft {
		t.Errorf("account 42 providers = %v, want Google then Microsoft (display order)", seen.Providers)
	}
	missing := accounts[99]
	if !missing.Visible || missing.Found {
		t.Errorf("account 99 visible=%v found=%v, want a visible row that reports the account is gone", missing.Visible, missing.Found)
	}
	// One read for the panel, with the repeats dropped.
	if len(store.reads) != 1 {
		t.Fatalf("subject reads = %v, want exactly one", store.reads)
	}
	if got := store.reads[0]; len(got) != 2 || got[0] != 42 || got[1] != 99 {
		t.Errorf("subject read ids = %v, want the deduped request [42 99]", got)
	}
}

// TestNotificationAccountsHidesTheFactsFromAReaderWithoutTheCapability is the
// confidentiality rule: the notice stays addressed to the reader, but the
// account's name, address and provider are NOT read, let alone rendered, once
// the reader no longer manages pending accounts.
func TestNotificationAccountsHidesTheFactsFromAReaderWithoutTheCapability(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		store := subjectStoreWithOneAccount()
		accounts, err := application.NotificationAccounts(context.Background(), pendingManager(role), store, []int64{42})
		if err != nil {
			t.Fatalf("%s NotificationAccounts: %v", role, err)
		}
		hidden := accounts[42]
		if hidden.Visible || hidden.Found {
			t.Errorf("%s reads the account facts: %+v", role, hidden)
		}
		if hidden.Name != "" || hidden.Email != "" || len(hidden.Providers) != 0 || hidden.Pending {
			t.Errorf("%s received account data: %+v", role, hidden)
		}
		if len(store.reads) != 0 {
			t.Errorf("%s caused a subject read: %v", role, store.reads)
		}
		title, body := application.NotificationAccountMessage(hidden)
		if title.Key != "notifications.account.anonymous.title" || body.Key != "notifications.account.restricted.body" {
			t.Errorf("%s fallback copy = %q / %q, want the restricted sentence", role, title.Key, body.Key)
		}
	}
}

// TestNotificationAccountsFailsClosedWithoutTheRead proves a composition that
// forgot the read renders the explicit fallback instead of publishing facts by
// accident.
func TestNotificationAccountsFailsClosedWithoutTheRead(t *testing.T) {
	accounts, err := application.NotificationAccounts(context.Background(), pendingManager(domain.RoleRoot), nil, []int64{42})
	if err != nil {
		t.Fatalf("NotificationAccounts without a store: %v", err)
	}
	if got := accounts[42]; got.Visible || got.Name != "" || got.Email != "" {
		t.Errorf("a missing read published account data: %+v", got)
	}
}

// TestNotificationAccountsReadsNothingWithNoSubjects keeps the panel's cost at
// zero when it holds no account-kind row.
func TestNotificationAccountsReadsNothingWithNoSubjects(t *testing.T) {
	store := subjectStoreWithOneAccount()
	accounts, err := application.NotificationAccounts(context.Background(), pendingManager(domain.RoleAdmin), store, nil)
	if err != nil {
		t.Fatalf("NotificationAccounts: %v", err)
	}
	if len(accounts) != 0 || len(store.reads) != 0 {
		t.Errorf("accounts = %v, reads = %v, want nothing read and nothing answered", accounts, store.reads)
	}
}

// TestNotificationAccountsPropagatesAReadFailure keeps an unproven read from
// rendering as if the account were missing: a store failure is an error, not a
// fallback sentence.
func TestNotificationAccountsPropagatesAReadFailure(t *testing.T) {
	failure := errors.New("subject read failed")
	store := &fakeNotificationSubjectStore{err: failure}
	if _, err := application.NotificationAccounts(context.Background(), pendingManager(domain.RoleAdmin), store, []int64{42}); !errors.Is(err, failure) {
		t.Fatalf("NotificationAccounts error = %v, want the store failure", err)
	}
}

// TestNotificationAccountMessageSpeaksTheStoredState covers the four statements
// the sentence can make, and the provider phrase it builds from the identities
// alone — never a guessed method.
func TestNotificationAccountMessageSpeaksTheStoredState(t *testing.T) {
	pending := application.NotificationAccount{
		UserID: 42, Visible: true, Found: true, Pending: true,
		Name: "Ana Pérez", Email: "ana.perez@example.com", Providers: []application.CompanyProvider{application.ProviderGoogle},
	}
	title, body := application.NotificationAccountMessage(pending)
	if title.Key != "notifications.account.pending.title" || len(title.Args) != 1 || title.Args[0].Value != "Ana Pérez" {
		t.Errorf("pending title = %+v, want the account named", title)
	}
	if body.Key != "notifications.account.pending.body" || len(body.Args) != 3 {
		t.Fatalf("pending body = %+v, want name, address and the provider phrase", body)
	}
	if body.Args[0].Value != "Ana Pérez" || body.Args[1].Value != "ana.perez@example.com" {
		t.Errorf("pending body args = %+v, want the stored name and address", body.Args)
	}
	if method := body.Args[2].Msg; method == nil || method.Key != "notifications.account.method" ||
		len(method.Args) != 1 || method.Args[0].Key != "label.company_provider.google" {
		t.Errorf("pending provider phrase = %+v, want the stored Google identity", body.Args[2].Msg)
	}

	settled := pending
	settled.Pending = false
	title, body = application.NotificationAccountMessage(settled)
	if title.Key != "notifications.account.resolved.title" || body.Key != "notifications.account.resolved.body" {
		t.Errorf("settled copy = %q / %q, want the past-tense sentence", title.Key, body.Key)
	}
	if strings.Contains(body.Key, "pending.body") {
		t.Errorf("a settled notice still claims to be waiting: %q", body.Key)
	}

	gone := application.NotificationAccount{UserID: 42, Visible: true, Found: false}
	if _, body = application.NotificationAccountMessage(gone); body.Key != "notifications.account.unavailable.body" {
		t.Errorf("missing-account copy = %q, want the explicit unavailable sentence", body.Key)
	}
	hidden := application.NotificationAccount{UserID: 42}
	if _, body = application.NotificationAccountMessage(hidden); body.Key != "notifications.account.restricted.body" {
		t.Errorf("restricted copy = %q, want the restricted sentence", body.Key)
	}
}

// TestNotificationAccountMessageNeverGuessesTheProvider covers the provider
// phrase's closed set: the pair, the single method, the explicit unknown
// provider, and the account that recorded none.
func TestNotificationAccountMessageNeverGuessesTheProvider(t *testing.T) {
	account := application.NotificationAccount{UserID: 42, Visible: true, Found: true, Pending: true, Name: "Ana", Email: "ana@example.com"}
	methodOf := func(a application.NotificationAccount) *application.Msg {
		_, body := application.NotificationAccountMessage(a)
		if len(body.Args) != 3 {
			t.Fatalf("body args = %+v, want name, address and the provider phrase", body.Args)
		}
		return body.Args[2].Msg
	}
	if method := methodOf(account); method == nil || method.Key != "notifications.account.method_unknown" {
		t.Errorf("no stored identity = %+v, want the explicit unknown method", method)
	}
	account.Providers = []application.CompanyProvider{"github"}
	if method := methodOf(account); method == nil || method.Key != "notifications.account.method" ||
		len(method.Args) != 1 || method.Args[0].Key != "label.company_provider.unknown" {
		t.Errorf("unnamed provider = %+v, want the explicit unknown label", method)
	}
	account.Providers = []application.CompanyProvider{application.ProviderGoogle, application.ProviderMicrosoft}
	if method := methodOf(account); method == nil || method.Key != "notifications.account.method_pair" ||
		len(method.Args) != 2 ||
		method.Args[0].Key != "label.company_provider.google" || method.Args[1].Key != "label.company_provider.microsoft" {
		t.Errorf("two providers = %+v, want both stored labels", method)
	}
}
