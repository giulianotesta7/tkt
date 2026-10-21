package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	stdmail "net/mail"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// The SMTP client is exercised against an in-process fake server over a
// connected socket pair: no listening socket (go-tests rule 10), real
// net/smtp on the client side, real TLS on both sides. A socket pair rather
// than net.Pipe because it is buffered like TCP: a failed TLS handshake has
// both sides write at once, which deadlocks an unbuffered pipe.

func socketPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	conn := func(fd int, name string) net.Conn {
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatalf("FileConn: %v", err)
		}
		return c
	}
	return conn(fds[0], "client"), conn(fds[1], "server")
}

type fakeServerOptions struct {
	implicitTLS bool
	startTLS    bool
	authMechs   string // e.g. "PLAIN LOGIN"; empty offers no AUTH
	rejectRcpt  string // a 5xx reply to RCPT, when set
	cert        tls.Certificate
}

// fakeServerLog is what the fake server observed.
type fakeServerLog struct {
	mu        sync.Mutex
	commands  []string
	tlsActive bool
	authUser  string
	authPass  string
	mailFrom  string
	rcptTo    []string
	data      string
}

func (l *fakeServerLog) add(cmd string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.commands = append(l.commands, cmd)
}

func (l *fakeServerLog) sawCommand(prefix string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.commands {
		if strings.HasPrefix(strings.ToUpper(c), prefix) {
			return true
		}
	}
	return false
}

