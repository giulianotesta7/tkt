#!/usr/bin/env node
/**
 * Stop the running tkt server started by `server:start:empty` or `server:start:seeded`.
 * Reads .e2e-state.json, kills the PID, and removes the temporary database.
 *
 * Usage: npm run server:stop
 */
import { stopServer } from "../lifecycle-core.js";

stopServer().catch((err) => {
  console.error("stop failed:", err.message);
  process.exit(1);
});
