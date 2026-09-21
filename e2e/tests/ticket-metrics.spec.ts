/**
 * Ticket metrics (issue #123): the list summary survives the #tickets-screen
 * swap; the dedicated /tickets/metrics page serves the five dashboard cards
 * (including SLA attainment, issue #211), View data tables, workload By desk
 * and attainment grouping HX swaps, HX filter recovery, and a safe return to
 * the list.
 */

import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  assertNoHorizontalOverflow,
  collectObservability,
  expectNoConsoleOrPageErrors,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { loginAsSeeded, setSLAEnabled } from "./helpers/auth.js";
import { createTicketViaUi } from "./helpers/navigation.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

// One dashboard card located by its h2 heading (every heading is unique).
function metricsPanel(page: Page, heading: string) {
  return page
    .locator(".ticket-metrics-panel")
    .filter({ has: page.getByRole("heading", { name: heading, level: 2 }) });
}

// The dashboard panels in render order. The count assertion derives from this
// list, so a future panel changes one place instead of leaving a silent gap.
const metricsPanelHeadings = [
  "Created vs resolved",
  "Age of pending tickets",
  "Pending workload",
  "Resolution time distribution",
  "SLA attainment",
];

// Panels that always render their native View data disclosure. SLA attainment
// renders one only when the period cohort carries a frozen commitment, so its
// disclosure is covered by the dedicated attainment journeys below.
const metricsDisclosureHeadings = metricsPanelHeadings.filter(
  (heading) => heading !== "SLA attainment",
);

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
    // The dashboard completes with exactly one visible panel per heading; the
    // count derives from the heading list. Every panel is visible, and the
    // panels that always disclose open their native View data caption table.
    const panels = detail.locator(".ticket-metrics-panel");
    await expect(panels).toHaveCount(metricsPanelHeadings.length);
    for (const heading of metricsPanelHeadings) {
      await expect(metricsPanel(page, heading)).toBeVisible();
    }
    for (const heading of metricsDisclosureHeadings) {
      const panel = metricsPanel(page, heading);
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
    await expect(panels).toHaveCount(metricsPanelHeadings.length);
    await assertNoHorizontalOverflow(page, 390);
    await expect(gridColumns()).resolves.toBe(1);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.getByRole("link", { name: "Back to tickets" }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator("#tickets-screen")).toBeVisible();
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});

/**
 * SLA attainment panel (issue #211, PR 5).
 *
 * The panel aggregates the frozen commitments of tickets CREATED in the
 * selected period, grouped by total, priority or category. Each journey
 * arranges its own committed cohort and disables the instance-wide switch in
 * afterEach, because the database is shared with the sibling metrics journeys.
 */
