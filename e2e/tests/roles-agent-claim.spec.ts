/**
 * Issue #122 — visible claim control, canonical winner/stale journey: two
 * authenticated agent contexts render the same claimable row; the winner
 * claim resets the queue, the stale duplicate swaps the friendly 422.
 * Asset gating and tickets.js guards: tickets_static_test.go (Go).
 */

import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import {
  base,
  createUserAsAdmin,
  loginAs,
  logout,
  seededCredentials,
} from "./helpers/auth.js";
import {
  createTicketViaUi,
  resolveUserEditHref,
} from "./helpers/navigation.js";

const uniq = Date.now().toString(36).slice(2, 8);
const agent = {
  name: "Claim Agent",
  email: `claim-agent-${uniq}@example.com`,
  password: "Secret123!",
};
const deskName = `Claim desk ${uniq}`;
const categoryName = `Claim flow ${uniq}`;
const ticketTitle = `Claimable ticket ${uniq}`;

const claimableRow = (page: Page, title: string) =>
  page.locator(".agent-row-claimable").filter({ hasText: title });

const queueHeading = (page: Page, name: string) =>
  page.getByRole("heading", { name, exact: true });

async function prepareFixtures(page: Page): Promise<void> {
  await loginAs(page, seededCredentials.email, seededCredentials.password);
  await createUserAsAdmin(page, agent);
  await page.goto(base() + "/users");
  const editHref = await resolveUserEditHref(page, agent.name);
  await page.goto(base() + editHref);
  const agentRow = page.locator(`tr[data-user-name="${agent.name}"]`);
  await page.locator('select[name="role"]').selectOption("agent");
  await page.getByRole("button", { name: /save changes/i }).click();
  await expect(page).toHaveURL(/\/users$/);
  await expect(agentRow).toContainText("Agent");

  await page.goto(base() + "/categories?view=structure");
  const deptHref = await page
    .locator(".category-level-departments .category-structure-row")
    .first()
    .getAttribute("href");
  const deptId = deptHref?.match(/department_id=(\d+)/)?.[1];
  if (!deptId) throw new Error(`no department at ${page.url()}`);

  await page.goto(
    base() + "/categories/desks/new?view=structure&department_id=" + deptId,
  );
  const deskDrawer = page.getByRole("dialog", { name: /New desk/i });
  await expect(deskDrawer).toBeVisible();
  await deskDrawer.locator('select[name="department_id"]').selectOption(deptId);
  await deskDrawer.getByLabel("Name").fill(deskName);
  await deskDrawer.getByRole("button", { name: /create desk/i }).click();
  const deskRow = page
    .locator(".category-level-desks .category-structure-item")
    .filter({ hasText: deskName });
  await expect(deskRow).toHaveCount(1);
  const deskHref = await deskRow
    .locator('a[href*="desk_id="]')
    .first()
    .getAttribute("href");
  const deskId = deskHref?.match(/desk_id=(\d+)/)?.[1];
  if (!deskId) throw new Error(`no desk ${deskName} at ${page.url()}`);

  await page.goto(base() + `/categories/desks/${deskId}/edit?view=structure`);
  const editDrawer = page.getByRole("dialog", { name: /Edit desk/i });
  await expect(editDrawer).toBeVisible();
  await editDrawer
    .locator("select#desk-member")
    .selectOption({ label: agent.name });
  await editDrawer.getByRole("button", { name: "Add member" }).click();
  await expect(
    editDrawer.getByRole("listitem").filter({ hasText: agent.name }),
  ).toHaveCount(1);

  const ctx = `view=structure&department_id=${deptId}&desk_id=${deskId}`;
  await page.goto(base() + `/categories/new?${ctx}`);
  await expect(page.locator('select[name="desk_id"]')).toHaveValue(deskId);
  await page.locator("#category-name").fill(categoryName);
  await page.getByRole("button", { name: /create category/i }).click();
  const catRow = page
    .locator(".category-level-categories .category-structure-item")
    .filter({ hasText: categoryName });
  await expect(catRow).toHaveCount(1);
  const catHref = await catRow.locator('a[href*="/edit"]').getAttribute("href");
  const catId = catHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
  if (!catId) throw new Error(`no category at ${page.url()}`);

  await page.goto(base() + `/categories/${catId}/workflow`);
  const addStep = page.locator(".workflow-add-popover summary");
  const stepCards = page.locator(".workflow-step-card");
  await addStep.click();
  await page.getByRole("button", { name: "Assign to desk" }).click();
  await expect(stepCards).toHaveCount(1);
  await addStep.click();
  await page.getByRole("button", { name: "Manual task" }).click();
  await expect(stepCards).toHaveCount(2);
  await stepCards.first().locator(".workflow-step-card-link").click();
  await page.locator('select[name="step_0_desk"]').selectOption(deskId);
  await expect(page.locator('select[name="step_0_strategy"]')).toHaveValue(
    "claim",
  );
  await stepCards.last().locator(".workflow-step-card-link").click();
  await page.getByLabel(/instructions/i).fill("Continue");
  const saveBtn = page.locator(
    '.page-actions button[name="action"][value="save"]',
  );
  await saveBtn.click();
  await expect(
    page.locator("#save-feedback .save-feedback-message"),
  ).toHaveText("Saved");
  await page.getByRole("button", { name: /publish/i }).click();
  await expect(
    page.locator("#save-feedback .save-feedback-message"),
  ).toHaveText("Published");
  await page.reload();
  await expect(stepCards).toHaveCount(2);

  await createTicketViaUi(page, { title: ticketTitle, category: categoryName });
}

