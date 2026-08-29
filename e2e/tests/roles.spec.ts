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
import { base, seededCredentials } from "./helpers/auth.js";
import { createTicketViaUi } from "./helpers/navigation.js";
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
    const row = page.locator("tr[data-user-name]").filter({ has: page.getByText(opts.name, { exact: true }) });
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
    const saveRespPromise = waitForExactPost(page, href);
    await page.getByRole("button", { name: /save changes/i }).click();
    const saveResp = await saveRespPromise;
    expect(saveResp.status()).toBe(200);
    await page.goto(baseURL() + "/users");
    await expect(page.getByText(opts.name)).toBeVisible();
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

    // Admin: one allowed admin action — create a category
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await login(page, adminEmail, "Secret123!");
    await page.goto(baseURL() + "/categories/new");
    const catName = "AdminCat " + Date.now().toString(36).slice(2, 8);
    await page.getByLabel(/name/i).fill(catName);
    await page.getByRole("button", { name: /create category|save/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.getByText(catName)).toBeVisible();

    // Agent: one allowed operative action — create a ticket (no error, even though not in agent's own list)
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await login(page, agentEmail, "Secret123!");
    const agentTicket = "Agent ticket " + Date.now().toString(36).slice(2, 8);
    await page.goto(baseURL() + "/tickets/new");
    await expect(page.locator("h2")).toContainText(/ticket details/i);
    await page.getByLabel(/title/i).fill(agentTicket);
    await page.getByLabel(/description/i).fill("agent probe");
    await page.getByLabel(/category/i).selectOption({ label: "General" });
    await page.getByLabel(/priority/i).selectOption("low");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator(".error-banner")).toHaveCount(0);
    // Agent admin access forbidden — browser navigation shows error
    for (const path of ["/users", "/categories", "/desks", "/settings"]) {
      await page.goto(baseURL() + path);
      await expect(page.locator("body")).toContainText(/forbidden|not allowed/i, { timeout: 10000 });
      expect(page.url()).toContain(path);
    }
    // Clear session before user (403 page has no logout)
    await page.context().clearCookies();
    await page.goto(baseURL() + "/login");
    await expect(page.getByRole("heading", { name: /sign in to tkt/i })).toBeVisible();

    // User: one allowed action — create a ticket, internal controls hidden, admin forbidden
    await login(page, userEmail, "Secret123!");
    const userTicket = "User ticket " + Date.now().toString(36).slice(2, 8);
    const userTicketId = await createTicketViaUi(page, { title: userTicket, category: "General", priority: "low" });
    await expect(page.getByText(userTicket)).toBeVisible();
    await page.goto(baseURL() + `/tickets/${userTicketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByLabel(/internal comment/i)).toHaveCount(0);
    await expect(page.getByLabel(/comment body/i)).toBeVisible();
    for (const path of ["/users", "/desks", "/categories", "/settings"]) {
      await page.goto(baseURL() + path);
      await expect(page.locator("body")).toContainText(/forbidden|not allowed/i, { timeout: 10000 });
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
