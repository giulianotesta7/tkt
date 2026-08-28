/**
 * Thin test wrapper around lifecycle-core.js.
 *
 * Each test.describe block starts its own isolated tkt server with its own
 * temporary SQLite database.  Provides full test isolation: first-user-setup
 * runs on an empty DB, login + ticket tests start from a pre-seeded DB.
 *
 * State is persisted to e2e/.e2e-state.json so global-teardown can recover
 * orphaned servers if a worker crashes before afterAll.
 *
 * Usage:
 *   import { startServer, stopServer } from "../server-lifecycle.js";
 *
 *   test.describe("My Journey", () => {
 *     test.beforeAll(async () => { await startServer({ seed: false }); });
 *     test.afterAll(async () => { await stopServer(); });
 *   });
 */
import { startServer as coreStart, stopServer as coreStop } from "./lifecycle-core.js";

export interface ServerHandle {
  baseURL: string;
  dbDir: string;
  port: number;
  pid: number;
}

export let activeServer: ServerHandle | null = null;

/**
 * Start an isolated tkt server with a temporary SQLite database.
 *
 * @param options.seed  When true, seed the database.  When false (default),
 *                      only run migrations — for the first-user setup test.
 */
export async function startServer(options: { seed?: boolean } = {}): Promise<ServerHandle> {
  if (activeServer) {
    throw new Error("a server is already running; call stopServer() first");
  }

  const handle = await coreStart({ seed: options.seed ?? false, mode: "test" });
  activeServer = { baseURL: handle.baseURL, dbDir: handle.dbDir, port: handle.port, pid: handle.pid };
  return activeServer;
}

/**
 * Stop the running tkt server and clean up its temporary database.
 */
export async function stopServer(): Promise<void> {
  await coreStop();
  activeServer = null;
}
