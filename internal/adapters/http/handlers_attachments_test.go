package httpadapter

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Ticket attachments at the HTTP boundary (issue #366, specs S1/S2/S3/S5/S6/S7).
// The files travel as a multipart/form-data body on the two routes that create
// one: POST /tickets and POST /tickets/{id}/comments. Everything is asserted
// through the real harness (real sqlite database, real services, real routes),
// so a stored file is read back through the store port and never through a
// fake.

// pngFixture and pdfFixture are the smallest payloads the domain sniffer
// accepts: the magic bytes are what is validated, not the extension.
var (
	pngFixture = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01")
	pdfFixture = []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
)

// notAnImage has a .png name and text bytes: the extension lies, so the
// content check is what refuses it.
var notAnImage = []byte("this is a text file wearing a png name")

func sessionHeaders(sessionID string) map[string]string {
	return map[string]string{"Cookie": sessionCookie + "=" + sessionID}
}

// TestCreateTicketWithAttachments stores the two files of a multipart creation
// on the ticket, in upload order, with the content type their bytes proved.
func TestCreateTicketWithAttachments(t *testing.T) {
	h := newHarness(t)

	rec := h.postMultipart(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), []multipartFile{
		{name: "shot.png", data: pngFixture},
		{name: "report.pdf", data: pdfFixture},
	}, h.adminSession.ID)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	tkt, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("created ticket must be readable: %v", err)
	}
	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), tkt.Ticket.ID, true)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("attachments = %d, want the 2 uploaded files: %+v", len(files), files)
	}
	for i, want := range []struct {
		name        string
		contentType string
		data        []byte
	}{
		{"shot.png", "image/png", pngFixture},
		{"report.pdf", "application/pdf", pdfFixture},
	} {
		if files[i].FileName != want.name {
			t.Errorf("attachment %d name = %q, want %q (upload order)", i, files[i].FileName, want.name)
		}
		if files[i].ContentType != want.contentType {
			t.Errorf("attachment %d content type = %q, want %q (proved by the bytes)", i, files[i].ContentType, want.contentType)
		}
		if files[i].Size != int64(len(want.data)) {
			t.Errorf("attachment %d size = %d, want %d", i, files[i].Size, len(want.data))
		}
		if files[i].CommentID != nil {
			t.Errorf("attachment %d belongs to comment %d, want a creation file", i, *files[i].CommentID)
		}
		if files[i].TicketID != tkt.Ticket.ID {
			t.Errorf("attachment %d ticket = %d, want %d", i, files[i].TicketID, tkt.Ticket.ID)
		}
	}
}

