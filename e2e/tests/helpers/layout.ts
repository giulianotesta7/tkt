import { expect, type Page } from "@playwright/test";

/**
 * Structural baseline helper — shared across every canonical screen.
 *
 * Asserts:
 *  - no document-level horizontal overflow (htmlScroll <= viewport)
 *  - zero console errors, zero page errors, zero failed own-requests, zero own 5xx responses
 *
 * Viewport must already be set before navigation.
 *
 * Observability rule: the app is loopback-only (127.0.0.1 / localhost).
 * We ignore ONLY non-loopback/external origins when checking failed requests
 * and 5xx responses. In practice nothing external is loaded, so any failure
 * is a real app regression.
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

function isLoopbackUrl(url: string): boolean {
  try {
    const u = new URL(url);
    return u.hostname === "127.0.0.1" || u.hostname === "localhost" || u.hostname === "::1";
  } catch {
    return false;
  }
}

export function collectObservability(page: Page): {
  consoleErrors: string[];
  pageErrors: string[];
  failedRequests: string[];
  failedResponses: string[];
} {
  const consoleErrors: string[] = [];
  const pageErrors: string[] = [];
  const failedRequests: string[] = [];
  const failedResponses: string[] = [];

  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("pageerror", (err) => pageErrors.push(String(err)));
  page.on("requestfailed", (req) => {
    const url = req.url();
    if (!isLoopbackUrl(url)) return; // ignore external origins
    const failure = req.failure();
    failedRequests.push(`${req.method()} ${url} :: ${failure?.errorText ?? "unknown"}`);
  });
  page.on("response", (resp) => {
    const status = resp.status();
    if (status < 500) return;
    const url = resp.url();
    if (!isLoopbackUrl(url)) return; // ignore external 5xx (e.g. nothing in practice)
    failedResponses.push(`${status} ${resp.request().method()} ${url}`);
  });

  return { consoleErrors, pageErrors, failedRequests, failedResponses };
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
    label: string;
    url: string;
    role: string;
    consoleErrors: string[];
    pageErrors: string[];
    failedRequests: string[];
    failedResponses: string[];
  },
): Promise<StructuralMetrics> {
  const metrics = await assertNoHorizontalOverflow(page, opts.viewport);

  const prefix = `[${opts.label}] ${opts.url} as ${opts.role}`;
  expect(
    opts.consoleErrors,
    `${prefix} — console errors: ${opts.consoleErrors.join("; ")}`,
  ).toEqual([]);
  expect(
    opts.pageErrors,
    `${prefix} — page errors: ${opts.pageErrors.join("; ")}`,
  ).toEqual([]);
  expect(
    opts.failedRequests,
    `${prefix} — failed own-requests: ${opts.failedRequests.join("; ")}`,
  ).toEqual([]);
  expect(
    opts.failedResponses,
    `${prefix} — own 5xx responses: ${opts.failedResponses.join("; ")}`,
  ).toEqual([]);

  return metrics;
}
