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
	"path/filepath"
	"syscall"
	"time"

	httpadapter "github.com/giulianotesta7/tkt/internal/adapters/http"
	"github.com/giulianotesta7/tkt/internal/adapters/mail"
	"github.com/giulianotesta7/tkt/internal/adapters/msgtext"
	"github.com/giulianotesta7/tkt/internal/adapters/secretbox"
	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// emailSendInterval is how often the background sender looks for pending
// notification emails.
const emailSendInterval = 15 * time.Second

// holdExpiryInterval is how often the automatic hold expiry looks for held
// tickets whose reminder or deadline is due (issue #353). Five minutes: the
// deadlines are working-day instants, so a pass that runs a few minutes late
// closes a ticket a few minutes late, while a pass with the feature off costs
// one setting read and stops there.
const holdExpiryInterval = 5 * time.Minute

// alertInterval is how often the operational alerts (issue #408) look for
// tickets that crossed an SLA threshold or stopped moving. The conditions are
// measured in hours and working days, so a five-minute cadence is already far
// finer than the rules it serves, and it matches the hold expiry next to it.
const alertInterval = 5 * time.Minute

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

	viewBuilder := application.NewViewBuilder(store.TicketStore(), store.UserStore(), store.CategoryStore(), store.CommentStore(), store.AuditStore(), store.DeskStore(), store.WorkflowResponseStore())
	userSvc := application.NewUserService(store.UserStore(), clock).WithPasswordSettings(store.PasswordSettingsStore()).WithPasswordChangeStore(store.PasswordChangeStore())
	catSvc := application.NewCategoryService(store.CategoryStore(), clock)
	catalogSvc := application.NewCatalogService(store.CatalogStore(), store.CategoryStore(), clock)
	deskSvc := application.NewDeskService(store.DeskStore(), store.UserStore(), clock)
	slaSvc := application.NewSLAService(store.SLAStore(), store.SettingsStore(), clock)
	// The requester's reply that ends a hold shifts the resolution SLA (#352).
	commentSvc := application.NewCommentService(store.TicketStore(), store.CommentStore(), store.UserStore(), store.CommentUnitOfWork(), clock).WithSLA(slaSvc)
	ticketSvc := application.NewTicketServiceWithWorkflowCreate(store.TicketStore(), store.UserStore(), store.CategoryStore(), store.TicketUnitOfWork(), viewBuilder, clock, store.WorkflowVersionStore(), application.NewWorkflowRunner(clock), store.WorkflowUnitOfWork(), slaSvc)
	authSvc := application.NewAuthService(store.UserStore(), store.SessionStore(), clock)
	searchSvc := application.NewSearchService(store.TicketStore(), store.SearchStore())
	settingsSvc := application.NewSettingsService(store.SettingsStore())
	organizationSvc := application.NewOrganizationService(store.OrganizationStore())
	signinSvc := application.NewSigninService(store.SigninStore())
	passwordSettingsSvc := application.NewPasswordSettingsService(store.PasswordSettingsStore())
	preferencesSvc := application.NewPreferencesService(store.PreferencesStore(), store.EmailPreferences(), store.SidebarPreferences(), store.ThemePreferences())
	workflowSvc := application.NewWorkflowService(store.WorkflowStore())
	metricsSvc := application.NewTicketMetricsService(store.TicketMetricsStore(), clock)
	// Issue #362: the SMTP password is sealed with a key file that lives in the
	// data volume next to the database (S9), and delivery goes out through the
	// company's own SMTP server. The key is not an environment variable, so the
	// container contract is unchanged.
	mailer := mail.NewSMTPMailer()
	emailSettingsSvc := application.NewEmailSettingsService(store.EmailSettingsStore(), secretbox.New(filepath.Dir(dbPath)), mailer, store.SettingsStore(), msgtext.Renderer{})
	emailSender := application.NewNotificationEmailSender(store.EmailOutbox(), emailSettingsSvc, store.UserStore(), store.TicketStore(), store.EmailPreferences(), mailer, msgtext.Renderer{}, clock)
	// Issue #414: self-service password recovery. The send leaves the request
	// goroutine through AsyncPasswordResetDispatch, so the neutral answer never
	// waits for the mail server and a known address cannot be told apart from an
	// unknown one by how long the form took. The send detaches from the request
	// context, bounds itself (PasswordResetSendTimeout) and logs its failure with
	// the token and the address redacted.
	resetSvc := application.NewPasswordResetService(store.UserStore(), store.PasswordResetStore(), store.PasswordSettingsStore(), emailSettingsSvc, mailer, msgtext.Renderer{}, application.AsyncPasswordResetDispatch, clock)
	// Issue #353: the automatic hold expiry closes a ticket whose requester
	// never replied, after warning them one working day earlier. It shares the
	// SLA service with the ticket use cases, so an automatic close shifts the
	// paused resolution commitment exactly as a manual resume does.
	holdExpiry := application.NewHoldExpiryService(store.HoldExpiryStore(), store.TicketUnitOfWork(), store.UserStore(), slaSvc, store.SettingsStore(), clock)
	// Issue #408: the operational alerts tell the assigned agent that a ticket
	// reached an SLA threshold or stopped moving. They are ordinary
	// notifications, so the bell and the opt-in email sender above carry them
	// without a second delivery path.
	alerts := application.NewAlertService(store.AlertStore(), store.AlertUnitOfWork(), store.UserStore(), store.SettingsStore(), clock)
	// Attachment downloads (issue #366): read-only, authorised per request by
	// the actor's ticket scope and the visibility of what the file belongs to.
	attachmentSvc := application.NewAttachmentService(store.TicketStore(), store.AttachmentStore())
	// Issue #415: what is new since a user's last visit. The visit store
	// answers the per-ticket history mark, records the visit (and reads that
	// ticket's notifications) and derives the per-page "changed for me" set.
	visitSvc := application.NewTicketVisitService(store.TicketVisitStore(), clock)

	// Issue #335: the interface language is an instance-wide setting, so the
	// renderer resolves it per request and selects the parsed template tree
	// (and the lang it declares) from it.
	renderer := httpadapter.NewRendererWithLanguage(store.SettingsStore())

	mux := http.NewServeMux()
	httpadapter.RegisterStatic(mux, store.SettingsStore())
	httpadapter.NewBrandHandlers(store.OrganizationStore()).WithSignin(store.SigninStore()).Register(mux)
	httpadapter.NewAuthHandlers(authSvc, userSvc, renderer).WithSignin(signinSvc).WithPasswordRecovery(resetSvc, passwordSettingsSvc, emailSettingsSvc).Register(mux)
	httpadapter.NewTicketHandlers(ticketSvc, commentSvc, searchSvc, catSvc, userSvc, store.DeskStore(), workflowSvc, application.NewWorkflowRunner(clock), store.WorkflowRunStore(), store.WorkflowUnitOfWork(), renderer, catalogSvc).WithMetrics(metricsSvc).WithSLA(slaSvc).WithPreferences(preferencesSvc).WithClock(clock).WithVisits(visitSvc).WithAttachments(store.AttachmentStore(), attachmentSvc).Register(mux)
	httpadapter.NewUserHandlers(userSvc, renderer).Register(mux)
	httpadapter.NewCategoryHandlersWithWorkflows(catSvc, workflowSvc, renderer, catalogSvc).WithSettings(store.SettingsStore()).Register(mux)
	httpadapter.NewCategoryWorkflowHandlers(catSvc, workflowSvc, deskSvc, renderer).Register(mux)
	httpadapter.NewDeskHandlers(deskSvc, renderer).Register(mux)
	httpadapter.NewSettingsHandlers(settingsSvc, slaSvc, store.SLAStore(), store.SettingsStore(), renderer).WithEmail(emailSettingsSvc).WithOrganization(organizationSvc).WithSignin(signinSvc).WithPasswordSettings(passwordSettingsSvc).Register(mux)
	httpadapter.NewCategorySLAHandlers(catSvc, slaSvc, store.SLAStore(), renderer).Register(mux)
	httpadapter.NewPreferencesHandlers(preferencesSvc, renderer).WithPasswordChange(userSvc, passwordSettingsSvc).Register(mux)
	httpadapter.NewNotificationHandlers(store.NotificationStore(), store.TicketStore(), store.UserStore(), renderer, clock).Register(mux)
	// D12: /healthz is exempt from auth — registered on the mux before the
	// session middleware wraps it, and the middleware already exempts the
	// public setup/login routes.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	// Issue #213: one body cap for every route. The limit middleware sits
	// inside the session middleware so the existing Origin/auth short-circuits
	// (403/303) still answer before any body byte is buffered; every request
	// that reaches the mux is bounded, /healthz and /static included.
	sessionHandler := httpadapter.NewSessionMiddleware(store.SessionStore(), store.UserStore(), store.SettingsStore()).WithSidebarPreferences(store.SidebarPreferences()).WithThemePreferences(store.ThemePreferences()).WithOrganization(store.OrganizationStore()).Wrap(httpadapter.NewBodyLimitMiddleware().Wrap(mux))

	srv := &http.Server{
		Addr:              listen,
		Handler:           sessionHandler,
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

	// Issue #362: notification emails go out in the background, outside the
	// transactions that write them, until shutdown. Settings are read on
	// every pass, so a saved change applies without a restart.
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		emailSender.Run(ctx, emailSendInterval)
	}()
	// Stop the sender and wait for it before the deferred database close, on
	// every exit path (not only a signal).
	defer func() {
		stop()
		<-senderDone
	}()

	// Issue #353: the hold expiry runs the same background shape — one pass per
	// interval, the window read on every pass — and is likewise stopped and
	// awaited before the database closes, so no pass outlives the connection.
	holdExpiryDone := make(chan struct{})
	go func() {
		defer close(holdExpiryDone)
		holdExpiry.Run(ctx, holdExpiryInterval)
	}()
	defer func() {
		stop()
		<-holdExpiryDone
	}()

	// Issue #408: the alert pass follows the same background shape and is
	// likewise stopped and awaited before the database closes.
	alertsDone := make(chan struct{})
	go func() {
		defer close(alertsDone)
		alerts.Run(ctx, alertInterval)
	}()
	defer func() {
		stop()
		<-alertsDone
	}()

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
