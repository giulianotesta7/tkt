package httpadapter

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// API errors (issue #489, S3) are RFC 9457 problem details: one
// application/problem+json body with a stable `type` per problem kind, a short
// `title` for that kind, the HTTP `status`, a `detail` about this occurrence,
// and the request path as `instance`. A validation failure adds the extension
// member `errors`, one entry per rejected field.
//
// Each type is a URN, so it is a valid absolute URI that needs no host the
// instance may not have. The vocabulary is closed and documented in the API
// reference; clients branch on `type`, never on `detail`, whose wording may
// change. The API speaks English: `detail` is the domain's English message, not
// the browser's localized one.

const problemContentType = "application/problem+json"

// apiProblemKind is one entry of the closed problem vocabulary.
type apiProblemKind struct {
	Type   string
	Title  string
	Status int
}

var (
	problemBadRequest       = apiProblemKind{"urn:tkt:problem:bad-request", "Bad request", http.StatusBadRequest}
	problemUnauthorized     = apiProblemKind{"urn:tkt:problem:unauthorized", "Invalid credentials", http.StatusUnauthorized}
	problemForbidden        = apiProblemKind{"urn:tkt:problem:forbidden", "Not permitted", http.StatusForbidden}
	problemScopeMissing     = apiProblemKind{"urn:tkt:problem:scope-missing", "Missing scope", http.StatusForbidden}
	problemNotFound         = apiProblemKind{"urn:tkt:problem:not-found", "Not found", http.StatusNotFound}
	problemMethodNotAllowed = apiProblemKind{"urn:tkt:problem:method-not-allowed", "Method not allowed", http.StatusMethodNotAllowed}
	problemConflict         = apiProblemKind{"urn:tkt:problem:conflict", "Conflict", http.StatusConflict}
	problemTooLarge         = apiProblemKind{"urn:tkt:problem:request-too-large", "Request too large", http.StatusRequestEntityTooLarge}
	problemUnsupportedMedia = apiProblemKind{"urn:tkt:problem:unsupported-media-type", "Unsupported media type", http.StatusUnsupportedMediaType}
	problemValidation       = apiProblemKind{"urn:tkt:problem:validation", "Invalid request", http.StatusUnprocessableEntity}
	problemRejected         = apiProblemKind{"urn:tkt:problem:rejected", "Request not allowed in the current state", http.StatusUnprocessableEntity}
	problemRateLimited      = apiProblemKind{"urn:tkt:problem:rate-limited", "Too many requests", http.StatusTooManyRequests}
	problemInternal         = apiProblemKind{"urn:tkt:problem:internal", "Internal error", http.StatusInternalServerError}
)

// apiProblemKinds lists the whole vocabulary, so a test can pin it against the
// API reference.
var apiProblemKinds = []apiProblemKind{
	problemBadRequest, problemUnauthorized, problemForbidden, problemScopeMissing,
	problemNotFound, problemMethodNotAllowed, problemConflict, problemTooLarge, problemUnsupportedMedia,
	problemValidation, problemRejected, problemRateLimited, problemInternal,
}

// apiProblem is the RFC 9457 body.
type apiProblem struct {
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Status   int               `json:"status"`
	Detail   string            `json:"detail,omitempty"`
	Instance string            `json:"instance,omitempty"`
	Errors   []apiProblemField `json:"errors,omitempty"`
}

// apiProblemField is one rejected field of a validation problem.
type apiProblemField struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// writeProblem answers one problem of kind with detail.
func writeProblem(w http.ResponseWriter, r *http.Request, kind apiProblemKind, detail string) {
	writeProblemBody(w, apiProblem{
		Type: kind.Type, Title: kind.Title, Status: kind.Status,
		Detail: detail, Instance: r.URL.Path,
	})
}

func writeProblemBody(w http.ResponseWriter, p apiProblem) {
	w.Header().Set("Content-Type", problemContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// writeErrorProblem maps an application error to its problem. Every domain
// error the services return has a kind; anything else is an internal error
// whose detail is never the raw error, so no store or driver message leaks.
func writeErrorProblem(w http.ResponseWriter, r *http.Request, err error) {
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		p := apiProblem{
			Type: problemValidation.Type, Title: problemValidation.Title, Status: problemValidation.Status,
			Detail: validation.Error(), Instance: r.URL.Path,
		}
		if validation.Field != "" {
			p.Errors = []apiProblemField{{Field: validation.Field, Detail: validation.Error()}}
		}
		writeProblemBody(w, p)
		return
	}
	status, english := mapError(err)
	switch status {
	case http.StatusNotFound:
		writeProblem(w, r, problemNotFound, english)
	case http.StatusForbidden:
		writeProblem(w, r, problemForbidden, english)
	case http.StatusConflict:
		writeProblem(w, r, problemConflict, english)
	case http.StatusUnprocessableEntity:
		writeProblem(w, r, problemRejected, english)
	case http.StatusUnauthorized:
		writeProblem(w, r, problemUnauthorized, "")
	default:
		writeProblem(w, r, problemInternal, "")
	}
}
