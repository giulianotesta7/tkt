/**
 * Ticket detail controls (issue #386).
 *
 * The detail no longer carries Apply buttons or native selects. The two
 * editable properties are listbox pickers whose options are submit buttons of
 * the existing forms (maintainer decision L2, so a pick is still an explicit
 * act and #232's accidental-mutation class stays closed), the state changes are
 * header buttons (the reason-requiring one opens a dialog), and Put on hold /
 * Resume moved into the header's overflow menu.
 *
 * Each helper comes in two shapes:
 *  - `apply*` performs the interaction only, for a journey that already proves
 *    its own HTMX evidence with `assertHtmxSwap`;
 *  - `pick*` / `transitionTo` / `holdTicket` / `resumeTicket` perform it AND
 *    assert the same HTMX evidence the previous journeys asserted — exact
 *    endpoint, method, status, the swapped #ticket-detail region, no main-frame
 *    navigation and an unchanged URL — so a conversion keeps every assertion
 *    instead of weakening it. The caller still asserts the visible result.
 */

import { expect, type Page } from "@playwright/test";
import { assertHtmxSwap } from "./htmx.js";

export type Priority = "critical" | "high" | "medium" | "low";

type PickerField = "ticket-priority" | "assign-user";

/** The ticket id of the detail page currently open. */
export function ticketId(page: Page): string {
  const match = new URL(page.url()).pathname.match(/^\/tickets\/(\d+)$/);
  if (!match) throw new Error(`not a ticket detail page: ${page.url()}`);
  return match[1];
}

/** The listbox root of one property picker. */
export function picker(page: Page, field: PickerField) {
  return page.locator(`#ticket-detail [data-listbox]:has(#${field})`);
}

/** The picker's opener button, which shows the value the ticket holds. */
export function pickerButton(page: Page, field: PickerField) {
  return picker(page, field).locator(".listbox-button");
}

/** One option button of a picker. */
export function pickerOption(page: Page, field: PickerField, value: string) {
  const name = field === "ticket-priority" ? "priority" : "user_id";
  return picker(page, field).locator(`.listbox-option[name="${name}"][value="${value}"]`);
}

/** The person a picker currently holds. */
export async function currentAssignee(page: Page): Promise<string> {
  return (await pickerButton(page, "assign-user").locator(".listbox-current").innerText()).trim();
}

/** The priority a picker currently holds. */
export async function currentPriority(page: Page): Promise<string> {
  return (
    await pickerButton(page, "ticket-priority").locator(".listbox-current").innerText()
  ).trim();
}

/**
 * Opens a property picker and waits for its popover. A closed picker is inert on
 * purpose (#232): opening it changes nothing, and every pick starts here.
 */
export async function openPicker(page: Page, field: PickerField): Promise<void> {
  const button = pickerButton(page, field);
  await expect(button).toBeVisible();
  if ((await button.getAttribute("aria-expanded")) !== "true") await button.click();
  await expect(button).toHaveAttribute("aria-expanded", "true");
  await expect(picker(page, field).locator(".listbox-popover")).toBeVisible();
}

/** Performs the pick that posts the priority, without asserting the swap. */
export async function applyPriority(page: Page, priority: Priority): Promise<void> {
  await openPicker(page, "ticket-priority");
  await pickerOption(page, "ticket-priority", priority).click();
}

/**
 * Picks a priority through the listbox and asserts the existing edit request
 * (POST `/tickets/{id}/edit`, 200) swapping #ticket-detail in place.
 */
export async function pickPriority(page: Page, priority: Priority) {
  const id = ticketId(page);
  return assertHtmxSwap(page, () => applyPriority(page, priority), {
    endpoint: `/tickets/${id}/edit`,
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#ticket-detail",
  });
}

/**
 * Filters the assignee popover by name and picks that person, without asserting
 * the swap. `label` is the visible name.
 */
export async function applyAssignee(page: Page, label: string): Promise<void> {
  await openPicker(page, "assign-user");
  const search = picker(page, "assign-user").locator(".listbox-search");
  if (await search.count()) await search.fill(label);
  await expect(
    picker(page, "assign-user").locator(".listbox-option", { hasText: label }).first(),
  ).toBeVisible();
  await picker(page, "assign-user").locator(".listbox-option", { hasText: label }).first().click();
}

