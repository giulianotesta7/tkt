/**
 * Ticket detail journeys: Properties sidebar, closed-state comment rejection, priority change.
 *
 * The canonical public-comment journey lives in tickets.spec.ts (native POST 303 + timeline + persistence).
 * Here: structural detail contract (Properties, state, category, description, timeline),
 * browser-visible comment-form rejection on closed states, and priority change via HTMX swap.
 *
 * Exhaustive HTTP rejection (403/422) for direct POSTs is covered by Go tests
 * (internal/adapters/http/handlers_comment_test.go).
 */

import { test, expect, type Page, type Request, type Route } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import {
  loginAsSeeded,
  base,
  createUserAsAdmin,
  grantUserRole,
  loginAs,
  logout,
  setSLAEnabled,
} from "./helpers/auth.js";
import {
  assertCanonicalScreen,
  collectObservability,
  expectHairlineBorder,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";
import { createTicketViaUi, resolveUserEditHref } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";
import {
  applyAssignee,
  pickAssignee,
  pickPriority,
  applyHold,
  applyPriority,
  applyResume,
  applyTransition,
  composer,
  currentAssignee,
  currentPriority,
  openPicker,
  picker,
  pickerButton,
  pickerOption,
  ticketId,
  transitionAction,
} from "./helpers/ticket.js";

/** The staff-only SLA panel of the ticket detail page. */
function slaPanel(page: Page) {
  return page
    .locator("#ticket-detail .prop-section")
    .filter({ has: page.locator(".prop-heading", { hasText: /^SLA/ }) });
}

/**
 * Create a user through the managed-users screen and promote them to agent, so
 * they join the instance-wide assignee candidates. Returns the edit href so a
 * journey can deactivate the same operator later.
 */
async function createAssignableAgent(
  page: Page,
  name: string,
): Promise<{ name: string; editHref: string }> {
  const slug = name.replace(/\s+/g, "-").toLowerCase();
  const unique = Math.random().toString(36).slice(2, 8);
  await createUserAsAdmin(page, {
    name,
    email: `${slug}-${unique}@tkt.test`,
    password: "Secret123!42",
  });
  await page.goto(base() + "/users");
  const editHref = await resolveUserEditHref(page, name);
  await page.goto(base() + editHref);
  await page.locator('select[name="role"]').selectOption("agent");
  await page.getByRole("button", { name: /save changes/i }).click();
  await expect(page).toHaveURL(/\/users$/);
  return { name, editHref };
}

/**
 * Create an account for one journey. The name carries the caller's unique
 * token, so the account resolves to exactly one row; the role is the one the
 * journey needs, granted the way an operator grants it.
 */
async function createAccount(
  page: Page,
  name: string,
  role?: "agent" | "admin",
): Promise<{ name: string; email: string; password: string }> {
  const suffix = Math.random().toString(36).slice(2, 8);
  const account = {
    name,
    email: `${name.replace(/\s+/g, "-").toLowerCase()}-${suffix}@example.com`,
    password: "Secret123!42",
  };
  await createUserAsAdmin(page, account);
  if (role) await grantUserRole(page, name, role);
  return account;
}

test.describe("Ticket detail", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("mention search Enter never publishes an unmatched draft and keeps keyboard editing", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const name = "Mention Keyboard Admin";
    await createUserAsAdmin(page, {
      name,
      email: "mention-keyboard@example.com",
      password: "KeyboardSecret42!",
    });
    await page.goto(base() + (await resolveUserEditHref(page, name)));
    await page.locator('select[name="role"]').selectOption("admin");
    await page.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/users$/);
    const id = await createTicketViaUi(page, {
      title: "Mention keyboard draft",
      category: "General",
    });
    await page.goto(base() + `/tickets/${id}`);
    const body = page.locator("#comment-body");
    const form = page.locator("form.comment-form");
    const search = page.getByRole("searchbox", { name: "Search people" });
    const draft = "This draft must remain unpublished";
    const commentPath = `/tickets/${id}/comments`;
    const posts: string[] = [];
    let navigations = 0;
    page.on("request", (request) => {
      if (request.method() === "POST") posts.push(new URL(request.url()).pathname);
    });
    page.on("framenavigated", (frame) => {
      if (frame === page.mainFrame()) navigations++;
    });
    await body.fill(draft);
    await page.getByRole("button", { name: "Mention", exact: true }).click();
    await search.fill("zzzz-unmatched-person");
    await expect(page.locator(".comment-mentions-option:visible")).toHaveCount(0);
    await page.evaluate(() => {
      document.addEventListener("keydown", (event) => {
        if (event.key === "Enter")
          document.body.dataset.enterPrevented = String(event.defaultPrevented);
      });
    });
    await search.press("Enter");
    await expect(page.locator("body")).toHaveAttribute("data-enter-prevented", "true");
    await expect(body).toHaveValue(draft);
    expect(posts).toEqual([]);
    expect(navigations).toBe(0);
    await expect(page.locator("#timeline")).not.toContainText(draft);
    // A fresh document proves the failed search did not persist a comment.
    await page.reload();
    await expect(page.locator("#timeline")).not.toContainText(draft);
    await body.fill(draft);
    await page.getByRole("button", { name: "Mention", exact: true }).click();
    await search.fill(name);
    await expect(page.getByRole("option", { name: new RegExp(name) })).toBeVisible();
    await search.press("ArrowDown");
    await search.press("Enter");
    await expect(body).toHaveValue(`${draft}@${name} `);
    expect(posts).toEqual([]);
    // A textarea with no matching completion still owns its newline.
    await body.fill("@zzzz-unmatched-person");
    await body.press("End");
    await body.press("Enter");
    await expect(body).toHaveValue("@zzzz-unmatched-person\n");
    const published = "Keyboard editing stays native\nSecond line";
    await body.fill("Keyboard editing stays native");
    await body.press("End");
    await body.press("Enter");
    await body.pressSequentially("Second line");
    await expect(body).toHaveValue(published);
    expect(posts).toEqual([]);
    const response = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" && new URL(response.url()).pathname === commentPath,
    );
    await form.locator('button[type="submit"]').click();
    const submitted = await response;
    expect(submitted.status()).toBe(303);
    expect(submitted.request().headers()["hx-request"]).toBeUndefined();
    await expect(page.locator("#timeline")).toContainText("Keyboard editing stays native");
    await page.reload();
    await expect(page.locator("#timeline")).toContainText("Second line");
    expect(posts).toEqual([commentPath]);
    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });

  test("detail shows Properties sidebar, timeline, state, description, and category", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Detail probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "detail probe",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByText("Properties").first()).toBeVisible();
    await expect(page.getByText("Requester", { exact: true })).toBeVisible();
    await expect(page.getByText("Category")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText("General");
    // Issue #386 (S1): the state badge moved to the header, beside the title.
    await expect(page.locator("#ticket-detail .detail-state .badge")).toHaveText("New");
    await expect(page.locator("#timeline")).toBeVisible();
    await expect(page.getByText("Description")).toBeVisible();

    // Requester-owned, unassigned ticket with a pending manual step: the
    // requester reads the step checklist, but the current step offers no
    // control and states honestly that nobody owns it yet.
    const passiveChecklist = page.locator("#workflow-pending");
    await expect(passiveChecklist).toBeVisible();
    await expect(passiveChecklist).toHaveClass(/workflow-pending-info/);
    await expect(passiveChecklist.locator("h3")).toHaveText("Steps");
    await expect(passiveChecklist.locator(".workflow-checklist-count")).toHaveText("0 of 1 done");
    const passiveStep = passiveChecklist.locator(".workflow-checklist-step.is-current");
    await expect(passiveStep.locator(".workflow-checklist-name")).toHaveText("Handle the ticket");
    await expect(passiveStep.locator(".workflow-checklist-meta")).toHaveText(
      "Nobody is assigned yet.",
    );
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(
      /workflow-pending-info/,
    );
    await expect(page.locator(".current-task-card")).toHaveCount(0);
    await expect(page.locator('form[action*="/workflow/steps/"]')).toHaveCount(0);

    // The compact passive projection must not introduce horizontal overflow
    // on the mobile breakpoint.
    await page.setViewportSize({ width: 390, height: 844 });
    const mobileOverflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    );
    expect(mobileOverflow).toBe(false);
    await page.setViewportSize({ width: 1280, height: 800 });

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail properties",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("the detail names the ticket in one h1 and keeps the description's line breaks", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const title = "VPN drops on the corporate network " + Date.now().toString(36).slice(2, 8);
    const description = "Steps to reproduce:\n1. Connect\n2. Wait";
    const id = await createTicketViaUi(page, {
      title,
      description,
      category: "General",
      priority: "high",
    });

    // The defect was measured at the phone breakpoint, so the assertions run
    // there: the title has 362px, not a desktop column.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    // S2: exactly one h1, and it names the ticket, so the browser tab, the
    // outline and the heading all agree about which ticket this is.
    const heading = page.locator("#ticket-detail h1");
    await expect(heading).toHaveCount(1);
    await expect(heading).toHaveText(title);
    const number = (await page.locator("#ticket-detail .meta-row .num").first().innerText()).trim();
    expect(number).toMatch(/^TKT-\d+$/);
    // Since issue #383 the title ends with the help desk name, "Help desk" until
    // an administrator configures the organization.
    await expect(page).toHaveTitle(`${number} · ${title} · Help desk`);

    // The editable title never clips silently: it carries an ellipsis
    // affordance and the whole value in its title attribute.
    const input = page.locator("#ticket-title");
    await expect(input).toHaveCSS("text-overflow", "ellipsis");
    await expect(input).toHaveAttribute("title", title);

    // S3: the description keeps the breaks the author typed.
    const rendered = page.locator("#ticket-detail .ticket-description");
    await expect(rendered).toHaveText(description);
    const metrics = await rendered.evaluate((el) => {
      const style = getComputedStyle(el);
      return {
        whiteSpace: style.whiteSpace,
        lineHeight: Number.parseFloat(style.lineHeight),
        height: el.getBoundingClientRect().height,
      };
    });
    expect(metrics.whiteSpace, "the description must render its authored line breaks").toBe(
      "pre-line",
    );
    expect(Number.isFinite(metrics.lineHeight), "the line height must be a length").toBe(true);
    expect(
      metrics.height,
      `three authored lines must occupy at least three line boxes, got ${metrics.height}px at ${metrics.lineHeight}px each`,
    ).toBeGreaterThanOrEqual(metrics.lineHeight * 2.5);
  });

  test("comment form hidden on closed states; requester-owned close blocked", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Closed comment probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "detail probe",
      category: "General",
      priority: "high",
    });

    // Drive ticket to resolved via transitions new → in_progress → resolved
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(page.locator("#ticket-detail")).toBeVisible();
      await expect(page.locator("#ticket-detail .detail-actions")).toBeVisible();
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await applyTransition(page, target);
        },
        {
          endpoint: `/tickets/${id}/transition`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      expect(resp.status()).toBe(200);
    }
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.getByText("Resolved").first()).toBeVisible({ timeout: 10_000 });
    const beforeTimeline = await page.locator("#timeline").textContent();
    // The ticket requester (the creating admin here) keeps the comment form in
    // resolved via the requester carve-out (issue #55); non-requesters do not.
    await expect(page.getByLabel(/comment body/i)).toHaveCount(1);
    await page.reload();
    const afterTimeline = await page.locator("#timeline").textContent();
    expect(afterTimeline).toEqual(beforeTimeline);

    // A requester-owned resolved ticket can no longer be closed via the
    // state transition (issue #55 closure gate): the Move-to control offers
    // only the reopen (in_progress), never `closed`, and the state stays
    // resolved until the requester confirms. Direct-POST rejection (403) is
    // exhaustively covered by Go tests (see spec header note).
    await page.goto(base() + `/tickets/${id}`);
    // The closure gate keeps the header from offering Close: the closed target
    // is dropped from the rendered actions for every actor.
    await expect(
      page.locator(
        '#ticket-detail .detail-actions form.detail-action:has(input[name="to"][value="closed"])',
      ),
    ).toHaveCount(0);
    // The requester keeps the comment form (carve-out); only the transition
    // to closed is blocked. The comment form stays for the requester.
    await expect(page.getByLabel(/comment body/i)).toHaveCount(1);
    // State badge stays resolved (no silent closure).
    await expect(page.getByText("Resolved").first()).toBeVisible({ timeout: 10_000 });

    // Cancelled state: create a fresh ticket and cancel from new
    const cancelTitle = "Cancel probe " + Date.now().toString(36).slice(2, 8);
    const cancelId = await createTicketViaUi(page, {
      title: cancelTitle,
      description: "detail probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${cancelId}`);
    await expect(transitionAction(page, "cancelled")).toBeVisible();
    {
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await applyTransition(page, "cancelled");
        },
        {
          endpoint: `/tickets/${cancelId}/transition`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      expect(resp.status()).toBe(200);
    }
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.goto(base() + `/tickets/${cancelId}`);
    await expect(page.getByText("Cancelled").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail comment rejection visible",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a closed ticket reopens from Move to once the reason is revealed and applied", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const title = "Reopen probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "closed reopen reason journey",
      category: "General",
      priority: "high",
    });

    // Drive the ticket to resolved, then the requester (the creating admin)
    // confirms, which is the only UI path to `closed`.
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(page.locator("#ticket-detail .detail-actions")).toBeVisible();
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await applyTransition(page, target);
        },
        {
          endpoint: `/tickets/${id}/transition`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      expect(resp.status()).toBe(200);
    }
    await page.goto(base() + `/tickets/${id}`);
    const confirmBtn = page.getByRole("button", { name: /Yes, close ticket/i });
    await expect(confirmBtn).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await confirmBtn.click();
      },
      {
        endpoint: `/tickets/${id}/confirmation`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.getByText("Closed").first()).toBeVisible({ timeout: 10_000 });

    // On a closed ticket the only header action is the reason-requiring reopen
    // (`in_progress`). Its dialog appears and carries a REQUIRED reason: the
    // button never posts until the reason is typed and submitted.
    const reasonOpener = transitionAction(page, "in_progress");
    await expect(reasonOpener).toBeVisible();
    await expect(reasonOpener).toHaveText("Reopen");
    const dialog = page.locator("#ticket-transition-dialog");
    await expect(dialog).toBeHidden();
    await reasonOpener.click();
    await expect(dialog).toBeVisible();
    const reason = page.locator("#state-reason");
    await expect(reason).toBeFocused();
    await expect(reason).toHaveAttribute("required", "");
    // Escape closes it and hands the focus back, without a request.
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(reasonOpener).toBeFocused();

    // The reopen swap changes the header by design: a closed ticket renders an
    // h1, an editable in_progress ticket renders the title form. That is the
    // one swap where assertHtmxSwap's "unchanged h1 chrome" clause cannot
    // hold, so prove the same HTMX contract inline and assert the header change
    // as the visible domain result.
    const navigations: string[] = [];
    const navigationHandler = (request: Request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push(request.url());
      }
    };
    const reopenReason = `Customer replied ${Date.now().toString(36).slice(2, 6)}`;
    page.on("request", navigationHandler);
    try {
      const urlBefore = page.url();
      const detailBefore = await page.locator("#ticket-detail").innerHTML();
      const transitionResponse = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          response.request().headers()["hx-request"] === "true" &&
          new URL(response.url()).pathname === `/tickets/${id}/transition`,
      );
      await reasonOpener.click();
      await expect(dialog).toBeVisible();
      await page.locator("#state-reason").fill(reopenReason);
      await dialog.locator('button[type="submit"]').click();
      const response = await transitionResponse;
      expect(response.status()).toBe(200);
      await expect.poll(() => page.locator("#ticket-detail").innerHTML()).not.toBe(detailBefore);
      expect(page.url()).toBe(urlBefore);
      expect(navigations, "the reopen swap must not navigate the main frame").toEqual([]);
      await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
      await expect(page.locator("#ticket-title")).toBeVisible();
      await expect(page.locator("#timeline")).toContainText(reopenReason);
    } finally {
      page.removeListener("request", navigationHandler);
    }

    // The reopen and its reason survive a reload.
    await page.reload();
    await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.locator("#timeline")).toContainText(reopenReason);
  });

  test("priority change via HTMX swap updates #ticket-detail without full navigation", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "HTMX probe " + Date.now().toString(36).slice(2, 8),
      description: "detail probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    // Complementary evidence: hx-post targeting #ticket-detail exists
    const hxForms = page.locator('[hx-post][hx-target="#ticket-detail"]');
    await expect(hxForms.first()).toBeVisible();

    await expect(pickerButton(page, "ticket-priority")).toBeVisible();

    const prioritySwap = await assertHtmxSwap(
      page,
      async () => {
        await applyPriority(page, "critical");
      },
      {
        endpoint: `/tickets/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    // Issue #386 (S5): the picker replaced the explicit Apply, and the
    // maintainer's chosen option promises the same "Saved" confirmation the
    // other explicit-save surfaces show. Assert the swapped value AND the toast.
    await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");
    expect(prioritySwap.headers()["x-save-feedback"]).toContain('"message":"Saved"');
    await expect(page.locator("#save-feedback")).toBeVisible();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail HTMX swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("typed comment survives a property apply that swaps #ticket-detail", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Comment draft probe " + Date.now().toString(36).slice(2, 8),
      description: "unsent comment survives a swap",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const comment = page.getByLabel(/comment body/i);
    await expect(comment).toBeVisible();
    const draft = "unsent draft " + Date.now().toString(36);
    await comment.fill(draft);

    // Applying a property replaces the whole #ticket-detail fragment in place,
    // so the typed comment must be carried across that outerHTML swap.
    await assertHtmxSwap(
      page,
      async () => {
        await applyPriority(page, "critical");
      },
      {
        endpoint: `/tickets/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(page.locator("#ticket-detail")).toContainText(/critical/i);
    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);
  });

  test("typed comment survives an apply that fails with a non-2xx response", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Comment draft failure probe " + Date.now().toString(36).slice(2, 8),
      description: "unsent comment survives a failed apply",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const comment = page.getByLabel(/comment body/i);
    await expect(comment).toBeVisible();
    const draft = "unsent draft " + Date.now().toString(36);
    await comment.fill(draft);

    // A non-2xx reply must not cost the operator their draft. htmx does not
    // swap on an error status, so the fragment (and the typed text) stays put.
    await page.route(`**/tickets/${id}/edit`, (route) =>
      route.fulfill({ status: 500, contentType: "text/plain", body: "boom" }),
    );
    try {
      await Promise.all([
        page.waitForResponse(
          (response) =>
            response.request().method() === "POST" &&
            new URL(response.url()).pathname === `/tickets/${id}/edit`,
        ),
        applyPriority(page, "critical"),
      ]);
    } finally {
      await page.unroute(`**/tickets/${id}/edit`);
    }

    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);
  });

  test("an internal draft keeps its visibility across a priority apply", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Internal draft priority " + Date.now().toString(36).slice(2, 8),
      description: "internal draft survives a priority apply",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const comment = page.getByLabel(/comment body/i);
    const internal = page.getByLabel("Internal comment");
    await expect(comment).toBeVisible();
    // The form defaults to public and re-renders unchecked; the author opts in.
    await expect(internal).not.toBeChecked();
    const draft = "internal draft " + Date.now().toString(36);
    await internal.check();
    await comment.fill(draft);

    await assertHtmxSwap(
      page,
      async () => {
        await applyPriority(page, "critical");
      },
      {
        endpoint: `/tickets/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(page.locator("#ticket-detail")).toContainText(/critical/i);
    // #261 preserved the typed text; #308 preserves the selected visibility too.
    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);
    await expect(page.getByLabel("Internal comment")).toBeChecked();
    // Applying a property is not posting the comment: nothing was stored.
    await expect(page.locator("#timeline")).not.toContainText(draft);
  });

  test("an internal draft keeps its visibility across an assignee apply", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Internal draft assignee " + Date.now().toString(36).slice(2, 8),
      description: "internal draft survives an assignee apply",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    await expect(pickerButton(page, "assign-user")).toBeVisible();
    const comment = page.getByLabel(/comment body/i);
    const internal = page.getByLabel("Internal comment");
    const draft = "internal draft " + Date.now().toString(36);
    await internal.check();
    await comment.fill(draft);

    // The first candidate the picker offers that the ticket does not hold yet.
    const target = (await currentAssignee(page)) === "Unassigned" ? "Alice Admin" : "Admin";
    await assertHtmxSwap(
      page,
      async () => {
        await applyAssignee(page, target);
      },
      {
        endpoint: `/tickets/${id}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    // Re-resolve after the swap: the fragment was replaced in place.
    await expect(pickerButton(page, "assign-user")).toContainText(target);
    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);
    await expect(page.getByLabel("Internal comment")).toBeChecked();
    await expect(page.locator("#timeline")).not.toContainText(draft);
  });

  test("a failed apply preserves the internal draft and its visibility", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Internal draft failure " + Date.now().toString(36).slice(2, 8),
      description: "internal draft survives a failed apply",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const comment = page.getByLabel(/comment body/i);
    const internal = page.getByLabel("Internal comment");
    const draft = "internal draft " + Date.now().toString(36);
    await internal.check();
    await comment.fill(draft);

    // A non-2xx reply must not cost the operator their draft or its visibility.
    await page.route(`**/tickets/${id}/edit`, (route) =>
      route.fulfill({ status: 500, contentType: "text/plain", body: "boom" }),
    );
    try {
      await Promise.all([
        page.waitForResponse(
          (response) =>
            response.request().method() === "POST" &&
            new URL(response.url()).pathname === `/tickets/${id}/edit`,
        ),
        applyPriority(page, "critical"),
      ]);
    } finally {
      await page.unroute(`**/tickets/${id}/edit`);
    }

    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);
    await expect(page.getByLabel("Internal comment")).toBeChecked();
  });

  test("a public draft stays public after an unrelated apply", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Public draft probe " + Date.now().toString(36).slice(2, 8),
      description: "public draft survives a priority apply",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const comment = page.getByLabel(/comment body/i);
    const internal = page.getByLabel("Internal comment");
    await expect(internal).not.toBeChecked();
    const draft = "public draft " + Date.now().toString(36);
    await comment.fill(draft);

    await assertHtmxSwap(
      page,
      async () => {
        await applyPriority(page, "critical");
      },
      {
        endpoint: `/tickets/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    // The author never changed visibility: the draft stays public.
    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);
    await expect(page.getByLabel("Internal comment")).not.toBeChecked();

    const submitted = waitForExactPost(page, `/tickets/${id}/comments`);
    await Promise.all([
      submitted,
      page.locator("form:has(#comment-body)").getByRole("button", { name: "Reply" }).click(),
    ]);
    expect((await submitted).status()).toBe(303);
    await expect(page.locator("#timeline")).toContainText("added a comment");
    await expect(page.locator("#timeline .timeline-comment.internal")).toHaveCount(0);
    await expect(page.locator("#timeline .timeline-comment")).toContainText(draft);
  });

  test("a retained internal draft is stored internal and stays hidden from the requester", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const requesterEmail = `internal-visibility-${Date.now().toString(36).slice(2, 8)}@tkt.test`;
    const requesterPassword = "Secret123!42";
    await createUserAsAdmin(page, {
      name: "Rosa Requester",
      email: requesterEmail,
      password: requesterPassword,
    });
    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);
    const id = await createTicketViaUi(page, {
      title: "Internal visibility " + Date.now().toString(36).slice(2, 8),
      description: "internal draft visibility",
      category: "General",
      priority: "high",
    });
    await logout(page);
    await loginAsSeeded(page);
    await page.goto(base() + `/tickets/${id}`);

    const comment = page.getByLabel(/comment body/i);
    const internal = page.getByLabel("Internal comment");
    await expect(internal).toBeVisible();
    const draft = "internal note " + Date.now().toString(36);
    await internal.check();
    await comment.fill(draft);

    // Retain the draft through a real property apply, then publish it.
    await assertHtmxSwap(
      page,
      async () => {
        await applyPriority(page, "critical");
      },
      {
        endpoint: `/tickets/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.getByLabel("Internal comment")).toBeChecked();
    await expect(page.getByLabel(/comment body/i)).toHaveValue(draft);

    const submitted = waitForExactPost(page, `/tickets/${id}/comments`);
    await Promise.all([
      submitted,
      page.locator("form:has(#comment-body)").getByRole("button", { name: "Reply" }).click(),
    ]);
    expect((await submitted).status()).toBe(303);
    await expect(page.locator("#timeline .timeline-comment.internal")).toContainText(draft);
    await expect(page.locator("#timeline")).toContainText("added an internal comment");

    // The requester must never receive the internal body or its marker.
    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#timeline")).not.toContainText(draft);
    await expect(page.locator("#timeline")).not.toContainText("added an internal comment");
    await expect(page.locator("#timeline .timeline-comment.internal")).toHaveCount(0);
  });

  test("a picker still saves with scripting disabled", async ({ browser }) => {
    // S5/L3: the options are real submit buttons, so the property changes
    // without a single line of script running. This context has JavaScript
    // switched off, which no other journey can prove.
    const context = await browser.newContext({ javaScriptEnabled: false });
    const page = await context.newPage();
    try {
      // The login form is a native POST, so signing in needs no script either.
      await page.goto(base() + "/login");
      await page.getByLabel(/email/i).fill("alice@example.com");
      await page.getByLabel(/password/i).fill("SuperSecret42!");
      await page.getByRole("button", { name: /log in|sign in/i }).click();
      await expect(page).toHaveURL(/\/tickets$/);
      // Fixture only: create the ticket over the same session with a plain form
      // post (the catalog picker page is script-only, so it cannot drive the
      // creation here). The journey itself — opening the ticket, picking, and
      // the resulting save — runs entirely through the no-script page.
      const categoryId = await page
        .goto(base() + "/tickets")
        .then(() =>
          page
            .locator('select[name="category_id"] option:not([value=""])')
            .first()
            .getAttribute("value"),
        );
      if (!categoryId) throw new Error(`no category to file the ticket under at ${page.url()}`);
      const created = await context.request.post(base() + "/tickets", {
        form: {
          title: `No-script picker ${Date.now()}`,
          description: "picked without scripts",
          category_id: categoryId,
          priority: "high",
        },
        maxRedirects: 0,
      });
      expect(created.status()).toBe(303);
      // The queue is server-rendered; its title link is the plain href a
      // no-script browser follows (the row-wide click is script-only).
      await page.goto(base() + "/tickets");
      const ticketHref = await page
        .locator('tr.queue-row a[href^="/tickets/"]')
        .first()
        .getAttribute("href");
      if (!ticketHref) throw new Error(`the created ticket is not in the queue at ${page.url()}`);
      await page.goto(new URL(ticketHref, page.url()).href);
      await expect(page.locator("#ticket-detail")).toBeVisible();
      const id = ticketId(page);

      // The picker's option list is rendered inline, so the pick is a plain
      // click on a submit button that posts its own field to the same route.
      const option = page.locator('#ticket-priority-options .listbox-option[value="low"]');
      await expect(option).toBeVisible();
      // The popover's own button is a script affordance and stays out of the way.
      await expect(pickerButton(page, "ticket-priority")).toBeHidden();
      // The script-only search field is hidden too: nothing invites typing into
      // a filter that cannot run.
      await expect(page.locator("#assign-user-options .listbox-search")).toBeHidden();

      await Promise.all([page.waitForNavigation(), option.click()]);
      await expect(page).toHaveURL(new RegExp(`/tickets/${id}$`));
      await expect(
        page.locator('#ticket-priority-options .listbox-option[aria-selected="true"]'),
      ).toContainText("Low");
      await expect(page.locator("#timeline")).toContainText("changed priority to Low");
    } finally {
      await context.close();
    }
  });

  test("a closed picker ignores the arrows and the wheel, and opening it changes nothing", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Picker guard " + Date.now().toString(36).slice(2, 8),
      description: "a closed picker must stay inert",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const assigneeButton = pickerButton(page, "assign-user");
    const priorityButton = pickerButton(page, "ticket-priority");
    await expect(assigneeButton).toBeVisible();
    await expect(priorityButton).toBeVisible();
    // The picker rests as a value on the card and draws its own hairline the
    // moment it is interactive (hover or expanded), so it never looks like dead
    // text. Read the computed value — no screenshot reveals a 1px border.
    await expect(priorityButton).toHaveCSS("border-top-color", "rgba(0, 0, 0, 0)");
    await priorityButton.hover();
    await expectHairlineBorder(priorityButton, "priority picker button on hover");
    await openPicker(page, "ticket-priority");
    await expectHairlineBorder(priorityButton, "priority picker button when expanded");
    await page.keyboard.press("Escape");

    // Every mutation the detail can make is recorded, so "nothing happened" is
    // evidence rather than an assumption.
    const posts: string[] = [];
    page.on("request", (request) => {
      if (request.method() === "POST") posts.push(new URL(request.url()).pathname);
    });

    // #232: no control carries a change-driven save. A bubbling `change` across
    // every input and button in the detail must not POST anything.
    await page.evaluate(() => {
      for (const control of document.querySelectorAll(
        "#ticket-detail input, #ticket-detail button",
      )) {
        control.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });
    await page.waitForTimeout(300);
    expect(posts, "a change event alone must not mutate the ticket").toEqual([]);

    // The closed picker ignores the arrow keys: a native closed select moved
    // its selection on ArrowDown and fired change, which is exactly how a
    // ticket was reassigned by accident before #232.
    const assigneeBefore = await currentAssignee(page);
    const priorityBefore = await currentPriority(page);
    await assigneeButton.focus();
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowUp");
    await priorityButton.focus();
    await page.keyboard.press("ArrowDown");
    await page.waitForTimeout(300);
    expect(posts, "the arrow keys on a closed picker must not mutate the ticket").toEqual([]);
    expect(await currentAssignee(page)).toBe(assigneeBefore);
    expect(await currentPriority(page)).toBe(priorityBefore);
    await expect(picker(page, "assign-user").locator(".listbox-popover")).toBeHidden();
    await expect(picker(page, "ticket-priority").locator(".listbox-popover")).toBeHidden();

    // The wheel over a closed picker does nothing either.
    await assigneeButton.hover();
    await page.mouse.wheel(0, 240);
    await page.waitForTimeout(300);
    expect(posts, "the wheel on a closed picker must not mutate the ticket").toEqual([]);
    expect(await currentAssignee(page)).toBe(assigneeBefore);

    // Opening and closing changes nothing: no request, no value, no selection.
    await openPicker(page, "assign-user");
    await page.keyboard.press("Escape");
    await expect(picker(page, "assign-user").locator(".listbox-popover")).toBeHidden();
    await expect(assigneeButton).toHaveAttribute("aria-expanded", "false");
    await expect(assigneeButton).toBeFocused();
    await openPicker(page, "ticket-priority");
    await page.keyboard.press("Escape");
    await expect(priorityButton).toHaveAttribute("aria-expanded", "false");
    await expect(posts, "opening and closing a picker must not mutate the ticket").toEqual([]);
    expect(await currentAssignee(page)).toBe(assigneeBefore);
    expect(await currentPriority(page)).toBe(priorityBefore);

    // The pick itself is the explicit act, and it is one field of one form: the
    // priority pick posts only `priority`, the assignment only `user_id`.
    const bodies: { path: string; body: URLSearchParams }[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      bodies.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
      });
    });
    await pickPriority(page, "critical");
    await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");
    const [priorityPost] = bodies;
    expect(priorityPost.path).toBe(`/tickets/${id}/edit`);
    expect(priorityPost.body.get("priority")).toBe("critical");
    expect(priorityPost.body.has("title")).toBe(false);
    expect(priorityPost.body.has("user_id")).toBe(false);

    await pickAssignee(page, "Alice Admin");
    await expect(pickerButton(page, "assign-user")).toContainText("Alice Admin");
    const assignPost = bodies[bodies.length - 1];
    expect(assignPost.path).toBe(`/tickets/${id}/assign`);
    expect(assignPost.body.has("user_id")).toBe(true);
    expect(assignPost.body.has("priority")).toBe(false);
  });

  test("a dirty title guards a sibling Apply and Save and continue ships both fields", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const originalTitle = "Title guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title priority guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await expect(titleInput).toHaveValue(originalTitle);
    const saveButton = page.locator("form:has(#ticket-title) .title-save");
    await expect(saveButton).toBeHidden();

    // The Save button mirrors dirtiness exactly: typed value shows it, the
    // original value hides it again.
    const editedTitle = `${originalTitle} edited`;
    await titleInput.fill(editedTitle);
    await expect(saveButton).toBeVisible();
    await titleInput.fill(originalTitle);
    await expect(saveButton).toBeHidden();
    await titleInput.fill(editedTitle);
    await expect(saveButton).toBeVisible();
    await expectHairlineBorder(saveButton, "detail title Save");

    const editPath = `/tickets/${id}/edit`;
    const posts: {
      path: string;
      body: URLSearchParams;
      htmx: string | undefined;
      target: string | undefined;
    }[] = [];
    const editResponses: number[] = [];
    const navigations: { method: string; url: string }[] = [];
    const detailUrl = page.url();
    page.on("request", (request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push({ method: request.method(), url: request.url() });
      }
      if (request.method() !== "POST") return;
      posts.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
        htmx: request.headers()["hx-request"],
        target: request.headers()["hx-target"],
      });
    });
    page.on("response", (response) => {
      if (response.request().method() === "POST" && new URL(response.url()).pathname === editPath) {
        editResponses.push(response.status());
      }
    });

    // A sibling pick must not silently destroy the typed title: the click is
    // intercepted and no request is issued until the user answers.
    await openPicker(page, "ticket-priority");
    await pickerOption(page, "ticket-priority", "critical").click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();
    await expectHairlineBorder(
      dialog.getByRole("button", { name: "Save and continue" }),
      "title guard dialog Save and continue",
    );
    // Discard is the danger twin: a FILLED control (styles.html:195 — border and
    // background both var(--red)), whose contrast comes from its fill, so the
    // hairline-vs-own-background contract does not apply; and its border-color is
    // ID-pinned, so the .btn base mutation cannot reach it either.
    await page.waitForTimeout(300);
    expect(posts, "opening the title guard must not POST").toEqual([]);

    // Consent: two explicit HTMX requests on the same /edit endpoint. The
    // title posts only its own field first, then the remembered priority row
    // posts only its own field. Neither request carries the other form's
    // field, and both swaps stay on the main frame.
    const detailBefore = await page.locator("#ticket-detail").innerHTML();
    await dialog.getByRole("button", { name: "Save and continue" }).click();

    await expect
      .poll(() => posts.length, {
        message: "Save and continue must issue the title POST then the priority POST",
      })
      .toBe(2);
    await expect.poll(() => editResponses.length).toBe(2);
    expect(editResponses).toEqual([200, 200]);
    const [titlePost, priorityPost] = posts;
    expect(titlePost.path).toBe(editPath);
    expect(titlePost.htmx).toBe("true");
    expect(titlePost.target).toBe("ticket-detail");
    expect(titlePost.body.get("title")).toBe(editedTitle);
    expect(titlePost.body.has("priority")).toBe(false);
    expect(priorityPost.path).toBe(editPath);
    expect(priorityPost.htmx).toBe("true");
    expect(priorityPost.target).toBe("ticket-detail");
    expect(priorityPost.body.get("priority")).toBe("critical");
    expect(priorityPost.body.has("title")).toBe(false);

    // Both swaps mutated the target region in place.
    await expect.poll(() => page.locator("#ticket-detail").innerHTML()).not.toBe(detailBefore);
    expect(navigations, "the two swaps must not navigate the main frame").toEqual([]);
    expect(page.url()).toBe(detailUrl);

    await expect(dialog).toBeHidden();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");

    await page.reload();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail dirty title guard",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("Save and continue ships the dirty title and the pending assignment on their own endpoints", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const originalTitle = "Assign guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title assignment guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await expect(titleInput).toHaveValue(originalTitle);
    const editedTitle = `${originalTitle} edited`;
    await titleInput.fill(editedTitle);

    await expect(pickerButton(page, "assign-user")).toBeVisible();
    // The picker lists the assignable people; pick the first one the ticket
    // does not already hold. The pick is the explicit act (S5).
    const assignee = "Alice Admin";
    const attemptedAssignee = "1";

    const editPath = `/tickets/${id}/edit`;
    const assignPath = `/tickets/${id}/assign`;
    const posts: {
      path: string;
      body: URLSearchParams;
      htmx: string | undefined;
      target: string | undefined;
    }[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      posts.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
        htmx: request.headers()["hx-request"],
        target: request.headers()["hx-target"],
      });
    });

    // A sibling pick with a dirty title is intercepted: nothing is posted
    // until the operator answers the guard.
    await openPicker(page, "assign-user");
    await pickerOption(page, "assign-user", attemptedAssignee).click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();
    await page.waitForTimeout(300);
    expect(posts, "opening the title guard must not POST").toEqual([]);

    // Save and continue must ship the title on its own endpoint, then the
    // remembered assignment on ITS endpoint. Before the fix the /edit copy
    // loop dropped the assignment while still rendering "Saved".
    const titleResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" && new URL(response.url()).pathname === editPath,
    );
    await dialog.getByRole("button", { name: "Save and continue" }).click();
    expect((await titleResponse).status()).toBe(200);

    await expect(dialog).toBeHidden();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(pickerButton(page, "assign-user")).toContainText(assignee);

    await expect
      .poll(() => posts.map((post) => post.path), {
        message: "Save and continue must issue the title POST then the assignment POST",
      })
      .toEqual([editPath, assignPath]);
    const [titlePost, assignPost] = posts;
    expect(titlePost.htmx).toBe("true");
    expect(titlePost.target).toBe("ticket-detail");
    expect(titlePost.body.get("title")).toBe(editedTitle);
    expect(titlePost.body.has("user_id")).toBe(false);
    expect(assignPost.htmx).toBe("true");
    expect(assignPost.target).toBe("ticket-detail");
    expect(assignPost.body.get("user_id")).toBe(attemptedAssignee);
    expect(assignPost.body.has("title")).toBe(false);

    await page.reload();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(pickerButton(page, "assign-user")).toContainText(assignee);
  });

  test("Save and continue ships the dirty title and the pending transition on their own endpoints", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const originalTitle = "State guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title transition guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await expect(titleInput).toHaveValue(originalTitle);
    const editedTitle = `${originalTitle} edited`;
    await titleInput.fill(editedTitle);

    const startAction = transitionAction(page, "in_progress");
    await expect(startAction).toBeVisible();

    const editPath = `/tickets/${id}/edit`;
    const transitionPath = `/tickets/${id}/transition`;
    const posts: {
      path: string;
      body: URLSearchParams;
      htmx: string | undefined;
      target: string | undefined;
    }[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      posts.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
        htmx: request.headers()["hx-request"],
        target: request.headers()["hx-target"],
      });
    });

    await startAction.click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();
    await page.waitForTimeout(300);
    expect(posts, "opening the title guard must not POST").toEqual([]);

    const titleResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" && new URL(response.url()).pathname === editPath,
    );
    await dialog.getByRole("button", { name: "Save and continue" }).click();
    expect((await titleResponse).status()).toBe(200);

    await expect(dialog).toBeHidden();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#ticket-detail .badge.in_progress")).toBeVisible();

    await expect
      .poll(() => posts.map((post) => post.path), {
        message: "Save and continue must issue the title POST then the transition POST",
      })
      .toEqual([editPath, transitionPath]);
    const [titlePost, transitionPost] = posts;
    expect(titlePost.htmx).toBe("true");
    expect(titlePost.target).toBe("ticket-detail");
    expect(titlePost.body.get("title")).toBe(editedTitle);
    expect(transitionPost.htmx).toBe("true");
    expect(transitionPost.target).toBe("ticket-detail");
    expect(transitionPost.body.get("to")).toBe("in_progress");
    expect(transitionPost.body.has("title")).toBe(false);

    await page.reload();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#ticket-detail .badge.in_progress")).toBeVisible();
  });

  test("Discard and continue lets the assignee Apply proceed and keeps the title", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const originalTitle = "Discard guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title assign discard guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await titleInput.fill(`${originalTitle} unsaved`);

    const attemptedAssignee = "Alice Admin";
    await openPicker(page, "assign-user");
    await picker(page, "assign-user")
      .locator(".listbox-option", { hasText: attemptedAssignee })
      .first()
      .click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();

    // Discard restores the persisted title, then submits the pending assign
    // form exactly as the explicit Apply would have.
    await assertHtmxSwap(
      page,
      async () => {
        await dialog.getByRole("button", { name: "Discard and continue" }).click();
      },
      {
        endpoint: `/tickets/${id}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(pickerButton(page, "assign-user")).toContainText(attemptedAssignee);
    await page.reload();
    await expect(pickerButton(page, "assign-user")).toContainText(attemptedAssignee);
    await expect(page.locator("#ticket-title")).toHaveValue(originalTitle);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail dirty title discard",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("persistent failure survives an unrelated drawer success beyond five seconds", async ({
    page,
  }) => {
    test.setTimeout(15_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Failure priority " + Date.now().toString(36).slice(2, 8),
      description: "feedback priority regression",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-priority")).toBeVisible();

    await page.evaluate(() => {
      const emit = (name: string, detail: object) =>
        document.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));
      const pageSource = document.querySelector("#ticket-priority");
      if (!pageSource) throw new Error("Missing priority control for feedback test");
      const pageXHR = {};
      emit("htmx:beforeRequest", {
        elt: pageSource,
        xhr: pageXHR,
        target: document.querySelector("#ticket-detail"),
        requestConfig: { parameters: {} },
      });
      emit("htmx:sendError", { xhr: pageXHR });
    });

    const feedback = page.locator("#save-feedback");
    await expect(feedback).toHaveAttribute("role", "alert");
    await expect(feedback.locator(".save-feedback-message")).toHaveText(
      "Unable to save changes. Please try again.",
    );

    await page.evaluate(() => {
      const emit = (name: string, detail: object) =>
        document.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));
      const drawer = document.createElement("section");
      drawer.className = "category-drawer";
      const source = document.createElement("button");
      source.setAttribute("hx-post", "/unrelated-save");
      drawer.append(source);
      document.body.append(drawer);
      const drawerXHR = {
        getResponseHeader: (name: string) =>
          name === "X-Save-Feedback"
            ? '{"save-feedback":{"message":"Unrelated drawer save.","kind":"success","target":"drawer"}}'
            : null,
      };
      emit("htmx:beforeRequest", {
        elt: source,
        xhr: drawerXHR,
        target: drawer,
        requestConfig: { parameters: {} },
      });
      emit("htmx:afterOnLoad", { xhr: drawerXHR });
    });

    await expect(feedback).toHaveAttribute("role", "alert");
    await expect(feedback.locator(".save-feedback-message")).toHaveText(
      "Unable to save changes. Please try again.",
    );
    await page.waitForTimeout(5_100);
    await expect(feedback).toBeVisible();
    await expect(feedback).toHaveAttribute("role", "alert");
  });

  test("a later aborted assignment keeps its error and selection when an older save returns", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Feedback race " + Date.now().toString(36).slice(2, 8),
      description: "hold the first response, then abort assignment",
      category: "General",
      priority: "high",
    });
    const detailPath = `/tickets/${id}`;
    const editPath = `${detailPath}/edit`;
    const assignPath = `${detailPath}/assign`;
    await page.goto(base() + detailPath);

    let releasePriorityResponse: () => void = () => undefined;
    let priorityRouteFulfilled!: () => void;
    const priorityRouteFulfilledPromise = new Promise<void>((resolve) => {
      priorityRouteFulfilled = resolve;
    });
    let markPriorityResponseHeld!: () => void;
    const priorityResponseHeld = new Promise<void>((resolve) => {
      markPriorityResponseHeld = resolve;
    });
    const holdPriorityResponse = async (route: Route) => {
      const request = route.request();
      if (!isHtmxPost(request, { path: editPath, target: "#ticket-detail" })) {
        await route.continue();
        return;
      }
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      // The held edit response is the re-rendered #ticket-detail fragment (the
      // in-place confirmation) and, since the picker replaced the explicit
      // Apply, carries the shared save toast (issue #386, S5).
      expect(response.headers()["x-save-feedback"]).toContain('"message":"Saved"');
      expect(await response.text()).toContain('aria-selected="true">');
      markPriorityResponseHeld();
      await new Promise<void>((resolve) => {
        releasePriorityResponse = resolve;
      });
      await route.fulfill({ response });
      priorityRouteFulfilled();
    };
    await page.route((url) => url.pathname === editPath, holdPriorityResponse);
    try {
      const prioritySwapRejected = page.evaluate(
        () =>
          new Promise<boolean>((resolve) => {
            let priorityXHR: XMLHttpRequest | undefined;
            document.addEventListener("htmx:beforeRequest", (event) => {
              const detail = (event as CustomEvent).detail;
              if (
                detail?.requestConfig?.parameters?.priority === "critical" &&
                detail?.target?.id === "ticket-detail"
              ) {
                priorityXHR = detail.xhr;
              }
            });
            let swapRejected = false;
            document.addEventListener("htmx:beforeSwap", (event) => {
              const detail = (event as CustomEvent).detail;
              if (detail?.xhr === priorityXHR) swapRejected = event.defaultPrevented;
            });
            document.addEventListener("htmx:afterOnLoad", (event) => {
              const detail = (event as CustomEvent).detail;
              if (detail?.xhr === priorityXHR) {
                window.setTimeout(() => resolve(swapRejected), 0);
              }
            });
          }),
      );
      await expect(pickerButton(page, "ticket-priority")).toBeVisible();
      const priorityRequestPromise = page.waitForRequest(
        (request) =>
          isHtmxPost(request, { path: editPath, target: "#ticket-detail" }) &&
          new URLSearchParams(request.postData() ?? "").get("priority") === "critical",
      );
      await applyPriority(page, "critical");
      const priorityRequest = await priorityRequestPromise;
      expect(priorityRequest.headers()["hx-request"]).toBe("true");
      expect(priorityRequest.headers()["hx-target"]).toBe("ticket-detail");
      expect(new URLSearchParams(priorityRequest.postData() ?? "").get("priority")).toBe(
        "critical",
      );
      await priorityResponseHeld;

      const attemptedAssigneeName = "Alice Admin";
      const attemptedAssignee = "1";
      const assigneeBefore = await currentAssignee(page);
      let assignmentAborted = false;
      const abortAssignment = async (route: Route) => {
        const request = route.request();
        if (isHtmxPost(request, { path: assignPath, target: "#ticket-detail" })) {
          await route.abort("failed");
          assignmentAborted = true;
          return;
        }
        await route.continue();
      };
      await page.route((url) => url.pathname === assignPath, abortAssignment);
      try {
        const assignmentRequestPromise = page.waitForRequest(
          (request) =>
            isHtmxPost(request, { path: assignPath, target: "#ticket-detail" }) &&
            new URLSearchParams(request.postData() ?? "").get("user_id") === attemptedAssignee,
        );
        await applyAssignee(page, attemptedAssigneeName);
        const assignmentRequest = await assignmentRequestPromise;
        expect(assignmentRequest.headers()["hx-request"]).toBe("true");
        expect(assignmentRequest.headers()["hx-target"]).toBe("ticket-detail");
        expect(new URLSearchParams(assignmentRequest.postData() ?? "").get("user_id")).toBe(
          attemptedAssignee,
        );
        await expect.poll(() => assignmentAborted).toBe(true);
        const feedback = page.locator("#save-feedback");
        await expect(feedback.locator(".save-feedback-message")).toHaveText(
          "Unable to save changes. Please try again.",
        );
        await expect(feedback).toHaveAttribute("role", "alert");
        await expect(pickerButton(page, "assign-user")).toContainText(assigneeBefore);

        releasePriorityResponse();
        const [, swapRejected] = await Promise.all([
          priorityRouteFulfilledPromise,
          prioritySwapRejected,
        ]);
        expect(swapRejected).toBe(true);
        await expect(feedback.locator(".save-feedback-message")).toHaveText(
          "Unable to save changes. Please try again.",
        );
        await expect(feedback).toHaveAttribute("role", "alert");
        // The picker keeps the STORED value: a refused pick must never leave
        // the row claiming a value the ticket does not hold.
        await expect(pickerButton(page, "assign-user")).toContainText(assigneeBefore);
      } finally {
        await page.unroute((url) => url.pathname === assignPath, abortAssignment);
      }
    } finally {
      releasePriorityResponse();
      await page.unroute((url) => url.pathname === editPath, holdPriorityResponse);
    }

    await page.reload();
    await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");
  });

  test("the assignee popover narrows candidates and never submits the ticket form", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36).slice(2, 8);
    const alphaName = `Filter Alpha ${suffix}`;
    const bravoName = `Filter Bravo ${suffix}`;
    await createAssignableAgent(page, alphaName);
    await createAssignableAgent(page, bravoName);

    const id = await createTicketViaUi(page, {
      title: `Assignee filter ${suffix}`,
      description: "the assignee filter narrows candidates",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const assigneePosts: string[] = [];
    page.on("request", (request) => {
      if (
        request.method() === "POST" &&
        new URL(request.url()).pathname === `/tickets/${id}/assign`
      ) {
        assigneePosts.push(request.url());
      }
    });

    await openPicker(page, "assign-user");
    const filter = picker(page, "assign-user").locator(".listbox-search");
    await expect(filter).toBeVisible();
    const alphaOption = picker(page, "assign-user").locator(".listbox-option", {
      hasText: alphaName,
    });
    const bravoOption = picker(page, "assign-user").locator(".listbox-option", {
      hasText: bravoName,
    });
    await expect(alphaOption).toHaveCount(1);
    await expect(bravoOption).toHaveCount(1);
    const total = await picker(page, "assign-user").locator(".listbox-option").count();
    expect(total).toBeGreaterThanOrEqual(4); // Unassigned + Alice + the two agents

    // Typing narrows the visible options; a non-matching candidate disappears.
    await filter.fill("bravo");
    await expect(alphaOption).toBeHidden();
    await expect(bravoOption).toBeVisible();

    // The search must never become a mutation path: Enter inside it cannot
    // submit the form, because it moves the highlight to the first visible
    // option instead (a closed form cannot pick anything by accident, #232).
    await filter.press("Enter");
    await page.waitForTimeout(300);
    expect(assigneePosts, "Enter in the search must not POST /assign").toEqual([]);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(bravoOption).toBeFocused();

    // Clearing the search restores the exact candidate set.
    await filter.fill("");
    await expect(alphaOption).toBeVisible();
    await expect(picker(page, "assign-user").locator(".listbox-option")).toHaveCount(total);

    // Picking the narrowed candidate is the explicit act and posts one field.
    await filter.fill("bravo");
    await assertHtmxSwap(
      page,
      async () => {
        await bravoOption.click();
      },
      {
        endpoint: `/tickets/${id}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(pickerButton(page, "assign-user")).toContainText(bravoName);
  });

  test("the assignee popover never hides the inactive current assignee", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36).slice(2, 8);
    const agentName = `Inactive Filter ${suffix}`;
    const { editHref } = await createAssignableAgent(page, agentName);

    const id = await createTicketViaUi(page, {
      title: `Inactive assignee filter ${suffix}`,
      description: "the filter keeps the inactive current assignee",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    await pickAssignee(page, agentName);
    await expect(pickerButton(page, "assign-user")).toContainText(agentName);

    // Deactivate the assigned operator: the detail page now appends the
    // inactive current assignee so the person the ticket still holds stays
    // visible and labelled.
    await page.goto(base() + editHref);
    await page.locator("#user-drawer-form [data-deactivate]").click();
    const deactivateDialog = page.locator("#users-deactivate-dialog");
    await expect(deactivateDialog).toBeVisible();
    const deactivatePath = new URL(editHref, page.url()).pathname;
    const deactivateResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        new URL(response.url()).pathname === deactivatePath,
    );
    await deactivateDialog.locator("[data-confirm-deactivate]").click();
    expect((await deactivateResponse).status()).toBe(200);

    await page.goto(base() + `/tickets/${id}`);
    await expect(pickerButton(page, "assign-user")).toContainText(agentName);
    await openPicker(page, "assign-user");
    const inactiveOption = picker(page, "assign-user").locator(".listbox-option", {
      hasText: `${agentName} (inactive)`,
    });
    await expect(inactiveOption).toHaveCount(1);
    await expect(inactiveOption).toHaveAttribute("aria-selected", "true");

    // A non-matching query must not hide it: the current assignee always stays.
    await picker(page, "assign-user").locator(".listbox-search").fill("no-such-assignee");
    await expect(inactiveOption).toBeHidden();
    const heldByQuery = await picker(page, "assign-user")
      .locator(".listbox-option[aria-selected='true']")
      .count();
    expect(heldByQuery, "the current assignee must stay in the list").toBe(1);
  });

  test("the reply box names only the staff who can open the ticket", async ({ page }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const unique = Date.now().toString(36).slice(2, 8);

    const requester = await createAccount(page, `Rita Requester ${unique}`);
    const assignee = await createAccount(page, `Ana Assignee ${unique}`, "agent");
    const outside = await createAccount(page, `Outside Agent ${unique}`, "agent");
    const colleague = await createAccount(page, `Beatrix Backoffice ${unique}`, "admin");

    await logout(page);
    await loginAs(page, requester.email, requester.password);
    const title = `Mention probe ${unique}`;
    const ticketID = await createTicketViaUi(page, {
      title,
      description: "mention probe",
      category: "General",
      priority: "high",
    });
    await logout(page);
    await loginAsSeeded(page);
    await page.goto(base() + `/tickets/${ticketID}`);
    await pickAssignee(page, assignee.name);
    await logout(page);

    await loginAs(page, assignee.email, assignee.password);
    await page.goto(base() + `/tickets/${ticketID}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    // The Mention button opens the one candidate list, with a search field.
    const toggle = page.locator("#ticket-detail [data-mentions-open]");
    await expect(toggle).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    const list = page.locator("#ticket-detail [data-mentions]");
    const search = list.locator("[data-mentions-search]");
    await expect(search).toBeFocused();
    // The colleague can open the ticket; the requester and an agent the ticket
    // is out of reach for cannot, so neither of them is in the document at all.
    await expect(list.locator(`[data-mention-name="${colleague.name}"]`)).toHaveCount(1);
    await expect(list.locator(`[data-mention-name="${requester.name}"]`)).toHaveCount(0);
    await expect(list.locator(`[data-mention-name="${outside.name}"]`)).toHaveCount(0);

    const narrowed = list.locator(".comment-mentions-option:not([hidden])");
    await search.fill(unique);
    await expect(narrowed).toHaveCount(1);
    await expect(narrowed).toHaveAttribute("data-mention-name", colleague.name);
    await page.keyboard.press("Escape");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    const comment = page.locator("#comment-body");
    await expect(comment).toBeFocused();

    // Typing "@" and the name opens the same list, and Enter takes the person
    // the list highlights, writing the name the server's grammar matches.
    await page.keyboard.type("Thanks ");
    await page.keyboard.type("@" + colleague.name);
    await expect(narrowed).toHaveCount(1);
    await page.keyboard.press("Enter");
    const mentioned = `Thanks @${colleague.name} `;
    await expect(comment).toHaveValue(mentioned);
    const popover = list.locator(".comment-mentions-popover");
    await expect(popover).toBeHidden();

    // Writing on after the chosen name is prose, not a new search: the list
    // stays closed instead of reopening to say nobody matches.
    await page.keyboard.type("please review");
    const continued = `${mentioned}please review`;
    await expect(comment).toHaveValue(continued);
    await expect(popover).toBeHidden();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");

    // A fresh "@" still opens the list, and erasing it falls back to the
    // settled text without reopening the list.
    await page.keyboard.type(" @Bea");
    await expect(popover).toBeVisible();
    await expect(narrowed).toHaveCount(1);
    for (let i = 0; i < " @Bea".length; i++) await page.keyboard.press("Backspace");
    await expect(comment).toHaveValue(continued);
    await expect(popover).toBeHidden();

    const navigation = page.waitForEvent("framenavigated", (frame) => frame === page.mainFrame());
    const posted = waitForExactPost(page, `/tickets/${ticketID}/comments`);
    await page.getByRole("button", { name: "Reply", exact: true }).click();
    await navigation;
    expect((await posted).status()).toBe(303);
    await expect(page.locator("#timeline")).toContainText(mentioned);
    await logout(page);

    // The person named in the comment is told, and the ticket's followers row
    // holds them: a mention is a subscription, never another way in.
    await loginAs(page, colleague.email, colleague.password);
    await page.goto(base() + "/notifications");
    const told = page.locator(".notifications-row").filter({ hasText: title });
    await expect(told).toHaveCount(1);
    await expect(told.locator(".notification-what")).toHaveText("Mentioned you in a comment");
    await page.goto(base() + `/tickets/${ticketID}`);
    const followers = page.locator("#ticket-detail .prop-followers");
    await expect(followers).toHaveCount(1);
    await expect(followers.locator(".prop-followers-count")).toHaveText("1");
    await expect(followers.locator(".prop-followers-person .prop-followers-name")).toHaveText(
      colleague.name,
    );
    await expect(followers.locator(".prop-followers-person .prop-followers-you")).toHaveText("You");
  });

  test("a follower keeps a draft and hears about the ticket until they unfollow", async ({
    page,
  }) => {
    test.setTimeout(240_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const unique = Date.now().toString(36).slice(2, 8);

    const follower = await createAccount(page, `Fay Follower ${unique}`, "admin");
    const title = `Follow probe ${unique}`;
    const ticketID = await createTicketViaUi(page, {
      title,
      description: "follow probe",
      category: "General",
      priority: "high",
    });
    await logout(page);

    await loginAs(page, follower.email, follower.password);
    await page.goto(base() + `/tickets/${ticketID}`);
    const button = page.locator("#ticket-detail .detail-follow button");
    await expect(button).toHaveText("Follow");
    await expect(button).toHaveAttribute("aria-pressed", "false");
    const draft = `Half a reply ${unique}`;
    await page.locator("#comment-body").fill(draft);

    await assertHtmxSwap(
      page,
      async () => {
        await button.click();
      },
      {
        endpoint: `/tickets/${ticketID}/follow`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    const following = page.locator("#ticket-detail .detail-follow button");
    await expect(following).toHaveText("Following");
    await expect(following).toHaveAttribute("aria-pressed", "true");
    // The whole detail fragment was replaced, and the composer's draft and the
    // followers row both came through it.
    await expect(page.locator("#comment-body")).toHaveValue(draft);
    await expect(page.locator("#ticket-detail .prop-followers")).toContainText("You");
    await page.reload();
    await expect(page.locator("#ticket-detail .detail-follow button")).toHaveText("Following");
    await expect(page.locator("#ticket-detail .detail-follow button")).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await logout(page);

    // Another operator changes the priority, and the follower is told.
    await loginAsSeeded(page);
    await page.goto(base() + `/tickets/${ticketID}`);
    await pickPriority(page, "critical");
    await logout(page);

    await loginAs(page, follower.email, follower.password);
    await page.goto(base() + "/notifications");
    const told = page.locator(".notifications-row").filter({ hasText: title });
    await expect(told).toHaveCount(1);
    await expect(told.locator(".notification-what")).toHaveText(
      "Priority changed from High to Critical",
    );

    // Unfollowing ends the subscription: the same change reaches nobody.
    await page.goto(base() + `/tickets/${ticketID}`);
    await assertHtmxSwap(
      page,
      async () => {
        await page.locator("#ticket-detail .detail-follow button").click();
      },
      {
        endpoint: `/tickets/${ticketID}/unfollow`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.locator("#ticket-detail .detail-follow button")).toHaveText("Follow");
    await expect(page.locator("#ticket-detail .detail-follow button")).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await logout(page);

    await loginAsSeeded(page);
    await page.goto(base() + `/tickets/${ticketID}`);
    await pickPriority(page, "low");
    await logout(page);

    await loginAs(page, follower.email, follower.password);
    await page.goto(base() + "/notifications");
    const stillTold = page.locator(".notifications-row").filter({ hasText: title });
    await expect(stillTold).toHaveCount(1);
    await expect(stillTold.locator(".notification-what")).toHaveText(
      "Priority changed from High to Critical",
    );
  });

  test("a requester's ticket offers no saved replies, mentions or following", async ({ page }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const unique = Date.now().toString(36).slice(2, 8);

    const requester = await createAccount(page, `Rui Requester ${unique}`);
    await logout(page);
    await loginAs(page, requester.email, requester.password);
    const ticketID = await createTicketViaUi(page, {
      title: `Requester control probe ${unique}`,
      description: "requester control probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${ticketID}`);

    // The requester still writes comments, with none of the staff tools beside
    // the box and no way into a ticket's following.
    await expect(page.locator("#comment-body")).toBeVisible();
    await expect(page.getByRole("button", { name: "Reply", exact: true })).toBeVisible();
    await expect(page.locator("#ticket-detail [data-saved-replies]")).toHaveCount(0);
    await expect(page.locator("#ticket-detail [data-mentions]")).toHaveCount(0);
    await expect(page.locator("#ticket-detail .detail-follow")).toHaveCount(0);
    await expect(page.locator("#ticket-detail .prop-followers")).toHaveCount(0);
    await expect(page.locator('.sidebar-link[href="/replies"]')).toHaveCount(0);
    await logout(page);

    // The same ticket shows all of them to staff, so the absences above are the
    // role and not a missing render.
    await loginAsSeeded(page);
    await page.goto(base() + `/tickets/${ticketID}`);
    await expect(page.locator("#ticket-detail [data-saved-replies]")).toHaveCount(1);
    await expect(page.locator("#ticket-detail [data-mentions]")).toHaveCount(1);
    await expect(page.locator("#ticket-detail .detail-follow")).toHaveCount(1);
    await expect(page.locator("#ticket-detail .prop-followers")).toHaveCount(1);
    await expect(page.locator('.sidebar-link[href="/replies"]')).toHaveCount(1);
  });
});

