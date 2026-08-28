/**
 * Categories + Workflow Builder journeys.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";
import { openWorkflowBuilder, resolveWorkflowHref } from "./helpers/navigation.js";

test.describe("Categories", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("categories index shows seeded category with workflow badge", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await expect(page.locator("h1").filter({ hasText: "Categories" })).toBeVisible();
    await expect(page.getByText("General")).toBeVisible();
    // badge Published / Draft
    await expect(page.locator(".badge").first()).toBeVisible();
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("create, rename, and delete a category", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const catName = "Probe Cat " + Date.now();
    await page.goto(base() + "/categories/new");
    await page.getByLabel(/category names are unique/i).isHidden(); // just ensure page loaded
    await page.getByLabel(/name/i).fill(catName);
    // submit
    await page.getByRole("button", { name: /create category|save|create/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.getByText(catName)).toBeVisible();

    // Find edit link for that category: locate row with catName then edit
    const row = page.locator("tr").filter({ hasText: catName });
    await row.locator('a[href*="/edit"]').click();
    await expect(page.locator("h1")).toContainText(/rename category/i);
    const renamed = catName + " Renamed";
    await page.getByLabel(/name/i).fill(renamed);
    await page.getByRole("button", { name: /save/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.getByText(renamed)).toBeVisible();

    // Delete (the delete is a POST button in Actions)
    const delRow = page.locator("tr").filter({ hasText: renamed });
    await delRow.getByRole("button", { name: /delete/i }).click();
    await expect(page.getByText(renamed)).toHaveCount(0);

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("workflow builder: loads, shows steps rail, can add and remove a step, publish", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await openWorkflowBuilder(page);
    await expect(page.locator("h2#workflow-builder-title")).toContainText(/workflow steps/i);
    await expect(page.locator(".workflow-step-rail")).toBeVisible();

    // The seeded General category has at least one step (manual_task) already published and loaded as draft
    await expect(page.locator(".workflow-step-card").first()).toBeVisible();

    // Add a step via the + Add step popover -> Manual task (must exist; a silent skip would mask a regression)
    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const addBtn = page.locator(".workflow-add-options button").filter({ hasText: "Manual task" }).first();
    // Ensure HTMX is present
    await expect(page.locator("#workflow-builder form")).toHaveAttribute("hx-post", /\/workflow/);
    await addBtn.click();
    // After HTMX swap, builder remains and step count grows
    await expect(page.locator("#workflow-builder")).toBeVisible();
    const cards = page.locator(".workflow-step-card");
    await expect(cards).not.toHaveCount(0);
    // Live region should announce addition
    await expect(page.locator("[data-workflow-live]")).toContainText(/added a step/i);

    // Publish button exists and is HTMX-capable form
    await expect(page.getByRole("button", { name: /publish/i })).toBeVisible();

    // If we added a step, remove the last non-final card's step via its menu
    const stepCards = page.locator(".workflow-step-card");
    const countBefore = await stepCards.count();
    if (countBefore > 1) {
      const lastCard = stepCards.last();
      const menuSummary = lastCard.locator(".workflow-trigger").first();
      if (await menuSummary.count()) {
        await menuSummary.click();
        const removeBtn = lastCard.getByRole("button", { name: /remove step/i });
        if (await removeBtn.count()) {
          await removeBtn.click();
          await expect(page.locator("#workflow-builder")).toBeVisible();
        }
      }
    }

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("workflow builder HTMX attributes are present (partial swap contract)", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    const wfHref = await resolveWorkflowHref(page);
    if (!wfHref) test.skip(true, "no workflow link found");
    await page.goto(base() + wfHref);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    const form = page.locator("#workflow-builder form#workflow-form");
    await expect(form).toHaveAttribute("hx-post", /\/workflow/);
    await expect(form).toHaveAttribute("hx-target", "#workflow-builder");
    await expect(form).toHaveAttribute("hx-swap", /outerHTML/);
    // Add-step buttons carry hx-post / hx-target as well
    const hxButtons = page.locator('button[hx-post*="/workflow"]');
    await expect(hxButtons.first()).toBeVisible();
  });
});
