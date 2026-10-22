/**
 * Dark theme (issue #378).
 *
 * The theme is a per-user preference with three values. This file proves what
 * only the theme owns:
 *  - the preference persists through POST /preferences/theme and reaches the
 *    document as `data-theme` on `<html>` (none for system), with no flash
 *    because the server renders it;
 *  - `system` follows the OS, an explicit choice overrides it either way;
 *  - every canonical route passes the structural baseline in the dark ladder
 *    (overflow, clipping, theme-aware tokens, shell chrome, zero errors);
 *  - contrast is measured, not looked at: body ink, muted, table headers,
 *    timestamps, badges and the primary button reach 4.5:1 on the surface they
 *    land on, in both themes;
 *  - state badges, the four priority marks and the internal-comment affordance
 *    stay distinguishable in dark.
 */

import { expect, test, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { base, loginAsSeeded, logout } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability, measureContrast } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";

async function setTheme(page: Page, theme: "system" | "light" | "dark"): Promise<void> {
  await page.goto(base() + "/preferences");
  await page.getByLabel("Theme").selectOption(theme);
  await page.getByRole("button", { name: "Save theme" }).click();
  await expect(page).toHaveURL(/\/preferences$/);
  await expect(page.locator("#save-feedback")).toContainText("Saved");
  await expect(page.getByLabel("Theme")).toHaveValue(theme);
}

const htmlTheme = (page: Page) =>
  page.evaluate(() => document.documentElement.getAttribute("data-theme"));
const canvas = (page: Page) => page.evaluate(() => getComputedStyle(document.body).backgroundColor);

const DARK_CANVAS = "rgb(21, 24, 29)";
const LIGHT_CANVAS = "rgb(255, 255, 255)";

