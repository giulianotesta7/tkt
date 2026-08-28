/**
 * Structural baselines for every canonical screen at 390px and 1280px.
 *
 * Each canonical route is asserted for:
 *  - URL or redirect
 *  - heading or main region
 *  - primary accessible control
 *  - no document-level horizontal overflow
 *  - zero console errors, page errors, failed own-requests, own 5xx responses
 *
 * Uses the shared layout helper — no copy-pasted viewport assertions.
 * Covers both empty-base and seeded profiles.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { base, loginAsSeeded } from "./helpers/auth.js";
import { createTicketViaUi, resolveCategoryEditHref, resolveUserEditHref, resolveWorkflowHref } from "./helpers/navigation.js";

const viewports = [
  { width: 390, height: 844, label: "390px" },
  { width: 1280, height: 800, label: "1280px" },
] as const;

async function assertScreen(
  page: import("@playwright/test").Page,
  opts: {
    viewport: number;
    label: string;
    path: string;
    expectedUrl?: RegExp;
    role: string;
    obs: ReturnType<typeof collectObservability>;
    heading: import("@playwright/test").Locator;
    control: import("@playwright/test").Locator;
  },
) {
  await page.goto(base() + opts.path);
  if (opts.expectedUrl) {
    await expect(page).toHaveURL(opts.expectedUrl);
  } else {
    expect(page.url()).toContain(opts.path);
  }
  await expect(opts.heading.first()).toBeVisible({ timeout: 10_000 });
  await expect(opts.control.first()).toBeVisible({ timeout: 10_000 });
  await assertCanonicalScreen(page, {
    viewport: opts.viewport,
    label: opts.label,
    url: page.url(),
    role: opts.role,
    consoleErrors: opts.obs.consoleErrors,
    pageErrors: opts.obs.pageErrors,
    failedRequests: opts.obs.failedRequests,
    failedResponses: opts.obs.failedResponses,
  });
}

test.describe("Structural — seeded canonical screens", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  for (const vp of viewports) {
    test(`seeded structural baselines at ${vp.label}`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height });
      const obs = collectObservability(page);

      // Unauthenticated canonical screens (loopback, no session)
      // /login — canonical login form
      await page.goto(base() + "/login");
      await expect(page).toHaveURL(/\/login/);
      await expect(page.getByRole("heading", { name: /sign in to tkt/i })).toBeVisible();
      await expect(page.getByLabel(/email/i)).toBeVisible();
      await expect(page.getByRole("button", { name: /sign in/i })).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/login (seeded, anonymous) @ ${vp.label}`,
        url: page.url(),
        role: "anonymous",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // /setup when users already exist — anonymous must be sent to /login (observed canonical behavior)
      await page.goto(base() + "/setup");
      await expect(page).toHaveURL(/\/login/);
      await expect(page.getByRole("heading", { name: /sign in to tkt/i })).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/setup with users (anonymous redirect) @ ${vp.label}`,
        url: page.url(),
        role: "anonymous",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // / unauthenticated — redirects to /login when users exist
      await page.goto(base() + "/");
      await expect(page).toHaveURL(/\/login/);
      await expect(page.getByLabel(/email/i)).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/ (anonymous redirect) @ ${vp.label}`,
        url: page.url(),
        role: "anonymous",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // Authenticate as seeded root for the remaining canonical screens
      await loginAsSeeded(page);

      // / when authenticated — redirects to /tickets
      await page.goto(base() + "/");
      await expect(page).toHaveURL(/\/tickets/);
      await expect(page.locator('h1:has-text("Tickets")').first()).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/ (authenticated redirect) @ ${vp.label}`,
        url: page.url(),
        role: "root",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // Prepare dynamic IDs: create a ticket for detail, resolve category/workflow/user edit hrefs
      const ticketTitle = "Structural probe " + Date.now() + Math.random().toString(36).slice(2, 6);
      const ticketId = await createTicketViaUi(page, {
        title: ticketTitle,
        description: "probe",
        category: "General",
        priority: "high",
      });

      await page.goto(base() + "/categories");
      const workflowHref = await resolveWorkflowHref(page);
      if (!workflowHref) throw new Error("seeded workflow href not found");
      const categoryEditHref = await resolveCategoryEditHref(page, "General");
      if (!categoryEditHref) throw new Error("seeded category edit href not found");

      // Create a non-root user so we have a manageable edit target (root is protected)
      const seededUserName = "StructUser " + Date.now().toString(36).slice(2, 6);
      const seededUserEmail = `struct-${Date.now().toString(36).slice(2, 8)}@example.com`;
      await page.goto(base() + "/users/new");
      await page.getByLabel(/^name$/i).fill(seededUserName);
      await page.getByLabel(/^email$/i).fill(seededUserEmail);
      await page.getByLabel(/^password$/i).fill("Secret123!");
      await page.getByRole("button", { name: /create user/i }).click();
      await expect(page).toHaveURL(/\/users/);
      await expect(page.getByText(seededUserName)).toBeVisible();

      await page.goto(base() + "/users");
      const userEditHref = await resolveUserEditHref(page);
      if (!userEditHref) throw new Error("seeded user edit href not found");

      // /setup when already authenticated with users — redirects to /tickets
      await page.goto(base() + "/setup");
      await expect(page).toHaveURL(/\/tickets/);
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/setup with users (authenticated redirect) @ ${vp.label}`,
        url: page.url(),
        role: "root",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // Canonical screens (authenticated, seeded)
      await assertScreen(page, {
        viewport: vp.width,
        label: `/tickets @ ${vp.label}`,
        path: "/tickets",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Tickets")'),
        control: page.getByRole("link", { name: /new ticket/i }).or(page.locator('input[aria-label="Search tickets"]')),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/tickets/new @ ${vp.label}`,
        path: "/tickets/new",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("New ticket")'),
        control: page.getByRole("button", { name: /create ticket/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/tickets/{id} @ ${vp.label}`,
        path: `/tickets/${ticketId}`,
        role: "root",
        obs,
        heading: page.locator("#ticket-detail"),
        control: page.locator("#ticket-detail").locator('textarea, [aria-label="Ticket title"], button:has-text("Add comment")').first(),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/users @ ${vp.label}`,
        path: "/users",
        role: "root",
        obs,
        heading: page.locator("#users-list-title"),
        control: page.getByRole("link", { name: /new user/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/users/new @ ${vp.label}`,
        path: "/users/new",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("New user")'),
        control: page.getByRole("button", { name: /create user/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/users/{id}/edit @ ${vp.label}`,
        path: userEditHref,
        role: "root",
        obs,
        heading: page.locator("h2").filter({ hasText: /edit user|operator details/i }),
        control: page.getByRole("button", { name: /save changes/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories @ ${vp.label}`,
        path: "/categories",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Categories")'),
        control: page.getByRole("link", { name: /new category/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories/new @ ${vp.label}`,
        path: "/categories/new",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("New category")'),
        control: page.getByRole("button", { name: /create category|save/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories/{id}/edit @ ${vp.label}`,
        path: categoryEditHref,
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Rename category")'),
        control: page.getByRole("button", { name: /save/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories/{id}/workflow @ ${vp.label}`,
        path: workflowHref,
        role: "root",
        obs,
        heading: page.locator("h1").filter({ hasText: /category workflow/i }),
        control: page.locator("#workflow-builder"),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/desks @ ${vp.label}`,
        path: "/desks",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Desks")'),
        control: page.locator("details.desk-create summary"),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/settings @ ${vp.label}`,
        path: "/settings",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Settings")'),
        control: page.locator('input[name="internal_comment_bg"]'),
      });
    });
  }
});

test.describe("Structural — empty base", () => {
  for (const vp of viewports) {
    test(`empty base structural baselines at ${vp.label}`, async ({ page }) => {
      await startServer({ seed: false });
      try {
        await page.setViewportSize({ width: vp.width, height: vp.height });
        const obs = collectObservability(page);

      // /login with empty users — redirects to /setup (observed canonical behavior)
      await page.goto(base() + "/login");
      await expect(page).toHaveURL(/\/setup/);
      await expect(page.getByRole("heading", { name: /set up tkt/i })).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/login (empty redirect) @ ${vp.label}`,
        url: page.url(),
        role: "anonymous",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // /setup with empty base — bootstrap form is reachable
      await page.goto(base() + "/setup");
      await expect(page).toHaveURL(/\/setup/);
      await expect(page.getByRole("heading", { name: /set up tkt/i })).toBeVisible();
      await expect(page.getByLabel(/name/i)).toBeVisible();
      await expect(page.getByRole("button", { name: /create account/i })).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/setup (empty) @ ${vp.label}`,
        url: page.url(),
        role: "anonymous",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // / with empty base — anonymous redirects to /setup
      await page.goto(base() + "/");
      await expect(page).toHaveURL(/\/setup/);
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/(empty anonymous redirect) @ ${vp.label}`,
        url: page.url(),
        role: "anonymous",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // Perform first-user bootstrap via the UI (empty base → root)
      const name = "Empty Root";
      const email = "empty-root@example.com";
      const password = "SuperSecret42!";
      await page.goto(base() + "/setup");
      await page.getByLabel(/name/i).fill(name);
      await page.getByLabel(/email/i).fill(email);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /create account|set up|sign up|create/i }).click();
      await expect(page).toHaveURL(/\/login/);
      await page.getByLabel(/email/i).fill(email);
      await page.getByLabel(/password/i).fill(password);
      await page.getByRole("button", { name: /log in|sign in/i }).click();
      await expect(page).toHaveURL(/\/tickets/);

      // After bootstrap, /setup must redirect to /login when anonymous and to /tickets when authenticated
      // Check authenticated redirect first (already logged in)
      await page.goto(base() + "/setup");
      await expect(page).toHaveURL(/\/tickets/);
      await assertCanonicalScreen(page, {
        viewport: vp.width,
        label: `/setup with users (authenticated redirect, empty→seeded) @ ${vp.label}`,
        url: page.url(),
        role: "root",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });

      // Minimal data for dynamic screens on empty base: create a category so we have edit/workflow targets
      const catName = "EmptyCat " + Date.now().toString(36).slice(2, 6);
      await page.goto(base() + "/categories/new");
      await page.getByLabel(/name/i).fill(catName);
      await page.getByRole("button", { name: /create category|save|create/i }).click();
      await expect(page).toHaveURL(/\/categories/);
      await page.goto(base() + "/categories");
      const categoryEditHref = await resolveCategoryEditHref(page, catName);
      if (!categoryEditHref) throw new Error("empty category edit href not found");
      const workflowHref = categoryEditHref.replace(/\/edit$/, "/workflow");

      // Create a user so we have a user edit target
      const userName = "Empty User " + Date.now().toString(36).slice(2, 4);
      const userEmail = `empty-${Date.now()}@example.com`;
      await page.goto(base() + "/users/new");
      await page.getByLabel(/^name$/i).fill(userName);
      await page.getByLabel(/^email$/i).fill(userEmail);
      await page.getByLabel(/^password$/i).fill("Secret123!");
      await page.getByRole("button", { name: /create user/i }).click();
      await expect(page).toHaveURL(/\/users/);
      await page.goto(base() + "/users");
      const userEditHref = await resolveUserEditHref(page);
      if (!userEditHref) throw new Error("empty user edit href not found");

      // Create a ticket is not possible without a published workflow on empty base —
      // we publish a minimal workflow first to obtain a ticket detail target
      await page.goto(base() + workflowHref);
      await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
      // Add one step if none exist (empty draft case)
      const cards = page.locator(".workflow-step-card");
      const before = await cards.count();
      if (before === 0) {
        const addSummary = page.locator(".workflow-add-step summary").first();
        await expect(addSummary).toBeVisible();
        await addSummary.click();
        const addBtn = page.locator(".workflow-add-options button").filter({ hasText: "Manual task" }).first();
        await expect(addBtn).toBeVisible();
        await addBtn.click();
        await expect(page.locator("#workflow-builder")).toBeVisible();
        await expect(cards).toHaveCount(1);
        // Fill required instructions so publish succeeds
        const instr = page.getByLabel(/instructions/i);
        await expect(instr).toBeVisible({ timeout: 10000 });
        await instr.fill("Handle the ticket");
        await instr.blur();
        await expect(page.locator("#workflow-builder")).toBeVisible();
      }
      // Publish so tickets can use this category
      const publishBtn = page.getByRole("button", { name: /publish/i });
      await expect(publishBtn).toBeVisible();
      const pubResp = await Promise.all([
        page.waitForResponse((r) => r.url().includes("/workflow") && r.request().method() === "POST"),
        publishBtn.click().then(() => {}),
      ]).then(([resp]) => resp).catch(() => null);
      if (pubResp) expect(pubResp.status(), `publish expected 200 got ${pubResp.status()}`).toBe(200);
      await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });

      const ticketTitle = "Empty probe " + Date.now().toString(36).slice(2, 6);
      const ticketId = await createTicketViaUi(page, {
        title: ticketTitle,
        description: "empty probe",
        category: catName,
        priority: "medium",
      });

      // Authenticated canonical screens on empty base (after minimal seeding)
      await assertScreen(page, {
        viewport: vp.width,
        label: `/tickets (empty) @ ${vp.label}`,
        path: "/tickets",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Tickets")'),
        control: page.getByRole("link", { name: /new ticket/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/tickets/new (empty) @ ${vp.label}`,
        path: "/tickets/new",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("New ticket")'),
        control: page.getByRole("button", { name: /create ticket/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/tickets/{id} (empty) @ ${vp.label}`,
        path: `/tickets/${ticketId}`,
        role: "root",
        obs,
        heading: page.locator("#ticket-detail"),
        control: page.locator('#ticket-detail textarea, #ticket-detail [aria-label="Comment body"]').first(),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/users (empty) @ ${vp.label}`,
        path: "/users",
        role: "root",
        obs,
        heading: page.locator("#users-list-title"),
        control: page.getByRole("link", { name: /new user/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/users/new (empty) @ ${vp.label}`,
        path: "/users/new",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("New user")'),
        control: page.getByRole("button", { name: /create user/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/users/{id}/edit (empty) @ ${vp.label}`,
        path: userEditHref,
        role: "root",
        obs,
        heading: page.locator("h2").filter({ hasText: /edit user|operator details/i }),
        control: page.getByRole("button", { name: /save changes/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories (empty) @ ${vp.label}`,
        path: "/categories",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Categories")'),
        control: page.getByRole("link", { name: /new category/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories/new (empty) @ ${vp.label}`,
        path: "/categories/new",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("New category")'),
        control: page.getByRole("button", { name: /create category|save/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories/{id}/edit (empty) @ ${vp.label}`,
        path: categoryEditHref,
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Rename category")'),
        control: page.getByRole("button", { name: /save/i }),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/categories/{id}/workflow (empty) @ ${vp.label}`,
        path: workflowHref,
        role: "root",
        obs,
        heading: page.locator("h1").filter({ hasText: /category workflow/i }),
        control: page.locator("#workflow-builder"),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/desks (empty) @ ${vp.label}`,
        path: "/desks",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Desks")'),
        control: page.locator("details.desk-create summary"),
      });

      await assertScreen(page, {
        viewport: vp.width,
        label: `/settings (empty) @ ${vp.label}`,
        path: "/settings",
        role: "root",
        obs,
        heading: page.locator('h1:has-text("Settings")'),
        control: page.locator('input[name="internal_comment_bg"]'),
      });
      } finally {
        await stopServer();
      }
    });
  }
});
