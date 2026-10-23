/**
 * Users: single creation+edition journey.
 *
 * Overflow baselines are covered by structural.spec.ts (all canonical screens).
 * Exclusions (remain covered by Go tests):
 *  - password change via /users/{id}/password
 *  - deactivation / reactivation lifecycle and session invalidation (D14)
 *  - deletion
 *  - exhaustive role-change protections and authorization matrix
 * See Go tests: internal/adapters/http/handlers_users*, user_reactivate_test, etc.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  assertCanonicalScreen,
  collectObservability,
  expectDesignSystem,
  measureContrast,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { applyAssignee, currentAssignee, pickerButton } from "./helpers/ticket.js";
import {
  catalogNode,
  catalogRowMenu,
  createCategoryViaUi,
  createTicketViaUi,
  openCatalogRowMenu,
  resolveUserEditHref,
  selectCatalogDesk,
} from "./helpers/navigation.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

async function login(page: import("@playwright/test").Page) {
  await page.goto(base() + "/login");
  await page.getByLabel(/email/i).fill("alice@example.com");
  await page.getByLabel(/password/i).fill("SuperSecret42!");
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}

test.describe("Users", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("creation+edition journey via UI with persistence", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await login(page);

    const baseName = "Probe User " + Date.now().toString(36).slice(2, 6);
    const email = `probe-${Date.now().toString(36).slice(2, 8)}@example.com`;
    const password = "ProbeSecret123!";

    // Create in the Users drawer.
    await page.goto(base() + "/users");
    await page.getByRole("link", { name: /new user/i }).click();
    await expect(page.getByRole("heading", { name: "New user", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: /create user/i })).toBeVisible();
    await page.getByLabel(/^name$/i).fill(baseName);
    await page.getByLabel(/^email$/i).fill(email);
    await page.getByLabel(/^password$/i).fill(password);
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /create user/i }).click();
      },
      {
        endpoint: "/users",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    expect(new URL(page.url()).pathname).toBe("/users");
    await expect(page.getByText(baseName)).toBeVisible();
    await expect(page.getByText(email)).toBeVisible();

    const userSearch = page.getByRole("searchbox", { name: "Search users" });
    const usersSearchIcon = page.locator(".users-search .search-icon");
    for (const [name, value] of [
      ["viewBox", "0 0 24 24"],
      ["width", "18"],
      ["height", "18"],
      ["aria-hidden", "true"],
      ["focusable", "false"],
    ]) {
      await expect(usersSearchIcon).toHaveAttribute(name, value);
    }
    await expect(userSearch).toHaveCSS("height", "36px");
    await expect(userSearch).toHaveCSS("border-radius", "8px");
    await expect(userSearch).toHaveCSS("font-size", "13px");
    await userSearch.focus();
    await expect(userSearch).toHaveCSS("border-top-color", "rgb(49, 94, 255)");

    const usersURL = page.url();
    let searchRequests = 0;
    const countSearchRequests = (request: import("@playwright/test").Request) => {
      if (request.url().startsWith(base())) searchRequests += 1;
    };
    page.on("request", countSearchRequests);
    await userSearch.fill(` ${baseName.toUpperCase()} `);
    await expect(page.locator(`tr[data-user-name="${baseName}"]`)).toBeVisible();
    await expect(page.locator('tr[data-user-name="Alice Admin"]')).toBeHidden();
    await userSearch.fill(email.toUpperCase());
    await expect(page.locator(`tr[data-user-name="${baseName}"]`)).toBeVisible();
    await userSearch.fill("no matching user");
    await expect(page.getByText("No users match your search.")).toBeVisible();
    await userSearch.fill("");
    page.off("request", countSearchRequests);
    expect(searchRequests).toBe(0);
    expect(page.url()).toBe(usersURL);

    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("link", { name: /active/i }).click();
      },
      {
        endpoint: "/users",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users\?status=active$/,
      },
    );
    const activeUsersSearch = page.getByRole("searchbox", {
      name: "Search users",
    });
    await expect(activeUsersSearch).toBeVisible();

    const activeUsersURL = page.url();
    let postSwapSearchRequests = 0;
    let postSwapNavigations = 0;
    const countPostSwapSearchRequests = (request: import("@playwright/test").Request) => {
      if (request.url().startsWith(base())) postSwapSearchRequests += 1;
    };
    const countPostSwapNavigations = (frame: import("@playwright/test").Frame) => {
      if (frame === page.mainFrame()) postSwapNavigations += 1;
    };
    page.on("request", countPostSwapSearchRequests);
    page.on("framenavigated", countPostSwapNavigations);
    await activeUsersSearch.fill("no matching user");
    await expect(page.locator(`tr[data-user-name="${baseName}"]`)).toBeHidden();
    await expect(page.locator('tr[data-user-name="Alice Admin"]')).toBeHidden();
    await expect(page.getByText("No users match your search.")).toBeVisible();
    page.off("request", countPostSwapSearchRequests);
    page.off("framenavigated", countPostSwapNavigations);
    expect(postSwapSearchRequests).toBe(0);
    expect(postSwapNavigations).toBe(0);
    expect(page.url()).toBe(activeUsersURL);
    await activeUsersSearch.fill("");

    // Resolve edit href for that user (drawer link)
    const cleanHref = await resolveUserEditHref(page, baseName);
    await page.goto(base() + cleanHref);
    await expect(page.getByRole("heading", { name: /edit user/i })).toBeVisible();

    const renamed = baseName + " Renamed";
    await page.getByLabel(/^name$/i).fill(renamed);
    const roleSelect = page.locator('select[name="role"]');
    await expect(roleSelect).toBeVisible();
    await roleSelect.selectOption("agent");
    const userID = new URL(cleanHref, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!userID)
      throw new Error(`Could not resolve exact user ID from ${cleanHref} at ${page.url()}`);
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /save changes/i }).click();
      },
      {
        endpoint: `/users/${userID}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    const savedRow = page.locator(`tr[data-user-name="${renamed}"]`);
    await expect(savedRow).toHaveCount(1);
    await expect(savedRow).toContainText("Agent");

    // Persistence: reload and verify renamed visible in list
    await page.goto(base() + "/users");
    await expect(page.getByText(renamed)).toBeVisible();
    await expect(page.getByText(email)).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "users creation+edition",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
  test("dirty user creation stays guarded after an unrelated HTMX settle", async ({ page }) => {
    await login(page);
    await page.goto(base() + "/users");
    await page.getByRole("link", { name: /new user/i }).click();

    const drawer = page.getByRole("dialog", { name: "New user" });
    const name = drawer.getByLabel(/^name$/i);
    await expect(name).toBeFocused();
    await page.waitForTimeout(50);
    await name.fill("Unsaved user");
    await page.locator("#users-root").dispatchEvent("htmx:afterSettle", { bubbles: true });
    await drawer.getByRole("button", { name: /close/i }).click();

    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    await expect(confirmation).toBeVisible();
    await expect(confirmation.getByRole("button", { name: "Stay", exact: true })).toBeFocused();
    await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
    await expect(name).toHaveValue("Unsaved user");
    await expect(page).toHaveURL(/\/users\/new$/);
    await drawer.getByRole("button", { name: /close/i }).click();
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(drawer).toHaveCount(0);
  });

  // Issue #47 regression journeys: atomic Agent-to-User downgrade handoff.

  // The Catalog is one tree (issue #387): the desk is resolved by its exact row
  // and the browser is left on its legacy selection URL, which is the context the
  // desk drawer and the member routes carry.
  async function selectDesk(page: import("@playwright/test").Page, name: string): Promise<string> {
    return selectCatalogDesk(page, name);
  }

  async function openSelectedDeskDrawer(
    page: import("@playwright/test").Page,
    name: string,
  ): Promise<void> {
    const row = catalogNode(page, "desk", name);
    await expect(row).toHaveCount(1);
    await catalogRowMenu(row).click();
    await openCatalogRowMenu(row).getByRole("menuitem", { name: "Edit desk", exact: true }).click();
    await expect(page.getByRole("dialog", { name: /Edit desk/i })).toBeVisible();
  }

  async function createAgent(
    page: import("@playwright/test").Page,
    name: string,
    email: string,
  ): Promise<{ id: string; name: string }> {
    await page.goto(base() + "/users");
    await page.getByRole("link", { name: /new user/i }).click();
    await expect(page.getByRole("heading", { name: "New user", exact: true })).toBeVisible();
    await page.getByLabel(/^name$/i).fill(name);
    await page.getByLabel(/^email$/i).fill(email);
    await page.getByLabel(/^password$/i).fill("AgentSecret1!");
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /create user/i }).click();
      },
      {
        endpoint: "/users",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    const editHref = await resolveUserEditHref(page, name);
    const id = new URL(editHref, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!id) throw new Error(`cannot resolve id for ${name} from ${editHref} at ${page.url()}`);
    await page.goto(base() + editHref);
    await page.locator('select[name="role"]').selectOption("agent");
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /save changes/i }).click();
      },
      {
        endpoint: `/users/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    return { id, name };
  }

  async function addDeskMember(
    page: import("@playwright/test").Page,
    deskName: string,
    memberName: string,
  ): Promise<void> {
    await page.goto(base() + "/categories?view=structure");
    await selectDesk(page, deskName);
    await openSelectedDeskDrawer(page, deskName);
    const addSelect = page.locator(".desk-add-member select");
    await expect(addSelect).toBeVisible();
    await expect(page.locator(`.desk-add-member option:has-text("${memberName}")`)).toBeAttached();
    await addSelect.selectOption({ label: memberName });
    const addAction = await page.locator(".desk-add-member").getAttribute("action");
    if (!addAction)
      throw new Error(`add-member form action missing for desk "${deskName}" at ${page.url()}`);
    const currentURL = page.url();
    await assertHtmxSwap(
      page,
      async () => {
        await page.locator(".desk-add-member button").click();
      },
      {
        endpoint: new URL(addAction, currentURL).pathname,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#category-drawer-host",
        expectedUrl: currentURL,
      },
    );
    await selectDesk(page, deskName);
    await openSelectedDeskDrawer(page, deskName);
    await expect(
      page.locator(".desk-member-list").getByText(memberName, { exact: true }),
    ).toBeVisible();
  }

  async function configureLeastLoadedWorkflow(
    page: import("@playwright/test").Page,
    categoryId: string,
    deskName: string,
  ): Promise<void> {
    await page.goto(base() + `/categories/${categoryId}/workflow`);
    await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
    const cards = page.locator(".workflow-step-card");
    const before = await cards.count();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await addSummary.click();
    const addBtn = page
      .locator(".workflow-add-options button")
      .filter({ hasText: "Send to a desk" })
      .first();
    await expect(addBtn).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await addBtn.click();
      },
      {
        endpoint: (url) =>
          new URL(url).pathname === `/categories/${categoryId}/workflow` &&
          new URL(url).searchParams.get("add_step_type") === "assign_to_desk",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(before + 1);
    const deskSelect = page.getByLabel(/^desk$/i);
    await expect(deskSelect).toBeVisible();
    await expect(deskSelect.locator(`option:has-text("${deskName}")`)).toBeAttached();
    await deskSelect.selectOption({ label: deskName });
    const strategySelect = page.getByLabel(/^strategy$/i);
    await expect(strategySelect).toBeVisible();
    await strategySelect.selectOption("least_loaded");
    const publishResp = await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /publish/i }).click();
      },
      {
        endpoint: `/categories/${categoryId}/workflow`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    expect(publishResp.status()).toBe(200);
    await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);
  }

  // The assignee picker shows the ticket's current assignee on its button
  // (issue #386, S5): that reading IS the stored value.
  async function assigneeLabel(page: import("@playwright/test").Page): Promise<string> {
    return currentAssignee(page);
  }

  test("downgrade desk member reassigns open ticket and records handoff audit", async ({
    page,
  }) => {
    test.setTimeout(120000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await login(page);

    const stamp = Date.now().toString(36).slice(2, 8);
    const agentA = await createAgent(page, "Nico Down " + stamp, `nico-${stamp}@example.com`);
    const agentB = await createAgent(page, "Bea Peer " + stamp, `bea-${stamp}@example.com`);
    await addDeskMember(page, "General Support", agentA.name);
    await addDeskMember(page, "General Support", agentB.name);

    const catName = "Handoff Cat " + stamp;
    const categoryId = await createCategoryViaUi(page, catName);
    await configureLeastLoadedWorkflow(page, categoryId, "General Support");
    const ticketId = await createTicketViaUi(page, {
      title: "Handoff probe " + stamp,
      description: "issue 47 handoff",
      category: catName,
      priority: "medium",
    });

    // The workflow assigned the open ticket at creation (least-loaded of the
    // two members). Ensure the ticket sits on agent A before the downgrade.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const before = await assigneeLabel(page);
    if (before !== agentA.name) {
      await assertHtmxSwap(
        page,
        async () => {
          await applyAssignee(page, agentA.name);
        },
        {
          endpoint: `/tickets/${ticketId}/assign`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      await expect(pickerButton(page, "assign-user")).toContainText(agentA.name);
    }

    // Downgrade agent A to user from the user edit drawer: must succeed with
    // the standard HX save contract (no generic 500).
    await page.goto(base() + "/users");
    const editHrefA = await resolveUserEditHref(page, agentA.name);
    await page.goto(base() + editHrefA);
    const userID = new URL(editHrefA, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!userID) throw new Error(`cannot resolve user id from ${editHrefA} at ${page.url()}`);
    await page.locator('select[name="role"]').selectOption("user");
    const saved = await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /save changes/i }).click();
      },
      {
        endpoint: `/users/${userID}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    expect(saved.status()).toBe(200);
    const rowA = page.locator(`tr[data-user-name="${agentA.name}"]`);
    await expect(rowA).toHaveCount(1);
    await expect(rowA).toContainText("User");

    // Membership removed: the desk member list no longer shows A.
    await page.goto(base() + "/categories?view=structure");
    await selectDesk(page, "General Support");
    await openSelectedDeskDrawer(page, "General Support");
    await expect(
      page.locator(".desk-member-list").getByText(agentA.name, { exact: true }),
    ).toHaveCount(0);
    await expect(
      page.locator(".desk-member-list").getByText(agentB.name, { exact: true }),
    ).toBeVisible();

    // The open ticket moved to agent B and persisted across reload.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(pickerButton(page, "assign-user")).toContainText(agentB.name);
    await page.reload();
    await expect(pickerButton(page, "assign-user")).toContainText(agentB.name);

    // Audit trail: the handoff event carries the downgrade reason.
    await expect(page.locator("#timeline")).toContainText("role downgrade handoff");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "users downgrade handoff",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("downgrade with unresolvable desk context leaves ticket unassigned and succeeds", async ({
    page,
  }) => {
    test.setTimeout(120000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await login(page);

    const stamp = Date.now().toString(36).slice(2, 8);
    const agentC = await createAgent(page, "Carl Lone " + stamp, `carl-${stamp}@example.com`);

    // Ticket on the seeded category: no assign_to_desk step and no manual
    // assignment audit row carrying a desk id, so no desk context resolves.
    const ticketId = await createTicketViaUi(page, {
      title: "Lone handoff " + stamp,
      description: "issue 47 unassigned branch",
      category: "General",
      priority: "low",
    });
    await page.goto(base() + `/tickets/${ticketId}`);
    await assertHtmxSwap(
      page,
      async () => {
        await applyAssignee(page, agentC.name);
      },
      {
        endpoint: `/tickets/${ticketId}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(pickerButton(page, "assign-user")).toContainText(agentC.name);

    // Downgrade C: succeeds (no 500) and the open ticket becomes unassigned.
    await page.goto(base() + "/users");
    const editHrefC = await resolveUserEditHref(page, agentC.name);
    await page.goto(base() + editHrefC);
    const userID = new URL(editHrefC, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!userID) throw new Error(`cannot resolve user id from ${editHrefC} at ${page.url()}`);
    await page.locator('select[name="role"]').selectOption("user");
    const saved = await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: /save changes/i }).click();
      },
      {
        endpoint: `/users/${userID}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      },
    );
    expect(saved.status()).toBe(200);

    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(pickerButton(page, "assign-user")).toContainText("Unassigned");
    await expect(page.locator("#timeline")).toContainText("role downgrade handoff");
    await page.reload();
    await expect(pickerButton(page, "assign-user")).toContainText("Unassigned");
  });
});

// The approved option A (issue #394, S1-S4): the list owns the page width and
// the side drawer is the only form surface. These journeys cover the viewports
// and input modes the canonical baselines cannot: a phone-sized drawer whose
// actions stay reachable, the same drawer on the dark ladder, and the whole
// flow with scripting disabled.

async function drawerGeometry(page: import("@playwright/test").Page, scrollToEnd = false) {
  return page.evaluate((scroll) => {
    const drawer = document.querySelector<HTMLElement>(".users-drawer");
    const footer = document.querySelector<HTMLElement>(".users-drawer-footer");
    const body = document.querySelector<HTMLElement>(".users-drawer-body");
    if (!drawer || !footer || !body) throw new Error("the Users drawer is not rendered");
    if (scroll) body.scrollTop = body.scrollHeight;
    const drawerRect = drawer.getBoundingClientRect();
    const footerRect = footer.getBoundingClientRect();
    return {
      pageOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      footerGap: Math.round(drawerRect.bottom - footerRect.bottom),
      footerVisible: footerRect.top >= 0 && footerRect.bottom <= window.innerHeight + 1,
      footerPosition: getComputedStyle(footer).position,
      drawerWidth: Math.round(drawerRect.width),
    };
  }, scrollToEnd);
}

async function createUserThroughDrawer(
  page: import("@playwright/test").Page,
  name: string,
  email: string,
): Promise<void> {
  await page.goto(base() + "/users");
  await page.getByRole("link", { name: /new user/i }).click();
  const drawer = page.getByRole("dialog", { name: "New user" });
  await expect(drawer).toBeVisible();
  await drawer.getByLabel(/^name$/i).fill(name);
  await drawer.getByLabel(/^email$/i).fill(email);
  await drawer.getByLabel(/^password$/i).fill("ProbeSecret123!");
  await page.getByRole("button", { name: /create user/i }).click();
  await expect(page).toHaveURL(/\/users$/);
  await expect(page.locator(`tr[data-user-name="${name}"]`)).toBeVisible();
}

async function openUserDrawer(
  page: import("@playwright/test").Page,
  name: string,
): Promise<import("@playwright/test").Locator> {
  await page.locator(`tr[data-user-name="${name}"] .user-launcher`).click();
  const drawer = page.getByRole("dialog", { name: /edit user/i });
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("heading", { name: "Account status", exact: true })).toBeVisible();
  return drawer;
}

test.describe("Users — mobile drawer", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("holds one column, its lifecycle section and its actions at 390px", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 500 });
    await login(page);
    await page.goto(base() + "/users");

    // The table pans inside its own panel, so the page itself never does.
    const list = await page.evaluate(() => {
      const panel = document.querySelector<HTMLElement>(".users-list");
      if (!panel) throw new Error("no .users-list");
      return {
        pageOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        tablePans: panel.scrollWidth > panel.clientWidth,
      };
    });
    expect(list.pageOverflow, "the Users list must not scroll the page sideways").toBe(0);
    expect(list.tablePans, "the table pans inside its own panel").toBe(true);
    await expect(page.locator(".users-access-card")).toHaveCount(0);

    const stamp = Date.now().toString(36).slice(2, 7);
    const name = `Mobile User ${stamp}`;
    await createUserThroughDrawer(page, name, `mobile-${stamp}@example.com`);

    // Edition keeps the reference hierarchy, and the drawer never exceeds the
    // phone's width (issue #394, S3/S4).
    await openUserDrawer(page, name);
    const drawer = page.getByRole("dialog", { name: /edit user/i });
    await expect(drawer).toHaveCSS("width", "390px");
    await expect(drawer.locator(".users-help")).toHaveText("Standard user access.");
    await expect(drawer.locator(".users-lifecycle")).toContainText("This user can sign in.");
    const geometry = await drawerGeometry(page, true);
    expect(geometry.drawerWidth).toBe(390);
    expect(geometry.footerPosition).toBe("sticky");
    expect(geometry.footerGap, "the actions stay at the drawer's foot").toBeLessThanOrEqual(1);
    expect(geometry.footerVisible, "the actions stay in the viewport").toBe(true);

    // The deactivation confirmation still opens and fits the phone viewport.
    await drawer.getByRole("button", { name: "Deactivate user", exact: true }).click();
    const confirmation = page.getByRole("dialog", { name: "Deactivate user?" });
    await expect(confirmation).toBeVisible();
    const dialog = await confirmation.evaluate((el) => {
      const rect = el.getBoundingClientRect();
      return {
        open: (el as HTMLDialogElement).open,
        fits:
          rect.top >= 0 &&
          rect.left >= 0 &&
          rect.bottom <= window.innerHeight &&
          rect.right <= document.documentElement.clientWidth,
      };
    });
    expect(dialog.open).toBe(true);
    expect(dialog.fits, "the confirmation must not overflow the 390px viewport").toBe(true);
    await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(confirmation).toBeHidden();
  });
});

test.describe("Users — dark drawer", () => {
  test.use({ colorScheme: "dark" });
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("keeps the dark ladder live and its drawer text readable at 390px", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page);
    const stamp = Date.now().toString(36).slice(2, 7);
    const name = `Dark User ${stamp}`;
    await createUserThroughDrawer(page, name, `dark-${stamp}@example.com`);
    await openUserDrawer(page, name);

    // The drawer paints the surface the dark ladder publishes, not a stale white.
    await expectDesignSystem(page);
    await expect(page.locator(".users-drawer")).toHaveCSS("background-color", "rgb(21, 24, 29)");
    for (const [label, locator] of [
      ["role help", page.locator(".users-help")],
      ["lifecycle copy", page.locator(".users-lifecycle-copy")],
      ["lifecycle status", page.locator(".users-lifecycle .user-status")],
    ] as const) {
      const { ratio } = await measureContrast(locator);
      expect(ratio, `${label} on the dark drawer must reach 4.5:1`).toBeGreaterThanOrEqual(4.5);
    }
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBe(0);
  });
});

test.describe("Users — without scripting", () => {
  test.use({ javaScriptEnabled: false });
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the server-rendered drawer creates a user as a plain navigation", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page);

    // The status filter is a real link: with no script it is an ordinary GET.
    await page.goto(base() + "/users");
    await page.getByRole("link", { name: /^Active/ }).click();
    await expect(page).toHaveURL(/\/users\?status=active$/);

    // /users/new renders the drawer's markup server-side, so the form is the
    // page: every field is there with no enhancement at all.
    await page.goto(base() + "/users/new");
    const drawer = page.getByRole("dialog", { name: "New user" });
    await expect(drawer).toBeVisible();
    await expect(drawer.getByLabel(/^name$/i)).toBeVisible();
    await expect(drawer.getByLabel(/^email$/i)).toBeVisible();
    await expect(drawer.getByLabel(/^password$/i)).toBeVisible();
    await expect(drawer.getByRole("button", { name: /create user/i })).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBe(0);

    const stamp = Date.now().toString(36).slice(2, 7);
    const name = `NoScript User ${stamp}`;
    await drawer.getByLabel(/^name$/i).fill(name);
    await drawer.getByLabel(/^email$/i).fill(`noscript-${stamp}@example.com`);
    await drawer.getByLabel(/^password$/i).fill("ProbeSecret123!");
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users$/);
    await expect(page.locator(`tr[data-user-name="${name}"]`)).toBeVisible();
  });
});
