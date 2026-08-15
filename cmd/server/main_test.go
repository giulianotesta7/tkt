package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/domain"
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

// --- S2: -recover-root (operator-selected root recovery) and fail-closed
// startup. These run the real binary against file-backed databases (design
// "Persistence and Recovery"; role-authorization "Operator-Selected Root
// Recovery"). Written before the flag exists: the tests fail until main.go
// parses -recover-root and calls the store's RecoverRoot.

// buildServer compiles the server binary once per test (Go's build cache
// keeps repeat builds cheap).
func buildServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "server")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// runServer runs the binary with TKT_DB_PATH set and returns stdout,
// stderr, and the exit code.
func runServer(bin, dbPath string, args ...string) (stdout, stderr string, code int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "TKT_DB_PATH="+dbPath)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return "", "", -1
		}
	}
	return out.String(), errb.String(), code
}

// seedAmbiguousLegacyDB writes the shape the backfill fails closed on:
// users exist, no root, and the reliable id=1 setup user is absent (the
// migration ran on an empty table, then agent users were created and the
// first user deleted). Returns the surviving user ids.
func seedAmbiguousLegacyDB(t *testing.T, dbPath string) []int64 {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate seed db: %v", err)
	}
	var ids []int64
	for _, name := range []string{"Ana", "Beto", "Caro"} {
		u := &domain.User{Name: name, Email: name + "@example.com", PasswordHash: "hash",
			Role: domain.RoleAgent, Active: true, CreatedAt: time.Now()}
		if err := s.UserStore().Create(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		ids = append(ids, u.ID)
	}
	if err := s.UserStore().Delete(ctx, ids[0]); err != nil {
		t.Fatalf("delete id=1: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	return ids[1:]
}

// verifyRecoveredRoot reopens the db after the child ran and asserts the
// selected user is root and active.
func verifyRecoveredRoot(t *testing.T, dbPath string, wantID int64) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open verify db: %v", err)
	}
	defer s.Close()
	u, err := s.UserStore().GetByID(ctx, wantID)
	if err != nil {
		t.Fatalf("get recovered user: %v", err)
	}
	if u.Role != domain.RoleRoot {
		t.Errorf("recovered role = %q, want %q", u.Role, domain.RoleRoot)
	}
	if !u.Active {
		t.Error("recovered user must be active")
	}
}

func TestRecoverRootFlagPromotesAndExitsZero(t *testing.T) {
	bin := buildServer(t)
	db := filepath.Join(t.TempDir(), "tkt.db")
	remaining := seedAmbiguousLegacyDB(t, db)

	stdout, stderr, code := runServer(bin, db, "-recover-root="+fmt.Sprint(remaining[0]))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	verifyRecoveredRoot(t, db, remaining[0])

	// The promotion is audited in role_changes. The sqlite adapter registers
	// the "sqlite" driver on import, so a direct database/sql handle can read
	// the append-only audit table (no store port exposes role_changes yet).
	audit, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatalf("open audit db: %v", err)
	}
	defer audit.Close()
	var reason string
	var actor sql.NullString
	if err := audit.QueryRow(`SELECT reason, actor_user_id FROM role_changes WHERE user_id = ? ORDER BY id DESC LIMIT 1`, remaining[0]).Scan(&reason, &actor); err != nil {
		t.Fatalf("read role_changes: %v", err)
	}
	if reason != "operator-selected root recovery" {
		t.Errorf("audit reason = %q, want %q", reason, "operator-selected root recovery")
	}
	if actor.Valid {
		t.Errorf("actor_user_id = %v, want NULL", actor.String)
	}
}

func TestRecoverRootFailsWhenRootExists(t *testing.T) {
	bin := buildServer(t)
	db := filepath.Join(t.TempDir(), "tkt.db")
	ctx := context.Background()

	s, err := sqlite.Open(db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	root := &domain.User{Name: "Root", Email: "root@example.com", PasswordHash: "hash",
		Role: domain.RoleRoot, Active: true, CreatedAt: time.Now()}
	agent := &domain.User{Name: "Agent", Email: "agent@example.com", PasswordHash: "hash",
		Role: domain.RoleAgent, Active: true, CreatedAt: time.Now()}
	if err := s.UserStore().Create(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := s.UserStore().Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runServer(bin, db, "-recover-root="+fmt.Sprint(agent.ID))
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero (root already exists)\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "root") {
		t.Errorf("stderr must explain the refusal, got: %s", stderr)
	}
}

func TestRecoverRootFailsForUnknownUser(t *testing.T) {
	bin := buildServer(t)
	db := filepath.Join(t.TempDir(), "tkt.db")
	seedAmbiguousLegacyDB(t, db)

	stdout, stderr, code := runServer(bin, db, "-recover-root=4242")
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero (user does not exist)\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestRecoverRootRejectsNonPositiveID(t *testing.T) {
	bin := buildServer(t)
	db := filepath.Join(t.TempDir(), "tkt.db")

	stdout, stderr, code := runServer(bin, db, "-recover-root=0")
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero (invalid user id)\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

// TestStartupFailsClosedWithoutRecoverRoot proves the fail-closed startup
// contract end to end (task 2.4): on a database where no root can be proven,
// the plain server refuses to serve and names -recover-root as the exit.
func TestStartupFailsClosedWithoutRecoverRoot(t *testing.T) {
	bin := buildServer(t)
	db := filepath.Join(t.TempDir(), "tkt.db")
	seedAmbiguousLegacyDB(t, db)

	stdout, stderr, code := runServer(bin, db)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero (fail closed)\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "recover-root") {
		t.Errorf("stderr must name -recover-root, got: %s", stderr)
	}
}
