package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.2: ticket store — atomic MAX+1 numbering in an immediate
// transaction (D8), update, reads, shared filter builder (D2/D11), and the
// TicketUnitOfWork atomicity contract (ticket write + audit appends roll
// back together).

func ptr[T any](v T) *T { return &v }

var testClock = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

// seedUser inserts a user directly (test arrange) and returns its id.
func seedUser(t *testing.T, s *Store, name, email string, active bool) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO users (name, email, password_hash, active, created_at) VALUES (?, ?, ?, ?, ?)`,
		name, email, "bcrypt-hash", active, "2026-08-06T10:00:00Z")
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user %s: last insert id: %v", email, err)
	}
	return id
}

// seedTicket inserts a ticket directly with explicit identity and
// timestamps (test arrange) and returns the stored ticket with its id.
func seedTicket(t *testing.T, s *Store, tk domain.Ticket) domain.Ticket {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, user_id, created_at, updated_at, resolved_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tk.Number, tk.Title, tk.Description, tk.RequesterName, tk.RequesterEmail,
		tk.CategoryID, string(tk.Priority), string(tk.State), nullableInt64(tk.UserID),
		tk.CreatedAt.Format(time.RFC3339), tk.UpdatedAt.Format(time.RFC3339),
		formatTimePtr(tk.ResolvedAt), formatTimePtr(tk.ClosedAt))
	if err != nil {
		t.Fatalf("seed ticket %d: %v", tk.Number, err)
	}
	tk.ID, err = res.LastInsertId()
	if err != nil {
		t.Fatalf("seed ticket %d: last insert id: %v", tk.Number, err)
	}
	return tk
}

func TestTicketCreateSequentialNumbers(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	// Acceptance: an existing ticket numbered 1042; the next creation must
	// be 1043, then 1044.
	seedTicket(t, s, domain.Ticket{Number: 1042, Title: "existing", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	first := &domain.Ticket{Title: "one", CategoryID: cat, Priority: domain.PriorityHigh,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketStore().Create(ctx, first); err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.Number != 1043 {
		t.Errorf("first number = %d, want 1043", first.Number)
	}
	if first.ID == 0 {
		t.Error("create did not assign an id")
	}

	second := &domain.Ticket{Title: "two", CategoryID: cat, Priority: domain.PriorityLow,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketStore().Create(ctx, second); err != nil {
		t.Fatalf("create: %v", err)
	}
	if second.Number != 1044 {
		t.Errorf("second number = %d, want 1044", second.Number)
	}
}

func TestTicketCreateConcurrentDistinctNumbers(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	// Acceptance: two concurrent creations must produce distinct numbers.
	// _txlock=immediate serializes the writers; MAX+1 stays race-free.
	const workers, perWorker = 2, 4
	var wg sync.WaitGroup
	numbers := make(chan int, workers*perWorker)
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				tk := &domain.Ticket{Title: fmt.Sprintf("t%d-%d", w, i), CategoryID: cat,
					Priority: domain.PriorityMedium, State: domain.StateNew,
					CreatedAt: testClock, UpdatedAt: testClock}
				if err := s.TicketStore().Create(ctx, tk); err != nil {
					errs <- fmt.Errorf("goroutine %d create: %w", w, err)
					return
				}
				numbers <- tk.Number
			}
		}()
	}
	wg.Wait()
	close(numbers)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	seen := map[int]bool{}
	for n := range numbers {
		if seen[n] {
			t.Fatalf("duplicate number %d assigned to two tickets", n)
		}
		seen[n] = true
	}
	if len(seen) != workers*perWorker {
		t.Errorf("distinct numbers = %d, want %d", len(seen), workers*perWorker)
	}
}

func TestTicketCreateRejectsUnknownCategory(t *testing.T) {
	s := newTestDB(t)
	tk := &domain.Ticket{Title: "bad", CategoryID: 999, Priority: domain.PriorityMedium,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	err := s.TicketStore().Create(context.Background(), tk)
	if err == nil {
		t.Fatal("create with unknown category succeeded, want FK error")
	}
	if !isForeignKeyViolation(err) {
		t.Errorf("err = %v, want foreign key violation", err)
	}
}

func TestTicketCreateRejectsUnknownUser(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	tk := &domain.Ticket{Title: "bad user", CategoryID: cat, UserID: ptr[int64](999),
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}
	err := s.TicketStore().Create(context.Background(), tk)
	if err == nil {
		t.Fatal("create with unknown user succeeded, want FK error")
	}
	if !isForeignKeyViolation(err) {
		t.Errorf("err = %v, want foreign key violation", err)
	}
}

func TestTicketUpdatePersistsFieldsAndState(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	tk := &domain.Ticket{Title: "before", Description: "d", RequesterName: "r",
		RequesterEmail: "e", CategoryID: cat, Priority: domain.PriorityMedium,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketStore().Create(ctx, tk); err != nil {
		t.Fatalf("create: %v", err)
	}

	now2 := testClock.Add(2 * time.Hour)
	tk.Title = "after"
	tk.Description = "d2"
	tk.Priority = domain.PriorityCritical
	tk.State = domain.StateInProgress
	tk.UpdatedAt = now2
	if err := s.TicketStore().Update(ctx, tk); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.TicketStore().GetByID(ctx, tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "after" || got.Description != "d2" {
		t.Errorf("fields not updated: %+v", got)
	}
	if got.Priority != domain.PriorityCritical || got.State != domain.StateInProgress {
		t.Errorf("priority/state not updated: %s/%s", got.Priority, got.State)
	}
	if !got.UpdatedAt.Equal(now2) {
		t.Errorf("updated_at = %v, want %v", got.UpdatedAt, now2)
	}
}

func TestTicketUpdateNotFound(t *testing.T) {
	s := newTestDB(t)
	err := s.TicketStore().Update(context.Background(), &domain.Ticket{ID: 42})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	var nfe *domain.NotFoundError
	if !errors.As(err, &nfe) || nfe.Kind != "ticket" {
		t.Errorf("err = %v, want NotFoundError{Kind: ticket}", err)
	}
}

func TestTicketGetByIDRoundTrip(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	userID := seedUser(t, s, "Ana", "ana@example.com", true)
	ctx := context.Background()

	resolved := testClock.Add(3 * time.Hour)
	tk := &domain.Ticket{Title: "t", Description: "desc", RequesterName: "r",
		RequesterEmail: "e", CategoryID: cat, UserID: ptr(userID),
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock.Add(time.Hour), ResolvedAt: &resolved}
	if err := s.TicketStore().Create(ctx, tk); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.TicketStore().GetByID(ctx, tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != tk.Title || got.Description != tk.Description {
		t.Errorf("text fields: %+v", got)
	}
	if got.CategoryID != cat || got.UserID == nil || *got.UserID != userID {
		t.Errorf("refs: %+v", got)
	}
	if got.State != domain.StateResolved || !got.ResolvedAt.Equal(resolved) {
		t.Errorf("state/lifecycle: %+v", got)
	}
	if !got.CreatedAt.Equal(testClock) || !got.UpdatedAt.Equal(testClock.Add(time.Hour)) {
		t.Errorf("timestamps: %+v", got)
	}
	if got.ClosedAt != nil {
		t.Errorf("closed_at = %v, want nil", *got.ClosedAt)
	}
}

func TestTicketGetByIDUnassignedAndNilTimestamps(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 7, Title: "unassigned", CategoryID: cat,
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	got, err := s.TicketStore().GetByID(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UserID != nil {
		t.Errorf("user_id = %v, want nil", *got.UserID)
	}
	if got.ResolvedAt != nil || got.ClosedAt != nil {
		t.Error("lifecycle timestamps must be nil for a new ticket")
	}
}

func TestTicketGetByIDNotFound(t *testing.T) {
	s := newTestDB(t)
	_, err := s.TicketStore().GetByID(context.Background(), 42)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestTicketListOrderNewestFirstWithIDTiebreak(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	older := seedTicket(t, s, domain.Ticket{Number: 1, Title: "older", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})
	// Same created_at as older → the later id must come first (tiebreak).
	newer := seedTicket(t, s, domain.Ticket{Number: 2, Title: "newer", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})
	mid := seedTicket(t, s, domain.Ticket{Number: 3, Title: "mid", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock.Add(-time.Hour), UpdatedAt: testClock})

	got, err := s.TicketStore().List(ctx, application.TicketQuery{}, application.Page{Offset: 0, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := []int64{got[0].ID, got[1].ID, got[2].ID}
	want := []int64{newer.ID, older.ID, mid.ID}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("order[%d] = %d, want %d (created_at DESC, id DESC)", i, ids[i], want[i])
		}
	}
}

func TestTicketListFiltersComposeWithAND(t *testing.T) {
	s := newTestDB(t)
	bugs := seedCategory(t, s, "Bugs")
	support := seedCategory(t, s, "Support")
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)
	ctx := context.Background()

	seedTicket(t, s, domain.Ticket{Number: 1, Title: "t1", CategoryID: bugs, UserID: ptr(ana),
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 2, Title: "t2", CategoryID: bugs, UserID: ptr(bob),
		Priority: domain.PriorityHigh, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 3, Title: "t3", CategoryID: support, UserID: ptr(ana),
		Priority: domain.PriorityLow, State: domain.StateCancelled,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 4, Title: "t4", CategoryID: support,
		Priority: domain.PriorityLow, State: domain.StateCancelled,
		CreatedAt: testClock, UpdatedAt: testClock})

	t.Run("empty filter returns all", func(t *testing.T) {
		got, err := s.TicketStore().List(ctx, application.TicketQuery{}, application.Page{Offset: 0, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 {
			t.Errorf("len = %d, want 4", len(got))
		}
	})

	t.Run("state filter", func(t *testing.T) {
		got, err := s.TicketStore().List(ctx, application.TicketQuery{State: ptr(domain.StateCancelled)}, application.Page{Offset: 0, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("len = %d, want 2", len(got))
		}
	})

	t.Run("priority filter", func(t *testing.T) {
		got, err := s.TicketStore().List(ctx, application.TicketQuery{Priority: ptr(domain.PriorityHigh)}, application.Page{Offset: 0, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("len = %d, want 2", len(got))
		}
	})

	t.Run("category filter", func(t *testing.T) {
		got, err := s.TicketStore().List(ctx, application.TicketQuery{CategoryID: ptr(support)}, application.Page{Offset: 0, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("len = %d, want 2", len(got))
		}
	})

	t.Run("user filter", func(t *testing.T) {
		got, err := s.TicketStore().List(ctx, application.TicketQuery{UserID: ptr(bob)}, application.Page{Offset: 0, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Title != "t2" {
			t.Errorf("user filter: %+v", got)
		}
	})

	t.Run("four filters AND", func(t *testing.T) {
		got, err := s.TicketStore().List(ctx, application.TicketQuery{
			State: ptr(domain.StateResolved), Priority: ptr(domain.PriorityHigh),
			CategoryID: ptr(bugs), UserID: ptr(ana),
		}, application.Page{Offset: 0, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Title != "t1" {
			t.Errorf("AND composition: %+v", got)
		}
	})
}

func TestTicketListPaginationNoOverlap(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	// 25 tickets, distinct created_at one minute apart (newest = highest
	// number). Pages must be 10/10/5 with no overlap (spec: stable
	// pagination).
	for i := 1; i <= 25; i++ {
		seedTicket(t, s, domain.Ticket{Number: i, Title: fmt.Sprintf("t%02d", i), CategoryID: cat,
			Priority: domain.PriorityMedium, State: domain.StateNew,
			CreatedAt: testClock.Add(time.Duration(i) * time.Minute),
			UpdatedAt: testClock.Add(time.Duration(i) * time.Minute)})
	}

	page := func(offset int) []domain.Ticket {
		t.Helper()
		got, err := s.TicketStore().List(ctx, application.TicketQuery{}, application.Page{Offset: offset, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	p1, p2, p3 := page(0), page(10), page(20)
	if len(p1) != 10 || len(p2) != 10 || len(p3) != 5 {
		t.Fatalf("page sizes = %d/%d/%d, want 10/10/5", len(p1), len(p2), len(p3))
	}

	seen := map[int64]bool{}
	for _, tk := range append(append(p1, p2...), p3...) {
		if seen[tk.ID] {
			t.Fatalf("ticket %d appears on two pages", tk.ID)
		}
		seen[tk.ID] = true
	}
	if len(seen) != 25 {
		t.Errorf("unique tickets across pages = %d, want 25", len(seen))
	}
	// Newest first: page 1 opens with ticket 25.
	if p1[0].Number != 25 {
		t.Errorf("p1[0].number = %d, want 25 (newest first)", p1[0].Number)
	}
}

func TestTicketCountRespectsFilters(t *testing.T) {
	s := newTestDB(t)
	bugs := seedCategory(t, s, "Bugs")
	support := seedCategory(t, s, "Support")
	ctx := context.Background()

	seedTicket(t, s, domain.Ticket{Number: 1, Title: "a", CategoryID: bugs,
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 2, Title: "b", CategoryID: bugs,
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 3, Title: "c", CategoryID: support,
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	total, err := s.TicketStore().Count(ctx, application.TicketQuery{})
	if err != nil || total != 3 {
		t.Errorf("total = %d, err = %v; want 3", total, err)
	}
	low, err := s.TicketStore().Count(ctx, application.TicketQuery{Priority: ptr(domain.PriorityLow)})
	if err != nil || low != 2 {
		t.Errorf("low = %d, err = %v; want 2", low, err)
	}
	bugsNew, err := s.TicketStore().Count(ctx, application.TicketQuery{
		CategoryID: ptr(bugs), State: ptr(domain.StateNew)})
	if err != nil || bugsNew != 1 {
		t.Errorf("bugs+new = %d, err = %v; want 1", bugsNew, err)
	}
}

func TestTicketCountsByStateReflectFilteredSet(t *testing.T) {
	s := newTestDB(t)
	bugs := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	// 3 resolved (2 high, 1 low) + 2 cancelled + 1 new.
	seedTicket(t, s, domain.Ticket{Number: 1, Title: "a", CategoryID: bugs,
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 2, Title: "b", CategoryID: bugs,
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 3, Title: "c", CategoryID: bugs,
		Priority: domain.PriorityLow, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 4, Title: "d", CategoryID: bugs,
		Priority: domain.PriorityMedium, State: domain.StateCancelled,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 5, Title: "e", CategoryID: bugs,
		Priority: domain.PriorityMedium, State: domain.StateCancelled,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 6, Title: "f", CategoryID: bugs,
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	// Filter by priority high: chips must reflect the FILTERED set (2
	// resolved), not the whole table (spec: chips reflect result set).
	byState, err := s.TicketStore().CountsByState(ctx, application.TicketQuery{Priority: ptr(domain.PriorityHigh)})
	if err != nil {
		t.Fatal(err)
	}
	if len(byState) != 1 || byState[domain.StateResolved] != 2 {
		t.Errorf("chips by state under priority=high: %v, want {resolved: 2}", byState)
	}

	all, err := s.TicketStore().CountsByState(ctx, application.TicketQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if all[domain.StateResolved] != 3 || all[domain.StateCancelled] != 2 || all[domain.StateNew] != 1 {
		t.Errorf("chips by state (all): %v", all)
	}
}

func TestTicketCountsByPriorityReflectFilteredSet(t *testing.T) {
	s := newTestDB(t)
	bugs := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	seedTicket(t, s, domain.Ticket{Number: 1, Title: "a", CategoryID: bugs,
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 2, Title: "b", CategoryID: bugs,
		Priority: domain.PriorityHigh, State: domain.StateResolved,
		CreatedAt: testClock, UpdatedAt: testClock})
	seedTicket(t, s, domain.Ticket{Number: 3, Title: "c", CategoryID: bugs,
		Priority: domain.PriorityLow, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	byPriority, err := s.TicketStore().CountsByPriority(ctx, application.TicketQuery{State: ptr(domain.StateResolved)})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPriority) != 1 || byPriority[domain.PriorityHigh] != 2 {
		t.Errorf("chips by priority under state=resolved: %v, want {high: 2}", byPriority)
	}

	all, err := s.TicketStore().CountsByPriority(ctx, application.TicketQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if all[domain.PriorityHigh] != 2 || all[domain.PriorityLow] != 1 {
		t.Errorf("chips by priority (all): %v", all)
	}
}

// --- TicketUnitOfWork (C1 atomicity: ticket write + audit appends) ---

func TestUnitOfWorkCreatePersistsTicketAndEvent(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	tk := &domain.Ticket{Title: "unit", CategoryID: cat, Priority: domain.PriorityHigh,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	event := domain.AuditEvent{Actor: "Ana", Action: domain.ActionCreated, CreatedAt: testClock}

	uow := s.TicketUnitOfWork()
	if err := uow.Create(ctx, tk, event); err != nil {
		t.Fatalf("uow create: %v", err)
	}
	if tk.ID == 0 || tk.Number == 0 {
		t.Fatalf("uow create did not assign id/number: %+v", tk)
	}

	got, err := s.TicketStore().GetByID(ctx, tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "unit" {
		t.Errorf("ticket not persisted: %+v", got)
	}

	// The audit event must carry the store-assigned ticket id (port
	// contract: the implementation stamps event.TicketID).
	var eventTicketID, eventActor, eventAction string
	var eventID int64
	if err := s.db.QueryRow(
		`SELECT id, ticket_id, actor, action FROM audit_events`,
	).Scan(&eventID, &eventTicketID, &eventActor, &eventAction); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if eventID == 0 || eventTicketID != fmt.Sprintf("%d", tk.ID) {
		t.Errorf("audit event ticket_id = %s, want %d", eventTicketID, tk.ID)
	}
	if eventActor != "Ana" || eventAction != domain.ActionCreated {
		t.Errorf("audit event = %s/%s, want Ana/created", eventActor, eventAction)
	}
}

// injectAuditFailure aborts every audit insert (test-only trigger). The
// unit-of-work must roll the ticket write back when the audit append fails
// (no-silent-mutations contract, C1).
func injectAuditFailure(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`CREATE TRIGGER trg_test_fail_audit BEFORE INSERT ON audit_events
		 BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`); err != nil {
		t.Fatalf("inject audit failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER trg_test_fail_audit`); err != nil {
			t.Errorf("drop audit failure trigger: %v", err)
		}
	})
}

