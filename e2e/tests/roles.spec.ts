/**
 * Role-scoped behavior: admin vs member views via seeded data.
 *
 * Seeded alice is root (admin+). We create a user-role operator via /users/new
 * and verify capability gates: user cannot reach managed screens, can create
 * tickets and add public comments but not internal or manage categories/desks/users/settings.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base, seededCredentials } from "./helpers/auth.js";

test.describe("Role-scoped behavior", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("admin can reach managed screens, member (user role) is forbidden", async ({ page }) => {
    await loginAsSeeded(page);
    const uname = "Member Mia " + Date.now();
    const uemail = `mia-${Date.now()}@example.com`;
    const upass = "Secret123!";
    // Create a user-role operator as admin
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(uname);
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill(upass);
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users/);

    // Admin can reach managed screens
    for (const path of ["/users", "/desks", "/categories", "/settings"]) {
      await page.goto(base() + path);
      await expect(page).not.toHaveURL(/\/login/);
      expect(page.url()).toContain(path);
    }

    // Log out admin, log in as member
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await page.getByLabel(/email/i).fill(uemail);
    await page.getByLabel(/password/i).fill(upass);
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    // Member is forbidden from managed screens (403)
    for (const path of ["/users", "/desks", "/categories", "/settings"]) {
      const resp = await page.request.get(base() + path, {
        headers: { Cookie: (await page.context().cookies()).map(c => `${c.name}=${c.value}`).join("; ") },
      });
      expect(resp.status(), `${path} should be forbidden for user role`).toBe(403);
    }

    // But member can still create a ticket (any authenticated role)
    await page.goto(base() + "/tickets/new");
    await expect(page.locator("h2")).toContainText(/ticket details/i);
    await expect(page.getByLabel(/category/i)).toBeVisible();

    // Member cannot see internal comment checkbox (agent+ only)
    // Create a ticket as member then check detail
    const title = "Role probe " + Date.now();
    await page.getByLabel(/title/i).fill(title);
    await page.getByLabel(/description/i).fill("role test");
    await page.getByLabel(/category/i).selectOption({ label: "General" });
    await page.getByLabel(/priority/i).selectOption("low");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.getByText(title).first()).toBeVisible();
    const href = await page.getByText(title).first().getAttribute("href");
    const mm = href?.match(/\/tickets\/(\d+)/);
    await page.goto(base() + `/tickets/${mm![1]}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByLabel(/internal comment/i)).toHaveCount(0);
    // Public comment should still be allowed (form present)
    await expect(page.getByLabel(/comment body/i)).toBeVisible();

    // Restore admin session for following tests (log out member, log in admin)
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await page.getByLabel(/email/i).fill(seededCredentials.email);
    await page.getByLabel(/password/i).fill(seededCredentials.password);
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
  });

  test("user-role internal comment is rejected server-side", async ({ page }) => {
    await loginAsSeeded(page);
    const uemail = `forbid-${Date.now()}@example.com`;
    const upass = "Secret123!";
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill("Forbid User " + Date.now());
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill(upass);
    await page.getByRole("button", { name: /create user/i }).click();
    await page.getByRole("button", { name: /log out|sign out/i }).click();
    await page.getByLabel(/email/i).fill(uemail);
    await page.getByLabel(/password/i).fill(upass);
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    // Create ticket as user
    const forbidTitle = "Forbid comment " + Date.now();
    await page.goto(base() + "/tickets/new");
    await page.getByLabel(/title/i).fill(forbidTitle);
    await page.getByLabel(/description/i).fill("x");
    await page.getByLabel(/category/i).selectOption({ label: "General" });
    await page.getByLabel(/priority/i).selectOption("low");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page).toHaveURL(/\/tickets/);
    await expect(page.getByText(forbidTitle).first()).toBeVisible();
    const fhref = await page.getByText(forbidTitle).first().getAttribute("href");
    const m = fhref?.match(/\/tickets\/(\d+)/);
    const id = m![1];
    const resp = await page.request.post(base() + `/tickets/${id}/comments`, {
      form: { body: "internal attempt", visibility: "internal", internal: "1" },
      headers: { Cookie: (await page.context().cookies()).map(c => `${c.name}=${c.value}`).join("; ") },
    });
    expect([403].includes(resp.status()), `expected 403 got ${resp.status()} body ${(await resp.text()).slice(0,400)}`).toBeTruthy();
  });
});
