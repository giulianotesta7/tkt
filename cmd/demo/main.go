// Command demo wires the full stack (sqlite + application + http) so the
// web UI can be exercised before Phase 6 lands the real composition root.
// SCRATCH: not part of any slice, never commit.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	httpadapter "github.com/giulianotesta7/tkt/internal/adapters/http"
	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func main() {
	store, err := sqlite.Open("data/demo.db")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	clock := systemClock{}

	users := store.UserStore()
	sessions := store.SessionStore()
	categories := store.CategoryStore()
	tickets := store.TicketStore()
	comments := store.CommentStore()
	audits := store.AuditStore()
	search := store.SearchStore()

	viewBuilder := application.NewViewBuilder(tickets, users, categories, comments, audits)
	userSvc := application.NewUserService(users, clock)
	catSvc := application.NewCategoryService(categories, clock)
	commentSvc := application.NewCommentService(tickets, comments, clock)
	ticketSvc := application.NewTicketService(tickets, users, categories, store.TicketUnitOfWork(), viewBuilder, clock)
	authSvc := application.NewAuthService(users, sessions, clock)
	searchSvc := application.NewSearchService(tickets, search)

	renderer := httpadapter.NewRenderer()
	if err != nil {
		log.Fatalf("renderer: %v", err)
	}

	mux := http.NewServeMux()
	httpadapter.RegisterStatic(mux)
	httpadapter.NewAuthHandlers(authSvc, userSvc, renderer).Register(mux)
	httpadapter.NewTicketHandlers(ticketSvc, commentSvc, searchSvc, catSvc, userSvc, renderer).Register(mux)
	httpadapter.NewUserHandlers(userSvc, renderer).Register(mux)
	httpadapter.NewCategoryHandlers(catSvc, renderer).Register(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	handler := httpadapter.NewSessionMiddleware(sessions, users).Wrap(mux)
	log.Println("demo server on http://localhost:8080 (db: data/demo.db)")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatal(err)
	}
}
