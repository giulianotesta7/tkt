package httpadapter

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Upload handling for ticket attachments (issue #366, specs S1/S2/S3/S5/S7).
//
// A ticket creation or a comment may carry files. They arrive as a
// multipart/form-data body, are validated by the domain and written by the
// unit of work that creates the ticket or the comment, and are read back by
// the second half of this file: the download route and the ticket-detail
// listing.

// multipartMemoryBytes is how much of a multipart body is held in memory
// before its file parts spill to temp files. The upload routes accept up to
// 51 MiB (five 10 MB files plus the form), so buffering them whole would cost
// that much per request; one mebibyte covers the ordinary form and streams
// the files to disk.
const multipartMemoryBytes = 1 << 20

// attachmentField is the form field both file inputs use (issue #366).
const attachmentField = "attachments"

// isMultipartSubmission reports whether the request declares a multipart body.
// Anything else — including a urlencoded post to the same route — follows the
// pre-#366 path untouched.
func isMultipartSubmission(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "multipart/form-data"
}

// parseTicketForm reads one ticket-creation or comment submission (issue #366).
// A urlencoded body keeps the previous behaviour exactly: r.ParseForm and no
// files. A multipart body is parsed with a small in-memory threshold, so the
// file parts stream to temp files instead of all of them living in memory, and
// its `attachments` parts become uploads in the order the browser sent them.
// The files are NOT validated here: the use cases validate the whole
// submission, so a refused file also refuses the ticket or comment.
func parseTicketForm(r *http.Request) ([]domain.AttachmentUpload, error) {
	if !isMultipartSubmission(r) {
		return nil, r.ParseForm()
	}
	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		return nil, err
	}
	headers := r.MultipartForm.File[attachmentField]
	uploads := make([]domain.AttachmentUpload, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		uploads = append(uploads, domain.AttachmentUpload{FileName: header.Filename, Data: data})
	}
	return uploads, nil
}

// releaseMultipartForm removes the temp files a parsed multipart body may have
// left on disk. Callers defer it as soon as the parse succeeded, so every
// response path — created, refused, or re-rendered — cleans up after itself.
func releaseMultipartForm(r *http.Request) {
	if r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}

// renderUploadError answers a body that could not be parsed (issue #366). The
// body-cap middleware streams the upload routes behind http.MaxBytesReader
// instead of buffering them, so an undeclared oversize body reaches the
// handler as a *http.MaxBytesError: that is the 413 the middleware answers on
// every other route. Any other parse failure keeps the historical 500.
func renderUploadError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpError(w, r, "error.request_too_large", http.StatusRequestEntityTooLarge)
		return
	}
	httpError(w, r, "error.internal_server", http.StatusInternalServerError)
}

// WithAttachments wires the attachment read side into the ticket handlers
// (issue #366): the download route and the ticket-detail listing. It follows
// the fluent shape WithSLA and WithMetrics already use, so the constructor
// keeps the positional signature its existing call sites depend on. Without
// it the download route is not registered and the detail lists no files: an
// instance that was never wired answers exactly as it did before #366.
func (h *TicketHandlers) WithAttachments(store application.AttachmentStore, downloads *application.AttachmentService) *TicketHandlers {
	h.attachments = store
	h.attachmentDownloads = downloads
	return h
}

// ticketAttachments loads one ticket's files for the detail page and groups
// them by the comment they belong to (issue #366, S3). A creation file has no
// comment and comes back in the first list, under the ticket description.
// includeInternal is the SAME visibility the comment timeline is read with, so
// a requester never learns that a staff-only file exists.
func (h *TicketHandlers) ticketAttachments(r *http.Request, ticketID int64, includeInternal bool) ([]domain.Attachment, map[int64][]domain.Attachment, error) {
	if h.attachments == nil {
		return nil, nil, nil
	}
	files, err := h.attachments.ListByTicket(r.Context(), ticketID, includeInternal)
	if err != nil {
		return nil, nil, err
	}
	var creation []domain.Attachment
	var byComment map[int64][]domain.Attachment
	for _, file := range files {
		if file.CommentID == nil {
			creation = append(creation, file)
			continue
		}
		if byComment == nil {
			byComment = make(map[int64][]domain.Attachment, 1)
		}
		byComment[*file.CommentID] = append(byComment[*file.CommentID], file)
	}
	return creation, byComment, nil
}

// downloadAttachment serves GET /attachments/{id} (issue #366, S7): the file
// the actor may read, as a download and never as a document. The service
// answers every refusal as the same not-found, so a missing file, a file on an
// internal comment read by a requester, and a ticket outside the actor's scope
// are indistinguishable here.
func (h *TicketHandlers) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	if id == 0 {
		httpError(w, r, "error.invalid_attachment_id", http.StatusBadRequest)
		return
	}
	actor := *userFromContext(r.Context())
	stored, err := h.attachmentDownloads.Download(r.Context(), actor, id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}

	// The headers are the security boundary: the browser must save the file
	// instead of rendering it in this origin. Content-Type is the type the
	// domain PROVED from the bytes, never the one the client claimed; the
	// disposition keeps the original name (RFC 2231 for non-ASCII) and always
	// says attachment; nosniff stops the type from being re-derived, the
	// sandbox CSP neutralises anything that still tries to execute, and
	// no-store keeps a personal file out of every cache.
	w.Header().Set("Content-Type", stored.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": stored.FileName}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(stored.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stored.Data)
}

// fileSize renders a byte count for the attachment lists. Sizes are data, not
// chrome: the units are the same in every language, so they are not
// catalogued. A whole unit drops its decimal, so a file that is exactly ten
// megabytes reads "10 MB" and not "10.0 MB".
func fileSize(bytes int64) string {
	const (
		kilobyte = 1 << 10
		megabyte = 1 << 20
	)
	switch {
	case bytes < kilobyte:
		return fmt.Sprintf("%d B", bytes)
	case bytes < megabyte:
		if bytes%kilobyte == 0 {
			return fmt.Sprintf("%d KB", bytes/kilobyte)
		}
		return fmt.Sprintf("%.1f KB", float64(bytes)/kilobyte)
	default:
		if bytes%megabyte == 0 {
			return fmt.Sprintf("%d MB", bytes/megabyte)
		}
		return fmt.Sprintf("%.1f MB", float64(bytes)/megabyte)
	}
}
