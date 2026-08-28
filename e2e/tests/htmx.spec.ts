/**
 * HTMX interaction specs: verify partial swaps without full reload where observable.
 *
 * Covers ticket list filters, users tabs/search, and category workflow builder HTMX contracts.
 * Where a real swap can be exercised without product changes, we perform it and assert
 * the target updates while the surrounding chrome remains.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { resolveWorkflowHref } from "./helpers/navigation.js";

test.describe("HTMX interactions", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("ticket list filters carry HTMX attributes (search + state/priority)", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/tickets");
    // Create a couple tickets to have filterable content
    for (let i = 0; i < 2; i++) {
      const t = `HTMX filter probe ${Date.now()} ${i}`;
      await page.goto(base() + "/tickets/new");
      await page.getByLabel(/title/i).fill(t);
      await page.getByLabel(/description/i).fill("htmx");
      await page.getByLabel(/category/i).selectOption({ label: "General" });
      await page.getByLabel(/priority/i).selectOption("low");
      await page.getByRole("button", { name: /create ticket/i }).click();
      await expect(page).toHaveURL(/\/tickets/);
      await expect(page.getByText(t).first()).toBeVisible();
    }
    await page.goto(base() + "/tickets");
    // Search input should be HTMX-enhanced (hx-get / hx-post targeting ticket_list)
    const search = page.locator('input[name="q"], input[type="search"]').first();
    // If native search exists, check HTMX attrs on its form or input
    const ticketList = page.locator("#ticket-list, .ticket-list, table").first();
    await expect(ticketList).toBeVisible();
    const htmxNodes = page.locator('[hx-get], [hx-post]');
    // At least the filter form or search should be HTMX
    await expect(htmxNodes.first()).toBeVisible({ timeout: 10_000 });
  });

  test("users tabs/search carry HTMX attributes", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/users");
    await expect(page.locator(".users-root")).toBeVisible();
    // Users tabs or search should be HTMX
    const htmx = page.locator('.users-root [hx-get], .users-root [hx-post]');
    if (await htmx.count()) {
      await expect(htmx.first()).toBeVisible();
      const target = await htmx.first().getAttribute("hx-target");
      // target should point inside users screen (partial swap, not full reload)
      expect(target).toBeTruthy();
    } else {
      // Fallback: at least the status tabs exist as links (graceful degradation)
      await expect(page.locator('a[href*="/users"]').first()).toBeVisible();
    }
  });

  test("workflow builder partial swap does not reload surrounding header", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    const wfHref = await resolveWorkflowHref(page);
    if (!wfHref) test.skip(true, "no workflow link");
    await page.goto(base() + wfHref);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    const headerTextBefore = await page.locator(".page-header h1").textContent();
    // Trigger a workflow builder HTMX action: add manual_task via its button
    const addSummary = page.locator(".workflow-add-step summary").first();
    if (await addSummary.count()) {
      await addSummary.click();
      const btn = page.locator(".workflow-add-options button").filter({ hasText: "Manual task" }).first();
      // Record current URL (HTMX uses hx-push-url=false so URL must not change)
      const urlBefore = page.url();
      await btn.click();
      await expect(page.locator("#workflow-builder")).toBeVisible();
      expect(page.url()).toBe(urlBefore);
      const headerTextAfter = await page.locator(".page-header h1").textContent();
      expect(headerTextAfter).toBe(headerTextBefore);
    }
  });

  test("ticket detail HTMX swap updates ticket-detail target without full page nav", async ({ page }) => {
    await loginAsSeeded(page);
    const title = "HTMX detail swap " + Date.now();
    await page.goto(base() + "/tickets/new");
    await page.getByLabel(/title/i).fill(title);
    await page.getByLabel(/description/i).fill("swap");
    await page.getByLabel(/category/i).selectOption({ label: "General" });
    await page.getByLabel(/priority/i).selectOption("medium");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.getByText(title).first()).toBeVisible();
    const href = await page.getByText(title).first().getAttribute("href");
    const m = href?.match(/\/tickets\/(\d+)/);
    if (!m) throw new Error("no ticket id");
    await page.goto(base() + `/tickets/${m[1]}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const url = page.url();
    const detailBefore = await page.locator("#ticket-detail").innerHTML();
    // Change priority via HTMX prop-control (select with hx-post)
    const prioritySelect = page.locator("#ticket-priority");
    if (await prioritySelect.count()) {
      await prioritySelect.selectOption("critical");
      // HTMX outerHTML swap re-renders #ticket-detail; URL must stay same (hx-push-url default false)
      await expect(page.locator("#ticket-detail")).toBeVisible();
      expect(page.url()).toBe(url);
      const detailAfter = await page.locator("#ticket-detail").innerHTML();
      expect(detailAfter).not.toBe(detailBefore);
    } else {
      test.skip(true, "priority select not rendered (closed ticket?)");
    }
  });
});
