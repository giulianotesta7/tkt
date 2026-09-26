/**
 * Ticket detail journeys: Properties sidebar, closed-state comment rejection, priority change.
 *
 * The canonical public-comment journey lives in tickets.spec.ts (native POST 303 + timeline + persistence).
 * Here: structural detail contract (Properties, state, category, description, timeline),
 * browser-visible comment-form rejection on closed states, and priority change via HTMX swap.
 *
 * Exhaustive HTTP rejection (403/422) for direct POSTs is covered by Go tests
 * (internal/adapters/http/handlers_comment_test.go).
 */

import { test, expect, type Page, type Request, type Route } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base, setSLAEnabled } from "./helpers/auth.js";
import {
  assertCanonicalScreen,
  collectObservability,
  expectHairlineBorder,
} from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";

/** The staff-only SLA panel of the ticket detail page. */
function slaPanel(page: Page) {
  return page
    .locator("#ticket-detail .prop-section")
    .filter({ has: page.locator(".prop-heading", { hasText: /^SLA/ }) });
}

test.describe("Ticket detail", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("detail shows Properties sidebar, timeline, state, description, and category", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Detail probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "detail probe",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByText("Properties").first()).toBeVisible();
    await expect(page.getByText("Requester")).toBeVisible();
    await expect(page.getByText("Category")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText("General");
    await expect(page.getByText("State")).toBeVisible();
    await expect(page.locator("#timeline")).toBeVisible();
    await expect(page.getByText("Description")).toBeVisible();

    // Requester-owned ticket with a pending manual step: passive viewer contract.
    // Alice created the ticket, so she cannot act on the seeded General workflow step.
    await expect(page.locator("#workflow-pending")).toBeVisible();
    await expect(page.locator("#workflow-pending")).toHaveClass(/workflow-pending-info/);
    await expect(page.locator("#workflow-pending")).toContainText("In progress");
    await expect(page.locator("#workflow-pending")).toContainText(
      "Updates will appear here when complete.",
    );
    await expect(page.locator("#workflow-pending .workflow-instruction")).toHaveCount(0);
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(
      /workflow-pending-info/,
    );
    await expect(page.locator(".current-task-card")).toHaveCount(0);
    await expect(page.locator('form[action*="/workflow/steps/"]')).toHaveCount(0);

    // The compact passive projection must not introduce horizontal overflow
    // on the mobile breakpoint.
    await page.setViewportSize({ width: 390, height: 844 });
    const mobileOverflow = await page.evaluate(
      () => document.documentElement.scrollWidth > window.innerWidth,
    );
    expect(mobileOverflow).toBe(false);
    await page.setViewportSize({ width: 1280, height: 800 });

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail properties",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("comment form hidden on closed states; requester-owned close blocked", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Closed comment probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "detail probe",
      category: "General",
      priority: "high",
    });

    // Drive ticket to resolved via transitions new → in_progress → resolved
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(page.locator("#ticket-detail")).toBeVisible();
      const moveSelect = page.locator("#ticket-state");
      await expect(moveSelect).toBeVisible();
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await moveSelect.selectOption(target);
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
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.getByText("Resolved").first()).toBeVisible({ timeout: 10_000 });
    const beforeTimeline = await page.locator("#timeline").textContent();
    // The ticket requester (the creating admin here) keeps the comment form in
    // resolved via the requester carve-out (issue #55); non-requesters do not.
    await expect(page.getByLabel(/comment body/i)).toHaveCount(1);
    await page.reload();
    const afterTimeline = await page.locator("#timeline").textContent();
    expect(afterTimeline).toEqual(beforeTimeline);

    // A requester-owned resolved ticket can no longer be closed via the
    // state transition (issue #55 closure gate): the Move-to control offers
    // only the reopen (in_progress), never `closed`, and the state stays
    // resolved until the requester confirms. Direct-POST rejection (403) is
    // exhaustively covered by Go tests (see spec header note).
    await page.goto(base() + `/tickets/${id}`);
    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();
    const closedOption = await moveSelect.locator('option[value="closed"]').count();
    expect(closedOption).toBe(0);
    // The requester keeps the comment form (carve-out); only the transition
    // to closed is blocked. The comment form stays for the requester.
    await expect(page.getByLabel(/comment body/i)).toHaveCount(1);
    // State badge stays resolved (no silent closure).
    await expect(page.getByText("Resolved").first()).toBeVisible({ timeout: 10_000 });

    // Cancelled state: create a fresh ticket and cancel from new
    const cancelTitle = "Cancel probe " + Date.now().toString(36).slice(2, 8);
    const cancelId = await createTicketViaUi(page, {
      title: cancelTitle,
      description: "detail probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${cancelId}`);
    const toCancel = page.locator("#ticket-state");
    await expect(toCancel).toBeVisible();
    {
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await toCancel.selectOption("cancelled");
          await page.locator("#state-apply").click();
        },
        {
          endpoint: `/tickets/${cancelId}/transition`,
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#ticket-detail",
        },
      );
      expect(resp.status()).toBe(200);
    }
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.goto(base() + `/tickets/${cancelId}`);
    await expect(page.getByText("Cancelled").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel(/comment body/i)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail comment rejection visible",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a closed ticket reopens from Move to once the reason is revealed and applied", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const title = "Reopen probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "closed reopen reason journey",
      category: "General",
      priority: "high",
    });

    // Drive the ticket to resolved, then the requester (the creating admin)
    // confirms, which is the only UI path to `closed`.
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      const moveSelect = page.locator("#ticket-state");
      await expect(moveSelect).toBeVisible();
      const resp = await assertHtmxSwap(
        page,
        async () => {
          await moveSelect.selectOption(target);
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
    await page.goto(base() + `/tickets/${id}`);
    const confirmBtn = page.getByRole("button", { name: /Yes, close ticket/i });
    await expect(confirmBtn).toBeVisible();
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
    await expect(page.getByText("Closed").first()).toBeVisible({ timeout: 10_000 });

    // On a closed ticket the only Move-to target is the reason-requiring
    // reopen (`in_progress`). Selecting it must reveal the reason field; the
    // Apply button stays the only thing that submits.
    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();
    const reasonField = page.locator("#state-reason-field");
    await expect(reasonField).toBeHidden();
    await moveSelect.selectOption("in_progress");
    await expect(reasonField).toBeVisible();
    const reason = page.locator("#state-reason");
    await expect(reason).toBeFocused();
    await expect(reason).toHaveAttribute("required", "");

    // A non-reason target hides the field again: reverting to the placeholder
    // (the closed ticket's only other choice) toggles it back off.
    await page.evaluate(() => {
      const select = document.querySelector("#ticket-state");
      if (!(select instanceof HTMLSelectElement)) throw new Error("Missing #ticket-state");
      select.selectedIndex = 0;
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await expect(reasonField).toBeHidden();
    await moveSelect.selectOption("in_progress");
    await expect(reasonField).toBeVisible();

    // The reopen swap changes the header by design: a closed ticket renders an
    // h1, an editable in_progress ticket renders the title form. That is the
    // one swap where assertHtmxSwap's "unchanged h1 chrome" clause cannot
    // hold, so prove the same HTMX contract inline and assert the header change
    // as the visible domain result.
    const navigations: string[] = [];
    const navigationHandler = (request: Request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push(request.url());
      }
    };
    const reopenReason = `Customer replied ${Date.now().toString(36).slice(2, 6)}`;
    page.on("request", navigationHandler);
    try {
      const urlBefore = page.url();
      const detailBefore = await page.locator("#ticket-detail").innerHTML();
      const transitionResponse = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          response.request().headers()["hx-request"] === "true" &&
          new URL(response.url()).pathname === `/tickets/${id}/transition`,
      );
      await reason.fill(reopenReason);
      await page.locator("#state-apply").click();
      const response = await transitionResponse;
      expect(response.status()).toBe(200);
      await expect.poll(() => page.locator("#ticket-detail").innerHTML()).not.toBe(detailBefore);
      expect(page.url()).toBe(urlBefore);
      expect(navigations, "the reopen swap must not navigate the main frame").toEqual([]);
      await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
      await expect(page.locator("#ticket-title")).toBeVisible();
      await expect(page.locator("#timeline")).toContainText(reopenReason);
    } finally {
      page.removeListener("request", navigationHandler);
    }

    // The reopen and its reason survive a reload.
    await page.reload();
    await expect(page.getByText("In Progress").first()).toBeVisible({ timeout: 10_000 });
    await expect(page.locator("#timeline")).toContainText(reopenReason);
  });

  test("priority change via HTMX swap updates #ticket-detail without full navigation", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "HTMX probe " + Date.now().toString(36).slice(2, 8),
      description: "detail probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    // Complementary evidence: hx-post targeting #ticket-detail exists
    const hxForms = page.locator('[hx-post][hx-target="#ticket-detail"]');
    await expect(hxForms.first()).toBeVisible();

    const prioritySelect = page.locator("#ticket-priority");
    await expect(prioritySelect).toBeVisible();

    await assertHtmxSwap(
      page,
      async () => {
        await prioritySelect.selectOption("critical");
        await page
          .locator("form:has(#ticket-priority)")
          .getByRole("button", { name: "Apply" })
          .click();
      },
      {
        endpoint: `/tickets/${id}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(page.locator("#ticket-detail")).toContainText(/critical/i);
    await expect(page.locator("#save-feedback")).toContainText("Saved");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail HTMX swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("detail selects mutate only after Apply, never on change alone", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Apply guard " + Date.now().toString(36).slice(2, 8),
      description: "select change must not mutate",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    const assignee = page.locator("#assign-user");
    await expect(assignee).toBeVisible();

    // A neutral control must draw its own edge: the shared button paints a white
    // surface, so a missing hairline leaves it indistinguishable from the label
    // beside it. Read the computed value — no screenshot reveals a 1px border —
    // and pin it against the --line token and the button's own background: a
    // white hairline on the white card is what a transparency check misses.
    for (const row of ["#ticket-priority", "#assign-user", "#ticket-state"]) {
      const apply = page.locator(`form:has(${row})`).getByRole("button", { name: "Apply" });
      await expect(apply).toBeVisible();
      await expectHairlineBorder(apply, `${row} Apply`);
    }

    const assignPath = `/tickets/${id}/assign`;
    const editPath = `/tickets/${id}/edit`;
    const transitionPath = `/tickets/${id}/transition`;
    const assignRequests: string[] = [];
    const editRequests: string[] = [];
    const transitionRequests: string[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      const path = new URL(request.url()).pathname;
      if (path === assignPath) assignRequests.push(request.url());
      if (path === editPath) editRequests.push(request.url());
      if (path === transitionPath) transitionRequests.push(request.url());
    });

    // Deterministic: a bubbling `change` event alone must never mutate. The
    // removed inline onchange used to requestSubmit() from exactly this event.
    await page.evaluate(() => {
      const select = document.querySelector("#assign-user");
      if (!select) throw new Error("Missing #assign-user");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await page.waitForTimeout(300);
    expect(assignRequests, "change alone must not POST /assign").toEqual([]);

    // The same contract holds for the priority and state controls: a bubbling
    // change must not POST /edit or /transition. Reintroducing an
    // hx-trigger="change" autosave would fail exactly here.
    await page.evaluate(() => {
      const select = document.querySelector("#ticket-priority");
      if (!select) throw new Error("Missing #ticket-priority");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await page.evaluate(() => {
      const select = document.querySelector("#ticket-state");
      if (!select) throw new Error("Missing #ticket-state");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await page.waitForTimeout(300);
    expect(editRequests, "change alone must not POST /edit").toEqual([]);
    expect(transitionRequests, "change alone must not POST /transition").toEqual([]);

    // Browser-real: on a closed native select Chromium moves the selection on
    // ArrowDown and fires change — the exact reassignment footgun. The value
    // moves; the POST must not.
    const beforeArrow = await assignee.inputValue();
    await assignee.focus();
    await page.keyboard.press("ArrowDown");
    await page.waitForTimeout(300);
    const afterArrow = await assignee.inputValue();
    expect(
      afterArrow,
      "ArrowDown must move the closed-select selection for this regression to be meaningful",
    ).not.toBe(beforeArrow);
    expect(assignRequests, "ArrowDown must not POST /assign").toEqual([]);

    // Apply is the deliberate act: the same control now mutates.
    const attemptedAssignee = await assignee
      .locator("option:not([value=''])")
      .first()
      .getAttribute("value");
    if (!attemptedAssignee) throw new Error(`No assignable user at ${page.url()}`);
    await assignee.selectOption(attemptedAssignee);
    await assertHtmxSwap(
      page,
      async () => {
        await page.locator("form:has(#assign-user)").getByRole("button", { name: "Apply" }).click();
      },
      {
        endpoint: assignPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(assignee).toHaveValue(attemptedAssignee);
  });

  test("a dirty title guards a sibling Apply and Save and continue ships both fields", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const originalTitle = "Title guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title priority guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await expect(titleInput).toHaveValue(originalTitle);
    const saveButton = page.locator("form:has(#ticket-title) .title-save");
    await expect(saveButton).toBeHidden();

    // The Save button mirrors dirtiness exactly: typed value shows it, the
    // original value hides it again.
    const editedTitle = `${originalTitle} edited`;
    await titleInput.fill(editedTitle);
    await expect(saveButton).toBeVisible();
    await titleInput.fill(originalTitle);
    await expect(saveButton).toBeHidden();
    await titleInput.fill(editedTitle);
    await expect(saveButton).toBeVisible();
    await expectHairlineBorder(saveButton, "detail title Save");

    const editPath = `/tickets/${id}/edit`;
    const posts: {
      path: string;
      body: URLSearchParams;
      htmx: string | undefined;
      target: string | undefined;
    }[] = [];
    const editResponses: number[] = [];
    const navigations: { method: string; url: string }[] = [];
    const detailUrl = page.url();
    page.on("request", (request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push({ method: request.method(), url: request.url() });
      }
      if (request.method() !== "POST") return;
      posts.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
        htmx: request.headers()["hx-request"],
        target: request.headers()["hx-target"],
      });
    });
    page.on("response", (response) => {
      if (response.request().method() === "POST" && new URL(response.url()).pathname === editPath) {
        editResponses.push(response.status());
      }
    });

    // A sibling Apply must not silently destroy the typed title: the click is
    // intercepted and no request is issued until the user answers.
    await page.locator("#ticket-priority").selectOption("critical");
    await page.locator("form:has(#ticket-priority)").getByRole("button", { name: "Apply" }).click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();
    await expectHairlineBorder(
      dialog.getByRole("button", { name: "Save and continue" }),
      "title guard dialog Save and continue",
    );
    // Discard is the danger twin: a FILLED control (styles.html:195 — border and
    // background both var(--red)), whose contrast comes from its fill, so the
    // hairline-vs-own-background contract does not apply; and its border-color is
    // ID-pinned, so the .btn base mutation cannot reach it either.
    await page.waitForTimeout(300);
    expect(posts, "opening the title guard must not POST").toEqual([]);

    // Consent: two explicit HTMX requests on the same /edit endpoint. The
    // title posts only its own field first, then the remembered priority row
    // posts only its own field. Neither request carries the other form's
    // field, and both swaps stay on the main frame.
    const detailBefore = await page.locator("#ticket-detail").innerHTML();
    await dialog.getByRole("button", { name: "Save and continue" }).click();

    await expect
      .poll(() => posts.length, {
        message: "Save and continue must issue the title POST then the priority POST",
      })
      .toBe(2);
    await expect.poll(() => editResponses.length).toBe(2);
    expect(editResponses).toEqual([200, 200]);
    const [titlePost, priorityPost] = posts;
    expect(titlePost.path).toBe(editPath);
    expect(titlePost.htmx).toBe("true");
    expect(titlePost.target).toBe("ticket-detail");
    expect(titlePost.body.get("title")).toBe(editedTitle);
    expect(titlePost.body.has("priority")).toBe(false);
    expect(priorityPost.path).toBe(editPath);
    expect(priorityPost.htmx).toBe("true");
    expect(priorityPost.target).toBe("ticket-detail");
    expect(priorityPost.body.get("priority")).toBe("critical");
    expect(priorityPost.body.has("title")).toBe(false);

    // Both swaps mutated the target region in place.
    await expect.poll(() => page.locator("#ticket-detail").innerHTML()).not.toBe(detailBefore);
    expect(navigations, "the two swaps must not navigate the main frame").toEqual([]);
    expect(page.url()).toBe(detailUrl);

    await expect(dialog).toBeHidden();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#ticket-priority")).toHaveValue("critical");

    await page.reload();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#ticket-priority")).toHaveValue("critical");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail dirty title guard",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("Save and continue ships the dirty title and the pending assignment on their own endpoints", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const originalTitle = "Assign guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title assignment guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await expect(titleInput).toHaveValue(originalTitle);
    const editedTitle = `${originalTitle} edited`;
    await titleInput.fill(editedTitle);

    const assignee = page.locator("#assign-user");
    await expect(assignee).toBeVisible();
    const currentAssignee = await assignee.inputValue();
    const assignable = await assignee
      .locator("option:not([value=''])")
      .evaluateAll((options) =>
        options.map((option) => (option as HTMLOptionElement).value).filter((value) => value),
      );
    const attemptedAssignee = assignable.find((value) => value !== currentAssignee);
    if (!attemptedAssignee) {
      throw new Error(`No alternative assignable user at ${page.url()}`);
    }
    await assignee.selectOption(attemptedAssignee);

    const editPath = `/tickets/${id}/edit`;
    const assignPath = `/tickets/${id}/assign`;
    const posts: {
      path: string;
      body: URLSearchParams;
      htmx: string | undefined;
      target: string | undefined;
    }[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      posts.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
        htmx: request.headers()["hx-request"],
        target: request.headers()["hx-target"],
      });
    });

    // A sibling Apply with a dirty title is intercepted: nothing is posted
    // until the operator answers the guard.
    await page.locator("form:has(#assign-user)").getByRole("button", { name: "Apply" }).click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();
    await page.waitForTimeout(300);
    expect(posts, "opening the title guard must not POST").toEqual([]);

    // Save and continue must ship the title on its own endpoint, then the
    // remembered assignment on ITS endpoint. Before the fix the /edit copy
    // loop dropped the assignment while still rendering "Saved".
    const titleResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" && new URL(response.url()).pathname === editPath,
    );
    await dialog.getByRole("button", { name: "Save and continue" }).click();
    expect((await titleResponse).status()).toBe(200);

    await expect(dialog).toBeHidden();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#assign-user")).toHaveValue(attemptedAssignee);

    await expect
      .poll(() => posts.map((post) => post.path), {
        message: "Save and continue must issue the title POST then the assignment POST",
      })
      .toEqual([editPath, assignPath]);
    const [titlePost, assignPost] = posts;
    expect(titlePost.htmx).toBe("true");
    expect(titlePost.target).toBe("ticket-detail");
    expect(titlePost.body.get("title")).toBe(editedTitle);
    expect(titlePost.body.has("user_id")).toBe(false);
    expect(assignPost.htmx).toBe("true");
    expect(assignPost.target).toBe("ticket-detail");
    expect(assignPost.body.get("user_id")).toBe(attemptedAssignee);
    expect(assignPost.body.has("title")).toBe(false);

    await page.reload();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#assign-user")).toHaveValue(attemptedAssignee);
  });

  test("Save and continue ships the dirty title and the pending transition on their own endpoints", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const originalTitle = "State guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title transition guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await expect(titleInput).toHaveValue(originalTitle);
    const editedTitle = `${originalTitle} edited`;
    await titleInput.fill(editedTitle);

    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();
    await moveSelect.selectOption("in_progress");

    const editPath = `/tickets/${id}/edit`;
    const transitionPath = `/tickets/${id}/transition`;
    const posts: {
      path: string;
      body: URLSearchParams;
      htmx: string | undefined;
      target: string | undefined;
    }[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      posts.push({
        path: new URL(request.url()).pathname,
        body: new URLSearchParams(request.postData() ?? ""),
        htmx: request.headers()["hx-request"],
        target: request.headers()["hx-target"],
      });
    });

    await page.locator("#state-apply").click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();
    await page.waitForTimeout(300);
    expect(posts, "opening the title guard must not POST").toEqual([]);

    const titleResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" && new URL(response.url()).pathname === editPath,
    );
    await dialog.getByRole("button", { name: "Save and continue" }).click();
    expect((await titleResponse).status()).toBe(200);

    await expect(dialog).toBeHidden();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#ticket-detail .badge.in_progress")).toBeVisible();

    await expect
      .poll(() => posts.map((post) => post.path), {
        message: "Save and continue must issue the title POST then the transition POST",
      })
      .toEqual([editPath, transitionPath]);
    const [titlePost, transitionPost] = posts;
    expect(titlePost.htmx).toBe("true");
    expect(titlePost.target).toBe("ticket-detail");
    expect(titlePost.body.get("title")).toBe(editedTitle);
    expect(transitionPost.htmx).toBe("true");
    expect(transitionPost.target).toBe("ticket-detail");
    expect(transitionPost.body.get("to")).toBe("in_progress");
    expect(transitionPost.body.has("title")).toBe(false);

    await page.reload();
    await expect(page.locator("#ticket-title")).toHaveValue(editedTitle);
    await expect(page.locator("#ticket-detail .badge.in_progress")).toBeVisible();
  });

  test("Discard and continue lets the assignee Apply proceed and keeps the title", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const originalTitle = "Discard guard " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title: originalTitle,
      description: "dirty title assign discard guard",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);

    const titleInput = page.locator("#ticket-title");
    await titleInput.fill(`${originalTitle} unsaved`);

    const assignee = page.locator("#assign-user");
    const attemptedAssignee = await assignee
      .locator("option:not([value=''])")
      .first()
      .getAttribute("value");
    if (!attemptedAssignee) throw new Error(`No assignable user at ${page.url()}`);
    await assignee.selectOption(attemptedAssignee);

    await page.locator("form:has(#assign-user)").getByRole("button", { name: "Apply" }).click();
    const dialog = page.locator("#ticket-title-dialog");
    await expect(dialog).toBeVisible();

    // Discard restores the persisted title, then submits the pending assign
    // form exactly as the explicit Apply would have.
    await assertHtmxSwap(
      page,
      async () => {
        await dialog.getByRole("button", { name: "Discard and continue" }).click();
      },
      {
        endpoint: `/tickets/${id}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(page.locator("#assign-user")).toHaveValue(attemptedAssignee);
    await page.reload();
    await expect(page.locator("#assign-user")).toHaveValue(attemptedAssignee);
    await expect(page.locator("#ticket-title")).toHaveValue(originalTitle);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail dirty title discard",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("persistent failure survives an unrelated drawer success beyond five seconds", async ({
    page,
  }) => {
    test.setTimeout(15_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Failure priority " + Date.now().toString(36).slice(2, 8),
      description: "feedback priority regression",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-priority")).toBeVisible();

    await page.evaluate(() => {
      const emit = (name: string, detail: object) =>
        document.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));
      const pageSource = document.querySelector("#ticket-priority");
      if (!pageSource) throw new Error("Missing priority control for feedback test");
      const pageXHR = {};
      emit("htmx:beforeRequest", {
        elt: pageSource,
        xhr: pageXHR,
        target: document.querySelector("#ticket-detail"),
        requestConfig: { parameters: {} },
      });
      emit("htmx:sendError", { xhr: pageXHR });
    });

    const feedback = page.locator("#save-feedback");
    await expect(feedback).toHaveAttribute("role", "alert");
    await expect(feedback.locator(".save-feedback-message")).toHaveText(
      "Unable to save changes. Please try again.",
    );

    await page.evaluate(() => {
      const emit = (name: string, detail: object) =>
        document.dispatchEvent(new CustomEvent(name, { bubbles: true, detail }));
      const drawer = document.createElement("section");
      drawer.className = "category-drawer";
      const source = document.createElement("button");
      source.setAttribute("hx-post", "/unrelated-save");
      drawer.append(source);
      document.body.append(drawer);
      const drawerXHR = {
        getResponseHeader: (name: string) =>
          name === "X-Save-Feedback"
            ? '{"save-feedback":{"message":"Unrelated drawer save.","kind":"success","target":"drawer"}}'
            : null,
      };
      emit("htmx:beforeRequest", {
        elt: source,
        xhr: drawerXHR,
        target: drawer,
        requestConfig: { parameters: {} },
      });
      emit("htmx:afterOnLoad", { xhr: drawerXHR });
    });

    await expect(feedback).toHaveAttribute("role", "alert");
    await expect(feedback.locator(".save-feedback-message")).toHaveText(
      "Unable to save changes. Please try again.",
    );
    await page.waitForTimeout(5_100);
    await expect(feedback).toBeVisible();
    await expect(feedback).toHaveAttribute("role", "alert");
  });

  test("a later aborted assignment keeps its error and selection when an older save returns", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const id = await createTicketViaUi(page, {
      title: "Feedback race " + Date.now().toString(36).slice(2, 8),
      description: "hold the first response, then abort assignment",
      category: "General",
      priority: "high",
    });
    const detailPath = `/tickets/${id}`;
    const editPath = `${detailPath}/edit`;
    const assignPath = `${detailPath}/assign`;
    await page.goto(base() + detailPath);

    let releasePriorityResponse: () => void = () => undefined;
    let priorityRouteFulfilled!: () => void;
    const priorityRouteFulfilledPromise = new Promise<void>((resolve) => {
      priorityRouteFulfilled = resolve;
    });
    let markPriorityResponseHeld!: () => void;
    const priorityResponseHeld = new Promise<void>((resolve) => {
      markPriorityResponseHeld = resolve;
    });
    const holdPriorityResponse = async (route: Route) => {
      const request = route.request();
      if (!isHtmxPost(request, { path: editPath, target: "#ticket-detail" })) {
        await route.continue();
        return;
      }
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      expect(response.headers()["x-save-feedback"]).toContain('"message":"Saved"');
      markPriorityResponseHeld();
      await new Promise<void>((resolve) => {
        releasePriorityResponse = resolve;
      });
      await route.fulfill({ response });
      priorityRouteFulfilled();
    };
    await page.route((url) => url.pathname === editPath, holdPriorityResponse);
    try {
      const prioritySwapRejected = page.evaluate(
        () =>
          new Promise<boolean>((resolve) => {
            let priorityXHR: XMLHttpRequest | undefined;
            document.addEventListener("htmx:beforeRequest", (event) => {
              const detail = (event as CustomEvent).detail;
              if (
                detail?.requestConfig?.parameters?.priority === "critical" &&
                detail?.target?.id === "ticket-detail"
              ) {
                priorityXHR = detail.xhr;
              }
            });
            let swapRejected = false;
            document.addEventListener("htmx:beforeSwap", (event) => {
              const detail = (event as CustomEvent).detail;
              if (detail?.xhr === priorityXHR) swapRejected = event.defaultPrevented;
            });
            document.addEventListener("htmx:afterOnLoad", (event) => {
              const detail = (event as CustomEvent).detail;
              if (detail?.xhr === priorityXHR) {
                window.setTimeout(() => resolve(swapRejected), 0);
              }
            });
          }),
      );
      const priority = page.locator("#ticket-priority");
      const priorityRequestPromise = page.waitForRequest(
        (request) =>
          isHtmxPost(request, { path: editPath, target: "#ticket-detail" }) &&
          new URLSearchParams(request.postData() ?? "").get("priority") === "critical",
      );
      await priority.selectOption("critical");
      await page
        .locator("form:has(#ticket-priority)")
        .getByRole("button", { name: "Apply" })
        .click();
      const priorityRequest = await priorityRequestPromise;
      expect(priorityRequest.headers()["hx-request"]).toBe("true");
      expect(priorityRequest.headers()["hx-target"]).toBe("ticket-detail");
      expect(new URLSearchParams(priorityRequest.postData() ?? "").get("priority")).toBe(
        "critical",
      );
      await priorityResponseHeld;

      const assignee = page.locator("#assign-user");
      const attemptedAssignee = await assignee
        .locator("option:not([value=''])")
        .first()
        .getAttribute("value");
      if (!attemptedAssignee) throw new Error(`No assignable user at ${page.url()}`);
      let assignmentAborted = false;
      const abortAssignment = async (route: Route) => {
        const request = route.request();
        if (isHtmxPost(request, { path: assignPath, target: "#ticket-detail" })) {
          await route.abort("failed");
          assignmentAborted = true;
          return;
        }
        await route.continue();
      };
      await page.route((url) => url.pathname === assignPath, abortAssignment);
      try {
        const assignmentRequestPromise = page.waitForRequest(
          (request) =>
            isHtmxPost(request, { path: assignPath, target: "#ticket-detail" }) &&
            new URLSearchParams(request.postData() ?? "").get("user_id") === attemptedAssignee,
        );
        await assignee.selectOption(attemptedAssignee);
        await page.locator("form:has(#assign-user)").getByRole("button", { name: "Apply" }).click();
        const assignmentRequest = await assignmentRequestPromise;
        expect(assignmentRequest.headers()["hx-request"]).toBe("true");
        expect(assignmentRequest.headers()["hx-target"]).toBe("ticket-detail");
        expect(new URLSearchParams(assignmentRequest.postData() ?? "").get("user_id")).toBe(
          attemptedAssignee,
        );
        await expect.poll(() => assignmentAborted).toBe(true);
        const feedback = page.locator("#save-feedback");
        await expect(feedback.locator(".save-feedback-message")).toHaveText(
          "Unable to save changes. Please try again.",
        );
        await expect(feedback).toHaveAttribute("role", "alert");
        await expect(assignee).toHaveValue(attemptedAssignee);

        releasePriorityResponse();
        const [, swapRejected] = await Promise.all([
          priorityRouteFulfilledPromise,
          prioritySwapRejected,
        ]);
        expect(swapRejected).toBe(true);
        await expect(feedback.locator(".save-feedback-message")).toHaveText(
          "Unable to save changes. Please try again.",
        );
        await expect(feedback).toHaveAttribute("role", "alert");
        await expect(assignee).toHaveValue(attemptedAssignee);
      } finally {
        await page.unroute((url) => url.pathname === assignPath, abortAssignment);
      }
    } finally {
      releasePriorityResponse();
      await page.unroute((url) => url.pathname === editPath, holdPriorityResponse);
    }

    await page.reload();
    await expect(page.locator("#ticket-priority")).toHaveValue("critical");
  });
});

