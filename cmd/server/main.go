// Command server is the tkt composition root (phase 6): environment
// configuration, sqlite open + migrate, service wiring, and the HTTP server.
//
// Configuration is read from environment variables:
//
//	TKT_DB_PATH    sqlite database file path (default "data/tkt.db")
//	TKT_LISTEN     listen address (default ":8080")
//
// Flags:
//
//	-healthcheck  open the database, run SELECT 1, and exit 0/1 without
//	              starting the HTTP server (used by Docker HEALTHCHECK).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	httpadapter "github.com/giulianotesta7/tkt/internal/adapters/http"
	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "open the database, run SELECT 1, exit 0/1")
	flag.Parse()

	dbPath := envOr("TKT_DB_PATH", "data/tkt.db")
	listen := envOr("TKT_LISTEN", ":8080")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	if *healthcheck {
		if err := store.Ping(context.Background()); err != nil {
			log.Fatalf("healthcheck: %v", err)
		}
		os.Exit(0)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close db: %v", err)
		}
	}()

	clock := systemClock{}

	viewBuilder := application.NewViewBuilder(store.TicketStore(), store.UserStore(), store.CategoryStore(), store.CommentStore(), store.AuditStore())
	userSvc := application.NewUserService(store.UserStore(), clock)
	catSvc := application.NewCategoryService(store.CategoryStore(), clock)
	commentSvc := application.NewCommentService(store.TicketStore(), store.CommentStore(), clock)
	ticketSvc := application.NewTicketService(store.TicketStore(), store.UserStore(), store.CategoryStore(), store.TicketUnitOfWork(), viewBuilder, clock)
	authSvc := application.NewAuthService(store.UserStore(), store.SessionStore(), clock)
	searchSvc := application.NewSearchService(store.TicketStore(), store.SearchStore())

	renderer := httpadapter.NewRenderer()

	mux := http.NewServeMux()
	httpadapter.RegisterStatic(mux)
	httpadapter.NewAuthHandlers(authSvc, userSvc, renderer).Register(mux)
	httpadapter.NewTicketHandlers(ticketSvc, commentSvc, searchSvc, catSvc, userSvc, renderer).Register(mux)
	httpadapter.NewUserHandlers(userSvc, renderer).Register(mux)
	httpadapter.NewCategoryHandlers(catSvc, renderer).Register(mux)
	// D12: /healthz is exempt from auth — registered on the mux before the
	// session middleware wraps it, and the middleware already exempts the
	// public setup/login routes.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	handler := httpadapter.NewSessionMiddleware(store.SessionStore(), store.UserStore()).Wrap(mux)
	log.Printf("tkt server on %s (db: %s)", listen, dbPath)
	if err := http.ListenAndServe(listen, handler); err != nil {
		log.Fatal(err)
	}
}
