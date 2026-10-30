package httpadapter

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #489, S3: every API error is an RFC 9457 problem with a stable type,
// its title and status, the request path as instance, and per-field errors for
// a validation failure. An unknown error never leaks its text.
func TestWriteErrorProblem(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		want       apiProblemKind
		wantDetail string
		wantFields []apiProblemField
	}{
		{"validation", &domain.ValidationError{Field: "title", Message: "title is required"}, problemValidation, "title is required",
			[]apiProblemField{{Field: "title", Detail: "title is required"}}},
		{"not found", &domain.NotFoundError{Kind: "ticket", ID: 9}, problemNotFound, (&domain.NotFoundError{Kind: "ticket", ID: 9}).Error(), nil},
		{"forbidden", domain.NewForbiddenError("nope"), problemForbidden, "nope", nil},
		{"conflict", &domain.DuplicateError{Kind: "user", Name: "x"}, problemConflict, (&domain.DuplicateError{Kind: "user", Name: "x"}).Error(), nil},
		{"rejected", &domain.InvalidTransitionError{From: domain.StateNew, To: domain.StateClosed}, problemRejected,
			(&domain.InvalidTransitionError{From: domain.StateNew, To: domain.StateClosed}).Error(), nil},
		{"internal", errors.New("sqlite: database is locked at /data/tkt.db"), problemInternal, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeErrorProblem(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tickets/9/transition", nil), tc.err)
			if rec.Code != tc.want.Status {
				t.Errorf("status = %d, want %d", rec.Code, tc.want.Status)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q", got)
			}
			var p apiProblem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
			}
			if p.Type != tc.want.Type || p.Title != tc.want.Title || p.Status != tc.want.Status ||
				p.Detail != tc.wantDetail || p.Instance != "/api/v1/tickets/9/transition" {
				t.Errorf("problem = %+v", p)
			}
			if len(p.Errors) != len(tc.wantFields) || (len(p.Errors) > 0 && p.Errors[0] != tc.wantFields[0]) {
				t.Errorf("errors = %+v, want %+v", p.Errors, tc.wantFields)
			}
			if strings.Contains(rec.Body.String(), "sqlite") {
				t.Errorf("the internal error leaked: %s", rec.Body.String())
			}
		})
	}
}

// The vocabulary is closed: unique types, each a tkt URN.
func TestAPIProblemVocabulary(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range apiProblemKinds {
		if !strings.HasPrefix(k.Type, "urn:tkt:problem:") || seen[k.Type] || k.Title == "" || k.Status < 400 {
			t.Errorf("bad problem kind %+v", k)
		}
		seen[k.Type] = true
	}
}
