/**
 * Categories + Workflow Builder journeys.
 *
 * The published workflow version CAN be observed on the ticket detail page via the
 * passive pending status line (#workflow-pending-status + .pending-status-detail) —
 * requester-owned tickets are passive and never render the active current-task card.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import {
  assertCanonicalScreen,
  collectObservability,
} from "./helpers/layout.js";
import { assertHtmxNoSwap, assertHtmxSwap } from "./helpers/htmx.js";
import { createTicketViaUi } from "./helpers/navigation.js";

test.describe("Categories", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("categories index shows seeded category with workflow badge", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await expect(
      page.locator("h1").filter({ hasText: "Categories" }),
    ).toBeVisible();
    await expect(page.getByText("General")).toBeVisible();
    await expect(page.locator(".badge").first()).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "categories index",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("create, rename, and delete a category", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const catName = "Probe Cat " + Date.now();
    await page.goto(base() + "/categories/new");
    await expect(page.locator('h1:has-text("New category")')).toBeVisible();
    await page.getByLabel(/name/i).fill(catName);
    await page
      .getByRole("button", { name: /create category|save|create/i })
      .click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.getByText(catName)).toBeVisible();

    const row = page.locator("tr").filter({ hasText: catName });
    await expect(row).toHaveCount(1);
    await row.locator('a[href*="/edit"]').click();
    await expect(page.locator("h1")).toContainText(/rename category/i);
    const renamed = catName + " Renamed";
    await page.getByLabel(/name/i).fill(renamed);
    await page.getByRole("button", { name: /save/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.getByText(renamed)).toBeVisible();

    const delRow = page.locator("tr").filter({ hasText: renamed });
    await expect(delRow).toHaveCount(1);
    await delRow.getByRole("button", { name: /delete/i }).click();
    await expect(page.getByText(renamed)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "categories create/rename/delete",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("workflow builder integrated journey: create category, add step, publish, reload, create ticket, verify published workflow in ticket", async ({
    page,
  }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // 1) create a category
    const catName = "FlowCat " + Date.now().toString(36).slice(2, 8);
    await page.goto(base() + "/categories/new");
    await page.getByLabel(/name/i).fill(catName);
    await page
      .getByRole("button", { name: /create category|save|create/i })
      .click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.getByText(catName)).toBeVisible();

    // 2) open its workflow
    const catRow = page.locator("tr").filter({ hasText: catName });
    await expect(catRow).toHaveCount(1);
    const editHref = await catRow
      .locator('a[href*="/edit"]')
      .getAttribute("href");
    const m = editHref?.match(/\/categories\/(\d+)\/edit/);
    if (!m) throw new Error("cannot extract category id for " + catName);
    const categoryId = m[1];
    const workflowPath = `/categories/${categoryId}/workflow`;
    await page.goto(base() + workflowPath);
    await expect(page.locator("#workflow-builder")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator("h2#workflow-builder-title")).toContainText(
      /workflow steps/i,
    );
    await expect(page.locator(".workflow-step-rail")).toBeVisible();

    // Ensure workflow builder form carries HTMX contract (complementary evidence)
    await expect(page.locator("#workflow-builder form")).toHaveAttribute(
      "hx-post",
      /\/workflow/,
    );
    await expect(page.locator("#workflow-builder form")).toHaveAttribute(
      "hx-target",
      "#workflow-builder",
    );

    // 3) add a VALID step — Manual task with instructions is valid by default
    const cards = page.locator(".workflow-step-card");
    const countBeforeAdd = await cards.count();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const addBtn = page
      .locator(".workflow-add-options button")
      .filter({ hasText: "Manual task" })
      .first();
    await expect(addBtn).toBeVisible();

    await assertHtmxSwap(
      page,
      async () => {
        await addBtn.click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("add_step_type") === "manual_task"
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(countBeforeAdd + 1);
    await expect(page.locator("[data-workflow-live]")).toContainText(
      /added a step/i,
    );

    // Configure the newly added manual_task step — instructions are required for publish
    const instructionsInput = page.getByLabel(/instructions/i);
    await expect(instructionsInput).toBeVisible({ timeout: 10000 });
    await expect(instructionsInput).toHaveAttribute(
      "hx-trigger",
      "input changed delay:600ms",
    );
    await expect(instructionsInput).toHaveAttribute("hx-swap", "none");
    await assertHtmxNoSwap(
      page,
      async () => {
        await instructionsInput.fill("Handle the ticket");
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.search === ""
          );
        },
        method: "POST",
        expectedStatus: 200,
      },
    );
    await expect(page.locator("#workflow-builder")).toBeVisible();

    // Remove step unconditionally (prove removal works)
    const countBeforeRemove = await cards.count();
    const lastCard = cards.last();
    const menuSummary = lastCard.locator(".workflow-trigger").first();
    await expect(menuSummary).toBeVisible();
    await menuSummary.click();
    const removeBtn = lastCard.getByRole("button", { name: /remove step/i });
    await expect(removeBtn).toBeVisible();

    // Remove-step POST goes to /categories/{id}/workflow?step_index=... —
    // the action=remove_step lives in the form body, not the query string.
    await assertHtmxSwap(
      page,
      async () => {
        await removeBtn.click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("step_index") ===
              String(countBeforeRemove - 1) &&
            !parsedURL.searchParams.has("action")
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(countBeforeRemove - 1);

    // Re-add a step so we have at least one to publish (workflow must be non-empty)
    const countBeforeReAdd = await cards.count();
    expect(countBeforeReAdd).toBe(0);
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    await expect(addBtn).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await addBtn.click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("add_step_type") === "manual_task"
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(1);
    const instr = page.getByLabel(/instructions/i);
    await expect(instr).toBeVisible();
    await expect(instr).toHaveAttribute(
      "hx-trigger",
      "input changed delay:600ms",
    );
    await expect(instr).toHaveAttribute("hx-swap", "none");
    if ((await instr.inputValue()) === "Handle the ticket") {
      await assertHtmxNoSwap(
        page,
        async () => {
          await instr.fill("Handle the ticket draft");
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === `/categories/${categoryId}/workflow` &&
              parsedURL.search === ""
            );
          },
          method: "POST",
          expectedStatus: 200,
        },
      );
    }
    await assertHtmxNoSwap(
      page,
      async () => {
        await instr.fill("Handle the ticket");
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.search === ""
          );
        },
        method: "POST",
        expectedStatus: 200,
      },
    );

    // 4) PUBLISH — must execute publication, not just check button exists
    const publishBtn = page.getByRole("button", { name: /publish/i });
    await expect(publishBtn).toBeVisible();
    const publishResp = await assertHtmxSwap(
      page,
      async () => {
        await publishBtn.click();
      },
      {
        endpoint: `/categories/${categoryId}/workflow`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    expect(publishResp.status()).toBe(200);
    // After publish, no inline errors
    await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);

    // 5) reload and verify persistence — step count survives reload
    const countAfterPublish = await cards.count();
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator(".workflow-step-card")).toHaveCount(
      countAfterPublish,
    );
    // Badge on /categories should now show Published for this category
    await page.goto(base() + "/categories");
    await expect(
      page.locator("tr").filter({ hasText: catName }).locator(".badge"),
    ).toContainText(/published/i);

    // 6) create a ticket using that category
    const ticketTitle = "FlowTicket " + Date.now().toString(36).slice(2, 8);
    const ticketId = await createTicketViaUi(page, {
      title: ticketTitle,
      description: "workflow published probe",
      category: catName,
      priority: "high",
    });

    // 7) verify the published workflow appears as a passive timeline item
    // because the newly created ticket has no assigned agent yet.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText(catName);
    await expect(page.locator("#workflow-pending")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator("#workflow-pending")).toHaveClass(
      /workflow-pending-info/,
    );
    await expect(page.locator("#workflow-pending")).toContainText(
      "IN PROGRESS",
    );
    await expect(page.locator("#workflow-pending")).toContainText(
      "The assigned agent is handling this task.",
    );
    await expect(page.locator("#workflow-pending")).toContainText(
      "Updates will appear here when complete.",
    );
    await expect(
      page.locator("#workflow-pending .workflow-instruction"),
    ).toHaveCount(0);
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(
      /workflow-pending-info/,
    );

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "workflow passive timeline item",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.locator("#workflow-pending")).toHaveClass(
      /workflow-pending-info/,
    );
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "workflow passive timeline item mobile",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    await page.setViewportSize({ width: 1280, height: 800 });
    await page.reload();
    const assignee = page.locator("#assign-user");
    await expect(assignee).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await assignee.selectOption({ label: "Alice Admin" });
      },
      {
        endpoint: `/tickets/${ticketId}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    const pending = page.locator("#workflow-pending");
    await expect(pending).toHaveClass(/workflow-pending-action/);
    await expect(pending.locator("h3")).toHaveText("CURRENT TASK");
    await expect(pending.locator(".workflow-instruction")).toContainText(
      "Handle the ticket",
    );
    await expect(pending.getByLabel("Solution (optional)")).toBeVisible();
    await expect(
      pending.getByRole("button", { name: "Complete" }),
    ).toBeVisible();
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(
      /workflow-pending-action/,
    );

    await assertHtmxSwap(
      page,
      async () => {
        await pending.getByRole("button", { name: "Complete" }).click();
      },
      {
        endpoint: `/tickets/${ticketId}/workflow/steps/1/complete`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.locator("#workflow-pending")).toHaveCount(0);
    const assertCompletedManualStatic = async () => {
      const completedManual = page
        .locator("#timeline .timeline-entry.timeline-manual")
        .first();
      await expect(completedManual).toHaveCount(1);
      await expect(completedManual).toBeVisible();
      const checkIcon = completedManual.locator(
        ".timeline-manual-heading .event-icon",
      );
      await expect(checkIcon.locator("svg")).toBeVisible();
      await expect(checkIcon).toHaveCSS("color", "rgb(24, 115, 77)");
      await expect(
        completedManual.locator(".timeline-manual-heading .main"),
      ).toHaveText("Alice Admin completed the task");
      await expect(
        completedManual.getByText("TASK", { exact: true }),
      ).toHaveCount(1);
      await expect(completedManual.locator("dd").first()).toHaveText(
        "Handle the ticket",
      );
      await expect(completedManual.locator("dl")).toBeVisible();
      await expect(completedManual.locator(".when")).toBeVisible();
      await expect(
        completedManual.getByText("SOLUTION", { exact: true }),
      ).toHaveCount(0);
      await expect(
        completedManual.locator(
          "details, summary, button, .timeline-event-summary, [open], [aria-expanded], [aria-controls], [tabindex]",
        ),
      ).toHaveCount(0);
      await expect(completedManual.locator(".event-icon")).not.toContainText(
        "›",
      );
      await expect(completedManual.locator(".when")).not.toContainText(
        "Alice Admin",
      );
      await expect(completedManual).not.toHaveCSS("cursor", "pointer");
      await expect(
        completedManual.locator(".timeline-manual-heading"),
      ).not.toHaveCSS("cursor", "pointer");
      const iconBefore = await completedManual
        .locator(".event-icon")
        .evaluate((element) => getComputedStyle(element, "::before").content);
      expect(iconBefore).toBe("none");
    };

    // Desktop: the completed task and both definition-list rows are visible
    // without a disclosure control; this completion has no solution.
    await assertCompletedManualStatic();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "workflow completed timeline",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // Mobile keeps the same static, always-visible event markup.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await assertCompletedManualStatic();
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "workflow completed timeline mobile",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
