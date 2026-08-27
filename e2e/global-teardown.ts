// Global teardown — cleans up the prebuilt binary and recovers orphaned servers
// from .e2e-state.json if a worker crashed before calling stopServer().
import { readFileSync, rmSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";

const PROJECT_ROOT = resolve(import.meta.dirname, "..");
const E2E_DIR = join(PROJECT_ROOT, "e2e");
const STATE_FILE = join(E2E_DIR, ".e2e-state.json");
const BIN = join(PROJECT_ROOT, "tkt-server");

export async function teardown(): Promise<void> {
  // Recover orphaned server from state file
  if (existsSync(STATE_FILE)) {
    try {
      const raw = readFileSync(STATE_FILE, "utf8");
      const state = JSON.parse(raw);

      // Kill orphaned PID
      if (state.pid) {
        try {
          process.kill(state.pid, "SIGTERM");
        } catch { /* already dead */ }

        // Busy-wait grace period
        const deadline = Date.now() + 2_000;
        while (Date.now() < deadline) {
          try {
            process.kill(state.pid, 0);
          } catch {
            break;
          }
          await new Promise((r) => setTimeout(r, 50));
        }

        // Force kill
        try {
          process.kill(state.pid, "SIGKILL");
        } catch { /* already dead */ }
      }

      // Remove orphaned temp database directory
      if (state.dbDir) {
        try {
          rmSync(state.dbDir, { recursive: true, force: true });
        } catch { /* ignore */ }
      }
    } catch { /* state corrupt — remove it */ }
    rmSync(STATE_FILE, { force: true });
  }

  // Clean up binary
  try {
    rmSync(BIN, { force: true });
  } catch { /* ignore */ }

  // Clean up any orphaned Playwright CLI artifacts
  try {
    rmSync(join(E2E_DIR, ".playwright-cli"), { recursive: true, force: true });
  } catch { /* ignore */ }
}

export default teardown;