package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Category suggestion settings (issue #416, S7) live in the keyed settings
// table: no migration seeds them, so an untouched instance reads back "off",
// and a save writes every row in one transaction.

func TestCategorySuggestionSettingsStoreDefaultsToOffWithoutRows(t *testing.T) {
	s := newTestDB(t)
	got, err := s.CategorySuggestionSettingsStore().GetCategorySuggestionSettings(context.Background())
	if err != nil {
		t.Fatalf("GetCategorySuggestionSettings: %v", err)
	}
	want := application.CategorySuggestionSettings{Engine: application.CategorySuggestionOff}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestCategorySuggestionSettingsStoreRoundTripsEveryField(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	want := application.CategorySuggestionSettings{
		Engine:       application.CategorySuggestionModel,
		EndpointURL:  "https://api.example.com/v1",
		Model:        "gpt-4.1-mini",
		SealedAPIKey: "v1:opaque",
	}
	if err := s.CategorySuggestionSettingsStore().SetCategorySuggestionSettings(ctx, want); err != nil {
		t.Fatalf("SetCategorySuggestionSettings: %v", err)
	}
	got, err := s.CategorySuggestionSettingsStore().GetCategorySuggestionSettings(ctx)
	if err != nil {
		t.Fatalf("GetCategorySuggestionSettings: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	// Clearing values writes them back empty, not "left over".
	cleared := application.CategorySuggestionSettings{Engine: application.CategorySuggestionOff}
	if err := s.CategorySuggestionSettingsStore().SetCategorySuggestionSettings(ctx, cleared); err != nil {
		t.Fatalf("SetCategorySuggestionSettings: %v", err)
	}
	if got, _ := s.CategorySuggestionSettingsStore().GetCategorySuggestionSettings(ctx); got != cleared {
		t.Fatalf("after clearing = %+v, want %+v", got, cleared)
	}
}

func TestCategorySuggestionSettingsStoreNeverAcceptsTheReadSideFlag(t *testing.T) {
	s := newTestDB(t)
	// APIKeySet is a read-side projection; the store refuses a value that
	// carries it so no caller can mistake a Get result for a save payload.
	err := s.CategorySuggestionSettingsStore().SetCategorySuggestionSettings(context.Background(), application.CategorySuggestionSettings{
		Engine:    application.CategorySuggestionModel,
		APIKeySet: true,
	})
	if err == nil {
		t.Fatal("SetCategorySuggestionSettings accepted a read-side value")
	}
}

func TestCategorySuggestionSettingsStoreReadsAnUnknownEngineAsOff(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES
		('category_suggest_engine', 'model-v2'),
		('category_suggest_endpoint_url', 'https://api.example.com/v1'),
		('category_suggest_model', 'gpt-4.1-mini')`); err != nil {
		t.Fatal(err)
	}
	got, err := s.CategorySuggestionSettingsStore().GetCategorySuggestionSettings(ctx)
	if err != nil {
		t.Fatalf("GetCategorySuggestionSettings: %v", err)
	}
	if got.Engine != application.CategorySuggestionOff {
		t.Fatalf("engine = %q, want off for an unknown stored value", got.Engine)
	}
	if got.EndpointURL != "https://api.example.com/v1" || got.Model != "gpt-4.1-mini" {
		t.Fatalf("the stored model config was dropped: %+v", got)
	}
}

func TestCategorySuggestionSettingsStoreLeavesTheEmailSettingsRowsAlone(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	// The two configurations share the keyed settings table and the same write
	// helper, so a category suggestion save must not touch the email rows.
	email := application.EmailSettings{
		Enabled:        true,
		Host:           "smtp.example.com",
		Port:           587,
		Encryption:     application.EmailEncryptionSTARTTLS,
		SealedPassword: "v1:opaque",
		FromAddress:    "tkt@example.com",
		BaseURL:        "https://tkt.example.com",
	}
	if err := s.EmailSettingsStore().SetEmailSettings(ctx, email); err != nil {
		t.Fatalf("SetEmailSettings: %v", err)
	}
	settings := application.CategorySuggestionSettings{
		Engine:       application.CategorySuggestionModel,
		EndpointURL:  "https://api.example.com/v1",
		Model:        "gpt-4.1-mini",
		SealedAPIKey: "v1:opaque",
	}
	if err := s.CategorySuggestionSettingsStore().SetCategorySuggestionSettings(ctx, settings); err != nil {
		t.Fatalf("SetCategorySuggestionSettings: %v", err)
	}
	got, err := s.EmailSettingsStore().GetEmailSettings(ctx)
	if err != nil {
		t.Fatalf("GetEmailSettings: %v", err)
	}
	if got != email {
		t.Fatalf("email settings after a suggestion save = %+v, want %+v", got, email)
	}
}

func TestCategorySuggestionSettingsStoreWritesInOneTransaction(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	// A trigger that fails the third row proves the save is all-or-nothing: the
	// first rows cannot be left behind by a reported failure.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_category_suggest_model
		BEFORE INSERT ON settings
		WHEN NEW.key = 'category_suggest_model'
		BEGIN SELECT RAISE(ABORT, 'category suggestion model row is refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	err := s.CategorySuggestionSettingsStore().SetCategorySuggestionSettings(ctx, application.CategorySuggestionSettings{
		Engine:       application.CategorySuggestionModel,
		EndpointURL:  "https://api.example.com/v1",
		Model:        "gpt-4.1-mini",
		SealedAPIKey: "v1:opaque",
	})
	if err == nil {
		t.Fatal("SetCategorySuggestionSettings succeeded although a row was refused")
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key LIKE 'category\_suggest\_%' ESCAPE '\'`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("%d category suggestion rows survived a refused save, want 0", rows)
	}
	if got, err := s.CategorySuggestionSettingsStore().GetCategorySuggestionSettings(ctx); err != nil {
		t.Fatalf("GetCategorySuggestionSettings: %v", err)
	} else if got.Engine != application.CategorySuggestionOff {
		t.Fatalf("engine = %q after a refused save, want off", got.Engine)
	}
}
