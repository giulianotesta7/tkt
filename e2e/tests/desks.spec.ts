/**
 * Desk compatibility and membership journeys.
 *
 * Desk administration is covered by the unified Categories/Structure screen;
 * this file owns only the legacy redirect and membership routes used by that
 * screen's drawer.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";

async function selectDesk(page: import("@playwright/test").Page, name: string): Promise<string> {
  await page.goto(base() + "/categories?view=structure");
  const departments = page.locator(".category-level-departments .category-structure-row");
  for (let i = 0; i < await departments.count(); i += 1) {
    await page.goto(base() + "/categories?view=structure");
    await departments.nth(i).click();
    const desk = page.locator(".category-level-desks .category-structure-row").filter({ has: page.getByText(name, { exact: true }) });
    if (await desk.count() !== 1) continue;
    const href = await desk.getAttribute("href");
    if (!href) throw new Error(`Desk link href missing for "${name}" at ${page.url()}`);
    const deskID = new URL(href, page.url()).searchParams.get("desk_id");
    if (!deskID || !/^\d+$/.test(deskID)) {
      throw new Error(`Could not resolve exact desk ID for "${name}" from ${href} at ${page.url()}`);
    }
    await desk.click();
    return deskID;
  }
  throw new Error(`Expected exactly one desk "${name}" in the catalog at ${page.url()}`);
}

test.describe("Desks", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("legacy GET redirects to the unified Categories screen", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/desks");
    await expect(page).toHaveURL(/\/categories$/);
    await expect(page.locator("h1")).toHaveText("Categories");
    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    await expect(page.locator(".category-level-categories")).toBeVisible();
  });

  test("desk membership add and remove uses the unified drawer context", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const uname = "Desk Member " + Date.now().toString(36).slice(2, 8);
    const uemail = `desk-member-${Date.now().toString(36).slice(2, 8)}@example.com`;
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(uname);
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill("Secret123!");
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users/);

    const userRow = page.locator(`tr[data-user-name="${uname}"]`);
    await expect(userRow).toHaveCount(1);
    const editHref = await userRow.locator('a[href*="/users/"][href*="/edit"]').first().getAttribute("href");
    if (!editHref) throw new Error(`edit href missing for user "${uname}" at ${page.url()}`);
    await page.goto(base() + editHref.split("?")[0]);
    await page.locator('select[name="role"]').selectOption("agent");
    await page.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/users$/);

    await selectDesk(page, "General Support");
    const drawerLink = page.locator('.category-level-desks .category-menu-button').first();
    await drawerLink.click();
    await page.locator('.category-level-desks .category-overflow-menu:not([hidden])').getByRole("menuitem", { name: "Edit desk", exact: true }).click();
    const drawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(drawer).toBeVisible();
    const addSelect = drawer.locator(".desk-add-member select");
    await expect(addSelect).toBeVisible();
    await addSelect.selectOption({ label: uname });
    const addAction = await drawer.locator(".desk-add-member").getAttribute("action");
    if (!addAction) throw new Error(`add-member form action missing at ${page.url()}`);
    const addResponse = await assertHtmxSwap(
      page,
      () => drawer.locator(".desk-add-member button").click(),
      {
        endpoint: new URL(addAction, page.url()).pathname,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#category-drawer-host",
      },
    );
    expect(addResponse.headers()["hx-retarget"]).toBe("#category-drawer-host");
    expect(addResponse.headers()["hx-reswap"]).toBe("outerHTML");
    await expect(drawer).toBeVisible();
    await expect(drawer.locator(".desk-member-list li").filter({ hasText: uname })).toHaveCount(1);

    const memberRow = drawer.locator(".desk-member-list li").filter({ hasText: uname });
    const removeForm = memberRow.locator("form");
    const removeAction = await removeForm.getAttribute("action");
    if (!removeAction) throw new Error(`remove-member form action missing at ${page.url()}`);
    const removeResponse = await assertHtmxSwap(
      page,
      () => memberRow.getByRole("button", { name: /remove/i }).click(),
      {
        endpoint: new URL(removeAction, page.url()).pathname,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#category-drawer-host",
      },
    );
    expect(removeResponse.headers()["hx-retarget"]).toBe("#category-drawer-host");
    expect(removeResponse.headers()["hx-reswap"]).toBe("outerHTML");
    await expect(drawer).toBeVisible();
    await expect(drawer.locator(".desk-member-list li").filter({ hasText: uname })).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "desk membership compatibility",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
