package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Sign-in page use cases (issue #383, S9/S10): the read never fails for
// configuration reasons and answers the documented defaults, every write is
// gated on CapManageSettings, validation runs BEFORE the store is reached, and
// one submission is one write carrying the panel and its background image.

func signinActor(role domain.Role) domain.User {
	return domain.User{ID: 43, Name: "Actor", Email: "actor@tkt.test", Role: role, Active: true}
}

func newSigninServiceFixture(t *testing.T) (*application.SigninService, *fakeSigninStore) {
	t.Helper()
	store := newFakeSigninStore()
	return application.NewSigninService(store), store
}

func TestSigninDefaultsWhenNothingWasEverSaved(t *testing.T) {
	svc, _ := newSigninServiceFixture(t)

	got, err := svc.GetSigninConfiguration(context.Background())
	if err != nil {
		t.Fatalf("GetSigninConfiguration: %v", err)
	}
	if got.Panel.Content != domain.SigninContentSteps || got.Panel.Background != domain.SigninBackgroundDark {
		t.Errorf("panel = %+v, want steps on the dark background", got.Panel)
	}
	if got.Panel.NoticeEnabled || len(got.Panel.Highlights) != 0 || got.HasBackground() {
		t.Errorf("configuration = %+v, want no notice, no highlights and no image", got)
	}
}

func TestSigninUpdatePersistsEveryField(t *testing.T) {
	svc, store := newSigninServiceFixture(t)

	err := svc.UpdateSignin(context.Background(), signinActor(domain.RoleAdmin), application.SigninInput{Panel: domain.SigninPanel{
		Title:    "  Sistemas, a un pedido  ",
		Subtitle: "  Cargá tu pedido  ",
		Content:  domain.SigninContentHighlights,
		Highlights: []domain.SigninHighlight{
			{Icon: domain.SigninIconClock, Title: " Four hours ", Text: " Or less "},
		},
		NoticeEnabled: true,
		NoticeKind:    domain.SigninNoticeWarning,
		NoticeTitle:   " Maintenance ",
		NoticeText:    " Saturday 10-12 ",
		Background:    domain.SigninBackgroundBrand,
		BrandColor:    "#c8102e",
		SupportPhone:  " 4400 ",
		SupportHours:  " Mon-Fri ",
	}})
	if err != nil {
		t.Fatalf("UpdateSignin: %v", err)
	}
	if store.setCalls != 1 {
		t.Fatalf("store writes = %d, want exactly one write per submission", store.setCalls)
	}
	panel := store.panel
	if panel.Title != "Sistemas, a un pedido" || panel.Subtitle != "Cargá tu pedido" {
		t.Errorf("panel = %+v, want the trimmed text", panel)
	}
	if len(panel.Highlights) != 1 || panel.Highlights[0].Title != "Four hours" {
		t.Errorf("highlights = %+v, want the trimmed row", panel.Highlights)
	}
	if !panel.NoticeEnabled || panel.NoticeTitle != "Maintenance" || panel.NoticeText != "Saturday 10-12" {
		t.Errorf("notice = %+v, want the trimmed notice", panel)
	}
	if panel.BrandColor != "#C8102E" {
		t.Errorf("brand color = %q, want it upper-cased", panel.BrandColor)
	}
	if panel.SupportPhone != "4400" || panel.SupportHours != "Mon-Fri" {
		t.Errorf("contact = %q / %q, want the trimmed values", panel.SupportPhone, panel.SupportHours)
	}
}

func TestSigninUpdateStoresTheBackgroundImageInTheSameWrite(t *testing.T) {
	svc, store := newSigninServiceFixture(t)
	image := signinImageBytes(domain.SigninBackgroundPNG)

	if err := svc.UpdateSignin(context.Background(), signinActor(domain.RoleAdmin), application.SigninInput{
		Panel:      domain.SigninPanel{Title: "Bienvenido", Background: domain.SigninBackgroundImage},
		Background: image,
	}); err != nil {
		t.Fatalf("UpdateSignin: %v", err)
	}
	if store.setCalls != 1 {
		t.Errorf("store writes = %d, want one write carrying the panel and the image", store.setCalls)
	}
	if len(store.background) != len(image) {
		t.Errorf("stored image = %d bytes, want %d", len(store.background), len(image))
	}

	// A later save with no file keeps the image: only an explicit removal drops
	// it.
	if err := svc.UpdateSignin(context.Background(), signinActor(domain.RoleAdmin), application.SigninInput{
		Panel: domain.SigninPanel{Title: "Changed", Background: domain.SigninBackgroundImage},
	}); err != nil {
		t.Fatalf("UpdateSignin without a file: %v", err)
	}
	if len(store.background) == 0 {
		t.Error("a submission with no file dropped the stored image")
	}
}

func TestSigninRemoveBackgroundResetsTheStyle(t *testing.T) {
	svc, store := newSigninServiceFixture(t)
	if err := svc.UpdateSignin(context.Background(), signinActor(domain.RoleRoot), application.SigninInput{
		Panel:      domain.SigninPanel{Title: "Bienvenido", Background: domain.SigninBackgroundImage},
		Background: signinImageBytes(domain.SigninBackgroundJPEG),
	}); err != nil {
		t.Fatalf("UpdateSignin: %v", err)
	}

	if err := svc.RemoveBackground(context.Background(), signinActor(domain.RoleRoot)); err != nil {
		t.Fatalf("RemoveBackground: %v", err)
	}
	if len(store.background) != 0 {
		t.Errorf("background survived the removal: %d bytes", len(store.background))
	}
	if store.panel.Background != domain.SigninBackgroundDark {
		t.Errorf("background style = %q, want it back on dark", store.panel.Background)
	}
	if store.panel.Title != "Bienvenido" {
		t.Errorf("removal changed the title: %q", store.panel.Title)
	}
	if err := svc.RemoveBackground(context.Background(), signinActor(domain.RoleRoot)); err != nil {
		t.Errorf("removing an absent image = %v, want nil", err)
	}
}

func TestSigninUpdateRejectionsStoreNothing(t *testing.T) {
	cases := []struct {
		name string
		in   application.SigninInput
		key  string
	}{
		{"title too long", application.SigninInput{Panel: domain.SigninPanel{Title: longString(domain.MaxSigninTitleRunes + 1)}}, "error.signin_title_invalid"},
		{"unknown icon", application.SigninInput{Panel: domain.SigninPanel{Content: domain.SigninContentHighlights, Highlights: []domain.SigninHighlight{{Icon: "rocket", Title: "x"}}}}, "error.signin_highlight_invalid"},
		{"notice without a title", application.SigninInput{Panel: domain.SigninPanel{NoticeEnabled: true}}, "error.signin_notice_title_required"},
		{"brand background without a colour", application.SigninInput{Panel: domain.SigninPanel{Background: domain.SigninBackgroundBrand}}, "error.signin_brand_color_invalid"},
		{"image background without an image", application.SigninInput{Panel: domain.SigninPanel{Background: domain.SigninBackgroundImage}}, "error.signin_image_required"},
		{"non-image upload", application.SigninInput{Panel: domain.SigninPanel{}, Background: []byte("not an image")}, "error.signin_image_invalid"},
		{"oversize upload", application.SigninInput{Panel: domain.SigninPanel{}, Background: signinImageBytes(domain.SigninBackgroundPNG, domain.MaxSigninBackgroundBytes+1)}, "error.signin_image_too_large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newSigninServiceFixture(t)
			err := svc.UpdateSignin(context.Background(), signinActor(domain.RoleAdmin), tc.in)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if validation.Key != tc.key {
				t.Errorf("key = %q, want %q", validation.Key, tc.key)
			}
			if store.setCalls != 0 {
				t.Errorf("store writes = %d, want zero: a rejected submission changes nothing", store.setCalls)
			}
		})
	}
}

