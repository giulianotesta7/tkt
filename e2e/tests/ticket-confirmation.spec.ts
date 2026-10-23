/**
 * Ticket confirmation journeys (issue #55): requester awaits resolution
 * confirmation. Agent resolves; requester confirms (closes) or rejects
 * (returns to manual in_progress with workflow detached).
 */

import { expect, test } from "@playwright/test";
import { base, createUserAsAdmin, loginAs, loginAsSeeded, logout } from "./helpers/auth.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import {
  assertNoHorizontalOverflow,
  assertNothingClipped,
  collectObservability,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import {
  applyAssignee,
  applyTransition,
  pickerButton,
  transitionAction,
} from "./helpers/ticket.js";
import { startServer, stopServer } from "../server-lifecycle.js";

// ---------- Lifecycle ----------

test.describe("Ticket confirmation", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  // ---------- Helpers ----------

  /**
   * Create a requester-owned ticket driven to `resolved`, then return it to the
   * requester. With a `solution`, the admin also assigns the ticket to
   * themselves and completes the pinned manual step WITH that solution, so the
   * ticket carries a historical completion before it is resolved.
   */
  async function requesterOwnedResolvedTicket(
    page: import("@playwright/test").Page,
    requesterEmail: string,
    requesterPassword: string,
    solution: string | null = null,
  ): Promise<string> {
    // The caller is logged in as admin (to create the user); log out first so
    // the requester session is clean before they create their own ticket.
    await logout(page);
    // Requester (role user) creates the ticket → they become the requester.
    await loginAs(page, requesterEmail, requesterPassword);
    const title = "Confirmation probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "probe",
      category: "General",
      priority: "high",
    });
    // Admin drives new → in_progress → resolved (the requester can't transition).
    await logout(page);
    await loginAsSeeded(page);
    if (solution !== null) {
      // A manual-task step requires the current assignee, so claim the ticket
      // first, then complete the pinned step with the visible solution.
      await page.goto(base() + `/tickets/${id}`);
      await assertHtmxSwap(
        page,
        async () => {
          await applyAssignee(page, "Alice Admin");
        },
        {
          endpoint: `/tickets/${id}/assign`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      const pending = page.locator("#workflow-pending");
      await expect(pending.getByLabel("Solution (optional)")).toBeVisible();
      await assertHtmxSwap(
        page,
        async () => {
          await pending.getByLabel("Solution (optional)").fill(solution);
          await pending.getByRole("button", { name: "Complete" }).click();
        },
        {
          endpoint: `/tickets/${id}/workflow/steps/1/complete`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      await expect(page.locator("#workflow-pending")).toHaveCount(0);
    }
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(transitionAction(page, target)).toBeVisible();
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
    // Hand back to the requester for the confirmation step.
    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);
    return id;
  }

  test("requester confirms and the ticket closes with confirmation attribution", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const requesterEmail = `rose-${Date.now().toString(36).slice(2, 6)}@tkt.test`;
    const requesterPassword = "Secret123!";
    await createUserAsAdmin(page, {
      name: "Rosa",
      email: requesterEmail,
      password: requesterPassword,
    });

    const id = await requesterOwnedResolvedTicket(page, requesterEmail, requesterPassword);
    const obs = collectObservability(page);

    await page.goto(base() + `/tickets/${id}`);
    // Resolution confirmation panel visible for the requester.
    await expect(page.locator(".resolution-confirmation")).toBeVisible();
    await expect(page.getByText("Resolution confirmation")).toBeVisible();
    await expect(page.getByRole("heading", { name: /Is your issue solved/i })).toBeVisible();
    await expect(page.getByText(/Support marked this ticket as resolved/i)).toBeVisible();
    const confirmBtn = page.getByRole("button", { name: /Yes, close ticket/i });
    const rejectBtn = page.getByRole("button", { name: /No, I still need help/i });
    await expect(confirmBtn).toBeVisible();
    await expect(rejectBtn).toBeVisible();
    // A requester holds no ticket-edit capability — policy.go's RoleUser grants
    // only CapCreateTicket and CapCommentPublic — so no Move-to control can ever
    // work for them: it must be absent, not merely missing the `closed` option
    // (#263). This assertion used to pin the select as visible with `closed`
    // filtered out, which was a broken affordance that always returned 403. The
    // resolution panel above is the requester's real path.
    await expect(transitionAction(page, "in_progress")).toHaveCount(0);
    await expect(page.getByText("Reopen", { exact: true })).toHaveCount(0);

    const resp = await assertHtmxSwap(
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
    expect(resp.status()).toBe(200);

    // State `closed`; panel gone; comment hidden (closed rejects everyone).
    await expect(page.getByText("Closed").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.locator(".resolution-confirmation")).toHaveCount(0);
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);
    // Refresh to prove persistence.
    await page.reload();
    await expect(page.getByText("Closed").first()).toBeVisible();

    // The closed ticket leaves the requester's open work and moves into the
    // folded "Closed" disclosure at the end of their list (issue #411, S4):
    // the header no longer counts it, the disclosure names its own count, and
    // it is collapsed until the requester opens it.
    await page.goto(base() + "/tickets");
    const listScreen = page.locator("#tickets-screen");
    await expect(listScreen.locator(".page-subtitle")).toHaveText("0 open tickets");
    await expect(listScreen.locator("#open-tickets-title")).toHaveText("Open · 0");
    await expect(listScreen.locator(".req-empty")).toContainText("No requests yet");
    const closedFold = listScreen.locator("details.req-fold");
    await expect(closedFold).toHaveCount(1);
    await expect(closedFold.locator("summary")).toHaveText("Closed · 1");
    await expect(closedFold).not.toHaveAttribute("open", /.*/);
    await expect(closedFold.locator("li.req")).toBeHidden();
    await closedFold.locator("summary").click();
    const closedRow = closedFold.locator("li.req").filter({ hasText: "Confirmation probe" });
    await expect(closedRow).toHaveCount(1);
    await expect(closedRow.locator(".badge.closed")).toHaveText("Closed");
    // The disclosure and its rows hold at the phone width like the rest of the
    // list: nothing clipped, nothing pushing the page sideways.
    await page.setViewportSize({ width: 390, height: 844 });
    await assertNoHorizontalOverflow(page, 390);
    await assertNothingClipped(page);
    await page.setViewportSize({ width: 1280, height: 800 });
  });

  test("requester rejects and the ticket reopens as a detached manual in_progress", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const requesterEmail = `hugo-${Date.now().toString(36).slice(2, 6)}@tkt.test`;
    const requesterPassword = "Secret123!";
    await createUserAsAdmin(page, {
      name: "Hugo",
      email: requesterEmail,
      password: requesterPassword,
    });

    const solution = "Replaced the failed disk and restarted the worker";
    const id = await requesterOwnedResolvedTicket(
      page,
      requesterEmail,
      requesterPassword,
      solution,
    );

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator(".resolution-confirmation")).toBeVisible();
    const rejectBtn = page.getByRole("button", { name: /No, I still need help/i });
    await expect(rejectBtn).toBeVisible();

    const resp = await assertHtmxSwap(
      page,
      async () => {
        await rejectBtn.click();
      },
      {
        endpoint: `/tickets/${id}/confirmation`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    expect(resp.status()).toBe(200);

    // State `in_progress`; panel gone; no workflow pending card (manual).
    await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.locator(".resolution-confirmation")).toHaveCount(0);
    await expect(page.locator("#workflow-pending")).toHaveCount(0);

    // Issue #264: the detached reopen must keep the historical completion's
    // solution and its responsible person, and the retained assignee, for the
    // requester — before and after a reload.
    const assertReopenedHistory = async () => {
      const completedTask = page.locator("#timeline .timeline-entry.timeline-manual", {
        hasText: solution,
      });
      await expect(completedTask).toHaveCount(1);
      // One grammar (issue #386, S3): the head line carries the actor and the
      // verb, with no nested body block.
      await expect(completedTask.locator(".timeline-entry-head .narrative")).toHaveText(
        "Alice Admin completed the task",
      );
      await expect(completedTask.getByText("Solution", { exact: true })).toHaveCount(1);
      await expect(completedTask.locator("dd").first()).toHaveText(solution);
      // The requester reads the assignee, never a picker (issue #263, S6).
      await expect(page.locator("#assign-user-value")).toHaveText("Alice Admin");
      await expect(page.locator("#workflow-pending")).toHaveCount(0);
    };
    await assertReopenedHistory();

    await page.reload();
    await expect(page.getByText("In Progress").first()).toBeVisible();
    await assertReopenedHistory();
  });

  test("requester-owned resolved ticket viewed by an agent hides the panel and blocks manual close", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const requesterEmail = `leo-${Date.now().toString(36).slice(2, 6)}@tkt.test`;
    const requesterPassword = "Secret123!";
    await createUserAsAdmin(page, {
      name: "Leo",
      email: requesterEmail,
      password: requesterPassword,
    });

    const id = await requesterOwnedResolvedTicket(page, requesterEmail, requesterPassword);

    // View as the seeded admin (not the requester): no panel, no comment form, no `closed` in Move-to.
    await logout(page);
    await loginAsSeeded(page);
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator(".resolution-confirmation")).toHaveCount(0);
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);
    // The closure gate keeps Close off the header; the reopen is the only action.
    await expect(
      page.locator(
        '#ticket-detail .detail-actions form.detail-action:has(input[name="to"][value="closed"])',
      ),
    ).toHaveCount(0);
    await expect(transitionAction(page, "in_progress")).toBeVisible();
  });
});
