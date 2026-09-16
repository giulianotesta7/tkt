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

import { test, expect, type Route } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";
import { createTicketViaUi } from "./helpers/navigation.js";

test.describe("Ticket detail", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("detail shows Properties sidebar, timeline, state, description, and category", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const title = "Detail probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, { title, description: "detail probe", category: "General", priority: "high" });

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
    await expect(page.locator("#workflow-pending")).toContainText("Updates will appear here when complete.");
    await expect(page.locator("#workflow-pending .workflow-instruction")).toHaveCount(0);
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(/workflow-pending-info/);
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
    const id = await createTicketViaUi(page, { title, description: "detail probe", category: "General", priority: "high" });

    // Drive ticket to resolved via transitions new → in_progress → resolved
    for (const target of ["in_progress", "resolved"] as const) {
      await page.goto(base() + `/tickets/${id}`);
      await expect(page.locator("#ticket-detail")).toBeVisible();
      const moveSelect = page.locator("#ticket-state");
      await expect(moveSelect).toBeVisible();
      const resp = await assertHtmxSwap(page, async () => {
        await moveSelect.selectOption(target);
      }, {
        endpoint: `/tickets/${id}/transition`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      });
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
    const cancelId = await createTicketViaUi(page, { title: cancelTitle, description: "detail probe", category: "General", priority: "high" });
    await page.goto(base() + `/tickets/${cancelId}`);
    const toCancel = page.locator("#ticket-state");
    await expect(toCancel).toBeVisible();
    {
      const resp = await assertHtmxSwap(page, async () => {
        await toCancel.selectOption("cancelled");
      }, {
        endpoint: `/tickets/${cancelId}/transition`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      });
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

  test("priority change via HTMX swap updates #ticket-detail without full navigation", async ({ page }) => {
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

    await assertHtmxSwap(page, async () => {
      await prioritySelect.selectOption("critical");
    }, {
      endpoint: `/tickets/${id}/edit`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#ticket-detail",
    });

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

      test("persistent failure survives an unrelated drawer success beyond five seconds", async ({ page }) => {
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

      test("a later aborted assignment keeps its error and selection when an older save returns", async ({ page }) => {
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
          const priorityRequestPromise = page.waitForRequest((request) =>
            isHtmxPost(request, { path: editPath, target: "#ticket-detail" }) &&
            new URLSearchParams(request.postData() ?? "").get("priority") === "critical",
          );
      await priority.selectOption("critical");
      const priorityRequest = await priorityRequestPromise;
      expect(priorityRequest.headers()["hx-request"]).toBe("true");
      expect(priorityRequest.headers()["hx-target"]).toBe("ticket-detail");
      expect(new URLSearchParams(priorityRequest.postData() ?? "").get("priority")).toBe("critical");
      await priorityResponseHeld;

      const assignee = page.locator("#assign-user");
      const attemptedAssignee = await assignee.locator("option:not([value=''])").first().getAttribute("value");
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
        const assignmentRequestPromise = page.waitForRequest((request) =>
          isHtmxPost(request, { path: assignPath, target: "#ticket-detail" }) &&
          new URLSearchParams(request.postData() ?? "").get("user_id") === attemptedAssignee,
        );
        await assignee.selectOption(attemptedAssignee);
        const assignmentRequest = await assignmentRequestPromise;
        expect(assignmentRequest.headers()["hx-request"]).toBe("true");
        expect(assignmentRequest.headers()["hx-target"]).toBe("ticket-detail");
        expect(new URLSearchParams(assignmentRequest.postData() ?? "").get("user_id")).toBe(attemptedAssignee);
        await expect.poll(() => assignmentAborted).toBe(true);
        const feedback = page.locator("#save-feedback");
        await expect(feedback.locator(".save-feedback-message")).toHaveText("Unable to save changes. Please try again.");
        await expect(feedback).toHaveAttribute("role", "alert");
        await expect(assignee).toHaveValue(attemptedAssignee);

            releasePriorityResponse();
            const [, swapRejected] = await Promise.all([
              priorityRouteFulfilledPromise,
              prioritySwapRejected,
            ]);
            expect(swapRejected).toBe(true);
            await expect(feedback.locator(".save-feedback-message")).toHaveText("Unable to save changes. Please try again.");
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