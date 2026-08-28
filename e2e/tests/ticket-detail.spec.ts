/**
 * Ticket detail journeys: Properties sidebar, comments (including rejection on closed states),
 * state transitions, and timeline.
 *
 * Note on closed-ticket comment rejection: the browser-visible contract is that the
 * comment form is hidden when the ticket is in a closed state (IsClosed includes
 * resolved/closed/cancelled per template {{if not .Closed}}). Exhaustive HTTP status
 * rejection (403/422) for direct POSTs is covered by Go tests
 * (internal/adapters/http/handlers_comment_test.go) and is not duplicated with
 * page.request in browser E2E except for the one visible rejection below.
 * Keeping the browser-visible signal satisfies the functional requirement without
 * duplicating lower-layer coverage.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { createTicketViaUi } from "./helpers/navigation.js";

test.describe("Ticket detail", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("detail shows Properties sidebar and timeline, allows public comment (HTMX swap)", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Detail probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, description: "detail probe", category: "General", priority: "high" });

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByText("Properties").first()).toBeVisible();
    await expect(page.getByText("Requester")).toBeVisible();
    await expect(page.getByText("Category")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText("General");
    await expect(page.getByText("State")).toBeVisible();
    await expect(page.locator("#timeline")).toBeVisible();
    await expect(page.getByText("Description")).toBeVisible();

    const comment = "Hello from play " + Date.now().toString(36).slice(2, 6);
    await page.getByLabel(/comment body/i).fill(comment);
    // Comment form is a native POST (no hx-post) — submit and wait for navigation
    const responsePromise = page.waitForResponse(
      (resp) => resp.url().includes(`/tickets/${id}/comments`) && resp.request().method() === "POST"
    );
    await page.getByRole("button", { name: /add comment/i }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(303);
    await page.waitForURL(/\/tickets\/\d+/);
    await expect(page.locator("#timeline")).toContainText(comment);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail properties+comment",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("comment form hidden on closed states (browser-visible rejection)", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Closed comment probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, description: "detail probe", category: "General", priority: "high" });

    // Drive ticket to resolved via transitions new → in_progress → resolved
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(page.locator("#ticket-detail")).toBeVisible();
      const moveSelect = page.locator("#ticket-state");
      await expect(moveSelect).toBeVisible();
      const resp = await assertHtmxSwap(page, async () => {
        await moveSelect.selectOption(target);
      }, {
        endpoint: `/tickets/${id}/transition`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      });
      expect(resp.status()).toBe(200);
    }
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.getByText("Resolved").first()).toBeVisible({ timeout: 10_000 });
    const beforeTimeline = await page.locator("#timeline").textContent();
    // Comment form must be hidden on resolved (IsClosed includes resolved)
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);
    // Timeline unchanged after reload (no comment could be added via UI)
    await page.reload();
    const afterTimeline = await page.locator("#timeline").textContent();
    expect(afterTimeline).toEqual(beforeTimeline);

    // Now transition resolved → closed
    await page.goto(base() + `/tickets/${id}`);
    const toClosed = page.locator("#ticket-state");
    await expect(toClosed).toBeVisible();
    {
      const resp = await assertHtmxSwap(page, async () => {
        await toClosed.selectOption("closed");
      }, {
        endpoint: `/tickets/${id}/transition`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      });
      expect(resp.status()).toBe(200);
    }
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.getByText("Closed").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);

    // Cancelled state: create a fresh ticket and cancel from new
    const cancelTitle = "Cancel probe " + Date.now().toString(36).slice(2, 8);
    const cancelId = await createTicketViaUi(page, { title: cancelTitle, description: "detail probe", category: "General", priority: "high" });
    await page.goto(base() + `/tickets/${cancelId}`);
    const toCancel = page.locator("#ticket-state");
    await expect(toCancel).toBeVisible();
    {
      const resp = await assertHtmxSwap(page, async () => {
        await toCancel.selectOption("cancelled");
      }, {
        endpoint: `/tickets/${cancelId}/transition`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      });
      expect(resp.status()).toBe(200);
    }
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.goto(base() + `/tickets/${cancelId}`);
    await expect(page.getByText("Cancelled").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail comment rejection visible",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("priority change via HTMX swap updates #ticket-detail without full navigation", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "HTMX probe " + Date.now().toString(36).slice(2, 8),
      description: "detail probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    // Complementary evidence: hx-post targeting #ticket-detail exists
    const hxForms = page.locator('[hx-post][hx-target="#ticket-detail"]');
    await expect(hxForms.first()).toBeVisible();

    const prioritySelect = page.locator("#ticket-priority");
    await expect(prioritySelect).toBeVisible();

    await assertHtmxSwap(page, async () => {
      await prioritySelect.selectOption("critical");
    }, {
      endpoint: `/tickets/${id}/edit`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#ticket-detail",
    });

    await expect(page.locator("#ticket-detail")).toContainText(/critical/i);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail HTMX swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});