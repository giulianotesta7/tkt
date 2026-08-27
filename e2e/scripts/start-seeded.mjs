#!/usr/bin/env node
/**
 * Start an isolated tkt server with a pre-seeded SQLite database
 * (root user, category, desk, published workflow).
 * Prints the URL to stdout and persists state for `npm run server:stop`.
 *
 * Usage: npm run server:start:seeded
 */
import { startServer } from "../lifecycle-core.js";

startServer({ seed: true, mode: "cli" }).catch((err) => {
  console.error("start-seeded failed:", err.message);
  process.exit(1);
});
