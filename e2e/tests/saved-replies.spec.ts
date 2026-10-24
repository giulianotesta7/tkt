/**
 * Saved replies (issue #418): the manager screen and the composer's picker.
 *
 * One screen holds a staff member's own reply texts and the ones a desk shares
 * with its members, and the comment box inserts any of them without a request.
 * These journeys prove what the browser does: the screen writes and lists a
 * reply, the picker filters them by what the operator types and inserts one at
 * the caret with the keyboard, the {requester} and {ticket} markers arrive
 * already replaced with this ticket's requester and number, and the comment
 * still posts as the same native multipart form it always was.
 *
 * A desk's shared reply is offered to the members of that desk and to nobody
 * else. The second journey proves that by presence for a member, by absence for
 * an agent outside the desk, and by the read-only tab the member reads on the
 * screen.
 *
 * Who may write which reply, and the refusal a requester meets on every one of
 * these routes, stay in Go: internal/application/canned_reply_service_test.go,
 * internal/adapters/http/handlers_replies_test.go.
 */

import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import {
  base,
  createUserAsAdmin,
  grantUserRole,
  loginAs,
  loginAsSeeded,
  logout,
} from "./helpers/auth.js";
import { catalogRowMenu, createTicketViaUi, selectCatalogDesk } from "./helpers/navigation.js";
import { pickAssignee } from "./helpers/ticket.js";
import { waitForExactPost } from "./helpers/network.js";

/** One suffix per run, so every fixture below resolves to exactly one row. */
const unique = Date.now().toString(36).slice(2, 8);

function agent(name: string): { name: string; email: string; password: string } {
  const slug = name.replace(/\s+/g, "-").toLowerCase();
  return { name, email: `${slug}-${unique}@example.com`, password: "Secret123!" };
}

/** The manager's list row for one exact title. */
function replyRow(page: Page, title: string) {
  return page.locator(".saved-replies-row").filter({ has: page.getByText(title, { exact: true }) });
}

/** The composer's saved-reply picker on the open ticket. */
function picker(page: Page) {
  return page.locator("#ticket-detail [data-saved-replies]");
}

/**
 * Write one reply on the screen's current tab: the tab's own New action, the
 * two fields, and the redirect that lands back on the tab it came from.
 */
async function createReply(
  page: Page,
  o: { title: string; body: string; tabHref: string; expectedTab: RegExp },
): Promise<void> {
  await page.goto(base() + o.tabHref);
  await page.getByRole("link", { name: "New reply", exact: true }).click();
  await expect(page.locator("#saved-reply-title")).toBeVisible();
  await page.locator("#saved-reply-title").fill(o.title);
  await page.locator("#saved-reply-body").fill(o.body);
  await page.getByRole("button", { name: "Save reply", exact: true }).click();
  await expect(page).toHaveURL(o.expectedTab);
  await expect(page.locator("#save-feedback")).toContainText("Saved");
  await expect(replyRow(page, o.title)).toHaveCount(1);
}

/**
 * Add one person to a desk through the desk drawer, which is where membership
 * lives. Returns the desk id the catalog resolved by its exact name.
 */
async function addDeskMember(page: Page, deskName: string, memberName: string): Promise<string> {
  const deskID = await selectCatalogDesk(page, deskName);
  const deskRow = page.locator(`[data-level="desk"][data-catalog-id="${deskID}"]`);
  await catalogRowMenu(deskRow).click();
  await deskRow
    .locator(".category-overflow-menu:not([hidden])")
    .getByRole("menuitem", { name: "Edit desk", exact: true })
    .click();
  const drawer = page.getByRole("dialog", { name: /Edit desk/i });
  await expect(drawer).toBeVisible();
  const addMember = drawer.locator(".desk-add-member");
  await addMember.locator("select").selectOption({ label: memberName });
  await assertHtmxSwap(page, () => addMember.locator("button").click(), {
    endpoint: `/desks/${deskID}/members`,
    method: "POST",
    expectedStatus: 200,
    hxTarget: "#category-drawer-host",
  });
  await expect(drawer.locator(".desk-member-list")).toContainText(memberName);
  return deskID;
}

