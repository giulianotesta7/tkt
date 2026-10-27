/**
 * Pending-tab E2E support (issue #419, S3/S12).
 *
 * Two things live here, both owned by the Pending journeys in `users.spec.ts`:
 *
 *  1. The opt-in waiting-account fixture. The default E2E seed never creates an
 *     account waiting for approval — only a real company sign-in does, and this
 *     suite has no identity provider — so a journey that needs such a row asks
 *     for one explicitly. The seed command's own `--pending-fixture` mode writes
 *     exactly that row (a requester, inactive and pending, linked to the provider
 *     identity a company sign-in would store) into the SAME isolated database the
 *     running server uses, through the real store port. No route and no row is
 *     faked, and the fixture is outside the approval behaviour under test.
 *
 *  2. The console provenance for the one intentional refusal. The Pending tab's
 *     empty-selection batch is a real `POST` refused with 422, and Chromium logs
 *     every refused resource as a console error whose TEXT does not name the URL.
 *     The shared observability helper keeps only that text, so it cannot tell the
 *     expected refusal from an unrelated error. This module records the console
 *     location too, matches the expected entry against the exact response URL and
 *     status, and returns every OTHER error text so the canonical gate keeps
 *     failing on anything unexpected. It never clears console errors wholesale.
 */

import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { expect, type ConsoleMessage, type Page, type Response } from "@playwright/test";
import { PROJECT_ROOT } from "../../lifecycle-core.js";
import { activeServer } from "../../server-lifecycle.js";

export type PendingProvider = "google" | "microsoft";

export interface PendingFixture {
  /** Display name the row carries; also the journey's entity anchor. */
  name: string;
  /** Address unique inside the describe's database. */
  email: string;
  /** Provider whose identity the account is linked to. */
  provider: PendingProvider;
}

/**
 * Create ONE account waiting for approval in the running test server's database.
 *
 * The per-describe temporary database is the cleanup: it is removed with the
 * server, so a journey never has to undo the row it approved, and two journeys
 * that need a waiting account name their own fixture instead of sharing a mutated
 * one. The command mirrors the lifecycle's own seed invocation (`go run`, project
 * root, `GOTOOLCHAIN=auto` so the toolchain go.mod names is used as-is).
 */
export function seedPendingFixture(fixture: PendingFixture): void {
  const server = activeServer;
  if (!server) {
    throw new Error("seedPendingFixture needs a running isolated server: call startServer first");
  }
  // The lifecycle creates the database at <dbDir>/tkt.db and points the server at
  // that path, so the fixture writes the database the server is serving.
  const dbPath = join(server.dbDir, "tkt.db");
  execFileSync(
    "go",
    [
      "run",
      "./e2e/cmd/seed/main.go",
      "--db=" + dbPath,
      "--pending-fixture=" + `${fixture.name}:${fixture.email}:${fixture.provider}`,
    ],
    { cwd: PROJECT_ROOT, stdio: "pipe", env: { ...process.env, GOTOOLCHAIN: "auto" } },
  );
}

/** One console message with the location Chromium attached to it. */
export interface ConsoleRecord {
  type: string;
  text: string;
  /** The resource URL Chromium names for a resource failure; "" when it names none. */
  url: string;
}

/** The single console error a refused resource is expected to produce. */
export interface ExpectedResourceError {
  /** HTTP status the refused request answered. */
  status: number;
  /** Chromium's reason phrase for that status, e.g. "Unprocessable Entity". */
  statusText: string;
  /** Exact request URL the console entry must report. */
  url: string;
}

/** The exact sentence Chromium logs for a refused resource. */
export function resourceErrorText(status: number, statusText: string): string {
  return `Failed to load resource: the server responded with a status of ${status} (${statusText})`;
}

/**
 * Record console messages with their location, which the shared observability
 * helper deliberately does not keep: the resource-failure sentence carries no
 * URL, so text alone cannot prove which response it reports.
 */
export function collectConsoleWithLocation(page: Page): ConsoleRecord[] {
  const records: ConsoleRecord[] = [];
  page.on("console", (message: ConsoleMessage) => {
    records.push({
      type: message.type(),
      text: message.text(),
      url: message.location()?.url ?? "",
    });
  });
  return records;
}

/**
 * Validate the expected refusal and answer every console error that is NOT it.
 *
 * Exactly one console error must exist, its text must be the exact sentence for
 * the expected status, and its location must name the same URL as the response
 * the journey asserted. That single comparison fails on a second entry, another
 * URL, another status, or a message with no location, so this cannot hide an
 * unrelated error: the returned list is empty by construction and is what the
 * canonical gate reads, which re-asserts it.
 */
export function unexpectedConsoleErrors(
  records: ConsoleRecord[],
  expected: ExpectedResourceError,
): string[] {
  const errors = records.filter((record) => record.type === "error");
  const text = resourceErrorText(expected.status, expected.statusText);
  const describe = (record: ConsoleRecord) => `${record.text} @ ${record.url || "<no location>"}`;
  expect(errors.map(describe), "console errors with their resource location").toEqual([
    `${text} @ ${expected.url}`,
  ]);
  return errors.filter((record) => describe(record) !== `${text} @ ${expected.url}`).map(describe);
}

/**
 * The one refused `POST /users/pending/approve` this journey made, proven from
 * the response itself: method, exact URL and status. The console entry above then
 * has a response to belong to, and the URL the console named is the same one.
 */
export function expectRefusalResponse(response: Response, baseURL: string, status: number): void {
  expect(response.request().method(), "the refused request method").toBe("POST");
  expect(response.url(), "the refused response URL").toBe(baseURL + "/users/pending/approve");
  expect(response.status(), "the refused response status").toBe(status);
}