// TestCommentAttachmentIsListedAndDownloadableByTheRequester proves S2/S3 for
// the comment path: the file lands on the comment, the requester's detail page
// links it, and the requester's download answers the bytes.
func TestCommentAttachmentIsListedAndDownloadableByTheRequester(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rita", "rita@example.com", domain.RoleUser)
	requesterSession := seedSession(t, h.store, requester.ID)
	tkt := seedUserOwnedTicket(t, h, requester, "Printer jams on the third floor")

	rec := h.postMultipart(t, ticketPath(tkt.ID)+"/comments", url.Values{
		"body":       {"Here is the screenshot"},
		"visibility": {"public"},
	}, []multipartFile{{name: "captura.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, ticketPath(tkt.ID))

	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), tkt.ID, true)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(files) != 1 || files[0].CommentID == nil {
		t.Fatalf("attachments = %+v, want one file on the new comment", files)
	}

	detail := doRequest(h.mux, h.mw, http.MethodGet, ticketPath(tkt.ID), sessionHeaders(requesterSession.ID))
	if detail.Code != http.StatusOK {
		t.Fatalf("requester detail = %d, want 200", detail.Code)
	}
	body := detail.Body.String()
	if !strings.Contains(body, "captura.png") {
		t.Errorf("requester detail must list the comment's file, got: %s", body)
	}
	link := "/attachments/" + strconv.FormatInt(files[0].ID, 10)
	if !strings.Contains(body, `href="`+link+`"`) {
		t.Errorf("requester detail must link %s, got: %s", link, body)
	}

	download := doRequest(h.mux, h.mw, http.MethodGet, link, sessionHeaders(requesterSession.ID))
	if download.Code != http.StatusOK {
		t.Fatalf("requester download = %d, want 200", download.Code)
	}
	if !bytes.Equal(download.Body.Bytes(), pngFixture) {
		t.Errorf("downloaded bytes = %q, want the stored file", download.Body.Bytes())
	}
}

// TestCreationAttachmentsRenderUnderTheDescription proves the ticket-detail
// listing for the creation files (S2): the file and its human size appear on
// the detail page as a download link.
func TestCreationAttachmentsRenderUnderTheDescription(t *testing.T) {
	h := newHarness(t)
	rec := h.postMultipart(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), []multipartFile{{name: "shot.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), 1, true)
	if err != nil || len(files) != 1 {
		t.Fatalf("attachments = %+v (err %v), want one creation file", files, err)
	}
	detail := doRequest(h.mux, h.mw, http.MethodGet, ticketPath(1), sessionHeaders(h.adminSession.ID))
	body := detail.Body.String()
	for _, want := range []string{
		`href="/attachments/` + strconv.FormatInt(files[0].ID, 10) + `"`,
		"shot.png",
		fileSize(int64(len(pngFixture))),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail must render the creation file %q, got: %s", want, body)
		}
	}
}

// TestRefusedAttachmentsWriteNothing is S5 at the create route: a submission
// whose files are refused answers 422 with the localized message and leaves no
// ticket and no attachment behind.
func TestRefusedAttachmentsWriteNothing(t *testing.T) {
	six := make([]multipartFile, 0, domain.MaxAttachmentsPerSubmission+1)
	for i := 0; i <= domain.MaxAttachmentsPerSubmission; i++ {
		six = append(six, multipartFile{name: "shot" + strconv.Itoa(i) + ".png", data: pngFixture})
	}
	for _, tc := range []struct {
		name  string
		files []multipartFile
		want  string
	}{
		{
			name:  "content does not match the extension",
			files: []multipartFile{{name: "fake.png", data: notAnImage}},
			want:  "fake.png cannot be attached: only images, PDF, Office documents, text files and zip files are allowed",
		},
		{
			name:  "too many files",
			files: six,
			want:  "you can attach at most 5 files at a time",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			rec := h.postMultipart(t, "/tickets", ticketForm(func(f url.Values) {
				f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
			}), tc.files, h.adminSession.ID)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.400s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("re-render must carry %q, got: %.600s", tc.want, rec.Body.String())
			}
			db := h.rawDB(t)
			if n := scanOneInt(t, db, "SELECT COUNT(*) FROM tickets"); n != 0 {
				t.Errorf("tickets = %d, want 0 (a refused creation writes nothing)", n)
			}
			if n := scanOneInt(t, db, "SELECT COUNT(*) FROM attachments"); n != 0 {
				t.Errorf("attachments = %d, want 0", n)
			}
			if n := scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events"); n != 0 {
				t.Errorf("audit events = %d, want 0", n)
			}
		})
	}
}

// TestRefusedCommentAttachmentWritesNothing is S5 at the comment route: the
// comment is not created either, so a refused file leaves no partial entry in
// the timeline.
func TestRefusedCommentAttachmentWritesNothing(t *testing.T) {
	six := make([]multipartFile, 0, domain.MaxAttachmentsPerSubmission+1)
	for i := 0; i <= domain.MaxAttachmentsPerSubmission; i++ {
		six = append(six, multipartFile{name: "shot" + strconv.Itoa(i) + ".png", data: pngFixture})
	}
	for _, tc := range []struct {
		name  string
		files []multipartFile
		want  string
	}{
		{
			name:  "content does not match the extension",
			files: []multipartFile{{name: "fake.png", data: notAnImage}},
			want:  "fake.png cannot be attached: only images, PDF, Office documents, text files and zip files are allowed",
		},
		{
			name:  "too many files",
			files: six,
			want:  "you can attach at most 5 files at a time",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tkt := h.seedTicket(t, "Login page down", nil)

			rec := h.postMultipart(t, ticketPath(tkt.ID)+"/comments", url.Values{
				"body": {"Here it is"},
			}, tc.files, h.adminSession.ID)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.400s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("re-render must carry %q, got: %.600s", tc.want, rec.Body.String())
			}
			comments, err := h.store.CommentStore().ListByTicket(t.Context(), tkt.ID, true)
			if err != nil {
				t.Fatalf("list comments: %v", err)
			}
			if len(comments) != 0 {
				t.Errorf("comments = %d, want 0 (a refused file refuses the comment)", len(comments))
			}
			if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM attachments"); n != 0 {
				t.Errorf("attachments = %d, want 0", n)
			}
		})
	}
}

