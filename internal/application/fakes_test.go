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
// simulation (C1): the ticket write and the audit appends either both
// persist or both roll back. failAuditAppend makes the audit part of the
// NEXT mutation fail, letting tests prove the rollback half of the
// atomicity contract (no-silent-mutations spec).
type fakeUnitOfWork struct {
	tickets         *fakeTicketStore
	audits          *fakeAuditStore
	failAuditAppend bool
}

func newFakeUnitOfWork(tickets *fakeTicketStore, audits *fakeAuditStore) *fakeUnitOfWork {
	return &fakeUnitOfWork{tickets: tickets, audits: audits}
}

// Create persists the ticket (store-assigned ID and number, D8) and its
// created event as one unit: a failing audit append rolls the ticket back.
func (f *fakeUnitOfWork) Create(ctx context.Context, t *domain.Ticket, event domain.AuditEvent) error {
	if err := f.tickets.Create(ctx, t); err != nil {
		return err
	}
	if f.failAuditAppend {
		delete(f.tickets.tickets, t.ID)
		return errAuditAppendFailed
	}
	event.TicketID = t.ID
	return f.audits.Append(ctx, event)
}

// Update persists the ticket and its event batch as one unit: a failing
// audit append restores the pre-mutation ticket copy. events may be empty
// (a plain ticket write is still atomic by construction).
func (f *fakeUnitOfWork) Update(ctx context.Context, t *domain.Ticket, events ...domain.AuditEvent) error {
	prev, ok := f.tickets.tickets[t.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "ticket", ID: t.ID}
	}
	if err := f.tickets.Update(ctx, t); err != nil {
		return err
	}
	if f.failAuditAppend {
		f.tickets.tickets[t.ID] = prev
		return errAuditAppendFailed
	}
	if len(events) > 0 {
		return f.audits.Append(ctx, events...)
	}
	return nil
}

// fakeUserStore implements UserStore: email uniqueness, delete guard via a
// referenced flag (the real store checks ticket FKs; tests set the flag).
type fakeUserStore struct {
	users       map[int64]*domain.User
	byEmail     map[string]int64
	nextID      int64
	referenced  map[int64]bool
	roleChanges []fakeRoleChange
}

type fakeRoleChange struct{ actorID int64 }

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{
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
	u, ok := f.users[updated.ID]
	if !ok {
		return &domain.NotFoundError{Kind: "user", ID: updated.ID}
	}
	if u.Role != expectedRole {
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
	published *domain.WorkflowDefinition
}

func newFakeWorkflowStore() *fakeWorkflowStore { return &fakeWorkflowStore{drafts: map[int64][]byte{}} }
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
func (f *fakeWorkflowStore) Publish(_ context.Context, categoryID int64, draft []byte, by *int64) (int64, []domain.WorkflowValidationIssue, error) {
	def, err := domain.ParseWorkflowDefinition(draft)
	if err != nil {
		return 0, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, nil
	}
	if iss := def.Validate(); len(iss) > 0 {
		return 0, iss, nil
	}
	cp := make([]byte, len(draft))
	copy(cp, draft)
	f.publishCalls = append(f.publishCalls, struct {
		cat   int64
		draft []byte
		by    *int64
	}{cat: categoryID, draft: cp, by: by})
	f.drafts[categoryID] = cp
	c := def
	f.published = &c
	return int64(len(f.publishCalls)), nil, nil
}
func (f *fakeWorkflowStore) ListSummaries(_ context.Context) ([]application.WorkflowSummary, error) {
	return []application.WorkflowSummary{}, nil
}
func (f *fakeWorkflowStore) ListAvailableCategories(_ context.Context) ([]domain.Category, error) {
	return []domain.Category{}, nil
}
