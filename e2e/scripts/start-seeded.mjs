#!/usr/bin/env node
/**
 * Start an isolated tkt server with a pre-seeded SQLite database
 * (root user, category, desk, published workflow).
 * Prints the URL to stdout and writes state to .e2e-state.json for `stop`.
 *
 * Usage: npm run server:start:seeded
 *
 * Prints: TKT server ready at http://127.0.0.1:PORT
 */
import { execSync, spawn } from "node:child_process";
import { createServer } from "node:net";
import { randomBytes } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = resolve(fileURLToPath(import.meta.url), "..");
const E2E_DIR = resolve(__dirname, "..");
const PROJECT_ROOT = resolve(E2E_DIR, "..");
const STATE_FILE = join(E2E_DIR, ".e2e-state.json");

async function findFreePort() {
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
    } catch { /* port in use */ }
  }
  throw new Error("could not find a free port");
}

async function start() {
  // Build binary
  const bin = join(PROJECT_ROOT, "tkt-server");
  execSync("go build -o " + bin + " ./cmd/server", {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
    env: { ...process.env, GOTOOLCHAIN: "auto" },
  });

  // Create temp dir
  const dbDir = mkdtempSync(join(tmpdir(), "tkt-e2e-"));
  const dbPath = join(dbDir, "tkt.db");

  try {
    // Migrate + seed
    execSync("go run ./e2e/cmd/migrate/main.go --db=" + dbPath, {
      cwd: PROJECT_ROOT,
      stdio: "pipe",
      env: { ...process.env, GOTOOLCHAIN: "auto" },
    });
    execSync("go run ./e2e/cmd/seed/main.go --db=" + dbPath, {
      cwd: PROJECT_ROOT,
      stdio: "pipe",
      env: { ...process.env, GOTOOLCHAIN: "auto" },
    });

    const port = await findFreePort();
    const baseURL = "http://127.0.0.1:" + port;

    const proc = spawn(bin, [], {
      cwd: PROJECT_ROOT,
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        TKT_DB_PATH: dbPath,
        TKT_LISTEN: "127.0.0.1:" + port,
      },
    });

    let stdout = "", stderr = "";
    proc.stdout.on("data", (chunk) => { stdout += chunk; });
    proc.stderr.on("data", (chunk) => { stderr += chunk; });
    proc.on("error", (err) => {
      throw new Error("spawn failed: " + err.message);
    });

    // Wait for health
    const deadline = Date.now() + 15000;
    let ready = false;
    while (Date.now() < deadline) {
      try {
        const res = await fetch(baseURL + "/healthz", {
          signal: AbortSignal.timeout(3000),
        });
        if (res.ok) { ready = true; break; }
      } catch { /* not ready */ }
      await new Promise((r) => setTimeout(r, 200));
    }

    if (!ready) {
      try { proc.kill("SIGKILL"); } catch {}
      try { rmSync(dbDir, { recursive: true, force: true }); } catch {}
      throw new Error(
        "tkt server not ready within 15s\nstdout: " + stdout.slice(0, 500) +
        "\nstderr: " + stderr.slice(0, 500),
      );
    }

    // Persist state
    const state = { dbDir, port, pid: proc.pid, baseURL };
    writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));

    console.log("TKT server ready at " + baseURL);
  } catch (err) {
    try { rmSync(dbDir, { recursive: true, force: true }); } catch {}
    try { rmSync(STATE_FILE, { force: true }); } catch {}
    throw err;
  }
}

start().catch((err) => {
  console.error("start-seeded failed:", err.message);
  process.exit(1);
});