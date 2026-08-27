// Global teardown — cleans up the prebuilt binary and recovers orphaned
// servers from .e2e-state.json if a worker crashed before calling
// stopServer() in afterAll.
import { stopServer, E2E_DIR, BIN } from "./lifecycle-core.js";
import { rmSync } from "node:fs";
import { join } from "node:path";

export async function teardown(): Promise<void> {
  // Recover orphaned server using the shared stopServer implementation.
  // stopServer reads the state, kills the PID, removes the temp directory,
  // and removes the state file — all scoped to the exact resources recorded.
  await stopServer();

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