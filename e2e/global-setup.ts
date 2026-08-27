import { buildServer } from "./lifecycle-core.js";

export async function setup(): Promise<void> {
  // Build the binary once — each test file starts its own server
  // from the prebuilt binary for full isolation.
  buildServer();
  console.log("tkt-server binary built");
}

export default setup;
