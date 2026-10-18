/**
 * Settings journeys: appearance panel and the SLA configuration panel.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, createUserAsAdmin, loginAs, logout, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";

test.describe("Settings", () => {
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
    await expect(page.getByText(/instance configuration/i)).toBeVisible();
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

  test("success confirmation presents as a fixed top-right toast at desktop and mobile widths", async ({
    page,
  }) => {
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

  test("reduced motion keeps the toast functional without transitions or transforms", async ({
    page,
  }) => {
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

  test("renders the SLA panel with the seeded values and persists edits after a reload", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.getByText(/instance configuration/i)).toBeVisible();
    await expect(page.locator("h2").filter({ hasText: "Service level agreements" })).toBeVisible();

    // Seeded configuration (migration 0013): the feature is OFF and the 4x2
    // matrix carries the default seconds decomposed into h/m/s units.
    await expect(page.locator('input[name="sla_enabled"]')).not.toBeChecked();
    await expect(page.getByLabel("Warning threshold")).toHaveValue("80");
    // [hours, minutes, seconds] per milestone: critical first response is
    // 1800s = 0h 30m 0s, every other seeded target is a whole number of hours.
    const seededMatrix = [
      { priority: "Critical", firstResponse: ["0", "30", "0"], resolve: ["4", "0", "0"] },
      { priority: "High", firstResponse: ["1", "0", "0"], resolve: ["8", "0", "0"] },
      { priority: "Medium", firstResponse: ["4", "0", "0"], resolve: ["24", "0", "0"] },
      { priority: "Low", firstResponse: ["8", "0", "0"], resolve: ["72", "0", "0"] },
    ];
    const units = ["hours", "minutes", "seconds"] as const;
    for (const row of seededMatrix) {
      for (const [unitIndex, unit] of units.entries()) {
        await expect(page.getByLabel(`${row.priority} first response, ${unit}`)).toHaveValue(
          row.firstResponse[unitIndex],
        );
        await expect(page.getByLabel(`${row.priority} resolve, ${unit}`)).toHaveValue(
          row.resolve[unitIndex],
        );
      }
    }

    // Edit the warning percent and one target, save, and verify persistence.
    await page.getByLabel("Warning threshold").fill("55");
    await page.getByLabel("Critical first response, minutes").fill("45");
    await page.getByLabel("Critical first response, seconds").fill("10");
    await page.getByRole("button", { name: "Save SLA settings" }).click();
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    await page.reload();
    await expect(page.getByLabel("Warning threshold")).toHaveValue("55");
    await expect(page.getByLabel("Critical first response, minutes")).toHaveValue("45");
    await expect(page.getByLabel("Critical first response, seconds")).toHaveValue("10");
    await expect(page.getByLabel("Critical first response, hours")).toHaveValue("0");
    await expect(page.getByLabel("High first response, hours")).toHaveValue("1");
    await expect(page.locator('input[name="sla_enabled"]')).not.toBeChecked();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings SLA panel persist",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("working calendar: a valid save persists across a reload and a rejected save stores nothing", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings");
    await expect(page.locator("h2").filter({ hasText: "Working calendar" })).toBeVisible();
    // Seven ISO working-day checkboxes, always all offered.
    await expect(page.locator('input[name="sla_calendar_days"]')).toHaveCount(7);

    // Change the week, the window, and the zone, then save. setChecked is
    // idempotent, so the journey does not depend on the stored calendar it
    // starts from.
    await page.locator('input[name="sla_calendar_days"][value="5"]').setChecked(false);
    await page.locator('input[name="sla_calendar_days"][value="6"]').setChecked(true);
    await page.getByLabel("Start").fill("08:30");
    await page.getByLabel("End").fill("17:45");
    await page.getByLabel("Timezone").fill("America/Argentina/Buenos_Aires");
    await page.getByRole("button", { name: "Save working calendar" }).click();
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // Persistence survives a reload.
    await page.reload();
    await expect(page.locator('input[name="sla_calendar_days"][value="5"]')).not.toBeChecked();
    await expect(page.locator('input[name="sla_calendar_days"][value="6"]')).toBeChecked();
    await expect(page.getByLabel("Start")).toHaveValue("08:30");
    await expect(page.getByLabel("End")).toHaveValue("17:45");
    await expect(page.getByLabel("Timezone")).toHaveValue("America/Argentina/Buenos_Aires");

    // Structural screen assertion here, before the deliberate 422 below:
    // the rejected save legitimately logs a failed resource, which the
    // canonical observability check does not allow.
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings working calendar persist",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // Rejected save: a start after its end re-renders at the POST URL with
    // the error banner and must not touch the stored calendar.
    await page.getByLabel("Start").fill("18:00");
    await page.getByLabel("End").fill("09:00");
    await page.getByRole("button", { name: "Save working calendar" }).click();
    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(page).toHaveURL(/\/settings\/calendar$/);

    // A fresh GET (not a reload: reloading a POST response re-submits the
    // form) shows the stored calendar untouched by the rejected post.
    await page.goto(base() + "/settings");
    await expect(page.locator('input[name="sla_calendar_days"][value="5"]')).not.toBeChecked();
    await expect(page.locator('input[name="sla_calendar_days"][value="6"]')).toBeChecked();
    await expect(page.getByLabel("Start")).toHaveValue("08:30");
    await expect(page.getByLabel("End")).toHaveValue("17:45");
    await expect(page.getByLabel("Timezone")).toHaveValue("America/Argentina/Buenos_Aires");
  });

  test("a rejected SLA save renders the error banner, echoes the submitted values, and stores nothing", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    // Fixture: a known valid configuration saved through the real UI, so the
    // rejection leg can prove the stored values survive untouched.
    await page.goto(base() + "/settings");
    await page.getByLabel("Warning threshold").fill("55");
    await page.getByLabel("Critical first response, minutes").fill("45");
    await page.getByLabel("Critical first response, seconds").fill("10");
    await page.getByRole("button", { name: "Save SLA settings" }).click();
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // A 30-second first response passes the grid's HTML constraints (min=0 on
    // the seconds input) and must be rejected server-side: every target needs
    // at least 60 seconds.
    await page.goto(base() + "/settings");
    await page.getByLabel("Warning threshold").fill("66");
    await page.getByLabel("Critical first response, minutes").fill("0");
    await page.getByLabel("Critical first response, seconds").fill("30");
    await page.getByLabel("High resolve, hours").fill("9");
    await page.getByRole("button", { name: "Save SLA settings" }).click();

    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    // Native form: the rejection re-renders at the POST URL, no redirect.
    await expect(page).toHaveURL(/\/settings\/sla$/);

    // The re-render echoes the SUBMITTED values, not the stored ones.
    await expect(page.getByLabel("Warning threshold")).toHaveValue("66");
    await expect(page.getByLabel("Critical first response, minutes")).toHaveValue("0");
    await expect(page.getByLabel("Critical first response, seconds")).toHaveValue("30");
    await expect(page.getByLabel("High resolve, hours")).toHaveValue("9");

    // A fresh GET (not a reload: reloading a POST response re-submits the
    // form) shows the stored configuration untouched by the rejected post.
    await page.goto(base() + "/settings");
    await expect(page.getByLabel("Warning threshold")).toHaveValue("55");
    await expect(page.getByLabel("Critical first response, minutes")).toHaveValue("45");
    await expect(page.getByLabel("Critical first response, seconds")).toHaveValue("10");
    await expect(page.getByLabel("High resolve, hours")).toHaveValue("8");
  });

  test("switching the interface language re-renders every screen and switching back restores English", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // Fixture: a ticket created while the instance is still English, so the
    // Spanish screens read data this journey arranged through the UI.
    const title = `Printer offline ${Date.now().toString(36)}`;
    const ticketId = await createTicketViaUi(page, { title });

    // The period range is the list surface that carries a month name, so it
    // anchors the before/after comparison of the rendered dates.
    const weekRange = page.locator(".ticket-metrics-summary-range");
    await expect(weekRange).toHaveText(/[A-Z][a-z]{2} \d{1,2} – [A-Z][a-z]{2} \d{1,2}, \d{4}/);

    // Switch the instance-wide language from Settings.
    await page.goto(base() + "/settings");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    const language = page.getByLabel("Language");
    await expect(language).toHaveValue("en");
    await language.selectOption("es");
    await page.getByRole("button", { name: "Save language" }).click();
    await expect(page).toHaveURL(/\/settings$/);

    // The save lands on a NEW request, so the whole shell follows the saved
    // language — including the save toast the previous request issued.
    await expect(page.locator("html")).toHaveAttribute("lang", "es");
    await expect(page.locator("h1").filter({ hasText: "Configuración" })).toBeVisible();
    await expect(page.locator("h2").filter({ hasText: "Idioma de la interfaz" })).toBeVisible();
    await expect(page.getByLabel("Idioma")).toHaveValue("es");
    await expect(page.locator("#save-feedback")).toContainText("Guardado");

    // Persisted (S9): the instance stays Spanish across a reload and across
    // users, because the setting is instance-wide and not per session.
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("lang", "es");
    await expect(page.getByLabel("Idioma")).toHaveValue("es");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings in Spanish",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // The tickets list: Spanish chrome, and the period range reads Spanish
    // months in the day-month order Spanish uses.
    await page.goto(base() + "/tickets");
    await expect(page.locator("html")).toHaveAttribute("lang", "es");
    await expect(page.locator("th").filter({ hasText: "Título" })).toBeVisible();
    await expect(page.getByRole("link", { name: title, exact: true })).toBeVisible();
    await expect(page.locator(".ticket-metrics-summary-range")).toHaveText(
      /\d{1,2} (ene|feb|mar|abr|may|jun|jul|ago|sep|oct|nov|dic) – \d{1,2} (ene|feb|mar|abr|may|jun|jul|ago|sep|oct|nov|dic) \d{4}/,
    );

    // The ticket detail: the state badge and the properties speak Spanish, and
    // the date keeps the numeric shape this surface has always had — that
    // shape carries no month name, so the language must not move it.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("html")).toHaveAttribute("lang", "es");
    await expect(page.locator(".prop-heading").filter({ hasText: "Propiedades" })).toBeVisible();
    await expect(page.locator("#ticket-detail .prop-value .badge.new")).toHaveText("Nuevo");
    await expect(page.locator("time[datetime]").first()).toHaveText(
      /^\d{2}:\d{2} · \d{2}-\d{2}-\d{4}$/,
    );

    // Switch back to English and prove the previous language is restored.
    await page.goto(base() + "/settings");
    await page.getByLabel("Idioma").selectOption("en");
    await page.getByRole("button", { name: "Guardar idioma" }).click();
    await expect(page).toHaveURL(/\/settings$/);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.getByLabel("Language")).toHaveValue("en");
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    await page.goto(base() + "/tickets");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("th").filter({ hasText: "Title" })).toBeVisible();
    await expect(weekRange).toHaveText(/[A-Z][a-z]{2} \d{1,2} – [A-Z][a-z]{2} \d{1,2}, \d{4}/);

    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("#ticket-detail .prop-value .badge.new")).toHaveText("New");
  });
});

test.describe("Preferences", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("a stored default queue order applies to the queue and an explicit sort wins", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // Reach the page the way a user does: through the rail link, not by URL.
    const preferencesLink = page.locator('a.rail-link[href="/preferences"]');
    await expect(preferencesLink).toBeVisible();
    await expect(preferencesLink).toHaveAttribute("aria-label", "Preferences");
    await preferencesLink.click();
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(page.locator("h1").filter({ hasText: "Preferences" })).toBeVisible();
    await expect(page.getByText(/personal settings/i)).toBeVisible();
    const order = page.getByLabel("Default queue order");
    await expect(order).toBeVisible();
    await expect(order).toHaveValue("newest");

    await order.selectOption("priority");
    await page.getByRole("button", { name: /save preferences/i }).click();
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // The stored default survives a reload.
    await page.reload();
    await expect(page.getByLabel("Default queue order")).toHaveValue("priority");

    // The stored default applies when the URL carries no sort.
    await page.goto(base() + "/tickets");
    await expect(page.getByLabel("Order by")).toHaveValue("priority");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "preferences queue default",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // An explicit URL parameter still wins.
    await page.goto(base() + "/tickets?sort=newest");
    await expect(page.getByLabel("Order by")).toHaveValue("newest");

    // Restore the default for any journey that follows.
    await page.goto(base() + "/preferences");
    await page.getByLabel("Default queue order").selectOption("newest");
    await page.getByRole("button", { name: /save preferences/i }).click();
    await expect(page).toHaveURL(/\/preferences$/);
  });

  test("a requester can open the preferences page and an anonymous visitor cannot", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const email = await createUserAsAdmin(page, {
      name: "Preferences Requester",
      email: "preferences-requester@example.com",
      password: "SuperSecret42!",
    });
    await logout(page);

    // Anonymous: the session gate bounces to /login.
    await page.goto(base() + "/preferences");
    await expect(page).toHaveURL(/\/login/);

    await loginAs(page, email, "SuperSecret42!");
    // The requester role sees the rail link too (the page is not
    // capability-gated) and reaches the page by clicking it.
    await expect(page.locator('a.rail-link[href="/preferences"]')).toBeVisible();
    await page.locator('a.rail-link[href="/preferences"]').click();
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(page.locator("h1").filter({ hasText: "Preferences" })).toBeVisible();
    await expect(page.getByLabel("Default queue order")).toBeVisible();
  });
});
