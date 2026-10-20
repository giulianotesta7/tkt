/**
 * Issue #344 — the notification center's canonical journey.
 *
 * As root: create an agent, create two tickets and assign both to that agent.
 * As the agent: the rail bell carries the unread count and its accessible name,
 * opening the panel is an HTMX swap into the drawer, opening it refreshes the
 * count through the bell's own endpoint, the 45s poll asks that endpoint again,
 * clicking one notification opens the ticket and marks it read, and "Mark all as
 * read" clears the badge. The 45s poll is proven with Playwright's clock API,
 * the same deterministic clock the SLA countdown journey uses.
 *
 * Every count the bell shows is read from the fragment the browser actually
 * received, so "the badge went down" is proven by the response body, never by
 * the markup the server happened to render first.
 *
 * Exhaustive recipient policy, atomicity, cross-user write scoping and the
 * internal-comment visibility rule stay in Go:
 * internal/adapters/http/handlers_notifications_test.go.
 */

import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { assertNoHorizontalOverflow, assertNothingClipped } from "./helpers/layout.js";
import { base, createUserAsAdmin, loginAs, logout, seededCredentials } from "./helpers/auth.js";
import { createTicketViaUi, resolveUserEditHref } from "./helpers/navigation.js";

const uniq = Date.now().toString(36).slice(2, 8);
const agent = {
  name: "Notify Agent",
  email: `notify-agent-${uniq}@example.com`,
  password: "Secret123!",
};
const firstTicket = `Notify first ${uniq}`;
const secondTicket = `Notify second ${uniq}`;

/** Every count fragment the browser received, in order. */
function watchBellCounts(page: Page): string[] {
  const bodies: string[] = [];
  page.on("response", (response) => {
    const url = new URL(response.url());
    if (url.pathname !== "/notifications/count") return;
    response
      .text()
      .then((body) => bodies.push(body))
      .catch(() => undefined);
  });
  return bodies;
}

async function promoteToAgent(page: Page): Promise<void> {
  await page.goto(base() + "/users");
  const editHref = await resolveUserEditHref(page, agent.name);
  await page.goto(base() + editHref);
  await page.locator('select[name="role"]').selectOption("agent");
  await page.getByRole("button", { name: /save changes/i }).click();
  await expect(page).toHaveURL(/\/users$/);
  await expect(page.locator(`tr[data-user-name="${agent.name}"]`)).toContainText("Agent");
}

async function assignToAgent(page: Page, ticketId: string): Promise<void> {
  await page.goto(base() + `/tickets/${ticketId}`);
  const assignee = page.locator('select[name="user_id"]');
  await expect(assignee).toHaveCount(1);
  await assertHtmxSwap(
    page,
    async () => {
      await assignee.selectOption({ label: agent.name });
      await page.locator("form:has(#assign-user)").getByRole("button", { name: "Apply" }).click();
    },
    {
      endpoint: `/tickets/${ticketId}/assign`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#ticket-detail",
    },
  );
}

test.describe("Notification center — assigned, opened, marked read (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("the assignee reads one notification and then marks everything read", async ({ page }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAs(page, seededCredentials.email, seededCredentials.password);
    await createUserAsAdmin(page, agent);
    await promoteToAgent(page);
    const firstId = await createTicketViaUi(page, {
      title: firstTicket,
      category: "General",
      priority: "high",
    });
    await assignToAgent(page, firstId);
    const secondId = await createTicketViaUi(page, {
      title: secondTicket,
      category: "General",
      priority: "low",
    });
    await assignToAgent(page, secondId);
    await logout(page);

    // The clock is installed before the agent's first navigation so the rail's
    // 45s poll can be fast-forwarded deterministically below.
    const counts = watchBellCounts(page);
    await page.clock.install({ time: new Date() });
    await loginAs(page, agent.email, agent.password);

    // S13: the bell carries the unread count in its accessible name, and the
    // number comes from the bell's own endpoint, not from the page render.
    await expect.poll(() => counts.length, { timeout: 10_000 }).toBeGreaterThan(0);
    expect(counts.at(-1)).toContain(`aria-label="Notifications, 2 unread"`);
    const counted = page.getByRole("link", { name: "Notifications, 2 unread" });
    await expect(counted).toBeVisible({ timeout: 10_000 });
    await expect(page.locator("#notification-bell .rail-badge")).toHaveText("2");

    // S13's poll: 45s of visible page time asks the count endpoint again.
    const beforePoll = counts.length;
    await page.clock.fastForward(45_000);
    await expect.poll(() => counts.length, { timeout: 10_000 }).toBeGreaterThan(beforePoll);

    // S14: the bell opens the panel as a drawer — an HTMX swap, zero navigation,
    // unchanged URL — and opening it refreshes the count through its endpoint.
    const beforeOpen = counts.length;
    await assertHtmxSwap(
      page,
      async () => {
        await counted.click();
      },
      {
        endpoint: "/notifications",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#notification-panel-host",
      },
    );
    await expect.poll(() => counts.length, { timeout: 10_000 }).toBeGreaterThan(beforeOpen);

    const panel = page.getByRole("dialog", { name: "Notifications" });
    await expect(panel).toBeVisible();
    // The drawer gets the narrow viewport's own pass: nothing overflows and
    // nothing inside it is clipped.
    await page.setViewportSize({ width: 390, height: 844 });
    await assertNoHorizontalOverflow(page, 390);
    await assertNothingClipped(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    // The drawer takes focus, and Escape closes it and returns focus to the bell.
    await expect(panel).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "Notifications" })).toHaveCount(0);
    await expect(counted).toBeFocused();
    await counted.click();
    await expect(panel).toBeVisible();
    await expect(panel.getByText(firstTicket)).toBeVisible();
    await expect(panel.getByText(secondTicket)).toBeVisible();
    await expect(panel.getByText("Assigned to you").first()).toBeVisible();
    await expect(panel.locator(".notification-unread")).toHaveCount(2);
    // The panel names who did it: the root operator who assigned the tickets.
    await expect(panel.getByText(seededCredentials.name).first()).toBeVisible();

    // S14: clicking one notification opens its ticket and marks it read. The row
    // is a POST form, so this is an ordinary navigation, never a writing GET.
    await panel.locator(".notification-open").filter({ hasText: firstTicket }).click();
    await expect(page).toHaveURL(new RegExp(`/tickets/${firstId}$`));
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect.poll(() => counts.at(-1) ?? "", { timeout: 10_000 }).toContain("1 unread");
    await expect(page.getByRole("link", { name: "Notifications, 1 unread" })).toBeVisible();

    // S14: "Mark all as read" clears the badge and the rows' marker.
    await page.getByRole("link", { name: "Notifications, 1 unread" }).click();
    const reopened = page.getByRole("dialog", { name: "Notifications" });
    await expect(reopened).toBeVisible();
    await reopened.getByRole("button", { name: "Mark all as read" }).click();
    await expect(page).toHaveURL(/\/notifications$/);
    await expect
      .poll(() => counts.at(-1) ?? "", { timeout: 10_000 })
      .toContain(`aria-label="Notifications"`);
    expect(counts.at(-1)).not.toContain("rail-badge");
    await expect(page.getByRole("link", { name: "Notifications", exact: true })).toBeVisible();
    await expect(page.locator(".notification-unread")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Mark all as read" })).toHaveCount(0);
    // Read history is kept: the rows the agent acted on are still listed.
    await expect(page.locator(".notifications-row")).toHaveCount(2);
  });
});
