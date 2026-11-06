package sqlite

import (
	"bytes"
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The sign-in panel store (issue #383, S9/S10): the text facts live in the keyed
// settings table — the highlighted rows as one JSON array — and the background
// image in settings_blobs under its own key, because settings.value is TEXT.
// Absent rows answer the documented defaults, a malformed value degrades instead
// of failing the login page, and one submission is one transaction.

func TestSigninStoreAnswersTheDefaultsWhenNothingIsStored(t *testing.T) {
	s := newTestDB(t)

	got, err := s.SigninStore().GetSignin(context.Background())
	if err != nil {
		t.Fatalf("GetSignin: %v", err)
	}
	if got.Panel.Content != domain.SigninContentSteps || got.Panel.Background != domain.SigninBackgroundDark {
		t.Errorf("panel = %+v, want steps on the dark background", got.Panel)
	}
	if got.Panel.NoticeEnabled || len(got.Panel.Highlights) != 0 || got.HasBackground() {
		t.Errorf("configuration = %+v, want no notice, no highlights and no image", got)
	}
}

func TestSigninStoreRoundTripsThePanelAndTheImage(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	image := append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 40)...)
	want := domain.SigninPanel{
		Title:    "Sistemas, a un pedido",
		Subtitle: "Cargá tu pedido y seguí su estado.",
		Content:  domain.SigninContentHighlights,
		Highlights: []domain.SigninHighlight{
			{Icon: domain.SigninIconClock, Title: "Four hours", Text: "Or less"},
			{Icon: domain.SigninIconPhone, Title: "24/7"},
		},
		NoticeEnabled: true,
		NoticeKind:    domain.SigninNoticeWarning,
		NoticeTitle:   "Maintenance",
		NoticeText:    "Saturday 10-12",
		Background:    domain.SigninBackgroundImage,
		BrandColor:    "#C8102E",
		SupportPhone:  "4400",
		SupportHours:  "Mon-Fri",
	}

	if err := s.SigninStore().SetSignin(ctx, want, image); err != nil {
		t.Fatalf("SetSignin: %v", err)
	}
	got, err := s.SigninStore().GetSignin(ctx)
	if err != nil {
		t.Fatalf("GetSignin: %v", err)
	}
	if got.Panel.Title != want.Title || got.Panel.Subtitle != want.Subtitle {
		t.Errorf("text = %q / %q, want %q / %q", got.Panel.Title, got.Panel.Subtitle, want.Title, want.Subtitle)
	}
	if len(got.Panel.Highlights) != 2 || got.Panel.Highlights[0] != want.Highlights[0] || got.Panel.Highlights[1] != want.Highlights[1] {
		t.Errorf("highlights = %+v, want %+v", got.Panel.Highlights, want.Highlights)
	}
	if !got.Panel.NoticeEnabled || got.Panel.NoticeKind != want.NoticeKind || got.Panel.NoticeTitle != want.NoticeTitle || got.Panel.NoticeText != want.NoticeText {
		t.Errorf("notice = %+v, want %+v", got.Panel, want)
	}
	if got.Panel.Background != domain.SigninBackgroundImage || got.Panel.BrandColor != "#C8102E" {
		t.Errorf("background = %+v, want the image style and the brand colour", got.Panel)
	}
	if got.Panel.SupportPhone != "4400" || got.Panel.SupportHours != "Mon-Fri" {
		t.Errorf("contact = %q / %q, want the stored values", got.Panel.SupportPhone, got.Panel.SupportHours)
	}
	if !bytes.Equal(got.Background, image) {
		t.Errorf("image = %d bytes, want the %d stored bytes", len(got.Background), len(image))
	}
	if got.BackgroundMIME != domain.SigninBackgroundJPEG {
		t.Errorf("image type = %q, want %q", got.BackgroundMIME, domain.SigninBackgroundJPEG)
	}
}

// TestSigninStoreDegradesAMalformedHighlightsValue proves the login page never
// fails because of a hand-edited row: a broken JSON value reads back as "no
// highlights", exactly the documented default.
func TestSigninStoreDegradesAMalformedHighlightsValue(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('signin_highlights', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		`{"icon":"clock"}`); err != nil {
		t.Fatalf("seed malformed highlights: %v", err)
	}
	// An unknown content value degrades to the default too, so the panel keeps
	// rendering a middle zone.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('signin_content', 'gallery')`); err != nil {
		t.Fatalf("seed unknown content: %v", err)
	}

	got, err := s.SigninStore().GetSignin(ctx)
	if err != nil {
		t.Fatalf("GetSignin: %v", err)
	}
	if got.Panel.Highlights != nil {
		t.Errorf("highlights = %+v, want none", got.Panel.Highlights)
	}
	if got.Panel.Content != domain.SigninContentSteps {
		t.Errorf("content = %q, want the default steps", got.Panel.Content)
	}
}

