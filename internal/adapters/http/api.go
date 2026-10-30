package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type ctxKeyAPIPrincipal struct{}

type apiBucket struct {
	tokens float64
	at     time.Time
}

// APIHandlers owns the bearer-only API boundary and its per-key token buckets.
// Role authorization stays in application services; endpoints add scope gates.
// Buckets allow 60 requests immediately and refill at one token per second.
// They are local to this process and reset when the server restarts.
type APIHandlers struct {
	api     *application.APIAccessService
	clock   domain.Clock
	mu      sync.Mutex
	buckets map[int64]apiBucket
}

func NewAPIHandlers(api *application.APIAccessService, clock domain.Clock) *APIHandlers {
	return &APIHandlers{api: api, clock: clock, buckets: make(map[int64]apiBucket)}
}

// Branch routes /api/ before browser sessions and their Origin check. The
// browser handler is left untouched, including its existing body-limit order.
func (h *APIHandlers) Branch(browser http.Handler) http.Handler {
	api := h.Wrap(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		browser.ServeHTTP(w, r)
	})
}

// Wrap authenticates, rate-limits, stamps both actor and key, and bounds the
// body. Authentication checks the instance switch even for a malformed header,
// so every API request answers 404 while the switch is off. A valid credential
// records use even if a subsequent gate refuses it (S1/L3).
func (h *APIHandlers) Wrap(next http.Handler) http.Handler {
	limited := NewBodyLimitMiddleware().Wrap(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string
		if values := r.Header.Values("Authorization"); len(values) == 1 {
			parts := strings.Fields(values[0])
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				token = parts[1]
			}
		}
		p, err := h.api.Authenticate(r.Context(), token)
		if err != nil {
			switch {
			case errors.Is(err, application.ErrAPIDisabled):
				writeProblem(w, r, problemNotFound, "")
			case errors.Is(err, application.ErrAPIUnauthorized):
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeProblem(w, r, problemUnauthorized, "")
			default:
				writeProblem(w, r, problemInternal, "")
			}
			return
		}
		if retry := h.take(p.KeyID); retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeProblem(w, r, problemRateLimited, "")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser{}, &p.User)
		ctx = context.WithValue(ctx, ctxKeyAPIPrincipal{}, p)
		ctx = application.WithAPIKeyID(ctx, p.KeyID)
		r = r.WithContext(ctx)
		// Only errors emitted by the transport cap need translation. Once it
		// forwards to the endpoint, endpoint responses pass through unchanged.
		bridge := &apiBodyResponse{ResponseWriter: w, request: r}
		limited.ServeHTTP(bridge, r)
	})
}

func (h *APIHandlers) take(keyID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.clock.Now()
	// A full, idle bucket is equivalent to an absent one. Evict them to
	// avoid retaining every key ever presented for the process lifetime.
	for id, b := range h.buckets {
		if now.Sub(b.at) >= time.Minute {
			delete(h.buckets, id)
		}
	}
	b, ok := h.buckets[keyID]
	if !ok {
		b = apiBucket{tokens: 60, at: now}
	}
	if now.After(b.at) {
		b.tokens = math.Min(60, b.tokens+now.Sub(b.at).Seconds())
		b.at = now
	}
	if b.tokens < 1 {
		h.buckets[keyID] = b
		return int(math.Ceil(1 - b.tokens))
	}
	b.tokens--
	h.buckets[keyID] = b
	return 0
}

// ServeHTTP deliberately does not use a browser mux: unknown paths (including
// unclean paths) cannot trigger HTML fallbacks or canonical-path redirects.
func (h *APIHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/me" {
		writeProblem(w, r, problemNotFound, "")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeProblem(w, r, problemMethodNotAllowed, "")
		return
	}
	p, _ := r.Context().Value(ctxKeyAPIPrincipal{}).(*application.APIPrincipal)
	if p == nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeProblem(w, r, problemUnauthorized, "")
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID     int64                `json:"id"`
		Name   string               `json:"name"`
		Role   domain.Role          `json:"role"`
		Scopes []domain.APIKeyScope `json:"scopes"`
	}{p.User.ID, p.User.Name, p.User.Role, p.Scopes})
}

func requireScope(w http.ResponseWriter, r *http.Request, scope domain.APIKeyScope) bool {
	p, _ := r.Context().Value(ctxKeyAPIPrincipal{}).(*application.APIPrincipal)
	if p == nil || !p.HasScope(scope) {
		writeProblem(w, r, problemScopeMissing, "")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeProblem(w, r, problemUnsupportedMedia, "")
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeProblem(w, r, problemBadRequest, "")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		writeProblem(w, r, problemBadRequest, "")
		return false
	}
	return true
}

// apiBodyResponse translates the existing cap's plain-text transport errors
// without changing the browser middleware or duplicating its size/read rules.
// Problem responses already produced by endpoints are never translated.
type apiBodyResponse struct {
	http.ResponseWriter
	request *http.Request
	refused bool
}

func (w *apiBodyResponse) WriteHeader(status int) {
	if status >= 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		w.refused = true
		kind := problemBadRequest
		if status == http.StatusRequestEntityTooLarge {
			kind = problemTooLarge
		}
		writeProblem(w.ResponseWriter, w.request, kind, "")
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *apiBodyResponse) Write(p []byte) (int, error) {
	if w.refused {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
