/**
 * Ticket on hold journey (issue #352, S1/S2/S3/S5/S8).
 *
 * The canonical browser path of the hold: an agent puts an open ticket on hold
 * with a mandatory reason, the agent's detail reads the hold banner, the
 * on-hold chip and the PAUSED resolution milestone, the requester reads "This
 * ticket is waiting for your reply" plus the reason and the timeline narrative,
 * the agent resumes (banner/chip gone, the resolution clock running again), the
 * requester's reply to a new hold ends it (S1), and resolving a held ticket
 * ends the hold in the same write.
 *
 * Layer boundary: exhaustive authorization, validation copy and persistence are
 * covered by Go tests (`internal/adapters/http/handlers_ticket_hold_test.go`,
 * `internal/application`, `internal/domain`). Here we prove the visible
 * behaviour of one critical journey.
 *
 * SLA is instance-wide, so the journey enables it, creates its own committed
 * fixture, and restores it (disable) in afterEach — the same arrangement the
 * SLA journeys in `ticket-detail.spec.ts` use.
 */

import { expect, test, type Page, type Request } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import {
  base,
  createUserAsAdmin,
  loginAs,
  loginAsSeeded,
  logout,
  setSLAEnabled,
} from "./helpers/auth.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import {
  assertCanonicalScreen,
  assertNoHorizontalOverflow,
  assertNothingClipped,
  collectObservability,
  expectHairlineBorder,
} from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { applyTransition, openTicketActions } from "./helpers/ticket.js";

/** The header's state badge, which carries the on-hold chip (issue #386, S1). */
function stateSection(page: Page) {
  return page.locator("#ticket-detail .detail-state");
}

/** The staff-only SLA panel of the ticket detail page. */
// holdNotice is the staff on-hold notice in the SLA section (with Resume).
function holdNotice(page: Page) {
  return slaSection(page).locator(".sla-hold-status");
}

// openHoldDialog opens the hold dialog from the header's overflow menu (issue
// #386, S1: Put on hold / Resume moved there; the dialog itself is unchanged).
async function openHoldDialog(page: Page) {
  await openTicketActions(page);
  await page.locator("#ticket-detail [data-hold-open]").click();
  await expect(page.locator("#ticket-hold-dialog")).toBeVisible();
}

function slaSection(page: Page) {
  return page
    .locator("#ticket-detail .prop-section")
    .filter({ has: page.locator(".prop-heading", { hasText: /^SLA/ }) });
}

