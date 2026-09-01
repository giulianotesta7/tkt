import { expect, type Page } from "@playwright/test";
import { activeServer } from "../../server-lifecycle.js";
import { assertHtmxSwap } from "./htmx.js";

export function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

export const seededCredentials = {
  email: "alice@example.com",
  password: "SuperSecret42!",
  name: "Alice Admin",
  initials: "AA",
};

export async function loginAsSeeded(page: Page): Promise<void> {
  await page.goto(base() + "/login");
  await expect(page.getByLabel(/email/i)).toBeVisible();
  await page.getByLabel(/email/i).fill(seededCredentials.email);
  await page.getByLabel(/password/i).fill(seededCredentials.password);
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}

export async function logout(page: Page): Promise<void> {
  const btn = page.getByRole("button", { name: /log out|sign out/i });
  await expect(btn).toBeVisible();
  await btn.click();
  await expect(page).toHaveURL(/\/login/);
}

/**
 * Create a user via the managed-users form as the currently logged-in admin.
 * Returns the email used (for later login).
 */
export async function createUserAsAdmin(
  page: Page,
  opts: { name: string; email: string; password: string },
): Promise<string> {
  await page.goto(base() + "/users/new");
  await expect(page.getByRole("heading", { name: "New user", exact: true })).toBeVisible();
  await page.getByLabel(/^name$/i).fill(opts.name);
  await page.getByLabel(/^email$/i).fill(opts.email);
  await page.getByLabel(/^password$/i).fill(opts.password);
  await assertHtmxSwap(page, async () => {
    await page.getByRole("button", { name: /create user/i }).click();
  }, {
    endpoint: "/users",
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#users-root",
    expectedUrl: /\/users$/,
  });
  return opts.email;
}

export async function loginAs(page: Page, email: string, password: string): Promise<void> {
  await page.goto(base() + "/login");
  await page.getByLabel(/email/i).fill(email);
  await page.getByLabel(/password/i).fill(password);
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}
