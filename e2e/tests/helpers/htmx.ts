/**
 * HTMX swap helper — shared across all HTMX-interaction tests.
 *
 * Asserts jointly:
 *  - A request matching endpoint, method, and HX-Request: true header
 *  - The exact expected response status
 *  - The hx-target region's innerHTML changed
 *  - Zero document navigation requests on the main frame during the swap
 *  - A non-target chrome region (h1) remained unchanged
 *  - URL unchanged or satisfies hx-push-url contract
 *
 * The consumer test must also assert the domain-visible result.
 *
 * A native form submission (no hx-post) must NOT use this helper.
 * Test it as an ordinary navigation: request, expected navigation, final URL, visible result.
 */

import { expect, type Locator, type Page, type Request, type Response } from "@playwright/test";

export interface HtmxSwapOptions {
  /** Endpoint pathname (exact URL path, e.g. "/tickets"). For query-param matching use a predicate. */
  endpoint: string | RegExp | ((url: string) => boolean);
  /** Expected HTTP method (GET, POST, etc.). */
  method: string;
  /** Expected response status code. */
  expectedStatus: number;
  /** CSS selector of the hx-target region. */
  hxTarget: string;
  /**
   * Expected URL after the swap.
   * Omit to assert URL unchanged. Provide a RegExp when hx-push-url is used.
   */
  expectedUrl?: RegExp;
}

interface NavigationEvent {
  method: string;
  url: string;
}

/**
 * Assert an HTMX partial swap.
 *
 * 1. Sets up a navigation listener on the main frame.
 * 2. Sets up a response interceptor for the expected HTMX request.
 * 3. Captures before-state of target and chrome.
 * 4. Executes the trigger action.
 * 5. Waits for the HTMX response and validates it.
 * 6. Waits for the target region to actually change via `expect.poll()`.
 * 7. Asserts zero navigation events, chrome intact, URL correct.
 * 8. Cleans up all listeners in a finally block.
 */
export async function assertHtmxSwap(
  page: Page,
  trigger: () => Promise<void>,
  opts: HtmxSwapOptions,
): Promise<Response> {
  const targetLocator: Locator = page.locator(opts.hxTarget).first();
  await expect(targetLocator).toBeVisible({ timeout: 10_000 });

  // Navigation events accumulator
  const navigations: NavigationEvent[] = [];
  const navigationHandler = (request: Request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
      navigations.push({ method: request.method(), url: request.url() });
    }
  };

  let responsePromise: Promise<Response>;

  try {
    page.on("request", navigationHandler);

    // Set up response interceptor for the HTMX request
    const endpointMatcher = (resp: Response): boolean => {
      if (resp.request().headers()["hx-request"] !== "true") return false;
      const url = resp.url();
      const method = resp.request().method();
      if (method !== opts.method) return false;
      if (opts.endpoint instanceof RegExp) return opts.endpoint.test(url);
      if (typeof opts.endpoint === "function") return opts.endpoint(url);
      // For string endpoints, compare the exact pathname. Invalid response
      // URLs are test failures, never a reason to fall back to substring matching.
      let parsedURL: URL;
      try {
        parsedURL = new URL(url);
      } catch (error) {
        throw new Error(`Invalid HTMX response URL "${url}"`, { cause: error });
      }
      return parsedURL.pathname === opts.endpoint;
    };

    responsePromise = page.waitForResponse(endpointMatcher);

    // Capture before state AFTER the interceptor is set up
    const beforeHTML: string = await targetLocator.innerHTML();
    const urlBefore: string = page.url();

    const chromeLocator: Locator = page.locator("h1").first();
    const chromeBefore: string | null =
      (await chromeLocator.count()) > 0 ? await chromeLocator.textContent() : null;

    // Execute the trigger action
    await trigger();

    // Wait for the HTMX response
    const response: Response = await responsePromise;

    // Assert exact expected status
    expect(
      response.status(),
      `HTMX ${opts.method} ${opts.endpoint} expected ${opts.expectedStatus} got ${response.status()}`,
    ).toBe(opts.expectedStatus);

    // Wait for the target region to actually change via polling (swap may still be processing)
    await expect.poll(
      async () => targetLocator.innerHTML(),
      {
        timeout: 10_000,
        message: `HTMX target ${opts.hxTarget} did not change after swap`,
      },
    ).not.toBe(beforeHTML);

    // URL unchanged or matches expectedUrl
    if (opts.expectedUrl) {
      expect(page.url()).toMatch(opts.expectedUrl);
    } else {
      expect(page.url()).toBe(urlBefore);
    }

    // Assert zero document navigation events
    expect(
      navigations,
      `Expected zero document navigations during HTMX swap for ${opts.hxTarget} — found: ${JSON.stringify(navigations)}`,
    ).toEqual([]);

    // Chrome (non-target region) unchanged
    if (chromeBefore !== null) {
      const chromeAfter: string | null = await chromeLocator.textContent();
      expect(
        chromeAfter,
        `Non-target region (h1) changed after HTMX swap for ${opts.hxTarget}`,
      ).toBe(chromeBefore);
    }

    return response;
  } finally {
    page.removeListener("request", navigationHandler);
  }
}
