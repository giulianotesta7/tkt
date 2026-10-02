/**
 * Ticket confirmation journeys (issue #55): requester awaits resolution
 * confirmation. Agent resolves; requester confirms (closes) or rejects
 * (returns to manual in_progress with workflow detached).
 */

import { expect, test } from "@playwright/test";
import { base, createUserAsAdmin, loginAs, loginAsSeeded, logout } from "./helpers/auth.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
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
          await page.locator("#assign-user").selectOption({ label: "Alice Admin" });
          await page
            .locator("form:has(#assign-user)")
            .getByRole("button", { name: "Apply" })
            .click();
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
      const stateSelect = page.locator("#ticket-state");
      await expect(stateSelect).toBeVisible();
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await stateSelect.selectOption(target);
          await page.locator("#state-apply").click();
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
    await expect(page.locator("#ticket-state")).toHaveCount(0);
    await expect(page.getByText("Move to", { exact: true })).toHaveCount(0);

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
      await expect(completedTask.locator(".timeline-manual-heading .main")).toHaveText(
        "Alice Admin completed the task",
      );
      await expect(completedTask.getByText("Solution", { exact: true })).toHaveCount(1);
      await expect(completedTask.locator("dd").first()).toHaveText(solution);
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
    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();
    await expect(moveSelect.locator('option[value="closed"]')).toHaveCount(0);
    await expect(moveSelect.locator('option[value="in_progress"]')).toHaveCount(1);
  });
});
