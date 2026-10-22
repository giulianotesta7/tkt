package domain

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Organization identity (issue #383, S3): the instance's own name, help desk
// name, support email and logo. These are the values the shell, the login
// page, the document titles and the emails answer with, so they are
// deliberately few, short, and validated here: a name that cannot be rendered
// is refused before anything is stored.
const (
	// MaxOrganizationNameRunes bounds the instance name a screen has to fit.
	MaxOrganizationNameRunes = 60
	// MaxOrganizationHelpDeskRunes bounds the help desk name the shell shows
	// beside the brand.
	MaxOrganizationHelpDeskRunes = 40
	// MaxOrganizationLogoBytes is the logo cap. A logo is rendered at 24-32px,
	// so 256 KiB is generous for a PNG and bounds what one request can carry.
	MaxOrganizationLogoBytes = 256 << 10
	// DefaultOrganizationName is what an instance calls itself until an
	// administrator names it: the product name the interface shipped with.
	DefaultOrganizationName = "tkt"
	// DefaultOrganizationHelpDesk is the help desk name an empty field means.
	DefaultOrganizationHelpDesk = "Help desk"
	// OrganizationLogoContentType is the only logo type the instance serves.
	// PNG only, deliberately: an SVG is an executable document, and this bytes
	// value is answered inline on its own route.
	OrganizationLogoContentType = "image/png"
)

// English user-facing organization messages (D5: the domain is their single
// source). Each one is also the English catalogue entry a rejected form shows.
const (
	ErrMsgOrgNameRequired        = "enter the organization name"
	ErrMsgOrgNameInvalid         = "the organization name must fit on one line and have at most 60 characters"
	ErrMsgOrgHelpDeskNameInvalid = "the help desk name must fit on one line and have at most 40 characters"
	ErrMsgOrgSupportEmailInvalid = "the support email must be a single email address, like support@example.com"
	ErrMsgOrgLogoInvalid         = "the logo must be a PNG image"
	ErrMsgOrgLogoTooLarge        = "the logo must be 256 KiB or smaller"
)

// Organization is the instance identity as stored and as rendered: the name,
// the help desk name shown beside it, an optional support email, and the logo
// bytes with the type they are served as. An instance that never configured
// itself answers the defaults of GetOrganization, so every read path can
// render without asking whether the identity was set.
type Organization struct {
	Name         string
	HelpDesk     string
	SupportEmail string
	Logo         []byte
	LogoMIME     string
}

// OrganizationInput is one submission of the organization form.
type OrganizationInput struct {
	Name         string
	HelpDesk     string
	SupportEmail string
}

// HasLogo reports whether the instance carries a logo. The monogram is the
// fallback the shell renders when it does not.
func (o Organization) HasLogo() bool { return len(o.Logo) > 0 }

// Monogram is the brand mark the instance's name stands for: the first letters
// of up to two words, upper-cased. It is empty only for an empty name.
func (o Organization) Monogram() string { return Monogram(o.Name) }

// NormalizeOrganization trims and validates one submission. The name is
// required and 1-60 characters; an empty help desk name means the documented
// default; the support email is optional and must be one bare address. Every
// refusal names the field it belongs to and the catalogue sentence it renders,
// so the form can show it beside that field.
func NormalizeOrganization(in OrganizationInput) (Organization, error) {
	out := Organization{
		Name:         strings.TrimSpace(in.Name),
		HelpDesk:     strings.TrimSpace(in.HelpDesk),
		SupportEmail: strings.TrimSpace(in.SupportEmail),
	}
	switch {
	case out.Name == "":
		return Organization{}, &ValidationError{Field: "name", Message: ErrMsgOrgNameRequired, Key: "error.org_name_required"}
	case utf8.RuneCountInString(out.Name) > MaxOrganizationNameRunes || strings.ContainsFunc(out.Name, isLineBreakOrControl):
		return Organization{}, &ValidationError{Field: "name", Message: ErrMsgOrgNameInvalid, Key: "error.org_name_invalid"}
	}
	if out.HelpDesk == "" {
		out.HelpDesk = DefaultOrganizationHelpDesk
	} else if utf8.RuneCountInString(out.HelpDesk) > MaxOrganizationHelpDeskRunes || strings.ContainsFunc(out.HelpDesk, isLineBreakOrControl) {
		return Organization{}, &ValidationError{Field: "help_desk", Message: ErrMsgOrgHelpDeskNameInvalid, Key: "error.org_helpdesk_name_invalid"}
	}
	if out.SupportEmail != "" && !IsSingleMailAddress(out.SupportEmail) {
		return Organization{}, &ValidationError{Field: "support_email", Message: ErrMsgOrgSupportEmailInvalid, Key: "error.org_support_email_invalid"}
	}
	return out, nil
}

// ValidateOrganizationLogo accepts a PNG logo of at most the documented cap.
// The type is proven by the bytes, never by a claimed name or extension: this
// value is served back as an image, so a file that only claims to be a PNG is
// refused here.
func ValidateOrganizationLogo(data []byte) error {
	switch {
	case len(data) > MaxOrganizationLogoBytes:
		return &ValidationError{Field: "logo", Message: ErrMsgOrgLogoTooLarge, Key: "error.org_logo_too_large"}
	case !IsPNG(data):
		return &ValidationError{Field: "logo", Message: ErrMsgOrgLogoInvalid, Key: "error.org_logo_invalid"}
	}
	return nil
}

// Monogram answers the brand mark a name stands for: the first letters of up
// to two words, upper-cased. Words are separated by any whitespace, so a name
// typed with a double space still marks two letters.
func Monogram(name string) string {
	words := strings.Fields(name)
	mark := make([]rune, 0, 2)
	for _, word := range words[:min(len(words), 2)] {
		first, _ := utf8.DecodeRuneInString(word)
		mark = append(mark, unicode.ToUpper(first))
	}
	return string(mark)
}

// IsSingleMailAddress reports whether s is exactly one bare address: the mail
// parser accepts it and it carries no display name of its own, because a name
// smuggled into an address is a header no form shows.
func IsSingleMailAddress(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Name == "" && addr.Address == s
}

// isLineBreakOrControl reports a rune that would break the one-line chrome
// these values render as: a newline splits the shell, and a control character
// has no place in a name.
func isLineBreakOrControl(r rune) bool { return r < 0x20 || r == 0x7f }
