/**
 * Settings (appearance) journeys.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";

test.describe("Settings appearance", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("renders appearance panel with three colors and persists selection", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
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

    // Valid assertion for checked radio — exactly one is checked by default
    const checked = page.locator('input[name="internal_comment_bg"]:checked');
    await expect(checked).toHaveCount(1);
    const initialValue = await checked.getAttribute("value");
    expect(initialValue).toBeTruthy();

    // Switch to Violet and save (chip overlays input, so click label)
    await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#EFE9FB"]')).toBeChecked();
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.locator('input[name="internal_comment_bg"][value="#EFE9FB"]')).toBeChecked();
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // Verify persistence survives a reload and the native one-time feedback does not replay.
    await page.reload();
    await expect(page.locator("#save-feedback")).toBeHidden();
    await expect(page.locator('input[name="internal_comment_bg"][value="#EFE9FB"]')).toBeChecked();

    // Switch back to Blue for determinism
    await page.locator('label.swatch:has(input[value="#E8EEFF"])').click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#E8EEFF"]')).toBeChecked();
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#E8EEFF"]')).toBeChecked();
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await expect(page.locator("#save-feedback")).toBeHidden({ timeout: 6000 });
    await page.reload();
    await expect(page.locator('input[name="internal_comment_bg"][value="#E8EEFF"]')).toBeChecked();

    await assertCanonicalScreen(page, {
          viewport: 1280,
          label: "settings appearance persist",
          url: page.url(),
          role: "root",
          consoleErrors: obs.consoleErrors,
          pageErrors: obs.pageErrors,
          failedRequests: obs.failedRequests,
          failedResponses: obs.failedResponses,
        });
      });

      test("success confirmation presents as a fixed top-right toast at desktop and mobile widths", async ({ page }) => {
        test.setTimeout(30_000);
        await loginAsSeeded(page);

        for (const width of [1280, 390]) {
          await page.setViewportSize({ width, height: 844 });
          await page.goto(base() + "/settings");
          await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();

          const focusBefore = await page.evaluate(() => document.activeElement?.tagName ?? "");
          await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
          await page.getByRole("button", { name: /save appearance/i }).click();
          await expect(page).toHaveURL(/\/settings/);

          const toast = page.locator("#save-feedback");
          await expect(toast).toBeVisible();
          await expect(toast).toHaveClass(/(?:^|\s)is-visible(?:\s|$)/);
          await expect(toast).toContainText("Saved");
          await expect(toast).toHaveAttribute("role", "status");
          await expect(toast).toHaveAttribute("aria-live", "polite");
          await expect(toast).toHaveAttribute("aria-atomic", "true");
          await expect(toast.locator(".save-feedback-dismiss")).toBeVisible();

          const geometry = await toast.evaluate((element) => {
            const style = getComputedStyle(element);
            const box = element.getBoundingClientRect();
            return {
              position: style.position,
              top: parseFloat(style.top),
              right: parseFloat(style.right),
              zIndex: parseInt(style.zIndex, 10),
              rectRight: box.right,
              rectTop: box.top,
              viewportWidth: window.innerWidth,
              viewportHeight: window.innerHeight,
              scrollWidth: document.documentElement.scrollWidth,
              bodyScrollWidth: document.body.scrollWidth,
            };
          });
          expect(geometry.position).toBe("fixed");
          expect(geometry.top).toBeGreaterThanOrEqual(0);
          expect(geometry.right).toBeGreaterThanOrEqual(0);
          expect(geometry.rectRight).toBeLessThanOrEqual(geometry.viewportWidth);
          expect(geometry.rectTop).toBeLessThanOrEqual(geometry.viewportHeight);
          expect(geometry.rectTop).toBeGreaterThanOrEqual(0);
          expect(geometry.zIndex).toBeGreaterThan(0);
          expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.viewportWidth);
          expect(geometry.bodyScrollWidth).toBeLessThanOrEqual(geometry.viewportWidth);

          const focusAfter = await page.evaluate(() => document.activeElement?.tagName ?? "");
          expect(focusAfter).toBe(focusBefore);

          // In-flow banner regression guard: the toast must not sit inside the page content column.
          expect(await toast.evaluate((element) => element.closest("main"))).toBeNull();

          await toast.getByRole("button", { name: "Dismiss confirmation" }).click();
          await expect(toast).toBeHidden();

          await page.locator('label.swatch:has(input[value="#E8EEFF"])').click();
          await page.getByRole("button", { name: /save appearance/i }).click();
          await expect(toast).toBeVisible();
          await page.waitForTimeout(5_100);
          await expect(toast).toBeHidden();
        }
      });

      test("reduced motion keeps the toast functional without transitions or transforms", async ({ page }) => {
        test.setTimeout(20_000);
        await page.emulateMedia({ reducedMotion: "reduce" });
        await page.setViewportSize({ width: 1280, height: 800 });
        await loginAsSeeded(page);
        await page.goto(base() + "/settings");
        await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();

        await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
        await page.getByRole("button", { name: /save appearance/i }).click();
        await expect(page).toHaveURL(/\/settings/);

        const toast = page.locator("#save-feedback");
        await expect(toast).toBeVisible();
        await expect(toast).toContainText("Saved");
        const motion = await toast.evaluate((element) => {
          const style = getComputedStyle(element);
          return {
            transitionDuration: style.transitionDuration,
            animationDuration: style.animationDuration,
            transform: style.transform,
          };
        });
        expect(motion.transitionDuration === "0s" || motion.transitionDuration === "0s, 0s").toBe(true);
        expect(motion.animationDuration === "0s" || motion.animationDuration === "0s, 0s").toBe(true);
        expect(motion.transform).toBe("none");

        await toast.getByRole("button", { name: "Dismiss confirmation" }).click();
        await expect(toast).toBeHidden();
      });
    });
