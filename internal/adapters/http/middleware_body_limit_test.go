package httpadapter

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// countingReader records how many bytes were pulled from the underlying
// reader, so a test can prove the middleware never buffers past its cap.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// TestBodyLimitAllowsPostAtAndUnderCap proves the pass-through half of the
// contract: a normal form post — and one whose body is exactly at the cap —
// reaches its handler with the body intact, through a real ParseForm call.
func TestBodyLimitAllowsPostAtAndUnderCap(t *testing.T) {
	const key = "description="
	cases := []struct {
		name string
		body string
	}{
		{"under cap", key + strings.Repeat("x", 4<<10)},
		{"exactly at cap", key + strings.Repeat("y", maxRequestBodyBytes-len(key))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			mux := http.NewServeMux()
			mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				got = r.Form.Get("description")
				w.Write([]byte("ok"))
			})

			req := httptest.NewRequest(http.MethodPost, "/tickets", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			NewBodyLimitMiddleware().Wrap(mux).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			if want := strings.TrimPrefix(tc.body, key); got != want {
				t.Errorf("handler saw %d body bytes, want %d", len(got), len(want))
			}
		})
	}
}

// TestBodyLimitRejectsDeclaredOversizeBeforeHandler proves a truthful
// Content-Length above the cap is refused 413 before the handler runs.
func TestBodyLimitRejectsDeclaredOversizeBeforeHandler(t *testing.T) {
	ran := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) {
		ran = true
		w.Write([]byte("created"))
	})

	body := strings.Repeat("x", maxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/tickets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// httptest derives ContentLength from the *strings.Reader, so the request
	// carries a truthful declared length above the cap.
	if req.ContentLength != int64(len(body)) {
		t.Fatalf("fixture ContentLength = %d, want %d", req.ContentLength, len(body))
	}
	rec := httptest.NewRecorder()
	NewBodyLimitMiddleware().Wrap(mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if ran {
		t.Error("handler must not run for a declared oversize body")
	}
}

// TestBodyLimitRejectsChunkedOversizeAndBoundsReads proves the second
// oversized case: a body that exceeds the cap without a truthful
// Content-Length (ContentLength -1, as a chunked upload reports) is rejected
// with a clean 413 — not the 500 the handlers' ParseForm error path would
// emit — and the middleware never pulls more than cap+1 bytes off the wire.
func TestBodyLimitRejectsChunkedOversizeAndBoundsReads(t *testing.T) {
	ran := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) {
		ran = true
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("created"))
	})

	src := &countingReader{r: strings.NewReader(strings.Repeat("x", 2*maxRequestBodyBytes))}
	req := httptest.NewRequest(http.MethodPost, "/tickets", src)
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	NewBodyLimitMiddleware().Wrap(mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 (a clean rejection, not 500)", rec.Code)
	}
	if ran {
		t.Error("handler must not run for an oversized body")
	}
	if src.n > maxRequestBodyBytes+1 {
		t.Errorf("middleware read %d body bytes, want at most %d (cap+1)", src.n, maxRequestBodyBytes+1)
	}
}

// TestBodyLimitRealFormPostReachesHandler proves a normal ticket-creation form
// post still flows through the real, fully wired server — real database,
// services, routes, and session middleware — with the body cap in the chain,
// using the production ordering (session outside, body limit inside).
func TestBodyLimitRealFormPostReachesHandler(t *testing.T) {
	h := newHarness(t)
	form := ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	})

	req := httptest.NewRequest(http.MethodPost, "/tickets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(NewBodyLimitMiddleware().Wrap(h.mux)).ServeHTTP(rec, req)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
}

// TestBodyLimitOrganizationUploadHasItsOwnCap proves the third upload route
// (issue #383, S4) carries neither the 1 MiB text cap nor the attachment cap: a
// multipart identity save with a logo under 256 KiB + 64 KiB reaches the
// handler, and a body above that cap is refused 413 before the handler runs.
func TestBodyLimitOrganizationUploadHasItsOwnCap(t *testing.T) {
	ran := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /settings/organization", func(w http.ResponseWriter, r *http.Request) {
		ran = true
		w.Write([]byte("saved"))
	})

	// A logo at the documented cap plus the multipart envelope travels: the
	// handler is what validates the image's type, not this middleware.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "Acme"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	part, err := writer.CreateFormFile("logo", "logo.png")
	if err != nil {
		t.Fatalf("create file part: %v", err)
	}
	if _, err := part.Write(make([]byte, maxOrganizationUploadBodyBytes-1024)); err != nil {
		t.Fatalf("write logo: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	// One byte over the cap: the middleware refuses before the handler.
	oversize := httptest.NewRequest(http.MethodPost, "/settings/organization", bytes.NewReader(make([]byte, maxOrganizationUploadBodyBytes+1)))
	oversize.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec := httptest.NewRecorder()
	NewBodyLimitMiddleware().Wrap(mux).ServeHTTP(rec, oversize)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize organization upload = %d, want 413", rec.Code)
	}
	if ran {
		t.Error("handler must not run for an oversize organization upload")
	}

	inside := httptest.NewRequest(http.MethodPost, "/settings/organization", &body)
	inside.Header.Set("Content-Type", writer.FormDataContentType())
	insideRec := httptest.NewRecorder()
	NewBodyLimitMiddleware().Wrap(mux).ServeHTTP(insideRec, inside)
	if insideRec.Code != http.StatusOK {
		t.Errorf("organization upload at the cap = %d, want it to reach the handler", insideRec.Code)
	}
	if !ran {
		t.Error("a body at the cap must reach the handler")
	}
}