test.describe("SLA attainment panel (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });
  test.afterEach(async ({ page }) => {
    // Restore the instance-wide switch whatever this test left behind, so the
    // next journey (here or in another describe on the same database) sees the
    // state it expects.
    await page.context().clearCookies();
    await loginAsSeeded(page);
    await setSLAEnabled(page, false);
  });

  test("renders the committed cohort with numeric milestone counts and a visible rate denominator", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    await createTicketViaUi(page, {
      title: "SLA attainment " + Date.now().toString(36).slice(2, 8),
      description: "sla attainment probe",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + "/tickets/metrics");
    await expect(page.locator("#ticket-metrics-detail-content")).toBeVisible();
    const panel = metricsPanel(page, "SLA attainment");
    await expect(panel).toBeVisible();
    await expect(panel.locator(".ticket-metrics-card-scope")).toHaveText(
      "Tickets created in the selected period that carry a frozen commitment",
    );
    await expect(panel.getByRole("heading", { name: "First response", level: 3 })).toBeVisible();
    await expect(panel.getByRole("heading", { name: "Resolution", level: 3 })).toBeVisible();

    // Both milestones render real numbers, and the rate denominator is the
    // decided count (Met + Breached), so the check cannot pass on a label alone.
    const counts = panel.locator(".ticket-metrics-attainment-counts");
    const rates = panel.locator(".ticket-metrics-attainment-rate");
    await expect(counts).toHaveCount(2);
    await expect(rates).toHaveCount(2);
    for (let index = 0; index < 2; index += 1) {
      const countsText = (await counts.nth(index).textContent()) ?? "";
      const countsMatch = countsText.match(/^Met (\d+) · Breached (\d+) · Open (\d+)$/);
      expect(countsMatch, `unparseable counts line: ${countsText}`).not.toBeNull();
      const met = Number(countsMatch![1]);
      const breached = Number(countsMatch![2]);
      const open = Number(countsMatch![3]);
      // The freshly created high-priority ticket is still On Track, so both
      // of its milestones are open: the numbers are tied to real cohort data.
      expect(open, `milestone ${index} has no open cohort`).toBeGreaterThanOrEqual(1);

      const rateText = (await rates.nth(index).textContent()) ?? "";
      const rateMatch = rateText.match(/^Rate (\d+)% \((\d+) decided\)$/);
      expect(rateMatch, `unparseable rate line: ${rateText}`).not.toBeNull();
      const decided = Number(rateMatch![2]);
      expect(decided).toBe(met + breached);
      const expectedPercent = met + breached === 0 ? 0 : Math.round((met / (met + breached)) * 100);
      expect(Number(rateMatch![1])).toBe(expectedPercent);
    }

    // The View data disclosure carries exactly one total group.
    await panel.locator(".ticket-metrics-data summary").click();
    const table = panel.locator(".ticket-metrics-data table");
    await expect(table).toBeVisible();
    await expect(table.locator("caption")).toHaveText("SLA attainment by Total");
    const rows = table.locator("tbody tr.ticket-metrics-attainment-row");
    await expect(rows).toHaveCount(1);
    await expect(rows.first().locator("td").first()).toHaveText("Total");

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("the grouping selector re-renders the attainment table by priority and category", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    await createTicketViaUi(page, {
      title: "SLA attainment grouping " + Date.now().toString(36).slice(2, 8),
      description: "sla attainment grouping probe",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + "/tickets/metrics");
    const initialPanel = metricsPanel(page, "SLA attainment");
    await expect(initialPanel).toBeVisible();
    await initialPanel.locator(".ticket-metrics-data summary").click();
    await expect(initialPanel.locator(".ticket-metrics-data table")).toBeVisible();

    // By priority always emits the four canonical priorities in rank order,
    // even for an empty group.
    await assertHtmxSwap(
      page,
      async () => {
        await initialPanel.getByText("By priority", { exact: true }).click();
      },
      {
        endpoint: (url) =>
          new URL(url).pathname === "/tickets/metrics" &&
          new URL(url).searchParams.get("metrics_attainment_group") === "priority",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#ticket-metrics-detail-content",
      },
    );
    const priorityPanel = metricsPanel(page, "SLA attainment");
    await expect(
      priorityPanel.locator('input[name="metrics_attainment_group"][value="priority"]'),
    ).toBeChecked();
    await priorityPanel.locator(".ticket-metrics-data summary").click();
    const priorityTable = priorityPanel.locator(".ticket-metrics-data table");
    await expect(priorityTable).toBeVisible();
    await expect(priorityTable.locator("caption")).toHaveText("SLA attainment by Priority");
    await expect(
      priorityTable.locator("tbody tr.ticket-metrics-attainment-row td:first-child"),
    ).toHaveText(["Critical", "High", "Medium", "Low"]);

    // By category emits only the categories present in the cohort, including
    // the one the journey created its ticket in.
    await assertHtmxSwap(
      page,
      async () => {
        await priorityPanel.getByText("By category", { exact: true }).click();
      },
      {
        endpoint: (url) =>
          new URL(url).pathname === "/tickets/metrics" &&
          new URL(url).searchParams.get("metrics_attainment_group") === "category",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#ticket-metrics-detail-content",
      },
    );
    const categoryPanel = metricsPanel(page, "SLA attainment");
    await expect(
      categoryPanel.locator('input[name="metrics_attainment_group"][value="category"]'),
    ).toBeChecked();
    await categoryPanel.locator(".ticket-metrics-data summary").click();
    const categoryTable = categoryPanel.locator(".ticket-metrics-data table");
    await expect(categoryTable).toBeVisible();
    await expect(categoryTable.locator("caption")).toHaveText("SLA attainment by Category");
    const categoryLabels = await categoryTable
      .locator("tbody tr.ticket-metrics-attainment-row td:first-child")
      .allTextContents();
    expect(categoryLabels).toContain("General");

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("states the reason and renders no counts when the period has no commitment", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // A period before any ticket exists in this database leaves the cohort
    // empty, so the panel explains the absence instead of showing zero rates.
    // SLA stays at its default OFF; afterEach restores the switch either way.
    await page.goto(base() + "/tickets/metrics?metrics_start=2020-01-06&metrics_end=2020-01-13");
    const panel = metricsPanel(page, "SLA attainment");
    await expect(panel).toBeVisible();
    await expect(panel.locator(".ticket-metrics-empty")).toHaveText(
      "No tickets created in the selected period carry a frozen commitment.",
    );
    await expect(panel.locator(".ticket-metrics-attainment")).toHaveCount(0);
    await expect(panel.locator(".ticket-metrics-attainment-counts")).toHaveCount(0);
    await expect(panel.locator(".ticket-metrics-attainment-rate")).toHaveCount(0);

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});
