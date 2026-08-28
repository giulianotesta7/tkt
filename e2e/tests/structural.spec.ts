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
 *
 * Seeded profile covers all canonical screens (anonymous + authenticated) at both viewports.
 * Empty profile covers ONLY /login, /setup, and / (onboarding redirects and bootstrap).
 * Add a new route by adding one entry to the data table — not by copying a block.
 */

import { test, expect, type Locator, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { base, seededCredentials, loginAsSeeded } from "./helpers/auth.js";
import { createTicketViaUi, resolveCategoryEditHref, resolveUserEditHref, resolveWorkflowHref } from "./helpers/navigation.js";

const viewports = [
  { width: 390, height: 844, label: "390px" },
  { width: 1280, height: 800, label: "1280px" },
] as const;

interface StructuralScreen {
  /** Display label for this screen */
  label: string;
  /** Static path or async resolver */
  path: string | (() => Promise<string>);
  /** Expected URL regex after navigation (default: path contained in URL) */
  expectedUrl?: RegExp;
  /** Function returning the heading locator */
  heading: (page: Page) => Locator;
  /** Function returning the primary control locator */
  control: (page: Page) => Locator;
}

async function resolvePath(entry: StructuralScreen): Promise<string> {
  return typeof entry.path === "function" ? await entry.path() : entry.path;
}

async function assertScreen(
  page: Page,
  opts: {
    viewport: number;
    label: string;
    path: string;
    expectedUrl?: RegExp;
    role: string;
    obs: ReturnType<typeof collectObservability>;
    heading: Locator;
    control: Locator;
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

// Data-driven table — add a route here to cover a new screen
function authenticatedScreens(deps: {
  ticketId: string;
  workflowHref: string;
  categoryEditHref: string;
  userEditHref: string;
}): StructuralScreen[] {
  return [
    { label: `/tickets`, path: "/tickets", heading: (p) => p.locator('h1:has-text("Tickets")'), control: (p) => p.getByRole("link", { name: /new ticket/i }).or(p.locator('input[aria-label="Search tickets"]')) },
    { label: `/tickets/new`, path: "/tickets/new", heading: (p) => p.locator('h1:has-text("New ticket")'), control: (p) => p.getByRole("button", { name: /create ticket/i }) },
    { label: `/tickets/{id}`, path: () => Promise.resolve(`/tickets/${deps.ticketId}`), heading: (p) => p.locator("#ticket-detail"), control: (p) => p.locator("#ticket-detail").locator('textarea, [aria-label="Ticket title"], button:has-text("Add comment")').first() },
    { label: `/users`, path: "/users", heading: (p) => p.locator("#users-list-title"), control: (p) => p.getByRole("link", { name: /new user/i }) },
    { label: `/users/new`, path: "/users/new", heading: (p) => p.locator('h1:has-text("New user")'), control: (p) => p.getByRole("button", { name: /create user/i }) },
    { label: `/users/{id}/edit`, path: () => Promise.resolve(deps.userEditHref), heading: (p) => p.locator("h2").filter({ hasText: /edit user|operator details/i }), control: (p) => p.getByRole("button", { name: /save changes/i }) },
    { label: `/categories`, path: "/categories", heading: (p) => p.locator('h1:has-text("Categories")'), control: (p) => p.getByRole("link", { name: /new category/i }) },
    { label: `/categories/new`, path: "/categories/new", heading: (p) => p.locator('h1:has-text("New category")'), control: (p) => p.getByRole("button", { name: /create category|save/i }) },
    { label: `/categories/{id}/edit`, path: () => Promise.resolve(deps.categoryEditHref), heading: (p) => p.locator('h1:has-text("Rename category")'), control: (p) => p.getByRole("button", { name: /save/i }) },
    { label: `/categories/{id}/workflow`, path: () => Promise.resolve(deps.workflowHref), heading: (p) => p.locator("h1").filter({ hasText: /category workflow/i }), control: (p) => p.locator("#workflow-builder") },
    { label: `/desks`, path: "/desks", heading: (p) => p.locator('h1:has-text("Desks")'), control: (p) => p.locator("details.desk-create summary") },
    { label: `/settings`, path: "/settings", heading: (p) => p.locator('h1:has-text("Settings")'), control: (p) => p.locator('input[name="internal_comment_bg"]') },
  ];
}

let suiteDeps: { ticketId: string; workflowHref: string; categoryEditHref: string; userEditHref: string } | undefined;

test.describe("Structural — seeded canonical screens", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  for (const vp of viewports) {
    test(`seeded structural baselines at ${vp.label}`, async ({ page, context }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height });
      const obs = collectObservability(page);

      // ---- Step 1: prepare dynamic IDs (login first, create data) ----
      // Resolve dynamic IDs once per test suite (lazy singleton)
      if (!suiteDeps) {
        await page.goto(base() + "/login", { waitUntil: "networkidle" });
        await expect(page.getByLabel(/email/i)).toBeVisible({ timeout: 10_000 });
        await page.getByLabel(/email/i).fill(seededCredentials.email);
        await page.getByLabel(/password/i).fill(seededCredentials.password);
        await page.getByRole("button", { name: /log in|sign in/i }).click();
        await expect(page).toHaveURL(/\/tickets/);

        const ticketTitle = "Structural ticket " + Date.now() + Math.random().toString(36).slice(2, 6);
        const ticketId = await createTicketViaUi(page, {
          title: ticketTitle,
          description: "probe",
          category: "General",
          priority: "high",
        });

        await page.goto(base() + "/categories");
        const workflowHref = await resolveWorkflowHref(page);
        const categoryEditHref = await resolveCategoryEditHref(page, "General");

        const seededUserName = "StructUser " + Date.now().toString(36).slice(2, 6);
        const seededUserEmail = `struct-${Date.now().toString(36).slice(2, 8)}@example.com`;
        await page.goto(base() + "/users/new");
        await page.getByLabel(/^name$/i).fill(seededUserName);
        await page.getByLabel(/^email$/i).fill(seededUserEmail);
        await page.getByLabel(/^password$/i).fill("Secret123!");
        await page.getByRole("button", { name: /create user/i }).click();
        await expect(page).toHaveURL(/\/users/);

        await page.goto(base() + "/users");
        const userEditHref = await resolveUserEditHref(page);

        suiteDeps = { ticketId, workflowHref, categoryEditHref, userEditHref };
      }

      // Clear cookies so anonymous sections are truly unauthenticated
      await context.clearCookies();

      // --- Anonymous screens (seeded) ---
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

      // /setup when users already exist — anonymous redirects to /login
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

      // After anonymous screens, re-login for authenticated section
      await page.goto(base() + "/login", { waitUntil: "networkidle" });
      await expect(page.getByLabel(/email/i)).toBeVisible({ timeout: 10_000 });
      await page.getByLabel(/email/i).fill(seededCredentials.email);
      await page.getByLabel(/password/i).fill(seededCredentials.password);
      await page.getByRole("button", { name: /log in|sign in/i }).click();
      await expect(page).toHaveURL(/\/tickets/);

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

      // --- Data-driven authenticated screens ---
      const deps = suiteDeps!;

      for (const entry of authenticatedScreens(deps)) {
        const path = await resolvePath(entry);
        await assertScreen(page, {
          viewport: vp.width,
          label: `${entry.label} @ ${vp.label}`,
          path,
          expectedUrl: entry.expectedUrl,
          role: "root",
          obs,
          heading: entry.heading(page),
          control: entry.control(page),
        });
      }
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

        // /login with empty DB — redirects to /setup
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

        // /setup with empty DB — bootstrap form is reachable
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

        // / with empty DB — anonymous redirects to /setup
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

        // Perform first-user bootstrap via UI
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

        // After bootstrap, /setup must redirect to /tickets when authenticated
        await page.goto(base() + "/setup");
        await expect(page).toHaveURL(/\/tickets/);
        await assertCanonicalScreen(page, {
          viewport: vp.width,
          label: `/setup with users (authenticated redirect, empty bootstrapped) @ ${vp.label}`,
          url: page.url(),
          role: "root",
          consoleErrors: obs.consoleErrors,
          pageErrors: obs.pageErrors,
          failedRequests: obs.failedRequests,
          failedResponses: obs.failedResponses,
        });
      } finally {
        await stopServer();
      }
    });
  }
});