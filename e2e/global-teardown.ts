// Global teardown for Playwright E2E: kill the isolated tkt server and
// remove the temporary database.  Reads state from the file written by
// global-setup.ts, or falls back to process-level cleanup.

import { rmSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = resolve(fileURLToPath(import.meta.url), "..");
const STATE_FILE = join(__dirname, "..", ".e2e-state.json");

export async function teardown(): Promise<void> {
  let state: { dbDir?: string; pid?: number } = {};

  // Read persisted state
  try {
    const raw = await import("node:fs").then((fs) =>
      fs.readFileSync(STATE_FILE, "utf8"),
    );
    state = JSON.parse(raw);
  } catch {
    // state file missing — fall back to process-level cleanup
  }

  // Kill the server process
  if (state.pid) {
    try {
      process.kill(state.pid, "SIGTERM");
      // Small grace window
      await new Promise((r) => setTimeout(r, 500));
      process.kill(state.pid, "SIGKILL");
    } catch {
      // already dead or no permission
    }
  }

  // Remove the temp database directory
  if (state.dbDir) {
    try {
      rmSync(state.dbDir, { recursive: true, force: true });
    } catch {
      // best-effort
    }
  }

  // Clean up state file and binary
  try {
    rmSync(STATE_FILE, { force: true });
  } catch {
    /* ignore */
  }
  try {
    rmSync(join(__dirname, "..", "tkt-server"), { force: true });
  } catch {
    /* ignore */
  }
}

export default teardown;