func TestUnitOfWorkCreateRollsBackOnAuditFailure(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()
	injectAuditFailure(t, s)

	tk := &domain.Ticket{Title: "doomed", CategoryID: cat, Priority: domain.PriorityMedium,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	err := s.TicketUnitOfWork().Create(ctx, tk, domain.AuditEvent{Actor: "Ana", Action: domain.ActionCreated, CreatedAt: testClock})
	if err == nil {
		t.Fatal("uow create succeeded, want audit append failure")
	}

	var tickets, events int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&tickets); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if tickets != 0 {
		t.Errorf("ticket persisted despite failed audit append (%d rows) — mutation must roll back", tickets)
	}
	if events != 0 {
		t.Errorf("audit rows = %d, want 0", events)
	}
}

func TestUnitOfWorkUpdatePersistsEventBatchInOrder(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	tk := &domain.Ticket{Title: "t", CategoryID: cat, Priority: domain.PriorityMedium,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketStore().Create(ctx, tk); err != nil {
		t.Fatalf("create: %v", err)
	}

	// One transition + two field edits, one batch (audit-log: events in
	// occurrence order).
	tk.State = domain.StateInProgress
	tk.UpdatedAt = testClock.Add(time.Hour)
	events := []domain.AuditEvent{
		{TicketID: tk.ID, Actor: "Ana", Action: domain.ActionTransition,
			Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: testClock.Add(time.Hour)},
		{TicketID: tk.ID, Actor: "Ana", Action: domain.ActionUpdate,
			Field: ptr("title"), FromValue: ptr("t"), ToValue: ptr("t2"), CreatedAt: testClock.Add(2 * time.Hour)},
		{TicketID: tk.ID, Actor: "Bob", Action: domain.ActionUpdate,
			Field: ptr("priority"), FromValue: ptr("medium"), ToValue: ptr("high"), CreatedAt: testClock.Add(3 * time.Hour)},
	}
	if err := s.TicketUnitOfWork().Update(ctx, tk, events...); err != nil {
		t.Fatalf("uow update: %v", err)
	}

	got, err := s.TicketStore().GetByID(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.StateInProgress {
		t.Errorf("state = %s, want in_progress", got.State)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT actor, action, field, from_value, to_value FROM audit_events WHERE ticket_id = ? ORDER BY id`, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var actor, action, field, from, to string
		if err := rows.Scan(&actor, &action, &field, &from, &to); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action+":"+field)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"transition:state", "update:title", "update:priority"}
	for i := range want {
		if actions[i] != want[i] {
			t.Errorf("event[%d] = %s, want %s (occurrence order)", i, actions[i], want[i])
		}
	}
	if len(actions) != 3 {
		t.Errorf("events = %d, want 3", len(actions))
	}
}

func TestUnitOfWorkUpdateRollsBackOnAuditFailure(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	ctx := context.Background()

	tk := &domain.Ticket{Title: "before", CategoryID: cat, Priority: domain.PriorityMedium,
		State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}
	if err := s.TicketStore().Create(ctx, tk); err != nil {
		t.Fatalf("create: %v", err)
	}
	injectAuditFailure(t, s)

	tk.Title = "after"
	tk.State = domain.StateResolved
	err := s.TicketUnitOfWork().Update(ctx, tk, domain.AuditEvent{
		TicketID: tk.ID, Actor: "Ana", Action: domain.ActionTransition,
		Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: testClock.Add(time.Hour)})
	if err == nil {
		t.Fatal("uow update succeeded, want audit append failure")
	}

	// The ticket must be restored to its pre-mutation values.
	got, err := s.TicketStore().GetByID(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "before" || got.State != domain.StateNew {
		t.Errorf("ticket not rolled back: %s/%s", got.Title, got.State)
	}
	var events int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Errorf("audit rows = %d, want 0", events)
	}
}

func TestUnitOfWorkUpdateNotFound(t *testing.T) {
	s := newTestDB(t)
	err := s.TicketUnitOfWork().Update(context.Background(), &domain.Ticket{ID: 42})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- UNIQUE retry backstop (D8 belt-and-suspenders) ---

// uniqueNumberViolation produces a REAL modernc *sqlite.Error by colliding
// with the tickets.number UNIQUE constraint (the error type's fields are
// unexported, so tests cannot fabricate one).
func uniqueNumberViolation(t *testing.T, s *Store, cat int64) error {
	t.Helper()
	insert := func(number int) error {
		_, err := s.db.ExecContext(context.Background(),
			`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
			 VALUES (?, 'x', '', 'r', 'e', ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
			number, cat)
		return err
	}
	if err := insert(1); err != nil {
		t.Fatalf("baseline insert: %v", err)
	}
	err := insert(1)
	if err == nil || !isUniqueViolation(err) {
		t.Fatalf("second insert = %v, want unique violation", err)
	}
	return err
}

func TestRetryUniqueRetriesOnNumberViolation(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	uniqueErr := uniqueNumberViolation(t, s, cat)

	var calls int
	err := retryUnique(3, func() error {
		calls++
		if calls < 3 {
			return uniqueErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryUnique = %v, want nil after third attempt", err)
	}
	if calls != 3 {
		t.Errorf("fn called %d times, want 3", calls)
	}
}

func TestRetryUniqueStopsOnNonUniqueError(t *testing.T) {
	var calls int
	want := errors.New("disk full")
	err := retryUnique(3, func() error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (no retry on non-unique error)", calls)
	}
}

func TestRetryUniqueExhaustsAttempts(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "Bugs")
	uniqueErr := uniqueNumberViolation(t, s, cat)

	var calls int
	err := retryUnique(3, func() error {
		calls++
		return uniqueErr
	})
	if err == nil {
		t.Fatal("retryUnique = nil, want error after exhausting attempts")
	}
	if calls != 3 {
		t.Errorf("fn called %d times, want 3", calls)
	}
}
