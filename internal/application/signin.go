package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// SigninInput is one submission of the sign-in page form: the panel's fields
// plus the three states a file field can have.
type SigninInput = domain.SigninInput

// SigninPanel is the stored panel configuration (issue #383, S9/S10).
type SigninPanel = domain.SigninPanel

// SigninConfiguration is everything the sign-in page renders: the panel and the
// background image with the type it is served as. An instance that never
// configured the panel answers the defaults, so the panel is never empty.
type SigninConfiguration struct {
	Panel          domain.SigninPanel
	Background     []byte
	BackgroundMIME string
}

// HasBackground reports whether the instance holds a background image.
func (c SigninConfiguration) HasBackground() bool { return len(c.Background) > 0 }

// SigninService implements the sign-in page use cases. The read is ungated on
// purpose: the login page renders before any session exists. Every write is
// admin/root only through CapManageSettings — the instance-configuration
// capability the other settings sections use — and re-enforced here after the
// HTTP boundary checks it.
type SigninService struct {
	store SigninStore
}

// NewSigninService wires the sign-in page use cases against the panel port.
func NewSigninService(store SigninStore) *SigninService {
	return &SigninService{store: store}
}

// GetSigninConfiguration answers the stored panel and image, or the documented
// defaults when the instance never configured itself. It carries no capability
// gate: the sign-in page is public.
func (s *SigninService) GetSigninConfiguration(ctx context.Context) (SigninConfiguration, error) {
	return s.store.GetSignin(ctx)
}

// UpdateSignin validates one submission and writes it. Validation runs BEFORE
// the write — every field, plus the image when one is submitted — so a rejected
// submission stores nothing, and the store applies the panel and the image in
// one transaction. A submission that carries no file keeps the stored image.
func (s *SigninService) UpdateSignin(ctx context.Context, actor domain.User, in SigninInput) error {
	if err := requireManageSettings(actor, "sign-in page settings are not permitted"); err != nil {
		return err
	}
	current, err := s.store.GetSignin(ctx)
	if err != nil {
		return err
	}
	panel, err := domain.NormalizeSignin(in, current.HasBackground())
	if err != nil {
		return err
	}
	background := current.Background
	switch {
	case in.RemoveBackground:
		background = nil
	case in.Background != nil:
		background = in.Background
	}
	return s.store.SetSignin(ctx, panel, background)
}

// RemoveBackground drops the stored background image and returns the panel to
// the dark surface, because a panel whose background style says "image" with no
// image would keep asking for a file the instance no longer has. Removing an
// image the instance does not have is not an error.
func (s *SigninService) RemoveBackground(ctx context.Context, actor domain.User) error {
	if err := requireManageSettings(actor, "sign-in page settings are not permitted"); err != nil {
		return err
	}
	current, err := s.store.GetSignin(ctx)
	if err != nil {
		return err
	}
	panel := current.Panel
	if panel.Background == domain.SigninBackgroundImage {
		panel.Background = domain.SigninBackgroundDark
	}
	return s.store.SetSignin(ctx, panel, nil)
}