// TestSigninStoreRemovalClearsTheImage proves nil means "no background": the
// blob row is deleted, so a later read answers no image rather than the bytes of
// a previous save.
func TestSigninStoreRemovalClearsTheImage(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "Hola", Background: domain.SigninBackgroundImage}, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)); err != nil {
		t.Fatalf("SetSignin: %v", err)
	}

	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "Hola", Background: domain.SigninBackgroundDark}, nil); err != nil {
		t.Fatalf("SetSignin without an image: %v", err)
	}
	got, err := s.SigninStore().GetSignin(ctx)
	if err != nil {
		t.Fatalf("GetSignin: %v", err)
	}
	if got.HasBackground() {
		t.Errorf("image survived a save that carried none: %d bytes", len(got.Background))
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings_blobs WHERE key = 'signin_background'`).Scan(&rows); err != nil {
		t.Fatalf("count settings_blobs: %v", err)
	}
	if rows != 0 {
		t.Errorf("settings_blobs rows = %d, want the background row deleted", rows)
	}
	// The organization's logo row is a different key and is not touched.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings_blobs (key, value, mime) VALUES ('org_logo', ?, 'image/png')`, []byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatalf("seed logo row: %v", err)
	}
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "Hola"}, nil); err != nil {
		t.Fatalf("SetSignin: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings_blobs WHERE key = 'org_logo'`).Scan(&rows); err != nil {
		t.Fatalf("count logo rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("logo rows = %d, want the sign-in save to leave it alone", rows)
	}
}

// TestSigninStoreWriteIsOneTransaction proves the atomicity the service
// promises: an image the table refuses fails the WHOLE write, so the panel's
// text of the same submission is not persisted either.
func TestSigninStoreWriteIsOneTransaction(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "Before"}, nil); err != nil {
		t.Fatalf("seed panel: %v", err)
	}

	// One byte over the background cap, which the table refuses even though the
	// service would have refused it first.
	tooBig := make([]byte, domain.MaxSigninBackgroundBytes+1)
	err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "After"}, tooBig)
	if err == nil {
		t.Fatal("SetSignin stored an image above the table cap, want a failure")
	}

	got, readErr := s.SigninStore().GetSignin(ctx)
	if readErr != nil {
		t.Fatalf("GetSignin: %v", readErr)
	}
	if got.Panel.Title != "Before" {
		t.Errorf("title = %q, want the text rolled back with the refused image", got.Panel.Title)
	}
}

// TestSigninStorePanelWriteKeepsTheStoredImage pins issue #388: a save with no
// file writes the text rows and keeps whatever image the store holds when the
// transaction runs. It carries no bytes, so it can never write back an image a
// concurrent save replaced or removed.
func TestSigninStorePanelWriteKeepsTheStoredImage(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	first := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)
	second := append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 24)...)
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "One", Background: domain.SigninBackgroundImage}, first); err != nil {
		t.Fatalf("SetSignin: %v", err)
	}
	// Another save replaces the image after this one's read saw the first.
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "Two", Background: domain.SigninBackgroundImage}, second); err != nil {
		t.Fatalf("SetSignin replacing the image: %v", err)
	}

	if err := s.SigninStore().SetSigninPanel(ctx, domain.SigninPanel{Title: "Three", Background: domain.SigninBackgroundImage}); err != nil {
		t.Fatalf("SetSigninPanel: %v", err)
	}
	got, err := s.SigninStore().GetSignin(ctx)
	if err != nil {
		t.Fatalf("GetSignin: %v", err)
	}
	if got.Panel.Title != "Three" || got.Panel.Background != domain.SigninBackgroundImage {
		t.Errorf("panel = %+v, want the new text on the image background", got.Panel)
	}
	if string(got.Background) != string(second) || got.BackgroundMIME != domain.SigninBackgroundJPEG {
		t.Errorf("image = %d bytes (%s), want the current %d-byte JPEG kept", len(got.Background), got.BackgroundMIME, len(second))
	}
}

// TestSigninStorePanelWriteAfterARemovalStaysOnDark proves the other half: when
// the image was removed before the panel write commits, the write does not
// restore it, and an "image" style with nothing to show is stored as dark —
// the same surface RemoveBackground leaves.
func TestSigninStorePanelWriteAfterARemovalStaysOnDark(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "One", Background: domain.SigninBackgroundImage}, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)); err != nil {
		t.Fatalf("SetSignin: %v", err)
	}
	if err := s.SigninStore().SetSignin(ctx, domain.SigninPanel{Title: "One", Background: domain.SigninBackgroundDark}, nil); err != nil {
		t.Fatalf("remove the image: %v", err)
	}

	if err := s.SigninStore().SetSigninPanel(ctx, domain.SigninPanel{Title: "Two", Background: domain.SigninBackgroundImage}); err != nil {
		t.Fatalf("SetSigninPanel: %v", err)
	}
	got, err := s.SigninStore().GetSignin(ctx)
	if err != nil {
		t.Fatalf("GetSignin: %v", err)
	}
	if got.HasBackground() {
		t.Errorf("a panel write restored a removed image: %d bytes", len(got.Background))
	}
	if got.Panel.Title != "Two" || got.Panel.Background != domain.SigninBackgroundDark {
		t.Errorf("panel = %+v, want the new text on the dark background", got.Panel)
	}
}

var _ application.SigninStore = (*settingsStore)(nil)
