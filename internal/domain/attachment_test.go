package domain_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Attachment validation (issue #366, S1/S5): at most 5 files per submission,
// 10 MB each, of the allowed types — checked against the real content, not
// only the extension.

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	jpegBytes = append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 32)...)
	gifBytes  = append([]byte("GIF89a"), make([]byte, 32)...)
	webpBytes = append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	pdfBytes  = []byte("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n1 0 obj\n")
)

// zipWith builds a real zip archive holding the named entries.
func zipWith(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, n := range names {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func requireAttachmentKey(t *testing.T, err error, key string) *domain.ValidationError {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Key != key {
		t.Fatalf("err = %#v, want ValidationError key %q", err, key)
	}
	return ve
}

func TestValidateAttachmentsAcceptsEveryAllowedType(t *testing.T) {
	docx := zipWith(t, "[Content_Types].xml", "word/document.xml")
	xlsx := zipWith(t, "[Content_Types].xml", "xl/workbook.xml")
	pptx := zipWith(t, "[Content_Types].xml", "ppt/presentation.xml")
	plainZip := zipWith(t, "logs/app.log")
	tests := []struct {
		name, wantType string
		data           []byte
	}{
		{"screen.png", "image/png", pngBytes},
		{"photo.JPG", "image/jpeg", jpegBytes},
		{"photo.jpeg", "image/jpeg", jpegBytes},
		{"anim.gif", "image/gif", gifBytes},
		{"pic.webp", "image/webp", webpBytes},
		{"manual.pdf", "application/pdf", pdfBytes},
		{"report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx},
		{"sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsx},
		{"deck.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", pptx},
		{"notes.txt", "text/plain; charset=utf-8", []byte("hola, la VPN se corta cada hora\n")},
		{"data.csv", "text/csv; charset=utf-8", []byte("a,b\n1,2\n")},
		{"app.log", "text/plain; charset=utf-8", []byte("2026-10-21 ERROR timeout\r\n\tat main\n")},
		{"bundle.zip", "application/zip", plainZip},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: tc.name, Data: tc.data}})
			if err != nil {
				t.Fatalf("ValidateAttachments: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d attachments", len(got))
			}
			a := got[0]
			if a.FileName != tc.name || a.ContentType != tc.wantType || a.Size != int64(len(tc.data)) || !bytes.Equal(a.Data, tc.data) {
				t.Errorf("attachment = {%q %q %d}, want {%q %q %d}", a.FileName, a.ContentType, a.Size, tc.name, tc.wantType, len(tc.data))
			}
		})
	}
}

func TestValidateAttachmentsRejectsContentThatDoesNotMatchTheName(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"screen.png", pdfBytes},                                              // a PDF named .png
		{"report.docx", zipWith(t, "xl/workbook.xml")},                        // a spreadsheet named .docx
		{"report.docx", zipWith(t, "readme.txt")},                             // a plain zip named .docx
		{"report.docx", zipWith(t, "[Content_Types].xml", "xl/workbook.xml")}, // a real .xlsx named .docx
		{"report.docx", zipWith(t, "word/document.xml")},                      // no Office package manifest
		{"notes.txt", pngBytes},                                               // binary named .txt
		{"notes.txt", []byte("bad \xff\xfe utf-8")},                           // invalid UTF-8
		{"page.html", []byte("<html><script>alert(1)</script></html>")},       // not an allowed type
		{"icon.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{"run.exe", []byte("MZ\x90\x00")},
		{"noextension", pngBytes},
		{"bundle.zip", []byte("PK not really a zip")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: tc.name, Data: tc.data}})
			if got != nil {
				t.Errorf("got attachments %v, want none", got)
			}
			ve := requireAttachmentKey(t, err, "error.attachment_type_not_allowed")
			if len(ve.Args) != 1 || ve.Args[0] != tc.name {
				t.Errorf("args = %v, want the file name", ve.Args)
			}
		})
	}
}