test.describe("Ticket detail SLA panel (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });
  test.afterEach(async ({ page }) => {
    await page.context().clearCookies();
    await loginAsSeeded(page);
    await setSLAEnabled(page, false);
  });

  test("shows the overall state and one row per milestone with the time left", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    const id = await createTicketViaUi(page, {
      title: "SLA detail " + Date.now().toString(36).slice(2, 8),
      description: "sla detail probe",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    const slaSection = page
      .locator("#ticket-detail .prop-section")
      .filter({ has: page.locator(".prop-heading", { hasText: /^SLA/ }) });
    await expect(slaSection).toHaveCount(1);

    // The heading names the section and stays silent while the overall state is
    // on_track, then ONE row per milestone: its label and the time left. The
    // target, due and achieved rows are gone by decision; the frozen due
    // instant survives as the <time datetime> the countdown reads.
    const headings = slaSection.locator(".prop-heading");
    await expect(headings).toHaveCount(1);
    await expect(headings.nth(0)).toContainText("SLA");
    await expect(headings.nth(0).locator(".badge, .sla-dot")).toHaveCount(0);

    const milestoneRows = slaSection.locator(".prop-row");
    await expect(milestoneRows).toHaveCount(2);
    await expect(milestoneRows.nth(0).locator(".prop-label")).toHaveText("First response");
    await expect(milestoneRows.nth(0).locator(".badge, .sla-dot")).toHaveCount(1);
    await expect(milestoneRows.nth(1).locator(".prop-label")).toHaveText("Resolve");
    await expect(milestoneRows.nth(1).locator(".badge, .sla-dot")).toHaveCount(1);
    // on_track is the quiet default: no green pill anywhere in the section.
    await expect(slaSection.locator(".badge, .sla-dot")).toHaveCount(2);
    // Every state speaks, on_track included: an empty cell would mean the
    // ticket has no commitment at all, and the two must not look the same.
    await expect(slaSection).toContainText("On Track");

    // Both milestones are pending, so both carry the live countdown and each
    // keeps its absolute due instant in the <time datetime>.
    const dueTimes = slaSection.locator("[data-sla-countdown]");
    await expect(dueTimes).toHaveCount(2);
    for (let index = 0; index < 2; index += 1) {
      await expect(dueTimes.nth(index)).toHaveAttribute(
        "datetime",
        /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z/,
      );
    }

    // The panel states the state and the time left, and nothing else.
    await expect(slaSection.getByText("Target", { exact: true })).toHaveCount(0);
    await expect(slaSection.getByText("Achieved", { exact: true })).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail SLA panel",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});

/**
 * Ticket detail SLA live countdown (issue #211, PR 6).
 *
 * The countdown is browser-owned: the server renders the ABSOLUTE due instant
 * in the <time datetime> and anchors the browser clock with data-server-now,
 * and the deferred /static/sla_countdown.js recomputes the remaining time from
 * that instant on every tick. Playwright's clock API installs a deterministic
 * client clock BEFORE navigation, so this journey can freeze and then
 * fast-forward the browser's "now" and prove the ticker moves while the
 * absolute instant in the markup survives.
 *
 * SLA is instance-wide: every journey enables it, creates its own committed
 * fixture, and restores it (disable) in afterEach, exactly like the panel
 * describe above.
 */
test.describe("Ticket detail SLA live countdown (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });
  test.afterEach(async ({ page }) => {
    await page.context().clearCookies();
    await loginAsSeeded(page);
    await setSLAEnabled(page, false);
  });

  test("the ticker moves with the frozen clock while the absolute due instant survives", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    const id = await createTicketViaUi(page, {
      title: "SLA countdown " + Date.now().toString(36).slice(2, 8),
      description: "sla countdown probe",
      category: "General",
      priority: "high",
    });

    // Install a deterministic client clock BEFORE navigating: the detail
    // page's deferred countdown script reads Date.now() during load and derives
    // its server/client offset from data-server-now. The high-priority target
    // (1 working hour) leaves the response due far ahead of the frozen instant.
    await page.clock.install({ time: new Date() });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    const slaSection = slaPanel(page);
    await expect(slaSection).toHaveCount(1);
    const countdowns = slaSection.locator("[data-sla-countdown]");
    await expect(countdowns).toHaveCount(2);

    const response = countdowns.first();
    const resolve = countdowns.nth(1);
    const ticker = response.locator(".sla-countdown-ticker");
    const coarse = response.locator(".sla-countdown-coarse");

    // The script has run: the fast per-second value is aria-hidden, and the
    // accessible coarse span carries non-empty text for a screen reader.
    await expect(ticker).toHaveAttribute("aria-hidden", "true");
    await expect(ticker).not.toBeEmpty();
    await expect(coarse).not.toBeEmpty();

    // Freeze the browser clock at the current page instant. From here only the
    // fast-forward below can move the reading, so a changed text proves ticking
    // rather than a naturally elapsed wall clock.
    const heldAt = await page.evaluate(() => Date.now());
    await page.clock.pauseAt(heldAt + 5_000);

    const before = await ticker.textContent();
    const absoluteBefore = await response.getAttribute("datetime");
    expect(absoluteBefore).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);

    await page.clock.fastForward(5_000);
    await expect(ticker).not.toHaveText(before ?? "");

    // The absolute truth must survive: the tick recomputed the text but never
    // rewrote the <time datetime> the server rendered.
    expect(await response.getAttribute("datetime")).toBe(absoluteBefore);

    // Fast-forward PAST the response due instant: the reading becomes the
    // stable "overdue" text, never negative time. The pending Resolve
    // milestone, seven working hours further out, is still counting down.
    const dueAt = Date.parse(absoluteBefore ?? "");
    const serverNow = Date.parse((await slaSection.getAttribute("data-server-now")) ?? "");
    await page.clock.fastForward(dueAt - serverNow + 60_000);
    await expect(ticker).toContainText("overdue");
    expect(await ticker.textContent()).not.toContain("-");
    await expect(resolve.locator(".sla-countdown-ticker")).not.toContainText("overdue");
    await expect(response).toHaveAttribute("datetime", absoluteBefore ?? "");

    // Hand the context back a running clock before afterEach restores the
    // instance-wide SLA switch.
    await page.clock.resume();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail SLA live countdown",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("an achieved milestone drops the countdown and the list never loads the script", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    const id = await createTicketViaUi(page, {
      title: "SLA achieved " + Date.now().toString(36).slice(2, 8),
      description: "sla achieved probe",
      category: "General",
      priority: "high",
    });

    // A public staff comment is the FIRST-RESPONSE observation, so the Response
    // milestone becomes achieved on the detail page.
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.getByLabel(/comment body/i).fill("first staff response");
    const commentPost = waitForExactPost(page, `/tickets/${id}/comments`);
    await page.getByRole("button", { name: "Reply" }).click();
    expect((await commentPost).status()).toBe(303);

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const slaSection = slaPanel(page);
    await expect(slaSection).toHaveCount(1);

    // Response is achieved: its row carries the label and the state badge and
    // NO time at all — nothing ticks there. Only the pending Resolve milestone
    // carries the countdown.
    const rows = slaSection.locator(".prop-row");
    await expect(rows.nth(0).locator(".prop-label")).toHaveText("First response");
    await expect(rows.nth(0).locator(".sla-dot")).toHaveClass(/met/);
    await expect(rows.nth(0)).toContainText("Met");
    await expect(rows.nth(0).locator("[data-sla-countdown]")).toHaveCount(0);
    await expect(rows.nth(0).locator("time")).toHaveCount(0);
    await expect(rows.nth(1).locator(".prop-label")).toHaveText("Resolve");
    await expect(rows.nth(1).locator("[data-sla-countdown]")).toHaveCount(1);
    await expect(slaSection.locator("[data-sla-countdown]")).toHaveCount(1);

    // The list page never loads /static/sla_countdown.js: no script tag and no
    // request for it. The listener is attached after the detail render, so it
    // only observes the list navigation.
    const countdownRequests: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === "/static/sla_countdown.js") {
        countdownRequests.push(request.url());
      }
    });
    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    await expect(page.locator('script[src="/static/sla_countdown.js"]')).toHaveCount(0);
    expect(countdownRequests).toEqual([]);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets list without the countdown script",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
