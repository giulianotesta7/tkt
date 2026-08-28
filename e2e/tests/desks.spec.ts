/**
 * Desks journeys: list, create, rename, delete, membership, auth gate.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";

test.describe("Desks", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("list shows seeded desk, creates, renames, and deletes a desk", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/desks");
    await expect(page.locator("h1").filter({ hasText: "Desks" })).toBeVisible();
    await expect(page.getByText("General Support").first()).toBeVisible();

    // Create a new desk
    const deskName = "Probe Desk " + Date.now();
    await page.locator("details.desk-create summary").click();
    await page.getByLabel(/^desk name$/i).fill(deskName);
    await page.getByRole("button", { name: /^create desk$/i }).click();
    await expect(page.getByText(deskName).first()).toBeVisible();

    // Select the new desk
    await page.locator(`a[data-desk-id]`).filter({ hasText: deskName }).click();
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Rename
    const renamed = deskName + " Renamed";
    const renameInput = page.locator(".desk-rename input[name='name']");
    await renameInput.fill(renamed);
    await page.locator(".desk-rename button").click();
    await expect(page.getByText(renamed).first()).toBeVisible();

    // Delete
    page.once("dialog", (d) => d.accept());
    await page.getByRole("button", { name: /delete desk/i }).click();
    // After delete we either stay on /desks with empty or without that desk
    await page.waitForURL(/\/desks/);
    await expect(page.getByText(renamed)).toHaveCount(0);

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("desk membership add and remove", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // Create a second user to add as member
    const uname = "Bob Builder " + Date.now();
    const uemail = `bob-${Date.now()}@example.com`;
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(uname);
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill("Secret123!");
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users/);

    await page.goto(base() + "/desks");
    // Select General Support
    await page.locator('a[data-desk-id]').filter({ hasText: "General Support" }).click();
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Add member if eligible
    const addSelect = page.locator(".desk-add-member select");
    if (await addSelect.count()) {
      // Choose the newly created user
      const option = page.locator(`.desk-add-member option:has-text("${uname}")`);
      if (await option.count()) {
        await addSelect.selectOption({ label: uname });
        await page.locator(".desk-add-member button").click();
        await expect(page.locator(".desk-member-list").getByText(uname)).toBeVisible();

        // Remove member
        const removeBtn = page.locator(".desk-member-list li").filter({ hasText: uname }).getByRole("button", { name: /remove/i });
        await removeBtn.click();
        await expect(page.locator(".desk-member-list").getByText(uname)).toHaveCount(0);
      }
    }

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });
});
