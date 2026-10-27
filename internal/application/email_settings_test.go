package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// fakeEmailSettingsStore holds the stored email settings and counts writes,
// so a rejected or denied save can be proven to have persisted nothing.
type fakeEmailSettingsStore struct {
	stored   EmailSettings
	getErr   error
	setErr   error
	setCalls int
}

func (f *fakeEmailSettingsStore) GetEmailSettings(_ context.Context) (EmailSettings, error) {
	if f.getErr != nil {
		return EmailSettings{}, f.getErr
	}
	return f.stored, nil
}

func (f *fakeEmailSettingsStore) SetEmailSettings(_ context.Context, s EmailSettings) error {
	f.setCalls++
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = s
	return nil
}

// fakeSealer is a reversible, visibly-marked stand-in for the secret box: it
// lets a test read back what was sealed while proving the stored value is
// never the plaintext.
type fakeSealer struct {
	sealErr error
	openErr error
}

func (f *fakeSealer) Seal(plaintext []byte) (string, error) {
	if f.sealErr != nil {
		return "", f.sealErr
	}
	return "sealed(" + reverse(string(plaintext)) + ")", nil
}

func (f *fakeSealer) Open(sealed string) ([]byte, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	inner, ok := strings.CutPrefix(sealed, "sealed(")
	if !ok || !strings.HasSuffix(inner, ")") {
		return nil, errors.New("fakeSealer: not sealed")
	}
	return []byte(reverse(strings.TrimSuffix(inner, ")"))), nil
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// fakeMailer records every send and fails on demand.
type fakeMailer struct {
	sent    []sentMail
	sendErr error
}

type sentMail struct {
	config SMTPConfig
	email  OutgoingEmail
}

func (f *fakeMailer) Send(_ context.Context, cfg SMTPConfig, email OutgoingEmail) error {
	f.sent = append(f.sent, sentMail{config: cfg, email: email})
	return f.sendErr
}

// fakeRenderer renders a message as "<lang>:<key>[args]" so a test can
// assert which sentence was chosen and in which language.
type fakeRenderer struct{}

func (fakeRenderer) Render(language string, m Msg) string {
	parts := make([]string, 0, len(m.Args))
	for _, a := range m.Args {
		switch {
		case a.Msg != nil:
			parts = append(parts, fakeRenderer{}.Render(language, *a.Msg))
		case a.Key != "":
			parts = append(parts, a.Key)
		default:
			parts = append(parts, a.Value)
		}
	}
	return language + ":" + m.Key + "[" + strings.Join(parts, ",") + "]"
}

type emailSettingsFixture struct {
	svc      *EmailSettingsService
	store    *fakeEmailSettingsStore
	sealer   *fakeSealer
	mailer   *fakeMailer
	language *fakeSettingsStore
}

func newEmailSettingsFixture() emailSettingsFixture {
	f := emailSettingsFixture{
		store:    &fakeEmailSettingsStore{},
		sealer:   &fakeSealer{},
		mailer:   &fakeMailer{},
		language: &fakeSettingsStore{language: "es"},
	}
	f.svc = NewEmailSettingsService(f.store, f.sealer, f.mailer, f.language, fakeRenderer{})
	return f
}

func emailRoot() domain.User {
	return domain.User{ID: 1, Name: "Root", Email: "root@example.com", Role: domain.RoleRoot, Active: true}
}

// validEmailInput is a complete, enabled configuration with credentials over
// STARTTLS: each test changes one thing from it.
func validEmailInput() EmailSettingsInput {
	return EmailSettingsInput{
		Enabled:     true,
		Host:        "smtp.example.com",
		Port:        587,
		Encryption:  EmailEncryptionSTARTTLS,
		Username:    "tkt",
		Password:    "s3cret",
		FromAddress: "tkt@example.com",
		FromName:    "TKT",
		BaseURL:     "https://tkt.example.com",
	}
}

func TestSaveEmailSettingsIsRootOnly(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent, domain.RoleAdmin} {
		f := newEmailSettingsFixture()
		actor := domain.User{ID: 2, Role: role, Active: true, Email: "x@example.com"}
		err := f.svc.Save(context.Background(), actor, validEmailInput())
		var forbidden *domain.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("%s: Save = %v, want ForbiddenError", role, err)
		}
		if _, err := f.svc.Get(context.Background(), actor); !errors.As(err, &forbidden) {
			t.Errorf("%s: Get = %v, want ForbiddenError", role, err)
		}
		if err := f.svc.SendTest(context.Background(), actor); !errors.As(err, &forbidden) {
			t.Errorf("%s: SendTest = %v, want ForbiddenError", role, err)
		}
		if f.store.setCalls != 0 || len(f.mailer.sent) != 0 {
			t.Errorf("%s: denied actor wrote %d times and sent %d emails", role, f.store.setCalls, len(f.mailer.sent))
		}
	}
}

