package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Category suggestion settings (issue #416, S1/S5/S7): the engine is an
// instance setting gated on CapManageSettings, the API key is sealed like the
// SMTP password and never read back, and an absent row means "off" on every
// instance.

// fakeCategorySuggestionSettingsStore holds the stored settings and counts
// writes, so a rejected or denied save can be proven to have persisted nothing.
type fakeCategorySuggestionSettingsStore struct {
	stored   CategorySuggestionSettings
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeCategorySuggestionSettingsStore) GetCategorySuggestionSettings(_ context.Context) (CategorySuggestionSettings, error) {
	if f.getErr != nil {
		return CategorySuggestionSettings{}, f.getErr
	}
	return f.stored, nil
}

func (f *fakeCategorySuggestionSettingsStore) SetCategorySuggestionSettings(_ context.Context, s CategorySuggestionSettings) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = s
	return nil
}

type suggestionSettingsFixture struct {
	svc    *CategorySuggestionSettingsService
	store  *fakeCategorySuggestionSettingsStore
	sealer *fakeSealer
}

func newSuggestionSettingsFixture() suggestionSettingsFixture {
	f := suggestionSettingsFixture{
		store:  &fakeCategorySuggestionSettingsStore{},
		sealer: &fakeSealer{},
	}
	f.svc = NewCategorySuggestionSettingsService(f.store, f.sealer)
	return f
}

func suggestionAdmin() domain.User {
	return domain.User{ID: 2, Name: "Admin", Email: "admin@example.com", Role: domain.RoleAdmin, Active: true}
}

// validSuggestionModelInput is a complete decision-model configuration: each
// test changes one thing from it.
func validSuggestionModelInput() CategorySuggestionSettingsInput {
	return CategorySuggestionSettingsInput{
		Engine:      CategorySuggestionModel,
		EndpointURL: "https://api.example.com/v1",
		Model:       "gpt-4.1-mini",
		APIKey:      "sk-test-key",
	}
}

func TestCategorySuggestionSettingsAreGatedOnCapManageSettings(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		f := newSuggestionSettingsFixture()
		actor := domain.User{ID: 3, Role: role, Active: true, Email: "x@example.com"}
		var forbidden *domain.ForbiddenError
		if err := f.svc.Save(context.Background(), actor, validSuggestionModelInput()); !errors.As(err, &forbidden) {
			t.Errorf("%s: Save = %v, want ForbiddenError", role, err)
		}
		if _, err := f.svc.Get(context.Background(), actor); !errors.As(err, &forbidden) {
			t.Errorf("%s: Get = %v, want ForbiddenError", role, err)
		}
		if f.store.setCalls != 0 {
			t.Errorf("%s: denied actor wrote the store %d times", role, f.store.setCalls)
		}
	}
	for _, actor := range []domain.User{suggestionAdmin(), emailRoot()} {
		f := newSuggestionSettingsFixture()
		if err := f.svc.Save(context.Background(), actor, validSuggestionModelInput()); err != nil {
			t.Errorf("%s: Save = %v, want permission", actor.Role, err)
		}
		if _, err := f.svc.Get(context.Background(), actor); err != nil {
			t.Errorf("%s: Get = %v, want permission", actor.Role, err)
		}
	}
}

