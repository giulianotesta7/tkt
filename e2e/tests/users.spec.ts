/**
 * Users: single creation+edition journey.
 *
 * Overflow baselines are covered by structural.spec.ts (all canonical screens).
 * Exclusions (remain covered by Go tests):
 *  - password change via /users/{id}/password
 *  - deactivation / reactivation lifecycle and session invalidation (D14)
 *  - deletion
 *  - exhaustive role-change protections and authorization matrix
 *  - the Pending tab's bulk approval effects, its refusal batches, the pending
 *    account's concealment from the edit route, and every approval matrix case
 * See Go tests: internal/adapters/http/handlers_users*, handlers_users_pending_test,
 * user_reactivate_test, etc. The journeys below cover what only a browser shows:
 * the tab's navigation contract, the stored sign-in method it states, the row
 * menu's keyboard and approval behaviour, the count arithmetic, and the one
 * intentional 422 refusal reaching the page with its console entry proven.
 *
 * The row-menu journey needs an account waiting for approval, which the default
 * seed never creates; it asks for one through the seed command's opt-in
 * `--pending-fixture` mode (helpers/pending.ts) against its own isolated database.
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
import {
  collectConsoleWithLocation,
  expectRefusalResponse,
  seedPendingFixture,
  unexpectedConsoleErrors,
} from "./helpers/pending.js";
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

/**
 * Fill the New user drawer through its own fields.
 *
 * The drawer arrives through an HTMX swap targeting #users-drawer-host, and its
 * fields are in the document before the drawer's own settle work focuses the name
 * field. A fill that starts in that gap is typed into whichever element holds
 * focus when the keystroke is delivered, not into the field the journey
 * addressed: a password typed while the drawer is still settling lands in the
 * name field, which leaves the required password empty so the browser refuses the
 * submit and no request is sent at all. Waiting for the name field to take focus
 * is the state the typing depends on.
 */
