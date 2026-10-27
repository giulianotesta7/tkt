// Seed the tkt database: creates root user, category, and published workflow.
// Used by the E2E global setup to prepare the database before tests run.
//
// Usage:
//
//	go run ./e2e/cmd/seed/main.go --db=<path>
//	go run ./e2e/cmd/seed/main.go --db=<path> --pending-fixture="<name>:<email>:<google|microsoft>"
//
// The second form is the opt-in Pending-tab fixture (issue #419, S12): it seeds
// ONLY one account waiting for approval, with the provider identity a real
// company sign-in would have linked, and writes nothing else. The browser
// journeys that assert that tab ask for it by name against their own isolated
// database; the default run above is unchanged and never creates a waiting
// account.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func main() {
	dbPath := flag.String("db", "", "path to the SQLite database")
	pendingFixture := flag.String("pending-fixture", "",
		`seed ONLY one account waiting for approval, as "<name>:<email>:<google|microsoft>" (opt-in Pending-tab fixture)`)
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, `usage: go run ./e2e/cmd/seed/main.go --db=<path> [--pending-fixture="<name>:<email>:<google|microsoft>"]`)
		os.Exit(1)
	}

	store, err := sqlite.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// The opt-in fixture is its own run: it writes one waiting account and
	// returns before the base seed, so a database already seeded by the command
	// above gains exactly that row and the default path stays byte-identical.
	if *pendingFixture != "" {
		fixture, err := seedPendingFixture(context.Background(), store, *pendingFixture, time.Now().UTC())
		if err != nil {
			log.Fatalf("pending fixture: %v", err)
		}
		log.Printf("pending fixture: %s <%s> (id=%d, provider=%s)",
			fixture.User.Name, fixture.User.Email, fixture.User.ID, fixture.Identity.Provider)
		fmt.Println("pending fixture complete")
		return
	}

	clock := fakeClock{}

	userSvc := application.NewUserService(store.UserStore(), clock)
	catSvc := application.NewCategoryService(store.CategoryStore(), clock)
	deskSvc := application.NewDeskService(store.DeskStore(), store.UserStore(), clock)
	workflowSvc := application.NewWorkflowService(store.WorkflowStore())

	// Create root user
	root, err := userSvc.BootstrapRoot(context.Background(), application.CreateUserInput{
		Name:     "Alice Admin",
		Email:    "alice@example.com",
		Password: "SuperSecret42!",
	})
	if err != nil {
		log.Fatalf("create root user: %v", err)
	}
	log.Printf("root user: %s <%s> (id=%d)", root.Name, root.Email, root.ID)

	// Create category
	cat, err := catSvc.Create(context.Background(), "General")
	if err != nil {
		log.Fatalf("create category: %v", err)
	}
	log.Printf("category: %s (id=%d)", cat.Name, cat.ID)

	// Create a published workflow: one manual_task step
	draft := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Handle the ticket"}},
	}
	issues, err := workflowSvc.Publish(context.Background(), *root, cat.ID, draft)
	if err != nil {
		log.Fatalf("publish workflow: %v", err)
	}
	if len(issues) > 0 {
		log.Fatalf("publish issues: %v", issues)
	}
	log.Printf("published workflow for category %d", cat.ID)

	// Create a legacy Desk without a Department. It is the isolated fixture for
	// the virtual Unassigned administration journey.
	desk, err := deskSvc.Create(context.Background(), *root, "General Support")
	if err != nil {
		log.Fatalf("create desk: %v", err)
	}
	log.Printf("desk: %s (id=%d)", desk.Name, desk.ID)

	legacyCategory, err := catSvc.CreateWithDescription(context.Background(), "Legacy Support Category", "Category under the legacy unassigned desk", desk.ID)
	if err != nil {
		log.Fatalf("create legacy category: %v", err)
	}
	log.Printf("legacy category: %s (id=%d)", legacyCategory.Name, legacyCategory.ID)

	// A PUBLISHED category that cannot move a ticket (issue #239). The publish
	// gate refuses an assignment step on a desk with no eligible member, so the
	// desk is staffed first, the workflow published, and the member removed: the
	// only honest way to reach the state, and the one the requester's picker has
	// to state instead of handing over a dead end.
	//
	// The desk lives in a real department on purpose: a desk with no department
	// sits under the virtual Unassigned group, which the picker only reaches when
	// no department exists at all, so the fixture would be unreachable there.
	catalogSvc := application.NewCatalogService(store.CatalogStore(), store.CategoryStore(), clock)
	departments, err := catalogSvc.ListDepartments(context.Background())
	if err != nil {
		log.Fatalf("list departments: %v", err)
	}
	if len(departments) == 0 {
		log.Fatalf("no department to host the unrunnable fixture")
	}
	unstaffedDesk, err := catalogSvc.CreateDeskFor(context.Background(), *root, departments[0].ID, "Unstaffed Desk")
	if err != nil {
		log.Fatalf("create unstaffed desk: %v", err)
	}
	if err := deskSvc.AddMember(context.Background(), *root, unstaffedDesk.ID, root.ID); err != nil {
		log.Fatalf("staff unstaffed desk: %v", err)
	}
	unrunnableCategory, err := catSvc.CreateWithDescription(context.Background(), "Unrunnable Requests", "Published while staffed, then unstaffed", unstaffedDesk.ID)
	if err != nil {
		log.Fatalf("create unrunnable category: %v", err)
	}
	assignDraft := domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: unstaffedDesk.ID, Strategy: domain.StrategyLeastLoaded},
	}}
	assignIssues, err := workflowSvc.Publish(context.Background(), *root, unrunnableCategory.ID, assignDraft)
	if err != nil {
		log.Fatalf("publish unrunnable workflow: %v", err)
	}
	if len(assignIssues) > 0 {
		log.Fatalf("publish unrunnable issues: %v", assignIssues)
	}
	if err := deskSvc.RemoveMember(context.Background(), *root, unstaffedDesk.ID, root.ID); err != nil {
		log.Fatalf("unstaff desk: %v", err)
	}
	log.Printf("unrunnable category: %s (id=%d, desk=%d)", unrunnableCategory.Name, unrunnableCategory.ID, unstaffedDesk.ID)

	fmt.Println("seed complete")
}

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Now().UTC() }