func TestSigninWritesAreGatedOnCapManageSettings(t *testing.T) {
	cases := []struct {
		name   string
		actor  domain.User
		permit bool
	}{
		{"admin", signinActor(domain.RoleAdmin), true},
		{"root", signinActor(domain.RoleRoot), true},
		{"agent", signinActor(domain.RoleAgent), false},
		{"user", signinActor(domain.RoleUser), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newSigninServiceFixture(t)
			for _, write := range []func() error{
				func() error {
					return svc.UpdateSignin(context.Background(), tc.actor, application.SigninInput{Panel: domain.SigninPanel{Title: "Hola"}})
				},
				func() error { return svc.RemoveBackground(context.Background(), tc.actor) },
			} {
				err := write()
				if tc.permit {
					if err != nil {
						t.Fatalf("permitted actor refused: %v", err)
					}
					continue
				}
				var forbidden *domain.ForbiddenError
				if !errors.As(err, &forbidden) {
					t.Fatalf("err = %v, want a ForbiddenError", err)
				}
			}
			if !tc.permit && store.setCalls != 0 {
				t.Errorf("store writes = %d, want zero for a denied actor", store.setCalls)
			}
		})
	}
}

// TestSigninReadIsNotGated proves the read the login page performs: it renders
// before any session exists, so it answers for every caller.
func TestSigninReadIsNotGated(t *testing.T) {
	svc, store := newSigninServiceFixture(t)
	store.panel = domain.SigninPanel{Title: "Bienvenido", Content: domain.SigninContentNone}

	got, err := svc.GetSigninConfiguration(context.Background())
	if err != nil {
		t.Fatalf("GetSigninConfiguration: %v", err)
	}
	if got.Panel.Title != "Bienvenido" {
		t.Errorf("title = %q, want the stored one", got.Panel.Title)
	}
}

func TestSigninStoreFailuresPropagate(t *testing.T) {
	svc, store := newSigninServiceFixture(t)
	boom := errors.New("store down")
	store.getErr = boom
	if _, err := svc.GetSigninConfiguration(context.Background()); !errors.Is(err, boom) {
		t.Errorf("GetSigninConfiguration err = %v, want the store failure", err)
	}
	store.getErr = nil
	store.setErr = boom
	if err := svc.UpdateSignin(context.Background(), signinActor(domain.RoleAdmin), application.SigninInput{Panel: domain.SigninPanel{Title: "Hola"}}); !errors.Is(err, boom) {
		t.Errorf("UpdateSignin err = %v, want the store failure", err)
	}
}

// longString builds a value of n runes.
func longString(n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}
