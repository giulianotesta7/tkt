/**
 * Auth journeys: First-user setup, login, /setup with existing users, / redirect, auth gate.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { loginAsSeeded } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

test.describe("First-User Setup", () => {
  test.beforeAll(async () => {
    await startServer({ seed: false });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("fresh instance creates root, logs in, and reaches tickets", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await expect(page).toHaveURL(/\/setup/);
    const name = "Alice Admin";
    const email = "alice@example.com";
    const password = "SuperSecret42!";
    await page.getByLabel(/name/i).fill(name);
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill(password);
    await page.getByRole("button", { name: /create account|set up|sign up|create/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill(password);
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.getByText("No tickets yet", { exact: true })).toBeVisible();
    await expect(page.getByText("AA")).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "first-user setup",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});

test.describe("Login and auth gates", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("seeded root user can log in and reach tickets", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.getByText("AA")).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "seeded login",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("/setup when users already exist redirects canonically", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    // Anonymous: /setup → /login (users exist, no session)
    await page.goto(base() + "/setup");
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByRole("heading", { name: /sign in to tkt/i })).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "/setup anonymous with users → /login",
      url: page.url(),
      role: "anonymous",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // Authenticated: /setup → /tickets (users exist, has session)
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    // Fresh observability for authenticated redirect
    const obs2 = collectObservability(page);
    await page.goto(base() + "/setup");
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator('h1:has-text("Tickets")')).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "/setup authenticated with users → /tickets",
      url: page.url(),
      role: "root",
      consoleErrors: obs2.consoleErrors,
      pageErrors: obs2.pageErrors,
      failedRequests: obs2.failedRequests,
      failedResponses: obs2.failedResponses,
    });
  });

  test("/ redirects to /tickets when authenticated and to /login when anonymous", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    // Anonymous: / → /login
    await page.goto(base() + "/");
    await expect(page).toHaveURL(/\/login/);
    const obs = collectObservability(page);
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "/ anonymous → /login",
      url: page.url(),
      role: "anonymous",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // Authenticated: / → /tickets
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    const obs2 = collectObservability(page);
    await page.goto(base() + "/");
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.locator('h1:has-text("Tickets")')).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "/ authenticated → /tickets",
      url: page.url(),
      role: "root",
      consoleErrors: obs2.consoleErrors,
      pageErrors: obs2.pageErrors,
      failedRequests: obs2.failedRequests,
      failedResponses: obs2.failedResponses,
    });
  });

  test("auth gate: unauthenticated /tickets redirects to /login", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    const logoutBtn = page.getByRole("button", { name: /log out|sign out/i });
    await expect(logoutBtn).toBeVisible();
    await logoutBtn.click();
    await expect(page).toHaveURL(/\/login/);
    await page.goto(base() + "/tickets");
    await expect(page).toHaveURL(/\/login/);
  });

  /**
   * Issue #347: a session that dies while a page is open must not leak the login
   * document into the region the next HTMX interaction targets. The gate refuses
   * the HTMX request with HX-Redirect (empty 204, never a 3xx htmx would follow
   * and swap), so htmx performs a full navigation and the user lands on /login.
   */
  test("expired session during an HTMX interaction lands on /login", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await expect(page.locator("#tickets-screen")).toBeVisible();
    await expect(page.locator("#ticket-search")).toBeVisible();

    // The session dies the way expiry does: the cookie the open document would
    // send no longer resolves to a session server-side.
    await page.context().clearCookies();

    const refusal = page.waitForResponse(
      (resp) =>
        resp.request().headers()["hx-request"] === "true" &&
        resp.request().method() === "GET" &&
        new URL(resp.url()).pathname === "/tickets",
    );
    await page.getByPlaceholder(/search by id or title/i).fill("expired session");
    await page.getByRole("button", { name: "Apply", exact: true }).click();

    // The refusal is a redirect instruction, not a document to swap.
    const response = await refusal;
    expect(response.status()).toBe(204);
    expect(response.headers()["hx-redirect"]).toBe("/login");

    // htmx reads HX-Redirect and navigates: the open page is replaced by the
    // login document instead of receiving login markup inside its list region.
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole("heading", { name: /sign in to tkt/i })).toBeVisible();
    await expect(page.locator("#tickets-screen")).toHaveCount(0);
    await expect(page.locator("#ticket-list")).toHaveCount(0);
    await expect(page.locator("#ticket-search")).toHaveCount(0);
  });
});
