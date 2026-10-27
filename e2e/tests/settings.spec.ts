/**
 * Settings journeys: appearance panel, the SLA configuration panel (including
 * the automatic hold close of issue #353), the root-only Email section (issue
 * #362), and the per-user Preferences page.
 *
 * The automatic hold close itself is NOT exercised here: it fires on a real
 * working-day deadline, so an end-to-end proof would need days of wall clock.
 * Go proves that rule with an injected clock
 * (`internal/application/hold_expiry_test.go`); this file proves the setting
 * the rule reads.
 */

import { test, expect, type Page, type Request } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, createUserAsAdmin, loginAs, logout, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { createTicketViaUi, openPreferencesFromUserMenu } from "./helpers/navigation.js";
import { createSuggestableCategory } from "./helpers/suggestions.js";
import { assertHtmxSwap } from "./helpers/htmx.js";

/**
 * Promote a freshly created user to administrator and return the credentials.
 *
 * The Email section is root-only (#362, S4), and an admin is the role that
 * reaches Settings without holding that capability — so this is the actor the
 * visibility journey needs. The role change goes through the same edit control
 * an administrator uses; the journey that owns its HTMX mechanism is
 * `roles.spec.ts`, so this fixture only waits for the visible result.
 */
async function createAdminUser(page: Page): Promise<{ name: string; email: string }> {
  const stamp = Date.now().toString(36);
  const name = `Settings Admin ${stamp}`;
  const email = `settings-admin-${stamp}@example.com`;
  await createUserAsAdmin(page, { name, email, password: "SuperSecret42!" });

  const row = page
    .locator("tr[data-user-name]")
    .filter({ has: page.getByText(name, { exact: true }) });
  await expect(row).toHaveCount(1);
  const href = await row.locator('a[href*="/users/"][href*="/edit"]').first().getAttribute("href");
  if (!href) throw new Error(`edit href missing for ${name} at ${page.url()}`);
  await page.goto(base() + href.split("?")[0]);
  await page.locator('select[name="role"]').selectOption("admin");
  await page.getByRole("button", { name: /save changes/i }).click();
  await expect(
    page.locator("tr[data-user-name]").filter({ has: page.getByText(name, { exact: true }) }),
  ).toContainText("Admin");
  return { name, email };
}

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
    await page.goto(base() + "/settings/tickets");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.locator("h2").filter({ hasText: "Tickets & SLA" })).toBeVisible();
    const appearance = page.locator('form[action="/settings/appearance"]');
    await expect(
      appearance.getByRole("heading", { name: "Internal comment background", exact: true }),
    ).toBeVisible();
    await expect(appearance.locator("h2")).toHaveCount(0);
    await expect(page.locator(".settings-content > h2")).toHaveText("Tickets & SLA");
    expect(
      await page
        .locator(".settings-content > form")
        .evaluateAll((forms) => forms.map((form) => form.getAttribute("action"))),
    ).toEqual([
      "/settings/ticket-creation",
      "/settings/category-suggestions",
      "/settings/sla",
      "/settings/hold-auto-close",
      "/settings/appearance",
    ]);

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

    // Switch to Violet and save (chip overlays input, so click label). The
    // sticky save bar appears with the first change and the save lands back on
    // the section with the bar hidden again.
    await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
    await expect(page.locator('input[name="internal_comment_bg"][value="#EFE9FB"]')).toBeChecked();
    const appearanceBar = page.locator(
      'form[action="/settings/appearance"] [data-settings-savebar]',
    );
    await expect(appearanceBar).toBeVisible();
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(appearanceBar).toBeHidden();
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
      await page.goto(base() + "/settings/tickets");
      await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();

      const focusBefore = await page.evaluate(() => document.activeElement?.tagName ?? "");
      await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
      await page.getByRole("button", { name: /save appearance/i }).click();
      await expect(page).toHaveURL(/\/settings\/tickets$/);

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
    await page.goto(base() + "/settings/tickets");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();

    await page.locator('label.swatch:has(input[value="#EFE9FB"])').click();
    await page.getByRole("button", { name: /save appearance/i }).click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);

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
    await page.goto(base() + "/settings/tickets");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.locator("h2").filter({ hasText: "Tickets & SLA" })).toBeVisible();
    await expect(page.locator("h3").filter({ hasText: "Service level agreements" })).toBeVisible();

    // Seeded configuration (migration 0013): the feature is OFF and each of the
    // eight default targets renders as one written duration (issue #395, S10).
    await expect(page.locator('input[name="sla_enabled"]')).not.toBeChecked();
    await expect(page.getByLabel("Warning threshold", { exact: true })).toHaveValue("80");
    await expect(page.locator(".sla-duration-input")).toHaveCount(8);
    const seededMatrix = [
      { priority: "Critical", firstResponse: "30m", resolve: "4h" },
      { priority: "High", firstResponse: "1h", resolve: "8h" },
      { priority: "Medium", firstResponse: "4h", resolve: "24h" },
      { priority: "Low", firstResponse: "8h", resolve: "72h" },
    ];
    for (const row of seededMatrix) {
      await expect(page.getByLabel(`${row.priority} first response`, { exact: true })).toHaveValue(
        row.firstResponse,
      );
      await expect(page.getByLabel(`${row.priority} resolve`, { exact: true })).toHaveValue(
        row.resolve,
      );
    }

    // Discard is a native reset: the typed duration, its reading and its
    // validity all return to the stored target, and the save bar hides.
    const critical = page.getByLabel("Critical first response", { exact: true });
    const reading = page.locator("#first_response_duration_critical-reading");
    const saveBar = page.locator('form[action="/settings/sla"] [data-settings-savebar]');
    await critical.fill("59s");
    await expect(critical).toHaveAttribute("aria-invalid", "true");
    await expect(reading).toHaveText("The target must be at least 60 seconds.");
    await expect(saveBar).toBeVisible();
    await saveBar.getByRole("button", { name: "Discard", exact: true }).click();
    await expect(critical).toHaveValue("30m");
    await expect(critical).toHaveAttribute("aria-invalid", "false");
    await expect(reading).toHaveText("30 min");
    await expect(saveBar).toBeHidden();

    // Edit the warning percent and one target, save, and verify persistence.
    await page.getByLabel("Warning threshold", { exact: true }).fill("55");
    await critical.fill("45m 10s");
    await expect(reading).toHaveText("45 min 10 s");
    await page.getByRole("button", { name: "Save SLA settings" }).click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    await page.reload();
    await expect(page.getByLabel("Warning threshold", { exact: true })).toHaveValue("55");
    await expect(critical).toHaveValue("45m 10s");
    await expect(page.getByLabel("High first response", { exact: true })).toHaveValue("1h");
    await expect(page.locator('input[name="sla_enabled"]')).not.toBeChecked();

    // The written durations stack inside a phone viewport.
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByLabel("Low resolve", { exact: true })).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth),
    ).toBe(false);
    await page.setViewportSize({ width: 1280, height: 800 });

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
    await page.goto(base() + "/settings/calendar");
    await expect(page.locator("h2").filter({ hasText: "Working calendar" })).toBeVisible();
    // Seven ISO working-day checkboxes, always all offered.
    await expect(page.locator('input[name="sla_calendar_days"]')).toHaveCount(7);

    // Change the week, the window, and the zone, then save. setChecked is
    // idempotent, so the journey does not depend on the stored calendar it
    // starts from.
    await page.locator('input[name="sla_calendar_days"][value="5"]').setChecked(false);
    await page.locator('input[name="sla_calendar_days"][value="6"]').setChecked(true);
    await page.getByLabel("Start", { exact: true }).fill("08:30");
    await page.getByLabel("End", { exact: true }).fill("17:45");
    await page.getByLabel("Timezone", { exact: true }).fill("America/Argentina/Buenos_Aires");
    await page.getByRole("button", { name: "Save working calendar" }).click();
    await expect(page).toHaveURL(/\/settings\/calendar$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // Persistence survives a reload.
    await page.reload();
    await expect(page.locator('input[name="sla_calendar_days"][value="5"]')).not.toBeChecked();
    await expect(page.locator('input[name="sla_calendar_days"][value="6"]')).toBeChecked();
    await expect(page.getByLabel("Start", { exact: true })).toHaveValue("08:30");
    await expect(page.getByLabel("End", { exact: true })).toHaveValue("17:45");
    await expect(page.getByLabel("Timezone", { exact: true })).toHaveValue(
      "America/Argentina/Buenos_Aires",
    );

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
    await page.getByLabel("Start", { exact: true }).fill("18:00");
    await page.getByLabel("End", { exact: true }).fill("09:00");
    await page.getByRole("button", { name: "Save working calendar" }).click();
    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(page).toHaveURL(/\/settings\/calendar$/);

    // A fresh GET (not a reload: reloading a POST response re-submits the
    // form) shows the stored calendar untouched by the rejected post.
    await page.goto(base() + "/settings/calendar");
    await expect(page.locator('input[name="sla_calendar_days"][value="5"]')).not.toBeChecked();
    await expect(page.locator('input[name="sla_calendar_days"][value="6"]')).toBeChecked();
    await expect(page.getByLabel("Start", { exact: true })).toHaveValue("08:30");
    await expect(page.getByLabel("End", { exact: true })).toHaveValue("17:45");
    await expect(page.getByLabel("Timezone", { exact: true })).toHaveValue(
      "America/Argentina/Buenos_Aires",
    );
  });

  test("a rejected SLA save renders the error banner, echoes the submitted values, and stores nothing", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    // Fixture: a known valid configuration saved through the real UI, so the
    // rejection leg can prove the stored values survive untouched.
    await page.goto(base() + "/settings/tickets");
    await page.getByLabel("Warning threshold", { exact: true }).fill("55");
    await page.getByLabel("Critical first response", { exact: true }).fill("45m 10s");
    await page.getByRole("button", { name: "Save SLA settings" }).click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // With scripting the browser refuses a 30-second target before any
    // request; without it the server is the only judge, which is the path
    // under test: every target needs at least 60 seconds.
    let posts = 0;
    const countPost = (request: import("@playwright/test").Request) => {
      if (new URL(request.url()).pathname === "/settings/sla" && request.method() === "POST")
        posts++;
    };
    page.on("request", countPost);
    await page.getByLabel("Critical first response", { exact: true }).fill("30s");
    await page.getByRole("button", { name: "Save SLA settings" }).click();
    await expect(page.getByLabel("Critical first response", { exact: true })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(posts).toBe(0);
    page.off("request", countPost);

    const noScriptContext = await page
      .context()
      .browser()!
      .newContext({ javaScriptEnabled: false, storageState: await page.context().storageState() });
    try {
      const noScript = await noScriptContext.newPage();
      await noScript.goto(base() + "/settings/tickets");
      await noScript.getByLabel("Warning threshold", { exact: true }).fill("66");
      await noScript.getByLabel("Critical first response", { exact: true }).fill("30s");
      await noScript.getByLabel("High resolve", { exact: true }).fill("9h");
      await noScript.getByLabel("Low first response", { exact: true }).fill("1h 2h");
      const rejected = noScript.waitForResponse(
        (r) => new URL(r.url()).pathname === "/settings/sla" && r.request().method() === "POST",
      );
      await noScript.getByRole("button", { name: "Save SLA settings" }).click();
      expect((await rejected).status()).toBe(422);

      const banner = noScript.locator(".error-banner");
      await expect(banner).toBeVisible();
      await expect(banner).toHaveAttribute("role", "alert");
      // Native form: the rejection re-renders at the POST URL, no redirect.
      await expect(noScript).toHaveURL(/\/settings\/sla$/);

      // The re-render echoes the SUBMITTED text, not the stored values.
      await expect(noScript.getByLabel("Warning threshold", { exact: true })).toHaveValue("66");
      await expect(noScript.getByLabel("Critical first response", { exact: true })).toHaveValue(
        "30s",
      );
      await expect(noScript.getByLabel("High resolve", { exact: true })).toHaveValue("9h");
      await expect(noScript.getByLabel("Low first response", { exact: true })).toHaveValue("1h 2h");
    } finally {
      await noScriptContext.close();
    }

    // A fresh GET shows the stored configuration untouched by the rejected post.
    await page.goto(base() + "/settings/tickets");
    await expect(page.getByLabel("Warning threshold", { exact: true })).toHaveValue("55");
    await expect(page.getByLabel("Critical first response", { exact: true })).toHaveValue(
      "45m 10s",
    );
    await expect(page.getByLabel("High resolve", { exact: true })).toHaveValue("8h");
    await expect(page.getByLabel("Low first response", { exact: true })).toHaveValue("8h");
  });

  test("hold auto-close window: persists, clears to off, and refuses an out-of-range value", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/tickets");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();

    // Issue #353, S2: the automatic close is configured in the SLA part of the
    // Tickets & SLA section (#383), and a seeded instance is OFF — which the
    // field shows as EMPTY.
    await expect(
      page.locator("h3").filter({ hasText: "Automatic close of held tickets" }),
    ).toBeVisible();
    const days = page.getByLabel("Close tickets on hold after N working days without a reply", {
      exact: true,
    });
    await expect(days).toHaveValue("");

    // A saved window persists across a reload.
    await days.fill("3");
    await page.getByRole("button", { name: "Save automatic close" }).click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(days).toHaveValue("3");

    // Clearing it turns the feature off, and THAT persists too.
    await days.fill("");
    await page.getByRole("button", { name: "Save automatic close" }).click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(days).toHaveValue("");

    // Structural screen assertion here, before the deliberate 422 below: the
    // refused save legitimately logs a failed resource, which the canonical
    // observability check does not allow.
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings hold auto-close persist",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // The field constrains only the lower bound, so an out-of-range window
    // reaches the server and is refused with nothing stored.
    await days.fill("99");
    await page.getByRole("button", { name: "Save automatic close" }).click();
    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(banner).toContainText(
      "the hold auto-close window must be between 0 and 60 working days",
    );
    // Native form: the rejection re-renders the section at the POST URL, no
    // redirect.
    await expect(page).toHaveURL(/\/settings\/hold-auto-close$/);
    await expect(days).toHaveValue("99");

    // A fresh GET (not a reload: reloading a POST response re-submits the form)
    // shows the previous value untouched.
    await page.goto(base() + "/settings/tickets");
    await expect(days).toHaveValue("");
  });

  test("the ticket creation flow is chosen in Settings and drives the creation screen", async ({
    page,
  }) => {
    // Issue #391, S1/S3/S4: how a new ticket reaches its category is an INSTANCE
    // setting, saved like every other settings form, and a missing value reads as
    // the one-page default.
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/tickets");
    await expect(page.locator("h3").filter({ hasText: "Ticket creation" })).toBeVisible();

    const single = page.getByRole("radio", { name: /^single page$/i });
    const twoSteps = page.getByRole("radio", { name: /^two steps$/i });
    await expect(single).toBeChecked();
    await expect(twoSteps).not.toBeChecked();
    // The save bar stays hidden until the choice changes.
    const save = page.getByRole("button", { name: "Save creation flow" });
    await expect(save).toBeHidden();

    await twoSteps.check();
    await expect(save).toBeVisible();
    await save.click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(twoSteps).toBeChecked();
    await expect(single).not.toBeChecked();

    // The stored flow decides what GET /tickets/new renders.
    await page.goto(base() + "/tickets/new");
    await expect(page.locator(".create-catalog")).toBeVisible();
    await expect(page.locator(".ticket-create-detail")).toHaveCount(0);

    // Back to the default: the form renders directly, with the picker.
    await page.goto(base() + "/settings/tickets");
    await single.check();
    await expect(save).toBeVisible();
    await save.click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await page.reload();
    await expect(single).toBeChecked();
    await page.goto(base() + "/tickets/new");
    await expect(page.locator(".ticket-create-detail")).toBeVisible();
    await expect(page.locator(".create-catalog")).toHaveCount(0);
    await expect(page.locator("#create-category-picker")).toBeVisible();

    await expect(obs.pageErrors).toEqual([]);
  });

  test("the Category suggestions card starts off, discloses the model fields and persists the engine choice", async ({
    page,
  }) => {
    // Issue #416, S1/S5/S6/S7: one instance setting with three choices. Off is
    // the shipped state, the decision model is the only choice that needs an
    // endpoint, a model and a key, and the card states what that choice sends to
    // the external service and what it never sends. The Try it box runs the
    // values the form submits without saving them.
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    // The Try it box answers with what the engine would suggest, so the database
    // holds one runnable category carrying the word the test types.
    const fixture = await createSuggestableCategory(page, "impresora, tóner");
    await page.goto(base() + "/settings/tickets");
    const card = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Category suggestions" }) });
    await expect(card).toHaveCount(1);
    const off = card.getByRole("radio", { name: /^off\b/i });
    const onThisServer = card.getByRole("radio", { name: /^on this server\b/i });
    const decisionModel = card.getByRole("radio", { name: /^decision model\b/i });
    await expect(off).toBeChecked();
    await expect(onThisServer).not.toBeChecked();
    await expect(decisionModel).not.toBeChecked();

    // The model fields are hidden until the model is the chosen engine.
    const endpoint = card.getByLabel("Endpoint URL", { exact: true });
    const model = card.getByLabel("Model", { exact: true });
    const apiKey = card.getByLabel("API key", { exact: true });
    await expect(endpoint).toBeHidden();
    await expect(model).toBeHidden();
    await expect(apiKey).toBeHidden();

    await decisionModel.check();
    const modelFields = card.locator(".settings-suggestion-model");
    await expect(modelFields).toBeVisible();
    await expect(endpoint).toBeVisible();
    await expect(model).toBeVisible();
    await expect(apiKey).toBeVisible();
    await expect(modelFields).toContainText("Sent:");
    await expect(modelFields).toContainText(
      "the description and each category's name and description.",
    );
    await expect(modelFields).toContainText("Never sent:");
    await expect(modelFields).toContainText(
      "the requester's name or email, attachments, or ticket history.",
    );
    // The key is never rendered back, so the field is empty and typed as a secret.
    await expect(apiKey).toHaveValue("");
    await expect(apiKey).toHaveAttribute("type", "password");

    // Choosing the local engine takes the model fields away again.
    await onThisServer.check();
    await expect(modelFields).toBeHidden();
    await expect(endpoint).toBeHidden();
    await expect(model).toBeHidden();
    await expect(apiKey).toBeHidden();

    // Saving is the section's ordinary form submit and the choice survives a
    // reload, which is what both creation screens read.
    const save = card.getByRole("button", { name: "Save category suggestions" });
    await expect(save).toBeVisible();
    await save.click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(onThisServer).toBeChecked();
    await expect(off).not.toBeChecked();
    await expect(endpoint).toBeHidden();

    // The Try it box follows the engine choice: Off hides it, and any engine that
    // answers reveals it.
    const tryBox = card.locator(".settings-suggestion-try");
    const tryInput = card.getByLabel("Try it", { exact: true });
    const tryResult = page.locator("#category-suggestions-try-result");
    const savebar = card.locator("[data-settings-savebar]");
    await expect(tryBox).toBeVisible();
    await off.check();
    await expect(tryBox).toBeHidden();
    await onThisServer.check();
    await expect(tryBox).toBeVisible();
    await decisionModel.check();
    await expect(tryBox).toBeVisible();
    await expect(modelFields).toBeVisible();
    await onThisServer.check();
    await expect(tryBox).toBeVisible();
    await expect(modelFields).toBeHidden();

    // Test runs the submitted configuration in place: the result is a row per
    // candidate with its share, over the card's own endpoint, with no
    // navigation and no save.
    const saves: string[] = [];
    const saveWatcher = (request: Request) => {
      if (request.method() !== "POST") return;
      if (new URL(request.url()).pathname !== "/settings/category-suggestions") return;
      saves.push(request.url());
    };
    page.on("request", saveWatcher);
    try {
      await assertHtmxSwap(
        page,
        async () => {
          await tryInput.fill("the impresora is not working");
          await card.getByRole("button", { name: "Test", exact: true }).click();
        },
        {
          endpoint: "/settings/category-suggestions/try",
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#category-suggestions-try-result",
          allowEmptyTarget: true,
        },
      );
      await expect(tryResult.locator(".suggestion-try-name").first()).toContainText(
        fixture.category,
      );
      await expect(tryResult.locator(".suggestion-try-percent").first()).toHaveText(/^\d+%$/);

      // Enter in the field the administrator types in runs the test instead of
      // submitting the save form: the bar keeps the state it had, the URL does
      // not move, and no save request leaves the page.
      await expect(savebar).toBeVisible();
      await assertHtmxSwap(
        page,
        async () => {
          await tryInput.fill("no encuentro mi tarjeta de transporte");
          await tryInput.press("Enter");
        },
        {
          endpoint: "/settings/category-suggestions/try",
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#category-suggestions-try-result",
        },
      );
      await expect(tryResult).toContainText(
        "No category matched: requesters would choose from the catalog.",
      );
      await expect(savebar).toBeVisible();
      await expect(page).toHaveURL(base() + "/settings/tickets");
      expect(saves, "Enter in the Try it field must never submit the save form").toEqual([]);
    } finally {
      page.removeListener("request", saveWatcher);
    }

    // Back to the shipped state, so a later journey inherits the default.
    await off.check();
    await save.click();
    await expect(page).toHaveURL(/\/settings\/tickets$/);
    await page.reload();
    await expect(off).toBeChecked();

    await expect(obs.pageErrors).toEqual([]);
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

    // The queue's relative "Updated" reading is the list surface whose words
    // come from the catalogue (issue #382), so it anchors the before/after
    // comparison of the rendered times; the absolute instant rides in its
    // title in the numeric shape no language moves.
    await page.goto(base() + "/tickets");
    const updated = page
      .locator("#ticket-list tr.queue-row")
      .filter({ has: page.getByRole("link", { name: title, exact: true }) })
      .locator(".queue-updated time");
    await expect(updated).toHaveText(/^(just now|\d+ (min|h|d) ago)$/);
    await expect(updated).toHaveAttribute("title", /^\d{2}:\d{2} · \d{2}-\d{2}-\d{4}$/);

    // Switch the instance-wide language from Settings.
    await page.goto(base() + "/settings/language");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    const language = page.getByLabel("Language");
    await expect(language).toHaveValue("en");
    await language.selectOption("es");
    await page.getByRole("button", { name: "Save language" }).click();
    await expect(page).toHaveURL(/\/settings\/language$/);

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

    await page.goto(base() + "/settings/tickets");
    await expect(page.locator(".settings-content > h2")).toHaveText("Tickets y SLA");
    await expect(page.locator('form[action="/settings/appearance"] h3')).toHaveText(
      "Fondo de los comentarios internos",
    );
    await expect(page.locator('form[action="/settings/appearance"] h2')).toHaveCount(0);

    // The tickets list: Spanish chrome, view tabs and the relative time.
    await page.goto(base() + "/tickets");
    await expect(page.locator("html")).toHaveAttribute("lang", "es");
    await expect(page.locator("th").filter({ hasText: "Categoría" })).toBeVisible();
    await expect(page.locator("th").filter({ hasText: "Asignado" })).toBeVisible();
    await expect(page.locator(".queue-tab").filter({ hasText: "Sin asignar" })).toBeVisible();
    await expect(page.getByRole("link", { name: title, exact: true })).toBeVisible();
    await expect(updated).toHaveText(/^(recién|hace \d+ (min|h|d))$/);

    // The ticket detail: the properties panel and the state badge speak Spanish,
    // and the creation instant keeps the numeric shape this surface has always
    // had — that shape carries no month name, so the language must not move it.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("html")).toHaveAttribute("lang", "es");
    // Issue #386: the properties panel heading is the disclosure summary of
    // `details.detail-properties`, and the state badge sits beside the title.
    await expect(
      page.locator("#ticket-detail .detail-properties-summary").filter({ hasText: "Propiedades" }),
    ).toBeVisible();
    await expect(page.locator("#ticket-detail .detail-state .badge.new")).toHaveText("Nuevo");
    // The header meta line reads relatively and carries the absolute instant in
    // its title (S3): the language moves the reading, never the instant.
    const headerTime = page.locator("#ticket-detail .detail-meta time");
    await expect(headerTime).toHaveAttribute("title", /^\d{2}:\d{2} · \d{2}-\d{2}-\d{4}$/);
    await expect(headerTime).toHaveText(/^(recién|hace \d+ (min|h|d))$/);

    // Switch back to English and prove the previous language is restored.
    await page.goto(base() + "/settings/language");
    await page.getByLabel("Idioma").selectOption("en");
    await page.getByRole("button", { name: "Guardar idioma" }).click();
    await expect(page).toHaveURL(/\/settings\/language$/);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.getByLabel("Language")).toHaveValue("en");
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    await page.goto(base() + "/tickets");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("th").filter({ hasText: "Category" })).toBeVisible();
    await expect(page.locator(".queue-tab").filter({ hasText: "Unassigned" })).toBeVisible();
    await expect(updated).toHaveText(/^(just now|\d+ (min|h|d) ago)$/);

    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("#ticket-detail .detail-state .badge.new")).toHaveText("New");
    await expect(page.locator("#ticket-detail .detail-meta time")).toHaveAttribute(
      "title",
      /^\d{2}:\d{2} · \d{2}-\d{2}-\d{4}$/,
    );
    await expect(page.locator("#ticket-detail .detail-meta time")).toHaveText(
      /^(just now|\d+ (min|h|d) ago)$/,
    );
  });

  // ------------------------------------------------------------------------
  // Email section (issue #362; specs S4, S5, S8 browser-visible parts).
  //
  // The Email section is root-only and persistent: root fills it, saves, and
  // reads the same values back after a reload; the test action reports the
  // mail server's refusal without ever carrying the password back (S8); a
  // saved password never renders again (S8). SMTP validation, the sealed
  // storage and the authorization matrix are Go-owned
  // (`handlers_settings_email_test.go`); these journeys prove only what the
  // browser can see.
  // ------------------------------------------------------------------------

  test("the Email notifications section saves the SMTP configuration and persists it after a reload", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/email");
    await expect(page.locator("h2").filter({ hasText: "Email notifications" })).toBeVisible();

    // An open internal relay (S2): no credentials, so no encryption is
    // required either (S15). Port 1 is deliberately a port nothing listens
    // on, and the values are filled rather than assumed, so the journey does
    // not depend on what an earlier run stored.
    await page.getByLabel("SMTP server", { exact: true }).fill("127.0.0.1");
    await page.getByLabel("Port", { exact: true }).fill("1");
    await page.getByLabel("Encryption", { exact: true }).selectOption("none");
    // S16: the certificate is verified by default, and opting out is an
    // explicit control with its warning on screen beside it.
    await expect(
      page.getByRole("checkbox", { name: "Do not verify the certificate" }),
    ).not.toBeChecked();
    await expect(
      page.getByText(/the connection is still encrypted, but TKT cannot check/),
    ).toBeVisible();
    await page.getByLabel("Sender address", { exact: true }).fill("tkt@example.com");
    await page.getByLabel("Sender name", { exact: true }).fill("TKT Notifications");
    await page.getByLabel("TKT address", { exact: true }).fill("https://tkt.example.com");
    await page.getByRole("button", { name: "Save email notifications" }).click();
    await expect(page).toHaveURL(/\/settings\/email$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // Persisted (S4): changing the configuration needs no restart, and a
    // reload renders the stored values rather than the submitted ones.
    await page.reload();
    await expect(page.getByLabel("SMTP server", { exact: true })).toHaveValue("127.0.0.1");
    await expect(page.getByLabel("Port", { exact: true })).toHaveValue("1");
    await expect(page.getByLabel("Encryption", { exact: true })).toHaveValue("none");
    await expect(page.getByLabel("Username", { exact: true })).toHaveValue("");
    await expect(page.getByLabel("Sender address", { exact: true })).toHaveValue("tkt@example.com");
    await expect(page.getByLabel("Sender name", { exact: true })).toHaveValue("TKT Notifications");
    await expect(page.getByLabel("TKT address", { exact: true })).toHaveValue(
      "https://tkt.example.com",
    );
    // Nothing was ever sealed, so no password is promised: an empty field with
    // the plain help text, not the "a password is saved" hint.
    await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");
    await expect(page.getByText("A password is saved; leave empty to keep it.")).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings email persist",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // The section is a new surface on a canonical screen, so it also owes the
    // narrow viewport its own pass: a filled row of SMTP fields must not clip
    // or overflow at 390px.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(base() + "/settings/email");
    await expect(page.locator("h2").filter({ hasText: "Email notifications" })).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "settings email at 390px",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a refused test email renders the localized delivery error and never the password", async ({
    page,
  }) => {
    test.setTimeout(45_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/email");

    // Fixture: a server that refuses the connection, reached with credentials.
    // Credentials force STARTTLS (S15/S16), so this journey also carries a
    // secret that no response is allowed to bring back (S8).
    const secret = `S3cret-Smtp-${Date.now().toString(36)}`;
    await page.getByLabel("SMTP server", { exact: true }).fill("127.0.0.1");
    await page.getByLabel("Port", { exact: true }).fill("1");
    await page.getByLabel("Encryption", { exact: true }).selectOption("starttls");
    await page.getByLabel("Username", { exact: true }).fill("tkt-smtp");
    await page.getByLabel("Password", { exact: true }).fill(secret);
    await page.getByLabel("Sender address", { exact: true }).fill("tkt@example.com");
    await page.getByLabel("TKT address", { exact: true }).fill("https://tkt.example.com");
    await page.getByRole("button", { name: "Save email notifications" }).click();
    await expect(page).toHaveURL(/\/settings\/email$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // "Send test email" addresses root's own mailbox with the STORED
    // configuration, so it never needs the form to be resubmitted.
    await page.getByRole("button", { name: "Send test email" }).click();
    await expect(page).toHaveURL(/\/settings\/email\/test$/);
    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(banner).toContainText("The mail server refused the test email:");
    await expect(banner).toContainText("connection refused");

    // S8: the refusal travels, the secret does not — not in the message, not
    // in any field of the re-rendered form.
    expect(await page.content()).not.toContain(secret);
    await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");
    await expect(page.getByText("A password is saved; leave empty to keep it.")).toBeVisible();
  });

  test("a saved password is never rendered back, only the hint that one is saved", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/email");

    const secret = `S3cret-Keep-${Date.now().toString(36)}`;
    await page.getByLabel("SMTP server", { exact: true }).fill("127.0.0.1");
    await page.getByLabel("Port", { exact: true }).fill("1");
    await page.getByLabel("Encryption", { exact: true }).selectOption("starttls");
    await page.getByLabel("Username", { exact: true }).fill("tkt-smtp");
    await page.getByLabel("Password", { exact: true }).fill(secret);
    await page.getByLabel("Sender address", { exact: true }).fill("tkt@example.com");
    await page.getByLabel("TKT address", { exact: true }).fill("https://tkt.example.com");
    await page.getByRole("button", { name: "Save email notifications" }).click();
    await expect(page).toHaveURL(/\/settings\/email$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // S8: the password is write-only. After a reload the field is empty, the
    // hint says a password is saved, and the stored username and mode are
    // still there, so root can change anything but the secret.
    await page.reload();
    await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");
    await expect(page.getByText("A password is saved; leave empty to keep it.")).toBeVisible();
    await expect(page.getByLabel("Username", { exact: true })).toHaveValue("tkt-smtp");
    await expect(page.getByLabel("Encryption", { exact: true })).toHaveValue("starttls");
    expect(await page.content()).not.toContain(secret);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings email saved password",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("an administrator's Settings shows no Email notifications section", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const admin = await createAdminUser(page);
    // The drawer the role change ran in stays open over the shell, so a full
    // navigation is what dismisses it before the session menu can be used.
    await page.goto(base() + "/users");
    await logout(page);
    await loginAs(page, admin.email, "SuperSecret42!");

    await page.goto(base() + "/settings/organization");
    // The administrator really is on Settings: the sections they do own render.
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();
    await expect(page.locator("h2").filter({ hasText: "Organization" })).toBeVisible();
    await page.goto(base() + "/settings/tickets");
    await expect(
      page.locator("h3").filter({ hasText: "Internal comment background" }),
    ).toBeVisible();
    // S1/S4: the Email section is root-only, so its nav entry is hidden and a
    // direct visit is refused.
    await expect(page.locator('.settings-nav a[href="/settings/email"]')).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Email notifications", exact: true })).toHaveCount(
      0,
    );
    await page.goto(base() + "/settings/email");
    await expect(page.locator("#email_host")).toHaveCount(0);
    await expect(page.getByLabel("SMTP server", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Send test email" })).toHaveCount(0);
    expect(await page.content()).not.toContain('settings-section-title">Email');
  });

  test("the Security section saves the password policy as one form", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/security");

    await expect(page.locator("h2").filter({ hasText: "Security" })).toBeVisible();
    const change = page.getByRole("checkbox", {
      name: "Allow people to change their own password from Preferences",
    });
    const recovery = page.getByRole("checkbox", { name: /Forgot your password\?/ });
    const minimum = page.getByLabel("Minimum password length", { exact: true });

    // A fresh instance: both switches on and the documented default minimum.
    await expect(change).toBeChecked();
    await expect(recovery).toBeChecked();
    await expect(minimum).toHaveValue("12");
    // The custom +/- buttons are the only visible steppers, including in Firefox.
    await expect(minimum).toHaveCSS("appearance", "textfield");
    // Nothing can send a recovery link yet, and the section says so next to the
    // switch it affects.
    await expect(page.locator(".dep-note")).toContainText("Email notifications are off");

    // One form: the switch, the stepper and nothing else. The stepper's buttons
    // move the number input, and the first change reveals the section's save bar.
    await change.uncheck();
    await page.getByRole("button", { name: "Increase minimum length" }).click();
    await expect(minimum).toHaveValue("13");
    await page.getByRole("button", { name: "Decrease minimum length" }).click();
    await expect(minimum).toHaveValue("12");
    await minimum.fill("13");
    await expect(minimum).toHaveValue("13");
    await page.getByRole("button", { name: "Increase minimum length" }).click();
    await expect(minimum).toHaveValue("14");
    const savebar = page.locator("[data-settings-savebar]");
    await expect(savebar).toBeVisible();

    await savebar.getByRole("button", { name: "Save password settings" }).click();
    await expect(page).toHaveURL(/\/settings\/security$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // The policy is what the next request renders, not what the form last held.
    await page.reload();
    await expect(
      page.getByRole("checkbox", {
        name: "Allow people to change their own password from Preferences",
      }),
    ).not.toBeChecked();
    await expect(page.getByRole("checkbox", { name: /Forgot your password\?/ })).toBeChecked();
    await expect(page.getByLabel("Minimum password length", { exact: true })).toHaveValue("14");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings security saved policy",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
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

    // Reach the page the way a user does: through the user menu, not by URL.
    await openPreferencesFromUserMenu(page);
    await expect(page.locator("h1").filter({ hasText: "Preferences" })).toBeVisible();
    // Issue #380: the subtitle repeated the heading, so it is gone.
    await expect(page.getByText(/personal settings/i)).toHaveCount(0);
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
    // The requester role has the user menu entry too (the page is not
    // capability-gated) and reaches the page through it.
    await openPreferencesFromUserMenu(page);
    await expect(page.locator("h1").filter({ hasText: "Preferences" })).toBeVisible();
    await expect(page.getByLabel("Default queue order")).toBeVisible();
  });

  test("a user turns email notifications off and back on from Preferences", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // S5: the opt-out belongs to each user, not to the instance — so the
    // journey runs as a plain requester, an actor who holds no settings
    // capability at all.
    const stamp = Date.now().toString(36);
    const email = await createUserAsAdmin(page, {
      name: `Preferences Email ${stamp}`,
      email: `preferences-email-${stamp}@example.com`,
      password: "SuperSecret42!",
    });
    await logout(page);
    await loginAs(page, email, "SuperSecret42!");

    await page.goto(base() + "/preferences");
    const toggle = page.getByRole("checkbox", { name: "Receive notifications by email" });
    // On by default (S14): the email adds to the bell, it does not replace it.
    await expect(toggle).toBeChecked();

    // Turned off, saved, and still off after a reload.
    await toggle.uncheck();
    await page.getByRole("button", { name: "Save email preference" }).click();
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(toggle).not.toBeChecked();

    // The email panel leaving the page alone does not take the rest of
    // Preferences with it: the other preference on this page still renders.
    await expect(page.getByLabel("Default queue order")).toBeVisible();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "preferences email off",
      url: page.url(),
      role: "user",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // And back on, so the journey proves the toggle writes both ways and
    // leaves the stored preference it found.
    await toggle.check();
    await page.getByRole("button", { name: "Save email preference" }).click();
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(toggle).toBeChecked();
  });

  test("a user changes their own password and every other session is signed out", async ({
    page,
    browser,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // A plain requester owns the password: the section is not capability-gated.
    const stamp = Date.now().toString(36);
    const email = `preferences-password-${stamp}@example.com`;
    const currentPassword = "SuperSecret42!";
    const newPassword = "EvenBetterPass42!";
    await createUserAsAdmin(page, {
      name: `Preferences Password ${stamp}`,
      email,
      password: currentPassword,
    });
    await logout(page);

    // The account is signed in on two devices: the page under test and a
    // second browser context the change must sign out.
    await loginAs(page, email, currentPassword);
    const otherContext = await browser.newContext();
    const otherPage = await otherContext.newPage();
    await loginAs(otherPage, email, currentPassword);
    await otherPage.goto(base() + "/preferences");
    await expect(otherPage.getByLabel("Default queue order")).toBeVisible();

    await page.goto(base() + "/preferences");
    await expect(page.getByLabel("Current password", { exact: true })).toBeVisible();
    await page.getByLabel("Current password", { exact: true }).fill(currentPassword);
    await page.getByLabel("New password", { exact: true }).fill(newPassword);
    await page.getByLabel("Confirm new password", { exact: true }).fill(newPassword);
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // The session that made the change keeps working, and the other device no
    // longer reaches an authenticated page.
    await page.reload();
    await expect(page.getByLabel("Default queue order")).toBeVisible();
    await otherPage.reload();
    await expect(otherPage).toHaveURL(/\/login/);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "preferences password changed",
      url: page.url(),
      role: "user",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // The old password is gone and the new one signs in.
    await logout(page);
    await loginAs(page, email, newPassword);
    await expect(page).toHaveURL(/\/tickets/);

    await otherContext.close();
  });

  test("pairs each label with its control and writes only on submit", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // Issue #438 adds Activity order alongside the four existing sections,
    // each pairing its label with its control and owning an explicit save.
    await page.goto(base() + "/preferences");
    await expect(page.locator(".preferences-screen")).toBeVisible();
    await expect(page.locator(".preferences-section")).toHaveCount(5);
    const saves = page.locator(".preferences-form button[type=submit]");
    await expect(saves).toHaveCount(5);
    await expect(saves.nth(0)).toHaveText("Save preferences");
    await expect(saves.nth(1)).toHaveText("Save theme");
    await expect(saves.nth(2)).toHaveText("Save activity order");
    await expect(saves.nth(3)).toHaveText("Save email preference");
    await expect(saves.nth(4)).toHaveText("Change password");

    // S3: a select change alone never saves. The value is only written by the
    // section's own submit, so the stored order survives a reload untouched.
    let writes = 0;
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      if (!new URL(request.url()).pathname.startsWith("/preferences")) return;
      writes += 1;
    });
    await page.getByLabel("Default queue order").selectOption("urgency");
    await page.getByLabel("Default activity order", { exact: true }).selectOption("oldest");
    await page.reload();
    await expect(page.getByLabel("Default queue order")).toHaveValue("newest");
    await expect(page.getByLabel("Default activity order", { exact: true })).toHaveValue("newest");
    expect(writes, "choosing an option must not save it").toBe(0);

    // 390px: the sections stack, nothing overflows, nothing is clipped.
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "preferences sections @ 390px",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
  });

  // ------------------------------------------------------------------------
  // Sub-navigation and the sticky save bar (issue #383, S1/S2).
  //
  // The settings surface is one section per URL under a left sub-navigation,
  // and each section's form owns a save bar that appears with the first change
  // and disappears again with the discard control or a completed save. The
  // section's own controls, validation and capabilities are Go-owned; these
  // journeys prove what the browser can see.
  // ------------------------------------------------------------------------

  test("the section rail marks the active section and every section has its own URL", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    // An unknown section is not a page: the URL answers 404. This runs before
    // observability is collected, because a deliberate refusal logs one.
    const missing = await page.goto(base() + "/settings/bogus");
    expect(missing?.status()).toBe(404);

    const obs = collectObservability(page);
    await page.goto(base() + "/settings");
    await expect(page).toHaveURL(/\/settings\/organization$/);
    await expect(page.locator("h1").filter({ hasText: "Settings" })).toBeVisible();

    const nav = page.locator("nav.settings-nav");
    await expect(nav).toBeVisible();
    const entries = [
      { label: "Organization", url: /\/settings\/organization$/ },
      { label: "Sign-in page", url: /\/settings\/signin$/ },
      { label: "Language", url: /\/settings\/language$/ },
      { label: "Tickets & SLA", url: /\/settings\/tickets$/ },
      { label: "Email notifications", url: /\/settings\/email$/ },
      { label: "Security", url: /\/settings\/security$/ },
      { label: "Working calendar", url: /\/settings\/calendar$/ },
    ];
    for (const entry of entries) {
      const link = nav.getByRole("link", { name: entry.label, exact: true });
      await expect(link).toBeVisible();
      await link.click();
      await expect(page).toHaveURL(entry.url);
      await expect(nav.getByRole("link", { name: entry.label, exact: true })).toHaveAttribute(
        "aria-current",
        "page",
      );
      await expect(nav.locator('[aria-current="page"]')).toHaveCount(1);
    }

    await page.goto(base() + "/settings/organization");
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings sub-navigation",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a section's save bar appears on the first change, hides on discard, and is gone after a save", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/organization");

    const savebar = page.locator("[data-settings-savebar]");
    const name = page.getByLabel("Name", { exact: true });
    const stored = await name.inputValue();

    // Nothing changed yet: the bar is not offered.
    await expect(savebar).toBeHidden();

    // The first keystroke reveals it, named by the section it saves.
    await name.fill("Acme Support");
    await expect(savebar).toBeVisible();
    await expect(savebar.getByRole("button", { name: "Save organization" })).toBeVisible();

    // Discard restores the stored value and takes the bar away with it.
    await savebar.getByRole("button", { name: "Discard" }).click();
    await expect(savebar).toBeHidden();
    await expect(name).toHaveValue(stored);

    // A file input counts as a change too: choosing a logo reveals the bar.
    await page.getByLabel("Logo", { exact: true }).setInputFiles(tinyPNG());
    await expect(savebar).toBeVisible();

    // Discard also drops the chosen file, so the form is back to stored state.
    await savebar.getByRole("button", { name: "Discard" }).click();
    await expect(savebar).toBeHidden();
  });

  test("the organization identity reaches the shell, the document title and the sign-in page", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/organization");

    // One submit carries the three text fields.
    await page.getByLabel("Name", { exact: true }).fill("Acme Support");
    await page.getByLabel("Help desk name", { exact: true }).fill("Service desk");
    await page.getByLabel("Support email", { exact: true }).fill("support@acme.test");
    await expect(page.locator("[data-settings-savebar]")).toBeVisible();
    await page.getByRole("button", { name: "Save organization" }).click();
    await expect(page).toHaveURL(/\/settings\/organization$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // S5: the shell renders the stored identity, and the document title ends
    // with the help desk name instead of the product name.
    const brand = page.locator(".sidebar-brand");
    await expect(brand.locator(".brand-name")).toHaveText("Acme Support");
    await expect(brand.locator(".brand-product")).toHaveText("Service desk");
    expect(await page.title()).toBe("Settings · Service desk");

    // Persisted: a reload reads the stored identity, not the submitted one.
    await page.reload();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Acme Support");
    await expect(page.getByLabel("Help desk name", { exact: true })).toHaveValue("Service desk");
    await expect(page.getByLabel("Support email", { exact: true })).toHaveValue(
      "support@acme.test",
    );

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings organization identity",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // S6: the sign-in page is the identity's: the name and the help desk name
    // above the form, and the support address as the footer line.
    await logout(page);
    await expect(page.locator("main.auth-entry")).toBeVisible();
    await expect(page.locator("main.auth-entry .auth-entry__brand-text b")).toHaveText(
      "Acme Support",
    );
    await expect(page.locator("main.auth-entry .auth-entry__brand-text span")).toHaveText(
      "Service desk",
    );
    await expect(page.getByRole("link", { name: "support@acme.test" })).toBeVisible();
    expect(await page.title()).toBe("Sign in · Service desk");
    await loginAsSeeded(page);

    // The address the footer offers is the one the section stored, so restore
    // the instance identity for the journeys that follow this one.
    await page.goto(base() + "/settings/organization");
    await page.getByLabel("Name", { exact: true }).fill("tkt");
    await page.getByLabel("Help desk name", { exact: true }).fill("Help desk");
    await page.getByLabel("Support email", { exact: true }).fill("");
    await page.getByRole("button", { name: "Save organization" }).click();
    await expect(page).toHaveURL(/\/settings\/organization$/);
    await expect(page.locator(".sidebar-brand .brand-name")).toHaveText("tkt");
  });

  test("the Sign-in page section mirrors the panel live, saves it, and uploads and removes its background image", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/signin");
    await expect(page.locator("h2").filter({ hasText: "Sign-in page" })).toBeVisible();

    // The sticky save bar and the live preview: typing changes both.
    const savebar = page.locator('form[action="/settings/signin"] [data-settings-savebar]');
    await expect(savebar).toBeHidden();
    await page.locator("#signin_title").fill("Sistemas, a un pedido");
    await expect(savebar).toBeVisible();
    await expect(page.locator("[data-preview-title-text]")).toHaveText("Sistemas, a un pedido");
    await page.locator("#signin_subtitle").fill("Cargá tu pedido y seguí su estado.");
    await expect(page.locator("[data-preview-subtitle-text]")).toHaveText(
      "Cargá tu pedido y seguí su estado.",
    );

    // Progressive disclosure: each option shows only its own fields, at once,
    // without a save. Steps and None hide the highlight rows.
    const highlightRows = page.locator('[data-show-when="content=highlights"]');
    const noticeFields = page.locator('[data-show-when="notice_enabled=checked"]');
    const brandField = page.locator('[data-show-when="background=brand"]');
    const imageField = page.locator('[data-show-when="background=image"]');
    await expect(highlightRows).toBeHidden();
    await expect(noticeFields).toBeHidden();
    await expect(brandField).toBeHidden();
    await expect(imageField).toBeHidden();
    await page.locator('label:has(input[name="content"][value="none"])').click();
    await expect(highlightRows).toBeHidden();
    // Dark shows neither colour nor image; brand shows only the colour; image
    // shows only the upload.
    await page.locator('label:has(input[name="background"][value="brand"])').click();
    await expect(brandField).toBeVisible();
    await expect(imageField).toBeHidden();
    await page.locator('label:has(input[name="background"][value="image"])').click();
    await expect(imageField).toBeVisible();
    await expect(brandField).toBeHidden();
    await page.locator('label:has(input[name="background"][value="dark"])').click();
    await expect(brandField).toBeHidden();
    await expect(imageField).toBeHidden();

    // The middle zone follows the segmented control.
    await page.locator('label:has(input[name="content"][value="steps"])').click();
    await expect(page.locator('[data-preview-zone="steps"]')).toBeVisible();
    await page.locator('label:has(input[name="content"][value="highlights"])').click();
    await expect(highlightRows).toBeVisible();
    await expect(page.locator('[data-preview-zone="steps"]')).toBeHidden();
    await page.locator("#highlight_0_title").fill("Primera respuesta en 4 h");
    await expect(page.locator('[data-preview-hl-row="0"]')).toBeVisible();
    await expect(page.locator('[data-preview-hl-title-text="0"]')).toHaveText(
      "Primera respuesta en 4 h",
    );

    // The notice appears in the preview when the toggle is on.
    await expect(page.locator("[data-preview-notice]")).toBeHidden();
    await page.getByRole("checkbox", { name: /show a notice/i }).check();
    await expect(noticeFields).toBeVisible();
    await page.locator("#signin_notice_title").fill("Mantenimiento programado");
    await expect(page.locator("[data-preview-notice]")).toBeVisible();
    await expect(page.locator("[data-preview-notice-title-text]")).toHaveText(
      "Mantenimiento programado",
    );

    // The contact facts the panel footer shows.
    await page.locator("#signin_support_phone").fill("Interno 4400");
    await expect(page.locator("[data-preview-phone-block]")).toBeVisible();
    await expect(page.locator("[data-preview-phone-text]")).toHaveText("Interno 4400");

    // One submit stores the panel.
    await page.getByRole("button", { name: "Save sign-in page" }).click();
    await expect(page).toHaveURL(/\/settings\/signin$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    // A real PNG uploads in the same submit and is served by its own route.
    await page.locator('label:has(input[name="background"][value="image"])').click();
    await page.locator("#signin_background").setInputFiles(tinyPNG());
    await page.getByRole("button", { name: "Save sign-in page" }).click();
    await expect(page).toHaveURL(/\/settings\/signin$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    const preview = page.locator(".settings-logo-mark img");
    await expect(preview).toBeVisible();
    const src = await preview.getAttribute("src");
    expect(src).toMatch(/^\/brand\/signin-background\?v=[0-9a-f]+$/);
    const served = await page.request.get(base() + src!);
    expect(served.status()).toBe(200);
    expect(served.headers()["content-type"]).toBe("image/png");
    expect(served.headers()["x-content-type-options"]).toBe("nosniff");
    expect(served.headers()["cache-control"]).toBe("public, max-age=86400");

    // A text file named .jpg is refused, and the stored image is untouched.
    await page.locator("#signin_background").setInputFiles({
      name: "background.jpg",
      mimeType: "image/jpeg",
      buffer: Buffer.from("this is not an image"),
    });
    await page.getByRole("button", { name: "Save sign-in page" }).click();
    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(banner).toContainText("the background must be a PNG or JPEG image");
    await expect(page).toHaveURL(/\/settings\/signin$/);
    await page.reload();
    await expect(page.locator(".settings-logo-mark img")).toBeVisible();

    // Remove is its own route: the section stops previewing an image.
    await page.getByRole("button", { name: "Remove image" }).click();
    await expect(page).toHaveURL(/\/settings\/signin$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await expect(page.locator(".settings-logo-mark img")).toHaveCount(0);
    const gone = await page.request.get(base() + "/brand/signin-background");
    expect(gone.status()).toBe(404);

    // Restore the default panel for the journeys that follow.
    await page.goto(base() + "/settings/signin");
    await page.locator("#signin_title").fill("");
    await page.locator("#signin_subtitle").fill("");
    await page.locator('label:has(input[name="content"][value="steps"])').click();
    await page.getByRole("checkbox", { name: /show a notice/i }).uncheck();
    await page.locator('label:has(input[name="background"][value="dark"])').click();
    await page.locator("#signin_support_phone").fill("");
    await page.getByRole("button", { name: "Save sign-in page" }).click();
    await expect(page).toHaveURL(/\/settings\/signin$/);
  });

  /**
   * Issue #383, S11: field help is a "?" button that opens on hover and on
   * keyboard focus, toggles on click, and closes on Escape and on an outside
   * click — while the security warning and the saved-password state stay
   * visible under their fields.
   */
  test("field help opens as a toggletip on hover, focus, click and closes on Escape", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/email");
    await expect(page.locator("h2").filter({ hasText: "Email notifications" })).toBeVisible();

    // Issue #380: the SMTP server's address is what its label says, so that
    // field carries no toggletip; the Username field's is the one exercised.
    await expect(page.locator('button[aria-describedby="help-tip-email-host"]')).toHaveCount(0);
    await expect(page.locator("#help-tip-email-host")).toHaveCount(0);

    const tip = page.locator('button[aria-describedby="help-tip-email-username"]');
    const bubble = page.locator("#help-tip-email-username");
    await expect(tip).toBeVisible();
    await expect(tip).toHaveAttribute("aria-label", "More about Username");
    await expect(bubble).toBeHidden();

    // Hover opens it.
    await tip.hover();
    await expect(bubble).toBeVisible();
    await expect(bubble).toContainText(
      "Leave it empty if your server accepts mail without authentication.",
    );

    // Moving away closes the hover-only state.
    await page.locator("#email_username").hover();
    await expect(bubble).toBeHidden();

    // Keyboard focus opens it too.
    await tip.focus();
    await expect(bubble).toBeVisible();

    // A click toggles: the focus had already opened it, so the first click
    // collapses it and the second pins it open again.
    await tip.click();
    await expect(tip).toHaveAttribute("aria-expanded", "false");
    await tip.click();
    await expect(tip).toHaveAttribute("aria-expanded", "true");
    await expect(bubble).toBeVisible();

    // Escape collapses it and leaves the focus on the control.
    await page.keyboard.press("Escape");
    await expect(tip).toHaveAttribute("aria-expanded", "false");
    await page.locator("#email_username").hover();
    await expect(bubble).toBeHidden();

    // A click opens it, a click outside closes it.
    await tip.click();
    await expect(bubble).toBeVisible();
    await page.locator("#email_username").click();
    await expect(tip).toHaveAttribute("aria-expanded", "false");
    await expect(bubble).toBeHidden();

    // The explanations the interface already gives are gone, and the two
    // exceptions stay readable without asking.
    await expect(page.getByText("25 by default")).toHaveCount(0);
    await expect(page.getByText("While off, TKT sends nothing.")).toHaveCount(0);
    await expect(
      page.getByText(/the connection is still encrypted, but TKT cannot check/),
    ).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings email toggletips",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a logo uploads, renders in the shell, refuses a text file named .png, and can be removed", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/organization");

    // Fixture: a known identity, so the logo is the only thing that moves.
    await page.getByLabel("Name", { exact: true }).fill("Acme Support");
    await page.getByLabel("Help desk name", { exact: true }).fill("Service desk");
    await page.getByRole("button", { name: "Save organization" }).click();
    await expect(page).toHaveURL(/\/settings\/organization$/);

    // Upload a real PNG: the shell marks the brand with the image, and the
    // section stops offering the monogram.
    await page.getByLabel("Logo", { exact: true }).setInputFiles(tinyPNG());
    await page.getByRole("button", { name: "Save organization" }).click();
    await expect(page).toHaveURL(/\/settings\/organization$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    const mark = page.locator(".sidebar-brand .brand-mark img");
    await expect(mark).toBeVisible();
    await expect(mark).toHaveAttribute("src", /^\/brand\/logo\?v=[0-9a-f]+$/);
    const served = await page.request.get(base() + (await mark.getAttribute("src"))!);
    expect(served.status()).toBe(200);
    expect(served.headers()["content-type"]).toBe("image/png");
    expect(served.headers()["x-content-type-options"]).toBe("nosniff");
    expect(served.headers()["cache-control"]).toBe("public, max-age=86400");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "settings organization logo",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // A text file named .png is refused: the message says which rule failed
    // and the stored logo is untouched.
    await page.getByLabel("Logo", { exact: true }).setInputFiles({
      name: "logo.png",
      mimeType: "image/png",
      buffer: Buffer.from("this is not a png"),
    });
    await page.getByRole("button", { name: "Save organization" }).click();
    const banner = page.locator(".error-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "alert");
    await expect(banner).toContainText("the logo must be a PNG image");
    await expect(page).toHaveURL(/\/settings\/organization$/);
    // The re-render still previews the STORED logo, not the refused bytes.
    await page.reload();
    await expect(page.locator(".sidebar-brand .brand-mark img")).toBeVisible();

    // Remove is its own endpoint: one click, back to the monogram.
    await page.getByRole("button", { name: "Remove logo" }).click();
    await expect(page).toHaveURL(/\/settings\/organization$/);
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await expect(page.locator(".sidebar-brand .brand-mark img")).toHaveCount(0);
    await expect(page.locator(".sidebar-brand .brand-mark")).toHaveText("AS");
    await expect(page.getByRole("button", { name: "Remove logo" })).toHaveCount(0);

    // Restore the instance identity and its monogram for the journeys after.
    await page.goto(base() + "/settings/organization");
    await page.getByLabel("Name", { exact: true }).fill("tkt");
    await page.getByLabel("Help desk name", { exact: true }).fill("Help desk");
    await page.getByRole("button", { name: "Save organization" }).click();
    await expect(page).toHaveURL(/\/settings\/organization$/);
  });
});

/** A real 1x1 PNG, so the uploaded logo renders and the served bytes are valid. */
function tinyPNG(): { name: string; mimeType: string; buffer: Buffer } {
  return {
    name: "logo.png",
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
      "base64",
    ),
  };
}

