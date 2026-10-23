package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Category suggestion settings row keys (issue #416). No migration seeds them:
// an absent row reads back the "off" default, so the keyed settings table needs
// no new column and the feature starts off on every instance (S7). The api key
// row holds the secret box output, never the key.
const (
	settingsKeyCategorySuggestEngine      = "category_suggest_engine"
	settingsKeyCategorySuggestEndpointURL = "category_suggest_endpoint_url"
	settingsKeyCategorySuggestModel       = "category_suggest_model"
	settingsKeyCategorySuggestAPIKey      = "category_suggest_api_key_sealed"
)

var _ application.CategorySuggestionSettingsStore = (*settingsStore)(nil)

// CategorySuggestionSettingsStore returns the category suggestion settings port
// (issue #416), served by the same keyed settings table.
func (s *Store) CategorySuggestionSettingsStore() application.CategorySuggestionSettingsStore {
	return newSettingsStore(s.db)
}

// GetCategorySuggestionSettings assembles the category suggestion configuration
// from its rows in one read. An absent row falls back to its default, and an
// unknown stored engine reads back as "off": an instance whose rows were written
// by another build never runs an engine this build does not ship (S7).
func (ss *settingsStore) GetCategorySuggestionSettings(ctx context.Context) (application.CategorySuggestionSettings, error) {
	rows, err := ss.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE 'category\_suggest\_%' ESCAPE '\'`)
	if err != nil {
		return application.CategorySuggestionSettings{}, fmt.Errorf("sqlite: get category suggestion settings: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return application.CategorySuggestionSettings{}, fmt.Errorf("sqlite: scan category suggestion settings: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return application.CategorySuggestionSettings{}, fmt.Errorf("sqlite: get category suggestion settings: %w", err)
	}

	out := application.CategorySuggestionSettings{
		Engine:       application.DefaultCategorySuggestionEngine,
		EndpointURL:  values[settingsKeyCategorySuggestEndpointURL],
		Model:        values[settingsKeyCategorySuggestModel],
		SealedAPIKey: values[settingsKeyCategorySuggestAPIKey],
	}
	for _, engine := range application.AllowedCategorySuggestionEngines() {
		if string(engine) == values[settingsKeyCategorySuggestEngine] {
			out.Engine = engine
		}
	}
	return out, nil
}

// SetCategorySuggestionSettings writes every category suggestion row in ONE
// transaction, so a save is never half-applied. The service validated the
// values; the store refuses only a read-side value (APIKeySet), which would mean
// a Get result is being written back.
func (ss *settingsStore) SetCategorySuggestionSettings(ctx context.Context, s application.CategorySuggestionSettings) error {
	if s.APIKeySet {
		return errors.New("sqlite: set category suggestion settings: APIKeySet is read-only; pass SealedAPIKey")
	}
	tx, err := beginImmediate(ctx, ss.db, "set category suggestion settings")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	writes := []struct {
		key   string
		value string
	}{
		{settingsKeyCategorySuggestEngine, string(s.Engine)},
		{settingsKeyCategorySuggestEndpointURL, s.EndpointURL},
		{settingsKeyCategorySuggestModel, s.Model},
		{settingsKeyCategorySuggestAPIKey, s.SealedAPIKey},
	}
	for _, w := range writes {
		if err := setSettingTx(ctx, tx, w.key, w.value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
