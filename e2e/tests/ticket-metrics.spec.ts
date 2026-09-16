/**
 * Ticket metrics (issue #123): the list summary survives the #tickets-screen
 * swap; the dedicated /tickets/metrics page serves the four dashboard cards,
 * View data tables, workload By desk HX swap, HX filter recovery, and a safe
 * return to the list.
 */

import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  assertNoHorizontalOverflow,
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

// One dashboard card located by its h2 heading (all four headings are unique).
function metricsPanel(page: Page, heading: string) {
  return page
    .locator(".ticket-metrics-panel")
    .filter({ has: page.getByRole("heading", { name: heading, level: 2 }) });
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
    await expect(summary.getByRole("heading", { name: "Operational summary" })).toBeVisible();
    await expect(summary.locator(".ticket-metrics-card")).toHaveCount(4);
    await expect(summary.getByRole("link", { name: "View metrics" })).toBeVisible();
    await expect(page.locator("#tickets-screen #ticket-metrics")).toHaveCount(0);

    const probe = "zz-no-such-ticket-metrics";
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByPlaceholder(/search by id or title/i).fill(probe);
        await page.getByRole("button", { name: "Apply", exact: true }).click();
      },
      {
        endpoint: (url) =>
          new URL(url).pathname === "/tickets" && new URL(url).searchParams.get("q") === probe,
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(page.locator("#ticket-list").getByText(/no tickets match/i)).toBeVisible({
      timeout: 10_000,
    });

    await expect(summary).toBeVisible();

    // The summary sits outside #tickets-screen, so the swap must have been
    // followed client-side by a link sync: the "View metrics" href now
    // mirrors the current list query (return starts with /tickets and
    // carries the searched term).
    const metricsHref = await summary
      .getByRole("link", { name: "View metrics" })
      .getAttribute("href");
    expect(metricsHref).toMatch(/^\/tickets\/metrics\?return=%2Ftickets/);
    const returnQuery = new URL(metricsHref ?? "", base()).searchParams.get("return");
    expect(returnQuery).toBeTruthy();
    expect(returnQuery?.startsWith("/tickets")).toBe(true);
    expect(returnQuery).toContain(probe);

    // The summary is server-rendered outside the swapped region, so a reload of
    // the pushed list URL must still show it and still mirror the query.
    await page.reload();
    await expect(summary).toBeVisible();
    const reloadedReturn = new URL(
      (await summary.getByRole("link", { name: "View metrics" }).getAttribute("href")) ?? "",
      base(),
    ).searchParams.get("return");
    expect(reloadedReturn).toContain(probe);

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
    await expect(page.getByRole("heading", { name: "Ticket metrics", level: 1 })).toBeVisible();
    const detail = page.locator("#ticket-metrics-detail-content");
    await expect(detail.getByRole("heading", { name: "Created vs resolved" })).toBeVisible();
    await expect(
      metricsPanel(page, "Created vs resolved").locator("svg.ticket-metrics-chart"),
    ).toBeVisible();
    await expect(
      metricsPanel(page, "Created vs resolved").locator(".ticket-metrics-legend"),
    ).toBeVisible();
    await expect(detail.locator(".ticket-metrics-chart-totals")).toContainText(
      /Created \d+ · Resolved \d+/,
    );
    const disclosure = metricsPanel(page, "Created vs resolved").locator(
      "details.ticket-metrics-data",
    );
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
    await expect(page.locator(".ticket-metrics-error")).toContainText("choose both metrics dates");
    await expect(page.locator("#tickets-screen")).toHaveCount(0);
    await fromInput.fill(fromDefault);
    await toInput.fill(toDefault);
    await applyFilters();
    await expect(page.locator(".ticket-metrics-error")).toHaveCount(0);
    await expect(
      metricsPanel(page, "Created vs resolved").locator("svg.ticket-metrics-chart"),
    ).toBeVisible();
    // The dashboard completes with exactly four visible panels; each opens
    // its native View data disclosure into a captioned table.
    const panels = detail.locator(".ticket-metrics-panel");
    await expect(panels).toHaveCount(4);
    for (const heading of [
      "Created vs resolved",
      "Age of pending tickets",
      "Pending workload",
      "Resolution time distribution",
    ]) {
      const panel = metricsPanel(page, heading);
      await expect(panel).toBeVisible();
      await panel.locator(".ticket-metrics-data summary").click();
      const table = panel.locator(".ticket-metrics-data table");
      await expect(table).toBeVisible();
      await expect(table.locator("caption")).toBeVisible();
      await panel.locator(".ticket-metrics-data summary").click();
    }
    // By desk re-renders the workload card through the same filter form: the
    // swap proves the exact endpoint, fragment target, and URL, and the
    // server-rendered result (checked desk radio, desk caption) persists.
    const detailURL = page.url();
    await assertHtmxSwap(
      page,
      async () => {
        await detail.getByText("By desk", { exact: true }).click();
      },
      {
        endpoint: (url) =>
          new URL(url).pathname === "/tickets/metrics" &&
          new URL(url).searchParams.get("metrics_group") === "desk",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#ticket-metrics-detail-content",
      },
    );
    expect(page.url()).toBe(detailURL);
    const workload = metricsPanel(page, "Pending workload");
    await expect(workload.locator('input[name="metrics_group"][value="desk"]')).toBeChecked();
    await workload.locator(".ticket-metrics-data summary").click();
    await expect(workload.locator(".ticket-metrics-data caption")).toContainText(
      "Pending workload by current desk",
    );
    // Two-column desktop grid down to a one-column mobile stack, never
    // overflowing horizontally.
    const gridColumns = () =>
      page
        .locator(".ticket-metrics-grid")
        .evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length);
    await expect(gridColumns()).resolves.toBe(2);
    await assertNoHorizontalOverflow(page, 1280);
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(panels).toHaveCount(4);
    await assertNoHorizontalOverflow(page, 390);
    await expect(gridColumns()).resolves.toBe(1);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.getByRole("link", { name: "Back to tickets" }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator("#tickets-screen")).toBeVisible();
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});
