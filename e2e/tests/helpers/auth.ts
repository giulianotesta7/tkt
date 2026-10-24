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
  // Since issue #377 Log out lives in the user menu at the foot of the sidebar.
  const toggle = page.locator("[data-user-menu-toggle]");
  await expect(toggle).toBeVisible();
  await toggle.click();
  const btn = page.locator("#user-menu").getByRole("menuitem", { name: /log out|sign out/i });
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
  await assertHtmxSwap(
    page,
    async () => {
      await page.getByRole("button", { name: /create user/i }).click();
    },
    {
      endpoint: "/users",
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#users-root",
      expectedUrl: /\/users$/,
    },
  );
  return opts.email;
}

/**
 * Give one existing user the role a journey needs. The new-user form creates a
 * requester account only, so every staff fixture becomes an agent or an
 * administrator the way an operator does it: on that user's own page, where the
 * role is a stored field.
 */
export async function grantUserRole(
  page: Page,
  name: string,
  role: "agent" | "admin",
): Promise<void> {
  await page.goto(base() + "/users");
  const row = page
    .locator("tr[data-user-name]")
    .filter({ has: page.getByText(name, { exact: true }) });
  await expect(row, `Expected exactly one user row for "${name}" at ${page.url()}`).toHaveCount(1);
  const launcher = row.locator('a.user-launcher, a[href*="/users/"][href*="/edit"]').first();
  const href = await launcher.getAttribute("href");
  if (!href) throw new Error(`No edit href found for user "${name}" at ${page.url()}`);
  await page.goto(base() + href.split("?")[0]);
  await page.locator('select[name="role"]').selectOption(role);
  await page.getByRole("button", { name: /save changes/i }).click();
  await expect(page).toHaveURL(/\/users$/);
}

export async function loginAs(page: Page, email: string, password: string): Promise<void> {
  await page.goto(base() + "/login");
  await page.getByLabel(/email/i).fill(email);
  await page.getByLabel(/password/i).fill(password);
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}

/**
 * Toggle the instance-wide SLA switch as the logged-in administrator and
 * prove the persisted value survives a reload.
 *
 * SLA is instance-wide and shared with the tickets created afterwards: an
 * SLA visibility journey MUST restore the previous state (disable it) when
 * it finishes, or a later journey in the same database inherits a frozen
 * commitment it never arranged. The remaining SLA panel fields are left at
 * their current values, so the POST re-submits the stored matrix unchanged;
 * check()/uncheck() are idempotent, so the helper does not depend on the
 * state it starts from.
 */
export async function setSLAEnabled(page: Page, enabled: boolean): Promise<void> {
  // Since issue #383 the SLA panel lives in the Tickets & SLA section, and its
  // save bar stays hidden until the form is dirty. The switch is the only
  // control this helper owns, so when it already holds the desired state the
  // helper flips it twice: the state is unchanged and the bar is revealed.
  await page.goto(base() + "/settings/tickets");
  const toggle = page.getByRole("checkbox", { name: /enable sla targets/i });
  await expect(toggle).toBeVisible();
  if ((await toggle.isChecked()) === enabled) {
    await toggle.click();
  }
  if (enabled) {
    await toggle.check();
  } else {
    await toggle.uncheck();
  }
  const save = page.getByRole("button", { name: "Save SLA settings" });
  await expect(save).toBeVisible();
  await save.click();
  await expect(page).toHaveURL(/\/settings\/tickets$/);
  await expect(page.locator("#save-feedback")).toContainText("Saved");
  await page.reload();
  if (enabled) {
    await expect(toggle).toBeChecked();
  } else {
    await expect(toggle).not.toBeChecked();
  }
}

/**
 * Choose how the instance reaches the category of a new ticket (issue #391,
 * S1). The choice is instance-wide and shared with every later creation, so a
 * journey that needs two-step mode MUST restore single-page mode when it
 * finishes, or a later journey in the same database inherits a flow it never
 * arranged. It proves the save lands back on the section with its own feedback,
 * and that the stored choice survives a reload.
 */
export async function setTicketCreationMode(
  page: Page,
  mode: "single" | "two_step",
): Promise<void> {
  await page.goto(base() + "/settings/tickets");
  const choice = page.getByRole("radio", {
    name: mode === "single" ? /^single page$/i : /^two steps$/i,
  });
  await expect(choice).toBeVisible();
  if (await choice.isChecked()) {
    // Nothing to save: the section's save bar stays hidden until its form is
    // dirty, and the stored choice is already the one requested.
    return;
  }
  await choice.check();
  const save = page.getByRole("button", { name: "Save creation flow" });
  await expect(save).toBeVisible();
  await save.click();
  await expect(page).toHaveURL(/\/settings\/tickets$/);
  await expect(page.locator("#save-feedback")).toContainText("Saved");
  await page.reload();
  await expect(choice).toBeChecked();
}
