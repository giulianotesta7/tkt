/**
 * Ticket metrics (issue #123, dashboard redesign issue #393): the dedicated
 * /tickets/metrics page serves the five KPI cards, every section's Chart/Table
 * switch, the server-rendered charts and the same values in the table view,
 * workload By desk and attainment grouping HX swaps, HX filter recovery with
 * Clear, the empty states, the no-script table fallback, and a safe return to
 * the list.
 */

import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  assertNoHorizontalOverflow,
  collectObservability,
  expectHairlineBorder,
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

// Every panel carries the same Chart/Table switch, named for the values it
// shows: the group's accessible name is the section's "View data" affordance.
function viewSwitch(page: Page, heading: string) {
  return metricsPanel(page, heading).getByRole("group", { name: "View data" });
}

function chartPanel(page: Page, heading: string) {
  return metricsPanel(page, heading).locator('[data-metrics-panel="chart"]');
}

function tablePanel(page: Page, heading: string) {
  return metricsPanel(page, heading).locator('[data-metrics-panel="table"]');
}

test.describe("Ticket metrics dashboard", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the queue carries no summary strip and the dashboard leads with five KPI cards", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await expect(page).toHaveURL(/\/tickets/);

    // Issue #382: the weekly strip left the queue; the page is tabs, filters
    // and the table, and the metrics asset is not loaded here.
    await expect(page.locator("#ticket-metrics")).toHaveCount(0);
    await expect(page.locator(".queue-tabs")).toBeVisible();
    expect(
      await page.evaluate(() =>
        [...document.styleSheets].some((s) => (s.href ?? "").includes("ticket_metrics.css")),
      ),
    ).toBe(false);

    await createTicketViaUi(page, { title: "kpi probe ticket" });
    await page.locator('a.sidebar-link[href="/tickets/metrics"]').click();
    await expect(page).toHaveURL(/\/tickets\/metrics$/);
    const kpis = page.locator(".ticket-metrics-kpis");
    await expect(kpis).toBeVisible();
    // Five headline figures, always present: an absent figure is prose, never
    // a hidden card (issue #393, S3).
    await expect(kpis.locator(".ticket-metrics-card")).toHaveCount(5);
    await expect(kpis.locator(".ticket-metrics-card-label")).toHaveText([
      "Pending",
      "Created",
      "Resolved",
      "Mean resolution",
      "SLA · first response",
    ]);
    const details = kpis.locator(".ticket-metrics-card-detail");
    await expect(details.nth(0)).toHaveText(/^\d+ unassigned · \d+% of pending$/);
    await expect(details.nth(1)).toHaveText(/^\d+ weeks? in the period$/);
    await expect(details.nth(2)).toHaveText(/^net \+\d+$/);
    // No resolution has been observed yet in this fresh database, so the mean
    // card states the absence and the SLA card names the missing commitment.
    await expect(kpis.locator(".ticket-metrics-card-value-prose").first()).toBeVisible();
    const kpiBox = await kpis.boundingBox();
    const gridBox = await page.locator(".ticket-metrics-grid").boundingBox();
    expect(kpiBox && gridBox && kpiBox.y < gridBox.y, "the KPI row sits above the charts").toBe(
      true,
    );

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("admin filters over HX with error recovery, clears the range, and returns safely", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await createTicketViaUi(page, { title: "metrics probe ticket" });
    await page.locator('a.sidebar-link[href="/tickets/metrics"]').click();
    await expect(page).toHaveURL(/\/tickets\/metrics$/);
    await expect(page.getByRole("heading", { name: "Ticket metrics", level: 1 })).toBeVisible();
    const detail = page.locator("#ticket-metrics-detail-content");
    await expect(detail.getByRole("heading", { name: "Created vs resolved" })).toBeVisible();
    // The metrics filter Apply is a bare neutral control: pin its hairline
    // before the first filter swap.
    await expectHairlineBorder(
      detail.getByRole("button", { name: "Apply", exact: true }),
      "metrics filter Apply",
    );
    await expect(
      chartPanel(page, "Created vs resolved").locator("svg.ticket-metrics-chart"),
    ).toBeVisible();
    await expect(
      metricsPanel(page, "Created vs resolved").locator(".ticket-metrics-legend"),
    ).toBeVisible();
    await expect(detail.locator(".ticket-metrics-chart-totals")).toContainText(
      /Created \d+ · Resolved \d+/,
    );
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
    // Issue #270: the error re-render keeps the range the operator submitted
    // (the parseable start) instead of resetting the form to empty.
    await expect(fromInput).toHaveValue("2026-01-05");
    await expect(toInput).toHaveValue("");
    await fromInput.fill(fromDefault);
    await toInput.fill(toDefault);
    await applyFilters();
    await expect(page.locator(".ticket-metrics-error")).toHaveCount(0);
    await expect(
      chartPanel(page, "Created vs resolved").locator("svg.ticket-metrics-chart"),
    ).toBeVisible();
    // Clear is a bare /tickets/metrics link: following it restores the default
    // period instead of re-submitting the current filter.
    await detail.getByRole("link", { name: "Clear" }).click();
    await expect(page).toHaveURL(/\/tickets\/metrics\?return=%2Ftickets$/);
    await expect(page.locator(".ticket-metrics-error")).toHaveCount(0);
    await expect(detail.getByLabel("From", { exact: true })).toHaveValue(fromDefault);
    // The dashboard completes with exactly one visible panel per heading; the
    // count derives from the heading list.
    const panels = detail.locator(".ticket-metrics-panel");
    await expect(panels).toHaveCount(metricsPanelHeadings.length);
    for (const heading of metricsPanelHeadings) {
      await expect(metricsPanel(page, heading)).toBeVisible();
      await expect(viewSwitch(page, heading)).toBeVisible();
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
    await viewSwitch(page, "Pending workload").getByRole("button", { name: "Table" }).click();
    await expect(tablePanel(page, "Pending workload").locator("caption")).toContainText(
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

  test("each section switches between its chart and the same values as a table", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await createTicketViaUi(page, { title: "table probe ticket" });
    await page.goto(base() + "/tickets/metrics");
    const detail = page.locator("#ticket-metrics-detail-content");
    await expect(detail.getByRole("heading", { name: "Created vs resolved" })).toBeVisible();

    const weeklyURL = page.url();
    const weekly = viewSwitch(page, "Created vs resolved");
    // Default view: the chart is shown, the table is in the document but not
    // rendered, so the two never read as two copies of one number.
    await expect(weekly.getByRole("button", { name: "Chart" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(chartPanel(page, "Created vs resolved").locator("svg")).toBeVisible();
    await expect(tablePanel(page, "Created vs resolved")).toBeHidden();

    await weekly.getByRole("button", { name: "Table" }).click();
    await expect(weekly.getByRole("button", { name: "Table" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(weekly.getByRole("button", { name: "Chart" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await expect(chartPanel(page, "Created vs resolved")).toBeHidden();
    const table = tablePanel(page, "Created vs resolved");
    await expect(table).toBeVisible();
    await expect(table.locator("caption")).toHaveText("Weekly ticket totals");
    await expect(table.getByRole("columnheader")).toHaveText([
      "Monday week",
      "Created",
      "Resolved",
    ]);
    // The table holds the same values as the chart: the weekly created column
    // sums to the Created KPI card.
    const createdCells = await table.locator("tbody tr td:nth-child(2)").allTextContents();
    const weeklyCreated = createdCells.reduce((sum, value) => sum + Number(value), 0);
    const kpiCreated = Number(
      await page.locator(".ticket-metrics-kpis .ticket-metrics-card-value").nth(1).textContent(),
    );
    expect(weeklyCreated).toBe(kpiCreated);
    // The switch is client-side state only: the URL never moves.
    expect(page.url()).toBe(weeklyURL);

    // Chart is the way back, and it restores the graphic, not another table.
    await weekly.getByRole("button", { name: "Chart" }).click();
    await expect(chartPanel(page, "Created vs resolved").locator("svg")).toBeVisible();
    await expect(tablePanel(page, "Created vs resolved")).toBeHidden();

    // The same switch exists in every section, including the attainment chart
    // that only exists when the period has a frozen commitment.
    for (const heading of metricsPanelHeadings.slice(1)) {
      await expect(viewSwitch(page, heading)).toBeVisible();
    }
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("an empty period renders each affected section's own empty state", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    // A period before any ticket exists in this database.
    await page.goto(base() + "/tickets/metrics?metrics_start=2020-01-06&metrics_end=2020-01-13");
    await expect(chartPanel(page, "Created vs resolved")).toContainText(
      "No results for selected period.",
    );
    // Age and workload are CURRENT-scope figures (the note under the filters
    // says so), so a past period does not empty them; their empty state is
    // proven by the Go layer against an empty database. Everything the period
    // scopes answers with its own message and draws no empty chart frame.
    await expect(chartPanel(page, "Resolution time distribution")).toContainText(
      "No results for selected period.",
    );
    await expect(chartPanel(page, "SLA attainment")).toContainText(
      "No tickets created in the selected period carry a frozen commitment.",
    );
    for (const heading of [
      "Created vs resolved",
      "Resolution time distribution",
      "SLA attainment",
    ]) {
      await expect(metricsPanel(page, heading).locator("svg")).toHaveCount(0);
    }
    // The table view still answers with its rows, so nothing is unreachable.
    await viewSwitch(page, "Created vs resolved").getByRole("button", { name: "Table" }).click();
    await expect(tablePanel(page, "Created vs resolved").getByRole("table")).toBeVisible();
    await assertNoHorizontalOverflow(page, 1280);
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("an explicit return keeps the list order on Back to tickets", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // The queue no longer links to metrics with its query (the sidebar owns
    // Metrics since #377), but the return contract still holds for any caller
    // that passes it: Back to tickets honours the recognised parameters.
    await page.goto(
      base() + "/tickets/metrics?return=" + encodeURIComponent("/tickets?sort=priority&evil=1"),
    );
    await expect(page.getByRole("link", { name: "Back to tickets" })).toHaveAttribute(
      "href",
      "/tickets?sort=priority",
    );
    await page.getByRole("link", { name: "Back to tickets" }).click();
    await expect(page).toHaveURL(/\/tickets\?.*sort=priority/);
    await expect(page.getByLabel("Order by")).toHaveValue("priority");

    // Without a return, Back goes to the plain queue.
    await page.locator('a.sidebar-link[href="/tickets/metrics"]').click();
    await expect(page).toHaveURL(/\/tickets\/metrics$/);
    await expect(page.getByRole("link", { name: "Back to tickets" })).toHaveAttribute(
      "href",
      "/tickets",
    );

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});

/**
 * Issue #393, S4: without scripting the switch cannot fire, so the stylesheet
 * reveals the table and hides the chart and the inert control. The dashboard
 * keeps every value it had with a script, and the filter form submits natively.
 */
test.describe("Ticket metrics without scripting (seeded)", () => {
  test.use({ javaScriptEnabled: false });

  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the table view renders instead of the chart", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/tickets/metrics");
    await expect(page.getByRole("heading", { name: "Ticket metrics", level: 1 })).toBeVisible();
    // The switch is inert without a script, so it is not offered at all.
    await expect(page.getByRole("group", { name: "View data" })).toBeHidden();
    // The chart is replaced by the table that holds the same values.
    await expect(chartPanel(page, "Created vs resolved").locator("svg")).toBeHidden();
    const table = tablePanel(page, "Created vs resolved").getByRole("table");
    await expect(table).toBeVisible();
    await expect(table.locator("caption")).toHaveText("Weekly ticket totals");
    await expect(table.getByRole("columnheader")).toHaveCount(3);
    await assertNoHorizontalOverflow(page, 390);
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});

/**
 * SLA attainment panel (issue #211, PR 5; chart added in #393, S4).
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

  test("renders the committed cohort with numeric counts, a visible denominator and the chart", async ({
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
      // The freshly created high-priority ticket is still within its SLA, so
      // both of its milestones are open: the numbers are tied to real cohort data.
      expect(open, `milestone ${index} has no open cohort`).toBeGreaterThanOrEqual(1);

      const rateText = (await rates.nth(index).textContent()) ?? "";
      const rateMatch = rateText.match(/^Rate (\d+)% \((\d+) decided\)$/);
      expect(rateMatch, `unparseable rate line: ${rateText}`).not.toBeNull();
      const decided = Number(rateMatch![2]);
      expect(decided).toBe(met + breached);
      const expectedPercent = met + breached === 0 ? 0 : Math.round((met / (met + breached)) * 100);
      expect(Number(rateMatch![1])).toBe(expectedPercent);
    }

    // The section's own inline SVG draws one row per group with the milestone
    // tags and the met/breached/open legend the table explains.
    const chart = chartPanel(page, "SLA attainment").locator("svg.ticket-metrics-attainment");
    await expect(chart).toBeVisible();
    await expect(chart).toHaveAttribute("role", "img");
    await expect(chart).toHaveAttribute("aria-label", /SLA attainment chart/);
    await expect(panel.locator(".ticket-metrics-legend-swatch")).toHaveCount(3);

    // The View data table carries exactly one total group.
    await viewSwitch(page, "SLA attainment").getByRole("button", { name: "Table" }).click();
    const table = tablePanel(page, "SLA attainment").locator("table");
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
    await viewSwitch(page, "SLA attainment").getByRole("button", { name: "Table" }).click();
    await expect(tablePanel(page, "SLA attainment").locator("table")).toBeVisible();

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
    await viewSwitch(page, "SLA attainment").getByRole("button", { name: "Table" }).click();
    const priorityTable = tablePanel(page, "SLA attainment").locator("table");
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
    await viewSwitch(page, "SLA attainment").getByRole("button", { name: "Table" }).click();
    const categoryTable = tablePanel(page, "SLA attainment").locator("table");
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
    await expect(chartPanel(page, "SLA attainment").locator(".ticket-metrics-empty")).toHaveText(
      "No tickets created in the selected period carry a frozen commitment.",
    );
    await expect(panel.locator(".ticket-metrics-attainment")).toHaveCount(0);
    await expect(panel.locator(".ticket-metrics-attainment-counts")).toHaveCount(0);
    await expect(panel.locator(".ticket-metrics-attainment-rate")).toHaveCount(0);
    await expect(panel.locator("svg.ticket-metrics-attainment")).toHaveCount(0);

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});
