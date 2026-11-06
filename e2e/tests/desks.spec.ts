/**
 * Desk journeys.
 *
 * Desks are teams with their own screen again (issue #484): /desks lists them
 * and its drawer creates, renames and staffs a desk. The membership routes also
 * serve the desk drawer the Categories screen still opens.
 */

import { test, expect, type Route } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";
import { catalogRowMenu, createDeskViaUi, selectCatalogDesk } from "./helpers/navigation.js";
import { expectPopoverInView } from "./helpers/popover.js";

test.describe("Desks", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the desks screen lists desks and creates one in its drawer", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/desks");
    await expect(page).toHaveURL(/\/desks$/);
    await expect(page.locator("h1")).toHaveText("Desks");
    await expect(page.getByRole("link", { name: "Desks", exact: true })).toHaveAttribute(
      "aria-current",
      "page",
    );
    const general = page
      .locator("tr[data-desk-id]")
      .filter({ has: page.getByRole("link", { name: "General", exact: true }) });
    await expect(general).toHaveCount(1);

    const name = "Facilities " + Date.now().toString(36).slice(2, 7);
    await page.getByRole("link", { name: "New desk" }).click();
    const drawer = page.locator(".category-drawer");
    await expect(drawer).toBeVisible();
    await drawer.getByLabel(/^name$/i).fill(name);
    await assertHtmxSwap(
      page,
      async () => {
        await drawer.getByRole("button", { name: /create desk/i }).click();
      },
      {
        endpoint: (url) => new URL(url).pathname === "/desks",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#categories-background",
        expectedUrl: /\/desks$/,
      },
    );
    const created = page
      .locator("tr[data-desk-id]")
      .filter({ has: page.getByRole("link", { name, exact: true }) });
    await expect(created).toHaveCount(1);
    await expect(created.locator("td").nth(1)).toHaveText("No agents");
    await expect(page.locator(".category-drawer")).toHaveCount(0);
    expect(obs.pageErrors).toEqual([]);
  });

  test("last desk row menu escapes the rounded table", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    let id = "";
    for (let index = 0; index < 5; index++) {
      id = await createDeskViaUi(page, `ZZ Overlay desk ${index}`);
    }
    const row = page.locator(`tr[data-desk-id="${id}"]`);
    const trigger = catalogRowMenu(row);
    const menu = row.getByRole("menu");
    await expect(page.locator(".desks-list")).toHaveCSS("overflow", "hidden");
    await trigger.click();
    await expectPopoverInView(menu);
    await expect(trigger).toHaveAttribute("aria-expanded", "true");
    await trigger.press("ArrowDown");
    await expect(menu.getByRole("menuitem").first()).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("desk membership add and remove stays in the desk drawer", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const uname = "Desk Member " + Date.now().toString(36).slice(2, 8);
    const uemail = `desk-member-${Date.now().toString(36).slice(2, 8)}@example.com`;
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(uname);
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill("Secret123!42");
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users/);

    const userRow = page.locator(`tr[data-user-name="${uname}"]`);
    await expect(userRow).toHaveCount(1);
    const editHref = await userRow
      .locator('a[href*="/users/"][href*="/edit"]')
      .first()
      .getAttribute("href");
    if (!editHref) throw new Error(`edit href missing for user "${uname}" at ${page.url()}`);
    await page.goto(base() + editHref.split("?")[0]);
    await page.locator('select[name="role"]').selectOption("agent");
    await page.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/users$/);

    const deskID = await selectCatalogDesk(page, "General Support");
    const deskRow = page.locator(`tr[data-desk-id="${deskID}"]`);
    await catalogRowMenu(deskRow).click();
    await deskRow
      .locator(".category-overflow-menu:not([hidden])")
      .getByRole("menuitem", { name: "Edit desk", exact: true })
      .click();
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
    await page.route(
      (url) => url.pathname === addPath,
      async (route) => {
        const response = await route.fetch();
        const responseHeld = new Promise<void>((resolve) => {
          releaseResponse = resolve;
        });
        responseIntercepted();
        await responseHeld;
        await route.fulfill({ response });
      },
    );
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
    // Issue #234: the drawer re-renders its member list in place, so the
    // refreshed list is the confirmation and no success toast fires.
    expect(addResponse.headers()["x-save-feedback"]).toBeUndefined();
    await expect(drawer).toBeVisible();
    await expect(page.locator("#save-feedback")).toBeHidden();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("");
    // The drawer stays usable and keeps the in-flight edits.
    await expect(name).toBeEditable();
    await expect(drawer.getByRole("button", { name: "Close catalog details" })).toBeEnabled();
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
    // Issue #234: the removed member disappears from the re-rendered list in
    // place; no success toast fires.
    expect(removeResponse.headers()["x-save-feedback"]).toBeUndefined();
    await expect(drawer).toBeVisible();
    await expect(page.locator("#save-feedback")).toBeHidden();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("");
    await expect(drawer.locator(".desk-member-list li").filter({ hasText: uname })).toHaveCount(0);
    await expect(name).toHaveValue(inFlightName);
    await expect(description).toHaveValue(inFlightDescription);

    await drawer.getByRole("button", { name: "Close catalog details" }).click();
    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
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

  test("aborted desk membership changes show one persistent drawer-local error", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await loginAsSeeded(page);
    let deskID = "";
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 844 });

      const suffix = `${width}-${Date.now().toString(36).slice(2, 8)}`;
      const name = `Drawer member ${suffix}`;
      await page.goto(base() + "/users/new");
      await page.getByLabel(/^name$/i).fill(name);
      await page.getByLabel(/^email$/i).fill(`drawer-member-${suffix}@example.com`);
      await page.getByLabel(/^password$/i).fill("Secret123!42");
      await page.getByRole("button", { name: /create user/i }).click();
      await expect(page).toHaveURL(/\/users/);
      const row = page.locator(`tr[data-user-name="${name}"]`);
      const editHref = await row
        .locator('a[href*="/users/"][href*="/edit"]')
        .first()
        .getAttribute("href");
      if (!editHref) throw new Error(`edit href missing for ${name} at ${page.url()}`);
      await page.goto(base() + editHref.split("?")[0]);
      await page.locator('select[name="role"]').selectOption("agent");
      await page.getByRole("button", { name: /save changes/i }).click();
      await expect(page).toHaveURL(/\/users$/);

      if (!deskID) deskID = await selectCatalogDesk(page, "General Support");
      await page.goto(base() + `/desks/${deskID}/edit`);
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
        {
          endpoint: addPath,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#category-drawer-host",
        },
      );
      expect(new URLSearchParams(addResponse.request().postData() ?? "").get("user_id")).not.toBe(
        "",
      );
      // Issue #234: the added member is visible in the re-rendered drawer
      // list and no success toast fires for the in-place write.
      await expect(page.locator("#save-feedback")).toBeHidden();
      await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("");
      await expect(drawer.locator(".desk-member-list li").filter({ hasText: name })).toHaveCount(1);

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
        await expect(feedback.locator(".save-feedback-message")).toHaveText(
          "Unable to save changes. Please try again.",
        );
        await expect(feedback).toBeVisible();
        await expect(feedback).toHaveAttribute("role", "alert");
        await expect(feedback).toHaveAttribute("aria-live", "assertive");
        await expect(feedback).toHaveClass(/error-banner/);
        await expect(feedback).not.toHaveClass(/(?:^|\s)save-feedback(?:\s|$)/);
        // The failure stays local to the active drawer, not on the fixed toast layer.
        expect(
          await feedback.evaluate((element) => element.closest(".users-drawer")),
        ).not.toBeNull();
        await expect(page.locator("#save-feedback")).toHaveCount(1);
        expect(await feedback.evaluate((element) => element.closest("[inert]") === null)).toBe(
          true,
        );
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
