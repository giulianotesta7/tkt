package application_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// In-memory port fakes (slice 2 runtime harness: "port fakes only; bcrypt
// exercised in unit tests" — tasks.md Unit 2). They mirror the port contracts
// documented in ports.go: MAX+1 numbering, ASC timelines, filter composition
// with AND, NotFound/Duplicate/Referenced errors.

func ptr[T any](v T) *T { return &v }

const timeMinute = time.Minute

// fakeClock is the injected time source (D7).
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func fixedClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)}
}

func matchesQuery(t *domain.Ticket, q application.TicketQuery) bool {
	// Actor scope first (ticket-access spec): the zero value (ScopeNone)
	// fails closed — an unscoped query matches nothing, mirroring the real
	// store's `0 = 1` clause.
	switch q.Scope {
	case application.ScopeOwned:
		if t.RequesterUserID == nil || *t.RequesterUserID != q.ActorID {
			return false
		}
	case application.ScopeAssigned:
		if t.UserID == nil || *t.UserID != q.ActorID {
			return false
		}
	case application.ScopeAssignedOrClaimable:
		// The in-memory fake carries no workflow runs / desk memberships, so the
		// claimable half of the read scope adds nothing here: it reduces to assigned
		// (design S6). The real SQLite store implements the claim EXCEPTION via its
		// own scope clause; policy-level read-scoping is tested against policy.go.
		if t.UserID == nil || *t.UserID != q.ActorID {
			return false
		}
	case application.ScopeAssignable:
		if t.UserID != nil && *t.UserID != q.ActorID {
			return false
		}
	case application.ScopeAll:
		// full queue: no restriction
	default:
		return false
	}
	if q.State != nil && t.State != *q.State {
		return false
	}
	if len(q.States) > 0 {
		matched := false
		for _, state := range q.States {
			if t.State == state {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if q.OnHold && t.OnHoldSince == nil {
		return false
	}
	if q.NotOnHold && t.OnHoldSince != nil {
		return false
	}
	// The requester's Needs-your-action predicate (issue #411, S8): active work
	// held for the requester's reply OR a resolved ticket awaiting their
	// confirmation. It mirrors the store's parenthesized OR.
	if q.RequesterNeedsAction {
		held := (t.State == domain.StateNew || t.State == domain.StateInProgress) && t.OnHoldSince != nil
		if !held && t.State != domain.StateResolved {
			return false
		}
	}
	if q.Priority != nil && t.Priority != *q.Priority {
		return false
	}
	if q.CategoryID != nil && t.CategoryID != *q.CategoryID {
		return false
	}
	if q.UserID != nil && (t.UserID == nil || *t.UserID != *q.UserID) {
		return false
	}
	if q.Text != "" || len(q.Numbers) > 0 {
		titleHit := q.Text == "" || matchesTitle(t, q.Text)
		numberHit := false
		for _, n := range q.Numbers {
			if int64(t.Number) == n {
				numberHit = true
				break
			}
		}
		if !titleHit && !numberHit {
			return false
		}
	}
	return true
}

// matchesTitle approximates the title-scoped FTS expression for the fake:
// every `title : "tok"` phrase must appear (case-insensitive) in the title.
func matchesTitle(t *domain.Ticket, expr string) bool {
	hay := strings.ToLower(t.Title)
	for _, phrase := range strings.Split(expr, " AND ") {
		tok := strings.Trim(strings.TrimPrefix(phrase, "title : "), `"`)
		if !strings.Contains(hay, strings.ToLower(tok)) {
			return false
		}
	}
	return true
}

// fakeTicketStore implements TicketStore with sequential MAX+1 numbering and
// created_at DESC, id DESC ordering (D2).
type fakeTicketStore struct {
	tickets      map[int64]*domain.Ticket
	nextID       int64
	getByIDCalls []int64
}

func newFakeTicketStore() *fakeTicketStore {
	return &fakeTicketStore{tickets: map[int64]*domain.Ticket{}, nextID: 1}
}

// seed inserts a ticket directly (test arrange), assigning ID and, when
// Number is 0, the next MAX+1 number.
func (f *fakeTicketStore) seed(t domain.Ticket) domain.Ticket {
	t.ID = f.nextID
	f.nextID++
	if t.Number == 0 {
		t.Number = f.maxNumber() + 1
	}
	f.store(&t)
	return t
}

func (f *fakeTicketStore) maxNumber() int {
	max := 0
	for _, x := range f.tickets {
		if x.Number > max {
			max = x.Number
		}
	}
	return max
}

func (f *fakeTicketStore) store(t *domain.Ticket) {
	cp := *t
	f.tickets[t.ID] = &cp
}

func (f *fakeTicketStore) Create(_ context.Context, t *domain.Ticket) error {
	t.ID = f.nextID
	f.nextID++
	t.Number = f.maxNumber() + 1
	f.store(t)
	return nil
}

func (f *fakeTicketStore) Update(_ context.Context, t *domain.Ticket) error {
	if _, ok := f.tickets[t.ID]; !ok {
		return &domain.NotFoundError{Kind: "ticket", ID: t.ID}
	}
	f.store(t)
	return nil
}

func (f *fakeTicketStore) GetByID(_ context.Context, id int64, q application.TicketQuery) (*domain.Ticket, error) {
	f.getByIDCalls = append(f.getByIDCalls, id)
	t, ok := f.tickets[id]
	if !ok || !matchesQuery(t, q) {
		// Out-of-scope tickets are indistinguishable from missing ones
		// (no existence leak, ticket-access spec).
		return nil, &domain.NotFoundError{Kind: "ticket", ID: id}
	}
	cp := *t
	return &cp, nil
}

func (f *fakeTicketStore) allMatching(q application.TicketQuery) []*domain.Ticket {
	var out []*domain.Ticket
	for _, t := range f.tickets {
		if matchesQuery(t, q) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func (f *fakeTicketStore) List(_ context.Context, q application.TicketQuery, p application.Page) ([]domain.Ticket, error) {
	all := f.allMatching(q)
	if p.Offset > len(all) {
		p.Offset = len(all)
	}
	end := p.Offset + p.Limit
	if end > len(all) {
		end = len(all)
	}
	var out []domain.Ticket
	for _, t := range all[p.Offset:end] {
		out = append(out, *t)
	}
	return out, nil
}

func (f *fakeTicketStore) Count(_ context.Context, q application.TicketQuery) (int, error) {
	return len(f.allMatching(q)), nil
}

func (f *fakeTicketStore) CountsByState(_ context.Context, q application.TicketQuery) (map[domain.State]int, error) {
	m := map[domain.State]int{}
	for _, t := range f.allMatching(q) {
		m[t.State]++
	}
	return m, nil
}

func (f *fakeTicketStore) CountsByPriority(_ context.Context, q application.TicketQuery) (map[domain.Priority]int, error) {
	m := map[domain.Priority]int{}
	for _, t := range f.allMatching(q) {
		m[t.Priority]++
	}
	return m, nil
}

// fakeSearchStore delegates to the ticket fake: the shared matchesQuery
// already AND-composes filters with text.
type fakeSearchStore struct{ tickets *fakeTicketStore }

func (f *fakeSearchStore) Search(ctx context.Context, q application.TicketQuery, p application.Page) ([]domain.Ticket, error) {
	return f.tickets.List(ctx, q, p)
}

func (f *fakeSearchStore) SearchCount(ctx context.Context, q application.TicketQuery) (int, error) {
	return f.tickets.Count(ctx, q)
}

// fakeCommentStore implements CommentStore with insertion-order timelines.
// ListByTicket mirrors the real store's visibility contract: when
// includeInternal is false, internal (staff-only) comments are excluded
// before the collection is returned (comment-visibility spec).
type fakeCommentStore struct {
	comments map[int64][]*domain.Comment
	nextID   int64
	addCalls []domain.Comment
}

func newFakeCommentStore() *fakeCommentStore {
	return &fakeCommentStore{comments: map[int64][]*domain.Comment{}, nextID: 1}
}

func (f *fakeCommentStore) Add(_ context.Context, c *domain.Comment) error {
	f.addCalls = append(f.addCalls, *c)
	c.ID = f.nextID
	f.nextID++
	cp := *c
	f.comments[c.TicketID] = append(f.comments[c.TicketID], &cp)
	return nil
}

func (f *fakeCommentStore) ListByTicket(_ context.Context, ticketID int64, includeInternal bool) ([]domain.Comment, error) {
	var out []domain.Comment
	for _, c := range f.comments[ticketID] {
		if !includeInternal && c.Visibility == domain.CommentInternal {
			continue
		}
		out = append(out, *c)
	}
	return out, nil
}

// fakeCommentUnitOfWork implements CommentUnitOfWork with the same atomic
// simulation as fakeUnitOfWork (issue #344 S12): the comment insert and its
// notifications commit together, and the assigned comment id is stamped onto
// every notification exactly as the SQLite adapter does. It records the rows
// the APPLICATION stamped, which is the only place the comment recipient
// policy is observable at this layer.
type fakeCommentUnitOfWork struct {
	comments      *fakeCommentStore
	notifications []domain.Notification
	// holds, when attached, applies a CommentMutation.Resume through the ticket
	// unit-of-work fake (issue #352). Simplification: the fake has no shared
	// transaction, so a resume is applied before the comment insert and never
	// rolled back by a later failure; the SQLite adapter owns that proof.
	holds *fakeUnitOfWork
	// attachments records the comment files the APPLICATION validated and
	// stamped (issue #366); the SQLite adapter owns the insert proof.
	attachments []domain.Attachment
}

func newFakeCommentUnitOfWork(comments *fakeCommentStore) *fakeCommentUnitOfWork {
	return &fakeCommentUnitOfWork{comments: comments}
}

// Add inserts the comment (store-assigned id) and stamps that id onto every
// notification, then records the batch.
func (f *fakeCommentUnitOfWork) Add(ctx context.Context, m application.CommentMutation) error {
	if m.Resume != nil && f.holds != nil {
		if err := f.holds.applyResume(ctx, *m.Resume); err != nil {
			return err
		}
	}
	if err := f.comments.Add(ctx, m.Comment); err != nil {
		return err
	}
	for i := range m.Notifications {
		id := m.Comment.ID
		m.Notifications[i].CommentID = &id
	}
	f.notifications = append(f.notifications, m.Notifications...)
	f.attachments = append(f.attachments, m.Attachments...)
	return nil
}

// notified returns the recorded notifications for one recipient, in order.
func (f *fakeCommentUnitOfWork) notified(userID int64) []domain.Notification {
	var out []domain.Notification
	for _, n := range f.notifications {
		if n.UserID == userID {
			out = append(out, n)
		}
	}
	return out
}

// fakeAuditStore implements AuditStore with append-order timelines.
type fakeAuditStore struct {
	events map[int64][]domain.AuditEvent
}

func newFakeAuditStore() *fakeAuditStore {
	return &fakeAuditStore{events: map[int64][]domain.AuditEvent{}}
}

func (f *fakeAuditStore) Append(_ context.Context, events ...domain.AuditEvent) error {
	for _, e := range events {
		f.events[e.TicketID] = append(f.events[e.TicketID], e)
	}
	return nil
}

func (f *fakeAuditStore) ListByTicket(_ context.Context, ticketID int64) ([]domain.AuditEvent, error) {
	out := make([]domain.AuditEvent, len(f.events[ticketID]))
	copy(out, f.events[ticketID])
	return out, nil
}

// errAuditAppendFailed is the simulated store failure the unit-of-work fake
// returns when failAuditAppend is set (C1): the service must propagate it
// untouched, never swallow it.
var errAuditAppendFailed = errors.New("audit append failed")

// fakeUnitOfWork implements TicketUnitOfWork with a transactional
// simulation (C1/S11): the ticket write, the audit appends, and the
// notification appends either all persist or all roll back. failAuditAppend
// makes the audit part of the NEXT mutation fail, letting tests prove the
// rollback half of the atomicity contract (no-silent-mutations spec);
// failNotifications does the same for the notification half.
type fakeUnitOfWork struct {
	tickets         *fakeTicketStore
	audits          *fakeAuditStore
	failAuditAppend bool
	// notifications records every notification the APPLICATION stamped into a
	// mutation, in commit order — the only place the recipient policy is
	// observable at this layer.
	notifications []domain.Notification
	// createCalls counts Create invocations: it proves the workflow create path
	// never falls back to the legacy TicketUnitOfWork create (PR5 S5).
	createCalls int
	// updateHoldCalls counts UpdateHold invocations (issue #352): the hold use
	// cases must persist through the compare-and-set write, while every other
	// mutation must keep using Update and never touch the hold columns.
	updateHoldCalls int
	// remindHoldCalls counts RemindHold invocations (issue #353): the automatic
	// hold pass must send one reminder per hold through the guarded write, and
	// none at all when the feature is off, the reminder is not due yet, or it
	// was already sent for this hold.
	remindHoldCalls int
	// slas, when non-nil, receives the shifted resolution instants of an
	// UpdateHold. It is nil by default because no service test under this
	// port needs the SLA side yet, and a fake must not invent a store it was
	// not given: with nil, ShiftedSLA is not persisted (documented
	// simplification, issue #352).
	slas *fakeSLAStore
	// attachments records the creation files of the legacy Create (issue #366).
	attachments []domain.Attachment
}

func newFakeUnitOfWork(tickets *fakeTicketStore, audits *fakeAuditStore) *fakeUnitOfWork {
	return &fakeUnitOfWork{tickets: tickets, audits: audits}
}

// Create persists the ticket (store-assigned ID and number, D8) and its
// created event as one unit: a failing audit append rolls the ticket back.
func (f *fakeUnitOfWork) Create(ctx context.Context, m application.TicketMutation) error {
	f.createCalls++
	if err := f.tickets.Create(ctx, m.Ticket); err != nil {
		return err
	}
	if f.failAuditAppend {
		delete(f.tickets.tickets, m.Ticket.ID)
		return errAuditAppendFailed
	}
	for i := range m.Events {
		m.Events[i].TicketID = m.Ticket.ID
	}
	f.notifications = append(f.notifications, m.Notifications...)
	f.attachments = append(f.attachments, m.Attachments...)
	return f.audits.Append(ctx, m.Events...)
}

// Update persists the ticket and its event batch as one unit: a failing
// audit append restores the pre-mutation ticket copy. events may be empty
// (a plain ticket write is still atomic by construction).
func (f *fakeUnitOfWork) Update(ctx context.Context, m application.TicketMutation) error {
	prev, ok := f.tickets.tickets[m.Ticket.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "ticket", ID: m.Ticket.ID}
	}
	// The real store refuses a terminal write over a row that is still on
	// hold (issue #352): resolving or cancelling ends the hold, so it goes
	// through UpdateHold. Mirrored here so a service test cannot pass on a
	// write the SQLite unit of work would refuse.
	if domain.IsClosed(m.Ticket.State) && prev.OnHoldSince != nil {
		return &domain.HoldConflictError{TicketID: m.Ticket.ID}
	}
	if err := f.tickets.Update(ctx, m.Ticket); err != nil {
		return err
	}
	if f.failAuditAppend {
		f.tickets.tickets[m.Ticket.ID] = prev
		return errAuditAppendFailed
	}
	f.notifications = append(f.notifications, m.Notifications...)
	if len(m.Events) > 0 {
		return f.audits.Append(ctx, m.Events...)
	}
	return nil
}

// UpdateHold mirrors TicketUnitOfWork.UpdateHold (issue #352): the hold
// columns are written only against the stored hold the caller read
// (compare-and-set), so a stale read is a HoldConflictError with no writes,
// and the ticket write, the shifted SLA and the event batch either all land
// or all roll back on a failing audit append.
//
// Simplifications, documented per the fakes contract: the fake has no
// workflow-run store, so ending the hold on a terminal change does not close
// an active run (the sqlite layer proves that half); ShiftedSLA is persisted
// only when an SLA fake is attached through f.slas, and that fake stores no
// transaction of its own, so the rollback here restores it by hand; the
// "missing ticket" and "lost race" cases are told apart exactly as the real
// store does (NotFoundError vs HoldConflictError).
func (f *fakeUnitOfWork) UpdateHold(ctx context.Context, w application.HoldWrite) error {
	f.updateHoldCalls++
	prev, ok := f.tickets.tickets[w.Ticket.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "ticket", ID: w.Ticket.ID}
	}
	if !sameHoldInstant(prev.OnHoldSince, w.ExpectedOnHoldSince) {
		return &domain.HoldConflictError{TicketID: w.Ticket.ID}
	}
	var prevSLA *domain.TicketSLA
	if f.slas != nil {
		if stored, ok := f.slas.frozen[w.Ticket.ID]; ok {
			copyOf := *stored
			prevSLA = &copyOf
		}
	}
	f.tickets.store(w.Ticket)
	if f.slas != nil && w.ShiftedSLA != nil {
		shifted := *w.ShiftedSLA
		if f.slas.frozen == nil {
			f.slas.frozen = map[int64]*domain.TicketSLA{}
		}
		f.slas.frozen[w.Ticket.ID] = &shifted
	}
	if f.failAuditAppend {
		f.tickets.tickets[w.Ticket.ID] = prev
		if f.slas != nil {
			if prevSLA == nil {
				delete(f.slas.frozen, w.Ticket.ID)
			} else {
				f.slas.frozen[w.Ticket.ID] = prevSLA
			}
		}
		return errAuditAppendFailed
	}
	f.notifications = append(f.notifications, w.Notifications...)
	if len(w.Events) > 0 {
		return f.audits.Append(ctx, w.Events...)
	}
	return nil
}

// RemindHold mirrors TicketUnitOfWork.RemindHold (issue #353): the reminder
// batch is appended only while the stored hold is still the one the caller
// read, and nothing else is written — not even a ticket column. A stale read is
// a HoldConflictError with no rows, and a missing ticket a NotFoundError,
// exactly as the SQLite unit of work answers.
func (f *fakeUnitOfWork) RemindHold(_ context.Context, ticketID int64, expectedOnHoldSince time.Time, notifications []domain.Notification) error {
	f.remindHoldCalls++
	stored, ok := f.tickets.tickets[ticketID]
	if !ok {
		return &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	if !sameHoldInstant(stored.OnHoldSince, &expectedOnHoldSince) {
		return &domain.HoldConflictError{TicketID: ticketID}
	}
	f.notifications = append(f.notifications, notifications...)
	return nil
}

// applyResume applies a hold resume that rides a comment (issue #352): the
// same compare-and-set as UpdateHold, except that a stored hold that no
// longer matches skips the resume instead of failing — the comment still
// commits, exactly as the SQLite comment unit of work does.
func (f *fakeUnitOfWork) applyResume(ctx context.Context, r application.HoldResume) error {
	prev, ok := f.tickets.tickets[r.Ticket.ID]
	if !ok || !sameHoldInstant(prev.OnHoldSince, r.ExpectedOnHoldSince) {
		return nil
	}
	return f.UpdateHold(ctx, application.HoldWrite{Ticket: r.Ticket, ExpectedOnHoldSince: r.ExpectedOnHoldSince, ShiftedSLA: r.ShiftedSLA, Events: r.Events})
}

// sameHoldInstant reports whether two hold instants are the same hold state:
// both nil, or the same instant. It is the fake's compare-and-set predicate.
func sameHoldInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// fakeUserStore implements UserStore: email uniqueness, delete guard via a
// referenced flag (the real store checks ticket FKs; tests set the flag).
type fakeUserStore struct {
	users       map[int64]*domain.User
	byEmail     map[string]int64
	nextID      int64
	referenced  map[int64]bool
	roleChanges []fakeRoleChange
	downgrades  []fakeDowngrade
	// signIns holds one provider row per stored identity of a waiting account
	// (issue #419, S3/S12), in insertion order, so a test can prove the service
	// reports the stored facts and normalizes the display order itself.
	signIns map[int64][]application.CompanyProvider
	// signInReads counts the identity reads that reached the port, so a refused
	// actor can prove the capability gate refused before any read.
	signInReads int
}

// fakeDowngrade records one DowngradeToUser call (issue #47 handoff route).
type fakeDowngrade struct {
	userID       int64
	expectedRole domain.Role
	actorID      int64
}

type fakeRoleChange struct{ actorID int64 }

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{
		signIns:    map[int64][]application.CompanyProvider{},
		users:      map[int64]*domain.User{},
		byEmail:    map[string]int64{},
		nextID:     1,
		referenced: map[int64]bool{},
	}
}

// seed inserts a user directly (test arrange).
func (f *fakeUserStore) seed(name, email string, active bool) domain.User {
	u := domain.User{Name: name, Email: email, Active: active}
	u.ID = f.nextID
	f.nextID++
	f.users[u.ID] = &u
	f.byEmail[u.Email] = u.ID
	return u
}

// seedRole inserts a user with an explicit role directly (test arrange;
// assignment rules depend on the target's role, so arrange must control it).
func (f *fakeUserStore) seedRole(name, email string, role domain.Role, active bool) domain.User {
	u := domain.User{Name: name, Email: email, Role: role, Active: active}
	u.ID = f.nextID
	f.nextID++
	f.users[u.ID] = &u
	f.byEmail[u.Email] = u.ID
	return u
}

func (f *fakeUserStore) markReferenced(id int64) { f.referenced[id] = true }

// seedPending inserts a requester that waits for approval directly (test
// arrange): inactive, with the pending flag the company sign-in decision reads
// (issue #419, S3).
func (f *fakeUserStore) seedPending(name, email string) domain.User {
	u := f.seedRole(name, email, domain.RoleUser, false)
	f.users[u.ID].Pending = true
	return *f.users[u.ID]
}

// seedPendingSignIn attaches one stored provider identity to an account, the
// fact the Pending tab's Sign-in column reads (issue #419, S12).
func (f *fakeUserStore) seedPendingSignIn(userID int64, provider application.CompanyProvider) {
	f.signIns[userID] = append(f.signIns[userID], provider)
}

func (f *fakeUserStore) Create(_ context.Context, u *domain.User) error {
	if _, dup := f.byEmail[u.Email]; dup {
		return &domain.DuplicateError{Kind: "user", Name: u.Email}
	}
	u.ID = f.nextID
	f.nextID++
	cp := *u
	f.users[u.ID] = &cp
	f.byEmail[u.Email] = u.ID
	return nil
}

// BootstrapRoot mirrors the real store's atomic contract: unavailable once
// any user exists; otherwise the user is stored as an active root.
func (f *fakeUserStore) BootstrapRoot(_ context.Context, u *domain.User) error {
	if len(f.users) > 0 {
		return domain.NewBootstrapUnavailableError()
	}
	u.Role = domain.RoleRoot
	return f.Create(context.Background(), u)
}

// RecoverRoot mirrors the real store's fail-closed contract: refused when a
// root exists or the user is unknown; otherwise activate + promote + return.
func (f *fakeUserStore) RecoverRoot(_ context.Context, id int64) (*domain.User, error) {
	for _, u := range f.users {
		if u.Role == domain.RoleRoot {
			return nil, errors.New("a root already exists; recovery refused")
		}
	}
	u, ok := f.users[id]
	if !ok {
		return nil, &domain.NotFoundError{Kind: "user", ID: id}
	}
	u.Role = domain.RoleRoot
	u.Active = true
	cp := *u
	return &cp, nil
}

func (f *fakeUserStore) Update(_ context.Context, u *domain.User) error {
	existing, ok := f.users[u.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "user", ID: u.ID}
	}
	if otherID, dup := f.byEmail[u.Email]; dup && otherID != u.ID {
		return &domain.DuplicateError{Kind: "user", Name: u.Email}
	}
	delete(f.byEmail, existing.Email)
	cp := *u
	f.users[u.ID] = &cp
	f.byEmail[u.Email] = u.ID
	return nil
}

func (f *fakeUserStore) UpdateManagedUser(_ context.Context, updated *domain.User, expectedRole domain.Role, actorID int64, _ time.Time) error {
	return f.applyManagedUpdate(updated, expectedRole, actorID)
}

// DowngradeToUser records the atomic downgrade+handoff call and applies the
// same managed-update mutation so service-level state assertions hold.
func (f *fakeUserStore) DowngradeToUser(_ context.Context, updated *domain.User, expectedRole domain.Role, actorID int64, _ time.Time) (*domain.User, error) {
	f.downgrades = append(f.downgrades, fakeDowngrade{userID: updated.ID, expectedRole: expectedRole, actorID: actorID})
	if err := f.applyManagedUpdate(updated, expectedRole, actorID); err != nil {
		return nil, err
	}
	cp := *updated
	return &cp, nil
}

func (f *fakeUserStore) applyManagedUpdate(updated *domain.User, expectedRole domain.Role, actorID int64) error {
	u, ok := f.users[updated.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "user", ID: updated.ID}
	}
	if u.Role != expectedRole {
		return &domain.NotFoundError{Kind: "user", ID: updated.ID}
	}
	if u.Pending {
		// Mirrors the real store's guarded WHERE (issue #419, S3): an account that
		// waits for approval is not edited or reactivated here.
		return &domain.NotFoundError{Kind: "user", ID: updated.ID}
	}
	if otherID, dup := f.byEmail[updated.Email]; dup && otherID != updated.ID {
		return &domain.DuplicateError{Kind: "user", Name: updated.Email}
	}
	changedRole := u.Role != updated.Role
	delete(f.byEmail, u.Email)
	cp := *updated
	f.users[updated.ID] = &cp
	f.byEmail[updated.Email] = updated.ID
	if changedRole {
		f.roleChanges = append(f.roleChanges, fakeRoleChange{actorID: actorID})
	}
	return nil
}

func (f *fakeUserStore) UpdatePasswordHash(_ context.Context, id int64, passwordHash string) error {
	u, ok := f.users[id]
	if !ok {
		return &domain.NotFoundError{Kind: "user", ID: id}
	}
	u.PasswordHash = passwordHash
	return nil
}

func (f *fakeUserStore) Delete(_ context.Context, id int64) error {
	if f.referenced[id] {
		return &domain.ReferencedError{Kind: "user", ID: id}
	}
	if _, ok := f.users[id]; !ok {
		return &domain.NotFoundError{Kind: "user", ID: id}
	}
	delete(f.byEmail, f.users[id].Email)
	delete(f.users, id)
	return nil
}

func (f *fakeUserStore) GetByID(_ context.Context, id int64) (*domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, &domain.NotFoundError{Kind: "user", ID: id}
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUserStore) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	id, ok := f.byEmail[email]
	if !ok {
		return nil, &domain.NotFoundError{Kind: "user", ID: email}
	}
	return f.GetByID(context.Background(), id)
}

