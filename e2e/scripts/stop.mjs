#!/usr/bin/env node
/**
 * Stop the running tkt server started by `server:start:empty` or `server:start:seeded`.
 * Reads .e2e-state.json, kills the PID, and removes the temporary database.
 *
 * Usage: npm run server:stop
 */
import { readFileSync, rmSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { execSync } from "node:child_process";

const __dirname = resolve(fileURLToPath(import.meta.url), "..");
const E2E_DIR = resolve(__dirname, "..");
const STATE_FILE = join(E2E_DIR, ".e2e-state.json");

async function stop() {
  if (!existsSync(STATE_FILE)) {
    console.log("No state file found — nothing to stop.");
    process.exit(0);
  }

  let state;
  try {
    const raw = readFileSync(STATE_FILE, "utf8");
    state = JSON.parse(raw);
  } catch {
    console.error("Cannot read state file; removing it.");
    rmSync(STATE_FILE, { force: true });
    process.exit(1);
  }

  const { pid, dbDir } = state;

  // Kill the exact PID
  if (pid) {
    try {
      process.kill(pid, "SIGTERM");
    } catch { /* already dead */ }

    // Grace period
    const deadline = Date.now() + 3000;
    while (Date.now() < deadline) {
      try {
        process.kill(pid, 0);
      } catch {
        break; // process is gone
      }
      await new Promise((r) => setTimeout(r, 50));
    }

    // Force kill
    try {
      process.kill(pid, "SIGKILL");
    } catch { /* already dead */ }
  }

  // Remove temp database directory
  if (dbDir) {
    try {
      rmSync(dbDir, { recursive: true, force: true });
      console.log("Removed temp database: " + dbDir);
    } catch (err) {
      console.error("Failed to remove " + dbDir + ": " + err.message);
    }
  }

  // Remove state file
  rmSync(STATE_FILE, { force: true });

  // Clean up Playwright CLI artifacts in e2e/
  try {
    rmSync(join(E2E_DIR, ".playwright-cli"), { recursive: true, force: true });
  } catch { /* ignore */ }

  console.log("Server stopped and cleaned up.");
}

stop().catch((err) => {
  console.error("stop failed:", err.message);
  process.exit(1);
});