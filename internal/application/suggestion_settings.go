package application

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Category suggestion settings (issue #416): an administrator chooses how TKT
// suggests a category while a ticket is written. Suggestions can be Off, run
// on this server (the local algorithm, S4), or ask an external decision model
// through the OpenAI Decisions API (S5). The engine values are the stored
// values, so display copy never changes them.

// CategorySuggestionEngine is the engine that produces category suggestions.
type CategorySuggestionEngine string

const (
	// CategorySuggestionOff keeps both creation screens exactly as they are
	// (S7). It is the default, so an instance that never chose answers it
	// without a stored row.
	CategorySuggestionOff CategorySuggestionEngine = "off"
	// CategorySuggestionLocal runs the local algorithm on this server: no
	// text leaves TKT (S4).
	CategorySuggestionLocal CategorySuggestionEngine = "local"
	// CategorySuggestionModel asks an external decision model (S5).
	CategorySuggestionModel CategorySuggestionEngine = "model"
)

// DefaultCategorySuggestionEngine is the engine an instance runs until an
// administrator chooses another one (S7).
const DefaultCategorySuggestionEngine = CategorySuggestionOff

// AllowedCategorySuggestionEngines lists the engines an administrator may
// choose, in the order the settings card offers them.
func AllowedCategorySuggestionEngines() []CategorySuggestionEngine {
	return []CategorySuggestionEngine{CategorySuggestionOff, CategorySuggestionLocal, CategorySuggestionModel}
}

// isAllowedCategorySuggestionEngine reports whether engine is one of the
// shipped engines. The values are exact: a case variant or a near-miss is
// refused rather than quietly normalized, so what an administrator saved is
// exactly what the instance runs.
func isAllowedCategorySuggestionEngine(engine CategorySuggestionEngine) bool {
	for _, allowed := range AllowedCategorySuggestionEngines() {
		if engine == allowed {
			return true
		}
	}
	return false
}

// CategorySuggestionSettings is the stored category-suggestion configuration.
// SealedAPIKey is the secret box output, never the key; Get blanks it and
// reports APIKeySet instead, so no read path renders it back.
type CategorySuggestionSettings struct {
	Engine      CategorySuggestionEngine
	EndpointURL string
	Model       string
	// SealedAPIKey is set only on the store side of the port.
	SealedAPIKey string
	// APIKeySet is set only on values returned by Get.
	APIKeySet bool
}

// CategorySuggestionSettingsInput is one submission of the settings card. An
// empty APIKey keeps the stored one, and choosing an engine other than the
// decision model keeps the stored model configuration, so turning the model
// back on does not lose the endpoint, the model, or the key.
type CategorySuggestionSettingsInput struct {
	Engine      CategorySuggestionEngine
	EndpointURL string
	Model       string
	APIKey      string
}

// CategorySuggestionConfig is the effective configuration the request path
// uses, with the API key already unsealed. It lives only in memory for one
// call.
type CategorySuggestionConfig struct {
	Engine      CategorySuggestionEngine
	EndpointURL string
	Model       string
	APIKey      string
}

// String keeps the API key out of every log line and error that formats a
// config with %v or %+v.
func (c CategorySuggestionConfig) String() string {
	return fmt.Sprintf("{%s endpoint=%s model=%q api_key=%t}", c.Engine, c.EndpointURL, c.Model, c.APIKey != "")
}

// GoString covers %#v, which bypasses String.
func (c CategorySuggestionConfig) GoString() string { return c.String() }

// CategorySuggestionSettingsStore persists the category-suggestion settings as
// one unit: a save writes every field in one transaction, so a half-applied
// configuration is never readable.
type CategorySuggestionSettingsStore interface {
	GetCategorySuggestionSettings(ctx context.Context) (CategorySuggestionSettings, error)
	SetCategorySuggestionSettings(ctx context.Context, settings CategorySuggestionSettings) error
}

// CategorySuggestionSettingsService implements the category-suggestion settings
// use cases. They are gated on CapManageSettings (admin and root), the same
// instance-configuration capability as the rest of the Tickets & SLA card.
type CategorySuggestionSettingsService struct {
	store  CategorySuggestionSettingsStore
	sealer SecretSealer
}

// NewCategorySuggestionSettingsService wires the category-suggestion settings
// use cases.
func NewCategorySuggestionSettingsService(store CategorySuggestionSettingsStore, sealer SecretSealer) *CategorySuggestionSettingsService {
	return &CategorySuggestionSettingsService{store: store, sealer: sealer}
}

func requireManageSuggestionSettings(actor domain.User) error {
	return requireManageSettings(actor, "category suggestion settings are not permitted")
}

// Get returns the stored settings without the API key: APIKeySet says whether
// one is stored, and SealedAPIKey is always empty.
func (s *CategorySuggestionSettingsService) Get(ctx context.Context, actor domain.User) (CategorySuggestionSettings, error) {
	if err := requireManageSuggestionSettings(actor); err != nil {
		return CategorySuggestionSettings{}, err
	}
	stored, err := s.store.GetCategorySuggestionSettings(ctx)
	if err != nil {
		return CategorySuggestionSettings{}, err
	}
	return withoutAPIKey(stored), nil
}

func withoutAPIKey(stored CategorySuggestionSettings) CategorySuggestionSettings {
	stored.Engine = normalizedCategorySuggestionEngine(stored.Engine)
	stored.APIKeySet = stored.SealedAPIKey != ""
	stored.SealedAPIKey = ""
	return stored
}

// normalizedCategorySuggestionEngine keeps an engine this binary does not ship
// from reaching a caller: an unknown stored value reads back as off (S7), so a
// row written by another build never turns an engine on.
func normalizedCategorySuggestionEngine(engine CategorySuggestionEngine) CategorySuggestionEngine {
	if isAllowedCategorySuggestionEngine(engine) {
		return engine
	}
	return DefaultCategorySuggestionEngine
}

// Save validates the whole submission and only then writes it (#345: a
// rejected save changes nothing). A new key is sealed before the write, an
// empty key field keeps the stored one, and an engine other than the decision
// model keeps the stored model configuration untouched.
func (s *CategorySuggestionSettingsService) Save(ctx context.Context, actor domain.User, in CategorySuggestionSettingsInput) error {
	if err := requireManageSuggestionSettings(actor); err != nil {
		return err
	}
	stored, err := s.store.GetCategorySuggestionSettings(ctx)
	if err != nil {
		return err
	}
	next, err := normalizeCategorySuggestionInput(in, stored)
	if err != nil {
		return err
	}
	if next.Engine == CategorySuggestionModel && in.APIKey != "" {
		sealed, err := s.sealer.Seal([]byte(in.APIKey))
		if err != nil {
			return fmt.Errorf("seal category suggestion api key: %w", err)
		}
		next.SealedAPIKey = sealed
	} else {
		next.SealedAPIKey = stored.SealedAPIKey
	}
	return s.store.SetCategorySuggestionSettings(ctx, next)
}

// Category suggestion validation errors. Each is a catalogue-backed sentence
// the settings card shows next to the field it names.
var (
	errSuggestionEngineInvalid    = &domain.ValidationError{Field: "engine", Message: "choose how categories are suggested: off, on this server, or the decision model", Key: "error.suggestions_engine_invalid"}
	errSuggestionEndpointRequired = &domain.ValidationError{Field: "endpoint_url", Message: "enter the decision model address to use the decision model", Key: "error.suggestions_endpoint_required"}
	errSuggestionEndpointInvalid  = &domain.ValidationError{Field: "endpoint_url", Message: "the decision model address must be an https URL with a host and no user, query, or fragment, like https://api.example.com/v1", Key: "error.suggestions_endpoint_invalid"}
	errSuggestionModelRequired    = &domain.ValidationError{Field: "model", Message: "enter the decision model to use the decision model", Key: "error.suggestions_model_required"}
	errSuggestionModelInvalid     = &domain.ValidationError{Field: "model", Message: "the model must fit on one line and have at most 100 characters", Key: "error.suggestions_model_invalid"}
	errSuggestionAPIKeyInvalid    = &domain.ValidationError{Field: "api_key", Message: "the API key must fit on one line and have at most 1024 characters", Key: "error.suggestions_api_key_invalid"}
	errSuggestionAPIKeyRequired   = &domain.ValidationError{Field: "api_key", Message: "enter the API key the decision model expects", Key: "error.suggestions_api_key_required"}
	errSuggestionKeyUnreadable    = &domain.ValidationError{Field: "api_key", Message: "the saved decision model API key cannot be read on this server; enter it again", Key: "error.suggestions_api_key_unreadable"}
)

// ErrCategorySuggestionKeyUnreadable reports that the stored API key cannot be
// used: the sealed value does not open, typically because the database was
// restored without its key file, or the decision model engine was stored
// without a key. The caller falls back to the local engine.
var ErrCategorySuggestionKeyUnreadable = errors.New("category suggestion api key cannot be read")

// normalizeCategorySuggestionInput trims and validates one submission. The
// decision-model fields are validated and stored only when the chosen engine is
// the decision model; any other engine keeps the stored model configuration, so
// turning the model back on does not lose it.
func normalizeCategorySuggestionInput(in CategorySuggestionSettingsInput, stored CategorySuggestionSettings) (CategorySuggestionSettings, error) {
	out := CategorySuggestionSettings{Engine: in.Engine}
	if !isAllowedCategorySuggestionEngine(out.Engine) {
		return out, errSuggestionEngineInvalid
	}
	if out.Engine != CategorySuggestionModel {
		out.EndpointURL = stored.EndpointURL
		out.Model = stored.Model
		return out, nil
	}
	out.EndpointURL = strings.TrimRight(strings.TrimSpace(in.EndpointURL), "/")
	out.Model = strings.TrimSpace(in.Model)

	switch {
	case out.EndpointURL == "":
		return out, errSuggestionEndpointRequired
	case !isDecisionModelEndpoint(out.EndpointURL):
		return out, errSuggestionEndpointInvalid
	}
	switch {
	case out.Model == "":
		return out, errSuggestionModelRequired
	case len(out.Model) > 100 || strings.ContainsFunc(out.Model, isControl):
		return out, errSuggestionModelInvalid
	}
	switch {
	case in.APIKey != "" && (len(in.APIKey) > 1024 || strings.ContainsFunc(in.APIKey, isControl)):
		return out, errSuggestionAPIKeyInvalid
	case in.APIKey == "" && stored.SealedAPIKey == "":
		return out, errSuggestionAPIKeyRequired
	}
	return out, nil
}

// isDecisionModelEndpoint accepts an absolute https URL with a host and nothing
// a "/decisions" path could not be appended to.
func isDecisionModelEndpoint(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && !strings.ContainsFunc(s, isSpaceOrControl)
}

// Config answers the effective configuration the request path uses, with the
// API key unsealed in memory. It takes no actor: it is read while a requester
// writes a ticket, not from the settings screen. An engine other than the
// decision model never opens the key, so Off or the local engine cannot fail
// here; when the engine is the decision model and the key cannot be read, the
// error carries ErrCategorySuggestionKeyUnreadable and the caller falls back to
// the local engine (S5).
func (s *CategorySuggestionSettingsService) Config(ctx context.Context) (CategorySuggestionConfig, error) {
	stored, err := s.store.GetCategorySuggestionSettings(ctx)
	if err != nil {
		return CategorySuggestionConfig{}, err
	}
	cfg := CategorySuggestionConfig{Engine: normalizedCategorySuggestionEngine(stored.Engine), EndpointURL: stored.EndpointURL, Model: stored.Model}
	if cfg.Engine != CategorySuggestionModel {
		return cfg, nil
	}
	if stored.SealedAPIKey == "" {
		return CategorySuggestionConfig{}, errors.Join(ErrCategorySuggestionKeyUnreadable, errSuggestionKeyUnreadable, errors.New("no stored api key"))
	}
	key, err := s.sealer.Open(stored.SealedAPIKey)
	if err != nil {
		return CategorySuggestionConfig{}, errors.Join(ErrCategorySuggestionKeyUnreadable, errSuggestionKeyUnreadable, err)
	}
	cfg.APIKey = string(key)
	return cfg, nil
}
