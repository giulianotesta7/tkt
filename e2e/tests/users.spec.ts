/**
 * Regression: /users must not produce document-level horizontal overflow on mobile.
 *
 * Fix: web/templates/static/users.css — contain the 620px table inside the
 * .users-list card via grid min-width:0 and in-panel scroll, preventing
 * document scrollWidth from exceeding the viewport at 390px.
 * Desktop layout (>900px) must remain untouched.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

async function login(page) {
  await page.goto(base() + "/login");
  await page.getByLabel(/email/i).fill("alice@example.com");
  await page.getByLabel(/password/i).fill("SuperSecret42!");
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}

test.describe("Users mobile overflow", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });

  test.afterAll(async () => {
    await stopServer();
  });

  test("no document overflow at 390px and table remains scrollable in-panel", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });

    const consoleErrors: string[] = [];
    const pageErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => pageErrors.push(String(err)));

    await login(page);
    await page.goto(base() + "/users");
    await expect(page.locator(".users-root")).toBeVisible();

    const metrics = await page.evaluate(() => ({
      viewport: window.innerWidth,
      htmlScroll: document.documentElement.scrollWidth,
      htmlClient: document.documentElement.clientWidth,
      bodyScroll: document.body.scrollWidth,
      listClient: document.querySelector<HTMLElement>(".users-list")!.clientWidth,
      listScroll: document.querySelector<HTMLElement>(".users-list")!.scrollWidth,
      listOverflowX: getComputedStyle(document.querySelector<HTMLElement>(".users-list")!).overflowX,
    }));

    expect(metrics.viewport).toBe(390);
    expect(metrics.htmlScroll).toBeLessThanOrEqual(390);
    expect(metrics.htmlClient).toBe(390);
    expect(metrics.bodyScroll).toBeLessThanOrEqual(390);

    // table remains usable via in-panel scroll
    expect(metrics.listScroll).toBeGreaterThan(metrics.listClient);
    expect(metrics.listOverflowX).toBe("auto");

    // verify scroll actually moves content
    const canScroll = await page.evaluate(() => {
      const el = document.querySelector<HTMLElement>(".users-list")!;
      const before = el.scrollLeft;
      el.scrollLeft = 50;
      const after = el.scrollLeft;
      el.scrollLeft = before;
      return after === 50;
    });
    expect(canScroll).toBe(true);

    expect(consoleErrors, `console errors: ${consoleErrors.join("; ")}`).toEqual([]);
    expect(pageErrors, `page errors: ${pageErrors.join("; ")}`).toEqual([]);
  });

  test("no document overflow at desktop 1280px", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });

    const consoleErrors: string[] = [];
    const pageErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") consoleErrors.push(msg.text());
    });
    page.on("pageerror", (err) => pageErrors.push(String(err)));

    await login(page);
    await page.goto(base() + "/users");
    await expect(page.locator(".users-root")).toBeVisible();

    const metrics = await page.evaluate(() => ({
      viewport: window.innerWidth,
      htmlScroll: document.documentElement.scrollWidth,
      htmlClient: document.documentElement.clientWidth,
    }));

    expect(metrics.viewport).toBe(1280);
    expect(metrics.htmlScroll).toBeLessThanOrEqual(1280);
    expect(metrics.htmlClient).toBe(1280);

    expect(consoleErrors).toEqual([]);
    expect(pageErrors).toEqual([]);
  });
});
