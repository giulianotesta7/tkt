/**
 * Ticket journeys: creation, list, detail, search/filter, public comment, transition.
 *
 * Logout and auth gate are covered in auth.spec.ts (auth domain).
 * Search filter is the canonical HTMX swap test for #ticket-list (this file).
 * Public comment is the canonical comment journey (this file).
 * Transitions use assertHtmxSwap for swap verification.
 * htmx.spec.ts covers transversal swaps only (users tabs, workflow builder).
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";
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

    // Create a uniquely titled ticket for search, plus a distractor ticket
    // so the full list (2+ tickets) is visibly different from the filtered result (1).
    // Without the distractor the filtered HTML is identical to the full list.
    const uniqueTitle = "FilterProbe " + Date.now().toString(36).slice(2, 10);
    const distractorTitle = "Distractor " + Date.now().toString(36).slice(2, 10);
    await createTicketViaUi(page, { title: uniqueTitle, description: "filter probe", category: "General", priority: "low" });
    await createTicketViaUi(page, { title: distractorTitle, description: "filter distractor", category: "General", priority: "low" });

    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible();
    await expect(page.locator("#ticket-list").getByText(distractorTitle)).toBeVisible();

    const searchInput = page.getByPlaceholder(/search by id or title/i);
    await expect(searchInput).toBeVisible();

    // 1. Search for the unique title — filtered result visible.
    // Fill and submit inside the trigger so the interceptor is armed before
    // the form's HTMX GET is dispatched.
    await assertHtmxSwap(page, async () => {
      await searchInput.fill(uniqueTitle);
      await page.getByRole("button", { name: /search/i }).click();
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === "/tickets" && parsedURL.searchParams.get("q") === uniqueTitle;
      },
      method: "GET",
      expectedStatus: 200,
      hxTarget: "#ticket-list",
    });
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({ timeout: 10_000 });
    expect(new URL(page.url()).pathname).toBe("/tickets");

    // 2. Search for an impossible term — empty state
    const impossibleTerm = "zzz_no_match_" + Date.now().toString(36).replace(/[0-9]/g, "x");
    await assertHtmxSwap(page, async () => {
      await searchInput.fill(impossibleTerm);
      await page.getByRole("button", { name: /search/i }).click();
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === "/tickets" && parsedURL.searchParams.get("q") === impossibleTerm;
      },
      method: "GET",
      expectedStatus: 200,
      hxTarget: "#ticket-list",
    });
    await expect(page.locator("#ticket-list").getByText(/no tickets match/i)).toBeVisible();
    expect(new URL(page.url()).pathname).toBe("/tickets");

    // 3. Clear the search — full list reappears
    await assertHtmxSwap(page, async () => {
      await searchInput.clear();
      await page.getByRole("button", { name: /search/i }).click();
    }, {
      endpoint: "/tickets",
      method: "GET",
      expectedStatus: 200,
      hxTarget: "#ticket-list",
    });
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({ timeout: 10_000 });
    expect(new URL(page.url()).pathname).toBe("/tickets");

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
    // Comment form is a native POST (no hx-post) — submit and wait for navigation
    const responsePromise = waitForExactPost(page, `/tickets/${id}/comments`);
    await page.getByRole("button", { name: /add comment/i }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(303);
    expect(new URL(page.url()).pathname).toBe(`/tickets/${id}`);
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
      endpoint: `/tickets/${id}/transition`,
      method: "POST",
      expectedStatus: 200,
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
