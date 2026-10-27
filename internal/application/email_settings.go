package application

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Email settings (issue #362): TKT delivers its notifications through the
// company's own SMTP server. The connection is generic SMTP (S1) with
// optional authentication (S2) and selectable encryption (S3), configured by
// root from the Settings screen and stored in the database (S4).

// EmailEncryption is how the SMTP connection is protected.
type EmailEncryption string

const (
	// EmailEncryptionNone speaks plain SMTP. It is only allowed without
	// credentials (S15): a password never crosses the network in clear.
	EmailEncryptionNone EmailEncryption = "none"
	// EmailEncryptionSTARTTLS upgrades a plain connection; the server must
	// offer STARTTLS or the send fails.
	EmailEncryptionSTARTTLS EmailEncryption = "starttls"
	// EmailEncryptionTLS opens the connection over TLS (implicit TLS).
	EmailEncryptionTLS EmailEncryption = "tls"
)

// DefaultSMTPPort is the port an empty port field means (S1).
const DefaultSMTPPort = 25

// AllowedEmailEncryptions lists the encryption modes in the order the
// settings form offers them.
func AllowedEmailEncryptions() []EmailEncryption {
	return []EmailEncryption{EmailEncryptionNone, EmailEncryptionSTARTTLS, EmailEncryptionTLS}
}

func isAllowedEmailEncryption(e EmailEncryption) bool {
	for _, allowed := range AllowedEmailEncryptions() {
		if e == allowed {
			return true
		}
	}
	return false
}

// EmailSettings is the stored email configuration. SealedPassword is the
// secret box output, never the password; Get blanks it and reports
// PasswordSet instead, so no read path can render it back (S8).
type EmailSettings struct {
	Enabled       bool
	Host          string
	Port          int
	Encryption    EmailEncryption
	SkipTLSVerify bool
	Username      string
	// SealedPassword is set only on the store side of the port.
	SealedPassword string
	// PasswordSet is set only on values returned by Get.
	PasswordSet bool
	FromAddress string
	FromName    string
	BaseURL     string
}

// EmailSettingsInput is one submission of the settings form. An empty
// Password keeps the stored one; an empty Username removes the credentials
// (an open relay, S2).
type EmailSettingsInput struct {
	Enabled       bool
	Host          string
	Port          int
	Encryption    EmailEncryption
	SkipTLSVerify bool
	Username      string
	Password      string
	FromAddress   string
	FromName      string
	BaseURL       string
}

// SMTPConfig is everything a mailer needs to deliver one message, with the
// password already unsealed. It lives only in memory for one send.
type SMTPConfig struct {
	Host          string
	Port          int
	Encryption    EmailEncryption
	SkipTLSVerify bool
	Username      string
	Password      string
	FromAddress   string
	FromName      string
}

// String keeps the password out of every log line and error that formats
// a config with %v or %+v (S8).
func (c SMTPConfig) String() string {
	return fmt.Sprintf("{%s:%d %s from=%s user=%q password=%t}", c.Host, c.Port, c.Encryption, c.FromAddress, c.Username, c.Password != "")
}

// GoString covers %#v, which bypasses String.
func (c SMTPConfig) GoString() string { return c.String() }

// OutgoingEmail is one rendered plain-text message (S11).
type OutgoingEmail struct {
	To      string
	Subject string
	Body    string
}

// DeliveryConfig is what the background sender needs: the SMTP connection,
// the public base URL for ticket links (S10), and the instance language the
// copy is rendered in.
type DeliveryConfig struct {
	SMTP     SMTPConfig
	BaseURL  string
	Language string
}

// EmailSettingsStore persists the email settings as one unit: a save writes
// every field in one transaction, so a half-applied configuration is never
// readable.
type EmailSettingsStore interface {
	GetEmailSettings(ctx context.Context) (EmailSettings, error)
	SetEmailSettings(ctx context.Context, settings EmailSettings) error
}

// SecretSealer encrypts a secret for storage and decrypts it for use.
type SecretSealer interface {
	Seal(plaintext []byte) (string, error)
	Open(sealed string) ([]byte, error)
}

// Mailer delivers one message through an SMTP server.
type Mailer interface {
	Send(ctx context.Context, cfg SMTPConfig, email OutgoingEmail) error
}

// LanguageSource answers the instance interface language (issue #335);
// SettingsStore satisfies it.
type LanguageSource interface {
	GetInterfaceLanguage(ctx context.Context) (string, error)
}

// MessageRenderer resolves a catalogue message in one language. Emails are
// rendered outside any request, so the language is explicit.
type MessageRenderer interface {
	Render(language string, m Msg) string
}

// EmailDeliveryError reports that the SMTP server refused or could not be
// reached. Detail is the transport's own error text, which never contains
// the password.
type EmailDeliveryError struct {
	Detail string
}

func (e *EmailDeliveryError) Error() string { return "email delivery failed: " + e.Detail }

// EmailSettingsService implements the email settings use cases. Every one of
// them is root-only (CapManageEmail): the SMTP credentials are the
// instance's to give away, not an administrator's.
type EmailSettingsService struct {
	store       EmailSettingsStore
	sealer      SecretSealer
	mailer      Mailer
	language    LanguageSource
	renderer    MessageRenderer
	preferences LanguagePreferencesStore
}

// NewEmailSettingsService wires the email settings use cases.
func NewEmailSettingsService(store EmailSettingsStore, sealer SecretSealer, mailer Mailer, language LanguageSource, renderer MessageRenderer) *EmailSettingsService {
	return &EmailSettingsService{store: store, sealer: sealer, mailer: mailer, language: language, renderer: renderer}
}

// WithLanguagePreferences enables recipient-specific email copy.
func (s *EmailSettingsService) WithLanguagePreferences(p LanguagePreferencesStore) *EmailSettingsService {
	s.preferences = p
	return s
}

func recipientEmailLanguage(ctx context.Context, p LanguagePreferencesStore, userID int64, fallback string) (string, error) {
	if p == nil {
		return fallback, nil
	}
	language, err := p.GetLanguage(ctx, userID)
	if err != nil {
		return "", err
	}
	if language == "en" || language == "es" {
		return language, nil
	}
	return fallback, nil
}

func requireManageEmail(actor domain.User) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageEmail) {
		return domain.NewForbiddenError("email settings are not permitted")
	}
	return nil
}

