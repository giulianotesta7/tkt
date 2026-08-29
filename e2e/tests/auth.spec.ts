/**
 * Auth journeys: First-user setup, login, /setup with existing users, / redirect, auth gate.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
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
    await expect(page.getByText("No tickets match your filters.")).toBeVisible();
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

  test("/ redirects to /tickets when authenticated and to /login when anonymous", async ({ page }) => {
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
});
