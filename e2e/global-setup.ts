import { execSync } from "node:child_process";
import { resolve } from "node:path";

const PROJECT_ROOT = resolve(import.meta.dirname, "..");

export async function setup(): Promise<void> {
  // Build the binary once — each test file starts its own server
  // from the prebuilt binary for full isolation.
  const bin = resolve(PROJECT_ROOT, "tkt-server");
  execSync(`go build -o ${bin} ./cmd/server`, {
    cwd: PROJECT_ROOT,
    stdio: "pipe",
    env: { ...process.env, GOTOOLCHAIN: "auto" },
  });
  console.log("tkt-server binary built");
}

export default setup;