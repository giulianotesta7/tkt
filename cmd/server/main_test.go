package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestHealthcheckExec proves the -healthcheck flag opens the database and
// exits 0 on a healthy DB, 1 on an unreachable one (task 6.1 acceptance:
// "go build ./... clean; -healthcheck exits 0 on healthy DB, 1 on failure").
func TestHealthcheckExec(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	t.Run("healthy db exits 0", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "tkt.db")
		cmd := exec.Command(bin, "-healthcheck")
		cmd.Env = append(os.Environ(), "TKT_DB_PATH="+db)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("-healthcheck on healthy db: %v\n%s", err, out)
		}
	})

	t.Run("unreachable db exits 1", func(t *testing.T) {
		cmd := exec.Command(bin, "-healthcheck")
		cmd.Env = append(os.Environ(), "TKT_DB_PATH="+filepath.Join(t.TempDir(), "no", "such", "dir", "tkt.db"))
		if err := cmd.Run(); err == nil {
			t.Fatal("-healthcheck on unreachable db must exit non-zero")
		}
	})
}
