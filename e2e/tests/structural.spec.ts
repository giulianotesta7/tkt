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
import { assertHtmxSwap } from "./helpers/htmx.js";
import {
  base,
  createUserAsAdmin,
  loginAs,
  loginAsSeeded,
  seededCredentials,
} from "./helpers/auth.js";
import {
  createTicketViaUi,
  resolveCategoryEditHref,
  resolveUserEditHref,
  resolveWorkflowHref,
} from "./helpers/navigation.js";

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
  categorySLAHref: string;
  userEditHref: string;
}): StructuralScreen[] {
  return [
    {
      label: `/tickets`,
      path: "/tickets",
      heading: (p) => p.locator('h1:has-text("Tickets")'),
      control: (p) =>
        p
          .getByRole("link", { name: /new ticket/i })
          .or(p.locator('input[aria-label="Search tickets"]')),
    },
    {
      // Single page is the shipped creation flow (issue #391, S1/L2): the form
      // renders directly and its category is a picker in the properties panel.
      label: `/tickets/new`,
      path: "/tickets/new",
      heading: (p) => p.getByRole("heading", { name: /create a ticket/i }),
      control: (p) => p.getByLabel("Title"),
    },
    {
      label: `/tickets/{id}`,
      path: () => Promise.resolve(`/tickets/${deps.ticketId}`),
      heading: (p) => p.locator("#ticket-detail"),
      control: (p) =>
        p
          .locator("#ticket-detail")
          .locator('textarea, [aria-label="Ticket title"], button:has-text("Reply")')
          .first(),
    },
    {
      // The metrics dashboard is a canonical screen: its charts, tables and
      // KPI cards must survive the 390px fold with no clipped content and no
      // horizontal page scroll (issue #393, S7).
      label: `/tickets/metrics`,
      path: "/tickets/metrics",
      heading: (p) => p.getByRole("heading", { name: "Ticket metrics", level: 1 }),
      control: (p) => p.getByRole("button", { name: "Apply", exact: true }),
    },
    {
      label: `/users`,
      path: "/users",
      heading: (p) => p.locator("#users-list-title"),
      control: (p) => p.getByRole("link", { name: /new user/i }),
    },
    {
      label: `/users/new`,
      path: "/users/new",
      heading: (p) => p.getByRole("heading", { name: "New user", exact: true }),
      control: (p) => p.getByRole("button", { name: /create user/i }),
    },
    {
      label: `/users/{id}/edit`,
      path: () => Promise.resolve(deps.userEditHref),
      heading: (p) => p.locator("h2").filter({ hasText: /edit user|operator details/i }),
      control: (p) => p.getByRole("button", { name: /save changes/i }),
    },
    {
      label: `/categories`,
      path: "/categories",
      heading: (p) => p.locator('h1:has-text("Catalog")'),
      // The tree replaced three creation launchers with one New menu; its trigger
      // is the screen's primary control, and the menu holds the three entries.
      control: (p) => p.locator("[data-catalog-new-toggle]"),
    },
    {
      label: `/categories/new`,
      path: "/categories/new",
      heading: (p) => p.getByRole("heading", { name: "New category", exact: true }),
      control: (p) => p.getByRole("button", { name: /create category/i }),
    },
    {
      label: `/categories/{id}/edit`,
      path: () => Promise.resolve(deps.categoryEditHref),
      heading: (p) => p.getByRole("heading", { name: /Edit category/i }),
      control: (p) => p.getByRole("button", { name: /save changes/i }),
    },
    {
      label: `/categories/{id}/workflow`,
      path: () => Promise.resolve(deps.workflowHref),
      heading: (p) => p.locator("h1").filter({ hasText: /category workflow/i }),
      control: (p) => p.locator("#workflow-builder"),
    },
    {
      label: `/categories/{id}/sla`,
      path: () => Promise.resolve(deps.categorySLAHref),
      heading: (p) => p.locator("h1").filter({ hasText: "Category SLA" }),
      control: (p) => p.locator(".sla-grid"),
    },
    {
      label: `/categories/desk-compatibility`,
      path: "/desks",
      expectedUrl: /\/categories$/,
      heading: (p) => p.locator('h1:has-text("Catalog")'),
      control: (p) => p.locator("[data-catalog-new-toggle]"),
    },
    // Settings is one section per URL since issue #383: each section is its
    // own canonical screen, and the bare /settings URL only redirects to the
    // first one.
    {
      label: `/settings (redirect)`,
      path: "/settings",
      expectedUrl: /\/settings\/organization$/,
      heading: (p) => p.locator('h1:has-text("Settings")'),
      control: (p) => p.locator("#org_name"),
    },
    {
      label: `/settings/organization`,
      path: "/settings/organization",
      heading: (p) => p.locator("h2").filter({ hasText: "Organization" }),
      control: (p) => p.locator("#org_name"),
    },
    {
      label: `/settings/language`,
      path: "/settings/language",
      heading: (p) => p.locator("h2").filter({ hasText: "Interface language" }),
      control: (p) => p.locator(".settings-segment"),
    },
    {
      label: `/settings/tickets`,
      path: "/settings/tickets",
      heading: (p) =>
        p.locator('.settings-nav-link[aria-current="page"]').filter({ hasText: "Tickets & SLA" }),
      control: (p) => p.locator('input[name="internal_comment_bg"]'),
    },
    {
      label: `/settings/signin`,
      path: "/settings/signin",
      heading: (p) =>
        p.locator('.settings-nav-link[aria-current="page"]').filter({ hasText: "Sign-in page" }),
      control: (p) => p.locator("#signin_title"),
    },
    {
      label: `/settings/email`,
      path: "/settings/email",
      heading: (p) =>
        p
          .locator('.settings-nav-link[aria-current="page"]')
          .filter({ hasText: "Email notifications" }),
      control: (p) => p.locator("#email_host"),
    },
    {
      label: `/settings/security`,
      path: "/settings/security",
      heading: (p) =>
        p.locator('.settings-nav-link[aria-current="page"]').filter({ hasText: "Security" }),
      control: (p) => p.locator("#password_min_length"),
    },
    {
      label: `/settings/calendar`,
      path: "/settings/calendar",
      heading: (p) => p.locator("h2").filter({ hasText: "Working calendar" }),
      control: (p) => p.locator('input[name="sla_calendar_days"]'),
    },
    {
      label: `/notifications`,
      path: "/notifications",
      heading: (p) => p.locator('h1:has-text("Notifications")'),
      control: (p) => p.getByRole("link", { name: /^Notifications/ }),
    },
    {
      // Saved replies is a staff screen of its own (issue #418): the operator's
      // own texts and the ones a desk shares, one screen with one tab per set.
      label: `/replies`,
      path: "/replies",
      heading: (p) => p.locator('h1:has-text("Saved replies")'),
      control: (p) => p.getByRole("link", { name: "New reply", exact: true }),
    },
    {
      label: `/preferences`,
      path: "/preferences",
      heading: (p) => p.locator('h1:has-text("Preferences")'),
      control: (p) => p.getByLabel("Default activity order", { exact: true }),
    },
  ];
}