// TestOversizeStreamedUploadAnswers413 is S6 from the handler's side: the
// middleware streams the upload route behind http.MaxBytesReader (its own cap
// is pinned in middleware_upload_limit_test.go), so the HANDLER must answer a
// MaxBytesError with 413 instead of the 500 its parse-error path emits. The
// body is undeclared (chunked) and longer than the route cap, so the refusal
// happens while parsing, and nothing is written.
func TestOversizeStreamedUploadAnswers413(t *testing.T) {
	h := newHarness(t)
	const boundary = "tktuploadboundary"
	head := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nBig upload\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"category_id\"\r\n\r\n1\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"attachments\"; filename=\"big.png\"\r\nContent-Type: image/png\r\n\r\n"
	tail := "\r\n--" + boundary + "--\r\n"
	body := io.MultiReader(strings.NewReader(head), io.LimitReader(zeros{}, maxUploadBodyBytes+1), strings.NewReader(tail))

	req := httptest.NewRequest(http.MethodPost, "/tickets", body)
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	// The production chain: session middleware outside the body cap.
	h.mw.Wrap(NewBodyLimitMiddleware().Wrap(h.mux)).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %.400s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Request entity too large") {
		t.Errorf("413 body = %q, want the catalogue copy", rec.Body.String())
	}
	if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM tickets"); n != 0 {
		t.Errorf("tickets = %d, want 0 (an oversize body writes nothing)", n)
	}
}

