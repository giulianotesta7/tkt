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

import { expect, type Locator, type Page, type Response } from "@playwright/test";

export interface HtmxSwapOptions {
  /** Endpoint URL pattern (string, RegExp, or predicate). */
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
 * 1. Sets up interceptor for the expected HTMX request.
 * 2. Sets up a listener for main-frame navigation requests.
 * 3. Captures before-state of target and chrome.
 * 4. Executes the trigger action.
 * 5. Waits for the HTMX response and validates it.
 * 6. Asserts zero navigation events.
 * 7. Asserts target region changed, chrome intact, URL correct.
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
  const navigationHandler = (request: {
    isNavigationRequest: () => boolean;
    frame: () => { equals: (f: unknown) => boolean };
    method: () => string;
    url: () => string;
  }) => {
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
      return url.includes(opts.endpoint);
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

    // Assert zero document navigation events
    expect(
      navigations,
      `Expected zero document navigations during HTMX swap for ${opts.hxTarget} — found: ${JSON.stringify(navigations)}`,
    ).toEqual([]);

    // URL unchanged or matches expectedUrl
    if (opts.expectedUrl) {
      expect(page.url()).toMatch(opts.expectedUrl);
    } else {
      expect(page.url()).toBe(urlBefore);
    }

    // Target region content must have changed
    await expect(targetLocator).toBeVisible();
    const afterHTML: string = await targetLocator.innerHTML();
    expect(
      afterHTML,
      `HTMX target ${opts.hxTarget} innerHTML did not change after swap`,
    ).not.toBe(beforeHTML);

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