async function fillNewUserDrawer(
  page: import("@playwright/test").Page,
  values: { name: string; email: string; password: string },
): Promise<void> {
  const name = page.getByLabel(/^name$/i);
  await expect(name).toBeFocused();
  await name.fill(values.name);
  await page.getByLabel(/^email$/i).fill(values.email);
  await page.getByLabel(/^password$/i).fill(values.password);
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
    await fillNewUserDrawer(page, { name: baseName, email, password });
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
    await fillNewUserDrawer(page, { name, email, password: "AgentSecret1!" });
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

  /**
   * The Pending tab (issue #419, S3, selected design 6A): its own address, its
   * count, its server-rendered bulk form, and a refused batch answering its own
   * sentence in the page. The approval's storage effects and its matrix are
   * Go-owned (handlers_users_pending_test); this journey proves the browser half:
   * the tab swaps the screen in place, the URL stays the tab's own, and an
   * approve without a selection shows the localized reason instead of a silent
   * no-op.
   */
  test("Pending tab keeps its own address and reports a refused batch", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    const consoleRecords = collectConsoleWithLocation(page);
    await login(page);

    await page.goto(base() + "/users");
    const pendingTab = page.locator('.users-status-tabs a[href="/users?status=pending"]');
    await expect(pendingTab).toBeVisible();
    await expect(pendingTab.locator(".users-pending-count")).toHaveText(/^\d+$/);

    await assertHtmxSwap(
      page,
      async () => {
        await pendingTab.click();
      },
      {
        endpoint: "/users",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users\?status=pending$/,
      },
    );

    // The address is the stable one the notification bell will target.
    expect(new URL(page.url()).search).toBe("?status=pending");
    await expect(page.locator("#users-root")).toHaveAttribute("data-users-status", "pending");
    await expect(page.getByText(/inactive and waiting for approval/i)).toBeVisible();
    await expect(page.locator(".users-status-tabs a.is-selected")).toHaveAttribute(
      "href",
      "/users?status=pending",
    );

    // The selected design's table states the sign-in method the stored identity
    // carries, and its own actions column (issue #419, S12).
    await expect(page.getByRole("columnheader", { name: "Sign-in", exact: true })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Requested", exact: true })).toBeVisible();

    // The selected design's arithmetic: every account counts once, so All is the
    // sum of the other three, and the Pending tab's badge is the pending count.
    const tabCounts = (await page.locator(".users-status-tabs a span").allTextContents()).map(
      (text) => Number(text.trim()),
    );
    expect(tabCounts).toHaveLength(4);
    const [allCount, activeCount, deactivatedCount, pendingCount] = tabCounts;
    expect(allCount).toBe(activeCount + deactivatedCount + pendingCount);
    expect(pendingCount).toBe(
      await page.locator(".users-pending-table tbody tr[data-user-name]").count(),
    );

    // The bulk form is server-rendered, so it is a real form before any script.
    const bulkForm = page.locator("#users-pending-form");
    await expect(bulkForm).toHaveAttribute("method", "post");
    await expect(bulkForm).toHaveAttribute("action", "/users/pending/approve");
    await expect(page.getByLabel("Approve as", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Approve", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Clear", exact: true })).toBeVisible();

    // A batch with nothing selected is refused with its own sentence, in the page.
    const refusal = await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: "Approve", exact: true }).click();
      },
      {
        endpoint: "/users/pending/approve",
        method: "POST",
        expectedStatus: 422,
        hxTarget: "#users-root",
      },
    );
    await expect(page.getByRole("alert")).toContainText("select at least one account to approve");
    expect(new URL(page.url()).search).toBe("?status=pending");

    // The refusal is a real POST the browser reports as a failed resource. Its
    // provenance is proven twice: the response is this exact POST, and the ONE
    // console error the page logged is the exact 422 sentence located at that
    // same URL. Nothing else is tolerated — a second console error, another URL,
    // another status or a page/network failure still fails here, and the
    // canonical gate below keeps every other check with that proven entry removed.
    expectRefusalResponse(refusal, base(), 422);
    await expect
      .poll(() => consoleRecords.filter((record) => record.type === "error").length)
      .toBe(1);
    const unexpectedConsole = unexpectedConsoleErrors(consoleRecords, {
      status: 422,
      statusText: "Unprocessable Entity",
      url: refusal.url(),
    });
    expect(unexpectedConsole).toEqual([]);
    expect(obs.consoleErrors, "the shared observability text of the same entry").toEqual([
      "Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)",
    ]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "users pending",
      url: page.url(),
      role: "root",
      consoleErrors: unexpectedConsole,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  /**
   * The approved per-row menu (issue #419, S12, selected 6A): its trigger states
   * what it controls, its items approve THAT account with a role this actor may
   * grant through the one approval route, and the approval removes exactly that
   * row and persists the promoted account.
   *
   * The default seed creates no account waiting for approval — only a real
   * company sign-in does — so this journey asks for one explicitly through the
   * seed command's opt-in fixture (helpers/pending.ts): a requester, inactive and
   * pending, linked to the provider identity a company sign-in would store. The
   * fixture lives in this describe's isolated database, so the journey never skips
   * and never undoes the row it approved; a missing row fails below with the
   * entity, the selector and the URL.
   */
  test("Pending row menu approves that row with a grantable role", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    // Unique per attempt: a CI retry must not collide with the account the failed
    // attempt already approved, and each attempt resolves its own exact row.
    const suffix = Date.now().toString(36).slice(-4);
    const google = {
      name: `Pending Menu Applicant ${suffix}`,
      email: `pending-menu-${suffix}@example.com`,
      provider: "google" as const,
    };
    const microsoft = {
      name: `Pending Menu Reviewer ${suffix}`,
      email: `pending-menu-ms-${suffix}@example.com`,
      provider: "microsoft" as const,
    };
    seedPendingFixture(google);
    seedPendingFixture(microsoft);
    await login(page);
    await page.goto(base() + "/users?status=pending");

    const rowSelector = `tr[data-user-name="${google.name}"]`;
    const row = page.locator(rowSelector);
    await expect(
      row,
      `the pending fixture ${google.email} must render as ${rowSelector} at ${page.url()}`,
    ).toHaveCount(1);
    const microsoftSelector = `tr[data-user-name="${microsoft.name}"]`;
    const microsoftRow = page.locator(microsoftSelector);
    await expect(
      microsoftRow,
      `the pending fixture ${microsoft.email} must render as ${microsoftSelector} at ${page.url()}`,
    ).toHaveCount(1);

    // The Sign-in column states the identity each account actually holds — the
    // same two providers a company sign-in would have linked — and the status is
    // the pending state the design draws.
    await expect(row.locator(".users-pending-signin")).toHaveText("Google");
    await expect(microsoftRow.locator(".users-pending-signin")).toHaveText("Microsoft");
    await expect(row.locator(".user-status")).toHaveText("Pending");

    // The selected design's arithmetic with a real waiting account: All is the sum
    // of the three filters, and the tab badge is the pending list's own length.
    const pendingRows = page.locator(".users-pending-table tbody tr[data-user-name]");
    const pendingBefore = await pendingRows.count();
    const tabCounts = (await page.locator(".users-status-tabs a span").allTextContents()).map(
      (text) => Number(text.trim()),
    );
    expect(tabCounts).toHaveLength(4);
    const [allCount, activeCount, deactivatedCount, pendingCount] = tabCounts;
    expect(allCount).toBe(activeCount + deactivatedCount + pendingCount);
    expect(pendingCount).toBe(pendingBefore);

    // Form-associated table checkboxes participate in select-all, live count,
    // and Clear even though they are outside the bulk form's DOM subtree.
    const selectedCount = page.locator("[data-users-bulk-count]");
    await page.locator("[data-users-pending-all]").check();
    await expect(selectedCount).toHaveText(String(pendingBefore));
    await expect(row.locator("input[type=checkbox][name=user_ids]")).toBeChecked();
    await page.getByRole("button", { name: "Clear", exact: true }).click();
    await expect(selectedCount).toHaveText("0");
    await expect(row.locator("input[type=checkbox][name=user_ids]")).not.toBeChecked();

    const trigger = row.locator("[data-users-pending-menu]");
    await expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    await expect(trigger).toHaveAttribute("aria-label", `Actions for ${google.name}`);

    // Keyboard first: Enter opens the menu, the arrows walk its items, and Escape
    // closes it and hands focus back to the trigger.
    await trigger.focus();
    await page.keyboard.press("Enter");
    await expect(trigger).toHaveAttribute("aria-expanded", "true");
    const menuId = await trigger.getAttribute("aria-controls");
    if (!menuId) {
      throw new Error(
        `the row menu trigger in ${rowSelector} has no aria-controls at ${page.url()}`,
      );
    }
    const menu = page.locator(`[id="${menuId}"]`);
    await expect(menu).toBeVisible();
    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem").first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem").nth(1)).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    await expect(trigger).toBeFocused();

    // The new surface holds the canonical checks with real waiting rows in it:
    // nothing clipped, the design tokens live, the shell intact, no console, page,
    // request or 5xx failure so far.
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "users pending with waiting accounts",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // This journey signs in as root, so the menu offers every grantable role — and
    // never root. Reject belongs to the separate bulk action, not this menu.
    await trigger.click();
    await expect(menu.getByRole("menuitem")).toHaveCount(3);
    for (const role of ["User", "Agent", "Admin"]) {
      await expect(
        menu.getByRole("menuitem", { name: `Approve as ${role}`, exact: true }),
      ).toBeVisible();
    }
    await expect(menu.getByRole("menuitem", { name: /root|reject/i })).toHaveCount(0);

    // Approving posts the same batch shape the bulk bar uses — this row and the
    // chosen role — and swaps the whole screen.
    await assertHtmxSwap(
      page,
      async () => {
        await menu.getByRole("menuitem", { name: "Approve as Agent", exact: true }).click();
      },
      {
        endpoint: "/users/pending/approve",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
      },
    );
    await expect(page.locator(rowSelector)).toHaveCount(0);
    // The approval touched exactly that row: the other waiting account is still
    // queued, with its own provider still stated.
    await expect(page.locator(microsoftSelector)).toHaveCount(1);
    await expect(page.locator(microsoftSelector).locator(".users-pending-signin")).toHaveText(
      "Microsoft",
    );
    expect(new URL(page.url()).search).toBe("?status=pending");
    await expect(page.locator(".users-pending-count")).toHaveText(String(pendingBefore - 1));

    // Reject is the separate bulk action: it keeps the account and its method,
    // clears pending, and leaves it deactivated rather than deleting it.
    await page.locator(microsoftSelector).locator("input[type=checkbox][name=user_ids]").check();
    await expect(page.locator("[data-users-bulk-count]")).toHaveText("1");
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: "Reject", exact: true }).click();
      },
      {
        endpoint: "/users/pending/reject",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
      },
    );
    await expect(page.locator(microsoftSelector)).toHaveCount(0);
    await expect(page.locator(".users-pending-count")).toHaveText(String(pendingBefore - 2));
    await page.goto(base() + "/users?status=deactivated");
    await expect(page.locator(microsoftSelector)).toContainText(microsoft.email);
    await page.reload();
    await expect(page.locator(microsoftSelector)).toContainText(microsoft.email);

    // The approved account left the queue with the role granted, and persists.
    await page.goto(base() + "/users?status=active");
    const promotedSelector = `tr[data-user-name="${google.name}"]`;
    const promoted = page.locator(promotedSelector);
    await expect(
      promoted,
      `the approved account must render as ${promotedSelector} at ${page.url()}`,
    ).toHaveCount(1);
    await expect(promoted).toContainText("Agent");
    await expect(promoted).toContainText(google.email);
    await page.reload();
    await expect(page.locator(promotedSelector)).toContainText("Agent");

    // The journey saw its own two fixtures and nothing unexpected along the way.
    expect(obs.consoleErrors, "console errors").toEqual([]);
    expect(obs.pageErrors, "page errors").toEqual([]);
    expect(obs.failedRequests, "failed requests").toEqual([]);
    expect(obs.failedResponses, "5xx responses").toEqual([]);
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
  await fillNewUserDrawer(page, { name, email, password: "ProbeSecret123!" });
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
