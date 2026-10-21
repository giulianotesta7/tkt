package httpadapter

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Upload routes (issue #366, S6): ticket creation and comments accept a
// multipart body of up to maxUploadBodyBytes — five 10 MB files plus the
// form — while every other route, and those routes without multipart, keep
// the 1 MiB cap. The upload body is never buffered whole by the middleware:
// it reaches the handler behind http.MaxBytesReader.

const multipartType = "multipart/form-data; boundary=x"

// zeros is an endless source of zero bytes for large synthetic bodies.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// readAllHandler drains the body and records how many bytes it got and the
// read error, so a test sees exactly what the handler saw.
func readAllHandler(n *int64, readErr *error, ran *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		got, err := io.Copy(io.Discard, r.Body)
		*n, *readErr = got, err
		w.Write([]byte("ok"))
	}
}

func uploadMux(n *int64, readErr *error, ran *bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", readAllHandler(n, readErr, ran))
	mux.HandleFunc("POST /tickets/{id}/comments", readAllHandler(n, readErr, ran))
	mux.HandleFunc("POST /tickets/{id}/transition", readAllHandler(n, readErr, ran))
	return mux
}

func TestUploadLimitIsFiveFilesPlusTheForm(t *testing.T) {
	if maxUploadBodyBytes != 5*(10<<20)+(1<<20) {
		t.Errorf("maxUploadBodyBytes = %d, want 5 x 10 MiB + 1 MiB", maxUploadBodyBytes)
	}
}

func TestUploadRoutesAcceptMoreThanOneMiB(t *testing.T) {
	for _, path := range []string{"/tickets", "/tickets/7/comments"} {
		t.Run(path, func(t *testing.T) {
			var n int64
			var readErr error
			var ran bool
			const size = 20 << 20
			req := httptest.NewRequest(http.MethodPost, path, io.LimitReader(zeros{}, size))
			req.ContentLength = size
			req.Header.Set("Content-Type", multipartType)
			rec := httptest.NewRecorder()
			NewBodyLimitMiddleware().Wrap(uploadMux(&n, &readErr, &ran)).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !ran || n != size || readErr != nil {
				t.Errorf("status=%d ran=%v read=%d err=%v, want the whole 20 MiB body", rec.Code, ran, n, readErr)
			}
		})
	}
}

func TestUploadRoutesRefuseADeclaredOversizeBeforeTheHandler(t *testing.T) {
	var n int64
	var readErr error
	var ran bool
	src := &countingReader{r: io.LimitReader(zeros{}, maxUploadBodyBytes+1)}
	req := httptest.NewRequest(http.MethodPost, "/tickets/7/comments", src)
	req.ContentLength = maxUploadBodyBytes + 1
	req.Header.Set("Content-Type", multipartType)
	rec := httptest.NewRecorder()
	NewBodyLimitMiddleware().Wrap(uploadMux(&n, &readErr, &ran)).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || ran || src.n != 0 {
		t.Errorf("status=%d ran=%v read=%d, want 413 before any read", rec.Code, ran, src.n)
	}
}

// TestUploadRoutesBoundAnUndeclaredBody: a chunked upload is not buffered by
// the middleware; the handler reads at most the cap and gets a MaxBytesError.
func TestUploadRoutesBoundAnUndeclaredBody(t *testing.T) {
	var n int64
	var readErr error
	var ran bool
	src := &countingReader{r: io.LimitReader(zeros{}, 2*maxUploadBodyBytes)}
	req := httptest.NewRequest(http.MethodPost, "/tickets", src)
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("Content-Type", multipartType)
	rec := httptest.NewRecorder()
	NewBodyLimitMiddleware().Wrap(uploadMux(&n, &readErr, &ran)).ServeHTTP(rec, req)
	var maxErr *http.MaxBytesError
	if !ran || !errors.As(readErr, &maxErr) {
		t.Fatalf("ran=%v err=%v, want the handler to see a MaxBytesError", ran, readErr)
	}
	if n > maxUploadBodyBytes || src.n > maxUploadBodyBytes+1<<16 {
		t.Errorf("read %d (source %d), want at most the cap", n, src.n)
	}
}

// TestOneMiBCapStaysEverywhereElse pins the previous behaviour: other routes,
// and the upload routes without a multipart body, still refuse 2 MiB.
func TestOneMiBCapStaysEverywhereElse(t *testing.T) {
	for _, tc := range []struct{ path, contentType string }{
		{"/tickets/7/transition", multipartType},
		{"/tickets", "application/x-www-form-urlencoded"},
		{"/tickets/7/comments", "application/x-www-form-urlencoded"},
		{"/tickets/abc/comments", multipartType},
		{"/tickets/7/comments/extra", multipartType},
	} {
		t.Run(tc.path+" "+strings.Fields(tc.contentType)[0], func(t *testing.T) {
			var n int64
			var readErr error
			var ran bool
			const size = 2 << 20
			req := httptest.NewRequest(http.MethodPost, tc.path, io.LimitReader(zeros{}, size))
			req.ContentLength = size
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			NewBodyLimitMiddleware().Wrap(uploadMux(&n, &readErr, &ran)).ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge || ran {
				t.Errorf("status=%d ran=%v, want the 1 MiB cap (413)", rec.Code, ran)
			}
		})
	}
}
