// Package mail delivers TKT's emails through the company's own SMTP server
// (issue #362): generic SMTP (S1) with optional authentication (S2) and
// selectable encryption — none, STARTTLS, or implicit TLS (S3). Messages are
// plain text (S11).
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	stdmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// sendTimeout bounds one whole delivery — connect, handshake, and
// conversation — so a silent server never holds the sender forever.
const sendTimeout = 30 * time.Second

// SMTPMailer implements application.Mailer with net/smtp.
type SMTPMailer struct {
	dial    func(ctx context.Context, addr string) (net.Conn, error)
	rootCAs *x509.CertPool // nil means the system roots
	now     func() time.Time
}

var _ application.Mailer = (*SMTPMailer)(nil)

// NewSMTPMailer returns a mailer that dials TCP and trusts the system roots.
func NewSMTPMailer() *SMTPMailer {
	return &SMTPMailer{
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
		now: time.Now,
	}
}

// Send delivers one message. Credentials are only ever sent over an
// encrypted connection (S15): an unencrypted connection with a username is
// refused before AUTH, whatever the server offers.
func (m *SMTPMailer) Send(ctx context.Context, cfg application.SMTPConfig, email application.OutgoingEmail) error {
	to, err := stdmail.ParseAddress(email.To)
	if err != nil || to.Address != email.To {
		return fmt.Errorf("smtp: invalid recipient address")
	}
	from, err := stdmail.ParseAddress(cfg.FromAddress)
	if err != nil || from.Address != cfg.FromAddress {
		return fmt.Errorf("smtp: invalid sender address")
	}
	if cfg.Username != "" && cfg.Encryption == application.EmailEncryptionNone {
		return errors.New("smtp: refusing to send credentials over an unencrypted connection")
	}
	message, err := m.buildMessage(cfg, to.Address, email)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := m.dial(ctx, addr)
	if err != nil {
		return fmt.Errorf("smtp: connect %s: %w", addr, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
	}

	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		RootCAs:            m.rootCAs,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.SkipTLSVerify, // root's explicit choice (S16)
	}
	encrypted := false
	if cfg.Encryption == application.EmailEncryptionTLS {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("smtp: tls handshake: %w", err)
		}
		conn = tlsConn
		encrypted = true
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	defer client.Close()
	if err := client.Hello(heloName(cfg.FromAddress)); err != nil {
		return fmt.Errorf("smtp: hello: %w", err)
	}
	if cfg.Encryption == application.EmailEncryptionSTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("smtp: the server does not offer STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
		encrypted = true
	}
	if cfg.Username != "" {
		if !encrypted {
			return errors.New("smtp: refusing to send credentials over an unencrypted connection")
		}
		auth, err := chooseAuth(client, cfg)
		if err != nil {
			return err
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: authentication: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp: sender rejected: %w", classifyReply(err))
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp: recipient rejected: %w", classifyReply(err))
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if _, err := w.Write(message); err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	if err := w.Close(); err != nil {
		var reply *textproto.Error
		if !errors.As(err, &reply) {
			// The message went out but no reply came back: the server may
			// have accepted it, so a retry could deliver it twice.
			return fmt.Errorf("smtp: no reply to the message: %w (%w)", err, application.ErrDeliveryOutcomeUnknown)
		}
		return fmt.Errorf("smtp: message rejected: %w", classifyReply(err))
	}
	// The message is accepted once DATA is answered; a failed QUIT does not
	// make it undelivered.
	_ = client.Quit()
	return nil
}

// classifyReply marks a 5xx reply to MAIL, RCPT, or the message as
// permanent: retrying the same message cannot change the answer. Every other
// failure stays transient. Authentication and TLS failures are deliberately
// NOT classified here: they are configuration problems root can fix while
// the retries run.
func classifyReply(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 500 {
		return fmt.Errorf("%w (%w)", err, application.ErrDeliveryPermanent)
	}
	return err
}

// heloName introduces the client with the sender's domain, which servers
// accept more readily than "localhost".
func heloName(fromAddress string) string {
	if at := strings.LastIndexByte(fromAddress, '@'); at >= 0 && at < len(fromAddress)-1 {
		return fromAddress[at+1:]
	}
	return "localhost"
}

// chooseAuth prefers PLAIN and falls back to LOGIN, which some corporate
// servers offer alone.
func chooseAuth(client *smtp.Client, cfg application.SMTPConfig) (smtp.Auth, error) {
	ok, mechs := client.Extension("AUTH")
	if !ok {
		return nil, errors.New("smtp: the server does not offer authentication")
	}
	offered := strings.Fields(strings.ToUpper(mechs))
	has := func(name string) bool {
		for _, m := range offered {
			if m == name {
				return true
			}
		}
		return false
	}
	switch {
	case has("PLAIN"):
		return smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host), nil
	case has("LOGIN"):
		return &loginAuth{username: cfg.Username, password: cfg.Password}, nil
	default:
		return nil, fmt.Errorf("smtp: no supported authentication mechanism (server offers %s)", mechs)
	}
}

// loginAuth implements the LOGIN mechanism. Send only reaches it over an
// encrypted connection.
type loginAuth struct {
	username, password string
	step               int
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("smtp: refusing LOGIN over an unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	default:
		return nil, errors.New("smtp: unexpected LOGIN challenge")
	}
}

// buildMessage renders one RFC 5322 plain-text message. Every header value
// comes from configuration or rendered copy that may hold user data (a
// ticket title in the subject), so line breaks are flattened before
// encoding: no value can start a header of its own.
func (m *SMTPMailer) buildMessage(cfg application.SMTPConfig, to string, email application.OutgoingEmail) ([]byte, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("smtp: message id: %w", err)
	}
	from := stdmail.Address{Name: oneLine(cfg.FromName), Address: cfg.FromAddress}
	var b bytes.Buffer
	header := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	header("From", from.String())
	header("To", (&stdmail.Address{Address: to}).String())
	header("Subject", mime.QEncoding.Encode("utf-8", oneLine(email.Subject)))
	header("Date", m.now().Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+heloName(cfg.FromAddress)+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	body := strings.ReplaceAll(strings.ReplaceAll(email.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	b.WriteString("\r\n")
	return b.Bytes(), nil
}

// oneLine turns every run of control characters and spaces into one space
// and trims the result.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r < 0x20 || r == 0x7f
	}), " ")
}