let fixture:
  | {
      ticketId: string;
      workflowHref: string;
      categoryEditHref: string;
      categorySLAHref: string;
      userEditHref: string;
      seededUserName: string;
      seededUserEmail: string;
    }
  | undefined;

test.describe("Structural — seeded canonical screens", () => {
  test.beforeAll(async ({ browser }) => {
    await startServer({ seed: true });
    // Prepare dynamic IDs once in an isolated page
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    try {
      await page.goto(base() + "/login", { waitUntil: "networkidle" });
      await expect(page.getByLabel(/email/i)).toBeVisible({ timeout: 10_000 });
      await page.getByLabel(/email/i).fill(seededCredentials.email);
      await page.getByLabel(/password/i).fill(seededCredentials.password);
      await page.getByRole("button", { name: /log in|sign in/i }).click();
      await expect(page).toHaveURL(/\/tickets/);

      // Long on purpose: the title field is only exercised when its value exceeds the
      // column, and a short fixture is what let the truncated title (#199) pass every
      // baseline. Real ticket titles run to ~70 characters.
      const ticketTitle =
        "Structural ticket with a title long enough that the field must fit all of it " +
        Date.now() +
        Math.random().toString(36).slice(2, 6);
      const ticketId = await createTicketViaUi(page, {
        title: ticketTitle,
        description: "probe",
        category: "General",
        priority: "high",
      });

      await page.goto(base() + "/categories");
      const workflowHref = await resolveWorkflowHref(page);
      const categoryEditHref = await resolveCategoryEditHref(page, "General");
      const categorySLAHref = categoryEditHref.split("?")[0].replace(/\/edit$/, "/sla");

      const seededUserName = "StructUser " + Date.now().toString(36).slice(2, 6);
      const seededUserEmail = `struct-${Date.now().toString(36).slice(2, 8)}@example.com`;
      await page.goto(base() + "/users/new");
      await page.getByLabel(/^name$/i).fill(seededUserName);
      await page.getByLabel(/^email$/i).fill(seededUserEmail);
      await page.getByLabel(/^password$/i).fill("Secret123!42");
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

      await page.goto(base() + "/users");
      const userEditHref = await resolveUserEditHref(page, seededUserName);

      fixture = {
        ticketId,
        workflowHref,
        categoryEditHref,
        categorySLAHref,
        userEditHref,
        seededUserName,
        seededUserEmail,
      };
    } finally {
      await ctx.close();
    }
  });
  test.afterAll(async () => {
    await stopServer();
  });

  for (const vp of viewports) {
    test(`seeded structural baselines at ${vp.label}`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height });
      const obs = collectObservability(page);
      const deps = fixture!;

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

test.describe("Structural — requester list (issue #411)", () => {
  /**
   * The requester's "My tickets" list is a canonical screen of its own since
   * issue #411: a full-width list with the Needs-your-action section, an open
   * section and a folded closed section. It has to survive the 390px fold and both themes
   * with nothing clipped, no horizontal scroll, and a single active nav mark,
   * exactly like every other canonical screen.
   */
  test("requester /tickets at 390px and 1280px in both themes", async ({ page }) => {
    await startServer({ seed: true });
    try {
      const email = `requester-baseline-${Date.now().toString(36).slice(2, 8)}@example.com`;
      await loginAsSeeded(page);
      // The instance minimum is 12 characters (issue #414, S4), so this fixture
      // uses the same length every other generated user does.
      await createUserAsAdmin(page, {
        name: "Requester Baseline",
        email,
        password: "Secret123!42",
      });
      await page.context().clearCookies();
      await loginAs(page, email, "Secret123!42");
      await createTicketViaUi(page, {
        title: `Requester baseline ${Date.now().toString(36).slice(2, 8)}`,
        description: "baseline probe",
        category: "General",
        priority: "high",
      });
      await page.goto(base() + "/tickets");
      await expect(page.getByRole("heading", { name: "My tickets" })).toBeVisible();
      await expect(page.locator("#open-tickets-title")).toHaveText("Open · 1");

      const obs = collectObservability(page);
      // Each pass loads the page AT its viewport: the shell's sidebar slides
      // off canvas with a transition, so a resized page measured mid-animation
      // would read a half-hidden rail rather than the settled layout.
      for (const theme of ["light", "dark"] as const) {
        await page.emulateMedia({ colorScheme: theme });
        for (const viewport of [390, 1280] as const) {
          await page.setViewportSize({ width: viewport, height: viewport === 390 ? 844 : 800 });
          await page.goto(base() + "/tickets");
          await expect(page.getByRole("heading", { name: "My tickets" })).toBeVisible();
          await expect(page.locator("#open-tickets-title")).toHaveText("Open · 1");
          await assertCanonicalScreen(page, {
            viewport,
            label: `requester /tickets (${theme}) @ ${viewport}px`,
            url: page.url(),
            role: "user",
            consoleErrors: obs.consoleErrors,
            pageErrors: obs.pageErrors,
            failedRequests: obs.failedRequests,
            failedResponses: obs.failedResponses,
          });
        }
      }
    } finally {
      await stopServer();
    }
  });
});