/**
 * Ticket detail SLA panel (issue #211, PR 4).
 *
 * The panel exists only for a non-`user` actor on a ticket with a frozen
 * commitment. SLA is instance-wide, so the journey enables it, creates the
 * committed ticket, and disables it again in afterEach.
 */
test.describe("Ticket detail SLA panel (seeded)", () => {
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

  test("shows the overall state and one row per milestone with the time left", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    const id = await createTicketViaUi(page, {
      title: "SLA detail " + Date.now().toString(36).slice(2, 8),
      description: "sla detail probe",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    const slaSection = page
      .locator("#ticket-detail .prop-section")
      .filter({ has: page.locator(".prop-heading", { hasText: /^SLA/ }) });
    await expect(slaSection).toHaveCount(1);

    // The heading names the section and stays silent while the overall state is
    // on_track, then ONE row per milestone: its label and the time left. The
    // target, due and achieved rows are gone by decision; the frozen due
    // instant survives as the <time datetime> the countdown reads.
    const headings = slaSection.locator(".prop-heading");
    await expect(headings).toHaveCount(1);
    await expect(headings.nth(0)).toContainText("SLA");
    await expect(headings.nth(0).locator(".badge, .sla-dot")).toHaveCount(0);

    const milestoneRows = slaSection.locator(".prop-row");
    await expect(milestoneRows).toHaveCount(2);
    await expect(milestoneRows.nth(0).locator(".prop-label")).toHaveText("First response");
    await expect(milestoneRows.nth(0).locator(".badge, .sla-dot")).toHaveCount(1);
    await expect(milestoneRows.nth(1).locator(".prop-label")).toHaveText("Resolve");
    await expect(milestoneRows.nth(1).locator(".badge, .sla-dot")).toHaveCount(1);
    // on_track is the quiet default: no green pill anywhere in the section.
    await expect(slaSection.locator(".badge, .sla-dot")).toHaveCount(2);
    // Every state speaks, on_track included: an empty cell would mean the
    // ticket has no commitment at all, and the two must not look the same.
    await expect(slaSection).toContainText("On Track");

    // Both milestones are pending, so both carry the live countdown and each
    // keeps its absolute due instant in the <time datetime>.
    const dueTimes = slaSection.locator("[data-sla-countdown]");
    await expect(dueTimes).toHaveCount(2);
    for (let index = 0; index < 2; index += 1) {
      await expect(dueTimes.nth(index)).toHaveAttribute(
        "datetime",
        /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z/,
      );
    }

    // The panel states the state and the time left, and nothing else.
    await expect(slaSection.getByText("Target", { exact: true })).toHaveCount(0);
    await expect(slaSection.getByText("Achieved", { exact: true })).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail SLA panel",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});

/**
 * Ticket detail SLA live countdown (issue #211, PR 6).
 *
 * The countdown is browser-owned: the server renders the ABSOLUTE due instant
 * in the <time datetime> and anchors the browser clock with data-server-now,
 * and the deferred /static/sla_countdown.js recomputes the remaining time from
 * that instant on every tick. Playwright's clock API installs a deterministic
 * client clock BEFORE navigation, so this journey can freeze and then
 * fast-forward the browser's "now" and prove the ticker moves while the
 * absolute instant in the markup survives.
 *
 * SLA is instance-wide: every journey enables it, creates its own committed
 * fixture, and restores it (disable) in afterEach, exactly like the panel
 * describe above.
 */
test.describe("Ticket detail SLA live countdown (seeded)", () => {
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

  test("the ticker moves with the frozen clock while the absolute due instant survives", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    const id = await createTicketViaUi(page, {
      title: "SLA countdown " + Date.now().toString(36).slice(2, 8),
      description: "sla countdown probe",
      category: "General",
      priority: "high",
    });

    // Install a deterministic client clock BEFORE navigating: the detail
    // page's deferred countdown script reads Date.now() during load and derives
    // its server/client offset from data-server-now. The high-priority target
    // (1 working hour) leaves the response due far ahead of the frozen instant.
    await page.clock.install({ time: new Date() });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();

    const slaSection = slaPanel(page);
    await expect(slaSection).toHaveCount(1);
    const countdowns = slaSection.locator("[data-sla-countdown]");
    await expect(countdowns).toHaveCount(2);

    const response = countdowns.first();
    const resolve = countdowns.nth(1);
    const ticker = response.locator(".sla-countdown-ticker");
    const coarse = response.locator(".sla-countdown-coarse");

    // The script has run: the fast per-second value is aria-hidden, and the
    // accessible coarse span carries non-empty text for a screen reader.
    await expect(ticker).toHaveAttribute("aria-hidden", "true");
    await expect(ticker).not.toBeEmpty();
    await expect(coarse).not.toBeEmpty();

    // Freeze the browser clock at the current page instant. From here only the
    // fast-forward below can move the reading, so a changed text proves ticking
    // rather than a naturally elapsed wall clock.
    const heldAt = await page.evaluate(() => Date.now());
    await page.clock.pauseAt(heldAt + 5_000);

    const before = await ticker.textContent();
    const absoluteBefore = await response.getAttribute("datetime");
    expect(absoluteBefore).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);

    await page.clock.fastForward(5_000);
    await expect(ticker).not.toHaveText(before ?? "");

    // The absolute truth must survive: the tick recomputed the text but never
    // rewrote the <time datetime> the server rendered.
    expect(await response.getAttribute("datetime")).toBe(absoluteBefore);

    // Fast-forward PAST the response due instant: the reading becomes the
    // stable "overdue" text, never negative time. The pending Resolve
    // milestone, seven working hours further out, is still counting down.
    const dueAt = Date.parse(absoluteBefore ?? "");
    const serverNow = Date.parse((await slaSection.getAttribute("data-server-now")) ?? "");
    await page.clock.fastForward(dueAt - serverNow + 60_000);
    await expect(ticker).toContainText("overdue");
    expect(await ticker.textContent()).not.toContain("-");
    await expect(resolve.locator(".sla-countdown-ticker")).not.toContainText("overdue");
    await expect(response).toHaveAttribute("datetime", absoluteBefore ?? "");

    // Hand the context back a running clock before afterEach restores the
    // instance-wide SLA switch.
    await page.clock.resume();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "ticket detail SLA live countdown",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("an achieved milestone drops the countdown and the list never loads the script", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);
    const id = await createTicketViaUi(page, {
      title: "SLA achieved " + Date.now().toString(36).slice(2, 8),
      description: "sla achieved probe",
      category: "General",
      priority: "high",
    });

    // A public staff comment is the FIRST-RESPONSE observation, so the Response
    // milestone becomes achieved on the detail page.
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.getByLabel(/comment body/i).fill("first staff response");
    const commentPost = waitForExactPost(page, `/tickets/${id}/comments`);
    await page.getByRole("button", { name: /add comment/i }).click();
    expect((await commentPost).status()).toBe(303);

    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const slaSection = slaPanel(page);
    await expect(slaSection).toHaveCount(1);

    // Response is achieved: its row carries the label and the state badge and
    // NO time at all — nothing ticks there. Only the pending Resolve milestone
    // carries the countdown.
    const rows = slaSection.locator(".prop-row");
    await expect(rows.nth(0).locator(".prop-label")).toHaveText("First response");
    await expect(rows.nth(0).locator(".sla-dot")).toHaveClass(/met/);
    await expect(rows.nth(0)).toContainText("Met");
    await expect(rows.nth(0).locator("[data-sla-countdown]")).toHaveCount(0);
    await expect(rows.nth(0).locator("time")).toHaveCount(0);
    await expect(rows.nth(1).locator(".prop-label")).toHaveText("Resolve");
    await expect(rows.nth(1).locator("[data-sla-countdown]")).toHaveCount(1);
    await expect(slaSection.locator("[data-sla-countdown]")).toHaveCount(1);

    // The list page never loads /static/sla_countdown.js: no script tag and no
    // request for it. The listener is attached after the detail render, so it
    // only observes the list navigation.
    const countdownRequests: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).pathname === "/static/sla_countdown.js") {
        countdownRequests.push(request.url());
      }
    });
    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    await expect(page.locator('script[src="/static/sla_countdown.js"]')).toHaveCount(0);
    expect(countdownRequests).toEqual([]);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets list without the countdown script",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
