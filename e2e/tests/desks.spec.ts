/**
 * Desk compatibility and membership journeys.
 *
 * Desk administration is covered by the unified Categories/Structure screen;
 * this file owns only the legacy redirect and membership routes used by that
 * screen's drawer.
 */

import { test, expect, type Route } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";

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
    const profileActions = drawer.locator("#category-drawer-form .users-drawer-footer");
    const members = drawer.locator(".desk-members");
    await expect(profileActions).toHaveCSS("border-top-style", "none");
    await expect(members).toContainText("Member changes are saved immediately.");
    const addSelect = drawer.locator(".desk-add-member select");
    await expect(addSelect).toBeVisible();
    const name = drawer.getByLabel("Name", { exact: true });
    const description = drawer.getByLabel("Description", { exact: true });
    const pendingName = "Unsaved desk " + Date.now().toString(36).slice(2, 8);
    const pendingDescription = "Preserve this across member updates.";
    await name.fill(pendingName);
    await description.fill(pendingDescription);
    await addSelect.selectOption({ label: uname });
    const addAction = await drawer.locator(".desk-add-member").getAttribute("action");
    if (!addAction) throw new Error(`add-member form action missing at ${page.url()}`);
    const addPath = new URL(addAction, page.url()).pathname;
    let responseIntercepted!: () => void;
    let releaseResponse!: () => void;
    const responseReady = new Promise<void>((resolve) => {
      responseIntercepted = resolve;
    });
    await page.route((url) => url.pathname === addPath, async (route) => {
      const response = await route.fetch();
      const responseHeld = new Promise<void>((resolve) => {
        releaseResponse = resolve;
      });
      responseIntercepted();
      await responseHeld;
      await route.fulfill({ response });
    });
    const addResponsePromise = assertHtmxSwap(
      page,
      () => drawer.locator(".desk-add-member button").click(),
      {
        endpoint: addPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#category-drawer-host",
      },
    );
    await responseReady;
    const inFlightName = "Latest desk " + Date.now().toString(36).slice(2, 8);
    const inFlightDescription = "Changed while the member response was pending.";
    await name.fill(inFlightName);
    await description.fill(inFlightDescription);
    releaseResponse();
    const addResponse = await addResponsePromise;
    await page.unroute((url) => url.pathname === addPath);
        expect(addResponse.headers()["hx-retarget"]).toBe("#category-drawer-host");
        expect(addResponse.headers()["hx-reswap"]).toBe("outerHTML");
        expect(addResponse.headers()["x-save-feedback"]).toContain('"message":"Saved"');
        await expect(drawer).toBeVisible();
        const addToast = page.locator("#save-feedback");
        await expect(addToast).toBeVisible();
        await expect(addToast.locator(".save-feedback-message")).toHaveText("Saved");
        await expect(addToast).toHaveClass(/(?:^|\s)is-visible(?:\s|$)/);
        const toastLayering = await addToast.evaluate((element) => ({
          toastZ: parseInt(getComputedStyle(element).zIndex, 10),
          toastTop: parseFloat(getComputedStyle(element).top),
          toastRectRight: element.getBoundingClientRect().right,
          viewportWidth: window.innerWidth,
        }));
        const drawerZ = await drawer.evaluate((element) => parseInt(getComputedStyle(element).zIndex, 10));
        expect(toastLayering.toastZ).toBeGreaterThan(drawerZ);
        expect(toastLayering.toastTop).toBeGreaterThanOrEqual(0);
        expect(toastLayering.toastRectRight).toBeLessThanOrEqual(toastLayering.viewportWidth);
        // The drawer stays usable while the toast is up.
        await expect(name).toBeEditable();
        await expect(drawer.getByRole("button", { name: "Close catalog details" })).toBeEnabled();
        await expect(page.locator("#save-feedback")).toHaveCount(1);
    await expect(drawer.locator(".desk-member-list li").filter({ hasText: uname })).toHaveCount(1);
    await expect(name).toHaveValue(inFlightName);
    await expect(description).toHaveValue(inFlightDescription);

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
    expect(removeResponse.headers()["x-save-feedback"]).toContain('"message":"Saved"');
    await expect(drawer).toBeVisible();
    const removeToast = page.locator("#save-feedback");
    await expect(removeToast).toBeVisible();
    await expect(removeToast.locator(".save-feedback-message")).toHaveText("Saved");
    await expect(page.locator("#save-feedback")).toHaveCount(1);
    await page.waitForTimeout(5_100);
    await expect(removeToast).toBeHidden();
    await expect(drawer.locator(".desk-member-list li").filter({ hasText: uname })).toHaveCount(0);
    await expect(name).toHaveValue(inFlightName);
    await expect(description).toHaveValue(inFlightDescription);

    await drawer.getByRole("button", { name: "Close catalog details" }).click();
    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    await expect(confirmation).toBeVisible();
    await confirmation
      .getByRole("button", { name: "Discard changes", exact: true })
      .click();
    await expect(drawer).toHaveCount(0);

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

  test("aborted desk membership changes show one persistent drawer-local error", async ({ page }) => {
    test.setTimeout(60_000);
    await loginAsSeeded(page);
    let deskID = "";
    let deskDepartmentID = "";
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 844 });

      const suffix = `${width}-${Date.now().toString(36).slice(2, 8)}`;
      const name = `Drawer member ${suffix}`;
      await page.goto(base() + "/users/new");
      await page.getByLabel(/^name$/i).fill(name);
      await page.getByLabel(/^email$/i).fill(`drawer-member-${suffix}@example.com`);
      await page.getByLabel(/^password$/i).fill("Secret123!");
      await page.getByRole("button", { name: /create user/i }).click();
      await expect(page).toHaveURL(/\/users/);
      const row = page.locator(`tr[data-user-name="${name}"]`);
      const editHref = await row.locator('a[href*="/users/"][href*="/edit"]').first().getAttribute("href");
      if (!editHref) throw new Error(`edit href missing for ${name} at ${page.url()}`);
      await page.goto(base() + editHref.split("?")[0]);
      await page.locator('select[name="role"]').selectOption("agent");
      await page.getByRole("button", { name: /save changes/i }).click();
      await expect(page).toHaveURL(/\/users$/);

      if (!deskID) {
        deskID = await selectDesk(page, "General Support");
        deskDepartmentID = new URL(page.url()).searchParams.get("department_id") ?? "";
        if (!deskDepartmentID) throw new Error(`General Support department missing at ${page.url()}`);
      }
      await page.goto(base() + `/categories/desks/${deskID}/edit?view=structure&department_id=${deskDepartmentID}&desk_id=${deskID}`);
      const drawer = page.getByRole("dialog", { name: /Edit desk/i });
      await expect(drawer).toBeVisible();
      const addMember = drawer.locator(".desk-add-member");
      const addAction = await addMember.getAttribute("action");
      if (!addAction) throw new Error(`add-member action missing at ${page.url()}`);
      const addPath = new URL(addAction, page.url()).pathname;
      const addResponse = await assertHtmxSwap(
        page,
        async () => {
          await addMember.locator("select").selectOption({ label: name });
          await addMember.getByRole("button").click();
        },
        { endpoint: addPath, method: "POST", expectedStatus: 200, hxTarget: "#category-drawer-host" },
      );
      expect(new URLSearchParams(addResponse.request().postData() ?? "").get("user_id")).not.toBe("");
      const addToast = page.locator("#save-feedback");
      await expect(addToast).toBeVisible();
      await expect(addToast.locator(".save-feedback-message")).toHaveText("Saved");

      const member = drawer.locator(".desk-member-list li").filter({ hasText: name });
      const removeForm = member.locator("form");
      const removeAction = await removeForm.getAttribute("action");
      if (!removeAction) throw new Error(`remove-member action missing at ${page.url()}`);
      const removePath = new URL(removeAction, page.url()).pathname;
      let removalAborted = false;
      const abortRemoval = async (route: Route) => {
        const request = route.request();
        if (isHtmxPost(request, { path: removePath, target: "#category-drawer-host" })) {
          removalAborted = true;
          await route.abort("failed");
          return;
        }
        await route.continue();
      };
      await page.route((url) => url.pathname === removePath, abortRemoval);
      try {
        const removalRequest = page.waitForRequest((request) =>
          isHtmxPost(request, { path: removePath, target: "#category-drawer-host" }),
        );
        await member.getByRole("button", { name: /remove/i }).click();
        const request = await removalRequest;
        expect(request.headers()["hx-request"]).toBe("true");
        expect(request.headers()["hx-target"]).toBe("category-drawer-host");
        await expect.poll(() => removalAborted).toBe(true);
        const feedback = drawer.locator("#save-feedback");
        await expect(feedback.locator(".save-feedback-message")).toHaveText("Unable to save changes. Please try again.");
        await expect(feedback).toBeVisible();
        await expect(feedback).toHaveAttribute("role", "alert");
        await expect(feedback).toHaveAttribute("aria-live", "assertive");
        await expect(feedback).toHaveClass(/error-banner/);
        await expect(feedback).not.toHaveClass(/(?:^|\s)save-feedback(?:\s|$)/);
        // The failure stays local to the active drawer, not on the fixed toast layer.
        expect(await feedback.evaluate((element) => element.closest(".users-drawer"))).not.toBeNull();
        await expect(page.locator("#save-feedback")).toHaveCount(1);
        expect(await feedback.evaluate((element) => element.closest("[inert]") === null)).toBe(true);
        await expect(drawer.getByRole("button", { name: "Close catalog details" })).toBeEnabled();
        await expect(drawer.getByLabel("Name", { exact: true })).toBeVisible();
        await expect(member.getByRole("button", { name: /remove/i })).toBeEnabled();
        await page.waitForTimeout(5_100);
        await expect(feedback).toBeVisible();
        await expect(feedback.locator(".save-feedback-message")).not.toContainText("Saved");
      } finally {
        await page.unroute((url) => url.pathname === removePath, abortRemoval);
      }

      await drawer.getByRole("button", { name: "Close catalog details" }).click();
      await expect(drawer).toHaveCount(0);
      const pageFeedback = page.locator("#save-feedback");
      await expect(pageFeedback).toBeHidden();
    }
  });
});
