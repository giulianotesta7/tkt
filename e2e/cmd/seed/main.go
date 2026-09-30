// Seed the tkt database: creates root user, category, and published workflow.
// Used by the E2E global setup to prepare the database before tests run.
//
// Usage: go run ./e2e/cmd/seed/main.go --db=<path>
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func main() {
	dbPath := flag.String("db", "", "path to the SQLite database")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./e2e/cmd/seed/main.go --db=<path>")
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
