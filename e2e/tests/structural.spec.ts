/**
 * Structural baselines for every canonical screen.
 *
 * Each canonical route is asserted at 390px and 1280px:
 *  - html.scrollWidth <= viewport (no document-level horizontal overflow)
 *  - zero console errors and zero page errors
 *
 * Uses the shared layout helper — no copy-pasted viewport assertions.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { loginAsSeeded, base } from "./helpers/auth.js";

const viewports = [
  { width: 390, height: 844, label: "390px" },
  { width: 1280, height: 800, label: "1280px" },
] as const;

async function createTicketAndGetId(page: import("@playwright/test").Page): Promise<string> {
  await page.goto(base() + "/tickets/new");
  await expect(page.locator("h2")).toContainText(/ticket details/i);
  const title = "Structural probe " + Date.now() + Math.random().toString(36).slice(2, 6);
  await page.getByLabel(/title/i).fill(title);
  await page.getByLabel(/description/i).fill("probe");
  await page.getByLabel(/category/i).selectOption({ label: "General" });
  await page.getByLabel(/priority/i).selectOption("high");
  await page.getByRole("button", { name: /create ticket/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
  await expect(page.getByText(title)).toBeVisible();
  const href = await page.getByText(title).first().getAttribute("href");
  if (href) {
    const m = href.match(/\/tickets\/(\d+)/);
    if (m) return m[1];
  }
  const link = page.locator('a[href*="/tickets/"]').first();
  const h = await link.getAttribute("href");
  const m2 = h?.match(/\/tickets\/(\d+)/);
  if (m2) return m2[1];
  throw new Error("could not extract ticket id for " + title);
}

test.describe("Structural baselines", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  for (const vp of viewports) {
    test(`no overflow or console errors on canonical screens at ${vp.label}`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height });
      const obs = collectObservability(page);
      await loginAsSeeded(page);

      // Create a ticket once for /tickets/{id} detail coverage
      const ticketId = await createTicketAndGetId(page);

      // Determine seeded category id for workflow builder (General)
      // Workflow builder lives at /categories/{id}/workflow — fetch via categories index.
      await page.goto(base() + "/categories");
      // Extract first category's workflow link
      let workflowHref: string | null = null;
      const workflowLink = page.locator('a[href*="/categories/"][href*="/workflow"]').first();
      if (await workflowLink.count()) {
        workflowHref = await workflowLink.getAttribute("href");
      } else {
        // Fallback: parse category id from edit link
        const editLink = page.locator('a[href*="/categories/"][href*="/edit"]').first();
        if (await editLink.count()) {
          const href = await editLink.getAttribute("href");
          const m = href?.match(/\/categories\/(\d+)\/edit/);
          if (m) workflowHref = `/categories/${m[1]}/workflow`;
        }
      }

      const screens: { path: string; locator: string }[] = [
        { path: "/tickets", locator: 'h1:has-text("Tickets")' },
        { path: `/tickets/${ticketId}`, locator: '#ticket-detail' },
        { path: "/desks", locator: 'h1:has-text("Desks")' },
        { path: "/categories", locator: 'h1:has-text("Categories")' },
        { path: "/users", locator: '.users-root' },
        { path: "/settings", locator: 'h1:has-text("Settings")' },
      ];
      if (workflowHref) {
        screens.push({ path: workflowHref, locator: "#workflow-builder" });
      }

      for (const { path, locator } of screens) {
        await page.goto(base() + path);
        await expect(page.locator(locator).first()).toBeVisible({ timeout: 10_000 });
        await assertCanonicalScreen(page, {
          viewport: vp.width,
          consoleErrors: obs.consoleErrors,
          pageErrors: obs.pageErrors,
        });
      }

      // Failed network requests are not expected on canonical screens
      expect(obs.consoleErrors, `console errors: ${obs.consoleErrors.join("; ")}`).toEqual([]);
      expect(obs.pageErrors, `page errors: ${obs.pageErrors.join("; ")}`).toEqual([]);
    });
  }
});
