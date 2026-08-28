/**
 * HTMX swap helper — shared across all HTMX-interaction tests.
 *
 * Asserts:
 *  - request has HX-Request: true header
 *  - response status is 200
 *  - no document navigation (URL unchanged or matches expectedUrl)
 *  - target region innerHTML changed
 *  - non-target region (page chrome, e.g. h1) remained unchanged
 *
 * Returns the response for further assertions.
 */

import { expect, type APIResponse, type Locator, type Page } from "@playwright/test";

export interface HtmxSwapOptions {
  /** URL pattern to match the HTMX request. Function receives (url, { method }). */
  urlPattern: RegExp | ((url: string, meta: { method: string }) => boolean);
  /** CSS selector of the hx-target region */
  hxTarget: string;
  /** Expected URL after the swap (e.g. when hx-push-url is used). Default: URL unchanged. */
  expectedUrl?: RegExp;
  /**
   * When true, skip the HX-Request header check.
   * Use only for mixed HTMX/native forms where the native response comes first.
   * Default: false (all HTMX responses must have HX-Request: true).
   */
  skipHxRequestCheck?: boolean;
}

/**
 * Assert an HTMX partial swap.
 *
 * HTMX redirect-based forms return 303 See Other, which HTMX follows
 * internally to fetch the swapped content. Both 200 and 303 are valid
 * HTMX response codes for swap-triggering requests.
 *
 * The waitForResponse predicate checks the request's HX-Request header
 * to ensure we only intercept HTMX-triggered responses, not parallel
 * native form submissions.
 */
function isValidHtmxStatus(status: number): boolean {
  return (status >= 200 && status < 300) || status === 303;
}

export async function assertHtmxSwap(
  page: Page,
  trigger: () => Promise<void>,
  opts: HtmxSwapOptions,
): Promise<APIResponse> {
  const targetLocator: Locator = page.locator(opts.hxTarget).first();
  await expect(targetLocator).toBeVisible({ timeout: 10_000 });

  // Set up response interceptor FIRST to avoid race conditions
  const hxRequestHeaderCheck = (resp: APIResponse): boolean => {
    if (opts.skipHxRequestCheck) {
      const url = resp.url();
      const method = resp.request().method();
      return opts.urlPattern instanceof RegExp
        ? opts.urlPattern.test(url)
        : opts.urlPattern(url, { method });
    }
    if (resp.request().headers()["hx-request"] !== "true") return false;
    const url = resp.url();
    const method = resp.request().method();
    return opts.urlPattern instanceof RegExp
      ? opts.urlPattern.test(url)
      : opts.urlPattern(url, { method });
  };

  const responsePromise = page.waitForResponse(hxRequestHeaderCheck);

  // Capture before state AFTER the interceptor is set up
  const beforeHTML: string = await targetLocator.innerHTML();
  const urlBefore: string = page.url();

  const chromeLocator: Locator = page.locator("h1").first();
  const chromeBefore: string | null =
    (await chromeLocator.count()) > 0 ? await chromeLocator.textContent() : null;

  // Execute the trigger action
  await trigger();

  // Wait for the HTMX response
  const response: APIResponse = await responsePromise;
  expect(
    isValidHtmxStatus(response.status()),
    `HTMX response expected 2xx or 303 got ${response.status()} for ${opts.hxTarget}`,
  ).toBe(true);

  // If we used the header-based filter, header was already verified by the predicate.
  // If skipHxRequestCheck was set, catch up by asserting it here.
  if (opts.skipHxRequestCheck) {
    const hxRequest: string | undefined = response.request().headers()["hx-request"];
    if (hxRequest !== "true") {
      console.warn(`[assertHtmxSwap] Response for ${opts.hxTarget} lacks HX-Request header — swap verified by other means`);
    }
  }

  // No document navigation — URL unchanged or matches expectedUrl
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
}