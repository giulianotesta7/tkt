package httpadapter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMapError proves the D5 status mapping: typed domain errors become the
// right status codes with their English messages, InvalidCredentialsError is
// a single generic 401, and unknown errors degrade to 500 "Internal server
// error" (never leaking internal text).
func TestMapError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"validation", &domain.ValidationError{Field: "title", Message: domain.ErrMsgTitleRequired}, 422, domain.ErrMsgTitleRequired},
		{"invalid transition", domain.NewInvalidTransitionError(domain.StateNew, domain.StateClosed), 422, "transition not allowed from new to closed"},
		{"reopen reason required", domain.NewReopenReasonRequiredError(), 422, domain.ErrMsgReopenReasonRequired},
		{"inactive user", domain.NewInactiveUserError("user"), 422, domain.ErrMsgUserInactive},
		{"invalid priority", &domain.InvalidPriorityError{Field: "priority", Message: domain.ErrMsgInvalidPriority}, 422, domain.ErrMsgInvalidPriority},
		{"not found", &domain.NotFoundError{Kind: "ticket", ID: int64(7)}, 404, "ticket not found"},
		{"duplicate", &domain.DuplicateError{Kind: "user", Name: "ana@example.com"}, 409, `user "ana@example.com" already exists`},
		{"referenced", &domain.ReferencedError{Kind: "user", ID: int64(7)}, 409, "user is referenced and cannot be deleted"},
		{"invalid credentials", &application.InvalidCredentialsError{}, 401, application.ErrMsgInvalidCredentials},
		{"unknown", errors.New("boom"), 500, "Internal server error"},
		{"wrapped unknown", fmt.Errorf("wrap: %w", errors.New("boom")), 500, "Internal server error"},
		{"wrapped validation", fmt.Errorf("wrap: %w", &domain.ValidationError{Field: "title", Message: domain.ErrMsgTitleRequired}), 422, domain.ErrMsgTitleRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg := mapError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("mapError(%v) status = %d, want %d", tt.err, status, tt.wantStatus)
			}
			if msg != tt.wantMsg {
				t.Errorf("mapError(%v) message = %q, want %q", tt.err, msg, tt.wantMsg)
			}
		})
	}
}