func TestSaveCategorySuggestionSettingsSealsTheKeyAndGetNeverReturnsIt(t *testing.T) {
	f := newSuggestionSettingsFixture()
	if err := f.svc.Save(context.Background(), suggestionAdmin(), validSuggestionModelInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.store.stored.SealedAPIKey != "sealed(yek-tset-ks)" {
		t.Fatalf("stored key = %q, want the sealed form", f.store.stored.SealedAPIKey)
	}
	if f.store.stored.APIKeySet {
		t.Fatal("Save wrote the read-side APIKeySet flag")
	}
	got, err := f.svc.Get(context.Background(), suggestionAdmin())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SealedAPIKey != "" || !got.APIKeySet {
		t.Fatalf("Get exposes SealedAPIKey=%q APIKeySet=%v; want empty and true", got.SealedAPIKey, got.APIKeySet)
	}
	want := validSuggestionModelInput()
	if got.Engine != want.Engine || got.EndpointURL != want.EndpointURL || got.Model != want.Model {
		t.Fatalf("Get = %+v, want the saved values", got)
	}
}

func TestSaveCategorySuggestionSettingsKeepsTheKeyAndTheModelConfig(t *testing.T) {
	f := newSuggestionSettingsFixture()
	ctx := context.Background()
	if err := f.svc.Save(ctx, suggestionAdmin(), validSuggestionModelInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// An empty key field keeps the stored key (the card never shows it back).
	keep := validSuggestionModelInput()
	keep.APIKey = ""
	keep.Model = "gpt-4.1"
	if err := f.svc.Save(ctx, suggestionAdmin(), keep); err != nil {
		t.Fatalf("Save keeping the key: %v", err)
	}
	if f.store.stored.SealedAPIKey != "sealed(yek-tset-ks)" {
		t.Fatalf("empty key field changed the stored key to %q", f.store.stored.SealedAPIKey)
	}
	if f.store.stored.Model != "gpt-4.1" {
		t.Fatalf("model = %q, want the submitted one", f.store.stored.Model)
	}

	// Switching the engine away keeps the model configuration, and the other
	// engine's unvalidated fields are not stored.
	for _, engine := range []CategorySuggestionEngine{CategorySuggestionOff, CategorySuggestionLocal} {
		if err := f.svc.Save(ctx, suggestionAdmin(), CategorySuggestionSettingsInput{
			Engine:      engine,
			EndpointURL: "not a url",
			Model:       "a\nb",
			APIKey:      "ignored",
		}); err != nil {
			t.Fatalf("Save %s: %v", engine, err)
		}
		if f.store.stored.Engine != engine {
			t.Fatalf("engine = %q, want %q", f.store.stored.Engine, engine)
		}
		if f.store.stored.EndpointURL != "https://api.example.com/v1" || f.store.stored.Model != "gpt-4.1" {
			t.Fatalf("%s lost the model config: %+v", engine, f.store.stored)
		}
		if f.store.stored.SealedAPIKey != "sealed(yek-tset-ks)" {
			t.Fatalf("%s lost the stored key: %q", engine, f.store.stored.SealedAPIKey)
		}
	}

	// Turning the model back on with no new key reuses the stored one.
	if err := f.svc.Save(ctx, suggestionAdmin(), keep); err != nil {
		t.Fatalf("Save back to the model: %v", err)
	}
	if f.store.stored.Engine != CategorySuggestionModel || f.store.stored.SealedAPIKey != "sealed(yek-tset-ks)" {
		t.Fatalf("turning the model back on lost the key: %+v", f.store.stored)
	}

	// A submitted key replaces the stored one.
	replace := validSuggestionModelInput()
	replace.APIKey = "sk-other"
	if err := f.svc.Save(ctx, suggestionAdmin(), replace); err != nil {
		t.Fatalf("Save replacing the key: %v", err)
	}
	if f.store.stored.SealedAPIKey != "sealed(rehto-ks)" {
		t.Fatalf("stored key = %q, want the new sealed value", f.store.stored.SealedAPIKey)
	}
}

func TestSaveCategorySuggestionSettingsValidatesBeforeWriting(t *testing.T) {
	cases := map[string]struct {
		mutate func(*CategorySuggestionSettingsInput)
		key    string
	}{
		"engine unknown":    {func(in *CategorySuggestionSettingsInput) { in.Engine = "bogus" }, "error.suggestions_engine_invalid"},
		"engine empty":      {func(in *CategorySuggestionSettingsInput) { in.Engine = "" }, "error.suggestions_engine_invalid"},
		"endpoint required": {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "  " }, "error.suggestions_endpoint_required"},
		"endpoint http":     {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "http://api.example.com" }, "error.suggestions_endpoint_invalid"},
		"endpoint no host":  {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "https://" }, "error.suggestions_endpoint_invalid"},
		"endpoint relative": {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "/v1" }, "error.suggestions_endpoint_invalid"},
		"endpoint userinfo": {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "https://u:p@api.example.com" }, "error.suggestions_endpoint_invalid"},
		"endpoint query":    {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "https://api.example.com/?a=b" }, "error.suggestions_endpoint_invalid"},
		"endpoint fragment": {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "https://api.example.com/#x" }, "error.suggestions_endpoint_invalid"},
		"endpoint with gap": {func(in *CategorySuggestionSettingsInput) { in.EndpointURL = "https://api example.com" }, "error.suggestions_endpoint_invalid"},
		"model required":    {func(in *CategorySuggestionSettingsInput) { in.Model = " " }, "error.suggestions_model_required"},
		"model newline":     {func(in *CategorySuggestionSettingsInput) { in.Model = "gpt\n4" }, "error.suggestions_model_invalid"},
		"model too long":    {func(in *CategorySuggestionSettingsInput) { in.Model = strings.Repeat("m", 101) }, "error.suggestions_model_invalid"},
		"key newline":       {func(in *CategorySuggestionSettingsInput) { in.APIKey = "sk\r\nX" }, "error.suggestions_api_key_invalid"},
		"key too long":      {func(in *CategorySuggestionSettingsInput) { in.APIKey = strings.Repeat("k", 1025) }, "error.suggestions_api_key_invalid"},
		"key required":      {func(in *CategorySuggestionSettingsInput) { in.APIKey = "" }, "error.suggestions_api_key_required"},
	}
	for name, tc := range cases {
		f := newSuggestionSettingsFixture()
		in := validSuggestionModelInput()
		tc.mutate(&in)
		err := f.svc.Save(context.Background(), suggestionAdmin(), in)
		var verr *domain.ValidationError
		if !errors.As(err, &verr) || verr.Key != tc.key {
			t.Errorf("%s: Save = %v, want ValidationError %s", name, err, tc.key)
		}
		if f.store.setCalls != 0 {
			t.Errorf("%s: a rejected save wrote the store", name)
		}
	}
}