// pendingFixtureMicrosoftTenant is the directory the fixture identity names for
// Microsoft, the same GUID the Go fixtures and the Settings tests use. Nothing
// talks to the provider: the row exists so the Pending tab has a truthful
// provider to state.
const pendingFixtureMicrosoftTenant = "0f8fad5b-d9cb-469f-a165-70867728950e"

// pendingFixture is one waiting account and the identity it was linked with.
type pendingFixture struct {
	User     *domain.User
	Identity *application.CompanyIdentity
}

// parsePendingFixture reads the closed three-part spec: a display name, an
// address, and one of the supported providers. Anything else is refused, so a
// typo fails the command instead of seeding a half-account.
func parsePendingFixture(spec string) (name, email string, provider application.CompanyProvider, err error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return "", "", "", errors.New(`the fixture must read "<name>:<email>:<google|microsoft>"`)
	}
	name, email = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	provider, ok := application.ParseCompanyProvider(strings.TrimSpace(parts[2]))
	if name == "" || email == "" || !ok {
		return "", "", "", errors.New(`the fixture must read "<name>:<email>:<google|microsoft>"`)
	}
	if !strings.Contains(email, "@") {
		return "", "", "", errors.New("the fixture email needs an address, such as wanda@example.com")
	}
	return name, email, provider, nil
}

// pendingFixtureIdentity answers the issuer and subject the fixture stores for a
// provider. The issuer is the real issuer shape the flow verifies and the
// subject is derived from the fixture's own address, so two fixtures never share
// the identity pair the table makes unique.
func pendingFixtureIdentity(provider application.CompanyProvider, email string) (issuer, subject string) {
	switch provider {
	case application.ProviderGoogle:
		return "https://accounts.google.com", "fixture-google-" + strings.ToLower(email)
	default:
		return "https://login.microsoftonline.com/" + pendingFixtureMicrosoftTenant + "/v2.0", "fixture-microsoft-" + strings.ToLower(email)
	}
}

// seedPendingFixture stores ONE account that waits for approval together with the
// provider identity a real company sign-in would have linked (issue #419, S3).
// It goes through the same store port the sign-in flow uses, so the fixture is
// the row production creates: a requester with no password, inactive and
// pending, and exactly one identity. It never announces the account: the notices
// the flow writes belong to the flow, and a fixture must not mimic its side
// effects.
func seedPendingFixture(ctx context.Context, store *sqlite.Store, spec string, at time.Time) (pendingFixture, error) {
	name, email, provider, err := parsePendingFixture(spec)
	if err != nil {
		return pendingFixture{}, err
	}
	issuer, subject := pendingFixtureIdentity(provider, email)
	user := &domain.User{
		Name: name, Email: email, PasswordHash: "", Role: domain.RoleUser,
		Active: false, Pending: true, CreatedAt: at,
	}
	identity := &application.CompanyIdentity{
		Provider: provider, Issuer: issuer, Subject: subject, Email: email,
		CreatedAt: at, LastSignInAt: at,
	}
	if err := store.CompanyIdentityStore().CreateUserWithIdentity(ctx, user, identity, nil); err != nil {
		return pendingFixture{}, fmt.Errorf("create the waiting account %q: %w", email, err)
	}
	return pendingFixture{User: user, Identity: identity}, nil
}
