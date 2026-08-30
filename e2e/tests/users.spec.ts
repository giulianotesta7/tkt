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
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxNoSwap, assertHtmxSwap } from "./helpers/htmx.js";
import { createTicketViaUi, resolveUserEditHref } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";

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

      // Create
      await page.goto(base() + "/users/new");
      await expect(page.locator('h1:has-text("New user")')).toBeVisible();
      await expect(page.getByRole("button", { name: /create user/i })).toBeVisible();
      await page.getByLabel(/^name$/i).fill(baseName);
      await page.getByLabel(/^email$/i).fill(email);
      await page.getByLabel(/^password$/i).fill(password);
      const createResponsePromise = waitForExactPost(page, "/users");
      await Promise.all([
        createResponsePromise,
        page.getByRole("button", { name: /create user/i }).click(),
      ]);
      const createResponse = await createResponsePromise;
      expect(createResponse.status()).toBe(303);
      expect(new URL(page.url()).pathname).toBe("/users");
      await expect(page.getByText(baseName)).toBeVisible();
      await expect(page.getByText(email)).toBeVisible();

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
      if (!userID) throw new Error(`Could not resolve exact user ID from ${cleanHref} at ${page.url()}`);
      await assertHtmxSwap(page, async () => {
        await page.getByRole("button", { name: /save changes/i }).click();
      }, {
        endpoint: `/users/${userID}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#users-root",
        expectedUrl: /\/users$/,
      });
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
  // Issue #47 regression journeys: atomic Agent-to-User downgrade handoff.

  async function selectDesk(page: import("@playwright/test").Page, name: string): Promise<string> {
    const link = page.locator("a[data-desk-id]").filter({ has: page.getByText(name, { exact: true }) });
    await expect(link).toHaveCount(1);
    const href = await link.getAttribute("href");
    if (!href) throw new Error(`Desk link href missing for "${name}" at ${page.url()}`);
    const deskID = new URL(href, page.url()).searchParams.get("desk_id");
    if (!deskID || !/^\d+$/.test(deskID)) {
      throw new Error(`Could not resolve exact desk ID for "${name}" from ${href} at ${page.url()}`);
    }
    await link.click();
    return deskID;
  }

  async function createAgent(page: import("@playwright/test").Page, name: string, email: string): Promise<{ id: string; name: string }> {
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(name);
    await page.getByLabel(/^email$/i).fill(email);
    await page.getByLabel(/^password$/i).fill("AgentSecret1!");
    const resp = await Promise.all([
      waitForExactPost(page, "/users"),
      page.getByRole("button", { name: /create user/i }).click(),
    ]);
    expect(resp[0].status()).toBe(303);
    await expect(page).toHaveURL(/\/users/);
    const editHref = await resolveUserEditHref(page, name);
    const id = new URL(editHref, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!id) throw new Error(`cannot resolve id for ${name} from ${editHref} at ${page.url()}`);
    await page.goto(base() + editHref);
    await page.locator('select[name="role"]').selectOption("agent");
    await assertHtmxSwap(page, async () => {
      await page.getByRole("button", { name: /save changes/i }).click();
    }, { endpoint: `/users/${id}/edit`, method: "POST", expectedStatus: 200, hxTarget: "#users-root", expectedUrl: /\/users$/ });
    return { id, name };
  }

  async function addDeskMember(page: import("@playwright/test").Page, deskName: string, memberName: string): Promise<void> {
    await page.goto(base() + "/desks");
    await selectDesk(page, deskName);
    const addSelect = page.locator(".desk-add-member select");
    await expect(addSelect).toBeVisible();
    await expect(page.locator(`.desk-add-member option:has-text("${memberName}")`)).toBeAttached();
    await addSelect.selectOption({ label: memberName });
    const addAction = await page.locator(".desk-add-member").getAttribute("action");
    if (!addAction) throw new Error(`add-member form action missing for desk "${deskName}" at ${page.url()}`);
    const resp = await Promise.all([
      waitForExactPost(page, addAction),
      page.locator(".desk-add-member button").click(),
    ]);
    expect(resp[0].status()).toBe(303);
    await expect(page.locator(".desk-member-list").getByText(memberName, { exact: true })).toBeVisible();
  }

  async function createCategory(page: import("@playwright/test").Page, name: string): Promise<string> {
    await page.goto(base() + "/categories/new");
    await page.getByLabel(/name/i).fill(name);
    await page.getByRole("button", { name: /create category|save|create/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    const row = page.locator("tr").filter({ hasText: name });
    await expect(row).toHaveCount(1);
    const editHref = await row.locator('a[href*="/edit"]').getAttribute("href");
    const id = editHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
    if (!id) throw new Error(`cannot resolve category id for ${name} at ${page.url()}`);
    return id;
  }

  async function configureLeastLoadedWorkflow(page: import("@playwright/test").Page, categoryId: string, deskName: string): Promise<void> {
    await page.goto(base() + `/categories/${categoryId}/workflow`);
    await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
    const cards = page.locator(".workflow-step-card");
    const before = await cards.count();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await addSummary.click();
    const addBtn = page.locator(".workflow-add-options button").filter({ hasText: "Assign to desk" }).first();
    await expect(addBtn).toBeVisible();
    await assertHtmxSwap(page, async () => {
      await addBtn.click();
    }, {
      endpoint: (url) => new URL(url).pathname === `/categories/${categoryId}/workflow` && new URL(url).searchParams.get("add_step_type") === "assign_to_desk",
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    });
    await expect(cards).toHaveCount(before + 1);
    const deskSelect = page.getByLabel(/^desk$/i);
    await expect(deskSelect).toBeVisible();
    await expect(deskSelect.locator(`option:has-text("${deskName}")`)).toBeAttached();
    await assertHtmxNoSwap(page, async () => {
      await deskSelect.selectOption({ label: deskName });
    }, {
      endpoint: (url) => new URL(url).pathname === `/categories/${categoryId}/workflow`,
      method: "POST",
      expectedStatus: 200,
    });
    const strategySelect = page.getByLabel(/^strategy$/i);
    await expect(strategySelect).toBeVisible();
    await assertHtmxNoSwap(page, async () => {
      await strategySelect.selectOption("least_loaded");
    }, {
      endpoint: (url) => new URL(url).pathname === `/categories/${categoryId}/workflow`,
      method: "POST",
      expectedStatus: 200,
    });
    const publishResp = await assertHtmxSwap(page, async () => {
      await page.getByRole("button", { name: /publish/i }).click();
    }, { endpoint: `/categories/${categoryId}/workflow`, method: "POST", expectedStatus: 200, hxTarget: "#workflow-builder" });
    expect(publishResp.status()).toBe(200);
    await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);
  }

  async function assigneeLabel(page: import("@playwright/test").Page): Promise<string> {
    const checked = page.locator('select[name="user_id"] option:checked');
    await expect(checked).toHaveCount(1);
    return (await checked.textContent())?.trim() ?? "";
  }

  test("downgrade desk member reassigns open ticket and records handoff audit", async ({ page }) => {
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
    const categoryId = await createCategory(page, catName);
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
      await page.locator('select[name="user_id"]').selectOption({ label: agentA.name });
      await expect(page.locator('select[name="user_id"] option:checked')).toHaveText(agentA.name);
    }

    // Downgrade agent A to user from the user edit drawer: must succeed with
    // the standard HX save contract (no generic 500).
    await page.goto(base() + "/users");
    const editHrefA = await resolveUserEditHref(page, agentA.name);
    await page.goto(base() + editHrefA);
    const userID = new URL(editHrefA, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!userID) throw new Error(`cannot resolve user id from ${editHrefA} at ${page.url()}`);
    await page.locator('select[name="role"]').selectOption("user");
    const saved = await assertHtmxSwap(page, async () => {
      await page.getByRole("button", { name: /save changes/i }).click();
    }, {
      endpoint: `/users/${userID}/edit`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#users-root",
      expectedUrl: /\/users$/,
    });
    expect(saved.status()).toBe(200);
    const rowA = page.locator(`tr[data-user-name="${agentA.name}"]`);
    await expect(rowA).toHaveCount(1);
    await expect(rowA).toContainText("User");

    // Membership removed: the desk member list no longer shows A.
    await page.goto(base() + "/desks");
    await selectDesk(page, "General Support");
    await expect(page.locator(".desk-member-list").getByText(agentA.name, { exact: true })).toHaveCount(0);
    await expect(page.locator(".desk-member-list").getByText(agentB.name, { exact: true })).toBeVisible();

    // The open ticket moved to agent B and persisted across reload.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator('select[name="user_id"] option:checked')).toHaveText(agentB.name);
    await page.reload();
    await expect(page.locator('select[name="user_id"] option:checked')).toHaveText(agentB.name);

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

  test("downgrade with unresolvable desk context leaves ticket unassigned and succeeds", async ({ page }) => {
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
    await page.locator('select[name="user_id"]').selectOption({ label: agentC.name });
    await expect(page.locator('select[name="user_id"] option:checked')).toHaveText(agentC.name);

    // Downgrade C: succeeds (no 500) and the open ticket becomes unassigned.
    await page.goto(base() + "/users");
    const editHrefC = await resolveUserEditHref(page, agentC.name);
    await page.goto(base() + editHrefC);
    const userID = new URL(editHrefC, page.url()).pathname.match(/^\/users\/(\d+)\/edit$/)?.[1];
    if (!userID) throw new Error(`cannot resolve user id from ${editHrefC} at ${page.url()}`);
    await page.locator('select[name="role"]').selectOption("user");
    const saved = await assertHtmxSwap(page, async () => {
      await page.getByRole("button", { name: /save changes/i }).click();
    }, {
      endpoint: `/users/${userID}/edit`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#users-root",
      expectedUrl: /\/users$/,
    });
    expect(saved.status()).toBe(200);

    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator('select[name="user_id"] option:checked')).toHaveText("Unassigned");
    await expect(page.locator("#timeline")).toContainText("role downgrade handoff");
    await page.reload();
    await expect(page.locator('select[name="user_id"] option:checked')).toHaveText("Unassigned");
  });
});
