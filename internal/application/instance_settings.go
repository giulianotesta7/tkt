package application

import (
	"context"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// InstanceAddress is shared by email delivery, password recovery and company
// sign-in. PreviousCompanyAddress is retained migration evidence, not a fallback.
type InstanceAddress struct {
	Address                string
	PreviousCompanyAddress string
}

type InstanceAddressEvent struct {
	ActorUserID     int64
	PreviousAddress string
	Address         string
	CreatedAt       time.Time
}

// InstanceSettingsStore serializes address changes with consumer configuration
// writes. A clear is refused while any consumer is enabled. Audit and value
// commit together; unchanged saves produce no event.
type InstanceSettingsStore interface {
	GetInstanceAddress(context.Context) (InstanceAddress, error)
	SetInstanceAddress(context.Context, int64, string, time.Time) error
	ListInstanceAddressEvents(context.Context) ([]InstanceAddressEvent, error)
}

func InstanceAddressRequiredError() error {
	return &domain.ValidationError{Field: "instance_address", Message: "set the TKT address in Settings → Instance before enabling email or company sign-in", Key: "error.instance_address_required"}
}

// LegacyInstanceAddressError rejects an address submitted to a consumer
// service: the address belongs to Instance settings alone, and acknowledging
// it as saved would hide that nothing changed.
func LegacyInstanceAddressError(field string) error {
	return &domain.ValidationError{Field: field, Message: "change the TKT address in Instance settings", Key: "error.legacy_instance_address"}
}

// legacyAddressConflicts reports a submitted address that differs from the
// canonical one. An empty submission means the caller supplied none.
func legacyAddressConflicts(submitted, canonical string) bool {
	submitted = strings.TrimRight(strings.TrimSpace(submitted), "/")
	return submitted != "" && submitted != strings.TrimRight(canonical, "/")
}

func InstanceAddressInUseError() error {
	return &domain.ValidationError{Field: "instance_address", Message: "turn off email and company sign-in before clearing the TKT address", Key: "error.instance_address_in_use"}
}

type InstanceSettingsService struct {
	store InstanceSettingsStore
	clock domain.Clock
}

func NewInstanceSettingsService(store InstanceSettingsStore, clock domain.Clock) *InstanceSettingsService {
	return &InstanceSettingsService{store: store, clock: clock}
}

func requireManageInstance(actor domain.User) error {
	if actor.Role != domain.RoleRoot {
		return domain.NewForbiddenError("instance settings are not permitted")
	}
	return nil
}

func (s *InstanceSettingsService) Get(ctx context.Context, actor domain.User) (InstanceAddress, error) {
	if err := requireManageInstance(actor); err != nil {
		return InstanceAddress{}, err
	}
	return s.store.GetInstanceAddress(ctx)
}

func (s *InstanceSettingsService) Save(ctx context.Context, actor domain.User, address string) error {
	if err := requireManageInstance(actor); err != nil {
		return err
	}
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	if address != "" && !isPublicBaseURL(address) {
		return &domain.ValidationError{Field: "instance_address", Message: "the TKT address must be an http or https URL, like https://tkt.example.com", Key: "error.email_base_url_invalid"}
	}
	if isSignInRoutePath(address) {
		return &domain.ValidationError{Field: "instance_address", Message: "enter only the address of TKT, such as https://tkt.example.com, not a sign-in callback URL; TKT adds the callback path itself", Key: "error.company_signin_address_callback"}
	}
	return s.store.SetInstanceAddress(ctx, actor.ID, address, s.clock.Now())
}
