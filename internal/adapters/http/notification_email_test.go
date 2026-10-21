package httpadapter

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/adapters/msgtext"
	"github.com/giulianotesta7/tkt/internal/application"
)

// End to end over the real store (issue #362): a ticket change writes the
// notification and its pending email in one transaction, and one sender pass
// delivers it exactly once, with the panel's sentence and a link to the
// ticket, unless the recipient turned email off.

func (h *harness) emailSender() *application.NotificationEmailSender {
	return application.NewNotificationEmailSender(h.store.EmailOutbox(), h.emailSettings, h.store.UserStore(), h.store.TicketStore(), h.store.EmailPreferences(), h.mailer, msgtext.Renderer{}, h.clock)
}

func (h *harness) enableEmail(t *testing.T) {
	t.Helper()
	err := h.store.EmailSettingsStore().SetEmailSettings(context.Background(), application.EmailSettings{
		Enabled: true, Host: "smtp.example.com", Port: 25, Encryption: application.EmailEncryptionNone,
		FromAddress: "tkt@example.com", FromName: "TKT", BaseURL: "https://tkt.example.com",
	})
	if err != nil {
		t.Fatalf("enable email: %v", err)
	}
}

func TestAssignmentIsEmailedOnceThroughTheRealOutbox(t *testing.T) {
	h := newHarness(t)
	h.enableEmail(t)
	agent := h.createUser(t, "Ana Agent", "ana@example.com", "SuperSecret42!")
	tkt := h.seedTicket(t, "Printer jams", nil)
	h.assignTicket(t, tkt.ID, agent.ID)

	sender := h.emailSender()
	if err := sender.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(h.mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(h.mailer.sent))
	}
	got := h.mailer.sent[0]
	if got.To != "ana@example.com" {
		t.Fatalf("To = %q", got.To)
	}
	if want := fmt.Sprintf("Ticket #%d: Printer jams", tkt.Number); got.Subject != want {
		t.Fatalf("Subject = %q, want %q", got.Subject, want)
	}
	for _, want := range []string{
		"Assigned to you",
		fmt.Sprintf("\nTicket: https://tkt.example.com/tickets/%d\n", tkt.ID),
		"\n--\nYou can turn email notifications off in Preferences.\n",
	} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("Body lacks %q:\n%s", want, got.Body)
		}
	}
	for _, gone := range []string{"Open the ticket", "You get this email"} {
		if strings.Contains(got.Body, gone) {
			t.Errorf("Body still says %q:\n%s", gone, got.Body)
		}
	}

	if err := sender.Pass(context.Background()); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(h.mailer.sent) != 1 {
		t.Fatalf("a second pass re-sent: %d emails", len(h.mailer.sent))
	}
}

func TestOptedOutRecipientGetsNoEmailButKeepsTheBell(t *testing.T) {
	h := newHarness(t)
	h.enableEmail(t)
	agent := h.createUser(t, "Ana Agent", "ana@example.com", "SuperSecret42!")
	if err := h.store.EmailPreferences().SetEmailNotificationsEnabled(context.Background(), agent.ID, false); err != nil {
		t.Fatal(err)
	}
	tkt := h.seedTicket(t, "Printer jams", nil)
	h.assignTicket(t, tkt.ID, agent.ID)

	if err := h.emailSender().Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(h.mailer.sent) != 0 {
		t.Fatalf("an opted-out recipient got %d emails", len(h.mailer.sent))
	}
	unread, err := h.store.NotificationStore().UnreadCount(context.Background(), agent.ID)
	if err != nil || unread != 1 {
		t.Fatalf("bell count = %d, %v; want 1", unread, err)
	}
}
