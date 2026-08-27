// Global teardown — cleans up the prebuilt binary and recovers orphaned
// servers from .e2e-state.json if a worker crashed before calling
// stopServer() in afterAll.
import { stopServer, readState, removeState, E2E_DIR, BIN } from "./lifecycle-core.js";
import { rmSync } from "node:fs";
import { join } from "node:path";

export async function teardown(): Promise<void> {
  // Recover orphaned server from state file.  stopServer() reads the
  // state, kills the PID, removes the temp directory, and removes the
  // state file — all scoped to the exact resources recorded in state.
  const state = readState();
  if (state) {
    // If the state has a PID, kill it; if it has a dbDir, remove it.
    if (state.pid) {
      try {
        process.kill(state.pid, "SIGTERM");
      } catch { /* already dead */ }
      const deadline = Date.now() + 2_000;
      while (Date.now() < deadline) {
        try {
          process.kill(state.pid, 0);
        } catch { break; }
        await new Promise((r) => setTimeout(r, 50));
      }
      try {
        process.kill(state.pid, "SIGKILL");
      } catch { /* already dead */ }
    }
    if (state.dbDir) {
      try {
        rmSync(state.dbDir, { recursive: true, force: true });
      } catch { /* ignore */ }
    }
    removeState();
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