/**
 * Issue #396 (S3/S5): Preferences must stay usable with scripting off — the
 * three preferences are native forms, so an explicit save is an ordinary POST
 * and the stored value is server truth.
 */
test.describe("Preferences without scripting (seeded)", () => {
  test.use({ javaScriptEnabled: false });

  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("an explicit save persists without a script", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    await page.goto(base() + "/preferences");
    await expect(page.locator(".preferences-section")).toHaveCount(5);
    await page.getByLabel("Default queue order").selectOption("priority");
    await page.getByRole("button", { name: "Save preferences" }).click();
    await expect(page).toHaveURL(/\/preferences$/);
    await page.reload();
    await expect(page.getByLabel("Default queue order")).toHaveValue("priority");
    // The queue the preference orders is the server's own list.
    await page.goto(base() + "/tickets");
    await expect(page.getByLabel("Order by")).toHaveValue("priority");

    // Back to the default so the seeded profile is left as it was found.
    await page.goto(base() + "/preferences");
    await page.getByLabel("Default queue order").selectOption("newest");
    await page.getByRole("button", { name: "Save preferences" }).click();
    await expect(page).toHaveURL(/\/preferences$/);
    await page.reload();
    await expect(page.getByLabel("Default queue order")).toHaveValue("newest");

    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });
});
