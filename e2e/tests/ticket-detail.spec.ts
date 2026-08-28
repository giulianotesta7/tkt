/**
 * Ticket detail journeys: Properties sidebar, comments (including rejection on closed states),
 * state transitions, and timeline.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";

test.describe("Ticket detail", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("detail shows Properties sidebar and timeline, allows public comment", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Detail probe " + Date.now();
    const id = await createTicketViaUi(page, { title, description: "detail probe", category: "General", priority: "high" });

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    // Properties sidebar contract
    await expect(page.getByText("Properties").first()).toBeVisible();
    await expect(page.getByText("Requester")).toBeVisible();
    await expect(page.getByText("Category")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText("General");
    await expect(page.getByText("State")).toBeVisible();
    // Timeline
    await expect(page.locator("#timeline")).toBeVisible();
    // Conversation + description
    await expect(page.getByText("Description")).toBeVisible();

    // Add a public comment
    await page.getByLabel(/comment body/i).fill("Hello from play " + Date.now());
    await page.getByRole("button", { name: /add comment/i }).click();
    // After POST, redirect back to detail and comment appears in timeline
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#timeline")).toContainText("Hello from play");
    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("comment rejection on resolved / closed / cancelled is enforced", async ({ page }) => {
    await loginAsSeeded(page);
    const title = "Closed comment probe " + Date.now();
    const id = await createTicketViaUi(page, { title, description: "detail probe", category: "General", priority: "high" });

    // Drive ticket to resolved via transitions (admin can transition any ticket)
    // new -> in_progress -> resolved (auto-submit without Apply for these)
    for (const target of ["in_progress", "resolved"]) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(page.locator("#ticket-detail")).toBeVisible();
      const moveSelect = page.locator("#ticket-state");
      if (await moveSelect.count()) {
        await moveSelect.selectOption(target);
        // auto-submit via toggleStateReason → requestSubmit; wait for detail to re-render
        await expect(page.locator("#ticket-detail")).toBeVisible();
        await page.waitForTimeout(600);
      }
    }
    // Verify resolved
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.getByText("Resolved").first()).toBeVisible({ timeout: 10_000 });

    // Attempt comment on resolved — should be rejected with error banner, no comment stored
    const beforeTimeline = await page.locator("#timeline").textContent();
    await page.goto(base() + `/tickets/${id}`);
    const commentBox = page.getByLabel(/comment body/i);
    // On closed states the comment form is hidden ({{if not .Closed}}). So for resolved it is hidden too (IsClosed includes resolved).
    // The spec says resolved/closed/cancelled reject comments; template hides form. Verify form is absent and direct POST is rejected.
    await expect(commentBox).toHaveCount(0);

    // Direct POST should be rejected (403/422) and surface an error banner via renderDetailError
    const resp = await page.request.post(base() + `/tickets/${id}/comments`, {
      form: { body: "should be rejected", visibility: "public" },
      headers: { Cookie: (await page.context().cookies()).map(c => `${c.name}=${c.value}`).join("; ") },
    });
    // App returns 422 or 403 with rendered page containing error text
    expect([403, 422, 400].includes(resp.status()), `unexpected status ${resp.status()} body ${await resp.text().then(t=>t.slice(0,500))}`).toBeTruthy();
    const body = await resp.text();
    expect(body.toLowerCase()).toContain("comment");

    // Ensure timeline unchanged (no new comment inserted)
    await page.goto(base() + `/tickets/${id}`);
    const afterTimeline = await page.locator("#timeline").textContent();
    expect(afterTimeline).toEqual(beforeTimeline);

    // Now transition resolved -> closed (no reason)
    await page.goto(base() + `/tickets/${id}`);
    const toClosed = page.locator("#ticket-state");
    if (await toClosed.count()) {
      await toClosed.selectOption("closed");
      await page.waitForTimeout(600);
      await expect(page.locator("#ticket-detail")).toBeVisible();
    }
    await page.goto(base() + `/tickets/${id}`);
    // closed state visible
    await expect(page.getByText("Closed").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);

    // Transition closed -> cancelled is NOT allowed; instead create a fresh ticket and cancel it from new
    const cancelTitle = "Cancel probe " + Date.now();
    const cancelId = await createTicketViaUi(page, { title: cancelTitle, description: "detail probe", category: "General", priority: "high" });
    await page.goto(base() + `/tickets/${cancelId}`);
    const toCancel = page.locator("#ticket-state");
    await toCancel.selectOption("cancelled");
    await page.waitForTimeout(600);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.goto(base() + `/tickets/${cancelId}`);
    await expect(page.getByText("Cancelled").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);
    const resp2 = await page.request.post(base() + `/tickets/${cancelId}/comments`, {
      form: { body: "cancelled comment", visibility: "public" },
      headers: { Cookie: (await page.context().cookies()).map(c => `${c.name}=${c.value}`).join("; ") },
    });
    expect([403, 422, 400].includes(resp2.status())).toBeTruthy();
  });

  test("HTMX partial swap attributes exist on ticket detail", async ({ page }) => {
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, { title: "HTMX probe " + Date.now(), description: "detail probe", category: "General", priority: "high" });
    await page.goto(base() + `/tickets/${id}`);
    // Title inline edit form, priority select, assign select, transition, comment form all carry hx-post / hx-target
    const hxForms = page.locator('[hx-post][hx-target="#ticket-detail"]');
    await expect(hxForms.first()).toBeVisible();
    // At least the priority control has hx-post
    await expect(page.locator('form[hx-post*="/edit"]').first()).toBeVisible();
  });
});