func (f *fakeUserStore) Count(_ context.Context) (int, error) { return len(f.users), nil }

func (f *fakeUserStore) List(_ context.Context) ([]domain.User, error) {
	ids := make([]int64, 0, len(f.users))
	for id := range f.users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []domain.User
	for _, id := range ids {
		if f.users[id].Pending {
			// Mirrors the real store: a pending account belongs to the Pending tab
			// and never to the managed-user list (issue #419, S3).
			continue
		}
		out = append(out, *f.users[id])
	}
	return out, nil
}

func (f *fakeUserStore) ListActive(_ context.Context) ([]domain.User, error) {
	all, err := f.List(context.Background())
	if err != nil {
		return nil, err
	}
	var out []domain.User
	for _, u := range all {
		if u.Active {
			out = append(out, u)
		}
	}
	return out, nil
}

// ListPending mirrors the real store's Pending tab read (issue #419, S3).
func (f *fakeUserStore) ListPending(_ context.Context) ([]domain.User, error) {
	ids := make([]int64, 0, len(f.users))
	for id, u := range f.users {
		if u.Pending {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []domain.User
	for _, id := range ids {
		out = append(out, *f.users[id])
	}
	return out, nil
}

// ListPendingSignIn answers the stored provider rows of the accounts that wait
// for approval, ordered by account id in insertion order per account: the
// service normalizes the display order, so the fake keeps the store's own.
func (f *fakeUserStore) ListPendingSignIn(_ context.Context) ([]application.PendingSignIn, error) {
	f.signInReads++
	ids := make([]int64, 0, len(f.signIns))
	for id := range f.signIns {
		if u, ok := f.users[id]; ok && u.Pending {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []application.PendingSignIn
	for _, id := range ids {
		for _, provider := range f.signIns[id] {
			out = append(out, application.PendingSignIn{UserID: id, Provider: provider})
		}
	}
	return out, nil
}

// ApprovePending mirrors the real store's atomic bulk approval: the batch is
// validated and every entry checked before anything is written, so a refused
// batch changes nothing (issue #419, S3).
func (f *fakeUserStore) ApprovePending(_ context.Context, approvals []application.PendingApproval, actorID int64, _ time.Time) ([]domain.User, error) {
	if err := application.ValidatePendingApprovals(approvals); err != nil {
		return nil, err
	}
	for _, approval := range approvals {
		if approval.Role == domain.RoleRoot {
			return nil, domain.NewRootProtectedError()
		}
	}
	for _, approval := range approvals {
		u, ok := f.users[approval.ID]
		if !ok || !u.Pending || u.Role == domain.RoleRoot {
			return nil, &domain.NotFoundError{Kind: "user", ID: approval.ID}
		}
	}
	var approved []domain.User
	for _, approval := range approvals {
		u := f.users[approval.ID]
		if u.Role != approval.Role {
			f.roleChanges = append(f.roleChanges, fakeRoleChange{actorID: actorID})
		}
		u.Role = approval.Role
		u.Active = true
		u.Pending = false
		approved = append(approved, *u)
	}
	return approved, nil
}

// fakeSessionStore implements SessionStore with lazy purge of expired
// sessions (D14). Expiry is checked against the injected clock (D7), never
// the real time — otherwise tests pass only while the fixed fake clock date
// is in the future.
type fakeSessionStore struct {
	sessions map[string]*domain.Session
	clock    domain.Clock
}

func newFakeSessionStore(clock domain.Clock) *fakeSessionStore {
	return &fakeSessionStore{sessions: map[string]*domain.Session{}, clock: clock}
}

func (f *fakeSessionStore) Create(_ context.Context, s *domain.Session) error {
	cp := *s
	f.sessions[s.ID] = &cp
	return nil
}

func (f *fakeSessionStore) GetByID(_ context.Context, id string) (*domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok || f.clock.Now().After(s.ExpiresAt) {
		delete(f.sessions, id)
		return nil, &domain.NotFoundError{Kind: "session", ID: id}
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSessionStore) Delete(_ context.Context, id string) error {
	delete(f.sessions, id)
	return nil
}

// fakeCategoryStore implements CategoryStore: name uniqueness and a delete
// guard for referenced categories.
type fakeCategoryStore struct {
	categories map[int64]*domain.Category
	byName     map[string]int64
	nextID     int64
	referenced map[int64]bool
}

func newFakeCategoryStore() *fakeCategoryStore {
	return &fakeCategoryStore{
		categories: map[int64]*domain.Category{},
		byName:     map[string]int64{},
		nextID:     1,
		referenced: map[int64]bool{},
	}
}

// seed inserts a category directly (test arrange).
func (f *fakeCategoryStore) seed(name string) domain.Category {
	c := domain.Category{Name: name}
	c.ID = f.nextID
	f.nextID++
	f.categories[c.ID] = &c
	f.byName[c.Name] = c.ID
	return c
}

func (f *fakeCategoryStore) markReferenced(id int64) { f.referenced[id] = true }

func (f *fakeCategoryStore) Create(_ context.Context, c *domain.Category) error {
	if _, dup := f.byName[c.Name]; dup {
		return &domain.DuplicateError{Kind: "category", Name: c.Name}
	}
	c.ID = f.nextID
	f.nextID++
	cp := *c
	f.categories[c.ID] = &cp
	f.byName[c.Name] = c.ID
	return nil
}

func (f *fakeCategoryStore) Update(_ context.Context, c *domain.Category) error {
	existing, ok := f.categories[c.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "category", ID: c.ID}
	}
	if otherID, dup := f.byName[c.Name]; dup && otherID != c.ID {
		return &domain.DuplicateError{Kind: "category", Name: c.Name}
	}
	delete(f.byName, existing.Name)
	cp := *c
	f.categories[c.ID] = &cp
	f.byName[c.Name] = c.ID
	return nil
}

func (f *fakeCategoryStore) Delete(_ context.Context, id int64) error {
	if f.referenced[id] {
		return &domain.ReferencedError{Kind: "category", ID: id}
	}
	c, ok := f.categories[id]
	if !ok {
		return &domain.NotFoundError{Kind: "category", ID: id}
	}
	delete(f.byName, c.Name)
	delete(f.categories, id)
	return nil
}

func (f *fakeCategoryStore) GetByID(_ context.Context, id int64) (*domain.Category, error) {
	c, ok := f.categories[id]
	if !ok {
		return nil, &domain.NotFoundError{Kind: "category", ID: id}
	}
	cp := *c
	return &cp, nil
}

func (f *fakeCategoryStore) List(_ context.Context) ([]domain.Category, error) {
	var out []domain.Category
	for _, c := range f.categories {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// fakeWorkflowStore is PR3 fake for WorkflowStore.
type fakeWorkflowStore struct {
	getCalls    []int64
	upsertCalls []struct {
		cat   int64
		draft []byte
	}
	publishCalls []struct {
		cat   int64
		draft []byte
		by    *int64
	}
	drafts    map[int64][]byte
	revisions map[int64]int64
	published *domain.WorkflowDefinition
	// attribution is the issue #253 read model the fake records alongside each
	// accepted draft write, exactly like the SQLite adapter: a refused write
	// leaves it untouched. Names are intentionally not resolved here (the fake
	// has no user table), so the application tests assert the recorded id and
	// instant; the store test proves name resolution.
	attribution map[int64]application.WorkflowAttribution
}

func newFakeWorkflowStore() *fakeWorkflowStore {
	return &fakeWorkflowStore{drafts: map[int64][]byte{}, revisions: map[int64]int64{}, attribution: map[int64]application.WorkflowAttribution{}}
}
func (f *fakeWorkflowStore) GetDraft(_ context.Context, categoryID int64) ([]byte, error) {
	f.getCalls = append(f.getCalls, categoryID)
	if d, ok := f.drafts[categoryID]; ok {
		cp := make([]byte, len(d))
		copy(cp, d)
		return cp, nil
	}
	return nil, nil
}
func (f *fakeWorkflowStore) UpsertDraft(_ context.Context, categoryID int64, draft []byte) error {
	cp := make([]byte, len(draft))
	copy(cp, draft)
	f.upsertCalls = append(f.upsertCalls, struct {
		cat   int64
		draft []byte
	}{cat: categoryID, draft: cp})
	f.drafts[categoryID] = cp
	return nil
}

// GetDraftWithRevision and SaveDraftIfRevision give the base fake the
// optimistic-lock capability (issue #254), one revision per category, exactly
// like the SQLite adapter: SaveDraftIfRevision is a compare-and-swap, and a
// stale expectation is refused with application.ErrDraftRevisionConflict.
func (f *fakeWorkflowStore) GetDraftWithRevision(ctx context.Context, categoryID int64) ([]byte, int64, error) {
	draft, err := f.GetDraft(ctx, categoryID)
	return draft, f.revisions[categoryID], err
}
func (f *fakeWorkflowStore) SaveDraftIfRevision(ctx context.Context, categoryID int64, expectedRevision int64, actorID int64, draft []byte) (int64, error) {
	if f.revisions[categoryID] != expectedRevision {
		return f.revisions[categoryID], application.ErrDraftRevisionConflict
	}
	if err := f.UpsertDraft(ctx, categoryID, draft); err != nil {
		return 0, err
	}
	f.revisions[categoryID]++
	// Attribution rides the accepted write, like the store's same-statement
	// UPDATE. fixedStamp is a frozen instant so the read model is deterministic.
	f.attribution[categoryID] = application.WorkflowAttribution{
		DraftUpdatedByUserID: actorID,
		DraftUpdatedAt:       fixedStamp,
		Version:              f.attribution[categoryID].Version,
		PublishedByUserID:    f.attribution[categoryID].PublishedByUserID,
		PublishedAt:          f.attribution[categoryID].PublishedAt,
	}
	return f.revisions[categoryID], nil
}

// fixedStamp is the frozen instant the fake store records on a draft write, so
// application-layer assertions never depend on the wall clock.
var fixedStamp = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

// GetAttribution and ListAttribution give the base fake the read capability
// (issue #253). A category with no recorded facts answers the zero value.
func (f *fakeWorkflowStore) GetAttribution(_ context.Context, categoryID int64) (application.WorkflowAttribution, error) {
	return f.attribution[categoryID], nil
}
func (f *fakeWorkflowStore) ListAttribution(_ context.Context) (map[int64]application.WorkflowAttribution, error) {
	out := map[int64]application.WorkflowAttribution{}
	for id, a := range f.attribution {
		out[id] = a
	}
	return out, nil
}

// Publish mirrors the store's historical method: it publishes against the
// CURRENT revision (the fixture path) and still advances it.
func (f *fakeWorkflowStore) Publish(ctx context.Context, categoryID int64, draft []byte, by *int64) (int64, []domain.WorkflowValidationIssue, error) {
	vid, _, iss, err := f.PublishAtRevision(ctx, categoryID, draft, f.revisions[categoryID], by)
	return vid, iss, err
}

// PublishAtRevision is the guarded publish (issue #254): a stale expectation
// writes nothing and is refused with ErrDraftRevisionConflict; a success
// advances the revision by one so a later save at the old revision is refused.
func (f *fakeWorkflowStore) PublishAtRevision(_ context.Context, categoryID int64, draft []byte, expectedRevision int64, by *int64) (int64, int64, []domain.WorkflowValidationIssue, error) {
	if f.revisions[categoryID] != expectedRevision {
		return 0, 0, nil, application.ErrDraftRevisionConflict
	}
	def, err := domain.ParseWorkflowDefinition(draft)
	if err != nil {
		return 0, 0, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	if iss := def.Validate(); len(iss) > 0 {
		return 0, 0, iss, nil
	}
	cp := make([]byte, len(draft))
	copy(cp, draft)
	f.publishCalls = append(f.publishCalls, struct {
		cat   int64
		draft []byte
		by    *int64
	}{cat: categoryID, draft: cp, by: by})
	f.drafts[categoryID] = cp
	f.revisions[categoryID] = expectedRevision + 1
	f.attribution[categoryID] = application.WorkflowAttribution{
		DraftUpdatedByUserID: attributionID(by),
		DraftUpdatedAt:       fixedStamp,
		Version:              int(f.revisions[categoryID]),
		PublishedByUserID:    attributionID(by),
		PublishedAt:          fixedStamp,
	}
	c := def
	f.published = &c
	return int64(len(f.publishCalls)), f.revisions[categoryID], nil, nil
}
func (f *fakeWorkflowStore) ListSummaries(_ context.Context) ([]application.WorkflowSummary, error) {
	return []application.WorkflowSummary{}, nil
}

func (f *fakeWorkflowStore) ListAvailableCategories(_ context.Context) ([]domain.Category, error) {
	return []domain.Category{}, nil
}

// attributionID dereferences an optional actor id for the fake's attribution
// read model.
func attributionID(by *int64) int64 {
	if by == nil {
		return 0
	}
	return *by
}

// --- PR5 S5 fakes: workflow create+pin+run orchestration ---

// fakeWorkflowVersionStore resolves a category's current published workflow
// version for ticket creation (WorkflowVersionStore, design S5: availability =
// a published version exists; new tickets read current_version_id only).
// publish() mirrors the store contract: it allocates MAX+1 version ids per
// category, switches the current pointer, and persists a DEEP COPY of the
// definition (the real store persists canonical bytes) so mutations of the
// caller's definition after publish never corrupt the published version.
type fakeWorkflowVersionStore struct {
	versions map[int64]*application.PublishedWorkflow
	next     map[int64]int
	calls    []int64
	// failWith, when non-nil, makes GetCurrentVersion fail with this exact
	// error: the service must propagate it untouched and write/plan nothing
	// (version-store-error regression).
	failWith error
}

func newFakeWorkflowVersionStore() *fakeWorkflowVersionStore {
	return &fakeWorkflowVersionStore{versions: map[int64]*application.PublishedWorkflow{}, next: map[int64]int{}}
}

// publish makes def the category's CURRENT published version (MAX+1 per
// category) and returns the new version id. The stored Workflow is a deep copy
// (domain.WorkflowDefinition.Clone), mirroring the real store persisting
// canonical bytes: mutating the caller's definition after publish must never
// corrupt the published version — OriginalDefMutationAfterPublishDoesNotLeak
// proves it.
func (f *fakeWorkflowVersionStore) publish(catID int64, def domain.WorkflowDefinition) int64 {
	f.next[catID]++
	ver := int64(f.next[catID])
	f.versions[catID] = &application.PublishedWorkflow{CategoryID: catID, VersionID: ver, Workflow: def.Clone()}
	return ver
}

// GetCurrentVersion returns a fresh struct copy of the store's current
// PublishedWorkflow, but its Workflow deliberately ALIASES the store-owned
// definition — like an adapter that caches/re-serves shared step/config memory.
// The application trust boundary MUST deep-snapshot what it receives before
// planning or capture; CapturedPlanImmuneToStoreMutation proves that contract
// by mutating the store-owned definition after a plan was captured.
func (f *fakeWorkflowVersionStore) GetCurrentVersion(_ context.Context, categoryID int64) (*application.PublishedWorkflow, error) {
	f.calls = append(f.calls, categoryID)
	if f.failWith != nil {
		return nil, f.failWith
	}
	p, ok := f.versions[categoryID]
	if !ok || p == nil {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

// fakeWorkflowUnitOfWork implements WorkflowUnitOfWork.CreateTicketWithRun as a
// scripted port: it records the EXACT CreateTicketWithRunInput for assertions,
// persists the ticket (store-assigned ID/Number, state from the plan) plus the
// created audit on success, and writes NOTHING when failCreate is set. It
// deliberately does not apply the planned operations — atomic application
// against real SQLite belongs to PR5 Batch B (workflow_uow_create_test.go).
type fakeWorkflowUnitOfWork struct {
	tickets    *fakeTicketStore
	audits     *fakeAuditStore
	calls      []application.CreateTicketWithRunInput
	applyCalls []application.WorkflowMutationPlan
	failCreate bool
	failWith   error
}

func newFakeWorkflowUnitOfWork(tickets *fakeTicketStore, audits *fakeAuditStore) *fakeWorkflowUnitOfWork {
	return &fakeWorkflowUnitOfWork{tickets: tickets, audits: audits}
}

func (f *fakeWorkflowUnitOfWork) CreateTicketWithRun(ctx context.Context, in application.CreateTicketWithRunInput) (*domain.Ticket, error) {
	// Record the EXACT plan the service submitted. The recording intentionally
	// aliases the submitted definition/operations slices (only the ticket gets
	// a struct-level copy): it is the alias canary — if the service ever
	// captures caller/store-owned memory instead of its own deep snapshot, the
	// caller-mutation regression
	// (TestTicketService_CreateWithWorkflow_CapturedPlanImmuneToStoreMutation)
	// fails immediately. The recording must not hide aliasing.
	rec := in
	if in.Ticket != nil {
		cp := *in.Ticket
		rec.Ticket = &cp
	}
	f.calls = append(f.calls, rec)
	if f.failCreate {
		return nil, f.failWith
	}
	t := *in.Ticket
	t.State = in.NextTicketState
	if err := f.tickets.Create(ctx, &t); err != nil {
		return nil, err
	}
	ev := in.CreatedAudit
	ev.TicketID = t.ID
	if err := f.audits.Append(ctx, ev); err != nil {
		return nil, err
	}
	return &t, nil
}

// ApplyWorkflowPlan implements WorkflowUnitOfWork.ApplyWorkflowPlan as a scripted
// port: it records the submitted plan and applies the fixed ticket state/cursor
// facts (NextTicketState/NextAssigneeUserID) without simulating per-operation
// writes — real atomic application belongs to the SQLite adapter. It returns a
// refreshed result built from the persisted ticket so application consumers can
// invoke both methods on the same seam.
func (f *fakeWorkflowUnitOfWork) ApplyWorkflowPlan(ctx context.Context, in application.WorkflowMutationPlan) (application.WorkflowExecutionResult, error) {
	f.applyCalls = append(f.applyCalls, in)
	if f.failCreate {
		return application.WorkflowExecutionResult{}, f.failWith
	}
	t, err := f.tickets.GetByID(ctx, in.TicketID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}
	t.State = in.NextTicketState
	if in.NextAssigneeUserID != nil {
		v := *in.NextAssigneeUserID
		t.UserID = &v
	}
	if err := f.tickets.Update(ctx, t); err != nil {
		return application.WorkflowExecutionResult{}, err
	}
	return application.WorkflowExecutionResult{Ticket: t, Run: &application.WorkflowRun{TicketID: t.ID, CurrentStepIndex: in.NextCursor, Status: in.NextRunStatus}}, nil
}

// fakeSLAStore is the in-memory SLA port fake (issue #211). It serves the
// frozen commitment and the observed milestones keyed by ticket id.
// Simplification documented per the fakes contract: storage failure is
// injectable ONLY through listByCategoryErr (the ResolveForCreate
// error-propagation seam), upsertDefaultsErr and upsertCategoryTargetsErr
// (the configuration-write error-propagation seams); every other method
// always succeeds because the remaining service contracts under test here
// are composition, not storage failure (the real store's absence-as-state
// semantics are covered at the sqlite layer). The batch upserts apply
// trivially atomically — the fake has no partial-write mode: real
// transactional atomicity is proven against SQLite in sla_store_test.go.
// The batch reads (TicketSLAs/MilestonesFor) mirror the real absence
// contract: a ticket with no row is absent from the map, and empty input
// answers an empty map WITHOUT recording a call. ticketSLAsCalls and
// milestonesForCalls are countable so a service test can prove the batch
// path performs exactly ONE read per method for any number of tickets.
type fakeSLAStore struct {
	frozen     map[int64]*domain.TicketSLA
	milestones map[int64]domain.SLAMilestones
	// policies mirrors sla_policies per category for ResolveForCreate; nil
	// means the category has no materialized matrix (absence as a state).
	policies map[int64][]domain.SLAPolicy
	// listByCategoryErr, when non-nil, makes ListByCategory fail with this
	// exact error: ResolveForCreate must propagate it so the create fails
	// rather than silently proceeding without a commitment.
	listByCategoryErr error
	// defaults mirrors sla_defaults for the SetDefaultTargets tests.
	defaults []domain.SLAPolicy
	// upsertDefaultsErr / upsertCategoryTargetsErr, when non-nil, make the
	// matching batch upsert fail with this exact error BEFORE any state
	// changes: SetDefaultTargets / SetCategoryTargets must propagate it
	// untouched.
	upsertDefaultsErr        error
	upsertCategoryTargetsErr error
	// call recorders for the write seams: the authorization tests assert
	// these stay at zero for a denied actor (a denied use case must touch
	// no store).
	upsertDefaultsCalls        int
	upsertCategoryTargetsCalls int
	// call recorders for the batch read seams: the ForTickets tests assert
	// exactly ONE call of each for a whole batch of tickets (never the
	// per-ticket N+1) and ZERO calls for empty input.
	ticketSLAsCalls    int
	milestonesForCalls int
	// insertTicketSLACalls counts freeze writes. The freeze-validation tests
	// assert it stays at zero when an out-of-range warning percent aborts the
	// freeze, so no commitment is persisted.
	insertTicketSLACalls int
}

func (f *fakeSLAStore) ListDefaults(_ context.Context) ([]domain.SLAPolicy, error) {
	return nil, errors.New("fakeSLAStore: ListDefaults not needed by the SLA service tests")
}

func (f *fakeSLAStore) ListByCategory(_ context.Context, categoryID int64) ([]domain.SLAPolicy, error) {
	if f.listByCategoryErr != nil {
		return nil, f.listByCategoryErr
	}
	return f.policies[categoryID], nil
}

func (f *fakeSLAStore) UpsertDefault(_ context.Context, policy domain.SLAPolicy) error {
	return errors.New("fakeSLAStore: UpsertDefault not needed by the SLA service tests")
}

// UpsertDefaults records the batch and replaces the whole in-memory
// default matrix. The single-row UpsertDefault stays unimplemented: only
// the transactional batch is on the service's write path.
func (f *fakeSLAStore) UpsertDefaults(_ context.Context, policies []domain.SLAPolicy) error {
	f.upsertDefaultsCalls++
	if f.upsertDefaultsErr != nil {
		return f.upsertDefaultsErr
	}
	f.defaults = append([]domain.SLAPolicy(nil), policies...)
	return nil
}

func (f *fakeSLAStore) UpsertCategoryTarget(_ context.Context, _ int64, _ domain.SLAPolicy) error {
	return errors.New("fakeSLAStore: UpsertCategoryTarget not needed by the SLA service tests")
}

// UpsertCategoryTargets records the batch and replaces the category's
// whole in-memory matrix. The single-row UpsertCategoryTarget stays
// unimplemented: only the transactional batch is on the service's write
// path.
func (f *fakeSLAStore) UpsertCategoryTargets(_ context.Context, categoryID int64, policies []domain.SLAPolicy) error {
	f.upsertCategoryTargetsCalls++
	if f.upsertCategoryTargetsErr != nil {
		return f.upsertCategoryTargetsErr
	}
	if f.policies == nil {
		f.policies = map[int64][]domain.SLAPolicy{}
	}
	f.policies[categoryID] = append([]domain.SLAPolicy(nil), policies...)
	return nil
}

func (f *fakeSLAStore) TicketSLA(_ context.Context, ticketID int64) (*domain.TicketSLA, error) {
	return f.frozen[ticketID], nil
}

func (f *fakeSLAStore) InsertTicketSLA(_ context.Context, ticketID int64, sla domain.TicketSLA) error {
	f.insertTicketSLACalls++
	if f.frozen == nil {
		f.frozen = map[int64]*domain.TicketSLA{}
	}
	f.frozen[ticketID] = &sla
	return nil
}

func (f *fakeSLAStore) Milestones(_ context.Context, ticketID int64) (domain.SLAMilestones, error) {
	return f.milestones[ticketID], nil
}

// TicketSLAs mirrors the real batch contract: a ticket with no frozen row
// is ABSENT from the map (absence is the "no SLA" state), and empty input
// answers an empty map without recording a call.
func (f *fakeSLAStore) TicketSLAs(_ context.Context, ticketIDs []int64) (map[int64]*domain.TicketSLA, error) {
	out := make(map[int64]*domain.TicketSLA, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return out, nil
	}
	f.ticketSLAsCalls++
	for _, id := range ticketIDs {
		if sla, ok := f.frozen[id]; ok {
			out[id] = sla
		}
	}
	return out, nil
}

// MilestonesFor mirrors the real batch contract: a ticket with NEITHER
// observation is ABSENT from the map, and empty input answers an empty
// map without recording a call.
func (f *fakeSLAStore) MilestonesFor(_ context.Context, ticketIDs []int64) (map[int64]domain.SLAMilestones, error) {
	out := make(map[int64]domain.SLAMilestones, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return out, nil
	}
	f.milestonesForCalls++
	for _, id := range ticketIDs {
		if m, ok := f.milestones[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

// fakeSLASettingsStore is the minimal settings double for the SLA service
// tests. Simplification documented per the fakes contract: only the
// enablement key, its failure seam, the warning percent, the working
// calendar, and the SLA write seams are configurable — the appearance key
// is irrelevant to the SLA paths under test. A warningPercent of 0 means
// "unset" and answers the documented default (a real instance stores the
// seeded 80); set slaWarningPercentSet to store an explicit 0 — the
// out-of-range row the freeze validation must refuse. A calendar with no
// working days means "unset" and answers the documented default, exactly
// like the real store's absent-or-unparseable fallback.
type fakeSLASettingsStore struct {
	slaWarningPercent    int
	slaWarningPercentSet bool
	// slaEnabled mirrors the sla_enabled setting (issue #211). The zero
	// value is disabled — exactly what migration 0014 seeds, so existing
	// tests keep their historical no-SLA behaviour by construction.
	slaEnabled bool
	// slaEnabledErr, when non-nil, makes GetSLAEnabled fail with this exact
	// error: ResolveForCreate must propagate it so the create fails rather
	// than silently proceeding without a commitment.
	slaEnabledErr error
	// Write seams for the configuration use cases. Each set*Err, when
	// non-nil, makes the matching writer fail with this exact error BEFORE
	// any state changes; each call recorder lets the authorization tests
	// prove a denied actor touched NO store method.
	setSLAEnabledStateErr     error
	setSLAWarningPercentErr   error
	setSLAConfigurationErr    error
	enabledAt                 time.Time
	defaults                  []domain.SLAPolicy
	setSLAEnabledStateCalls   int
	setSLAWarningPercentCalls int
	setSLAConfigurationCalls  int
	// calendar mirrors the sla_calendar_* settings (issue #211); the zero
	// value (no working days) answers the documented default.
	calendar            domain.SLACalendar
	calendarErr         error
	setSLACalendarErr   error
	setSLACalendarCalls int
	// holdAutoCloseDays mirrors the hold_auto_close_working_days setting (issue
	// #353); the zero value is the documented default — the feature off.
	holdAutoCloseDays         int
	holdAutoCloseDaysErr      error
	setHoldAutoCloseDaysErr   error
	setHoldAutoCloseDaysCalls int
}

func (f *fakeSLASettingsStore) GetInternalCommentBg(_ context.Context) (string, error) {
	return application.DefaultInternalCommentBg, nil
}

func (f *fakeSLASettingsStore) SetInternalCommentBg(_ context.Context, _ string) error {
	return errors.New("fakeSLASettingsStore: SetInternalCommentBg not needed by the SLA service tests")
}

// The interface language (issue #335) is unused by the SLA service; the fake
// answers the documented default so it keeps satisfying the widened
// SettingsStore port.
func (f *fakeSLASettingsStore) GetInterfaceLanguage(_ context.Context) (string, error) {
	return application.DefaultInterfaceLanguage, nil
}

func (f *fakeSLASettingsStore) SetInterfaceLanguage(_ context.Context, _ string) error {
	return errors.New("fakeSLASettingsStore: SetInterfaceLanguage not needed by the SLA service tests")
}

// The ticket creation flow (issue #391) is unused by the SLA service; the fake
// answers the documented default so it keeps satisfying the widened
// SettingsStore port.
func (f *fakeSLASettingsStore) GetTicketCreationMode(_ context.Context) (string, error) {
	return application.DefaultTicketCreationMode, nil
}

func (f *fakeSLASettingsStore) SetTicketCreationMode(_ context.Context, _ string) error {
	return errors.New("fakeSLASettingsStore: SetTicketCreationMode not needed by the SLA service tests")
}

func (f *fakeSLASettingsStore) GetSLAEnabled(_ context.Context) (bool, error) {
	if f.slaEnabledErr != nil {
		return false, f.slaEnabledErr
	}
	return f.slaEnabled, nil
}

func (f *fakeSLASettingsStore) GetSLAWarningPercent(_ context.Context) (int, error) {
	if f.slaWarningPercent == 0 && !f.slaWarningPercentSet {
		return application.DefaultSLAWarningPercent, nil
	}
	return f.slaWarningPercent, nil
}

func (f *fakeSLASettingsStore) GetSLAEnabledAt(_ context.Context) (time.Time, error) {
	return f.enabledAt, nil
}

// SetSLAEnabledState mirrors the store contract: the flag and the instant
// are one write, and the instant records the FIRST enable (an existing
// value survives a second enable and a disable).
func (f *fakeSLASettingsStore) SetSLAEnabledState(_ context.Context, enabled bool, at time.Time) error {
	f.setSLAEnabledStateCalls++
	if f.setSLAEnabledStateErr != nil {
		return f.setSLAEnabledStateErr
	}
	f.slaEnabled = enabled
	if enabled && f.enabledAt.IsZero() {
		f.enabledAt = at
	}
	return nil
}

// SetSLAConfiguration mirrors the store contract: the whole panel is one
// write, so a failure leaves none of it applied.
func (f *fakeSLASettingsStore) SetSLAConfiguration(_ context.Context, enabled bool, at time.Time, warningPercent int, defaults []domain.SLAPolicy) error {
	f.setSLAConfigurationCalls++
	if f.setSLAConfigurationErr != nil {
		return f.setSLAConfigurationErr
	}
	f.slaEnabled = enabled
	f.slaWarningPercent = warningPercent
	if enabled && f.enabledAt.IsZero() {
		f.enabledAt = at
	}
	f.defaults = defaults
	return nil
}

func (f *fakeSLASettingsStore) SetSLAWarningPercent(_ context.Context, percent int) error {
	f.setSLAWarningPercentCalls++
	if f.setSLAWarningPercentErr != nil {
		return f.setSLAWarningPercentErr
	}
	f.slaWarningPercent = percent
	return nil
}

func (f *fakeSLASettingsStore) GetSLACalendar(_ context.Context) (domain.SLACalendar, error) {
	if f.calendarErr != nil {
		return domain.SLACalendar{}, f.calendarErr
	}
	if len(f.calendar.WorkingDays) == 0 {
		return domain.DefaultSLACalendar(), nil
	}
	return f.calendar, nil
}

// SetSLACalendar mirrors the store contract: the whole calendar is one
// write, so a failure leaves the previous calendar standing.
func (f *fakeSLASettingsStore) SetSLACalendar(_ context.Context, calendar domain.SLACalendar) error {
	f.setSLACalendarCalls++
	if f.setSLACalendarErr != nil {
		return f.setSLACalendarErr
	}
	f.calendar = calendar
	return nil
}

// GetHoldAutoCloseDays mirrors the store contract of issue #353: an absent or
// unparseable row reads back 0 (off), and a stored value answers itself.
func (f *fakeSLASettingsStore) GetHoldAutoCloseDays(_ context.Context) (int, error) {
	if f.holdAutoCloseDaysErr != nil {
		return 0, f.holdAutoCloseDaysErr
	}
	return f.holdAutoCloseDays, nil
}

// SetHoldAutoCloseDays records the window and fails on demand before any state
// changes, so a rejected or denied actor can be proven to have persisted
// nothing.
func (f *fakeSLASettingsStore) SetHoldAutoCloseDays(_ context.Context, days int) error {
	f.setHoldAutoCloseDaysCalls++
	if f.setHoldAutoCloseDaysErr != nil {
		return f.setHoldAutoCloseDaysErr
	}
	f.holdAutoCloseDays = days
	return nil
}

// fakeHoldExpiryStore implements HoldExpiryStore (issue #353) over the ticket
// and unit-of-work fakes: the held-ticket read walks the ticket fake and the
// reminder check reads the notification rows the fakes recorded as committed,
// exactly as the real store answers from the notifications table.
//
// Simplifications, documented per the fakes contract: the read is unbounded
// (the real one limits one pass, proven in the SQLite layer) and the ticket
// fake has no SQL predicate, so the filtering is the same open-and-held
// predicate written in Go.

// errHeldTickets mirrors a store failure of the pass's first read.
var errHeldTickets = errors.New("held tickets read failed")

type fakeHoldExpiryStore struct {
	tickets *fakeTicketStore
	tx      *fakeUnitOfWork
	// heldErr, when non-nil, makes the read fail with this exact error: the
	// pass must abort instead of closing or reminding about nothing.
	heldErr error
}

func (f *fakeHoldExpiryStore) HeldTickets(_ context.Context, afterID int64, limit int) ([]domain.Ticket, error) {
	if f.heldErr != nil {
		return nil, f.heldErr
	}
	var out []domain.Ticket
	for _, t := range f.tickets.tickets {
		if t.ID > afterID && t.OnHoldSince != nil && !domain.IsClosed(t.State) {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeHoldExpiryStore) ReminderSent(_ context.Context, ticketID int64, since time.Time) (bool, error) {
	for _, n := range f.tx.notifications {
		if n.TicketID == ticketID && n.Kind == domain.NotificationHoldReminder && !n.CreatedAt.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

// fakeAlertStore serves the operational alert pass (issue #408) from a fixed
// candidate list: the tests arrange the exact facts the pass reads, so the
// alert RULES are what each case exercises, not the SQL that gathers them (the
// real query is covered in the sqlite adapter tests).
//
// Documented simplification: the list is NOT filtered by assignee or state the
// way the real read is. The real store excludes unassigned and closed tickets
// at the source; keeping them here lets a test prove the pass ALSO refuses to
// alert about them, which is the behavior the specs name.
type fakeAlertStore struct {
	candidates []application.AlertCandidate
	tx         *fakeAlertUnitOfWork
	// err, when non-nil, makes the candidate read fail with this exact error:
	// the pass must abort rather than alert on partial information.
	err error
}

func (f *fakeAlertStore) AlertCandidates(_ context.Context, afterID int64, limit int) ([]application.AlertCandidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []application.AlertCandidate
	for _, c := range f.candidates {
		if c.Ticket.ID > afterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticket.ID < out[j].Ticket.ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeAlertStore) AlertSent(_ context.Context, ticketID int64, kind domain.NotificationKind, since *time.Time) (bool, error) {
	return f.tx.sent(ticketID, kind, since), nil
}

// fakeAlertUnitOfWork records the alerts the pass appends. It re-checks the
// same once-per-condition predicate the real unit of work evaluates inside its
// transaction, so a test can prove the pass never writes a duplicate even when
// the cheap read missed it.
type fakeAlertUnitOfWork struct {
	notifications []domain.Notification
	// err, when non-nil, makes the write fail with this exact error.
	err error
}

func (f *fakeAlertUnitOfWork) AppendAlert(_ context.Context, ticketID int64, kind domain.NotificationKind, since *time.Time, notifications []domain.Notification) error {
	if f.err != nil {
		return f.err
	}
	if len(notifications) == 0 || f.sent(ticketID, kind, since) {
		return nil
	}
	for _, n := range notifications {
		if err := n.Validate(); err != nil {
			return err
		}
	}
	f.notifications = append(f.notifications, notifications...)
	return nil
}

func (f *fakeAlertUnitOfWork) sent(ticketID int64, kind domain.NotificationKind, since *time.Time) bool {
	for _, n := range f.notifications {
		if n.TicketID != ticketID || n.Kind != kind {
			continue
		}
		if since == nil || !n.CreatedAt.Before(*since) {
			return true
		}
	}
	return false
}

// fakeAttachmentStore serves downloads from a fixed map (issue #366); it has
// no list semantics beyond the id lookup the download use case needs.
type fakeAttachmentStore struct {
	files map[int64]application.StoredAttachment
}

func (f *fakeAttachmentStore) ListByTicket(_ context.Context, ticketID int64, includeInternal bool) ([]domain.Attachment, error) {
	var out []domain.Attachment
	for _, a := range f.files {
		if a.TicketID == ticketID && (includeInternal || a.Visibility != domain.CommentInternal) {
			out = append(out, a.Attachment)
		}
	}
	return out, nil
}

func (f *fakeAttachmentStore) Get(_ context.Context, id int64) (*application.StoredAttachment, error) {
	a, ok := f.files[id]
	if !ok {
		return nil, &domain.NotFoundError{Kind: "attachment", ID: id}
	}
	return &a, nil
}

// fakeOrganizationStore is the in-memory OrganizationStore double for the
// identity use cases (issue #383, S3). Simplifications, documented per the
// fakes contract: one memory value stands in for the keyed settings rows and
// the settings_blobs logo row, and the copy is a shallow one — these tests
// never mutate a stored logo in place, so a deep copy would prove nothing. The
// zero value answers the documented defaults, exactly like the real store's
// absent-or-empty fallback, and every field is replaced by one Set call, which
// is the transaction the real store commits.
type fakeOrganizationStore struct {
	org      domain.Organization
	getErr   error
	setErr   error
	setCalls int
}

var _ application.OrganizationStore = (*fakeOrganizationStore)(nil)

func newFakeOrganizationStore() *fakeOrganizationStore { return &fakeOrganizationStore{} }

func (f *fakeOrganizationStore) GetOrganization(_ context.Context) (domain.Organization, error) {
	if f.getErr != nil {
		return domain.Organization{}, f.getErr
	}
	org := f.org
	if org.Name == "" {
		org.Name = domain.DefaultOrganizationName
	}
	if org.HelpDesk == "" {
		org.HelpDesk = domain.DefaultOrganizationHelpDesk
	}
	return org, nil
}

func (f *fakeOrganizationStore) SetOrganization(_ context.Context, org domain.Organization) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.org = org
	return nil
}

// fakeSigninStore is the in-memory SigninStore double for the sign-in page use
// cases (issue #383, S9/S10). Simplifications, documented per the fakes
// contract: one panel value and one image stand in for the keyed settings rows
// and the settings_blobs row, the copy is shallow because no test mutates a
// stored value in place, and a Set call replaces both in one step — the
// transaction the real store commits. The zero value answers the documented
// defaults, like the real store's absent-row fallback, and setErr fails the
// whole write before any state changes, like a rolled-back transaction.
type fakeSigninStore struct {
	panel          domain.SigninPanel
	background     []byte
	backgroundMIME string
	getErr         error
	setErr         error
	setCalls       int
}

var _ application.SigninStore = (*fakeSigninStore)(nil)

func newFakeSigninStore() *fakeSigninStore { return &fakeSigninStore{} }

func (f *fakeSigninStore) GetSignin(_ context.Context) (application.SigninConfiguration, error) {
	if f.getErr != nil {
		return application.SigninConfiguration{}, f.getErr
	}
	panel := f.panel
	if panel.Content == "" {
		panel.Content = domain.SigninContentSteps
	}
	if panel.Background == "" {
		panel.Background = domain.SigninBackgroundDark
	}
	return application.SigninConfiguration{
		Panel:          panel,
		Background:     f.background,
		BackgroundMIME: f.backgroundMIME,
	}, nil
}

func (f *fakeSigninStore) SetSignin(_ context.Context, panel domain.SigninPanel, background []byte) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.panel = panel
	f.background = background
	f.backgroundMIME = ""
	if len(background) > 0 {
		if mime, ok := domain.SigninBackgroundContentType(background); ok {
			f.backgroundMIME = mime
		}
	}
	return nil
}

// signinImageBytes builds an image of the given type and total size.
func signinImageBytes(contentType string, size ...int) []byte {
	magic := []byte("\x89PNG\r\n\x1a\n")
	if contentType == domain.SigninBackgroundJPEG {
		magic = []byte("\xFF\xD8\xFF\xE0")
	}
	total := 64
	if len(size) > 0 {
		total = size[0]
	}
	data := make([]byte, total)
	copy(data, magic)
	return data
}

// fakePasswordSettingsStore is the in-memory PasswordSettingsStore double for
// the self-service password configuration (issue #414, S1/S4). Simplifications,
// documented per the fakes contract: ONE value stands in for the three keyed
// settings rows, and a Set call replaces all three in one step — the
// transaction the real store commits. An unset fake answers the documented
// defaults (both switches ON, 12), exactly like the real store's absent-row
// fallback, and setErr fails the whole write before any state changes, like a
// rolled-back transaction.
type fakePasswordSettingsStore struct {
	settings application.PasswordSettings
	stored   bool
	getErr   error
	setErr   error
	setCalls int
}

var _ application.PasswordSettingsStore = (*fakePasswordSettingsStore)(nil)

func newFakePasswordSettingsStore() *fakePasswordSettingsStore {
	return &fakePasswordSettingsStore{}
}

func (f *fakePasswordSettingsStore) GetPasswordSettings(_ context.Context) (application.PasswordSettings, error) {
	if f.getErr != nil {
		return application.PasswordSettings{}, f.getErr
	}
	if !f.stored {
		return application.DefaultPasswordSettings(), nil
	}
	return f.settings, nil
}

func (f *fakePasswordSettingsStore) SetPasswordSettings(_ context.Context, settings application.PasswordSettings) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.settings = settings
	f.stored = true
	return nil
}

// fakePasswordChangeStore is the in-memory PasswordChangeStore double for the
// own-password change (issue #414, S2). It writes through the two other fakes,
// so a service-level test can assert the hash AND the surviving sessions. Three
// documented simplifications: the hash update and the session delete happen in
// one untorn step standing in for the real immediate transaction (a test that
// needs a real rollback belongs to the sqlite layer), an unknown user is
// answered before anything is written, like the real store's rows-affected
// check, and the keep session is resolved before either write so a session that
// belongs to another account (or to nobody) is refused with nothing changed.
type fakePasswordChangeStore struct {
	users    *fakeUserStore
	sessions *fakeSessionStore
	calls    int
}

var _ application.PasswordChangeStore = (*fakePasswordChangeStore)(nil)

func newFakePasswordChangeStore(users *fakeUserStore, sessions *fakeSessionStore) *fakePasswordChangeStore {
	return &fakePasswordChangeStore{users: users, sessions: sessions}
}

func (f *fakePasswordChangeStore) ChangePasswordAndSignOutOtherSessions(_ context.Context, userID int64, passwordHash, keepSessionID string) error {
	f.calls++
	u, ok := f.users.users[userID]
	if !ok {
		return &domain.NotFoundError{Kind: "user", ID: userID}
	}
	kept, ok := f.sessions.sessions[keepSessionID]
	if !ok {
		return &domain.NotFoundError{Kind: "session", ID: keepSessionID}
	}
	if kept.UserID != userID {
		return domain.NewForbiddenError("the session making the change belongs to another account")
	}
	u.PasswordHash = passwordHash
	for id, session := range f.sessions.sessions {
		if session.UserID == userID && id != keepSessionID {
			delete(f.sessions.sessions, id)
		}
	}
	return nil
}

// fakePasswordResetStore is the in-memory PasswordResetStore double for the
// self-service recovery path (issue #414, S3/S8/S9). Three documented
// simplifications: one flat slice stands in for the table, every mutation is
// one untorn step standing in for the real immediate transaction (a test that
// needs a real rollback belongs to the sqlite layer), and an unknown, spent or
// expired hash answers domain.ErrNotFound alike, exactly like the real store, so
// the use case owns the one refusal the caller sees.
type fakePasswordResetStore struct {
	tokens    []*fakePasswordResetToken
	users     *fakeUserStore
	sessions  *fakeSessionStore
	lastErr   error
	issueErr  error
	usableErr error
	redeemErr error
}

// fakePasswordResetToken is one stored link.
type fakePasswordResetToken struct {
	userID    int64
	hash      string
	createdAt time.Time
	expiresAt time.Time
	usedAt    *time.Time
}

var _ application.PasswordResetStore = (*fakePasswordResetStore)(nil)

func newFakePasswordResetStore(users *fakeUserStore, sessions *fakeSessionStore) *fakePasswordResetStore {
	return &fakePasswordResetStore{users: users, sessions: sessions}
}

func (f *fakePasswordResetStore) LastPasswordResetRequest(_ context.Context, userID int64) (*time.Time, error) {
	if f.lastErr != nil {
		return nil, f.lastErr
	}
	var newest *time.Time
	for _, token := range f.tokens {
		if token.userID != userID {
			continue
		}
		if newest == nil || token.createdAt.After(*newest) {
			at := token.createdAt
			newest = &at
		}
	}
	return newest, nil
}

func (f *fakePasswordResetStore) IssuePasswordResetToken(_ context.Context, userID int64, hash string, createdAt, expiresAt time.Time) error {
	if f.issueErr != nil {
		return f.issueErr
	}
	f.voidPending(userID)
	f.tokens = append(f.tokens, &fakePasswordResetToken{userID: userID, hash: hash, createdAt: createdAt, expiresAt: expiresAt})
	return nil
}

func (f *fakePasswordResetStore) PasswordResetTokenUsable(_ context.Context, hash string, now time.Time) (bool, error) {
	if f.usableErr != nil {
		return false, f.usableErr
	}
	token := f.find(hash)
	return token != nil && token.usedAt == nil && now.Before(token.expiresAt), nil
}

func (f *fakePasswordResetStore) PasswordResetTokenOwner(_ context.Context, hash string, now time.Time) (int64, bool, error) {
	if f.usableErr != nil {
		return 0, false, f.usableErr
	}
	token := f.find(hash)
	if token == nil || token.usedAt != nil || !now.Before(token.expiresAt) {
		return 0, false, nil
	}
	return token.userID, true, nil
}

func (f *fakePasswordResetStore) RedeemPasswordResetToken(_ context.Context, hash, passwordHash string, now time.Time) error {
	if f.redeemErr != nil {
		return f.redeemErr
	}
	token := f.find(hash)
	if token == nil || token.usedAt != nil || !now.Before(token.expiresAt) {
		return domain.ErrNotFound
	}
	user, ok := f.users.users[token.userID]
	if !ok {
		return domain.ErrNotFound
	}
	user.PasswordHash = passwordHash
	spentAt := now
	token.usedAt = &spentAt
	f.voidPending(token.userID)
	for id, session := range f.sessions.sessions {
		if session.UserID == token.userID {
			delete(f.sessions.sessions, id)
		}
	}
	return nil
}

// voidPending drops the account's redeemable links, standing in for the real
// store's delete of the rows that have no used_at.
func (f *fakePasswordResetStore) voidPending(userID int64) {
	kept := make([]*fakePasswordResetToken, 0, len(f.tokens))
	for _, token := range f.tokens {
		if token.userID == userID && token.usedAt == nil {
			continue
		}
		kept = append(kept, token)
	}
	f.tokens = kept
}

func (f *fakePasswordResetStore) find(hash string) *fakePasswordResetToken {
	for _, token := range f.tokens {
		if token.hash == hash {
			return token
		}
	}
	return nil
}

// fakeCompanySignInStore is the in-memory CompanySignInSettingsStore double
// for the company sign-in configuration (issue #419). Simplifications,
// documented per the fakes contract: ONE value stands in for the keyed settings
// rows and a Set replaces it in one step — the transaction the real store
// commits; an unset fake answers the documented defaults, like the real
// store's absent-row fallback; writes counts every Set, refused or not.
type fakeCompanySignInStore struct {
	settings application.CompanySignInSettings
	stored   bool
	getErr   error
	writes   int
}

var _ application.CompanySignInSettingsStore = (*fakeCompanySignInStore)(nil)

func newFakeCompanySignInStore() *fakeCompanySignInStore {
	return &fakeCompanySignInStore{}
}

func (f *fakeCompanySignInStore) GetCompanySignIn(_ context.Context) (application.CompanySignInSettings, error) {
	if f.getErr != nil {
		return application.CompanySignInSettings{}, f.getErr
	}
	if !f.stored {
		return application.DefaultCompanySignInSettings(), nil
	}
	return f.settings, nil
}

func (f *fakeCompanySignInStore) SetCompanySignIn(_ context.Context, settings application.CompanySignInSettings) error {
	f.writes++
	f.settings = settings
	f.stored = true
	return nil
}

// fakeCompanyIdentityStore is the in-memory CompanyIdentityStore double for the
// company sign-in flow (issue #419). Simplifications, documented per the fakes
// contract: the attempt table is a map keyed by state hash, which reproduces the
// real primary key and the delete-on-consume rule; identities are a slice and
// the account they point at lives in the fake user store, so the pending flag
// and the role are the ones that store holds; the two uniques
// ((issuer, subject) and (user_id, provider)) are checked before the write, which
// is what the real table's constraints do; a state hash never collides here; and
// the notices a pending creation carries are stamped with the account id the fake
// is about to assign and validated before the write, so a rejected batch stores
// nothing — the rollback the real transaction performs.
type fakeCompanyIdentityStore struct {
	users          *fakeUserStore
	attempts       map[string]application.CompanySignInAttempt
	identities     []*application.CompanyIdentity
	nextIdentityID int64
	purges         []time.Time
	// failNextCreate makes the next CreateUserWithIdentity behave like a lost
	// race: the hook runs first (the winning flow's rows), then the call
	// answers ErrCompanyIdentityTaken and stores nothing.
	failNextCreate bool
	beforeCreate   func()
	// notices records the account announcements the service asked the store to
	// write with the account it created, stamped the way the real store stamps
	// them (issue #419, S3): the pending account as the subject and no ticket.
	notices []domain.Notification
	// candidates, when not nil, is the roster ActiveUsers answers instead of the
	// active accounts of the user store, so a test can arrange a duplicate row or
	// a roster the user store would never answer.
	candidates []domain.User
	// activeUsersErr makes ActiveUsers fail, the way a roster read can fail.
	activeUsersErr error
}

var _ application.CompanyIdentityStore = (*fakeCompanyIdentityStore)(nil)

func newFakeCompanyIdentityStore(users *fakeUserStore) *fakeCompanyIdentityStore {
	return &fakeCompanyIdentityStore{
		users:          users,
		attempts:       map[string]application.CompanySignInAttempt{},
		nextIdentityID: 1,
	}
}

func (f *fakeCompanyIdentityStore) CreateAttempt(_ context.Context, attempt *application.CompanySignInAttempt) error {
	if _, dup := f.attempts[attempt.StateHash]; dup {
		return errors.New("duplicate state hash")
	}
	f.attempts[attempt.StateHash] = *attempt
	return nil
}

func (f *fakeCompanyIdentityStore) ConsumeAttempt(_ context.Context, stateHash string) (*application.CompanySignInAttempt, error) {
	attempt, ok := f.attempts[stateHash]
	if !ok {
		return nil, &domain.NotFoundError{Kind: "sign-in attempt", ID: stateHash}
	}
	delete(f.attempts, stateHash)
	return &attempt, nil
}

func (f *fakeCompanyIdentityStore) PurgeExpiredAttempts(_ context.Context, before time.Time) (int, error) {
	f.purges = append(f.purges, before)
	purged := 0
	for hash, attempt := range f.attempts {
		if attempt.ExpiresAt.Before(before) {
			delete(f.attempts, hash)
			purged++
		}
	}
	return purged, nil
}

func (f *fakeCompanyIdentityStore) ResolveIdentity(_ context.Context, provider application.CompanyProvider, issuer, subject string) (*application.CompanyIdentity, *domain.User, error) {
	for _, identity := range f.identities {
		if identity.Provider != provider || identity.Issuer != issuer || identity.Subject != subject {
			continue
		}
		user, err := f.users.GetByID(context.Background(), identity.UserID)
		if err != nil {
			return nil, nil, err
		}
		cp := *identity
		return &cp, user, nil
	}
	return nil, nil, &domain.NotFoundError{Kind: "user identity", ID: subject}
}

func (f *fakeCompanyIdentityStore) UsersByEmail(_ context.Context, email string) ([]domain.User, error) {
	// It reads every account, pending ones included: the company sign-in decision
	// must recognise a waiting account by its address (issue #419, S3). It
	// therefore cannot use List, which answers managed users only.
	ids := make([]int64, 0, len(f.users.users))
	for id := range f.users.users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []domain.User
	for _, id := range ids {
		if strings.EqualFold(f.users.users[id].Email, email) {
			out = append(out, *f.users.users[id])
		}
	}
	return out, nil
}

func (f *fakeCompanyIdentityStore) CreateUserWithIdentity(_ context.Context, user *domain.User, identity *application.CompanyIdentity, notifications []domain.Notification) error {
	if f.failNextCreate {
		f.failNextCreate = false
		if f.beforeCreate != nil {
			f.beforeCreate()
		}
		return application.ErrCompanyIdentityTaken
	}
	if f.hasIdentity(identity.Provider, identity.Issuer, identity.Subject) {
		return application.ErrCompanyIdentityTaken
	}
	// The account's own id is the subject of every notice, exactly as the real
	// store stamps it (issue #419, S3). The fake knows the id it is about to
	// assign, so the batch is stamped and validated BEFORE the write: a rejected
	// notice stores neither the account nor the identity, which is the rollback
	// the real transaction performs.
	batch := make([]domain.Notification, 0, len(notifications))
	for _, n := range notifications {
		subject := f.users.nextID
		n.PendingUserID = &subject
		if err := n.Validate(); err != nil {
			return err
		}
		batch = append(batch, n)
	}
	if err := f.users.Create(context.Background(), user); err != nil {
		return err
	}
	cp := *identity
	cp.ID = f.nextIdentityID
	f.nextIdentityID++
	cp.UserID = user.ID
	f.identities = append(f.identities, &cp)
	f.notices = append(f.notices, batch...)
	return nil
}

// ActiveUsers answers the roster the recipient policy filters (issue #419, S3):
// every active account, pending ones excluded, exactly like the real read.
func (f *fakeCompanyIdentityStore) ActiveUsers(_ context.Context) ([]domain.User, error) {
	if f.activeUsersErr != nil {
		return nil, f.activeUsersErr
	}
	if f.candidates != nil {
		return append([]domain.User(nil), f.candidates...), nil
	}
	return f.users.ListActive(context.Background())
}

func (f *fakeCompanyIdentityStore) LinkIdentity(_ context.Context, identity *application.CompanyIdentity) error {
	for _, existing := range f.identities {
		if existing.UserID == identity.UserID && existing.Provider == identity.Provider {
			return application.ErrCompanyIdentityTaken
		}
	}
	cp := *identity
	cp.ID = f.nextIdentityID
	f.nextIdentityID++
	f.identities = append(f.identities, &cp)
	return nil
}

func (f *fakeCompanyIdentityStore) TouchIdentity(_ context.Context, identityID int64, email string, at time.Time) error {
	for _, identity := range f.identities {
		if identity.ID == identityID {
			identity.Email = email
			identity.LastSignInAt = at
			return nil
		}
	}
	return &domain.NotFoundError{Kind: "user identity", ID: identityID}
}

func (f *fakeCompanyIdentityStore) hasIdentity(provider application.CompanyProvider, issuer, subject string) bool {
	for _, identity := range f.identities {
		if identity.Provider == provider && identity.Issuer == issuer && identity.Subject == subject {
			return true
		}
	}
	return false
}

// seedIdentity links an account to a provider identity directly (test arrange).
func (f *fakeCompanyIdentityStore) seedIdentity(userID int64, provider application.CompanyProvider, issuer, subject, email string) {
	f.identities = append(f.identities, &application.CompanyIdentity{
		ID: f.nextIdentityID, UserID: userID, Provider: provider, Issuer: issuer, Subject: subject, Email: email,
	})
	f.nextIdentityID++
}

// fakeCompanyIdentityProvider is the CompanyIdentityProvider double: it answers
// a predictable URL and the claims the test gave it. Simplifications, per the
// fakes contract: the nonce, state, signature and lifetime checks belong to the
// adapter (internal/adapters/oidc), so this double only records the request it
// was handed and returns the arranged claims.
type fakeCompanyIdentityProvider struct {
	authURL     string
	claims      application.CompanyIdentityClaims
	exchangeErr error
	requests    []application.CompanyAuthorizationRequest
	codes       []string
}

var _ application.CompanyIdentityProvider = (*fakeCompanyIdentityProvider)(nil)

func (f *fakeCompanyIdentityProvider) AuthorizationURL(_ context.Context, _ application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest) (string, error) {
	f.requests = append(f.requests, req)
	return f.authURL + "?state=" + req.State, nil
}

func (f *fakeCompanyIdentityProvider) Exchange(_ context.Context, _ application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest, code string) (application.CompanyIdentityClaims, error) {
	f.requests = append(f.requests, req)
	f.codes = append(f.codes, code)
	if f.exchangeErr != nil {
		return application.CompanyIdentityClaims{}, f.exchangeErr
	}
	return f.claims, nil
}

// fakeApprovalNotifier records the accounts a pending first sign-in announced
// (the seam for the administrator notification).
type fakeApprovalNotifier struct {
	accounts []domain.User
	err      error
}

func (f *fakeApprovalNotifier) AccountAwaitingApproval(_ context.Context, account domain.User) error {
	f.accounts = append(f.accounts, account)
	return f.err
}

// fakeVisitEntry is one piece of a ticket's history in the fake: an audit
// event or a comment, with the actor who made it and — for comments — whether
// it is internal. id 0 on the actor means "no actor" (an automatic or legacy
// action), which the port contract says counts as somebody else's change.
type fakeVisitEntry struct {
	id       int64
	actorID  int64
	internal bool
}

// fakeTicketVisitStore is the in-memory TicketVisitStore double for the
// "what's new since the last visit" use cases (issue #415).
//
// Documented simplifications, per the fakes contract:
//   - the real store derives history from the audit_events and comments
//     tables; this fake keeps one log per kind instead, each with the same
//     shape the derivation needs (a monotonic id, an actor, a visibility),
//     because the SQL that gathers them is covered in the sqlite adapter
//     tests and is not what the service rules are about;
//   - the marks live in a nested map instead of one upserted row, so a
//     MAX(seen, new) upsert would be re-implemented rather than exercised —
//     the fake's RecordVisit overwrites, and the real store's monotonic
//     upsert has its own adapter test;
//   - notifications are a flat list the test appends to, and RecordVisit
//     stamps read_at on the reader's unread rows, mirroring the store's
//     same-transaction second statement.
type fakeTicketVisitStore struct {
	audits        map[int64][]fakeVisitEntry
	comments      map[int64][]fakeVisitEntry
	marks         map[int64]map[int64]application.VisitMark
	notifications []*domain.Notification
	nextAuditID   int64
	nextCommentID int64
	// returned is every mark the service asked for, in call order.
	returned []int64
	// recorded is every visit the service wrote, in call order.
	recorded []fakeVisitRecord
	// markErr, changedErr and recordErr make the matching call fail with the
	// exact error, so a test can prove the service never swallows one.
	markErr    error
	changedErr error
	recordErr  error
}

// fakeVisitRecord is one recorded visit as the port received it.
type fakeVisitRecord struct {
	userID   int64
	ticketID int64
	mark     application.VisitMark
	at       time.Time
}

var _ application.TicketVisitStore = (*fakeTicketVisitStore)(nil)

func newFakeTicketVisitStore() *fakeTicketVisitStore {
	return &fakeTicketVisitStore{
		audits:   map[int64][]fakeVisitEntry{},
		comments: map[int64][]fakeVisitEntry{},
		marks:    map[int64]map[int64]application.VisitMark{},
	}
}

// addAudit appends one audit event to the ticket's history and answers its id.
// actorID 0 means an automatic action.
func (f *fakeTicketVisitStore) addAudit(ticketID, actorID int64) int64 {
	f.nextAuditID++
	f.audits[ticketID] = append(f.audits[ticketID], fakeVisitEntry{id: f.nextAuditID, actorID: actorID})
	return f.nextAuditID
}

// addComment appends one comment to the ticket's history and answers its id.
func (f *fakeTicketVisitStore) addComment(ticketID, actorID int64, internal bool) int64 {
	f.nextCommentID++
	f.comments[ticketID] = append(f.comments[ticketID], fakeVisitEntry{id: f.nextCommentID, actorID: actorID, internal: internal})
	return f.nextCommentID
}

// addNotification appends one unread notification about the ticket.
func (f *fakeTicketVisitStore) addNotification(userID, ticketID int64) {
	f.notifications = append(f.notifications, &domain.Notification{UserID: userID, TicketID: ticketID})
}

func (f *fakeTicketVisitStore) Mark(_ context.Context, ticketID int64) (application.VisitMark, error) {
	f.returned = append(f.returned, ticketID)
	if f.markErr != nil {
		return application.VisitMark{}, f.markErr
	}
	var mark application.VisitMark
	for _, e := range f.audits[ticketID] {
		mark.AuditID = e.id
	}
	for _, e := range f.comments[ticketID] {
		mark.CommentID = e.id
	}
	return mark, nil
}

func (f *fakeTicketVisitStore) RecordVisit(_ context.Context, userID, ticketID int64, mark application.VisitMark, at time.Time) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.recorded = append(f.recorded, fakeVisitRecord{userID: userID, ticketID: ticketID, mark: mark, at: at})
	if f.marks[userID] == nil {
		f.marks[userID] = map[int64]application.VisitMark{}
	}
	f.marks[userID][ticketID] = mark
	for _, n := range f.notifications {
		if n.UserID == userID && n.TicketID == ticketID && n.ReadAt == nil {
			read := at
			n.ReadAt = &read
		}
	}
	return nil
}

func (f *fakeTicketVisitStore) Changed(_ context.Context, userID int64, ticketIDs []int64, includeInternal bool) (map[int64]bool, error) {
	if f.changedErr != nil {
		return nil, f.changedErr
	}
	out := map[int64]bool{}
	for _, ticketID := range ticketIDs {
		mark := f.marks[userID][ticketID]
		for _, e := range f.audits[ticketID] {
			if e.id > mark.AuditID && e.actorID != userID {
				out[ticketID] = true
			}
		}
		for _, e := range f.comments[ticketID] {
			if e.id <= mark.CommentID || e.actorID == userID {
				continue
			}
			if e.internal && !includeInternal {
				continue
			}
			out[ticketID] = true
		}
	}
	return out, nil
}
