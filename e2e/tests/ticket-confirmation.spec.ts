/**
 * Ticket confirmation journeys (issue #55): requester awaits resolution
 * confirmation. Agent resolves; requester confirms (closes) or rejects
 * (returns to manual in_progress with workflow detached).
 */

import { expect, test } from "@playwright/test";
import { base, createUserAsAdmin, loginAs, loginAsSeeded } from "./helpers/auth.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { collectObservability } from "./helpers/layout.js";

// ---------- Helpers ----------

async function requesterOwnedResolvedTicket(
  page: import("@playwright/test").Page,
  requesterEmail: string,
  requesterPassword: string,
): Promise<string> {
  // Login as the requester (role user), create ticket (they are the creator → requester),
  // then log out, log in as admin, drive new → in_progress → resolved, log back as requester.
  await loginAs(page, requesterEmail, requesterPassword);
  const title = "Confirm probe " + Date.now().toString(36).slice(2, 8);
  const id = await createTicketViaUi(page, { title, description: "probe", category: "General", priority: "high" });
  // Drive the ticket to resolved as admin (who can transition).
  const { logout } = await import("./helpers/auth.js");
  const { loginAs: loginAsFn } = await import("./helpers/auth.js");
  await logout(page);
  await loginAsSeeded(page);
  for (const target of ["in_progress", "resolved"] as const) {
    await page.goto(base() + `/tickets/${id}`);
    const stateSelect = page.locator("#ticket-state");
    await expect(stateSelect).toBeVisible();
    const { assertHtmxSwap } = await import("./helpers/htmx.js");
    await assertHtmxSwap(
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
  }
  // Hand back to the requester for the confirmation step.
  await logout(page);
  await loginAs(page, requesterEmail, requesterPassword);
  return id;
}

// ---------- Journeys ----------

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
  // Resolution confirmation panel must be visible for the requester.
  await expect(page.locator(".resolution-confirmation")).toBeVisible();
  await expect(page.getByText("Resolution confirmation")).toBeVisible();
  await expect(page.getByRole("heading", { name: /Is your issue solved/i })).toBeVisible();
  await expect(page.getByText(/Support marked this ticket as resolved/i)).toBeVisible();
  const confirmBtn = page.getByRole("button", { name: /Yes, close ticket/i });
  const rejectBtn = page.getByRole("button", { name: /No, I still need help/i });
  await expect(confirmBtn).toBeVisible();
  await expect(rejectBtn).toBeVisible();
  // The Move-to control hides the `closed` option for requester-owned tickets; `closed` is offered only through the panel.
  const moveSelect = page.locator("#ticket-state");
  await expect(moveSelect).toBeVisible();
  const closedOptions = moveSelect.locator('option[value="closed"]');
  await expect(closedOptions).toHaveCount(0);

  const { assertHtmxSwap } = await import("./helpers/htmx.js");
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

  // State badge is now `closed`; both timestamps remain; panel gone; comment hidden.
  await expect(page.getByText("Closed").first()).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(/^Resolved$/).first()).toBeVisible();
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

  const { assertHtmxSwap } = await import("./helpers/htmx.js");
  await assertHtmxSwap(
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

  await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
  await expect(page.locator(".resolution-confirmation")).toHaveCount(0);
  // No workflow pending-actions card — the ticket is manual after rejection.
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

  // Create the requester-owned resolved ticket (see helper above).
  const id = await requesterOwnedResolvedTicket(page, requesterEmail, requesterPassword);

  // View as the seeded admin/agent (not the requester): no confirmation panel, no comment form, no closed in Move-to.
  await page.goto(base() + `/tickets/${id}`);
  await expect(page.locator(".resolution-confirmation")).toHaveCount(0);
  await expect(page.getByLabel(/comment body/i)).toHaveCount(0);

  const moveSelect = page.locator("#ticket-state");
  await expect(moveSelect).toBeVisible();
  await expect(moveSelect.locator('option[value="closed"]')).toHaveCount(0);
  await expect(moveSelect.locator('option[value="in_progress"]')).toHaveCount(1);
});

// Browser-evidenced coverage tail: update e2e/README.md.
