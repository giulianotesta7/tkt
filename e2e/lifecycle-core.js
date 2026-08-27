/**
 * Shared lifecycle core for the Playwright E2E test suite.
 *
 * Single implementation shared by:
 *   - server-lifecycle.ts  (test lifecycle)
 *   - scripts/start-empty.mjs  (CLI)
 *   - scripts/start-seeded.mjs (CLI)
 *   - scripts/stop.mjs         (CLI)
 *   - global-setup.ts          (Playwright global setup)
 *   - global-teardown.ts       (Playwright global teardown)
 *
 * State is persisted to e2e/.e2e-state.json immediately after temp dir
 * creation, so global-teardown can recover from any interruption point.
 */

import { execFileSync, spawn } from "node:child_process";
import { createServer } from "node:net";
import { randomBytes } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync, renameSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
export const E2E_DIR = resolve(dirname(__filename));
export const PROJECT_ROOT = resolve(E2E_DIR, "..");
export const BIN = join(PROJECT_ROOT, "tkt-server");

// ── State file ─────────────────────────────────────────────────────────
export const STATE_FILE = join(E2E_DIR, ".e2e-state.json");
const STATE_TMP = STATE_FILE + ".tmp";

let _runIdCounter = 0;
function nextRunId() {
  return `${Date.now()}-${++_runIdCounter}`;
}

/**
 * Write state atomically.  Called immediately after temp dir creation and
 * updated after each bootstrap phase so any interrupted execution leaves
 * a recoverable record.
 */
export function writeState(state) {
  writeFileSync(STATE_TMP, JSON.stringify(state, null, 2) + "\n", "utf8");
  renameSync(STATE_TMP, STATE_FILE);
}

export function readState() {
  if (!existsSync(STATE_FILE)) return null;
  try {
    return JSON.parse(readFileSync(STATE_FILE, "utf8"));
  } catch {
    return null;
  }
}

export function removeState() {
  try { rmSync(STATE_FILE, { force: true }); } catch {}
  try { rmSync(STATE_TMP, { force: true }); } catch {}
}

// ── Port allocation ────────────────────────────────────────────────────
export async function findFreePort() {
  for (let attempt = 0; attempt < 20; attempt++) {
    const port = 20000 + (parseInt(randomBytes(2).toString("hex"), 16) % 10000);
    try {
      await new Promise((resolve, reject) => {
        const server = createServer();
        server.unref();
        server.on("error", reject);
        server.listen(port, "127.0.0.1", () => {
          server.close(() => resolve());
        });
      });
      return port;
    } catch {
      // port in use, try another
    }
  }
  throw new Error("could not find a free port after 20 attempts");
}

// ── Build ──────────────────────────────────────────────────────────────
export function buildServer() {
  execFileSync("go", ["build", "-o", BIN, "./cmd/server"], {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
    env: { ...process.env, GOTOOLCHAIN: "auto" },
  });
}

// ── Temp directory (persist state immediately) ─────────────────────────
export function createTempDir() {
  const dbDir = mkdtempSync(join(tmpdir(), "tkt-e2e-"));
  const dbPath = join(dbDir, "tkt.db");
  const runId = nextRunId();
  const state = { runId, dbDir, dbPath, bootstrapStatus: "created", pid: null, port: null, baseURL: null };
  writeState(state);
  return { dbDir, dbPath, runId };
}

// ── Migrate & Seed (execFileSync — separated args) ────────────────────
export function runMigrate(dbPath) {
  execFileSync("go", ["run", "./e2e/cmd/migrate/main.go", "--db=" + dbPath], {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
    env: { ...process.env, GOTOOLCHAIN: "auto" },
  });
}

export function runSeed(dbPath) {
  execFileSync("go", ["run", "./e2e/cmd/seed/main.go", "--db=" + dbPath], {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
    env: { ...process.env, GOTOOLCHAIN: "auto" },
  });
}

