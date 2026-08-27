#!/usr/bin/env node
/**
 * Start an isolated tkt server with an empty (migrated-only) SQLite database.
 * Prints the URL to stdout and persists state for `npm run server:stop`.
 *
 * Usage: npm run server:start:empty
 */
import { startServer } from "../lifecycle-core.js";

startServer({ seed: false, mode: "cli" }).catch((err) => {
  console.error("start-empty failed:", err.message);
  process.exit(1);
});
