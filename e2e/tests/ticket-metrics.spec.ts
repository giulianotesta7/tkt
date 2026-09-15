/**
 * Ticket metrics summary (issue #123, metrics slice): the fixed-current-UTC-
 * week summary on GET /tickets is visible to a root/admin and lives OUTSIDE
 * #tickets-screen — the assertHtmxSwap-proven list search swap replaces the
 * screen and the summary survives it. The detail route is a later slice.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  collectObservability,
  expectNoConsoleOrPageErrors,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

test.describe("Ticket metrics summary", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("summary is visible to root and survives the #tickets-screen HTMX swap", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);

    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const summary = page.locator("#ticket-metrics");
    await expect(summary).toBeVisible();
    await expect(
      summary.getByRole("heading", { name: "Operational summary" }),
    ).toBeVisible();
    await expect(summary.locator(".ticket-metrics-card")).toHaveCount(4);
    await expect(
      summary.getByRole("link", { name: "View metrics" }),
    ).toBeVisible();
    await expect(page.locator("#tickets-screen #ticket-metrics")).toHaveCount(
      0,
    );

    const probe = "zz-no-such-ticket-metrics";
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByPlaceholder(/search by id or title/i).fill(probe);
        await page.getByRole("button", { name: "Apply", exact: true }).click();
      },
      {
        endpoint: (url) =>
          new URL(url).pathname === "/tickets" &&
          new URL(url).searchParams.get("q") === probe,
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(
      page.locator("#ticket-list").getByText(/no tickets match/i),
    ).toBeVisible({ timeout: 10_000 });

    await expect(summary).toBeVisible();

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});
