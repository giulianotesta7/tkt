package application

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// fakeSettingsStore records the persisted internal-comment background color
// and fails on demand (settings service test double).
type fakeSettingsStore struct {
	bg  string
	err error
}

func (f *fakeSettingsStore) GetInternalCommentBg(_ context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.bg == "" {
		return DefaultInternalCommentBg, nil
	}
	return f.bg, nil
}

func (f *fakeSettingsStore) SetInternalCommentBg(_ context.Context, color string) error {
	if f.err != nil {
		return f.err
	}
	f.bg = color
	return nil
}

func adminActor() domain.User { return domain.User{ID: 1, Name: "Admin", Role: domain.RoleAdmin} }
func agentActor() domain.User { return domain.User{ID: 2, Name: "Agent", Role: domain.RoleAgent} }
func rootActor() domain.User  { return domain.User{ID: 3, Name: "Root", Role: domain.RoleRoot} }

func TestSettingsGetAppearance(t *testing.T) {
	st := &fakeSettingsStore{bg: "#EFE9FB"}
	svc := NewSettingsService(st)

	got, err := svc.GetAppearance(context.Background())
	if err != nil {
		t.Fatalf("get appearance: %v", err)
	}
	if got != "#EFE9FB" {
		t.Errorf("bg = %q, want %q", got, "#EFE9FB")
	}
}

func TestSettingsSetRejectsNonAdmin(t *testing.T) {
	st := &fakeSettingsStore{bg: "#E8EEFF"}
	svc := NewSettingsService(st)

	err := svc.SetInternalCommentBg(context.Background(), agentActor(), "#EFE9FB")
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}
	if st.bg != "#E8EEFF" {
		t.Errorf("store mutated by denied actor: %q", st.bg)
	}
}

func TestSettingsSetRejectsInvalidColor(t *testing.T) {
	st := &fakeSettingsStore{bg: "#E8EEFF"}
	svc := NewSettingsService(st)

	err := svc.SetInternalCommentBg(context.Background(), adminActor(), "#123456")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if validation.Field != "internal_comment_bg" {
		t.Errorf("validation field = %q, want %q", validation.Field, "internal_comment_bg")
	}
	if st.bg != "#E8EEFF" {
		t.Errorf("store changed to %q on invalid color", st.bg)
	}
}

func TestSettingsSetPersistsAllowedColors(t *testing.T) {
	for _, color := range AllowedInternalCommentBg() {
		t.Run(color, func(t *testing.T) {
			st := &fakeSettingsStore{}
			svc := NewSettingsService(st)

			if err := svc.SetInternalCommentBg(context.Background(), adminActor(), color); err != nil {
				t.Fatalf("set: %v", err)
			}
			if st.bg != color {
				t.Errorf("stored = %q, want %q", st.bg, color)
			}
		})
	}
}

func TestSettingsSetRootAllowed(t *testing.T) {
	st := &fakeSettingsStore{}
	svc := NewSettingsService(st)

	if err := svc.SetInternalCommentBg(context.Background(), rootActor(), "#EFE9FB"); err != nil {
		t.Fatalf("root set: %v", err)
	}
	if st.bg != "#EFE9FB" {
		t.Errorf("stored = %q, want %q", st.bg, "#EFE9FB")
	}
}