test.describe("Saved replies", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("an agent inserts a saved reply at the caret and posts it as a comment", async ({
    page,
  }) => {
    test.setTimeout(180_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const writer = agent(`Reply Agent ${unique}`);
    await createUserAsAdmin(page, writer);
    await grantUserRole(page, writer.name, "agent");

    const ticketTitle = `Saved reply probe ${unique}`;
    const ticketID = await createTicketViaUi(page, {
      title: ticketTitle,
      description: "saved reply probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${ticketID}`);
    await pickAssignee(page, writer.name);
    await logout(page);

    await loginAs(page, writer.email, writer.password);

    // The screen: one tab for the operator's own replies, and the two texts.
    const careTitle = `Care needs a look ${unique}`;
    const careBody = "Hi {requester}, about {ticket}: we are on it.";
    await createReply(page, {
      title: careTitle,
      body: careBody,
      tabHref: "/replies",
      expectedTab: /\/replies$/,
    });
    await expect(replyRow(page, careTitle)).toContainText("Hi {requester}, about {ticket}");
    const closingTitle = `Closing soon ${unique}`;
    await createReply(page, {
      title: closingTitle,
      body: "Hi {requester}, I am closing this now.",
      tabHref: "/replies",
      expectedTab: /\/replies$/,
    });

    // The composer: a reply typed into the box keeps what was already there, so
    // the insertion proves the caret and not just the field.
    await page.goto(base() + `/tickets/${ticketID}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const number = (await page.locator("#ticket-detail .detail-head .num").innerText()).trim();
    const comment = page.locator("#comment-body");
    await comment.click();
    await page.keyboard.type("Before ");

    const toggle = page.locator("#ticket-detail [data-saved-replies-open]");
    await expect(toggle).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    const search = picker(page).locator("[data-saved-replies-search]");
    await expect(search).toBeFocused();
    await page.keyboard.type("care");

    const visible = picker(page).locator(".comment-saved-replies-option:not([hidden])");
    await expect(visible).toHaveCount(1);
    await expect(visible.locator(".comment-saved-replies-title")).toHaveText(careTitle);
    // The picker shows the text the comment will hold, placeholders replaced.
    await expect(visible.locator(".comment-saved-replies-preview")).toHaveText(
      `Hi Alice Admin, about ${number}: we are on it.`,
    );

    await page.keyboard.press("ArrowDown");
    await expect(visible).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    const inserted = `Before Hi Alice Admin, about ${number}: we are on it.`;
    await expect(comment).toHaveValue(inserted);
    await expect(comment).toBeFocused();

    // Posting is the form's own native multipart submission: a document
    // navigation answered by the 303, never an HTMX swap.
    await expect(page.locator("#ticket-detail form.comment-form")).not.toHaveAttribute(
      "hx-post",
      /.*/,
    );
    const navigation = page.waitForEvent("framenavigated", (frame) => frame === page.mainFrame());
    const posted = waitForExactPost(page, `/tickets/${ticketID}/comments`);
    await page.getByRole("button", { name: "Reply", exact: true }).click();
    await navigation;
    expect((await posted).status()).toBe(303);
    expect(new URL(page.url()).pathname).toBe(`/tickets/${ticketID}`);
    await expect(page.locator("#timeline")).toContainText(inserted);
    await page.reload();
    await expect(page.locator("#timeline")).toContainText(inserted);
  });

  test("a desk's shared reply is offered to its members and to nobody else", async ({ page }) => {
    test.setTimeout(240_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const member = agent(`Desk Reply Agent ${unique}`);
    const outsider = agent(`Outside Agent ${unique}`);
    await createUserAsAdmin(page, member);
    await grantUserRole(page, member.name, "agent");
    await createUserAsAdmin(page, outsider);
    await grantUserRole(page, outsider.name, "agent");
    const deskID = await addDeskMember(page, "General Support", member.name);

    // An administrator writes the desk's shared reply on that desk's tab.
    const sharedTitle = `Desk receipt ${unique}`;
    await createReply(page, {
      title: sharedTitle,
      body: "Desk reply: Hi {requester}, we have your {ticket}.",
      tabHref: `/replies?desk=${deskID}`,
      expectedTab: new RegExp(`/replies\\?desk=${deskID}$`),
    });

    // Each agent gets a ticket, so each one has a composer to open.
    const memberTicket = await createTicketViaUi(page, {
      title: `Member ticket ${unique}`,
      description: "desk reply probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${memberTicket}`);
    const memberNumber = (
      await page.locator("#ticket-detail .detail-head .num").innerText()
    ).trim();
    await pickAssignee(page, member.name);
    const outsiderTicket = await createTicketViaUi(page, {
      title: `Outside ticket ${unique}`,
      description: "desk reply probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${outsiderTicket}`);
    await pickAssignee(page, outsider.name);
    await logout(page);

    await loginAs(page, member.email, member.password);
    await page.goto(base() + `/tickets/${memberTicket}`);
    await page.locator("#ticket-detail [data-saved-replies-open]").click();
    await expect(picker(page).locator("[data-saved-replies-search]")).toBeFocused();
    await expect(
      picker(page)
        .locator(".comment-saved-replies-group-label")
        .filter({ hasText: "General Support" }),
    ).toHaveCount(1);
    const shared = picker(page)
      .locator(".comment-saved-replies-option")
      .filter({ has: page.getByText(sharedTitle, { exact: true }) });
    await expect(shared).toHaveCount(1);
    await expect(shared.locator(".comment-saved-replies-preview")).toHaveText(
      `Desk reply: Hi Alice Admin, we have your ${memberNumber}.`,
    );

    // The same reply on the screen a member may read but not change.
    await page.goto(base() + `/replies?desk=${deskID}`);
    await expect(page.locator(".saved-replies-tab[aria-selected='true']")).toHaveText(
      "General Support",
    );
    await expect(replyRow(page, sharedTitle)).toHaveCount(1);
    await expect(replyRow(page, sharedTitle).locator(".chip")).toHaveText("Read only");
    await expect(page.getByRole("link", { name: "New reply", exact: true })).toHaveCount(0);
    await logout(page);

    await loginAs(page, outsider.email, outsider.password);
    await page.goto(base() + `/tickets/${outsiderTicket}`);
    const outsideToggle = page.locator("#ticket-detail [data-saved-replies-open]");
    await outsideToggle.click();
    await expect(outsideToggle).toHaveAttribute("aria-expanded", "true");
    const outsidePicker = picker(page);
    // Nothing is offered here, so the popover carries no list to filter and no
    // group for the desk the agent does not work on.
    await expect(outsidePicker.locator("[data-saved-replies-search]")).toHaveCount(0);
    await expect(outsidePicker.locator(".comment-saved-replies-group-label")).toHaveCount(0);
    await expect(outsidePicker).not.toContainText(sharedTitle);
    await expect(outsidePicker.locator(".comment-saved-replies-empty")).toHaveText(
      "No saved replies yet.",
    );

    // And the screen offers no tab for a desk the agent does not work on.
    await page.goto(base() + "/replies");
    await expect(page.locator(".saved-replies-tab")).toHaveCount(1);
    await expect(page.locator(".saved-replies-tabs")).not.toContainText("General Support");
  });
});