test.describe("Ticket on hold", () => {
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

  test("an agent holds and resumes a ticket while the requester reads why it waits", async ({
    page,
  }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const suffix = Date.now().toString(36).slice(2, 8);
    const requesterEmail = `on-hold-${suffix}@tkt.test`;
    const requesterPassword = "Secret123!";
    const requesterName = `Rosa Hold ${suffix}`;

    await loginAsSeeded(page);
    await createUserAsAdmin(page, {
      name: requesterName,
      email: requesterEmail,
      password: requesterPassword,
    });
    // The requester files their own ticket, so the requester detail below is
    // their own screen rather than an admin's view of it.
    await setSLAEnabled(page, true);
    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);
    const title = `On hold journey ${suffix}`;
    const id = await createTicketViaUi(page, {
      title,
      description: "the laptop charger stopped working",
      category: "General",
      priority: "high",
    });
    await logout(page);
    await loginAsSeeded(page);

    const detailPath = `/tickets/${id}`;
    const holdPath = `${detailPath}/hold`;
    const resumePath = `${detailPath}/resume`;
    const transitionPath = `${detailPath}/transition`;

    // An agent picks the ticket up first, so the resolve step of S5 below is a
    // real in_progress → resolved move.
    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await applyTransition(page, "in_progress");
      },
      {
        endpoint: transitionPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.locator("#ticket-detail .badge.in_progress")).toBeVisible();

    // ---------- S3: the reason is mandatory ----------

    // The hold costs no room until it is needed: the reason lives in a dialog
    // opened from the SLA section.
    await expect(page.locator("#hold-reason")).toBeHidden();
    await openHoldDialog(page);
    const holdForm = page.locator("form:has(#hold-reason)");
    const reasonField = page.locator("#hold-reason");
    await expect(holdForm).toBeVisible();
    await expect(reasonField).toBeFocused();
    await expect(page.locator("#ticket-hold-dialog-title")).toHaveText("Put on hold");
    // S8: the label tells the agent the requester will read the reason.
    await expect(page.locator('label[for="hold-reason"]')).toHaveText(
      "Reason (the requester will read it)",
    );
    await expect(reasonField).toHaveAttribute("required", "");
    const holdSubmit = holdForm.getByRole("button", { name: "Put on hold" });
    await expect(holdSubmit).toBeVisible();

    const holdPosts: string[] = [];
    const navigations: string[] = [];
    const holdPostListener = (request: Request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push(`${request.method()} ${request.url()}`);
      }
      if (request.method() === "POST" && new URL(request.url()).pathname === holdPath) {
        holdPosts.push(request.url());
      }
    };
    page.on("request", holdPostListener);

    // A blank reason never reaches the server: the field is `required`, so the
    // native constraint validation blocks the submit and nothing changes.
    await holdSubmit.click();
    await page.waitForTimeout(300);
    expect(holdPosts, "a blank reason must not POST /hold").toEqual([]);
    const validity = await reasonField.evaluate((el) => {
      const field = el as HTMLTextAreaElement;
      return { valid: field.checkValidity(), message: field.validationMessage };
    });
    expect(validity.valid, "the empty reason must be invalid").toBe(false);
    expect(validity.message.length, "the browser must explain the refusal").toBeGreaterThan(0);
    expect(navigations, "the blank submit must not navigate").toEqual([]);
    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);

    // A whitespace-only reason passes the browser check and is refused by the
    // server with 422: the ticket is not held and nothing was written.
    const detailUrl = page.url();
    await reasonField.fill("   ");
    const whitespaceResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response.request().headers()["hx-request"] === "true" &&
        new URL(response.url()).pathname === holdPath,
    );
    await holdSubmit.click();
    expect((await whitespaceResponse).status(), "a whitespace reason is refused 422").toBe(422);
    await page.removeListener("request", holdPostListener);
    expect(holdPosts, "only the whitespace attempt may reach /hold").toHaveLength(1);
    expect(navigations, "the refused hold must not navigate").toEqual([]);
    expect(page.url(), "the refused hold must stay on the detail").toBe(detailUrl);
    // The refusal swaps in the re-rendered detail and reopens the dialog with
    // the server's message inside it and the reason the agent typed; the
    // ticket keeps its open, off-hold surface.
    await expect(page.locator("#ticket-hold-dialog")).toBeVisible();
    await expect(page.locator("#ticket-hold-dialog .hold-dialog-error")).toHaveText(
      "a reason is required to put the ticket on hold",
    );
    await expect(page.locator("#hold-reason")).toHaveValue("   ");
    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
    await expect(page.locator("#timeline")).not.toContainText("put the ticket on hold");
    // Cancel closes the dialog without a request.
    await page.locator("#ticket-hold-dialog").getByRole("button", { name: "Cancel" }).click();
    await expect(page.locator("#ticket-hold-dialog")).toBeHidden();

    // Nothing was persisted: a reload still shows an open ticket with the hold
    // form, an empty reason, and no hold narrative in the timeline.
    await page.reload();
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#hold-reason")).toHaveValue("");
    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
    await expect(page.locator("#timeline")).not.toContainText("put the ticket on hold");

    // The deliberate 422 above is a console error the browser logs on purpose,
    // so the structural baseline starts collecting HERE and measures only
    // unexpected failures of the surface this journey adds.
    const obs = collectObservability(page);

    // ---------- S3: the hold records the reason ----------

    const reason = `send us a photo of the jammed tray ${suffix}`;
    await openHoldDialog(page);
    await page.locator("#hold-reason").fill(reason);
    const holdSwap = await assertHtmxSwap(
      page,
      async () => {
        await page
          .locator("form:has(#hold-reason)")
          .getByRole("button", { name: "Put on hold" })
          .click();
      },
      {
        endpoint: holdPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    expect(holdSwap.status()).toBe(200);

    // The staff notice lives in the SLA section of the properties, next to
    // the clock it pauses; no banner above the ticket.
    await expect(page.locator("#ticket-detail .hold-banner")).toHaveCount(0);
    const banner = holdNotice(page);
    await expect(banner).toBeVisible();
    await expect(banner).toHaveAttribute("role", "status");
    const bannerTitle = banner.locator(".sla-hold-since");
    await expect(bannerTitle).toContainText("On hold since");
    await expect(banner.locator(".sla-hold-reason")).toHaveText(reason);
    // The pause instant is a semantic <time>, not a bare string.
    await expect(bannerTitle.locator("time")).toHaveAttribute(
      "datetime",
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/,
    );
    // The Resume control lives in the header overflow menu (issue #386, S1),
    // while the notice keeps saying since when and why.
    await openTicketActions(page);
    const resumeButton = page.locator("#ticket-actions-overflow .hold-resume button");
    await expect(resumeButton).toBeVisible();
    // A menu row carries no edge of its own: the popover that holds it draws
    // the hairline, so the row never floats on an unbounded surface.
    await expectHairlineBorder(
      page.locator("#ticket-actions-overflow .detail-overflow-menu"),
      "header overflow menu",
    );
    await page.locator("#ticket-actions-overflow summary").click();
    // A held ticket offers no second hold.
    await expect(page.locator("#hold-reason")).toHaveCount(0);

    const holdChip = stateSection(page).locator(".chip");
    await expect(holdChip).toHaveCount(1);
    await expect(holdChip).toHaveText("On hold");
    // Measured, never looked at: the chip keeps the design system's 6px chip
    // radius, its 12px metadata floor, and shows its own whole label.
    const chipMetrics = await holdChip.evaluate((el) => {
      const style = getComputedStyle(el);
      return {
        radius: style.borderTopLeftRadius,
        fontSize: style.fontSize,
        height: el.getBoundingClientRect().height,
        clientWidth: el.clientWidth,
        scrollWidth: el.scrollWidth,
      };
    });
    expect(chipMetrics.radius, "a chip is the 6px shape").toBe("6px");
    expect(chipMetrics.fontSize, "chip copy sits on the 12px metadata floor").toBe("12px");
    expect(chipMetrics.height, "a chip is one 22px line").toBe(22);
    expect(
      chipMetrics.scrollWidth,
      `the chip label must fit its box, got ${chipMetrics.scrollWidth}px in ${chipMetrics.clientWidth}px`,
    ).toBeLessThanOrEqual(chipMetrics.clientWidth + 1);

    // ---------- S2: only the resolution milestone pauses ----------

    const sla = slaSection(page);
    await expect(sla).toHaveCount(1);
    const milestones = sla.locator(".prop-row");
    await expect(milestones.nth(0).locator(".prop-label")).toHaveText("First response");
    await expect(milestones.nth(1).locator(".prop-label")).toHaveText("Resolve");
    const pausedChip = milestones.nth(1).locator(".chip");
    await expect(pausedChip).toHaveCount(1);
    await expect(pausedChip).toHaveText("Paused");
    // Paused means no countdown: the milestone stops ticking while it waits.
    await expect(milestones.nth(1).locator("[data-sla-countdown]")).toHaveCount(0);
    // The first response never pauses; it keeps counting down.
    await expect(milestones.nth(0).locator(".chip")).toHaveCount(0);
    await expect(milestones.nth(0).locator("[data-sla-countdown]")).toHaveCount(1);

    // The timeline narrates the hold with its reason, attributed to the agent
    // and without printing the reason a second time.
    const timeline = page.locator("#timeline");
    const holdEntry = timeline.locator(".timeline-entry", {
      hasText: `put the ticket on hold: ${reason}`,
    });
    await expect(holdEntry).toHaveCount(1);
    await expect(holdEntry.locator(".timeline-actor")).toHaveText("Alice Admin");
    await expect(holdEntry.locator(".timeline-action")).toHaveText(
      `put the ticket on hold: ${reason}`,
    );
    await expect(timeline.getByText(`Reason: ${reason}`)).toHaveCount(0);

    // The new visible surface keeps the structural contract: nothing clipped,
    // no horizontal overflow, the tokens and the vendored font live.
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail on hold",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // The banner and the chip must survive the phone breakpoint without
    // clipping or pushing the page sideways.
    await page.setViewportSize({ width: 390, height: 844 });
    await assertNoHorizontalOverflow(page, 390);
    await assertNothingClipped(page);
    await page.setViewportSize({ width: 1280, height: 800 });

    // ---------- S8: the requester reads the reason ----------

    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);

    // The requester's list opens with its own "waiting for your reply"
    // section (issue #411, S3): the held ticket is named by that section and
    // its label, with one primary Reply action, instead of a chip beside a
    // state badge.
    await page.goto(base() + "/tickets");
    const waitingSection = page.locator("#user-ticket-list .req-section").filter({
      has: page.locator("#waiting-tickets-title"),
    });
    await expect(waitingSection.locator("#waiting-tickets-title")).toHaveText(
      "Waiting for your reply · 1",
    );
    const waitingRow = waitingSection.locator(".req-waiting").filter({ hasText: title });
    await expect(waitingRow).toHaveCount(1);
    await expect(waitingRow.locator(".req-waiting-label")).toHaveText(
      "This ticket is waiting for your reply",
    );
    await expect(waitingRow.locator(".chip")).toHaveCount(0);
    await expect(waitingRow.getByRole("link", { name: "Reply" })).toHaveAttribute(
      "href",
      `${detailPath}#reply`,
    );
    // The held ticket is in the waiting section only: the open section never
    // lists it twice.
    await expect(
      page.locator("#user-ticket-list .req-list li.req").filter({ hasText: title }),
    ).toHaveCount(0);
    // The waiting section survives the phone fold like the rest of the shell.
    await page.setViewportSize({ width: 390, height: 844 });
    await assertNoHorizontalOverflow(page, 390);
    await assertNothingClipped(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    // Reply lands on the ticket with the composer in reach.
    await waitingRow.getByRole("link", { name: "Reply" }).click();
    await expect(page).toHaveURL(new RegExp(`${detailPath}#reply$`));
    await expect(page.locator("#reply")).toBeVisible();
    await expect(page.locator("#reply").getByLabel(/comment body/i)).toBeVisible();

    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const requesterBanner = page.locator("#ticket-detail .hold-banner");
    await expect(requesterBanner).toBeVisible();
    await expect(requesterBanner.locator(".hold-banner-title")).toHaveText(
      "This ticket is waiting for your reply",
    );
    await expect(requesterBanner.locator(".hold-banner-reason")).toHaveText(reason);
    // The hold is the agent's to end: the requester gets no control.
    await expect(requesterBanner.getByRole("button", { name: "Resume" })).toHaveCount(0);
    await expect(page.locator("#hold-reason")).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveText("On hold");
    await expect(page.locator("#timeline")).toContainText(`put the ticket on hold: ${reason}`);

    // ---------- S1/S2: the agent resumes and the clock restarts ----------

    await logout(page);
    await loginAsSeeded(page);
    await page.goto(base() + detailPath);
    await expect(holdNotice(page)).toBeVisible();
    const resumeSwap = await assertHtmxSwap(
      page,
      async () => {
        await openTicketActions(page);
        await page.locator("#ticket-actions-overflow .hold-resume button").click();
      },
      {
        endpoint: resumePath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    expect(resumeSwap.status()).toBe(200);

    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
    await openTicketActions(page);
    await expect(page.locator("#ticket-detail [data-hold-open]")).toBeVisible();
    await page.locator("#ticket-actions-overflow summary").click();
    await expect(page.locator("#timeline")).toContainText("took the ticket off hold");
    // Resumed: the resolution milestone counts down again, no "Paused".
    const resumedSla = slaSection(page);
    await expect(resumedSla.locator(".chip")).toHaveCount(0);
    await expect(resumedSla.locator("[data-sla-countdown]")).toHaveCount(2);

    await page.reload();
    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
    await expect(page.locator("#timeline")).toContainText("took the ticket off hold");

    // ---------- S1: the requester's reply ends the hold ----------

    const replyReason = `waiting for the asset tag ${suffix}`;
    await openHoldDialog(page);
    await page.locator("#hold-reason").fill(replyReason);
    await assertHtmxSwap(
      page,
      async () => {
        await page
          .locator("form:has(#hold-reason)")
          .getByRole("button", { name: "Put on hold" })
          .click();
      },
      {
        endpoint: holdPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(holdNotice(page)).toBeVisible();

    await logout(page);
    await loginAs(page, requesterEmail, requesterPassword);
    await page.goto(base() + detailPath);
    await expect(page.locator("#ticket-detail .hold-banner")).toBeVisible();
    const replyBody = `asset tag is TK-${suffix}`;
    await page.getByLabel(/comment body/i).fill(replyBody);
    // The comment form is a native POST: 303 back to the detail.
    const replyResponse = page.waitForResponse(
      (r) =>
        r.request().method() === "POST" && new URL(r.url()).pathname === `${detailPath}/comments`,
    );
    await page.getByRole("button", { name: "Reply", exact: true }).click();
    expect((await replyResponse).status()).toBe(303);
    await page.waitForURL((u) => u.pathname === detailPath);
    await expect(page.locator("#ticket-detail .hold-banner")).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
    await expect(page.locator("#timeline")).toContainText(replyBody);
    await expect(page.locator("#timeline")).toContainText("took the ticket off hold");

    await logout(page);
    await loginAsSeeded(page);
    await page.goto(base() + detailPath);
    await expect(holdNotice(page)).toHaveCount(0);

    // ---------- S5: resolving a held ticket ends the hold ----------

    const secondReason = `waiting for the charger photo ${suffix}`;
    await openHoldDialog(page);
    await page.locator("#hold-reason").fill(secondReason);
    await assertHtmxSwap(
      page,
      async () => {
        await page
          .locator("form:has(#hold-reason)")
          .getByRole("button", { name: "Put on hold" })
          .click();
      },
      {
        endpoint: holdPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(holdNotice(page)).toBeVisible();
    await expect(stateSection(page).locator(".chip")).toHaveText("On hold");
    await expect(slaSection(page).locator(".chip")).toHaveText("Paused");

    await assertHtmxSwap(
      page,
      async () => {
        await applyTransition(page, "resolved");
      },
      {
        endpoint: transitionPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(page.locator("#ticket-detail .badge.resolved")).toBeVisible();
    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
    await expect(page.locator("#hold-reason")).toHaveCount(0);
    await expect(page.locator("#timeline")).toContainText(
      `put the ticket on hold: ${secondReason}`,
    );
    await expect(page.locator("#timeline")).toContainText("took the ticket off hold");

    // The held-then-resolved ticket stays resolved and off hold after reload.
    await page.reload();
    await expect(page.locator("#ticket-detail .badge.resolved")).toBeVisible();
    await expect(holdNotice(page)).toHaveCount(0);
    await expect(stateSection(page).locator(".chip")).toHaveCount(0);
  });
});
