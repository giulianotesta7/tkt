// Migrate the tkt database: runs migrations only, no seed data.
// Used by the E2E first-user setup test and the general global setup
// (each journey seeds what it needs).
//
// Usage: go run ./e2e/cmd/migrate/main.go --db=<path>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
)

func main() {
	dbPath := flag.String("db", "", "path to the SQLite database")
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./e2e/cmd/migrate/main.go --db=<path>")
		os.Exit(1)
	}

	store, err := sqlite.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("migrate complete")
}
