package httpadapter

import (
	"net/http"

	"github.com/giulianotesta7/tkt/internal/application"
)

// requireCapability rejects before a handler queries, mutates, or renders
// restricted data. The session middleware supplies the actor for all app
// routes; the nil check still fails closed for handlers used in isolation.
func requireCapability(w http.ResponseWriter, r *http.Request, cap application.Capability) bool {
	actor := userFromContext(r.Context())
	if actor == nil || !application.NewPolicy().Capabilities(actor.Role).Require(cap) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	return true
}