func serveFakeSMTP(conn net.Conn, opts fakeServerOptions, log *fakeServerLog) {
	defer conn.Close()
	if opts.implicitTLS {
		tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{opts.cert}})
		if err := tc.Handshake(); err != nil {
			return
		}
		conn = tc
		log.tlsActive = true
	}
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(s string) {
		w.WriteString(s + "\r\n")
		w.Flush()
	}
	readLine := func() (string, bool) {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		return strings.TrimRight(line, "\r\n"), true
	}
	reply("220 fake.example.com ESMTP")
	for {
		line, ok := readLine()
		if !ok {
			return
		}
		log.add(line)
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO", "HELO":
			ext := []string{"250-fake.example.com"}
			if opts.startTLS && !log.tlsActive {
				ext = append(ext, "250-STARTTLS")
			}
			if opts.authMechs != "" {
				ext = append(ext, "250-AUTH "+opts.authMechs)
			}
			ext = append(ext, "250 8BITMIME")
			for _, e := range ext {
				w.WriteString(e + "\r\n")
			}
			w.Flush()
		case "STARTTLS":
			reply("220 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{opts.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			conn = tc
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
			log.tlsActive = true
		case "AUTH":
			fields := strings.Fields(line)
			switch strings.ToUpper(fields[1]) {
			case "PLAIN":
				raw, _ := base64.StdEncoding.DecodeString(fields[2])
				parts := strings.Split(string(raw), "\x00")
				log.authUser, log.authPass = parts[1], parts[2]
				reply("235 ok")
			case "LOGIN":
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				u, _ := readLine()
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				p, _ := readLine()
				user, _ := base64.StdEncoding.DecodeString(u)
				pass, _ := base64.StdEncoding.DecodeString(p)
				log.authUser, log.authPass = string(user), string(pass)
				reply("235 ok")
			default:
				reply("504 unsupported")
			}
		case "MAIL":
			log.mailFrom = line
			reply("250 ok")
		case "RCPT":
			if opts.rejectRcpt != "" {
				reply(opts.rejectRcpt)
				continue
			}
			log.rcptTo = append(log.rcptTo, line)
			reply("250 ok")
		case "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, ok := readLine()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				b.WriteString(l + "\r\n")
			}
			log.data = b.String()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// testCert is a self-signed certificate for smtp.example.com.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "smtp.example.com"},
		DNSNames:     []string{"smtp.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

type harness struct {
	mailer *SMTPMailer
	log    *fakeServerLog
	dialed []string
	done   chan struct{}
}

// newHarness wires a mailer whose dialer hands one end of a pipe to the fake
// server. trusted controls whether the server certificate is in the
// mailer's root pool.
func newHarness(t *testing.T, opts fakeServerOptions, trusted bool) *harness {
	t.Helper()
	cert, pool := testCert(t)
	opts.cert = cert
	h := &harness{log: &fakeServerLog{}, done: make(chan struct{})}
	m := NewSMTPMailer()
	if trusted {
		m.rootCAs = pool
	} else {
		m.rootCAs = x509.NewCertPool()
	}
	m.dial = func(_ context.Context, addr string) (net.Conn, error) {
		h.dialed = append(h.dialed, addr)
		client, server := socketPair(t)
		go func() {
			defer close(h.done)
			serveFakeSMTP(server, opts, h.log)
		}()
		return client, nil
	}
	h.mailer = m
	// The client closes its end on every path, so a dialed server always
	// finishes; waiting for it keeps its log writes ordered before the next
	// test.
	t.Cleanup(func() {
		if len(h.dialed) > 0 {
			<-h.done
		}
	})
	return h
}

func baseConfig() application.SMTPConfig {
	return application.SMTPConfig{
		Host:        "smtp.example.com",
		Port:        25,
		Encryption:  application.EmailEncryptionNone,
		FromAddress: "tkt@example.com",
		FromName:    "TKT",
	}
}

func testEmail() application.OutgoingEmail {
	return application.OutgoingEmail{To: "ana@example.com", Subject: "Ticket #7", Body: "Hello\nworld"}
}

// parseDelivered parses the DATA the fake server received.
func parseDelivered(t *testing.T, data string) (*stdmail.Message, string) {
	t.Helper()
	msg, err := stdmail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("delivered message does not parse: %v\n%s", err, data)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	return msg, string(body)
}

func TestSendPlainWithoutAuthOverOpenRelay(t *testing.T) {
	h := newHarness(t, fakeServerOptions{authMechs: "PLAIN"}, true)
	if err := h.mailer.Send(context.Background(), baseConfig(), testEmail()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(h.dialed) != 1 || h.dialed[0] != "smtp.example.com:25" {
		t.Fatalf("dialed %v, want smtp.example.com:25", h.dialed)
	}
	if h.log.sawCommand("AUTH") || h.log.sawCommand("STARTTLS") {
		t.Fatalf("open relay send issued AUTH or STARTTLS: %v", h.log.commands)
	}
	if h.log.mailFrom != "MAIL FROM:<tkt@example.com> BODY=8BITMIME" && h.log.mailFrom != "MAIL FROM:<tkt@example.com>" {
		t.Fatalf("MAIL = %q", h.log.mailFrom)
	}
	if len(h.log.rcptTo) != 1 || h.log.rcptTo[0] != "RCPT TO:<ana@example.com>" {
		t.Fatalf("RCPT = %v", h.log.rcptTo)
	}
	msg, body := parseDelivered(t, h.log.data)
	if got := msg.Header.Get("From"); got != `"TKT" <tkt@example.com>` {
		t.Errorf("From = %q", got)
	}
	if got := msg.Header.Get("To"); got != "<ana@example.com>" {
		t.Errorf("To = %q", got)
	}
	if got := msg.Header.Get("Subject"); got != "Ticket #7" {
		t.Errorf("Subject = %q", got)
	}
	if got := msg.Header.Get("Content-Type"); got != "text/plain; charset=UTF-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if msg.Header.Get("Auto-Submitted") != "auto-generated" || msg.Header.Get("Date") == "" || msg.Header.Get("Message-Id") == "" {
		t.Errorf("missing Auto-Submitted, Date or Message-ID: %v", msg.Header)
	}
	if body != "Hello\r\nworld\r\n" {
		t.Errorf("body = %q", body)
	}
}

func TestSendSTARTTLSWithPlainAuth(t *testing.T) {
	h := newHarness(t, fakeServerOptions{startTLS: true, authMechs: "PLAIN LOGIN"}, true)
	cfg := baseConfig()
	cfg.Port = 587
	cfg.Encryption = application.EmailEncryptionSTARTTLS
	cfg.Username, cfg.Password = "tkt", "s3cret"
	if err := h.mailer.Send(context.Background(), cfg, testEmail()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !h.log.sawCommand("STARTTLS") || !h.log.tlsActive {
		t.Fatal("STARTTLS was not negotiated")
	}
	if h.log.authUser != "tkt" || h.log.authPass != "s3cret" {
		t.Fatalf("AUTH = %q/%q", h.log.authUser, h.log.authPass)
	}
	if h.log.data == "" {
		t.Fatal("nothing delivered")
	}
}

func TestSendLoginAuthWhenPlainIsNotOffered(t *testing.T) {
	h := newHarness(t, fakeServerOptions{startTLS: true, authMechs: "LOGIN"}, true)
	cfg := baseConfig()
	cfg.Encryption = application.EmailEncryptionSTARTTLS
	cfg.Username, cfg.Password = "tkt", "s3cret"
	if err := h.mailer.Send(context.Background(), cfg, testEmail()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if h.log.authUser != "tkt" || h.log.authPass != "s3cret" {
		t.Fatalf("LOGIN = %q/%q", h.log.authUser, h.log.authPass)
	}
}

func TestSendSTARTTLSRequiredButNotOfferedSendsNothing(t *testing.T) {
	h := newHarness(t, fakeServerOptions{authMechs: "PLAIN"}, true)
	cfg := baseConfig()
	cfg.Encryption = application.EmailEncryptionSTARTTLS
	cfg.Username, cfg.Password = "tkt", "s3cret"
	err := h.mailer.Send(context.Background(), cfg, testEmail())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send = %v, want a STARTTLS error", err)
	}
	if h.log.sawCommand("AUTH") || h.log.sawCommand("MAIL") {
		t.Fatalf("credentials or mail sent without STARTTLS: %v", h.log.commands)
	}
}

func TestSendImplicitTLS(t *testing.T) {
	h := newHarness(t, fakeServerOptions{implicitTLS: true, authMechs: "PLAIN"}, true)
	cfg := baseConfig()
	cfg.Port = 465
	cfg.Encryption = application.EmailEncryptionTLS
	cfg.Username, cfg.Password = "tkt", "s3cret"
	if err := h.mailer.Send(context.Background(), cfg, testEmail()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if h.log.authPass != "s3cret" || h.log.data == "" {
		t.Fatal("implicit TLS send did not authenticate and deliver")
	}
}

func TestSendRefusesAnUntrustedCertificateUnlessVerificationIsSkipped(t *testing.T) {
	cfg := baseConfig()
	cfg.Encryption = application.EmailEncryptionTLS
	cfg.Username, cfg.Password = "tkt", "s3cret"

	h := newHarness(t, fakeServerOptions{implicitTLS: true, authMechs: "PLAIN"}, false)
	err := h.mailer.Send(context.Background(), cfg, testEmail())
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) && (err == nil || !strings.Contains(err.Error(), "certificate")) {
		t.Fatalf("Send to an untrusted server = %v, want a certificate error", err)
	}
	if h.log.authPass != "" {
		t.Fatal("password reached an unverified server")
	}

	cfg.SkipTLSVerify = true
	h2 := newHarness(t, fakeServerOptions{implicitTLS: true, authMechs: "PLAIN"}, false)
	if err := h2.mailer.Send(context.Background(), cfg, testEmail()); err != nil {
		t.Fatalf("Send with verification skipped: %v", err)
	}
}

func TestSendRefusesCredentialsOverAnUnencryptedConnection(t *testing.T) {
	h := newHarness(t, fakeServerOptions{authMechs: "PLAIN LOGIN"}, true)
	cfg := baseConfig()
	cfg.Username, cfg.Password = "tkt", "s3cret"
	err := h.mailer.Send(context.Background(), cfg, testEmail())
	if err == nil {
		t.Fatal("Send authenticated over a clear connection")
	}
	if h.log.sawCommand("AUTH") || h.log.authPass != "" {
		t.Fatal("password crossed a clear connection")
	}
}

func TestSendNeutralizesHeaderInjectionAndEncodesNonASCII(t *testing.T) {
	h := newHarness(t, fakeServerOptions{}, true)
	cfg := baseConfig()
	cfg.FromName = "Mesa de ayuda Ñandú"
	email := testEmail()
	email.Subject = "Impresora rota\r\nBcc: evil@example.com"
	if err := h.mailer.Send(context.Background(), cfg, email); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg, _ := parseDelivered(t, h.log.data)
	if msg.Header.Get("Bcc") != "" || len(h.log.rcptTo) != 1 {
		t.Fatalf("header injection added a recipient: %v %v", msg.Header, h.log.rcptTo)
	}
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Impresora rota Bcc: evil@example.com" {
		t.Fatalf("Subject = %q (%v)", subject, err)
	}
	from, err := stdmail.ParseAddress(msg.Header.Get("From"))
	if err != nil || from.Name != "Mesa de ayuda Ñandú" {
		t.Fatalf("From = %q (%v)", msg.Header.Get("From"), err)
	}
}

func TestSendRejectsAnInvalidRecipientBeforeConnecting(t *testing.T) {
	m := NewSMTPMailer()
	dialed := false
	m.dial = func(context.Context, string) (net.Conn, error) { dialed = true; return nil, errors.New("unused") }
	email := testEmail()
	email.To = "ana@example.com\r\nRCPT TO:<evil@example.com>"
	if err := m.Send(context.Background(), baseConfig(), email); err == nil {
		t.Fatal("Send accepted a recipient with a line break")
	}
	if dialed {
		t.Fatal("Send connected for an invalid recipient")
	}
}

func TestSendReportsServerRejectionWithoutThePassword(t *testing.T) {
	h := newHarness(t, fakeServerOptions{startTLS: true, authMechs: "PLAIN", rejectRcpt: "550 5.1.1 mailbox unavailable"}, true)
	cfg := baseConfig()
	cfg.Encryption = application.EmailEncryptionSTARTTLS
	cfg.Username, cfg.Password = "tkt", "s3cret"
	err := h.mailer.Send(context.Background(), cfg, testEmail())
	if err == nil || !strings.Contains(err.Error(), "550") {
		t.Fatalf("Send = %v, want the 550 reply", err)
	}
	if strings.Contains(fmt.Sprint(err), "s3cret") {
		t.Fatal("error leaks the password")
	}
}

func TestSendClassifiesFailuresForTheRetryPolicy(t *testing.T) {
	h := newHarness(t, fakeServerOptions{rejectRcpt: "550 5.1.1 mailbox unavailable"}, true)
	err := h.mailer.Send(context.Background(), baseConfig(), testEmail())
	if !errors.Is(err, application.ErrDeliveryPermanent) {
		t.Fatalf("5xx RCPT = %v, want ErrDeliveryPermanent", err)
	}

	h2 := newHarness(t, fakeServerOptions{rejectRcpt: "451 4.3.0 try later"}, true)
	err = h2.mailer.Send(context.Background(), baseConfig(), testEmail())
	if err == nil || errors.Is(err, application.ErrDeliveryPermanent) || errors.Is(err, application.ErrDeliveryOutcomeUnknown) {
		t.Fatalf("4xx RCPT = %v, want a transient error", err)
	}
}

func TestSendReportsAnUnknownOutcomeWhenTheConnectionDropsAfterTheMessage(t *testing.T) {
	m := NewSMTPMailer()
	done := make(chan struct{})
	m.dial = func(context.Context, string) (net.Conn, error) {
		client, server := socketPair(t)
		go func() {
			defer close(done)
			defer server.Close()
			r := bufio.NewReader(server)
			say := func(s string) { server.Write([]byte(s + "\r\n")) }
			say("220 fake")
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				switch strings.ToUpper(strings.Fields(line)[0]) {
				case "EHLO":
					say("250 fake")
				case "DATA":
					say("354 go ahead")
					for {
						l, err := r.ReadString('\n')
						if err != nil || l == ".\r\n" {
							// Hang up without answering the message.
							return
						}
					}
				default:
					say("250 ok")
				}
			}
		}()
		return client, nil
	}
	err := m.Send(context.Background(), baseConfig(), testEmail())
	<-done
	if !errors.Is(err, application.ErrDeliveryOutcomeUnknown) {
		t.Fatalf("drop after DATA = %v, want ErrDeliveryOutcomeUnknown", err)
	}
}
