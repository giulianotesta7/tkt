/**
 * Critical journey: First-user setup → login → tickets overview.
 *
 * Why this journey:
 * - It is the only bootstrap path for a new instance.
 * - Protects the branded auth contract (auth-entry-experience spec).
 * - The first user is created as `root`, which gates every management
 *   capability.
 * - A broken setup/login blocks every other E2E journey.
 *
 * This test starts its OWN isolated tkt server with an empty SQLite DB,
 * so the /setup endpoint is available and the first-user bootstrap flow
 * is exercised from scratch.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";

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
    // ── /login redirects to /setup when no users exist ────────────────
    await page.goto(base() + "/login");
    await expect(page).toHaveURL(/\/setup/);

    // ── Fill and submit the setup form ────────────────────────────────
    const name = "Alice Admin";
    const email = "alice@example.com";
    const password = "SuperSecret42!";

    await page.getByLabel(/name/i).fill(name);
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill(password);
    await page.getByRole("button", { name: /create account|set up|sign up|create/i }).click();

    // After creation, the server redirects to /login
    await expect(page).toHaveURL(/\/login/);

    // ── Login with the newly created credentials ──────────────────────
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).fill(password);
    await page.getByRole("button", { name: /log in|sign in/i }).click();

    // After login, redirect to /tickets
    await expect(page).toHaveURL(/\/tickets/);

    // Tickets page should show empty state
    await expect(page.locator("h1, h2").first()).toBeVisible();

    // Verify the user's initials appear in the sidebar rail
    await expect(page.getByText("AA")).toBeVisible();
  });
});

test.describe("Login", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });

  test.afterAll(async () => {
    await stopServer();
  });

  test("seeded root user can log in and reach tickets empty state", async ({ page }) => {
    await page.goto(base() + "/login");
    await expect(page.getByLabel(/email/i)).toBeVisible();

    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();

    // After login, redirect to /tickets
    await expect(page).toHaveURL(/\/tickets/);

    // Tickets page should show empty state
    await expect(page.locator("h1, h2").first()).toBeVisible();

    // Verify the user's initials appear in the sidebar rail
    await expect(page.getByText("AA")).toBeVisible();
  });
});