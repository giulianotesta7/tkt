/**
 * Desks journeys: list, create, rename, delete, membership.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { waitForExactPost } from "./helpers/network.js";

async function selectDesk(page: import("@playwright/test").Page, name: string): Promise<string> {
  const link = page.locator("a[data-desk-id]").filter({ has: page.getByText(name, { exact: true }) });
  await expect(link).toHaveCount(1);
  const href = await link.getAttribute("href");
  if (!href) throw new Error(`Desk link href missing for "${name}" at ${page.url()}`);
  const deskID = new URL(href, page.url()).searchParams.get("desk_id");
  if (!deskID || !/^\d+$/.test(deskID)) {
    throw new Error(`Could not resolve exact desk ID for "${name}" from ${href} at ${page.url()}`);
  }
  await link.click();
  return deskID;
}

test.describe("Desks", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("list shows seeded desk, creates, renames, and deletes a desk", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/desks");
    await expect(page.locator("h1").filter({ hasText: "Desks" })).toBeVisible();
    await expect(page.getByText("General Support").first()).toBeVisible();

    const deskName = "Probe Desk " + Date.now();
    await page.locator("details.desk-create summary").click();
    await expect(page.getByLabel(/^desk name$/i)).toBeVisible();
    await page.getByLabel(/^desk name$/i).fill(deskName);
    // Create desk — wait for POST to complete
    const createResponsePromise = waitForExactPost(page, "/desks");
    await Promise.all([
      createResponsePromise,
      page.getByRole("button", { name: /^create desk$/i }).click(),
    ]);
    const createResponse = await createResponsePromise;
    expect(createResponse.status()).toBe(303);
    await expect(page.getByText(deskName).first()).toBeVisible();

    // Select the new desk
    await selectDesk(page, deskName);
    await expect(page.locator(".desk-detail")).toBeVisible();
    const renameAction = await page.locator(".desk-rename").getAttribute("action");
    if (!renameAction) throw new Error(`Rename form action missing for desk "${deskName}" at ${page.url()}`);

    // Rename — assert persistence by checking list updates
    const renamed = deskName + " Renamed";
    const renameInput = page.locator(".desk-rename input[name='name']");
    await expect(renameInput).toBeVisible();
    await renameInput.fill(renamed);
    const renameResponsePromise = waitForExactPost(page, renameAction);
    await Promise.all([
      renameResponsePromise,
      page.locator(".desk-rename button").click(),
    ]);
    const renameResponse = await renameResponsePromise;
    expect(renameResponse.status()).toBe(303);
    await expect(page.getByText(renamed).first()).toBeVisible();
    // Persistence: reload and verify renamed persists
    await page.reload();
    await expect(page.getByText(renamed).first()).toBeVisible();
    // Re-select since reload may clear selection
    await selectDesk(page, renamed);
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Delete — must execute and verify persistence
    const deleteAction = await page.locator('.desk-detail form[action*="/delete"]').getAttribute("action");
    if (!deleteAction) throw new Error(`Delete form action missing for desk "${renamed}" at ${page.url()}`);
    page.once("dialog", (d) => d.accept());
    const deleteResponsePromise = waitForExactPost(page, deleteAction);
    await Promise.all([
      deleteResponsePromise,
      page.getByRole("button", { name: /delete desk/i }).click(),
    ]);
    const deleteResponse = await deleteResponsePromise;
    expect(deleteResponse.status()).toBe(303);
    expect(new URL(page.url()).pathname).toBe("/desks");
    await expect(page.getByText(renamed)).toHaveCount(0);
    // Persistence: reload and still gone
    await page.reload();
    await expect(page.getByText(renamed)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "desks create/rename/delete",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("desk membership add and remove must actually change membership", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const uname = "Bob Builder " + Date.now().toString(36).slice(2, 6);
    const uemail = `bob-${Date.now().toString(36).slice(2, 6)}@example.com`;
    await page.goto(base() + "/users/new");
    await page.getByLabel(/^name$/i).fill(uname);
    await page.getByLabel(/^email$/i).fill(uemail);
    await page.getByLabel(/^password$/i).fill("Secret123!");
    await page.getByRole("button", { name: /create user/i }).click();
    await expect(page).toHaveURL(/\/users/);
    await expect(page.getByText(uname)).toBeVisible();
    // Desk members must be agent+ — promote to agent
    {
      const row = page.locator("tr[data-user-name]").filter({ has: page.getByText(uname, { exact: true }) });
      await expect(row).toHaveCount(1);
      const editLink = row.locator('a[href*="/users/"][href*="/edit"]').first();
      let href = await editLink.getAttribute("href");
      if (!href) throw new Error(`edit href missing for user "${uname}" using exact row at ${page.url()}`);
      href = href.split("?")[0];
      await page.goto(base() + href);
      await expect(page.locator('h1:has-text("Edit user"), h2:has-text("Edit user")').first()).toBeVisible();
      const roleSelect = page.locator('select[name="role"]');
      await expect(roleSelect).toBeVisible();
      await roleSelect.selectOption("agent");
      const respPromise = waitForExactPost(page, href);
      await page.getByRole("button", { name: /save changes/i }).click();
      const resp = await respPromise;
      expect(resp.status()).toBe(200);
      await page.goto(base() + "/users");
      await expect(page.getByText(uname)).toBeVisible();
    }

    await page.goto(base() + "/desks");
    await selectDesk(page, "General Support");
    await expect(page.locator(".desk-detail")).toBeVisible();

    // Membership add — must execute unconditionally (no silent skip)
    const addSelect = page.locator(".desk-add-member select");
    await expect(addSelect).toBeVisible();
    const option = page.locator(`.desk-add-member option:has-text("${uname}")`);
    await expect(option).toBeAttached();
    await addSelect.selectOption({ label: uname });
    await expect(addSelect.locator("option:checked")).toHaveText(uname);
    const addAction = await page.locator(".desk-add-member").getAttribute("action");
    if (!addAction) throw new Error(`Add-member form action missing for desk "General Support" at ${page.url()}`);
    const selectedMemberID = await addSelect.inputValue();
    const addPath = new URL(addAction, page.url()).pathname;
    if (!/^\/desks\/\d+\/members$/.test(addPath) || !/^\d+$/.test(selectedMemberID)) {
      throw new Error(`Could not resolve exact add-member target for user "${uname}" from ${addAction} at ${page.url()}`);
    }
    const addResponsePromise = waitForExactPost(page, addAction);
    await Promise.all([
      addResponsePromise,
      page.locator(".desk-add-member button").click(),
    ]);
    const addResponse = await addResponsePromise;
    expect(addResponse.status()).toBe(303);
    await expect(page.locator(".desk-member-list").getByText(uname)).toBeVisible();
    // Persistence check: reload still shows member
    await page.reload();
    // After reload, selection resets — re-select desk
    await selectDesk(page, "General Support");
    await expect(page.locator(".desk-member-list").getByText(uname)).toBeVisible({ timeout: 10_000 });

    // Remove member — must actually disappear
    const memberRow = page.locator(".desk-member-list li").filter({ has: page.getByText(uname, { exact: true }) });
    await expect(memberRow).toHaveCount(1);
    const removeBtn = memberRow.getByRole("button", { name: /remove/i });
    await expect(removeBtn).toBeVisible();
    const removeForm = removeBtn.locator("xpath=ancestor::form");
    const removeAction = await removeForm.getAttribute("action");
    if (!removeAction) throw new Error(`Remove-member form action missing for user "${uname}" at ${page.url()}`);
    const removePath = new URL(removeAction, page.url()).pathname;
    if (!/^\/desks\/\d+\/members\/\d+\/delete$/.test(removePath)) {
      throw new Error(`Unexpected remove-member target ${removePath} for user "${uname}" at ${page.url()}`);
    }
    const removeResponsePromise = waitForExactPost(page, removeAction);
    await Promise.all([
      removeResponsePromise,
      removeBtn.click(),
    ]);
    const removeResponse = await removeResponsePromise;
    expect(removeResponse.status()).toBe(303);
    await expect(page.locator(".desk-member-list").getByText(uname)).toHaveCount(0);
    await page.reload();
    await selectDesk(page, "General Support");
    await expect(page.locator(".desk-member-list").getByText(uname)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "desks membership",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
