// Seed the tkt database: creates root user, category, and published workflow.
// Used by the E2E global setup to prepare the database before tests run.
//
// Usage: go run ./e2e/seed.go --db=<path>
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
		fmt.Fprintln(os.Stderr, "usage: go run ./e2e/seed.go --db=<path>")
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

	// Create a desk
	desk, err := deskSvc.Create(context.Background(), *root, "General Support")
	if err != nil {
		log.Fatalf("create desk: %v", err)
	}
	log.Printf("desk: %s (id=%d)", desk.Name, desk.ID)

	fmt.Println("seed complete")
}

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Now().UTC() }