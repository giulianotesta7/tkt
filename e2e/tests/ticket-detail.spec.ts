/**
 * Ticket detail journeys: Properties sidebar, comments (including rejection on closed states),
 * state transitions, and timeline.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";

async function createTicket(page: import("@playwright/test").Page, title: string): Promise<string> {
  await page.goto(base() + "/tickets/new");
  await expect(page.locator("h2")).toContainText(/ticket details/i);
  await page.getByLabel(/title/i).fill(title);
  await page.getByLabel(/description/i).fill("detail probe");
  await page.getByLabel(/category/i).selectOption({ label: "General" });
  await page.getByLabel(/priority/i).selectOption("high");
  await page.getByRole("button", { name: /create ticket/i }).click();
  // Creation currently redirects to /tickets list (slice 1); detail lands later. Follow the list link to detail.
  await expect(page).toHaveURL(/\/tickets/);
  await expect(page.getByText(title)).toBeVisible({ timeout: 10_000 });
  const href = await page.getByText(title).first().getAttribute("href");
  // Ticket list renders title as link to /tickets/{id}; fallback: first TKT link in same row
  let id: string | null = null;
  if (href) {
    const m = href.match(/\/tickets\/(\d+)/);
    if (m) id = m[1];
  }
  if (!id) {
    const link = page.locator('a[href*="/tickets/"]').first();
    const h = await link.getAttribute("href");
    const m = h?.match(/\/tickets\/(\d+)/);
    if (m) id = m[1];
  }
  if (!id) throw new Error("could not extract ticket id for " + title + " at " + page.url());
  // Navigate to detail for caller convenience (caller may goto again anyway)
  await page.goto(base() + `/tickets/${id}`);
  await expect(page.locator("#ticket-detail")).toBeVisible({ timeout: 10_000 });
  return id;
}

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
    const id = await createTicket(page, title);

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
    const id = await createTicket(page, title);

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
    const cancelId = await createTicket(page, cancelTitle);
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
    const id = await createTicket(page, "HTMX probe " + Date.now());
    await page.goto(base() + `/tickets/${id}`);
    // Title inline edit form, priority select, assign select, transition, comment form all carry hx-post / hx-target
    const hxForms = page.locator('[hx-post][hx-target="#ticket-detail"]');
    await expect(hxForms.first()).toBeVisible();
    // At least the priority control has hx-post
    await expect(page.locator('form[hx-post*="/edit"]').first()).toBeVisible();
  });
});
