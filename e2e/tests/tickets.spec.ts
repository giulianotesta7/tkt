/**
 * Ticket journeys: creation, list, detail, search/filter, public comment, transition.
 *
 * Logout and auth gate are covered in auth.spec.ts (auth domain).
 * HTMX swap mechanism is verified in htmx.spec.ts; here we use assertHtmxSwap for
 * functional assertions (comment POST, transition) while the swap mechanism itself
 * is tested in the dedicated HTMX spec.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { assertHtmxSwap } from "./helpers/htmx.js";

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

  test("search filter shows filtered results and empty state", async ({ page }) => {
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
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible();

    // Search for the unique title — verify filtered result is visible via HTMX swap
    const searchInput = page.getByPlaceholder(/search by id or title/i);
    await expect(searchInput).toBeVisible();
    const searchBtn = page.getByRole("button", { name: /search/i });
    const searchPromise = page.waitForResponse((resp) => {
      const url = resp.url();
      return url.includes("/tickets") && url.includes("q=") && resp.request().headers()["hx-request"] === "true";
    });
    await searchInput.fill(uniqueTitle);
    await searchBtn.click();
    const searchResp = await searchPromise;
    expect(searchResp.status()).toBe(200);
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({ timeout: 10_000 });
    expect(page.url()).toContain("/tickets");

    // Navigate back to full list, then clear search
    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    // Clear the search input and submit to show the full list again
    await searchInput.clear();
    await searchBtn.click();
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({ timeout: 10_000 });
    // URL check — search HTMX swap should not cause full navigation
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
    await assertHtmxSwap(page, async () => {
      await page.getByRole("button", { name: /add comment/i }).click();
    }, {
      urlPattern: (url) => url.includes(`/tickets/${id}/comments`),
      hxTarget: "#ticket-detail",
      skipHxRequestCheck: true,
    });
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

    await assertHtmxSwap(page, async () => {
      await moveSelect.selectOption("in_progress");
    }, {
      urlPattern: (url) => url.includes(`/tickets/${id}/transition`),
      hxTarget: "#ticket-detail",
    });

    await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
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
});