func TestSaveEmailSettingsSealsPasswordAndGetNeverReturnsIt(t *testing.T) {
	f := newEmailSettingsFixture()
	if err := f.svc.Save(context.Background(), emailRoot(), validEmailInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.store.stored.SealedPassword != "sealed(terc3s)" {
		t.Fatalf("stored password = %q, want the sealed form", f.store.stored.SealedPassword)
	}
	got, err := f.svc.Get(context.Background(), emailRoot())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SealedPassword != "" || !got.PasswordSet {
		t.Fatalf("Get exposes SealedPassword=%q PasswordSet=%v; want empty and true", got.SealedPassword, got.PasswordSet)
	}
	want := validEmailInput()
	if got.Host != want.Host || got.Port != want.Port || got.Encryption != want.Encryption ||
		got.Username != want.Username || got.FromAddress != want.FromAddress ||
		got.FromName != want.FromName || got.BaseURL != want.BaseURL || !got.Enabled {
		t.Fatalf("Get = %+v, want the saved values", got)
	}
}

func TestSaveEmailSettingsKeepsReplacesOrClearsPassword(t *testing.T) {
	f := newEmailSettingsFixture()
	ctx := context.Background()
	if err := f.svc.Save(ctx, emailRoot(), validEmailInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	keep := validEmailInput()
	keep.Password = ""
	keep.FromName = "Help desk"
	if err := f.svc.Save(ctx, emailRoot(), keep); err != nil {
		t.Fatalf("Save keeping password: %v", err)
	}
	if f.store.stored.SealedPassword != "sealed(terc3s)" {
		t.Fatalf("empty password field changed the stored password to %q", f.store.stored.SealedPassword)
	}

	replace := validEmailInput()
	replace.Password = "other"
	if err := f.svc.Save(ctx, emailRoot(), replace); err != nil {
		t.Fatalf("Save replacing password: %v", err)
	}
	if f.store.stored.SealedPassword != "sealed(rehto)" {
		t.Fatalf("stored password = %q, want the new sealed value", f.store.stored.SealedPassword)
	}

	// Dropping the username drops the credentials: an open relay (S2).
	open := validEmailInput()
	open.Username, open.Password = "", ""
	if err := f.svc.Save(ctx, emailRoot(), open); err != nil {
		t.Fatalf("Save without credentials: %v", err)
	}
	if f.store.stored.SealedPassword != "" || f.store.stored.Username != "" {
		t.Fatalf("credentials survived an open-relay save: %+v", f.store.stored)
	}
}

func TestSaveEmailSettingsDefaultsPortTo25AndAllowsOpenRelayWithoutEncryption(t *testing.T) {
	f := newEmailSettingsFixture()
	in := validEmailInput()
	in.Port = 0
	in.Encryption = EmailEncryptionNone
	in.Username, in.Password = "", ""
	in.SkipTLSVerify = true
	if err := f.svc.Save(context.Background(), emailRoot(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.store.stored.Port != DefaultSMTPPort || DefaultSMTPPort != 25 {
		t.Fatalf("port = %d, want 25", f.store.stored.Port)
	}
	if f.store.stored.SkipTLSVerify {
		t.Fatal("SkipTLSVerify stored for an unencrypted connection")
	}
}

func TestSaveEmailSettingsValidatesBeforeWriting(t *testing.T) {
	cases := map[string]struct {
		mutate func(*EmailSettingsInput)
		key    string
	}{
		"host required":        {func(in *EmailSettingsInput) { in.Host = " " }, "error.email_host_required"},
		"host with space":      {func(in *EmailSettingsInput) { in.Host = "smtp example.com" }, "error.email_host_invalid"},
		"port too high":        {func(in *EmailSettingsInput) { in.Port = 70000 }, "error.email_port_invalid"},
		"port negative":        {func(in *EmailSettingsInput) { in.Port = -1 }, "error.email_port_invalid"},
		"encryption unknown":   {func(in *EmailSettingsInput) { in.Encryption = "ssl3" }, "error.email_encryption_invalid"},
		"from required":        {func(in *EmailSettingsInput) { in.FromAddress = "" }, "error.email_from_address_required"},
		"from invalid":         {func(in *EmailSettingsInput) { in.FromAddress = "not-an-address" }, "error.email_from_address_invalid"},
		"from with name":       {func(in *EmailSettingsInput) { in.FromAddress = "TKT <tkt@example.com>" }, "error.email_from_address_invalid"},
		"from name newline":    {func(in *EmailSettingsInput) { in.FromName = "TKT\r\nBcc: x@example.com" }, "error.email_from_name_invalid"},
		"base url required":    {func(in *EmailSettingsInput) { in.BaseURL = "" }, "error.email_base_url_required"},
		"base url scheme":      {func(in *EmailSettingsInput) { in.BaseURL = "ftp://tkt.example.com" }, "error.email_base_url_invalid"},
		"base url no host":     {func(in *EmailSettingsInput) { in.BaseURL = "https://" }, "error.email_base_url_invalid"},
		"base url query":       {func(in *EmailSettingsInput) { in.BaseURL = "https://tkt.example.com/?a=b" }, "error.email_base_url_invalid"},
		"base url userinfo":    {func(in *EmailSettingsInput) { in.BaseURL = "https://u:p@tkt.example.com" }, "error.email_base_url_invalid"},
		"username newline":     {func(in *EmailSettingsInput) { in.Username = "a\nb" }, "error.email_username_invalid"},
		"password newline":     {func(in *EmailSettingsInput) { in.Password = "a\r\nb" }, "error.email_password_invalid"},
		"username no password": {func(in *EmailSettingsInput) { in.Password = "" }, "error.email_password_required"},
		"password no username": {func(in *EmailSettingsInput) { in.Username = "" }, "error.email_username_required"},
		"credentials in clear": {func(in *EmailSettingsInput) { in.Encryption = EmailEncryptionNone }, "error.email_credentials_need_encryption"},
	}
	for name, tc := range cases {
		f := newEmailSettingsFixture()
		in := validEmailInput()
		tc.mutate(&in)
		err := f.svc.Save(context.Background(), emailRoot(), in)
		var verr *domain.ValidationError
		if !errors.As(err, &verr) || verr.Key != tc.key {
			t.Errorf("%s: Save = %v, want ValidationError %s", name, err, tc.key)
		}
		if f.store.setCalls != 0 {
			t.Errorf("%s: a rejected save wrote the store", name)
		}
	}
}

func TestSaveEmailSettingsDisabledAllowsAnEmptyForm(t *testing.T) {
	f := newEmailSettingsFixture()
	if err := f.svc.Save(context.Background(), emailRoot(), EmailSettingsInput{}); err != nil {
		t.Fatalf("Save disabled empty form: %v", err)
	}
	if f.store.stored.Enabled || f.store.stored.Port != DefaultSMTPPort || f.store.stored.Encryption != EmailEncryptionNone {
		t.Fatalf("stored = %+v, want disabled, port 25, no encryption", f.store.stored)
	}
}

func TestSaveEmailSettingsSealFailureWritesNothing(t *testing.T) {
	f := newEmailSettingsFixture()
	f.sealer.sealErr = errors.New("disk full")
	if err := f.svc.Save(context.Background(), emailRoot(), validEmailInput()); err == nil {
		t.Fatal("Save succeeded although the password could not be sealed")
	}
	if f.store.setCalls != 0 {
		t.Fatal("Save wrote the store after a seal failure")
	}
}

func TestSendTestEmailGoesToTheActingRootInTheInstanceLanguage(t *testing.T) {
	f := newEmailSettingsFixture()
	if err := f.svc.Save(context.Background(), emailRoot(), validEmailInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := f.svc.SendTest(context.Background(), emailRoot()); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if len(f.mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(f.mailer.sent))
	}
	got := f.mailer.sent[0]
	if got.email.To != "root@example.com" {
		t.Fatalf("test email to %q, want the acting root", got.email.To)
	}
	if got.email.Subject != "es:email.test.subject[]" || !strings.HasPrefix(got.email.Body, "es:email.test.body[") {
		t.Fatalf("test email copy = %q / %q, want the es catalogue sentences", got.email.Subject, got.email.Body)
	}
	want := SMTPConfig{Host: "smtp.example.com", Port: 587, Encryption: EmailEncryptionSTARTTLS,
		Username: "tkt", Password: "s3cret", FromAddress: "tkt@example.com", FromName: "TKT"}
	if got.config != want {
		t.Fatalf("config = %+v, want %+v", got.config, want)
	}
}

func TestTestEmailRecipientLanguage(t *testing.T) {
	f := newEmailSettingsFixture()
	prefs := &languageTestStore{values: map[int64]string{emailRoot().ID: "en"}}
	wire, ok := any(f.svc).(interface {
		WithLanguagePreferences(LanguagePreferencesStore) *EmailSettingsService
	})
	if !ok {
		t.Fatal("test email service cannot use recipient language preferences")
	}
	wire.WithLanguagePreferences(prefs)
	if err := f.svc.Save(context.Background(), emailRoot(), validEmailInput()); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SendTest(context.Background(), emailRoot()); err != nil {
		t.Fatal(err)
	}
	if len(f.mailer.sent) != 1 || f.mailer.sent[0].email.Subject != "en:email.test.subject[]" {
		t.Fatalf("sent: %+v", f.mailer.sent)
	}
	if !strings.Contains(f.mailer.sent[0].email.Body, "smtp.example.com") {
		t.Fatal("host changed")
	}
}

func TestTestEmailLanguageInheritanceAndFailure(t *testing.T) {
	f := newEmailSettingsFixture()
	prefs := &languageTestStore{values: map[int64]string{}}
	f.svc.WithLanguagePreferences(prefs)
	if err := f.svc.Save(context.Background(), emailRoot(), validEmailInput()); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SendTest(context.Background(), emailRoot()); err != nil {
		t.Fatal(err)
	}
	if len(f.mailer.sent) != 1 || f.mailer.sent[0].email.Subject != "es:email.test.subject[]" {
		t.Fatalf("sent: %+v", f.mailer.sent)
	}
	prefs.err = errors.New("language unavailable")
	if err := f.svc.SendTest(context.Background(), emailRoot()); !errors.Is(err, prefs.err) {
		t.Fatalf("error: %v", err)
	}
	if len(f.mailer.sent) != 1 {
		t.Fatal("sent after preference failure")
	}
}

func TestSendTestEmailWorksBeforeEnablingButNeedsAServer(t *testing.T) {
	f := newEmailSettingsFixture()
	err := f.svc.SendTest(context.Background(), emailRoot())
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Key != "error.email_not_configured" {
		t.Fatalf("SendTest unconfigured = %v, want error.email_not_configured", err)
	}

	in := validEmailInput()
	in.Enabled = false
	if err := f.svc.Save(context.Background(), emailRoot(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := f.svc.SendTest(context.Background(), emailRoot()); err != nil {
		t.Fatalf("SendTest while disabled: %v", err)
	}
}

func TestSendTestEmailReportsDeliveryAndKeyFailures(t *testing.T) {
	f := newEmailSettingsFixture()
	if err := f.svc.Save(context.Background(), emailRoot(), validEmailInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f.mailer.sendErr = errors.New("535 authentication failed")
	err := f.svc.SendTest(context.Background(), emailRoot())
	var delivery *EmailDeliveryError
	if !errors.As(err, &delivery) || !strings.Contains(delivery.Detail, "535 authentication failed") {
		t.Fatalf("SendTest = %v, want EmailDeliveryError carrying the SMTP reply", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatal("delivery error leaks the password")
	}

	f.mailer.sendErr = nil
	f.sealer.openErr = errors.New("key unavailable")
	err = f.svc.SendTest(context.Background(), emailRoot())
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Key != "error.email_password_unreadable" {
		t.Fatalf("SendTest with an unreadable password = %v, want error.email_password_unreadable", err)
	}
}

func TestDeliveryConfigIsOffUntilEnabled(t *testing.T) {
	f := newEmailSettingsFixture()
	ctx := context.Background()
	if _, ok, err := f.svc.DeliveryConfig(ctx); err != nil || ok {
		t.Fatalf("DeliveryConfig before configuration = %v, %v; want off", ok, err)
	}
	in := validEmailInput()
	in.Enabled = false
	if err := f.svc.Save(ctx, emailRoot(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok, _ := f.svc.DeliveryConfig(ctx); ok {
		t.Fatal("DeliveryConfig is on while sending is disabled")
	}
	if err := f.svc.Save(ctx, emailRoot(), validEmailInput()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, ok, err := f.svc.DeliveryConfig(ctx)
	if err != nil || !ok {
		t.Fatalf("DeliveryConfig enabled = %v, %v", ok, err)
	}
	if cfg.SMTP.Password != "s3cret" || cfg.BaseURL != "https://tkt.example.com" || cfg.Language != "es" {
		t.Fatalf("DeliveryConfig = %+v", cfg)
	}
}

func TestSaveEmailSettingsNormalizesBaseURLTrailingSlash(t *testing.T) {
	f := newEmailSettingsFixture()
	in := validEmailInput()
	in.BaseURL = "  https://tkt.example.com/helpdesk/  "
	if err := f.svc.Save(context.Background(), emailRoot(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.store.stored.BaseURL != "https://tkt.example.com/helpdesk" {
		t.Fatalf("BaseURL = %q, want trimmed without trailing slash", f.store.stored.BaseURL)
	}
}

func TestSMTPConfigNeverFormatsThePassword(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.example.com", Port: 587, Username: "tkt", Password: "s3cret"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, cfg); strings.Contains(got, "s3cret") {
			t.Errorf("%s formats the password: %s", verb, got)
		}
	}
	if got := fmt.Sprintf("%+v", DeliveryConfig{SMTP: cfg}); strings.Contains(got, "s3cret") {
		t.Errorf("DeliveryConfig formats the password: %s", got)
	}
}
