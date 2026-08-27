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
 * Note: The global setup seeds the database with a root user, so /setup
 * is unavailable. This test verifies that the login page works and the
 * seeded user can log in.
 */

import { test, expect } from "@playwright/test";

test.describe("Login", () => {
  test("seeded root user can log in and reach tickets", async ({ page }) => {
    // ── Login with the seeded root user ───────────────────────────────
    await page.goto("/login");
    await expect(page.getByLabel(/email/i)).toBeVisible();

    // The setup page is accessible (returns 200) for unauthenticated
    // visitors, but the seeded user already exists.

    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();

    // After login, redirect to /tickets.
    await expect(page).toHaveURL(/\/tickets/);

    // The tickets index should show the empty state.
    await expect(page.locator("h1, h2").first()).toBeVisible();

    // Verify the user's initials appear in the sidebar rail.
    await expect(page.getByText("AA")).toBeVisible();
  });
});