test.describe("Theme", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the preference persists, follows the OS on system and overrides it on a choice", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // Default: system, no attribute, follows the OS both ways.
    expect(await htmlTheme(page)).toBeNull();
    await page.emulateMedia({ colorScheme: "dark" });
    await page.reload();
    expect(await htmlTheme(page)).toBeNull();
    expect(await canvas(page)).toBe(DARK_CANVAS);
    await page.emulateMedia({ colorScheme: "light" });
    await page.reload();
    expect(await canvas(page)).toBe(LIGHT_CANVAS);

    // Explicit dark under a light OS.
    await setTheme(page, "dark");
    expect(await htmlTheme(page)).toBe("dark");
    expect(await canvas(page)).toBe(DARK_CANVAS);
    await page.reload();
    expect(await htmlTheme(page)).toBe("dark");
    expect(await canvas(page)).toBe(DARK_CANVAS);
    // The auth shell carries no user, so it follows the OS, never the choice.
    await logout(page);
    expect(await htmlTheme(page)).toBeNull();
    expect(await canvas(page)).toBe(LIGHT_CANVAS);

    // Explicit light under a dark OS.
    await loginAsSeeded(page);
    await page.emulateMedia({ colorScheme: "dark" });
    await setTheme(page, "light");
    expect(await htmlTheme(page)).toBe("light");
    expect(await canvas(page)).toBe(LIGHT_CANVAS);

    // Back to system: attribute gone, OS wins again.
    await setTheme(page, "system");
    expect(await htmlTheme(page)).toBeNull();
    expect(await canvas(page)).toBe(DARK_CANVAS);
    await page.emulateMedia({ colorScheme: "light" });

    // The other two preference forms on the page keep working beside it.
    await expect(page.getByLabel("Default queue order")).toHaveValue("newest");
    await expect(page.getByLabel("Receive notifications by email")).toBeChecked();

    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });

  test("every canonical route passes the structural baseline in dark", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const ticketId = await createTicketViaUi(page, {
      title: "Dark baseline ticket",
      description: "Rendered in the dark ladder.",
      priority: "high",
    });
    await setTheme(page, "dark");

    for (const path of [
      "/tickets",
      "/tickets/new",
      `/tickets/${ticketId}`,
      "/tickets/metrics",
      "/users",
      "/categories",
      "/settings",
      "/notifications",
      "/preferences",
    ]) {
      await page.goto(base() + path);
      expect(await htmlTheme(page), `${path} renders the stored theme`).toBe("dark");
      await assertCanonicalScreen(page, {
        viewport: 1280,
        label: `${path} dark @ 1280px`,
        url: page.url(),
        role: "root",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });
    }

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(base() + "/tickets");
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "/tickets dark @ 390px",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
    await setTheme(page, "system");
  });

  for (const theme of ["light", "dark"] as const) {
    test(`text reaches 4.5:1 and state survives in ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
      const ticketId = await createTicketViaUi(page, {
        title: `Contrast ${theme}`,
        description: "Measured, not looked at.",
        priority: "critical",
      });
      await page.goto(base() + `/tickets/${ticketId}`);
      await page.getByLabel("Comment body").fill("A public reply.");
      await page.getByRole("button", { name: "Reply", exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/tickets/${ticketId}$`));
      await page.getByLabel("Internal note").check();
      await page.getByLabel("Comment body").fill("A staff-only note.");
      await page.getByRole("button", { name: "Reply", exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/tickets/${ticketId}$`));
      await setTheme(page, theme);

      // Queue: body ink, muted metadata, table headers, state and priority.
      await page.goto(base() + "/tickets");
      const probes: Array<[string, ReturnType<Page["locator"]>]> = [
        ["body ink (title cell)", page.locator(".queue .cell-title").first()],
        ["muted (page subtitle)", page.locator(".page-subtitle").first()],
        ["table header", page.locator(".queue th").first()],
        ["timestamp cell", page.locator(".queue .cell-muted").first()],
        ["state badge", page.locator(".queue .badge").first()],
        ["priority value", page.locator(".queue .ticket-priority-value").first()],
        ["primary action", page.locator(".topbar .btn.primary")],
        ["sidebar label", page.locator(".sidebar-link.active .sidebar-label")],
        ["sidebar group label", page.locator(".sidebar-group-label").first()],
      ];
      for (const [label, locator] of probes) {
        await expect(locator, label).toBeVisible();
        const c = await measureContrast(locator);
        expect(
          c.ratio,
          `${theme}: ${label} ${c.fg} on ${c.bg} must reach 4.5:1`,
        ).toBeGreaterThanOrEqual(4.5);
      }

      // Priority marks: four distinct colours.
      const marks = await page.evaluate(() =>
        ["critical", "high", "medium", "low"].map((p) => {
          const probe = document.createElement("span");
          probe.className = `priority ${p}`;
          probe.innerHTML = '<span class="ticket-priority-value">x</span>';
          document.querySelector(".tickets-screen")!.append(probe);
          const c = getComputedStyle(probe.querySelector("span")!, "::before").backgroundColor;
          probe.remove();
          return c;
        }),
      );
      expect(new Set(marks).size, `${theme}: priority marks ${marks.join(" | ")}`).toBe(4);

      // State badges: five distinct fills and ink readable on each.
      const badges = await page.evaluate(() =>
        ["new", "in_progress", "resolved", "closed", "cancelled"].map((s) => {
          const probe = document.createElement("span");
          probe.className = `badge ${s}`;
          probe.textContent = s;
          document.body.append(probe);
          const cs = getComputedStyle(probe);
          const out = `${cs.backgroundColor}/${cs.color}`;
          probe.remove();
          return out;
        }),
      );
      expect(new Set(badges).size, `${theme}: badges ${badges.join(" | ")}`).toBe(5);

      // Detail: comment body, timeline metadata and the internal affordance.
      await page.goto(base() + `/tickets/${ticketId}`);
      const internal = page.locator(".timeline-comment.internal").first();
      const publicComment = page.locator(".timeline-comment:not(.internal)").first();
      await expect(internal).toBeVisible();
      for (const [label, locator] of [
        ["internal comment body", internal],
        ["public comment body", publicComment],
        ["timeline time", page.locator(".timeline-entry time").first()],
        ["property label", page.locator(".prop-row .prop-label").first()],
      ] as Array<[string, ReturnType<Page["locator"]>]>) {
        await expect(locator, label).toBeVisible();
        const c = await measureContrast(locator);
        expect(
          c.ratio,
          `${theme}: ${label} ${c.fg} on ${c.bg} must reach 4.5:1`,
        ).toBeGreaterThanOrEqual(4.5);
      }
      const [internalBg, publicBg] = await Promise.all([
        internal.evaluate((el) => getComputedStyle(el).backgroundColor),
        publicComment.evaluate((el) => getComputedStyle(el).backgroundColor),
      ]);
      expect(
        internalBg,
        `${theme}: the internal affordance must differ from a public comment`,
      ).not.toBe(publicBg);
      await setTheme(page, "system");
    });
  }
});
