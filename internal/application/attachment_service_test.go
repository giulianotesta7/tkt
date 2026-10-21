package application_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Attachments in the use cases (issue #366, S2/S3/S5): files ride the ticket
// creation and the comment, validated before anything is written, and a
// download re-checks the ticket read scope and the comment visibility.

var pngUpload = domain.AttachmentUpload{FileName: "captura.png", Data: append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)}

func requireKey(t *testing.T, err error, key string) {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Key != key {
		t.Fatalf("err = %#v, want ValidationError key %q", err, key)
	}
}

func TestCreateWithWorkflowCarriesValidatedAttachments(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}})
	actor := domain.User{ID: 7, Name: "Ada", Role: domain.RoleUser}
	in := validCreateInput(cat.ID)
	in.Attachments = []domain.AttachmentUpload{pngUpload}

	if _, err := h.svc.Create(context.Background(), actor, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(h.wfTx.calls) != 1 {
		t.Fatalf("CreateTicketWithRun calls = %d, want 1", len(h.wfTx.calls))
	}
	files := h.wfTx.calls[0].Attachments
	if len(files) != 1 {
		t.Fatalf("attachments = %+v, want one", files)
	}
	a := files[0]
	if a.FileName != "captura.png" || a.ContentType != "image/png" || a.Size != int64(len(pngUpload.Data)) ||
		a.UploaderUserID == nil || *a.UploaderUserID != actor.ID || !a.CreatedAt.Equal(h.clock.now) || a.CommentID != nil {
		t.Errorf("attachment = %+v, want the validated png by the actor at the clock instant", a)
	}
}

func TestCreateRejectsABadAttachmentBeforeWriting(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}})
	in := validCreateInput(cat.ID)
	in.Attachments = []domain.AttachmentUpload{pngUpload, {FileName: "virus.exe", Data: []byte("MZ")}}

	_, err := h.svc.Create(context.Background(), domain.User{ID: 7, Role: domain.RoleUser}, in)
	requireKey(t, err, "error.attachment_type_not_allowed")
	if len(h.wfTx.calls) != 0 || len(h.tickets.tickets) != 0 || len(h.audits.events) != 0 {
		t.Errorf("a refused attachment wrote: calls=%d tickets=%d audits=%d", len(h.wfTx.calls), len(h.tickets.tickets), len(h.audits.events))
	}
}

// TestCreateWithoutAttachmentsIsUnchanged pins the previous behaviour.
func TestCreateWithoutAttachmentsIsUnchanged(t *testing.T) {
	h := newWorkflowCreateHarness()
	cat := h.categories.seed("Bugs")
	h.versions.publish(cat.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}})
	if _, err := h.svc.Create(context.Background(), domain.User{ID: 7, Role: domain.RoleUser}, validCreateInput(cat.ID)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n := len(h.wfTx.calls[0].Attachments); n != 0 {
		t.Errorf("attachments = %d, want 0", n)
	}
}