func TestValidateAttachmentsSizeLimits(t *testing.T) {
	exact := append([]byte("%PDF-"), make([]byte, domain.MaxAttachmentBytes-5)...)
	if _, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: "a.pdf", Data: exact}}); err != nil {
		t.Errorf("exactly 10 MB: %v, want accepted", err)
	}
	over := append(exact, 0)
	_, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: "big.pdf", Data: over}})
	ve := requireAttachmentKey(t, err, "error.attachment_too_large")
	if len(ve.Args) != 1 || ve.Args[0] != "big.pdf" || ve.Message != "big.pdf is larger than 10 MB" {
		t.Errorf("args/message = %v %q, want the file name", ve.Args, ve.Message)
	}
	_, err = domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: "empty.txt", Data: nil}})
	requireAttachmentKey(t, err, "error.attachment_empty")
	if domain.MaxAttachmentBytes != 10<<20 {
		t.Errorf("MaxAttachmentBytes = %d, want 10 MiB", domain.MaxAttachmentBytes)
	}
}

func TestValidateAttachmentsCountLimit(t *testing.T) {
	five := make([]domain.AttachmentUpload, 5)
	for i := range five {
		five[i] = domain.AttachmentUpload{FileName: "s.png", Data: pngBytes}
	}
	if got, err := domain.ValidateAttachments(five); err != nil || len(got) != 5 {
		t.Fatalf("five files: %d, %v", len(got), err)
	}
	six := append(five, domain.AttachmentUpload{FileName: "t.png", Data: pngBytes})
	got, err := domain.ValidateAttachments(six)
	if got != nil {
		t.Errorf("got %d attachments, want none", len(got))
	}
	requireAttachmentKey(t, err, "error.attachment_too_many")
	if none, err := domain.ValidateAttachments(nil); err != nil || len(none) != 0 {
		t.Errorf("no files: %v, %v", none, err)
	}
}

// TestValidateAttachmentsIsAllOrNothing: one bad file refuses the whole
// submission, so nothing is ever written alongside a refusal (S5).
func TestValidateAttachmentsIsAllOrNothing(t *testing.T) {
	got, err := domain.ValidateAttachments([]domain.AttachmentUpload{
		{FileName: "ok.png", Data: pngBytes},
		{FileName: "bad.exe", Data: []byte("MZ")},
	})
	if got != nil {
		t.Errorf("got %v, want nothing", got)
	}
	requireAttachmentKey(t, err, "error.attachment_type_not_allowed")
}

func TestValidateAttachmentsSanitisesTheFileName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"C:\\Users\\ana\\Desktop\\captura.png", "captura.png"},
		{"../../etc/passwd.txt", "passwd.txt"},
		{"  informe final.pdf  ", "informe final.pdf"},
		{"línea\x00\x1b[31m.txt", "línea[31m.txt"},
		{"\"quoted\".txt", "quoted.txt"},
	}
	for _, tc := range tests {
		data := pngBytes
		switch {
		case strings.HasSuffix(tc.want, ".pdf"):
			data = pdfBytes
		case strings.HasSuffix(tc.want, ".txt"):
			data = []byte("text")
		}
		got, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: tc.in, Data: data}})
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got[0].FileName != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got[0].FileName, tc.want)
		}
	}
	long := strings.Repeat("a", 300) + ".txt"
	got, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: long, Data: []byte("x")}})
	if err != nil {
		t.Fatalf("long name: %v", err)
	}
	if n := len(got[0].FileName); n > domain.MaxAttachmentNameBytes || !strings.HasSuffix(got[0].FileName, ".txt") {
		t.Errorf("long name -> %d bytes %q, want <= %d keeping the extension", n, got[0].FileName, domain.MaxAttachmentNameBytes)
	}
	for _, blank := range []string{"", "   ", "../", "\x00"} {
		_, err := domain.ValidateAttachments([]domain.AttachmentUpload{{FileName: blank, Data: []byte("x")}})
		requireAttachmentKey(t, err, "error.attachment_name_required")
	}
}
