/**
 * HTMX interaction specs: verify partial swaps without full reload where observable.
 *
 * Each HTMX interaction uses the shared assertHtmxSwap helper which verifies:
 *  - HX-Request: true header on the request
 *  - exact expected method and status
 *  - target region innerHTML changed
 *  - zero document navigation events on the main frame
 *  - non-target chrome (h1) unchanged
 *  - URL unchanged or matched expectedUrl
 *
 * Search filter is covered in tickets.spec.ts (functional home).
 * Priority change is covered in ticket-detail.spec.ts (functional home).
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { resolveWorkflowHref } from "./helpers/navigation.js";

test.describe("HTMX interactions", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
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
      endpoint: "/users",
      method: "GET",
      expectedStatus: 200,
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
      endpoint: "/workflow",
      method: "POST",
      expectedStatus: 200,
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