// ── Spawn as Promise — captures stderr for diagnostics ─────────────────
export function spawnServer(port, dbPath) {
  const baseURL = `http://127.0.0.1:${port}`;
  return new Promise((resolve, reject) => {
    const proc = spawn(BIN, [], {
      cwd: PROJECT_ROOT,
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        TKT_DB_PATH: dbPath,
        TKT_LISTEN: `127.0.0.1:${port}`,
      },
    });

    let resolved = false;
    let stderr = "";

    if (proc.stderr) {
      proc.stderr.on("data", (chunk) => {
        stderr += chunk.toString();
      });
    }

    proc.on("error", (err) => {
      if (!resolved) {
        resolved = true;
        reject(new Error("spawn failed: " + err.message + (stderr ? "\nstderr:\n" + stderr.slice(0, 500) : "")));
      }
    });

    proc.on("exit", (code, signal) => {
      if (!resolved) {
        resolved = true;
        reject(new Error(
          "process exited with code " + code + " signal " + signal + " before ready" +
          (stderr ? "\nstderr:\n" + stderr.slice(0, 500) : ""),
        ));
      }
    });

    // Healthcheck loop
    const deadline = Date.now() + 15000;
    (async () => {
      while (Date.now() < deadline) {
        try {
          const res = await fetch(baseURL + "/healthz", {
            signal: AbortSignal.timeout(3000),
          });
          if (res.ok) {
            if (!resolved) {
              resolved = true;
              resolve({ proc, baseURL, stderr });
            }
            return;
          }
        } catch {
          // not ready yet
        }
        await new Promise((r) => setTimeout(r, 200));
      }
      if (!resolved) {
        resolved = true;
        reject(new Error("server not ready within 15s" + (stderr ? "\nstderr:\n" + stderr.slice(0, 500) : "")));
      }
    })();
  });
}

// ── Stop + cleanup (idempotent, scoped to exact state) ─────────────────
export async function stopServer() {
  const state = readState();
  if (!state) {
    console.log("No state file found — nothing to stop.");
    return;
  }

  // Kill the exact PID we own
  if (state.pid) {
    try {
      process.kill(state.pid, "SIGTERM");
    } catch {
      // already dead
    }

    // Busy-wait grace period
    const deadline = Date.now() + 3000;
    while (Date.now() < deadline) {
      try {
        process.kill(state.pid, 0);
      } catch {
        break; // process is gone
      }
      await new Promise((r) => setTimeout(r, 50));
    }

    // Force kill
    try {
      process.kill(state.pid, "SIGKILL");
    } catch {
      // already dead
    }
  }

  // Remove the exact temp directory only
  if (state.dbDir) {
    try {
      rmSync(state.dbDir, { recursive: true, force: true });
      console.log("Removed temp database: " + state.dbDir);
    } catch (err) {
      console.error("Failed to remove " + state.dbDir + ": " + err.message);
    }
  }

  // Remove state file
  removeState();

  // Clean up Playwright CLI artifacts
  try {
    rmSync(join(E2E_DIR, ".playwright-cli"), { recursive: true, force: true });
  } catch {
    // ignore
  }

  console.log("Server stopped and cleaned up.");
}

// ── Full lifecycle (shared by all callers) ─────────────────────────────
export async function startServer(options = {}) {
  const { seed = false, mode = "test" } = options;
  const isCLI = mode === "cli";

  // Build (idempotent — fast if already built)
  buildServer();

  // Create temp dir — state persisted immediately for recovery
  const { dbDir, dbPath, runId } = createTempDir();

  try {
    // Migrate
    runMigrate(dbPath);
    writeState({ runId, dbDir, dbPath, bootstrapStatus: "migrated", pid: null, port: null, baseURL: null });

    // Optionally seed
    if (seed) {
      runSeed(dbPath);
      writeState({ runId, dbDir, dbPath, bootstrapStatus: "seeded", pid: null, port: null, baseURL: null });
    }

    // Allocate port
    const port = await findFreePort();
    const baseURL = `http://127.0.0.1:${port}`;

    // Spawn + wait for readiness
    const { proc } = await spawnServer(port, dbPath);

    // Persist final state with PID and connection info
    writeState({ runId, dbDir, dbPath, bootstrapStatus: "ready", pid: proc.pid, port, baseURL });

    if (isCLI) {
      // Detach so the parent can exit while the server continues.
      proc.unref();
      // Destroy pipe streams so the event loop is not kept alive.
      if (proc.stdout) proc.stdout.destroy();
      if (proc.stderr) proc.stderr.destroy();
      console.log("TKT server ready at " + baseURL);
      return;
    }

    // For tests: return the handle
    return { dbDir, port, pid: proc.pid, baseURL, proc };
  } catch (err) {
    // Cleanup on any failure: kill process if spawned, remove dir, remove state
    const currentState = readState();
    if (currentState && currentState.pid) {
      try {
        process.kill(currentState.pid, "SIGKILL");
      } catch {
        // already dead
      }
    }
    try {
      rmSync(dbDir, { recursive: true, force: true });
    } catch {
      // ignore
    }
    removeState();
    throw err;
  }
}
