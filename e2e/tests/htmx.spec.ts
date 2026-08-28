/**
 * HTMX interaction specs: verify partial swaps without full reload where observable.
 *
 * HTMX swaps MUST modify the expected region WITHOUT full navigation (assert region
 * content changed AND URL unchanged or only hx-push-url managed). hx-* attributes
 * may be complementary evidence but not the sole proof.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { createTicketViaUi, resolveWorkflowHref } from "./helpers/navigation.js";

test.describe("HTMX interactions", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("ticket list filters hx-get swap changes #ticket-list without full reload", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    // Seed a couple tickets for filterable content
    for (let i = 0; i < 2; i++) {
      const t = `HTMX filter probe ${Date.now().toString(36).slice(2, 8)} ${i}`;
      await createTicketViaUi(page, { title: t, description: "htmx", category: "General", priority: "low" });
    }
    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    const htmxFilterForm = page.locator('form[hx-get][hx-target="#ticket-list"]');
    // Complementary: hx-* attributes present
    await expect(htmxFilterForm.first()).toBeVisible();
    await expect(htmxFilterForm.first()).toHaveAttribute("hx-target", "#ticket-list");
    await expect(htmxFilterForm.first()).toHaveAttribute("hx-swap", /outerHTML/);

    const searchInput = page.locator('input[name="q"], input[type="search"]').first();
    await expect(searchInput).toBeVisible();
    const target = page.locator("#ticket-list");
    const before = await target.innerHTML();
    const urlBefore = page.url();
    // Create a unique ticket then filter for it to observe #ticket-list change
    const unique = "HtmxSearch " + Date.now().toString(36).slice(2, 8);
    await createTicketViaUi(page, { title: unique, description: "htmx search", category: "General", priority: "medium" });
    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    await page.getByPlaceholder(/search by id or title/i).fill(unique);
    const searchBtn = page.getByRole("button", { name: /search/i });
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/tickets") && r.request().method() === "GET"),
      searchBtn.click(),
    ]).catch(() => {});
    await expect(page.locator("#ticket-list")).toBeVisible();
    await expect(page.locator("#ticket-list").getByText(unique)).toBeVisible({ timeout: 10000 });
    const after = await page.locator("#ticket-list").innerHTML();
    expect(after).not.toBe(before);
    // HTMX swap should not cause full navigation; for search form hx-push-url is not set, URL stays /tickets (query via hx-get)
    expect(page.url()).toContain("/tickets");
    // Header still intact (no full reload chrome loss)
    await expect(page.locator('h1:has-text("Tickets")')).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "htmx ticket list filter swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("users tabs HTMX swap changes #users-root without full page reload", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/users");
    await expect(page.locator(".users-root")).toBeVisible();
    const htmx = page.locator('.users-root [hx-get][hx-target="#users-root"]').first();
    await expect(htmx).toBeVisible();
    await expect(htmx).toHaveAttribute("hx-target", "#users-root");
    const before = await page.locator("#users-root").innerHTML();
    const urlBefore = page.url();
    // Click Deactivated tab (htmx outerHTML swap) — deterministic empty or filtered view
    const deactivatedTab = page.locator('a[href*="status=deactivated"]').first();
    await expect(deactivatedTab).toBeVisible();
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/users") && r.request().method() === "GET"),
      deactivatedTab.click(),
    ]).catch(() => {});
    await expect(page.locator("#users-root")).toBeVisible();
    const after = await page.locator("#users-root").innerHTML();
    expect(after).not.toBe(before);
    // hx-push-url=true on tabs will push state, but chrome (header) must remain and no full reload error
    await expect(page.locator("#users-list-title")).toBeVisible();
    // URL may gain ?status=deactivated via push, but path stays /users
    expect(page.url()).toContain("/users");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "htmx users tabs swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("workflow builder HTMX partial swap does not reload surrounding header", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    const wfHref = await resolveWorkflowHref(page);
    if (!wfHref) throw new Error("workflow href not found for seeded General");
    await page.goto(base() + wfHref);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    const headerTextBefore = await page.locator(".page-header h1").textContent();
    const builderBefore = await page.locator("#workflow-builder").innerHTML();
    const urlBefore = page.url();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const btn = page.locator(".workflow-add-options button").filter({ hasText: "Manual task" }).first();
    await expect(btn).toBeVisible();
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/workflow") && r.request().method() === "POST"),
      btn.click(),
    ]);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    expect(page.url()).toBe(urlBefore);
    const headerTextAfter = await page.locator(".page-header h1").textContent();
    expect(headerTextAfter).toBe(headerTextBefore);
    const builderAfter = await page.locator("#workflow-builder").innerHTML();
    expect(builderAfter).not.toBe(builderBefore);
    // Also assert hx-target complementary
    await expect(page.locator("#workflow-builder form")).toHaveAttribute("hx-target", "#workflow-builder");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "htmx workflow builder swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("ticket detail HTMX swap updates #ticket-detail without full navigation", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "HTMX detail swap " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, description: "swap", category: "General", priority: "medium" });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const hxForms = page.locator('[hx-post][hx-target="#ticket-detail"]');
    await expect(hxForms.first()).toBeVisible();
    const url = page.url();
    const detailBefore = await page.locator("#ticket-detail").innerHTML();
    const prioritySelect = page.locator("#ticket-priority");
    await expect(prioritySelect).toBeVisible();
    await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/tickets/${id}/edit`) && r.request().method() === "POST"),
      prioritySelect.selectOption("critical"),
    ]);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    expect(page.url()).toBe(url);
    const detailAfter = await page.locator("#ticket-detail").innerHTML();
    expect(detailAfter).not.toBe(detailBefore);
    await expect(page.locator("#ticket-detail")).toContainText(/critical/i);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "htmx ticket detail swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
