/**
 * Shared shell journeys (issue #377).
 *
 * Structural baselines prove every screen renders the shell; this file proves
 * the behaviour that only the shell owns:
 *  - the sidebar collapses to a rail, the choice is persisted through
 *    POST /preferences/sidebar (204, no swap) and survives a reload;
 *  - the `[` shortcut toggles it outside form fields;
 *  - the user menu is a real popup: toggle state, Escape closes it and returns
 *    focus, Preferences and Log out live inside it;
 *  - on a phone the sidebar is a drawer opened from the top bar or the tab
 *    bar's More, closed by Escape, the scrim and its close button.
 */

import { expect, test } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { base, loginAsSeeded } from "./helpers/auth.js";
import { collectObservability } from "./helpers/layout.js";

const sidebarWidth = (page: import("@playwright/test").Page) =>
  page.evaluate(() => document.querySelector(".sidebar")!.getBoundingClientRect().width);

test.describe("Shell — collapsible sidebar", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the collapse control persists per user and `[` toggles it", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await expect(page).toHaveURL(/\/tickets/);

    const collapse = page.locator("[data-sidebar-collapse]");
    await expect(collapse).toHaveAttribute("aria-pressed", "false");
    expect(await sidebarWidth(page)).toBe(232);

    // Collapse: the width changes at once and the choice is posted without a swap.
    const saved = page.waitForResponse(
      (r) => r.url().endsWith("/preferences/sidebar") && r.request().method() === "POST",
    );
    await collapse.click();
    const response = await saved;
    expect(response.status()).toBe(204);
    expect(response.request().postData()).toBe("sidebar=collapsed");
    await expect(collapse).toHaveAttribute("aria-pressed", "true");
    expect(await sidebarWidth(page)).toBe(72);
    // The label still names every section for assistive technology.
    await expect(page.locator('a.sidebar-link[href="/tickets"]')).toHaveAccessibleName("Tickets");

    // Persisted: a reload renders the rail from the server, no flash.
    await page.reload();
    await expect(page.locator("body")).toHaveClass(/sidebar-collapsed/);
    expect(await sidebarWidth(page)).toBe(72);

    // `[` outside a field expands again and persists.
    const expanded = page.waitForResponse(
      (r) => r.url().endsWith("/preferences/sidebar") && r.request().method() === "POST",
    );
    await page.locator("body").press("[");
    expect((await expanded).request().postData()).toBe("sidebar=expanded");
    expect(await sidebarWidth(page)).toBe(232);

    // `[` inside a text field types a bracket and leaves the sidebar alone.
    const search = page.locator('input[type="search"]').first();
    await search.click();
    await search.press("[");
    await expect(search).toHaveValue("[");
    expect(await sidebarWidth(page)).toBe(232);

    await page.reload();
    await expect(page.locator("body")).not.toHaveClass(/sidebar-collapsed/);
    expect(await sidebarWidth(page)).toBe(232);

    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });

  test("the user menu is a popup with Preferences and Log out", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const toggle = page.locator("[data-user-menu-toggle]");
    const menu = page.locator("#user-menu");
    await expect(toggle).toContainText("Alice Admin");
    await expect(toggle).toContainText("Root");
    await expect(menu).toBeHidden();

    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem", { name: "Preferences" })).toBeFocused();
    await expect(menu.getByRole("menuitem", { name: "Log out" })).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(toggle).toBeFocused();

    // Log out still posts the form from inside the menu.
    await toggle.click();
    await menu.getByRole("menuitem", { name: "Log out" }).click();
    await expect(page).toHaveURL(/\/login/);
  });

  test("on a phone the sidebar is a drawer", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const sidebar = page.locator("#sidebar");
    const opener = page.locator(".topbar [data-sidebar-open]");
    const scrim = page.locator(".sidebar-scrim");
    const offCanvas = () =>
      page.evaluate(() => document.querySelector(".sidebar")!.getBoundingClientRect().right <= 0);

    await expect.poll(offCanvas, { message: "the drawer transition settles" }).toBe(true);
    await expect(opener).toHaveAttribute("aria-expanded", "false");
    await expect(page.locator(".tabbar")).toBeVisible();

    await opener.click();
    await expect(opener).toHaveAttribute("aria-expanded", "true");
    await expect(scrim).toBeVisible();
    await expect(sidebar.locator('a.sidebar-link[href="/tickets"]')).toBeVisible();
    await expect.poll(offCanvas, { message: "the drawer transition settles" }).toBe(false);

    await page.keyboard.press("Escape");
    await expect(scrim).toBeHidden();
    await expect.poll(offCanvas, { message: "the drawer transition settles" }).toBe(true);
    await expect(opener).toBeFocused();

    // The tab bar's More opens the same drawer; the scrim closes it.
    await page.locator(".tabbar [data-sidebar-open]").click();
    await expect(scrim).toBeVisible();
    await scrim.click({ position: { x: 380, y: 400 } });
    await expect(scrim).toBeHidden();

    // The close button inside the drawer closes it too.
    await opener.click();
    await sidebar.locator("[data-sidebar-close]").click();
    await expect(scrim).toBeHidden();
    await expect.poll(offCanvas, { message: "the drawer transition settles" }).toBe(true);

    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
  });
});