/**
 * Picks an assignee by name and asserts the existing assignment request
 * (POST `/tickets/{id}/assign`, 200) swapping #ticket-detail in place.
 */
export async function pickAssignee(page: Page, label: string) {
  const id = ticketId(page);
  return assertHtmxSwap(page, () => applyAssignee(page, label), {
    endpoint: `/tickets/${id}/assign`,
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#ticket-detail",
  });
}

/** Clears the assignment through the picker's Unassigned option. */
export async function unassignTicket(page: Page) {
  const id = ticketId(page);
  return assertHtmxSwap(
    page,
    async () => {
      await openPicker(page, "assign-user");
      await pickerOption(page, "assign-user", "").click();
    },
    {
      endpoint: `/tickets/${id}/assign`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#ticket-detail",
    },
  );
}

/** The header action for one target state. */
export function transitionAction(page: Page, state: string) {
  return page
    .locator("#ticket-detail .detail-actions")
    .locator(
      `form.detail-action:has(input[name="to"][value="${state}"]) button[type="submit"], [data-transition-open="${state}"]`,
    )
    .first();
}

/** Takes the header action for a target state, without asserting the swap. */
export async function applyTransition(page: Page, state: string, reason?: string): Promise<void> {
  const reasonOpener = page.locator(
    `#ticket-detail .detail-actions [data-transition-open="${state}"]`,
  );
  if (await reasonOpener.count()) {
    // A reason-requiring transition opens its dialog: the same form posts the
    // same `to` with the same `reason` field the inline control used.
    await reasonOpener.first().click();
    const dialog = page.locator("#ticket-transition-dialog");
    await expect(dialog).toBeVisible();
    const field = page.locator("#state-reason");
    await expect(field).toBeFocused();
    if (reason !== undefined) await field.fill(reason);
    await dialog.locator('button[type="submit"]').click();
    return;
  }
  const button = transitionAction(page, state);
  await expect(button).toBeVisible();
  await button.click();
}

/**
 * Takes the header transition for one target state and asserts the existing
 * transition request (POST `/tickets/{id}/transition`, 200).
 */
export async function transitionTo(page: Page, state: string, reason?: string) {
  const id = ticketId(page);
  return assertHtmxSwap(page, () => applyTransition(page, state, reason), {
    endpoint: `/tickets/${id}/transition`,
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#ticket-detail",
  });
}

/** Opens the header's overflow menu (Put on hold / Resume live there). */
export async function openTicketActions(page: Page): Promise<void> {
  const overflow = page.locator("#ticket-actions-overflow");
  await expect(overflow).toBeVisible();
  if (!(await overflow.evaluate((node) => (node as HTMLDetailsElement).open))) {
    await overflow.locator("summary").click();
  }
  await expect(overflow.locator(".detail-overflow-menu")).toBeVisible();
}

/** Puts the ticket on hold through the header menu and the existing dialog. */
export async function applyHold(page: Page, reason: string): Promise<void> {
  await openTicketActions(page);
  await page.locator("#ticket-detail [data-hold-open]").click();
  await expect(page.locator("#ticket-hold-dialog")).toBeVisible();
  const field = page.locator("#hold-reason");
  await expect(field).toBeFocused();
  await field.fill(reason);
  await page.locator("form:has(#hold-reason)").getByRole("button", { name: "Put on hold" }).click();
}

/** Puts the ticket on hold and asserts POST `/tickets/{id}/hold` with a 200. */
export async function holdTicket(page: Page, reason: string) {
  const id = ticketId(page);
  return assertHtmxSwap(page, () => applyHold(page, reason), {
    endpoint: `/tickets/${id}/hold`,
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#ticket-detail",
  });
}

/** Resumes a held ticket through the header menu's Resume form. */
export async function applyResume(page: Page): Promise<void> {
  await openTicketActions(page);
  await page.locator("#ticket-actions-overflow .hold-resume button").click();
}

/** Resumes a held ticket and asserts POST `/tickets/{id}/resume` with a 200. */
export async function resumeTicket(page: Page) {
  const id = ticketId(page);
  return assertHtmxSwap(page, () => applyResume(page), {
    endpoint: `/tickets/${id}/resume`,
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#ticket-detail",
  });
}

/** Submits the composer through the same native POST the form always used. */
export function composer(page: Page) {
  return page.locator("#ticket-detail form.comment-form");
}
