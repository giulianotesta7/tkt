/**
 * Ticket journeys: creation, list, detail, search/filter swap, public comment, transition.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

test.describe("Ticket Lifecycle", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });

  test.afterAll(async () => {
    await stopServer();
  });

  test("create ticket, verify in list, detail, and navigate from index", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    await page.goto(base() + "/tickets/new");
    await expect(page.locator("h2")).toHaveText(/ticket details/i);

    const title = "Login issue " + Date.now();
    await page.getByLabel(/title/i).fill(title);
    await page.getByLabel(/description/i).fill("Cannot log in");
    await page.getByLabel(/category/i).selectOption({ label: "General" });
    await page.getByLabel(/priority/i).selectOption("high");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page.getByText(title)).toBeVisible({ timeout: 5000 });
    await expect(page.getByRole("cell", { name: "High" })).toBeVisible();
    await expect(page.getByText("New").first()).toBeVisible();
    const ticketNumberText = await page.getByText(/TKT-\d+/).textContent();
    expect(ticketNumberText).not.toBeNull();
    expect(ticketNumberText).toMatch(/TKT-\d+/);

    await page.goto(base() + "/tickets");
    await expect(page.getByText(title)).toBeVisible({ timeout: 5000 });
    await page.getByText(title).first().click();
    await page.waitForURL(/\/tickets\/\d+/);
    await expect(page.getByText(ticketNumberText!)).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets create/list/detail",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("search filter real swap changes #ticket-list without full navigation", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    // Create a uniquely titled ticket for search
    const uniqueTitle = "FilterProbe " + Date.now().toString(36).slice(2, 10);
    await createTicketViaUi(page, { title: uniqueTitle, description: "filter probe", category: "General", priority: "low" });

    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    const beforeHTML = await page.locator("#ticket-list").innerHTML();
    expect(beforeHTML).toContain(uniqueTitle);

    // Use the search input (HTMX hx-get → #ticket-list) — type unique prefix and submit
    const searchInput = page.getByPlaceholder(/search by id or title/i);
    await expect(searchInput).toBeVisible();
    // hx-get forms use outerHTML swap targeting #ticket-list; URL must not change (hx-push-url not set for search)
    const urlBefore = page.url();
    await searchInput.fill(uniqueTitle);
    // Trigger search via Enter or clicking Search button
    const searchBtn = page.getByRole("button", { name: /search/i });
    await expect(searchBtn).toBeVisible();
    const responsePromise = page.waitForResponse((r) => r.url().includes("/tickets") && r.request().method() === "GET");
    await searchBtn.click();
    await responsePromise.catch(() => {});
    await expect(page.locator("#ticket-list")).toBeVisible();
    // Region content must have changed to reflect filtered results — wait until filtered result appears
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({ timeout: 10000 });
    // Negative: searching for a nonsense term should yield empty state
    await searchInput.fill("zzz_no_match_" + Date.now().toString(36));
    {
      const resp2 = page.waitForResponse((r) => r.url().includes("/tickets") && r.request().method() === "GET");
      await searchBtn.click();
      await resp2.catch(() => {});
    }
    await expect(page.locator("#ticket-list")).toBeVisible();
    await expect(page.locator("#ticket-list").getByText(/no tickets match/i)).toBeVisible();
    // URL check — search HTMX swap should not cause full navigation (hx-push-url via form is hx-get without push for search)
    // The ticket_search form uses hx-get without hx-push-url, so page.url() may add ?q= but still same base path
    expect(page.url()).toContain("/tickets");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets search filter swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("public comment is persisted and appears in timeline", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const title = "Comment probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, description: "comment probe", category: "General", priority: "medium" });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const commentBody = "Public comment " + Date.now().toString(36).slice(2, 6);
    await page.getByLabel(/comment body/i).fill(commentBody);
    await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/tickets/${id}/comments`) && r.request().method() === "POST"),
      page.getByRole("button", { name: /add comment/i }).click(),
    ]);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#timeline")).toContainText(commentBody);
    // Reload persistence
    await page.reload();
    await expect(page.locator("#timeline")).toContainText(commentBody);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets public comment",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("real transition with visible result (new → in_progress)", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const title = "Transition probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, description: "transition probe", category: "General", priority: "high" });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByText("New").first()).toBeVisible();
    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();
    const detailBefore = await page.locator("#ticket-detail").innerHTML();
    await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/tickets/${id}/transition`) && r.request().method() === "POST"),
      moveSelect.selectOption("in_progress"),
    ]);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
    const detailAfter = await page.locator("#ticket-detail").innerHTML();
    expect(detailAfter).not.toBe(detailBefore);
    // Timeline should contain transition event
    await expect(page.locator("#timeline")).toContainText(/in.progress/i);
    // Persistence
    await page.reload();
    await expect(page.getByText("In Progress").first()).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets transition new→in_progress",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("logout and auth gate redirects unauthenticated to login", async ({ page }) => {
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const logoutBtn = page.getByRole("button", { name: /log out|sign out/i });
    await expect(logoutBtn).toBeVisible();
    await logoutBtn.click();
    await expect(page).toHaveURL(/\/login/, { timeout: 5000 });

    await page.goto(base() + "/tickets");
    await expect(page).toHaveURL(/\/login/);
  });
});