func TestSaveCategorySuggestionSettingsNormalizesTheEndpoint(t *testing.T) {
	f := newSuggestionSettingsFixture()
	in := validSuggestionModelInput()
	in.EndpointURL = "  https://api.example.com/v1/  "
	if err := f.svc.Save(context.Background(), suggestionAdmin(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.store.stored.EndpointURL != "https://api.example.com/v1" {
		t.Fatalf("EndpointURL = %q, want trimmed without the trailing slash", f.store.stored.EndpointURL)
	}
}

func TestSaveCategorySuggestionSettingsSealFailureWritesNothing(t *testing.T) {
	f := newSuggestionSettingsFixture()
	f.sealer.sealErr = errors.New("key file unavailable")
	if err := f.svc.Save(context.Background(), suggestionAdmin(), validSuggestionModelInput()); err == nil {
		t.Fatal("Save succeeded although the key could not be sealed")
	}
	if f.store.setCalls != 0 {
		t.Fatal("Save wrote the store after a seal failure")
	}
}

func TestCategorySuggestionConfigUnsealsTheKeyInMemory(t *testing.T) {
	f := newSuggestionSettingsFixture()
	ctx := context.Background()
	if err := f.svc.Save(ctx, suggestionAdmin(), validSuggestionModelInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := f.svc.Config(ctx)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.APIKey != "sk-test-key" || cfg.Model != "gpt-4.1-mini" || cfg.EndpointURL != "https://api.example.com/v1" || cfg.Engine != CategorySuggestionModel {
		t.Fatalf("Config = %+v, want the unsealed key and the stored model config", cfg)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, cfg); strings.Contains(got, "sk-test-key") {
			t.Errorf("%s formats the api key: %s", verb, got)
		}
	}
	modelCfg := DecisionModelConfig{EndpointURL: cfg.EndpointURL, Model: cfg.Model, APIKey: cfg.APIKey}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, modelCfg); strings.Contains(got, "sk-test-key") {
			t.Errorf("%s formats the decision model api key: %s", verb, got)
		}
	}
}

func TestCategorySuggestionConfigIsOffWithoutRowsAndNeverOpensTheKey(t *testing.T) {
	f := newSuggestionSettingsFixture()
	f.sealer.openErr = errors.New("key file unavailable")
	cfg, err := f.svc.Config(context.Background())
	if err != nil {
		t.Fatalf("Config without rows = %v, want the off default", err)
	}
	if cfg.Engine != CategorySuggestionOff || cfg.APIKey != "" {
		t.Fatalf("Config = %+v, want off with no key", cfg)
	}
	if err := f.svc.Save(context.Background(), suggestionAdmin(), CategorySuggestionSettingsInput{Engine: CategorySuggestionLocal}); err != nil {
		t.Fatalf("Save local: %v", err)
	}
	if cfg, err := f.svc.Config(context.Background()); err != nil || cfg.Engine != CategorySuggestionLocal {
		t.Fatalf("Config local = %+v, %v; want the local engine and no key opened", cfg, err)
	}
}

func TestCategorySuggestionConfigReportsAnUnusableKey(t *testing.T) {
	ctx := context.Background()

	// The database was restored without its key file.
	f := newSuggestionSettingsFixture()
	if err := f.svc.Save(ctx, suggestionAdmin(), validSuggestionModelInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f.sealer.openErr = errors.New("secretbox: key unavailable")
	cfg, err := f.svc.Config(ctx)
	if !errors.Is(err, ErrCategorySuggestionKeyUnreadable) {
		t.Fatalf("Config with an unreadable key = %v, want ErrCategorySuggestionKeyUnreadable", err)
	}
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Key != "error.suggestions_api_key_unreadable" {
		t.Fatalf("Config error = %v, want the catalogue-backed error", err)
	}
	if cfg.Engine != "" || cfg.APIKey != "" {
		t.Fatalf("Config returned %+v with an unreadable key, want the zero value", cfg)
	}

	// A stored decision-model engine with no key cannot run either.
	broken := newSuggestionSettingsFixture()
	broken.store.stored = CategorySuggestionSettings{Engine: CategorySuggestionModel, EndpointURL: "https://api.example.com/v1", Model: "gpt-4.1-mini"}
	if _, err := broken.svc.Config(ctx); !errors.Is(err, ErrCategorySuggestionKeyUnreadable) {
		t.Fatalf("Config with no stored key = %v, want ErrCategorySuggestionKeyUnreadable", err)
	}
}