// Get returns the stored settings without the password: PasswordSet says
// whether one is stored, and SealedPassword is always empty.
func (s *EmailSettingsService) Get(ctx context.Context, actor domain.User) (EmailSettings, error) {
	if err := requireManageEmail(actor); err != nil {
		return EmailSettings{}, err
	}
	stored, err := s.store.GetEmailSettings(ctx)
	if err != nil {
		return EmailSettings{}, err
	}
	return withoutPassword(stored), nil
}

func withoutPassword(stored EmailSettings) EmailSettings {
	stored.PasswordSet = stored.SealedPassword != ""
	stored.SealedPassword = ""
	return stored
}

// Save validates the whole submission and only then writes it (#345: a
// rejected save changes nothing). A new password is sealed before the write,
// an empty password field keeps the stored one, and removing the username
// removes the credentials.
func (s *EmailSettingsService) Save(ctx context.Context, actor domain.User, in EmailSettingsInput) error {
	if err := requireManageEmail(actor); err != nil {
		return err
	}
	stored, err := s.store.GetEmailSettings(ctx)
	if err != nil {
		return err
	}
	next, err := normalizeEmailInput(in, stored.SealedPassword != "")
	if err != nil {
		return err
	}
	switch {
	case next.Username == "":
		next.SealedPassword = ""
	case in.Password != "":
		sealed, err := s.sealer.Seal([]byte(in.Password))
		if err != nil {
			return fmt.Errorf("seal smtp password: %w", err)
		}
		next.SealedPassword = sealed
	default:
		next.SealedPassword = stored.SealedPassword
	}
	return s.store.SetEmailSettings(ctx, next)
}

