import { expect, type Page } from "@playwright/test";

/**
 * Structural baseline helper — shared across every canonical screen.
 *
 * Asserts:
 *  - no document-level horizontal overflow (htmlScroll <= viewport)
 *  - zero console errors and zero page errors
 *
 * Viewport must already be set before navigation.
 */
export interface StructuralMetrics {
  viewport: number;
  htmlScroll: number;
  htmlClient: number;
  bodyScroll: number;
}

export async function assertNoHorizontalOverflow(
  page: Page,
  expectedViewport: number,
): Promise<StructuralMetrics> {
  const metrics = await page.evaluate(() => ({
    viewport: window.innerWidth,
    htmlScroll: document.documentElement.scrollWidth,
    htmlClient: document.documentElement.clientWidth,
    bodyScroll: document.body.scrollWidth,
  }));
  expect(metrics.viewport).toBe(expectedViewport);
  expect(
    metrics.htmlScroll,
    `html scrollWidth ${metrics.htmlScroll} exceeds viewport ${expectedViewport}`,
  ).toBeLessThanOrEqual(expectedViewport);
  expect(metrics.htmlClient).toBe(expectedViewport);
  expect(metrics.bodyScroll).toBeLessThanOrEqual(expectedViewport);
  return metrics;
}

export function collectObservability(page: Page): {
  consoleErrors: string[];
  pageErrors: string[];
  failedRequests: string[];
} {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  const failedRequests: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("pageerror", (err) => pageErrors.push(String(err)));
  page.on("requestfailed", (req) => {
    const failure = req.failure();
    failedRequests.push(`${req.method()} ${req.url()} :: ${failure?.errorText ?? "unknown"}`);
  });
  return { consoleErrors, pageErrors, failedRequests };
}

export function expectNoConsoleOrPageErrors(
  consoleErrors: string[],
  pageErrors: string[],
): void {
  expect(consoleErrors, `console errors: ${consoleErrors.join("; ")}`).toEqual([]);
  expect(pageErrors, `page errors: ${pageErrors.join("; ")}`).toEqual([]);
}

export async function assertCanonicalScreen(
  page: Page,
  opts: {
    viewport: number;
    consoleErrors: string[];
    pageErrors: string[];
  },
): Promise<StructuralMetrics> {
  const metrics = await assertNoHorizontalOverflow(page, opts.viewport);
  expectNoConsoleOrPageErrors(opts.consoleErrors, opts.pageErrors);
  return metrics;
}
