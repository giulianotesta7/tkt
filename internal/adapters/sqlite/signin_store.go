package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The sign-in panel's settings rows (issue #383, S9/S10). Every text fact is one
// keyed row beside the other instance settings; the highlighted rows travel as
// one JSON array so the list needs no table of its own. No migration seeds them:
// an absent row answers the documented default, so a fresh instance renders the
// full default panel without a stored row.
const (
	settingsKeySigninTitle          = "signin_title"
	settingsKeySigninSubtitle       = "signin_subtitle"
	settingsKeySigninContent        = "signin_content"
	settingsKeySigninHighlights     = "signin_highlights"
	settingsKeySigninNoticeEnabled  = "signin_notice_enabled"
	settingsKeySigninNoticeKind     = "signin_notice_kind"
	settingsKeySigninNoticeTitle    = "signin_notice_title"
	settingsKeySigninNoticeText     = "signin_notice_text"
	settingsKeySigninBackground     = "signin_background_style"
	settingsKeySigninBrandColor     = "signin_brand_color"
	settingsKeySigninSupportPhone   = "signin_support_phone"
	settingsKeySigninSupportHours   = "signin_support_hours"
	settingsKeySigninBackgroundBlob = "signin_background"
)

// The stored text rows of one panel, in the order SetSignin writes them.
var signinPanelKeys = []struct {
	key      string
	fromPane func(domain.SigninPanel) string
}{
	{settingsKeySigninTitle, func(p domain.SigninPanel) string { return p.Title }},
	{settingsKeySigninSubtitle, func(p domain.SigninPanel) string { return p.Subtitle }},
	{settingsKeySigninContent, func(p domain.SigninPanel) string { return p.Content }},
	{settingsKeySigninHighlights, func(p domain.SigninPanel) string { return domain.EncodeSigninHighlights(p.Highlights) }},
	{settingsKeySigninNoticeEnabled, func(p domain.SigninPanel) string { return boolSetting(p.NoticeEnabled) }},
	{settingsKeySigninNoticeKind, func(p domain.SigninPanel) string { return p.NoticeKind }},
	{settingsKeySigninNoticeTitle, func(p domain.SigninPanel) string { return p.NoticeTitle }},
	{settingsKeySigninNoticeText, func(p domain.SigninPanel) string { return p.NoticeText }},
	{settingsKeySigninBackground, func(p domain.SigninPanel) string { return p.Background }},
	{settingsKeySigninBrandColor, func(p domain.SigninPanel) string { return p.BrandColor }},
	{settingsKeySigninSupportPhone, func(p domain.SigninPanel) string { return p.SupportPhone }},
	{settingsKeySigninSupportHours, func(p domain.SigninPanel) string { return p.SupportHours }},
}

// GetSignin answers the stored panel and background image (issue #383, S9/S10).
// It never fails for configuration reasons: absent rows answer the documented
// defaults, a value outside a closed set degrades to its default, and a
// malformed highlights array reads back as no highlights — the login page must
// render even for a hand-edited database. Only a real storage failure is an
// error.
func (ss *settingsStore) GetSignin(ctx context.Context) (application.SigninConfiguration, error) {
	values := make(map[string]string, len(signinPanelKeys))
	for _, row := range signinPanelKeys {
		value, err := ss.settingValue(ctx, row.key)
		if err != nil {
			return application.SigninConfiguration{}, err
		}
		values[row.key] = value
	}
	panel := domain.SigninPanel{
		Title:         values[settingsKeySigninTitle],
		Subtitle:      values[settingsKeySigninSubtitle],
		Content:       values[settingsKeySigninContent],
		Highlights:    domain.DecodeSigninHighlights(values[settingsKeySigninHighlights]),
		NoticeEnabled: values[settingsKeySigninNoticeEnabled] == "1",
		NoticeKind:    values[settingsKeySigninNoticeKind],
		NoticeTitle:   values[settingsKeySigninNoticeTitle],
		NoticeText:    values[settingsKeySigninNoticeText],
		Background:    values[settingsKeySigninBackground],
		BrandColor:    values[settingsKeySigninBrandColor],
		SupportPhone:  values[settingsKeySigninSupportPhone],
		SupportHours:  values[settingsKeySigninSupportHours],
	}
	if !isSigninContentValue(panel.Content) {
		panel.Content = domain.SigninContentSteps
	}
	if !isSigninBackgroundValue(panel.Background) {
		panel.Background = domain.SigninBackgroundDark
	}
	if panel.NoticeEnabled && !isSigninNoticeKindValue(panel.NoticeKind) {
		panel.NoticeKind = domain.SigninNoticeInfo
	}
	image, mime, err := ss.blobValue(ctx, settingsKeySigninBackgroundBlob)
	if err != nil {
		return application.SigninConfiguration{}, err
	}
	return application.SigninConfiguration{Panel: panel, Background: image, BackgroundMIME: mime}, nil
}

// SetSignin writes the panel's text rows and the background image in ONE
// transaction, so one submission lands entirely or not at all: a refused image
// must not leave the text that travelled with it stored. A nil background
// deletes the stored image.
func (ss *settingsStore) SetSignin(ctx context.Context, panel domain.SigninPanel, background []byte) error {
	tx, err := beginImmediate(ctx, ss.db, "set signin panel")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, row := range signinPanelKeys {
		if err := setSettingTx(ctx, tx, row.key, row.fromPane(panel)); err != nil {
			return err
		}
	}
	if len(background) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings_blobs WHERE key = ?`, settingsKeySigninBackgroundBlob); err != nil {
			return fmt.Errorf("sqlite: delete %s: %w", settingsKeySigninBackgroundBlob, err)
		}
	} else {
		mime, ok := domain.SigninBackgroundContentType(background)
		if !ok {
			return fmt.Errorf("sqlite: set %s: unsupported image type", settingsKeySigninBackgroundBlob)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings_blobs (key, value, mime) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, mime = excluded.mime`,
			settingsKeySigninBackgroundBlob, background, mime); err != nil {
			return fmt.Errorf("sqlite: set %s: %w", settingsKeySigninBackgroundBlob, err)
		}
	}
	return tx.Commit()
}

// blobValue reads one settings_blobs row. An absent row answers (nil, ""), which
// every caller renders as "no image".
func (ss *settingsStore) blobValue(ctx context.Context, key string) ([]byte, string, error) {
	var (
		value []byte
		mime  string
	)
	err := ss.db.QueryRowContext(ctx,
		`SELECT value, mime FROM settings_blobs WHERE key = ?`, key).Scan(&value, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("sqlite: get %s: %w", key, err)
	}
	return value, mime, nil
}

// isSigninContentValue, isSigninBackgroundValue and isSigninNoticeKindValue keep
// the stored closed sets in step with the domain's, so a value the binary no
// longer offers reads back as the default instead of an unrenderable panel.
func isSigninContentValue(value string) bool {
	for _, option := range domain.SigninContentOptions() {
		if value == option {
			return true
		}
	}
	return false
}

func isSigninBackgroundValue(value string) bool {
	for _, option := range domain.SigninBackgroundOptions() {
		if value == option {
			return true
		}
	}
	return false
}

func isSigninNoticeKindValue(value string) bool {
	for _, option := range domain.SigninNoticeKinds() {
		if value == option {
			return true
		}
	}
	return false
}