// Email settings validation errors. Each is a catalogue-backed sentence the
// settings form shows next to the field it names.
var (
	errEmailHostRequired              = &domain.ValidationError{Field: "host", Message: "enter the SMTP server to turn email on", Key: "error.email_host_required"}
	errEmailHostInvalid               = &domain.ValidationError{Field: "host", Message: "the SMTP server must be a host name or IP address without spaces", Key: "error.email_host_invalid"}
	errEmailPortInvalid               = &domain.ValidationError{Field: "port", Message: "the port must be a number from 1 to 65535", Key: "error.email_port_invalid"}
	errEmailEncryptionInvalid         = &domain.ValidationError{Field: "encryption", Message: "choose none, STARTTLS, or TLS", Key: "error.email_encryption_invalid"}
	errEmailFromAddressRequired       = &domain.ValidationError{Field: "from_address", Message: "enter the sender address to turn email on", Key: "error.email_from_address_required"}
	errEmailFromAddressInvalid        = &domain.ValidationError{Field: "from_address", Message: "the sender address must be a single email address, like tkt@example.com", Key: "error.email_from_address_invalid"}
	errEmailFromNameInvalid           = &domain.ValidationError{Field: "from_name", Message: "the sender name must fit on one line and have at most 100 characters", Key: "error.email_from_name_invalid"}
	errEmailBaseUrlRequired           = &domain.ValidationError{Field: "base_url", Message: "enter the TKT address to turn email on: emails link to tickets with it", Key: "error.email_base_url_required"}
	errEmailBaseUrlInvalid            = &domain.ValidationError{Field: "base_url", Message: "the TKT address must be an http or https URL, like https://tkt.example.com", Key: "error.email_base_url_invalid"}
	errEmailUsernameInvalid           = &domain.ValidationError{Field: "username", Message: "the username must fit on one line and have at most 255 characters", Key: "error.email_username_invalid"}
	errEmailPasswordInvalid           = &domain.ValidationError{Field: "password", Message: "the password must fit on one line", Key: "error.email_password_invalid"}
	errEmailUsernameRequired          = &domain.ValidationError{Field: "username", Message: "enter the username that goes with the password", Key: "error.email_username_required"}
	errEmailPasswordRequired          = &domain.ValidationError{Field: "password", Message: "enter the password for this username", Key: "error.email_password_required"}
	errEmailCredentialsNeedEncryption = &domain.ValidationError{Field: "encryption", Message: "a username and password need STARTTLS or TLS: they are never sent unencrypted", Key: "error.email_credentials_need_encryption"}
	errEmailNotConfigured             = &domain.ValidationError{Field: "host", Message: "save an SMTP server and a sender address before sending a test email", Key: "error.email_not_configured"}
	errEmailPasswordUnreadable        = &domain.ValidationError{Field: "password", Message: "the saved SMTP password cannot be read on this server; enter it again", Key: "error.email_password_unreadable"}
)

// normalizeEmailInput trims and validates one submission. Host, sender
// address and base URL are required only to enable sending, so a disabled
// configuration can be saved half-filled; every value that is present must
// be valid either way.
func normalizeEmailInput(in EmailSettingsInput, passwordStored bool) (EmailSettings, error) {
	out := EmailSettings{
		Enabled:       in.Enabled,
		Host:          strings.TrimSpace(in.Host),
		Port:          in.Port,
		Encryption:    in.Encryption,
		SkipTLSVerify: in.SkipTLSVerify,
		Username:      strings.TrimSpace(in.Username),
		FromAddress:   strings.TrimSpace(in.FromAddress),
		FromName:      strings.TrimSpace(in.FromName),
		BaseURL:       strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"),
	}
	if out.Port == 0 {
		out.Port = DefaultSMTPPort
	}
	if out.Encryption == "" {
		out.Encryption = EmailEncryptionNone
	}

	switch {
	case out.Host == "" && out.Enabled:
		return out, errEmailHostRequired
	case out.Host != "" && (len(out.Host) > 253 || strings.ContainsFunc(out.Host, isSpaceOrControl)):
		return out, errEmailHostInvalid
	}
	if out.Port < 1 || out.Port > 65535 {
		return out, errEmailPortInvalid
	}
	if !isAllowedEmailEncryption(out.Encryption) {
		return out, errEmailEncryptionInvalid
	}
	if out.Encryption == EmailEncryptionNone {
		out.SkipTLSVerify = false
	}
	switch {
	case out.FromAddress == "" && out.Enabled:
		return out, errEmailFromAddressRequired
	case out.FromAddress != "" && !domain.IsSingleMailAddress(out.FromAddress):
		return out, errEmailFromAddressInvalid
	}
	if len(out.FromName) > 100 || strings.ContainsFunc(out.FromName, isControl) {
		return out, errEmailFromNameInvalid
	}
	switch {
	case out.BaseURL == "" && out.Enabled:
		return out, errEmailBaseUrlRequired
	case out.BaseURL != "" && !isPublicBaseURL(out.BaseURL):
		return out, errEmailBaseUrlInvalid
	}
	if len(out.Username) > 255 || strings.ContainsFunc(out.Username, isControl) {
		return out, errEmailUsernameInvalid
	}
	if len(in.Password) > 1024 || strings.ContainsFunc(in.Password, isControl) {
		return out, errEmailPasswordInvalid
	}
	switch {
	case out.Username == "" && in.Password != "":
		return out, errEmailUsernameRequired
	case out.Username != "" && in.Password == "" && !passwordStored:
		return out, errEmailPasswordRequired
	case out.Username != "" && out.Encryption == EmailEncryptionNone:
		return out, errEmailCredentialsNeedEncryption
	}
	return out, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func isSpaceOrControl(r rune) bool { return r == ' ' || isControl(r) }

// isPublicBaseURL accepts an absolute http(s) URL with a host and nothing a
// ticket path could not be appended to.
func isPublicBaseURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && !strings.ContainsFunc(s, isSpaceOrControl)
}