func TestLegacyCreateCarriesAttachments(t *testing.T) {
	tickets, users, categories, comments, audits := newFakeTicketStore(), newFakeUserStore(), newFakeCategoryStore(), newFakeCommentStore(), newFakeAuditStore()
	tx := newFakeUnitOfWork(tickets, audits)
	clock := fixedClock()
	svc := application.NewTicketService(tickets, users, categories, tx, application.NewViewBuilder(tickets, users, categories, comments, audits, newFakeDeskStore()), clock)
	cat := categories.seed("Bugs")
	in := validCreateInput(cat.ID)
	in.Attachments = []domain.AttachmentUpload{pngUpload}
	if _, err := svc.Create(context.Background(), domain.User{ID: 3, Role: domain.RoleAdmin}, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(tx.attachments) != 1 || tx.attachments[0].FileName != "captura.png" {
		t.Errorf("attachments = %+v, want captura.png", tx.attachments)
	}
}

func TestCommentCarriesValidatedAttachments(t *testing.T) {
	h := newCommentHoldHarness(t)
	ticket := h.ticket
	ticket.OnHoldSince, ticket.OnHoldReason = nil, ""
	h.storeTicket(t, ticket)
	if _, err := h.comments.AddWithAttachments(context.Background(), h.requester, ticket.ID, "here it is", "public",
		[]domain.AttachmentUpload{pngUpload}); err != nil {
		t.Fatalf("AddWithAttachments: %v", err)
	}
	if len(h.commentTx.attachments) != 1 {
		t.Fatalf("attachments = %+v", h.commentTx.attachments)
	}
	a := h.commentTx.attachments[0]
	if a.UploaderUserID == nil || *a.UploaderUserID != h.requester.ID || !a.CreatedAt.Equal(h.clock.now) || a.ContentType != "image/png" {
		t.Errorf("attachment = %+v", a)
	}
}

func TestCommentRejectsABadAttachmentBeforeWriting(t *testing.T) {
	h := newCommentHoldHarness(t)
	six := make([]domain.AttachmentUpload, 6)
	for i := range six {
		six[i] = pngUpload
	}
	_, err := h.comments.AddWithAttachments(context.Background(), h.requester, h.ticket.ID, "files", "public", six)
	requireKey(t, err, "error.attachment_too_many")
	if len(h.holdHarness.comments.comments[h.ticket.ID]) != 0 || len(h.commentTx.attachments) != 0 {
		t.Error("a refused submission wrote a comment or attachments")
	}
	if stored := h.stored(t, h.ticket.ID); stored.OnHoldSince == nil {
		t.Error("a refused reply must not resume the hold")
	}
}

// --- downloads ---

type downloadFixture struct {
	svc       *application.AttachmentService
	store     *fakeAttachmentStore
	tickets   *fakeTicketStore
	requester domain.User
	agent     domain.User
	ticketID  int64
}

func newDownloadFixture(t *testing.T) downloadFixture {
	t.Helper()
	tickets := newFakeTicketStore()
	requester := domain.User{ID: 11, Role: domain.RoleUser}
	agent := domain.User{ID: 12, Role: domain.RoleAgent}
	tk := tickets.seed(domain.Ticket{Title: "t", CategoryID: 1, Priority: domain.PriorityMedium, State: domain.StateInProgress,
		RequesterUserID: &requester.ID, UserID: &agent.ID})
	store := &fakeAttachmentStore{files: map[int64]application.StoredAttachment{}}
	for id, vis := range map[int64]domain.CommentVisibility{1: "", 2: domain.CommentPublic, 3: domain.CommentInternal} {
		store.files[id] = application.StoredAttachment{Attachment: domain.Attachment{ID: id, TicketID: tk.ID, FileName: "f.txt", Data: []byte{byte(id)}}, Visibility: vis}
	}
	return downloadFixture{svc: application.NewAttachmentService(tickets, store), store: store, tickets: tickets, requester: requester, agent: agent, ticketID: tk.ID}
}

func TestDownloadFollowsTheCommentVisibility(t *testing.T) {
	f := newDownloadFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		actor domain.User
		id    int64
		ok    bool
	}{
		{"requester, creation file", f.requester, 1, true},
		{"requester, public comment file", f.requester, 2, true},
		{"requester, internal comment file", f.requester, 3, false},
		{"agent, internal comment file", f.agent, 3, true},
		{"missing file", f.agent, 99, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.svc.Download(ctx, tc.actor, tc.id)
			if tc.ok {
				if err != nil || !bytes.Equal(got.Data, []byte{byte(tc.id)}) {
					t.Errorf("Download = %+v, %v, want the file", got, err)
				}
				return
			}
			if got != nil || !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("Download = %+v, %v, want NotFound (no existence leak)", got, err)
			}
		})
	}
}

func TestDownloadRequiresTheTicketReadScope(t *testing.T) {
	f := newDownloadFixture(t)
	stranger := domain.User{ID: 99, Role: domain.RoleUser}
	got, err := f.svc.Download(context.Background(), stranger, 2)
	if got != nil || !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Download by a stranger = %+v, %v, want NotFound", got, err)
	}
}
