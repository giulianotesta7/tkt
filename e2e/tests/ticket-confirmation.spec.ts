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

  /** Create a requester-owned ticket driven to `resolved`, then return it to the requester. */
  async function requesterOwnedResolvedTicket(
    page: import("@playwright/test").Page,
    requesterEmail: string,
    requesterPassword: string,
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
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      const stateSelect = page.locator("#ticket-state");
      await expect(stateSelect).toBeVisible();
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await stateSelect.selectOption(target);
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
    // Move-to hides `closed` for requester-owned resolved tickets.
    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();
    await expect(moveSelect.locator('option[value="closed"]')).toHaveCount(0);

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

    const id = await requesterOwnedResolvedTicket(page, requesterEmail, requesterPassword);

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

    await page.reload();
    await expect(page.getByText("In Progress").first()).toBeVisible();
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
