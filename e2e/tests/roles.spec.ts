/**
 * Role-scoped behavior: minimal matrix with real actors root, admin, agent, user.
 *
 * - root via bootstrap (first-run setup on empty base)
 * - admin with one allowed administrative action (create a category)
 * - agent with one allowed operative action (create ticket) AND admin access forbidden (browser-visible)
 * - user with one allowed action (create ticket), internal controls hidden, admin access forbidden
 *
 * Exhaustive HTTP codes and full matrix remain covered by Go tests (handlers_admin_test, etc.).
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { seededCredentials } from "./helpers/auth.js";
import { createCategoryViaUi, createTicketViaUi } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";

function baseURL(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

async function login(page: import("@playwright/test").Page, email: string, password: string) {
  await page.goto(baseURL() + "/login");
  await page.getByLabel(/email/i).fill(email);
  await page.getByLabel(/password/i).fill(password);
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}

async function createUserAndSetRole(
  page: import("@playwright/test").Page,
  opts: { name: string; email: string; password: string; role?: "user" | "agent" | "admin" },
) {
  await page.goto(baseURL() + "/users/new");
  await page.getByLabel(/^name$/i).fill(opts.name);
  await page.getByLabel(/^email$/i).fill(opts.email);
  await page.getByLabel(/^password$/i).fill(opts.password);
  await page.getByRole("button", { name: /create user/i }).click();
  await expect(page).toHaveURL(/\/users/);
  await expect(page.getByText(opts.name)).toBeVisible();
  if (opts.role && opts.role !== "user") {
    const row = page
      .locator("tr[data-user-name]")
      .filter({ has: page.getByText(opts.name, { exact: true }) });
    await expect(row).toHaveCount(1);
    const editLink = row.locator('a[href*="/users/"][href*="/edit"]').first();
    let href = await editLink.getAttribute("href");
    if (!href) throw new Error("edit href missing for " + opts.name);
    href = href.split("?")[0];
    await page.goto(baseURL() + href);
    await expect(page.getByRole("heading", { name: /edit user/i })).toBeVisible();
    const roleSelect = page.locator('select[name="role"]');
    await expect(roleSelect).toBeVisible();
    await roleSelect.selectOption(opts.role);
    const userID = new URL(href, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!userID) throw new Error(`Could not resolve exact user ID from ${href} at ${page.url()}`);
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /save changes/i }).click();
      },
      {
        endpoint: `/users/${userID}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    const savedRow = page.locator(`tr[data-user-name="${opts.name}"]`);
    await expect(savedRow).toHaveCount(1);
    await expect(savedRow).toContainText(opts.role === "admin" ? "Admin" : "Agent");
  }
  return opts.email;
}

test.describe("Role — root via bootstrap (empty base)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: false });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("first user is root and can access admin screens", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(baseURL() + "/setup");
    await expect(page.getByRole("heading", { name: /set up tkt/i })).toBeVisible();
    const email = "root-bootstrap@example.com";
    await page.getByLabel(/name/i).fill("Root Bootstrap");
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /create account/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await login(page, email, "SuperSecret42!");
    for (const path of ["/users", "/desks", "/categories", "/settings"]) {
      await page.goto(baseURL() + path);
      await expect(page).not.toHaveURL(/\/login/);
      await expect(page.locator("body")).not.toContainText(/forbidden|not allowed/i);
      await expect(page.locator("h1").first()).toBeVisible();
    }
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "root bootstrap admin access",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});

test.describe("Role — minimal matrix admin / agent / user (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("admin / agent / user minimal matrix (browser-visible)", async ({ page }) => {
    test.setTimeout(90000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await login(page, seededCredentials.email, seededCredentials.password);
    const adminEmail = await createUserAndSetRole(page, {
      name: "Admin Adam",
      email: `admin-${Date.now().toString(36).slice(2, 8)}@example.com`,
      password: "Secret123!",
      role: "admin",
    });
    const agentEmail = await createUserAndSetRole(page, {
      name: "Agent Ava",
      email: `agent-${Date.now().toString(36).slice(2, 8)}@example.com`,
      password: "Secret123!",
      role: "agent",
    });
    const userEmail = await createUserAndSetRole(page, {
      name: "User Uma",
      email: `user-${Date.now().toString(36).slice(2, 8)}@example.com`,
      password: "Secret123!",
      role: "user",
    });
    const assignedTitle = "Assigned queue ticket " + Date.now().toString(36).slice(2, 8);
    const assignedID = await createTicketViaUi(page, {
      title: assignedTitle,
      category: "General",
      priority: "low",
    });
    await page.goto(baseURL() + `/tickets/${assignedID}`);
    const assignee = page.locator('select[name="user_id"]');
    await expect(assignee).toHaveCount(1);
    await assertHtmxSwap(
      page,
      async () => {
        await assignee.selectOption({ label: "Agent Ava" });
      },
      {
        endpoint: `/tickets/${assignedID}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    // Admin: one allowed admin action — create a category
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await login(page, adminEmail, "Secret123!");
    const catName = "AdminCat " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, catName);
    await expect(
      page
        .locator(".category-level-categories .category-structure-row strong")
        .filter({ hasText: catName }),
    ).toBeVisible();

    // Agent: one allowed operative action — create a ticket (no error, even though not in agent's own list)
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await login(page, agentEmail, "Secret123!");
    await expect(page.getByRole("heading", { name: "My work", exact: true })).toBeVisible();
    const agentQueue = page.locator("#agent-ticket-list");
    await expect(agentQueue.getByRole("link", { name: assignedTitle, exact: true })).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Assigned to me · 1", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("heading", { name: /Available to claim/ })).toBeVisible();
    await expect(agentQueue.getByRole("button", { name: "Claim ticket", exact: true })).toHaveCount(
      0,
    );
    const agentGrid = agentQueue.locator(".agent-queue-list").first();
    expect(
      await agentGrid.evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length),
    ).toBe(3);
    const assignedCard = agentQueue.locator(".agent-row-assigned").first();
    await expect(assignedCard.locator(".agent-row-open")).toHaveCSS("color", "rgb(49, 94, 255)");
    await expect(assignedCard.locator(".agent-row-meta").first()).toHaveCSS(
      "color",
      "rgba(0, 0, 0, 0.62)",
    );
    await expect(assignedCard.locator("time.card-timestamp")).toHaveAttribute("tabindex", "0");
    await expect(assignedCard.locator("time.card-timestamp")).toHaveAttribute(
      "data-full-date",
      /\d{2}:\d{2} · \d{2}-\d{2}-\d{4}/,
    );
    await page.setViewportSize({ width: 800, height: 900 });
    expect(
      await agentGrid.evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length),
    ).toBe(2);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      await agentGrid.evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length),
    ).toBe(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      390,
    );
    await page.setViewportSize({ width: 1280, height: 800 });
    const agentTicket = "Agent ticket " + Date.now().toString(36).slice(2, 8);
    await page.goto(baseURL() + "/tickets/new");
    await page.locator(".catalog-category").filter({ hasText: "General" }).first().click();
    await page.getByLabel(/title/i).fill(agentTicket);
    await page.getByLabel(/description/i).fill("agent probe");
    await page.getByLabel(/priority/i).selectOption("low");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator(".error-banner")).toHaveCount(0);
    // Agent admin access forbidden — browser navigation shows error
    for (const path of ["/users", "/categories", "/desks", "/settings"]) {
      await page.goto(baseURL() + path);
      await expect(page.locator("body")).toContainText(/forbidden|not allowed/i, {
        timeout: 10000,
      });
      expect(page.url()).toContain(path);
    }
    // Clear session before user (403 page has no logout)
    await page.context().clearCookies();
    await page.goto(baseURL() + "/login");
    await expect(page.getByRole("heading", { name: /sign in to tkt/i })).toBeVisible();

    // User: one allowed action — create a ticket, internal controls hidden, admin forbidden
    await login(page, userEmail, "Secret123!");
    const userTicket = "User ticket " + Date.now().toString(36).slice(2, 8);
    const userTicketId = await createTicketViaUi(page, {
      title: userTicket,
      category: "General",
      priority: "low",
    });
    await expect(page.getByText(userTicket)).toBeVisible();
    const listScreen = page.locator("#tickets-screen");
    // The page title stays outside #tickets-screen: the metrics summary sits
    // between them and is never part of an HX list swap. Only the swapped region
    // carries the live count, so the subtitle is what must track the fragment.
    await expect(page.getByRole("heading", { name: "My tickets" })).toBeVisible();
    await expect(listScreen.locator(".page-subtitle")).toHaveText("1 ticket");
    const userCard = listScreen
      .locator(".user-request-card")
      .filter({ has: page.getByText(userTicket, { exact: true }) });
    await expect(userCard).toHaveCount(1);
    await expect(userCard.locator(".badge.new")).toHaveText("Received");
    await expect(userCard.getByRole("link", { name: "View request" })).toBeVisible();
    await expect(userCard.locator(".user-request-meta")).toContainText("TKT-");
    await expect(userCard).not.toContainText(/priority|assignee|requester|state/i);
    await expect(listScreen.locator("table")).toHaveCount(0);
    const userGrid = listScreen.locator(".user-ticket-grid");
    expect(
      await userGrid.evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length),
    ).toBe(3);
    await expect(userCard.getByRole("link", { name: "View request" })).toHaveCSS(
      "color",
      "rgb(49, 94, 255)",
    );
    await expect(userCard.locator(".user-request-meta")).toHaveCSS("color", "rgba(0, 0, 0, 0.62)");
    await expect(userCard.locator("time.card-timestamp")).toHaveAttribute("tabindex", "0");
    await expect(userCard.locator("time.card-timestamp")).toHaveAttribute(
      "data-full-date",
      /\d{2}:\d{2} · \d{2}-\d{2}-\d{4}/,
    );
    await page.setViewportSize({ width: 800, height: 900 });
    expect(
      await userGrid.evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length),
    ).toBe(2);
    await page.setViewportSize({ width: 390, height: 800 });
    expect(
      await userGrid.evaluate((el) => getComputedStyle(el).gridTemplateColumns.split(" ").length),
    ).toBe(1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      390,
    );
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(baseURL() + `/tickets/${userTicketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByLabel(/internal comment/i)).toHaveCount(0);
    await expect(page.getByLabel(/comment body/i)).toBeVisible();
    for (const path of ["/users", "/desks", "/categories", "/settings"]) {
      await page.goto(baseURL() + path);
      await expect(page.locator("body")).toContainText(/forbidden|not allowed/i, {
        timeout: 10000,
      });
    }

    // Filter expected 403 console errors from intentional forbidden navigations
    const filteredConsole = obs.consoleErrors.filter((m) => !m.includes("403"));
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "roles admin/agent/user matrix",
      url: page.url(),
      role: "matrix",
      consoleErrors: filteredConsole,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("user cannot see internal comment checkbox but can add public comment", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    // Use the seeded root to create a fresh user for this isolated test
    await login(page, seededCredentials.email, seededCredentials.password);
    const uEmail = `user2-${Date.now().toString(36).slice(2, 8)}@example.com`;
    await createUserAndSetRole(page, {
      name: "User Two",
      email: uEmail,
      password: "Secret123!",
      role: "user",
    });
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await login(page, uEmail, "Secret123!");
    const title = "User public comment " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, category: "General", priority: "low" });
    await page.goto(baseURL() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByLabel(/internal comment/i)).toHaveCount(0);
    const comment = "User public " + Date.now().toString(36).slice(2, 6);
    await page.getByLabel(/comment body/i).fill(comment);
    const commentResponsePromise = waitForExactPost(page, `/tickets/${id}/comments`);
    await Promise.all([
      commentResponsePromise,
      page.getByRole("button", { name: /add comment/i }).click(),
    ]);
    const commentResponse = await commentResponsePromise;
    expect(commentResponse.status()).toBe(303);
    expect(new URL(page.url()).pathname).toBe(`/tickets/${id}`);
    await expect(page.locator("#timeline")).toContainText(comment);
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "roles user public comment",
      url: page.url(),
      role: "user",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
