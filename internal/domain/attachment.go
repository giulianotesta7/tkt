package domain

import (
	"archive/zip"
	"bytes"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Attachment limits (issue #366, maintainer decision S1).
const (
	// MaxAttachmentBytes is the per-file size cap: 10 MiB.
	MaxAttachmentBytes = 10 << 20
	// MaxAttachmentsPerSubmission caps the files one ticket creation or one
	// comment may carry.
	MaxAttachmentsPerSubmission = 5
	// MaxAttachmentNameBytes bounds the stored file name; a longer name is
	// shortened, keeping its extension.
	MaxAttachmentNameBytes = 255
)

// Attachment is a file attached to a ticket at creation (CommentID nil) or to
// one of its comments (issue #366). Its visibility is the visibility of what
// it is attached to: an attachment on an internal comment is staff-only, any
// other attachment is visible to everyone who can open the ticket. Data is the
// file itself; ContentType is the canonical type of the VALIDATED content,
// never the one a client claimed.
type Attachment struct {
	ID             int64
	TicketID       int64
	CommentID      *int64
	FileName       string
	ContentType    string
	Size           int64
	Data           []byte
	UploaderUserID *int64
	CreatedAt      time.Time
}

// AttachmentUpload is one file as received: the name the client sent and the
// bytes. Nothing about it is trusted until ValidateAttachments accepts it.
type AttachmentUpload struct {
	FileName string
	Data     []byte
}

// attachmentKind maps an allowed extension to the canonical content type
// stored for it and the check its bytes must pass. The extension picks the
// expected kind and the content has to prove it: a PDF renamed to .png, a
// spreadsheet renamed to .docx, or binary data renamed to .txt is refused.
type attachmentKind struct {
	contentType string
	matches     func([]byte) bool
}

var attachmentKinds = map[string]attachmentKind{
	".png":  {"image/png", hasPrefix("\x89PNG\r\n\x1a\n")},
	".jpg":  {"image/jpeg", hasPrefix("\xFF\xD8\xFF")},
	".jpeg": {"image/jpeg", hasPrefix("\xFF\xD8\xFF")},
	".gif":  {"image/gif", func(b []byte) bool { return hasPrefix("GIF87a")(b) || hasPrefix("GIF89a")(b) }},
	".webp": {"image/webp", func(b []byte) bool { return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" }},
	".pdf":  {"application/pdf", hasPrefix("%PDF-")},
	".docx": {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", officeZip("word/")},
	".xlsx": {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", officeZip("xl/")},
	".pptx": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", officeZip("ppt/")},
	".txt":  {"text/plain; charset=utf-8", isPlainText},
	".log":  {"text/plain; charset=utf-8", isPlainText},
	".csv":  {"text/csv; charset=utf-8", isPlainText},
	".zip":  {"application/zip", func(b []byte) bool { _, ok := zipEntries(b); return ok }},
}

func hasPrefix(magic string) func([]byte) bool {
	return func(b []byte) bool { return bytes.HasPrefix(b, []byte(magic)) }
}

// zipEntries opens b as a zip archive and lists its entry names; a payload
// that only starts like a zip but does not parse is not a zip.
func zipEntries(b []byte) ([]string, bool) {
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names, true
}

// officeZip recognises an Office Open XML document: a zip with the package's
// [Content_Types].xml and the part folder of the expected application.
func officeZip(folder string) func([]byte) bool {
	return func(b []byte) bool {
		names, ok := zipEntries(b)
		if !ok {
			return false
		}
		var types, part bool
		for _, n := range names {
			types = types || n == "[Content_Types].xml"
			part = part || strings.HasPrefix(n, folder)
		}
		return types && part
	}
}

// isPlainText accepts valid UTF-8 without control characters other than the
// whitespace a text, CSV or log file legitimately carries.
func isPlainText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' {
			return false
		}
	}
	return true
}

// ValidateAttachments checks one submission's files (issue #366, S1/S5) and
// returns them ready to store, or a ValidationError naming the first problem.
// It is all-or-nothing: a single refused file refuses the submission, so a
// ticket or comment is never written with part of its files. The checks, in
// order: at most MaxAttachmentsPerSubmission files; every name sanitised to a
// usable base name; no empty file; at most MaxAttachmentBytes each; and an
// allowed extension whose content proves it. No files is a valid submission.
func ValidateAttachments(uploads []AttachmentUpload) ([]Attachment, error) {
	if len(uploads) > MaxAttachmentsPerSubmission {
		return nil, &ValidationError{Field: "attachments", Message: fmt.Sprintf(ErrMsgAttachmentTooManyFmt, MaxAttachmentsPerSubmission),
			Key: "error.attachment_too_many", Args: []any{MaxAttachmentsPerSubmission}}
	}
	out := make([]Attachment, 0, len(uploads))
	for _, u := range uploads {
		name := sanitizeAttachmentName(u.FileName)
		if name == "" {
			return nil, &ValidationError{Field: "attachments", Message: ErrMsgAttachmentNameRequired, Key: "error.attachment_name_required"}
		}
		if len(u.Data) == 0 {
			return nil, &ValidationError{Field: "attachments", Message: fmt.Sprintf(ErrMsgAttachmentEmptyFmt, name), Key: "error.attachment_empty", Args: []any{name}}
		}
		if len(u.Data) > MaxAttachmentBytes {
			return nil, &ValidationError{Field: "attachments", Message: fmt.Sprintf(ErrMsgAttachmentTooLargeFmt, name), Key: "error.attachment_too_large", Args: []any{name}}
		}
		kind, ok := attachmentKinds[strings.ToLower(path.Ext(name))]
		if !ok || !kind.matches(u.Data) {
			return nil, &ValidationError{Field: "attachments", Message: fmt.Sprintf(ErrMsgAttachmentTypeNotAllowedFmt, name), Key: "error.attachment_type_not_allowed", Args: []any{name}}
		}
		out = append(out, Attachment{FileName: name, ContentType: kind.contentType, Size: int64(len(u.Data)), Data: u.Data})
	}
	return out, nil
}

// sanitizeAttachmentName keeps only the base name a client sent (both slash
// styles: browsers on Windows may send a full path), drops control characters
// and quotes (they would break the Content-Disposition header on download),
// trims spaces, and shortens a name longer than MaxAttachmentNameBytes while
// keeping its extension. An empty result means the name is unusable.
func sanitizeAttachmentName(raw string) string {
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == utf8.RuneError {
			return -1
		}
		return r
	}, raw)
	name := strings.TrimSpace(raw)
	if name == "." || name == ".." {
		return ""
	}
	if len(name) <= MaxAttachmentNameBytes {
		return name
	}
	ext := path.Ext(name)
	if len(ext) > 16 {
		ext = ""
	}
	stem := name[:len(name)-len(ext)]
	limit := MaxAttachmentNameBytes - len(ext)
	for len(stem) > limit || !utf8.ValidString(stem) {
		stem = stem[:len(stem)-1]
	}
	return stem + ext
}
