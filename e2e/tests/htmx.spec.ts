/**
 * HTMX interaction specs: verify partial swaps without full reload where observable.
 *
 * HTMX swaps MUST modify the expected region WITHOUT full navigation (assert region
 * content changed AND URL unchanged or only hx-push-url managed). hx-* attributes
 * may be complementary evidence but not the sole proof.
 *
 * Each HTMX interaction uses the shared assertHtmxSwap helper which verifies:
 *  - HX-Request: true header on the request
 *  - 200 response status
 *  - target region innerHTML changed
 *  - non-target chrome (h1) unchanged
 *  - URL unchanged or matched expectedUrl
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
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
    // Seed a unique ticket to filter for
    const unique = "HtmxSearch " + Date.now().toString(36).slice(2, 8);
    await createTicketViaUi(page, { title: unique, description: "htmx search", category: "General", priority: "medium" });

    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    // Complementary: hx-* attributes present
    const htmxFilterForm = page.locator('form[hx-get][hx-target="#ticket-list"]');
    await expect(htmxFilterForm.first()).toBeVisible();
    await expect(htmxFilterForm.first()).toHaveAttribute("hx-target", "#ticket-list");
    await expect(htmxFilterForm.first()).toHaveAttribute("hx-swap", /outerHTML/);

    // Search for the unique ticket — verify the HTMX request and filtered result
    const searchInput = page.getByPlaceholder(/search by id or title/i);
    const urlBefore = page.url();
    await searchInput.fill(unique);
    // Set up response interceptor BEFORE the action to avoid race conditions
    const searchPromise = page.waitForResponse(
      (resp) => resp.url().includes("/tickets") && resp.request().headers()["hx-request"] === "true",
    );
    await page.getByRole("button", { name: /search/i }).click();
    const searchResp = await searchPromise;
    expect(searchResp.status()).toBe(200);
    await expect(page.locator("#ticket-list").getByText(unique)).toBeVisible({ timeout: 10_000 });
    // URL unchanged (HTMX state swap, no push)
    expect(page.url()).toBe(urlBefore);

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

    const deactivatedTab = page.locator('a[href*="status=deactivated"]').first();
    await expect(deactivatedTab).toBeVisible();

    await assertHtmxSwap(page, async () => {
      await deactivatedTab.click();
    }, {
      urlPattern: (url) => url.includes("/users"),
      hxTarget: "#users-root",
      expectedUrl: /\/users/,
    });

    // Header must remain intact (no full reload chrome loss)
    await expect(page.locator("#users-list-title")).toBeVisible();
    // URL may gain ?status=deactivated via hx-push-url, but path stays /users
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
    await page.goto(base() + wfHref);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    // Also assert hx-target complementary
    await expect(page.locator("#workflow-builder form")).toHaveAttribute("hx-target", "#workflow-builder");

    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const btn = page.locator(".workflow-add-options button").filter({ hasText: "Manual task" }).first();
    await expect(btn).toBeVisible();

    await assertHtmxSwap(page, async () => {
      await btn.click();
    }, {
      urlPattern: (url) => url.includes("/workflow"),
      hxTarget: "#workflow-builder",
    });

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
});