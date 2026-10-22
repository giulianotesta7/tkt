package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Organization is the instance identity the shell, the login page, the
// document titles and the emails render (issue #383, S3/S5). It is the domain
// value: the service adds the capability gate and the read/write order, and no
// layer re-declares the shape.
type Organization = domain.Organization

// OrganizationInput is one submission of the organization form. Logo, when
// non-nil, replaces the stored logo in the SAME save, so the text and the file
// of one submit land together or not at all; RemoveLogo drops it, and an
// unsubmitted file leaves the stored logo alone.
type OrganizationInput struct {
	Name         string
	HelpDesk     string
	SupportEmail string
	Logo         []byte
	RemoveLogo   bool
}

// OrganizationService implements the instance-identity use cases. The reads
// are ungated on purpose: the request middleware resolves the brand for every
// page, including the login page, which has no actor at all. Every write is
// admin/root only through CapManageSettings — the instance-configuration
// capability the other settings sections use — and re-enforced here after the
// HTTP boundary checks it.
type OrganizationService struct {
	store OrganizationStore
}

// NewOrganizationService wires the organization use cases against the identity
// port.
func NewOrganizationService(store OrganizationStore) *OrganizationService {
	return &OrganizationService{store: store}
}

// GetOrganization answers the stored identity, or the documented defaults when
// the instance never configured itself. It carries no capability gate: the
// brand is part of the shell every page renders.
func (s *OrganizationService) GetOrganization(ctx context.Context) (Organization, error) {
	return s.store.GetOrganization(ctx)
}

// UpdateOrganization validates one submission and writes it. Validation runs
// BEFORE any read or write — the text fields and the logo both — so a rejected
// submission stores nothing, and the store applies every accepted field in one
// transaction.
func (s *OrganizationService) UpdateOrganization(ctx context.Context, actor domain.User, in OrganizationInput) error {
	if err := requireManageSettings(actor, "organization settings are not permitted"); err != nil {
		return err
	}
	org, err := normalizeOrganizationInput(ctx, s.store, in)
	if err != nil {
		return err
	}
	return s.store.SetOrganization(ctx, org)
}

// SetLogo replaces the instance logo. The bytes must prove they are a PNG of
// at most the documented cap; a refused logo changes nothing.
func (s *OrganizationService) SetLogo(ctx context.Context, actor domain.User, logo []byte) error {
	if err := requireManageSettings(actor, "organization settings are not permitted"); err != nil {
		return err
	}
	org, err := s.currentOrganization(ctx)
	if err != nil {
		return err
	}
	return s.acceptLogo(ctx, org, logo)
}

// RemoveLogo drops the stored logo, leaving the name and help desk name as they
// are. Removing a logo an instance does not have is not an error: the state the
// caller asked for is the state it finds.
func (s *OrganizationService) RemoveLogo(ctx context.Context, actor domain.User) error {
	if err := requireManageSettings(actor, "organization settings are not permitted"); err != nil {
		return err
	}
	org, err := s.currentOrganization(ctx)
	if err != nil {
		return err
	}
	org.Logo = nil
	org.LogoMIME = ""
	return s.store.SetOrganization(ctx, org)
}

// currentOrganization reads the stored identity so one field can be changed
// without losing the others: every write is a whole-organization write.
func (s *OrganizationService) currentOrganization(ctx context.Context) (Organization, error) {
	return s.store.GetOrganization(ctx)
}

// acceptLogo validates logo and stores it on top of org.
func (s *OrganizationService) acceptLogo(ctx context.Context, org Organization, logo []byte) error {
	if err := domain.ValidateOrganizationLogo(logo); err != nil {
		return err
	}
	org.Logo = logo
	org.LogoMIME = domain.OrganizationLogoContentType
	return s.store.SetOrganization(ctx, org)
}

// normalizeOrganizationInput turns one submission into the identity to store,
// validating every field the form carries. It reads the stored organization
// only when the submission leaves the logo alone, so the caller's current logo
// survives a text-only save.
func normalizeOrganizationInput(ctx context.Context, store OrganizationStore, in OrganizationInput) (Organization, error) {
	org, err := domain.NormalizeOrganization(domain.OrganizationInput{
		Name:         in.Name,
		HelpDesk:     in.HelpDesk,
		SupportEmail: in.SupportEmail,
	})
	if err != nil {
		return Organization{}, err
	}
	switch {
	case len(in.Logo) > 0:
		if err := domain.ValidateOrganizationLogo(in.Logo); err != nil {
			return Organization{}, err
		}
		org.Logo = in.Logo
		org.LogoMIME = domain.OrganizationLogoContentType
	case in.RemoveLogo:
		org.Logo = nil
		org.LogoMIME = ""
	default:
		// The submission carried no file: the stored logo stays, so editing
		// the name never costs the instance its mark.
		current, err := store.GetOrganization(ctx)
		if err != nil {
			return Organization{}, err
		}
		org.Logo = current.Logo
		org.LogoMIME = current.LogoMIME
	}
	return org, nil
}

// requireManageSettings is the one instance-configuration gate: the actor must
// hold CapManageSettings (admin/root) or nothing is read or written. The
// message names the refusal the caller's surface owns, so an administrator
// reading a 403 still learns which section refused.
func requireManageSettings(actor domain.User, message string) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageSettings) {
		return domain.NewForbiddenError(message)
	}
	return nil
}
