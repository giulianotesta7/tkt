/**
 * Shared server lifecycle for E2E tests.
 *
 * Each test file starts its own isolated tkt server with its own temporary
 * SQLite database.  This provides full test isolation: the first-user-setup
 * test runs on an empty DB, while the login + ticket tests start from a
 * pre-seeded DB.  Tests can run individually, in any order, or repeated,
 * without sharing mutable state.
 *
 * Usage:
 *   import { startServer, stopServer } from "./server-lifecycle.js";
 *   import { seed } from "./seed-db.js";
 *
 *   test.describe("My Journey", () => {
 *     test.beforeAll(async () => {
 *       await startServer();       // empty migrated DB (for first-user setup)
 *       // OR
 *       await startServer({ seed: true });  // seeded DB (for login + tickets)
 *     });
 *     test.afterAll(async () => {
 *       await stopServer();
 *     });
 *   });
 */
import { execSync, spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { createServer } from "node:net";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const PROJECT_ROOT = resolve(import.meta.dirname, "..");
const BIN = join(PROJECT_ROOT, "tkt-server");

export interface ServerHandle {
  baseURL: string;
  dbDir: string;
  port: number;
  pid: number;
}

/** Find an available loopback port. */
async function findFreePort(): Promise<number> {
  for (let attempt = 0; attempt < 20; attempt++) {
    const port = 20000 + (parseInt(randomBytes(2).toString("hex"), 16) % 10000);
    try {
      await new Promise<void>((resolve, reject) => {
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

const running: { proc: ChildProcess | null; dbDir: string | null } = {
  proc: null,
  dbDir: null,
};

export let activeServer: ServerHandle | null = null;

/**
 * Start an isolated tkt server with a temporary SQLite database.
 *
 * @param options.seed  When true, seed the database with root user, category,
 *                      desk, and published workflow.  When false (default),
 *                      only run migrations — for the first-user setup test.
 */
export async function startServer(options: { seed?: boolean } = {}): Promise<ServerHandle> {
  if (running.proc) {
    throw new Error("a server is already running; call stopServer() first");
  }

  const bin = BIN;
  const dbDir = mkdtempSync(join(tmpdir(), "tkt-e2e-"));
  const dbPath = join(dbDir, "tkt.db");

  // Run migrations
  execSync(`go run ./e2e/cmd/migrate/main.go --db=${dbPath}`, {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
    env: { ...process.env, GOTOOLCHAIN: "auto" },
  });

  // Optionally seed
  if (options.seed) {
    execSync(`go run ./e2e/cmd/seed/main.go --db=${dbPath}`, {
      cwd: PROJECT_ROOT,
      stdio: "pipe",
      env: { ...process.env, GOTOOLCHAIN: "auto" },
    });
  }

  const port = await findFreePort();
  const baseURL = `http://127.0.0.1:${port}`;

  const proc = spawn(bin, [], {
    cwd: PROJECT_ROOT,
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      TKT_DB_PATH: dbPath,
      TKT_LISTEN: `127.0.0.1:${port}`,
    },
  });

  let stdout = "";
  let stderr = "";
  let killed = false;

  if (proc.stdout) {
    proc.stdout.on("data", (chunk: Buffer) => { stdout += chunk.toString(); });
  }
  if (proc.stderr) {
    proc.stderr.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });
  }

  const cleanup = () => {
    if (killed) return;
    killed = true;
    try { if (proc.exitCode === null) proc.kill("SIGKILL"); } catch { /* ignore */ }
    try { rmSync(dbDir, { recursive: true, force: true }); } catch { /* ignore */ }
  };

  // Wait for health endpoint
  const deadline = Date.now() + 15_000;
  let ready = false;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${baseURL}/healthz`, {
        signal: AbortSignal.timeout(3_000),
      });
      if (res.ok) {
        ready = true;
        break;
      }
    } catch {
      // not ready yet
    }
    await new Promise((r) => setTimeout(r, 200));
  }

  if (!ready) {
    cleanup();
    throw new Error(
      `tkt server did not become ready within 15s\nstdout: ${stdout.slice(0, 500)}\nstderr: ${stderr.slice(0, 500)}`,
    );
  }

  running.proc = proc;
  running.dbDir = dbDir;
  activeServer = { baseURL, dbDir, port, pid: proc.pid! };

  // Set TKT_BASE_URL for this test's page requests
  process.env.TKT_BASE_URL = baseURL;

  return activeServer;
}

/**
 * Stop the running tkt server and clean up its temporary database.
 */
export async function stopServer(): Promise<void> {
  const { proc, dbDir } = running;

  if (!proc) {
    // Nothing running — still clean up any orphaned temp dir
    if (dbDir) {
      try { rmSync(dbDir, { recursive: true, force: true }); } catch { /* ignore */ }
    }
    running.dbDir = null;
    return;
  }

  // Graceful stop
  try {
    proc.kill("SIGTERM");
  } catch { /* ignore */ }

  // Wait up to 3s for the process to exit
  const deadline = Date.now() + 3_000;
  while (Date.now() < deadline && proc.exitCode === null) {
    await new Promise((r) => setTimeout(r, 50));
  }

  // Force kill if still alive
  if (proc.exitCode === null) {
    try { proc.kill("SIGKILL"); } catch { /* ignore */ }
  }

  // Remove temp database directory
  if (dbDir) {
    try { rmSync(dbDir, { recursive: true, force: true }); } catch { /* ignore */ }
  }

  // Clean up Playwright CLI artifacts
  try {
    rmSync(join(PROJECT_ROOT, ".playwright-cli"), { recursive: true, force: true });
  } catch { /* ignore */ }

  running.proc = null;
  running.dbDir = null;
  activeServer = null;

  // Verify no leftover temp dirs
  const leftover = join(tmpdir(), "tkt-e2e-");
  // Only check pattern if the tmpdir isn't the standard one
}