// TestAttachmentDownloadHeaders is S7: the download is an attachment with the
// stored content type, the original name, and headers that stop a browser from
// treating the file as a document of this origin.
func TestAttachmentDownloadHeaders(t *testing.T) {
	h := newHarness(t)
	rec := h.postMultipart(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), []multipartFile{{name: "shot.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), 1, true)
	if err != nil || len(files) != 1 {
		t.Fatalf("attachments = %+v (err %v), want one", files, err)
	}

	download := doRequest(h.mux, h.mw, http.MethodGet, "/attachments/"+strconv.FormatInt(files[0].ID, 10), sessionHeaders(h.adminSession.ID))
	if download.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.200s)", download.Code, download.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/png",
		"Content-Disposition":     "attachment; filename=shot.png",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"Cache-Control":           "private, no-store",
		"Content-Length":          strconv.Itoa(len(pngFixture)),
	} {
		if got := download.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if !bytes.Equal(download.Body.Bytes(), pngFixture) {
		t.Errorf("body = %q, want the stored bytes", download.Body.Bytes())
	}
}

// TestAttachmentDownloadEncodesANonASCIIName proves the RFC 2231 form: the
// original UTF-8 name survives the header instead of being dropped or mangled.
func TestAttachmentDownloadEncodesANonASCIIName(t *testing.T) {
	h := newHarness(t)
	rec := h.postMultipart(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), []multipartFile{{name: "captura-ñandú.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), 1, true)
	if err != nil || len(files) != 1 {
		t.Fatalf("attachments = %+v (err %v), want one", files, err)
	}

	download := doRequest(h.mux, h.mw, http.MethodGet, "/attachments/"+strconv.FormatInt(files[0].ID, 10), sessionHeaders(h.adminSession.ID))
	if download.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", download.Code)
	}
	if got, want := download.Header().Get("Content-Disposition"), "attachment; filename*=utf-8''captura-%C3%B1and%C3%BA.png"; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}

// TestAttachmentDownloadRefusalsAre404 is S3: a missing file, a staff-only file
// read by a requester, and a ticket outside the actor's scope are all the same
// 404, so a download never reveals that a file exists.
func TestAttachmentDownloadRefusalsAre404(t *testing.T) {
	h := newHarness(t)
	owner := seedUserRole(t, h.store, "Rita", "rita@example.com", domain.RoleUser)
	outsider := seedUserRole(t, h.store, "Otto", "otto@example.com", domain.RoleUser)
	ownerSession := seedSession(t, h.store, owner.ID)
	outsiderSession := seedSession(t, h.store, outsider.ID)

	ownerTicket := seedUserOwnedTicket(t, h, owner, "Owner's ticket")
	publicRec := h.postMultipart(t, ticketPath(ownerTicket.ID)+"/comments", url.Values{
		"body":       {"public answer"},
		"visibility": {"public"},
	}, []multipartFile{{name: "public.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, publicRec, http.StatusSeeOther, ticketPath(ownerTicket.ID))
	internalRec := h.postMultipart(t, ticketPath(ownerTicket.ID)+"/comments", url.Values{
		"body":       {"staff only"},
		"visibility": {"internal"},
	}, []multipartFile{{name: "internal.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, internalRec, http.StatusSeeOther, ticketPath(ownerTicket.ID))

	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), ownerTicket.ID, true)
	if err != nil || len(files) != 2 {
		t.Fatalf("attachments = %+v (err %v), want the public and the internal file", files, err)
	}
	var publicID, internalID int64
	for _, f := range files {
		if f.FileName == "internal.png" {
			internalID = f.ID
		} else {
			publicID = f.ID
		}
	}
	if publicID == 0 || internalID == 0 {
		t.Fatalf("attachments = %+v, want both files", files)
	}

	for _, tc := range []struct {
		name      string
		target    string
		sessionID string
	}{
		{name: "missing file", target: "/attachments/9999", sessionID: h.adminSession.ID},
		{name: "requester on an internal comment's file", target: "/attachments/" + strconv.FormatInt(internalID, 10), sessionID: ownerSession.ID},
		{name: "user outside the ticket scope", target: "/attachments/" + strconv.FormatInt(publicID, 10), sessionID: outsiderSession.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(h.mux, h.mw, http.MethodGet, tc.target, sessionHeaders(tc.sessionID))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %.200s)", rec.Code, rec.Body.String())
			}
			if bytes.Contains(rec.Body.Bytes(), pngFixture) {
				t.Error("a refused download must not answer the file's bytes")
			}
		})
	}

	// The internal file IS downloadable by the staff actor who may read the
	// comment it belongs to: the refusal is about visibility, not the route.
	staff := doRequest(h.mux, h.mw, http.MethodGet, "/attachments/"+strconv.FormatInt(internalID, 10), sessionHeaders(h.adminSession.ID))
	if staff.Code != http.StatusOK {
		t.Errorf("staff download of an internal file = %d, want 200", staff.Code)
	}
}

// TestRequesterDetailHidesAnInternalCommentAttachment is S3 end to end: the
// requester's rendered detail page carries neither the staff-only file's name
// nor a link to it, because the list is filtered at the store boundary.
func TestRequesterDetailHidesAnInternalCommentAttachment(t *testing.T) {
	h := newHarness(t)
	owner := seedUserRole(t, h.store, "Rita", "rita@example.com", domain.RoleUser)
	ownerSession := seedSession(t, h.store, owner.ID)
	tkt := seedUserOwnedTicket(t, h, owner, "Owner's ticket")

	h.postMultipart(t, ticketPath(tkt.ID)+"/comments", url.Values{
		"body": {"public answer"}, "visibility": {"public"},
	}, []multipartFile{{name: "public.png", data: pngFixture}}, h.adminSession.ID)
	h.postMultipart(t, ticketPath(tkt.ID)+"/comments", url.Values{
		"body": {"staff only"}, "visibility": {"internal"},
	}, []multipartFile{{name: "internal.png", data: pngFixture}}, h.adminSession.ID)

	requesterView := doRequest(h.mux, h.mw, http.MethodGet, ticketPath(tkt.ID), sessionHeaders(ownerSession.ID))
	body := requesterView.Body.String()
	if !strings.Contains(body, "public.png") {
		t.Errorf("requester detail must list the public comment's file, got: %s", body)
	}
	if strings.Contains(body, "internal.png") {
		t.Errorf("requester detail must not list the internal comment's file, got: %s", body)
	}

	staffView := doRequest(h.mux, h.mw, http.MethodGet, ticketPath(tkt.ID), sessionHeaders(h.adminSession.ID))
	if !strings.Contains(staffView.Body.String(), "internal.png") {
		t.Errorf("staff detail must list the internal comment's file, got: %s", staffView.Body.String())
	}
}

// TestUrlencodedTicketAndCommentKeepWorking pins the previous behaviour of the
// two routes (S6): a plain form post carries no files, creates as it always
// did, and stores no attachment.
func TestUrlencodedTicketAndCommentKeepWorking(t *testing.T) {
	h := newHarness(t)
	rec := h.postForm(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	rec = h.postForm(t, ticketPath(1)+"/comments", url.Values{"body": {"Checking now"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, ticketPath(1))

	files, err := h.store.AttachmentStore().ListByTicket(t.Context(), 1, true)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("attachments = %d, want 0 for a urlencoded submission", len(files))
	}
}

// TestFileSizeRendersHumanUnits pins the formatter the timeline and the
// description reuse: bytes below a kilo, whole kilobytes, and tenths of a
// megabyte.
func TestFileSizeRendersHumanUnits(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1500, "1.5 KB"},
		{4096, "4 KB"},
		{10 << 20, "10 MB"},
		{1258291, "1.2 MB"},
	} {
		if got := fileSize(tc.bytes); got != tc.want {
			t.Errorf("fileSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// TestMultipartSpillFilesAreRemovedAfterHandling pins the cleanup half of the
// upload path (issue #366): a file part larger than the in-memory threshold
// spills to a temp file, and the request releases it once handled, so a
// ticket with five files cannot leave the server's temp directory growing.
func TestMultipartSpillFilesAreRemovedAfterHandling(t *testing.T) {
	spill := t.TempDir()
	t.Setenv("TMPDIR", spill)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile(attachmentField, "big.png")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	payload := append(append([]byte{}, pngFixture...), make([]byte, multipartMemoryBytes+1)...)
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/tickets", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	uploads, err := parseTicketForm(req)
	if err != nil {
		t.Fatalf("parse multipart form: %v", err)
	}
	if len(uploads) != 1 || len(uploads[0].Data) != len(payload) {
		t.Fatalf("uploads = %d parts, want the single %d-byte file", len(uploads), len(payload))
	}

	spilled, err := filepath.Glob(filepath.Join(spill, "multipart-*"))
	if err != nil {
		t.Fatalf("glob spill dir: %v", err)
	}
	if len(spilled) == 0 {
		t.Fatalf("a file part over the in-memory threshold must spill to disk, got no temp file in %s", spill)
	}

	releaseMultipartForm(req)

	remaining, err := filepath.Glob(filepath.Join(spill, "multipart-*"))
	if err != nil {
		t.Fatalf("glob spill dir: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("temp files left behind after handling: %v", remaining)
	}
}

// TestSpanishAttachmentCopyFollowsTheInstanceLanguage is S8 on the real path:
// a Spanish instance renders the refused-file message and both new form
// strings in Spanish, interpolating the file name through the catalogue key
// instead of falling back to the English domain text.
func TestSpanishAttachmentCopyFollowsTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}

	rec := h.postMultipart(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), []multipartFile{{name: "fake.png", data: notAnImage}}, h.adminSession.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %.400s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"no se puede adjuntar fake.png: solo se permiten imágenes, PDF, documentos de Office, archivos de texto y zip",
		"Archivos adjuntos",
		"Hasta 5 archivos de 10 MB cada uno",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish re-render must carry %q, got: %.600s", want, body)
		}
	}
	if strings.Contains(body, `"error.`) {
		t.Errorf("Spanish re-render leaked a raw catalogue key: %.600s", body)
	}
}
