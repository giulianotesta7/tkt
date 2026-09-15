/**
 * Ticket metrics (issue #123): the list summary survives the #tickets-screen
 * swap; the dedicated /tickets/metrics page serves the line card, View data
 * table, HX filter recovery, and a safe return to the list.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  collectObservability,
  expectNoConsoleOrPageErrors,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { loginAsSeeded } from "./helpers/auth.js";
import { createTicketViaUi } from "./helpers/navigation.js";

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

  test("admin opens the metrics page, filters over HX with error recovery, and returns safely", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await createTicketViaUi(page, { title: "metrics probe ticket" });
    await page.getByRole("link", { name: "View metrics" }).click();
    await expect(page).toHaveURL(/\/tickets\/metrics\?return=/);
    await expect(
      page.getByRole("heading", { name: "Ticket metrics", level: 1 }),
    ).toBeVisible();
    const detail = page.locator("#ticket-metrics-detail-content");
    await expect(
      detail.getByRole("heading", { name: "Created vs resolved" }),
    ).toBeVisible();
    await expect(detail.locator("svg.ticket-metrics-chart")).toBeVisible();
    await expect(detail.locator(".ticket-metrics-legend")).toBeVisible();
    await expect(detail.locator(".ticket-metrics-chart-totals")).toContainText(
      /Created \d+ · Resolved \d+/,
    );
    const disclosure = detail.locator("details.ticket-metrics-data");
    await disclosure.locator("summary").click();
    const table = disclosure.getByRole("table");
    await expect(table).toBeVisible();
    await expect(table.getByRole("columnheader")).toHaveText([
      "Monday week",
      "Created",
      "Resolved",
    ]);
    // Capture the server-resolved default period before mutating inputs;
    // recovery reuses it so the journey stays clock-stable.
    const fromInput = detail.getByLabel("From", { exact: true });
    const toInput = detail.getByLabel("To", { exact: true });
    const fromDefault = await fromInput.inputValue();
    const toDefault = await toInput.inputValue();
    // Both Apply submits assert the HX contract; strictQuery also pins the
    // incomplete-period query of the first (alert) swap, whose To is cleared
    // because the server pre-fills the default period.
    const applyFilters = (strictQuery = false) =>
      assertHtmxSwap(
        page,
        async () => {
          await detail.getByRole("button", { name: "Apply" }).click();
        },
        {
          endpoint: (url) => {
            const u = new URL(url);
            return (
              u.pathname === "/tickets/metrics" &&
              (!strictQuery ||
                (u.searchParams.get("metrics_start") === "2026-01-05" &&
                  u.searchParams.get("metrics_end") === ""))
            );
          },
          method: "GET",
          expectedStatus: 200,
          hxTarget: "#ticket-metrics-detail-content",
        },
      );
    await fromInput.fill("2026-01-05");
    await toInput.fill("");
    await applyFilters(true);
    await expect(page.locator(".ticket-metrics-error")).toContainText(
      "choose both metrics dates",
    );
    await expect(page.locator("#tickets-screen")).toHaveCount(0);
    await fromInput.fill(fromDefault);
    await toInput.fill(toDefault);
    await applyFilters();
    await expect(page.locator(".ticket-metrics-error")).toHaveCount(0);
    await expect(detail.locator("svg.ticket-metrics-chart")).toBeVisible();
    await page.getByRole("link", { name: "Back to tickets" }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator("#tickets-screen")).toBeVisible();
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});
