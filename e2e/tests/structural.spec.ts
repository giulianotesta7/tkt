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
import { createTicketViaUi, resolveWorkflowHref } from "./helpers/navigation.js";

const viewports = [
  { width: 390, height: 844, label: "390px" },
  { width: 1280, height: 800, label: "1280px" },
] as const;

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
      const ticketTitle = "Structural probe " + Date.now() + Math.random().toString(36).slice(2, 6);
      const ticketId = await createTicketViaUi(page, { title: ticketTitle, description: "probe", category: "General", priority: "high" });

      // Determine seeded category id for workflow builder (General)
      await page.goto(base() + "/categories");
      const workflowHref = await resolveWorkflowHref(page);

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
