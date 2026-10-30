package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// MCP (issue #493, S7-S9): /mcp is a stateless Streamable HTTP endpoint whose
// tools act as the person who approved the client. tkt authenticates the
// bearer token itself, so the challenges, the Origin check and the switch-off
// 404 follow the spec; the go-sdk only speaks JSON-RPC. Each tool runs its
// /api/v1 handler in process, so the API's scope gates, role rules and problem
// vocabulary are the only authorization MCP has.

const (
	mcpPath = "/mcp"
	// mcpMaxBody caps a JSON-RPC request, as the API caps its bodies.
	mcpMaxBody = 1 << 20
)

// MCPHandlers owns /mcp: token verification, the per-grant rate limit through
// the API's buckets, and the go-sdk server holding the tools.
type MCPHandlers struct {
	oauth   *application.OAuthService
	api     *APIHandlers
	server  *mcp.Server
	handler http.Handler
}

// NewMCPHandlers wires /mcp over the authorization server and the API whose
// handlers the tools run. Configure api with its reads and writes first.
func NewMCPHandlers(oauth *application.OAuthService, api *APIHandlers) *MCPHandlers {
	h := &MCPHandlers{oauth: oauth, api: api}
	h.server = mcp.NewServer(&mcp.Implementation{Name: "tkt", Title: "tkt help desk", Version: "1"}, nil)
	h.addTools()
	// The go-sdk's localhost guard refuses a loopback connection with a public
	// Host, which is exactly a reverse proxy on the same machine. A bearer
	// token is not an ambient credential, and ServeHTTP checks Origin itself.
	h.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return h.server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, DisableLocalhostProtection: true, MaxRequestBodyBytes: mcpMaxBody,
	})
	return h
}

// Branch routes exactly /mcp before browser sessions and their Origin gate.
func (h *MCPHandlers) Branch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == mcpPath {
			h.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ServeHTTP checks, in order: the switch, Origin, the bearer token, the
// grant's rate limit and the scopes a tools/call needs. Only then does the
// request reach the go-sdk, carrying the person as the API would.
func (h *MCPHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ep, err := h.oauth.Endpoints(r.Context())
	if errors.Is(err, application.ErrMCPDisabled) {
		writeMCPError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeMCPError(w, http.StatusInternalServerError, "server_error")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, ep.Issuer) {
		writeMCPError(w, http.StatusForbidden, "forbidden")
		return
	}
	metadata := `resource_metadata="` + ep.Issuer + `/.well-known/oauth-protected-resource/mcp"`
	scopes := `scope="` + joinMCPScopes(ep.ScopesSupported) + `"`
	token := bearerToken(r)
	if token == "" {
		w.Header().Set("WWW-Authenticate", "Bearer "+metadata+", "+scopes)
		writeMCPError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	p, err := h.oauth.Authenticate(r.Context(), token)
	switch {
	case errors.Is(err, application.ErrMCPDisabled):
		writeMCPError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, application.ErrAPIUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", `+metadata+", "+scopes)
		writeMCPError(w, http.StatusUnauthorized, "invalid_token")
		return
	case err != nil:
		writeMCPError(w, http.StatusInternalServerError, "server_error")
		return
	}
	if retry := h.api.take("grant:" + strconv.FormatInt(p.GrantID, 10)); retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeMCPError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	required, err := peekRequiredScopes(r)
	if err != nil {
		writeMCPError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if missing := missingScopes(p, required); len(missing) > 0 {
		// RFC 6750 step-up: ask for what the grant holds plus what this call
		// needs, so re-authorizing does not drop a scope already granted.
		var union []domain.APIKeyScope
		for _, s := range ep.ScopesSupported {
			if p.HasScope(s) || containsScope(missing, s) {
				union = append(union, s)
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+joinMCPScopes(union)+`", `+metadata)
		writeMCPError(w, http.StatusForbidden, "insufficient_scope")
		return
	}
	// No API key: audit provenance is the grant, and the actor the person.
	ctx := context.WithValue(r.Context(), ctxKeyUser{}, &p.User)
	ctx = context.WithValue(ctx, ctxKeyAPIPrincipal{}, p)
	ctx = application.WithOAuthGrantID(ctx, p.GrantID)
	h.handler.ServeHTTP(w, r.WithContext(ctx))
}

func writeMCPError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

// sameOrigin compares an Origin header with the public URL by scheme and host.
func sameOrigin(origin, issuer string) bool {
	o, err := url.Parse(origin)
	if err != nil || o.Host == "" {
		return false
	}
	i, err := url.Parse(issuer)
	if err != nil {
		return false
	}
	return strings.EqualFold(o.Scheme, i.Scheme) && strings.EqualFold(originHost(o), originHost(i))
}

// originHost answers the host of u with its scheme's default port dropped, so
// a public URL stored as https://host:443 matches the Origin https://host.
func originHost(u *url.URL) string {
	port := u.Port()
	if (strings.EqualFold(u.Scheme, "https") && port == "443") || (strings.EqualFold(u.Scheme, "http") && port == "80") {
		return u.Hostname()
	}
	return u.Host
}

func joinMCPScopes(scopes []domain.APIKeyScope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = string(s)
	}
	return strings.Join(parts, " ")
}

func containsScope(scopes []domain.APIKeyScope, scope domain.APIKeyScope) bool {
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func missingScopes(p *application.APIPrincipal, required []domain.APIKeyScope) []domain.APIKeyScope {
	var missing []domain.APIKeyScope
	for _, s := range required {
		if !p.HasScope(s) && !containsScope(missing, s) {
			missing = append(missing, s)
		}
	}
	return missing
}

// peekRequiredScopes reads the JSON-RPC body, a message or a batch, and
// answers the scopes its tools/call messages need. The body is restored for the
// go-sdk. A body over the cap or that is not JSON needs nothing here: the
// go-sdk refuses it. This is only the step-up hint; each tool still runs the
// API's own scope gate.
func peekRequiredScopes(r *http.Request) ([]domain.APIKeyScope, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, mcpMaxBody+1))
	if err != nil {
		return nil, err
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), r.Body), r.Body}
	if len(data) > mcpMaxBody {
		return nil, nil
	}
	var messages []json.RawMessage
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		if json.Unmarshal(trimmed, &messages) != nil {
			return nil, nil
		}
	} else {
		messages = []json.RawMessage{trimmed}
	}
	var required []domain.APIKeyScope
	for _, raw := range messages {
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Method != "tools/call" {
			continue
		}
		if scope, ok := toolScope(msg.Params.Name, msg.Params.Arguments); ok {
			required = append(required, scope)
		}
	}
	return required, nil
}

// toolScope answers the scope a tool needs; whoami and unknown tools need none.
func toolScope(name string, arguments json.RawMessage) (domain.APIKeyScope, bool) {
	switch name {
	case "list_categories":
		return domain.ScopeCatalogRead, true
	case "search_tickets", "get_ticket":
		return domain.ScopeTicketsRead, true
	case "create_ticket", "complete_workflow_step", "transition_ticket", "confirm_resolution", "reject_resolution":
		return domain.ScopeTicketsWrite, true
	case "add_comment":
		var in struct {
			Internal bool `json:"internal"`
		}
		if json.Unmarshal(arguments, &in) == nil && in.Internal {
			return domain.ScopeCommentsInternal, true
		}
		return domain.ScopeCommentsWrite, true
	}
	return "", false
}
