/**
 * Settings (appearance) journeys.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";

test.describe("Settings appearance", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("renders appearance panel with three colors and persists selection", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.getByText(/instance appearance/i)).toBeVisible();
    await expect(page.locator("h2").filter({ hasText: "Appearance" })).toBeVisible();

    const radios = page.locator('input[name="internal_comment_bg"]');
    await expect(radios).toHaveCount(3);
    await expect(page.locator(".swatch-label").filter({ hasText: "Blue" })).toBeVisible();
    await expect(page.locator(".swatch-label").filter({ hasText: "Violet" })).toBeVisible();
    await expect(page.locator(".swatch-label").filter({ hasText: "Yellow" })).toBeVisible();

    // One is checked by default
    await expect(radios.filter({ hasAttribute: "checked" })).not.toHaveCount(0);

    // Switch to Violet and save (chip overlays input, so click label)
    await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#EFE9FB"]')).toBeChecked();
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.locator('input[name="internal_comment_bg"][value="#EFE9FB"]')).toBeChecked();

    // Switch back to Blue for determinism
    await page.locator('label.swatch:has(input[value="#E8EEFF"])').click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#E8EEFF"]')).toBeChecked();
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#E8EEFF"]')).toBeChecked();

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
  });

  test("HTMX-like: appearance form posts and redirects without console errors", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings");
    const form = page.locator('form[action="/settings/appearance"]');
    await expect(form).toBeVisible();
    await expect(form).toHaveAttribute("method", "post");
    // Save again to ensure no 500
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.locator(".error-banner")).toHaveCount(0);
  });
});
