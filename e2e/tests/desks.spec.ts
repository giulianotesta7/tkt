/**
 * Desks journeys: list, create, rename, delete, membership.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";

test.describe("Desks", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("list shows seeded desk, creates, renames, and deletes a desk", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/desks");
    await expect(page.locator("h1").filter({ hasText: "Desks" })).toBeVisible();
    await expect(page.getByText("General Support").first()).toBeVisible();

    const deskName = "Probe Desk " + Date.now();
    await page.locator("details.desk-create summary").click();
    await expect(page.getByLabel(/^desk name$/i)).toBeVisible();
    await page.getByLabel(/^desk name$/i).fill(deskName);
    // Create desk — wait for POST to complete
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/desks") && r.request().method() === "POST"),
      page.getByRole("button", { name: /^create desk$/i }).click(),
    ]);
    await expect(page.getByText(deskName).first()).toBeVisible();

    // Select the new desk
    await page.locator(`a[data-desk-id]`).filter({ hasText: deskName }).click();
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Rename — assert persistence by checking list updates
    const renamed = deskName + " Renamed";
    const renameInput = page.locator(".desk-rename input[name='name']");
    await expect(renameInput).toBeVisible();
    await renameInput.fill(renamed);
    await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/desks/`) && r.request().method() === "POST"),
      page.locator(".desk-rename button").click(),
    ]);
    await expect(page.getByText(renamed).first()).toBeVisible();
    // Persistence: reload and verify renamed persists
    await page.reload();
    await expect(page.getByText(renamed).first()).toBeVisible();
    // Re-select since reload may clear selection
    await page.locator(`a[data-desk-id]`).filter({ hasText: renamed }).click();
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Delete — must execute and verify persistence
    page.once("dialog", (d) => d.accept());
    await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/desks/`) && r.request().method() === "POST"),
      page.getByRole("button", { name: /delete desk/i }).click(),
    ]);
    await expect(page).toHaveURL(/\/desks/);
    await expect(page.getByText(renamed)).toHaveCount(0);
    // Persistence: reload and still gone
    await page.reload();
    await expect(page.getByText(renamed)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "desks create/rename/delete",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("desk membership add and remove must actually change membership", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const uname = "Bob Builder " + Date.now().toString(36).slice(2, 6);
    const uemail = `bob-${Date.now().toString(36).slice(2, 6)}@example.com`;
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(uname);
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill("Secret123!");
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users/);
    await expect(page.getByText(uname)).toBeVisible();
    // Desk members must be agent+ — promote to agent
    {
      const row = page.locator("tr").filter({ hasText: uname });
      await expect(row).toHaveCount(1);
      const editLink = row.locator('a[href*="/users/"][href*="/edit"]').first();
      let href = await editLink.getAttribute("href");
      if (!href) throw new Error("edit href missing for " + uname);
      href = href.split("?")[0];
      await page.goto(base() + href);
      await expect(page.locator('h1:has-text("Edit user"), h2:has-text("Edit user")').first()).toBeVisible();
      const roleSelect = page.locator('select[name="role"]');
      await expect(roleSelect).toBeVisible();
      await roleSelect.selectOption("agent");
      await Promise.all([
        page.waitForResponse((r) => r.url().includes(href) && r.request().method() === "POST").catch(() => {}),
        page.getByRole("button", { name: /save changes/i }).click(),
      ]);
      await page.goto(base() + "/users");
      await expect(page.getByText(uname)).toBeVisible();
    }

    await page.goto(base() + "/desks");
    await page.locator('a[data-desk-id]').filter({ hasText: "General Support" }).click();
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Membership add — must execute unconditionally (no silent skip)
    const addSelect = page.locator(".desk-add-member select");
    await expect(addSelect).toBeVisible();
    const option = page.locator(`.desk-add-member option:has-text("${uname}")`);
    await expect(option).toBeAttached();
    await addSelect.selectOption({ label: uname });
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/members") && r.request().method() === "POST"),
      page.locator(".desk-add-member button").click(),
    ]);
    await expect(page.locator(".desk-member-list").getByText(uname)).toBeVisible();
    // Persistence check: reload still shows member
    await page.reload();
    // After reload, selection resets — re-select desk
    await page.locator('a[data-desk-id]').filter({ hasText: "General Support" }).click();
    await expect(page.locator(".desk-member-list").getByText(uname)).toBeVisible({ timeout: 10_000 });

    // Remove member — must actually disappear
    const removeBtn = page.locator(".desk-member-list li").filter({ hasText: uname }).getByRole("button", { name: /remove/i });
    await expect(removeBtn).toBeVisible();
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/members") && r.request().method() === "POST"),
      removeBtn.click(),
    ]);
    await expect(page.locator(".desk-member-list").getByText(uname)).toHaveCount(0);
    await page.reload();
    await page.locator('a[data-desk-id]').filter({ hasText: "General Support" }).click();
    await expect(page.locator(".desk-member-list").getByText(uname)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "desks membership",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
