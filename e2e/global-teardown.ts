// Global teardown — cleans up the prebuilt binary and any stale state.
import { rmSync } from "node:fs";
import { join, resolve } from "node:path";

const PROJECT_ROOT = resolve(import.meta.dirname, "..");

export function teardown(): void {
  // Clean up binary
  try {
    rmSync(join(PROJECT_ROOT, "tkt-server"), { force: true });
  } catch { /* ignore */ }

  // Clean up any leftover Playwright CLI artifacts
  try {
    rmSync(join(PROJECT_ROOT, ".playwright-cli"), { recursive: true, force: true });
  } catch { /* ignore */ }
}

export default teardown;