// SendTest sends one test email to the acting root's own address with the
// stored settings, enabled or not, so root can try a server before turning
// delivery on. It never sends to another address, so the action cannot be
// used as a relay.
func (s *EmailSettingsService) SendTest(ctx context.Context, actor domain.User) error {
	if err := requireManageEmail(actor); err != nil {
		return err
	}
	stored, err := s.store.GetEmailSettings(ctx)
	if err != nil {
		return err
	}
	if stored.Host == "" || stored.FromAddress == "" {
		return errEmailNotConfigured
	}
	cfg, err := s.smtpConfig(stored)
	if err != nil {
		return err
	}
	language, err := s.language.GetInterfaceLanguage(ctx)
	if err != nil {
		return err
	}
	language, err = recipientEmailLanguage(ctx, s.preferences, actor.ID, language)
	if err != nil {
		return err
	}
	email := OutgoingEmail{
		To:      actor.Email,
		Subject: s.renderer.Render(language, MsgRef("email.test.subject")),
		Body:    s.renderer.Render(language, MsgRef("email.test.body", Text(stored.Host))),
	}
	if err := s.mailer.Send(ctx, cfg, email); err != nil {
		return &EmailDeliveryError{Detail: err.Error()}
	}
	return nil
}

// DeliveryConfig answers the configuration the background sender uses for
// one pass. ok is false while sending is disabled or the configuration is
// incomplete; the sender then delivers nothing. It is read on every pass, so
// a saved change applies without a restart (S4).
func (s *EmailSettingsService) DeliveryConfig(ctx context.Context) (DeliveryConfig, bool, error) {
	stored, err := s.store.GetEmailSettings(ctx)
	if err != nil {
		return DeliveryConfig{}, false, err
	}
	if !stored.Enabled || stored.Host == "" || stored.FromAddress == "" || stored.BaseURL == "" {
		return DeliveryConfig{}, false, nil
	}
	cfg, err := s.smtpConfig(stored)
	if err != nil {
		return DeliveryConfig{}, false, err
	}
	language, err := s.language.GetInterfaceLanguage(ctx)
	if err != nil {
		return DeliveryConfig{}, false, err
	}
	return DeliveryConfig{SMTP: cfg, BaseURL: stored.BaseURL, Language: language}, true, nil
}

func (s *EmailSettingsService) smtpConfig(stored EmailSettings) (SMTPConfig, error) {
	cfg := SMTPConfig{
		Host:          stored.Host,
		Port:          stored.Port,
		Encryption:    stored.Encryption,
		SkipTLSVerify: stored.SkipTLSVerify,
		Username:      stored.Username,
		FromAddress:   stored.FromAddress,
		FromName:      stored.FromName,
	}
	if stored.SealedPassword != "" {
		password, err := s.sealer.Open(stored.SealedPassword)
		if err != nil {
			// Typically a database restored without its key file (S9): root
			// re-enters the password.
			return SMTPConfig{}, errors.Join(errEmailPasswordUnreadable, err)
		}
		cfg.Password = string(password)
	}
	return cfg, nil
}
