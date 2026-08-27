/**
 * Critical journey: Login → create ticket using pre-seeded category →
 * verify in list and detail.
 *
 * Why this journey:
 * - Ticket creation requires a published workflow, which the seed creates.
 * - The ticket must appear in the index and open to its detail page,
 *   covering the ticket-management and ticket-workflow-execution specs.
 *
 * The global setup seeds the database with:
 *   - Root user "Alice Admin"
 *   - Category "General" with a published workflow
 *   - Desk "General Support"
 */

import { test, expect } from "@playwright/test";

test.describe("Ticket Lifecycle", () => {
  test("create ticket using seeded category and verify in list and detail", async ({ page }) => {
    // ── Login ─────────────────────────────────────────────────────────
    await page.goto("/login");
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    // ── Create ticket ─────────────────────────────────────────────────
    await page.goto("/tickets/new");
    await expect(page.locator("h2")).toHaveText(/ticket details/i);

    const title = "Login issue " + Date.now();
    await page.getByLabel(/title/i).fill(title);
    await page.getByLabel(/description/i).fill("Cannot log in");

    // The seeded "General" category has a published workflow
    await page.getByLabel(/category/i).selectOption({ label: "General" });
    await page.getByLabel(/priority/i).selectOption("high");
    await page.getByRole("button", { name: /create ticket/i }).click();

    // After creation, the server redirects to the ticket detail page
    await expect(page.getByText(title)).toBeVisible({ timeout: 5000 });

    // Verify priority and state are shown
    await expect(page.getByRole("cell", { name: "High" })).toBeVisible();
    await expect(page.locator(".badge.new")).toBeVisible();

    // ── Verify ticket in the index ────────────────────────────────────
    await page.goto("/tickets");
    await expect(page.getByText(title)).toBeVisible({ timeout: 5000 });

    // ── Open ticket detail from index ─────────────────────────────────
    await page.getByText(title).first().click();
    await page.waitForURL(/\/tickets\/\d+/);
    // The title is displayed as a textbox value on the detail page
    await expect(page.getByText(/TKT-1/)).toBeVisible();
  });

  test("logout and redirect to login", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const logoutBtn = page.getByRole("button", { name: /log out|sign out/i });
    await expect(logoutBtn).toBeVisible();
    await logoutBtn.click();
    await expect(page).toHaveURL(/\/login/, { timeout: 5000 });

    await page.goto("/tickets");
    await expect(page).toHaveURL(/\/login/);
  });
});