package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestIsAgentQueueClaimRequest(t *testing.T) {
	tests := []struct {
		name      string
		role      domain.Role
		hxRequest string
		target    string
		want      bool
	}{
		{"agent queue claim", domain.RoleAgent, "true", "agent-ticket-list", true},
		{"root", domain.RoleRoot, "true", "agent-ticket-list", false},
		{"admin", domain.RoleAdmin, "true", "agent-ticket-list", false},
		{"user", domain.RoleUser, "true", "agent-ticket-list", false},
		{"missing HTMX", domain.RoleAgent, "", "agent-ticket-list", false},
		{"wrong target", domain.RoleAgent, "true", "ticket-detail", false},
		{"hash-prefixed target", domain.RoleAgent, "true", "#agent-ticket-list", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/tickets/1/workflow/steps/1/complete", nil)
			if tt.hxRequest != "" {
				r.Header.Set("HX-Request", tt.hxRequest)
			}
			r.Header.Set("HX-Target", tt.target)
			if got := isAgentQueueClaimRequest(r, domain.User{Role: tt.role}); got != tt.want {
				t.Errorf("isAgentQueueClaimRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentQueueCurrentContext(t *testing.T) {
	tests := []struct {
		name              string
		currentURL        string
		isConflict        bool
		wantQ             string
		wantAssignedPage  int
		wantClaimablePage int
	}{
		{
			name:              "conflict retains valid context",
			currentURL:        "https://tkt.test/tickets?q=claims+today&assigned_page=2&claimable_page=3",
			isConflict:        true,
			wantQ:             "claims today",
			wantAssignedPage:  2,
			wantClaimablePage: 3,
		},
		{
			name:              "conflict normalizes invalid cursors",
			currentURL:        "https://tkt.test/tickets?q=claims&assigned_page=0&claimable_page=-2",
			isConflict:        true,
			wantQ:             "claims",
			wantAssignedPage:  1,
			wantClaimablePage: 1,
		},
		{
			name:              "success resets cursors",
			currentURL:        "https://tkt.test/tickets?q=claims&assigned_page=4&claimable_page=5",
			isConflict:        false,
			wantQ:             "claims",
			wantAssignedPage:  1,
			wantClaimablePage: 1,
		},
		{
			name:              "missing URL falls back",
			isConflict:        true,
			wantAssignedPage:  1,
			wantClaimablePage: 1,
		},
		{
			name:              "malformed URL falls back",
			currentURL:        "http://[::1",
			isConflict:        true,
			wantAssignedPage:  1,
			wantClaimablePage: 1,
		},
		{
			name:              "missing and malformed cursors normalize",
			currentURL:        "https://tkt.test/tickets?q=claims&assigned_page=nope",
			isConflict:        true,
			wantQ:             "claims",
			wantAssignedPage:  1,
			wantClaimablePage: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/tickets/1/workflow/steps/1/complete", nil)
			if tt.currentURL != "" {
				r.Header.Set("HX-Current-URL", tt.currentURL)
			}
			q, assignedPage, claimablePage := agentQueueCurrentContext(r, tt.isConflict)
			if q != tt.wantQ || assignedPage != tt.wantAssignedPage || claimablePage != tt.wantClaimablePage {
				t.Errorf("agentQueueCurrentContext() = (%q, %d, %d), want (%q, %d, %d)", q, assignedPage, claimablePage, tt.wantQ, tt.wantAssignedPage, tt.wantClaimablePage)
			}
		})
	}
}
