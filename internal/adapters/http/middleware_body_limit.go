package httpadapter

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// maxRequestBodyBytes is the single, documented request-body cap applied to
// every route by BodyLimitMiddleware.
//
// Why this exact value: the only large payloads TKT accepts are free text
// typed by an authenticated user — a ticket title/description, a comment, a
// managed-user form, or the workflow builder's serialized draft. The largest
// realistic case is a long-form bug report pasted with a stack trace, or a
// workflow draft with dozens of steps; both are a few tens of kilobytes at
// the very most. 1 MiB leaves more than an order of magnitude of headroom for
// that text while still bounding every in-flight request to a single
// mebibyte of buffered memory, instead of the multi-gigabyte allocation an
// unbounded body would allow. It also mirrors net/http's own header bound
// (http.DefaultMaxHeaderBytes), so "one request may not cost unbounded
// memory" stays a familiar release.
//
// It is deliberately a constant and not configuration: issue #213 asks for
// one documented limit, and a knob would only add a way to disable the bound
// by accident.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// BodyLimitMiddleware caps how many body bytes any wrapped route may consume.
// It is a transport-boundary control: it runs before the mux and never
// inspects route semantics.
type BodyLimitMiddleware struct{}

// NewBodyLimitMiddleware builds the body-cap middleware.
func NewBodyLimitMiddleware() *BodyLimitMiddleware { return &BodyLimitMiddleware{} }

// Wrap returns the mux-wrapping handler. It covers every registered route and
// handles the two oversized cases explicitly:
//
//   - A truthful `Content-Length` above the cap is refused with 413 before
//     any handler runs, and without reading a single body byte.
//   - A body whose size is not truthfully declared (`Content-Length` absent
//     or -1, e.g. a chunked upload) is read through http.MaxBytesReader, so at
//     most cap+1 bytes can ever be buffered. Exceeding the cap becomes a clean
//     413 instead of the 500 that the handlers' ParseForm error path would
//     otherwise emit.
//
// A body at or under the cap is replayed to the handler untouched; a normal
// form post therefore behaves exactly as before.
//
// The response text follows the package's existing http.Error precedent
// rather than introducing a new error format.
func (m *BodyLimitMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}

		if r.ContentLength > maxRequestBodyBytes {
			http.Error(w, "Request entity too large", http.StatusRequestEntityTooLarge)
			return
		}

		limited := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		body, err := io.ReadAll(limited)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(w, "Request entity too large", http.StatusRequestEntityTooLarge)
				return
			}
			// Any other read failure is a malformed request, not a server
			// fault: reject it rather than forwarding a partial body.
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// Hand the handler a replayable, already-bounded body and a
		// Content-Length that reflects what was actually read.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}
