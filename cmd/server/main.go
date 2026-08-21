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
//	-healthcheck   open the database, run SELECT 1, and exit 0/1 without
//	               starting the HTTP server (used by Docker HEALTHCHECK).
//	-recover-root  one-shot operator-selected root recovery for ambiguous
//	               legacy databases: activate and promote user <id> to root,
//	               audit the recovery, and exit (fails closed when a root
//	               already exists or the user is unknown).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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
	recoverRoot := flag.Int64("recover-root", 0, "one-shot operator-selected root recovery: activate and promote user <id> to root, audit, exit (fails closed when a root already exists or the user is unknown)")
	flag.Parse()

	// flag.Visit distinguishes "flag absent" from "explicitly -recover-root=0":
	// the latter is an operator error, not a no-op.
	recoverRootSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "recover-root" {
			recoverRootSet = true
		}
	})
	if recoverRootSet && *recoverRoot <= 0 {
		log.Fatalf("recover-root: invalid user id %d (must be a positive user id)", *recoverRoot)
	}

	dbPath := envOr("TKT_DB_PATH", "data/tkt.db")
	listen := envOr("TKT_LISTEN", ":8080")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		// The fail-closed legacy backfill (users exist without a provable
		// root) is the exact situation -recover-root exists to resolve:
		// tolerate that one sentinel error ONLY when the flag is set and
		// proceed to recovery. Without the flag, or for any other migration
		// failure, startup fails closed and never serves.
		if !recoverRootSet || !errors.Is(err, sqlite.ErrRecoverRootRequired) {
			log.Fatalf("migrate: %v", err)
		}
	}

	if recoverRootSet {
		u, err := store.UserStore().RecoverRoot(context.Background(), *recoverRoot)
		if err != nil {
			log.Fatalf("recover root: %v", err)
		}
		log.Printf("recovered root: %s <%s> (id %d)", u.Name, u.Email, u.ID)
		if err := store.Close(); err != nil {
			log.Printf("close db: %v", err)
		}
		os.Exit(0)
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

	viewBuilder := application.NewViewBuilder(store.TicketStore(), store.UserStore(), store.CategoryStore(), store.CommentStore(), store.AuditStore(), store.WorkflowResponseStore())
	userSvc := application.NewUserService(store.UserStore(), clock)
	catSvc := application.NewCategoryService(store.CategoryStore(), clock)
	deskSvc := application.NewDeskService(store.DeskStore(), store.UserStore(), clock)
	commentSvc := application.NewCommentService(store.TicketStore(), store.CommentStore(), clock)
	ticketSvc := application.NewTicketServiceWithWorkflowCreate(store.TicketStore(), store.UserStore(), store.CategoryStore(), store.TicketUnitOfWork(), viewBuilder, clock, store.WorkflowVersionStore(), application.NewWorkflowRunner(clock), store.WorkflowUnitOfWork())
	authSvc := application.NewAuthService(store.UserStore(), store.SessionStore(), clock)
	searchSvc := application.NewSearchService(store.TicketStore(), store.SearchStore())
	settingsSvc := application.NewSettingsService(store.SettingsStore())
	workflowSvc := application.NewWorkflowService(store.WorkflowStore())

	renderer := httpadapter.NewRenderer()

	mux := http.NewServeMux()
	httpadapter.RegisterStatic(mux)
	httpadapter.NewAuthHandlers(authSvc, userSvc, renderer).Register(mux)
	httpadapter.NewTicketHandlers(ticketSvc, commentSvc, searchSvc, catSvc, userSvc, renderer).Register(mux)
	httpadapter.NewUserHandlers(userSvc, renderer).Register(mux)
	httpadapter.NewCategoryHandlersWithWorkflows(catSvc, workflowSvc, renderer).Register(mux)
	httpadapter.NewCategoryWorkflowHandlers(workflowSvc, deskSvc, renderer).Register(mux)
	httpadapter.NewDeskHandlers(deskSvc, renderer).Register(mux)
	httpadapter.NewSettingsHandlers(settingsSvc, renderer).Register(mux)
	// D12: /healthz is exempt from auth — registered on the mux before the
	// session middleware wraps it, and the middleware already exempts the
	// public setup/login routes.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	handler := httpadapter.NewSessionMiddleware(store.SessionStore(), store.UserStore(), store.SettingsStore()).Wrap(mux)

	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("tkt server on %s (db: %s)", listen, dbPath)

	// Graceful shutdown: SIGINT/SIGTERM drain in-flight requests (with a
	// bounded window) and run the deferred database close before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		log.Println("shutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}
