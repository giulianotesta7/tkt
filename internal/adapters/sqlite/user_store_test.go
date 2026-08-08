package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.4: user store (UNIQUE email → Duplicate, delete guard →
// Referenced, inactive users readable) and session store (create, expired
// lookup → NotFound + lazy purge, logout delete).

func TestUserCreateAssignsIDAndPersists(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "hash",
		Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == 0 {
		t.Error("create did not assign an id")
	}

	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ana" || got.Email != "ana@example.com" || got.PasswordHash != "hash" {
		t.Errorf("user = %+v", got)
	}
	if !got.Active {
		t.Error("new user must be active by default")
	}
	if !got.CreatedAt.Equal(testClock) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, testClock)
	}
}

func TestUserCreateDuplicateEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.UserStore().Create(ctx, &domain.User{Name: "Ana", Email: "ana@example.com",
		PasswordHash: "h", Active: true, CreatedAt: testClock}); err != nil {
		t.Fatal(err)
	}
	err := s.UserStore().Create(ctx, &domain.User{Name: "Other", Email: "ana@example.com",
		PasswordHash: "h", Active: true, CreatedAt: testClock})
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	var de *domain.DuplicateError
	if !errors.As(err, &de) || de.Kind != "user" {
		t.Errorf("err = %v, want DuplicateError{Kind: user}", err)
	}
}

func TestUserUpdatePersistsFields(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "old",
		Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	u.Name = "Ana Maria"
	u.Email = "anamaria@example.com"
	u.PasswordHash = "new-hash"
	u.Active = false
	if err := s.UserStore().Update(ctx, u); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ana Maria" || got.Email != "anamaria@example.com" || got.PasswordHash != "new-hash" {
		t.Errorf("user = %+v", got)
	}
	if got.Active {
		t.Error("deactivation (active=false) not persisted")
	}
}

func TestUserUpdateDuplicateEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	ana := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	beto := &domain.User{Name: "Beto", Email: "beto@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, ana); err != nil {
		t.Fatal(err)
	}
	if err := s.UserStore().Create(ctx, beto); err != nil {
		t.Fatal(err)
	}

	// Renaming Beto to Ana's email must be rejected (spec: reject update
	// to duplicate email) — and Ana's row must keep its own email.
	beto.Email = "ana@example.com"
	err := s.UserStore().Update(ctx, beto)
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	got, err := s.UserStore().GetByID(ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "ana@example.com" {
		t.Errorf("ana email = %q, want unchanged", got.Email)
	}
}

func TestUserUpdateNotFound(t *testing.T) {
	s := newTestDB(t)
	err := s.UserStore().Update(context.Background(), &domain.User{ID: 42})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUserDeleteUnreferenced(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.UserStore().Delete(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.UserStore().GetByID(ctx, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

func TestUserDeleteReferencedByTicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	cat := seedCategory(t, s, "Bugs")
	userID := seedUser(t, s, "Ana", "ana@example.com", true)
	seedTicket(t, s, domain.Ticket{Number: 1, Title: "assigned", CategoryID: cat,
		UserID: ptr(userID), Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	err := s.UserStore().Delete(ctx, userID)
	if !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("err = %v, want ErrReferenced", err)
	}
	var re *domain.ReferencedError
	if !errors.As(err, &re) || re.Kind != "user" {
		t.Errorf("err = %v, want ReferencedError{Kind: user}", err)
	}
	// The referenced user must survive the blocked delete (deactivation is
	// the only removal path; historical assignment preserved).
	if _, err := s.UserStore().GetByID(ctx, userID); err != nil {
		t.Errorf("user deleted despite reference: %v", err)
	}
}

func TestUserDeleteRemovesSessions(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	sess := &domain.Session{ID: "tok", UserID: u.ID, ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := s.SessionStore().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// A user with sessions but no tickets is deletable; the sessions must
	// go with them (the sessions FK would otherwise block the delete).
	if err := s.UserStore().Delete(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sessions left behind after user delete: %d", n)
	}
}

func TestUserDeleteNotFound(t *testing.T) {
	s := newTestDB(t)
	err := s.UserStore().Delete(context.Background(), 42)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUserGetByIDIncludesInactive(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: false, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserStore().GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active {
		t.Error("inactive user must be readable (historical display)")
	}
}

func TestUserGetByEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	u := &domain.User{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, u); err != nil {
		t.Fatal(err)
	}

	got, err := s.UserStore().GetByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("id = %d, want %d", got.ID, u.ID)
	}

	_, err = s.UserStore().GetByEmail(ctx, "nobody@example.com")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown email = %v, want ErrNotFound", err)
	}
}

func TestUserCount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	n, err := s.UserStore().Count(ctx)
	if err != nil || n != 0 {
		t.Fatalf("empty count = %d, err = %v", n, err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := s.UserStore().Create(ctx, &domain.User{Name: email, Email: email,
			PasswordHash: "h", Active: true, CreatedAt: testClock}); err != nil {
			t.Fatal(err)
		}
	}
	n, err = s.UserStore().Count(ctx)
	if err != nil || n != 2 {
		t.Errorf("count = %d, err = %v; want 2", n, err)
	}
}

func TestUserListAndListActive(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	for _, u := range []domain.User{
		{Name: "Ana", Email: "ana@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock},
		{Name: "Bob", Email: "bob@example.com", PasswordHash: "h", Active: false, CreatedAt: testClock},
		{Name: "Cid", Email: "cid@example.com", PasswordHash: "h", Active: true, CreatedAt: testClock},
	} {
		u := u
		if err := s.UserStore().Create(ctx, &u); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.UserStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("list len = %d, want 3", len(all))
	}

	active, err := s.UserStore().ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Errorf("list active len = %d, want 2", len(active))
	}
	for _, u := range active {
		if !u.Active {
			t.Errorf("inactive user %s in ListActive", u.Email)
		}
	}
}
