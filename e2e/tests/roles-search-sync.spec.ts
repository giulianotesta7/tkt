/**
 * Issue #122 — role live-search sync (user role; the agent shares the same
 * preserved control and asset, admin/root receive neither — Go evidence in
 * tickets_static_test.go and handlers_tickets_search_sync_test.go). Covers:
 * 1. focus, value, and caret survive debounced swaps, including characters
 *    typed while a swap is in flight;
 * 2. Gamma → Delta live searches synchronize URL, input, and results on
 *    Back (→ Gamma) and Forward (→ Delta) with no main-frame navigation.
 */
import {
  expect,
  test,
  type Page,
  type Request,
  type Response,
} from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import {
  base,
  createUserAsAdmin,
  loginAs,
  seededCredentials,
} from "./helpers/auth.js";
import {
  createTicketViaUi,
  resolveUserEditHref,
} from "./helpers/navigation.js";

const uniq = Date.now().toString(36).slice(2, 8);
const search = (page: Page) =>
  page.locator(".page-actions .ticket-search input");
const heading = (page: Page) =>
  page.getByRole("heading", { name: "My tickets", exact: true });
const q = (page: Page) => new URL(page.url()).searchParams.get("q");
const focusedId = (page: Page) =>
  page.evaluate(() => document.activeElement?.id ?? "");

/** Create a user-role account as admin, then log in as it and create tickets. */
async function prepareUserWithTickets(
  page: Page,
  name: string,
  tickets: string[],
): Promise<void> {
  const email = `${name.toLowerCase().replace(/\s+/g, "-")}-${uniq}@example.com`;
  await loginAs(page, seededCredentials.email, seededCredentials.password);
  await createUserAsAdmin(page, { name, email, password: "Secret123!" });
  await page.goto(base() + "/users");
  const editHref = await resolveUserEditHref(page, name);
  await page.goto(base() + editHref);
  await page.locator('select[name="role"]').selectOption("user");
  await page.getByRole("button", { name: /save changes/i }).click();
  await expect(page).toHaveURL(/\/users$/);
  // Session swap per roles.spec.ts canon: clear cookies and sign in as the
  // new user-role account instead of clicking the rail logout control.
  await page.context().clearCookies();
  await loginAs(page, email, "Secret123!");
  for (const title of tickets) {
    await createTicketViaUi(page, {
      title,
      category: "General",
      priority: "low",
    });
  }
  await page.goto(base() + "/tickets");
  await expect(heading(page)).toBeVisible();
}

const navigationWatcher = (page: Page) => {
  const navigations: string[] = [];
  const onRequest = (request: Request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
      navigations.push(request.url());
    }
  };
  return {
    navigations,
    start: () => page.on("request", onRequest),
    stop: () => page.off("request", onRequest),
  };
};

test.describe("Role search sync (issue #122, seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("focus, value and caret survive debounced and in-flight swaps", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    const title = `Focus probe ${uniq}`;
    await prepareUserWithTickets(page, "Search User", [title]);

    // Latency makes each swap land well after its debounce, exposing any
    // input state lost across the outerHTML replacement of #tickets-screen.
    await page.route("**/tickets?*", async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 350));
      await route.continue();
    });
    let htmxResponses = 0;
    const onResponse = (response: Response) => {
      if (
        response.request().headers()["hx-request"] === "true" &&
        response.status() === 200 &&
        new URL(response.url()).pathname === "/tickets"
      ) {
        htmxResponses += 1;
      }
    };
    const nav = navigationWatcher(page);
    page.on("response", onResponse);
    nav.start();
    try {
      // Slow typing completes at least two debounced swaps mid-entry; the
      // input must stay focused and end up in sync with URL and results.
      await search(page).pressSequentially("Focus", { delay: 450 });
      expect(htmxResponses).toBeGreaterThanOrEqual(2);
      await expect(search(page)).toHaveValue("Focus");
      await expect.poll(() => q(page)).toBe("Focus");
      await expect(page.locator("#user-ticket-list")).toContainText(title);
      await expect.poll(() => focusedId(page)).toBe("role-ticket-search");
      expect(nav.navigations).toEqual([]);

      // Type while the previous swap is in flight: "b" is issued, then "c"
      // is typed before the "b" response lands. The preserved input must
      // settle as Focusbc, focused, with the caret at the end.
      await search(page).pressSequentially("b");
      await page.waitForRequest(
        (request) =>
          request.headers()["hx-request"] === "true" &&
          new URL(request.url()).searchParams.get("q") === "Focusb",
      );
      await search(page).pressSequentially("c");
      const settled = await page.waitForResponse(
        (response) =>
          response.request().headers()["hx-request"] === "true" &&
          new URL(response.url()).searchParams.get("q") === "Focusbc",
      );
      expect(settled.status()).toBe(200);
      await expect(search(page)).toHaveValue("Focusbc");
      await expect.poll(() => q(page)).toBe("Focusbc");
      await expect.poll(() => focusedId(page)).toBe("role-ticket-search");
      await expect
        .poll(() =>
          search(page).evaluate(
            (el) => (el as HTMLInputElement).selectionStart,
          ),
        )
        .toBe("Focusbc".length);
      await expect(page.locator("#user-ticket-list")).toContainText(
        "No requests match your search",
      );
      await expect(heading(page)).toBeVisible();
      expect(nav.navigations).toEqual([]);
    } finally {
      page.off("response", onResponse);
      nav.stop();
      await page.unroute("**/tickets?*");
    }
  });

  test("Back and Forward restore the input from the URL q without navigation", async ({
    page,
  }) => {
    const gamma = `Gamma probe ${uniq}`;
    const delta = `Delta probe ${uniq}`;
    await prepareUserWithTickets(page, "History User", [gamma, delta]);
    const nav = navigationWatcher(page);
    nav.start();
    try {
      const swap = (term: string) =>
        assertHtmxSwap(page, () => search(page).fill(term), {
          endpoint: (url) =>
            new URL(url).pathname === "/tickets" &&
            new URL(url).searchParams.get("q") === term,
          method: "GET",
          expectedStatus: 200,
          hxTarget: "#tickets-screen",
          expectedUrl: new RegExp(`/tickets\\?q=${term}$`),
        });

      await swap("Gamma");
      await expect(page.locator("#user-ticket-list")).toContainText(gamma);
      await expect(page.locator("#user-ticket-list")).not.toContainText(delta);
      await swap("Delta");
      await expect(page.locator("#user-ticket-list")).toContainText(delta);
      await expect(page.locator("#user-ticket-list")).not.toContainText(gamma);

      // Back must re-sync the preserved input (still holding "Delta") to the
      // restored Gamma URL and show the Gamma results — no navigation.
      await page.goBack();
      await expect.poll(() => q(page)).toBe("Gamma");
      await expect(search(page)).toHaveValue("Gamma");
      await expect(page.locator("#user-ticket-list")).toContainText(gamma);
      await expect(page.locator("#user-ticket-list")).not.toContainText(delta);
      await expect(heading(page)).toBeVisible();

      // Forward mirrors the contract for the Delta entry.
      await page.goForward();
      await expect.poll(() => q(page)).toBe("Delta");
      await expect(search(page)).toHaveValue("Delta");
      await expect(page.locator("#user-ticket-list")).toContainText(delta);
      await expect(page.locator("#user-ticket-list")).not.toContainText(gamma);
      await expect(heading(page)).toBeVisible();
      expect(nav.navigations).toEqual([]);
    } finally {
      nav.stop();
    }
  });
});
