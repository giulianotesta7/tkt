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
 * creation AND immediately after spawn (before healthcheck), so
 * global-teardown can recover from any interruption point — including
 * during migration, spawn, or readiness.
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
 * Write state atomically.  Called immediately after temp dir creation,
 * after each bootstrap phase, and immediately after spawn.
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

/** Check whether a PID is alive (exists and we can signal it). */
function pidAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

/**
 * Kill a PID: SIGTERM + grace window + SIGKILL.
 * Returns true if the process was killed, false if it was already dead.
 */
export async function killPID(pid) {
  if (!pidAlive(pid)) return false;
  try {
    process.kill(pid, "SIGTERM");
  } catch { /* race */ }
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline) {
    if (!pidAlive(pid)) return true;
    await new Promise((r) => setTimeout(r, 50));
  }
  try {
    process.kill(pid, "SIGKILL");
  } catch { /* race */ }
  return true;
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

// ── Spawn + persist PID + healthcheck ─────────────────────────────────
/**
 * Spawn the server, persist PID immediately, then wait for readiness.
 *
 * Returns { proc, baseURL, stderr } on success.
 * On failure (spawn error, exit before ready, or readiness timeout):
 *   - kills the local ChildProcess
 *   - removes the temp directory
 *   - removes the state file
 *   - rejects with a descriptive error
 */
export async function spawnServer(port, dbPath, state) {
  const baseURL = `http://127.0.0.1:${port}`;
  const { dbDir, runId } = state;

  const proc = spawn(BIN, [], {
    cwd: PROJECT_ROOT,
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      TKT_DB_PATH: dbPath,
      TKT_LISTEN: `127.0.0.1:${port}`,
    },
  });

  return new Promise((resolve, reject) => {
    let resolved = false;
    let stderr = "";

    if (proc.stderr) {
      proc.stderr.on("data", (chunk) => {
        stderr += chunk.toString();
      });
    }

    // Persist PID immediately — before healthcheck — so recovery is possible
    writeState({ runId, dbDir, dbPath, bootstrapStatus: "starting", pid: proc.pid, port: port, baseURL });

    // Shared cleanup helper: kill the local proc, remove dir, remove state.
    // Does NOT re-read the state file — uses the local references.
    const cleanup = () => {
      try { if (proc.exitCode === null) proc.kill("SIGKILL"); } catch { /* ignore */ }
      try { rmSync(dbDir, { recursive: true, force: true }); } catch { /* ignore */ }
      removeState();
    };

    proc.on("error", (err) => {
      if (!resolved) {
        resolved = true;
        cleanup();
        reject(new Error(
          "spawn failed: " + err.message + (stderr ? "\nstderr:\n" + stderr.slice(0, 500) : ""),
        ));
      }
    });

    proc.on("exit", (code, signal) => {
      if (!resolved) {
        resolved = true;
        cleanup();
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
        cleanup();
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
    await killPID(state.pid);
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

  // ── Guard: don't overwrite a running server ────────────────────────
  const existing = readState();
  if (existing) {
    if (existing.pid && pidAlive(existing.pid)) {
      throw new Error(
        "A server is already running (PID " + existing.pid + "). " +
        "Run `npm run server:stop` first, or kill the process manually.",
      );
    }
    // Stale state: clean up its exact resources, then continue
    if (existing.dbDir) {
      try { rmSync(existing.dbDir, { recursive: true, force: true }); } catch { /* ignore */ }
    }
    removeState();
  }

  // Build (idempotent — fast if already built)
  buildServer();

  // Create temp dir — state persisted immediately for recovery
  let dbDir;
  let dbPath;
  let runId;
  try {
    const tmp = createTempDir();
    dbDir = tmp.dbDir;
    dbPath = tmp.dbPath;
    runId = tmp.runId;

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

    // Spawn: persists PID immediately (before healthcheck), then waits for readiness.
    // On failure, spawnServer handles its own cleanup (kill proc, remove dir, remove state).
    const { proc } = await spawnServer(port, dbPath, { dbDir, runId });

    // Persist final state with ready status
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
    // If the error came from outside spawnServer (migrate, seed, port),
    // we need to clean up here.  If it came from spawnServer, cleanup
    // already happened inside that function — but it's safe to repeat.
    try { rmSync(dbDir, { recursive: true, force: true }); } catch { /* ignore */ }
    removeState();
    throw err;
  }
}