test.describe("Agent claim — winner and stale journey", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("winner and stale claim journey", async ({ page, browser }) => {
    await prepareFixtures(page);
    await logout(page);
    await loginAs(page, agent.email, agent.password);
    await page.goto(base() + "/tickets");
    const claimJS = page.locator('script[src="/static/tickets.js"]');
    await expect(claimJS).toHaveCount(1);

    const rowA = claimableRow(page, ticketTitle);
    await expect(rowA).toHaveCount(1);
    const viewHref = await rowA
      .locator("a.agent-row-open")
      .getAttribute("href");
    const ticketId = viewHref?.match(/^\/tickets\/(\d+)$/)?.[1];
    if (!ticketId) throw new Error(`no ticket at ${page.url()}`);
    const claimPost = await rowA
      .locator("form.agent-claim-form")
      .getAttribute("hx-post");
    const claimPath = `/tickets/${ticketId}/workflow/steps/1/complete`;
    if (claimPost !== claimPath) {
      throw new Error(`bad claim endpoint ${claimPost} at ${page.url()}`);
    }
    const orderOk = await rowA.evaluate((li) => {
      const view = li.querySelector("a.agent-row-open");
      const claim = li.querySelector(".agent-claim-form button");
      if (!view || !claim) return false;
      const pos = view.compareDocumentPosition(claim);
      return !!(pos & Node.DOCUMENT_POSITION_FOLLOWING);
    });
    expect(orderOk, "View before Claim").toBe(true);

    const contextB = await browser.newContext();
    const pageB = await contextB.newPage();
    await loginAs(pageB, agent.email, agent.password);
    await pageB.goto(base() + "/tickets");
    const rowB = claimableRow(pageB, ticketTitle);
    await expect(rowB).toHaveCount(1);

    const claimOpts = {
      endpoint: claimPath,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#agent-ticket-list",
    };
    const claimBtn = (row: typeof rowA) =>
      row.getByRole("button", { name: "Claim ticket" });
    await assertHtmxSwap(page, () => claimBtn(rowA).click(), claimOpts);
    await expect(queueHeading(page, "Assigned to me · 1")).toBeVisible();
    await expect(queueHeading(page, "Available to claim · 0")).toBeVisible();
    const winnerRow = page
      .locator(".agent-row-assigned")
      .filter({ hasText: ticketTitle });
    await expect(winnerRow.locator(".badge.in_progress")).toBeVisible();
    await expect(page.locator(".agent-claim-error")).toHaveCount(0);
    await expect(page).toHaveURL(/\/tickets$/);

    const stale = await assertHtmxSwap(pageB, () => claimBtn(rowB).click(), {
      ...claimOpts,
      expectedStatus: 422,
    });
    expect(stale.headers()["hx-retarget"]).toBe("#agent-ticket-list");
    expect(stale.headers()["hx-reswap"]).toBe("outerHTML");
    await expect(pageB.locator(".agent-claim-error")).toHaveText(
      "This ticket is no longer available to claim.",
    );
    await expect(pageB.locator("body")).not.toContainText(
      "workflow position conflict",
    );
    await expect(queueHeading(pageB, "Available to claim · 0")).toBeVisible();

    await page.reload();
    await expect(queueHeading(page, "Assigned to me · 1")).toBeVisible();
    await expect(queueHeading(page, "Available to claim · 0")).toBeVisible();
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("#ticket-detail")).toContainText(agent.name);
    await pageB.reload();
    await expect(queueHeading(pageB, "Assigned to me · 1")).toBeVisible();
    await contextB.close();
  });
});
