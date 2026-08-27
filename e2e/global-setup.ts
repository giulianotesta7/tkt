import { execSync, spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const PROJECT_ROOT = resolve(import.meta.dirname, "..");
const STATE_FILE = join(PROJECT_ROOT, ".e2e-state.json");

type State = {
  dbDir: string;
  port: number;
  pid: number;
  baseURL: string;
};

let proc: ChildProcess | null = null;
let dbDir: string | null = null;

/**
 * Build the tkt binary and start it with an isolated temp SQLite database.
 * Writes state so the teardown hook can find and clean it up.
 */
export async function setup(): Promise<void> {
  const bin = join(PROJECT_ROOT, "tkt-server");

  // Build the binary
  execSync(`go build -o ${bin} ./cmd/server`, {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
  });

  // Create temp dir for the database
  dbDir = mkdtempSync(join(tmpdir(), "tkt-e2e-"));
  const dbPath = join(dbDir, "tkt.db");

  // Seed the database with root user, category, and published workflow
  execSync(`go run ./e2e/seed.go --db=${dbPath}`, {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
  });

  // Pick a random high port
  const port =
    20000 + (parseInt(randomBytes(2).toString("hex"), 16) % 10000);
  const baseURL = `http://127.0.0.1:${port}`;

  // Start the server
  proc = spawn(bin, [], {
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

  if (proc.stdout) {
    proc.stdout.on("data", (chunk: Buffer) => {
      stdout += chunk.toString();
    });
  }
  if (proc.stderr) {
    proc.stderr.on("data", (chunk: Buffer) => {
      stderr += chunk.toString();
    });
  }

  // Wait for health endpoint
  const deadline = Date.now() + 15_000;
  let ready = false;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${baseURL}/healthz`);
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
    // Kill on failure
    if (proc.exitCode === null) proc.kill("SIGKILL");
    throw new Error(
      `tkt server did not become ready within 15s\nstdout: ${stdout.slice(0, 500)}\nstderr: ${stderr.slice(0, 500)}`,
    );
  }

  // Persist state for the teardown hook
  const state: State = { dbDir, port, pid: proc.pid!, baseURL };
  writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));

  // Export for Playwright via environment
  process.env.TKT_BASE_URL = baseURL;

  // Signal that we're ready — Playwright reads TKT_BASE_URL from env
  console.log(`tkt E2E server ready at ${baseURL}`);
}

export async function teardown(): Promise<void> {
  // Try reading state file first (handle both setup-run and teardown-only invocations)
  try {
    const { readFileSync } = await import("node:fs");
    const raw = readFileSync(STATE_FILE, "utf8");
    const state: State = JSON.parse(raw);

    if (proc && proc.exitCode === null) {
      proc.kill("SIGTERM");
      await new Promise((r) => setTimeout(r, 1000));
      if (proc.exitCode === null) proc.kill("SIGKILL");
    }

    if (state.dbDir) {
      rmSync(state.dbDir, { recursive: true, force: true });
    }

    try {
      rmSync(STATE_FILE, { force: true });
    } catch {
      // best-effort
    }

    const bin = join(PROJECT_ROOT, "tkt-server");
    try {
      rmSync(bin, { force: true });
    } catch {
      // best-effort
    }
  } catch {
    // State file missing — nothing to clean up
  }
}

// Run setup when this module is loaded as Playwright globalSetup
export default setup;