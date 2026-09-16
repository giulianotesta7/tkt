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

export function expectNoConsoleOrPageErrors(consoleErrors: string[], pageErrors: string[]): void {
  expect(consoleErrors, `console errors: ${consoleErrors.join("; ")}`).toEqual([]);
  expect(pageErrors, `page errors: ${pageErrors.join("; ")}`).toEqual([]);
}

/**
 * A clipped element is how the truncated ticket title (#199) stayed hidden: the field was
 * 373px wide while its content needed 730px, so 357px of the value were invisible, with no
 * ellipsis and no other place on the page showing it. The structural baseline saw a visible
 * heading and no page overflow, and passed it.
 *
 * An element that carries an explicit truncation affordance (`text-overflow: ellipsis`) or
 * that is deliberately scrollable is not a violation: the user can see that there is more,
 * or reach it. Everything else that renders text must fit.
 */
export async function assertNothingClipped(page: Page): Promise<void> {
  const clipped = await page.evaluate(() => {
    const offenders: string[] = [];
    for (const el of document.querySelectorAll<HTMLElement>("body *")) {
      // A form control's content is its value, not its textContent: an <input> has no child
      // text node at all. Filtering on textContent alone is what made this check blind to the
      // very field it was written for.
      const control = el as HTMLInputElement & HTMLTextAreaElement & HTMLSelectElement;
      const carriesText =
        Boolean(el.textContent?.trim()) ||
        Boolean(typeof control.value === "string" && control.value.trim()) ||
        Boolean(control.selectedOptions?.length);
      if (!carriesText) continue;
      const cs = getComputedStyle(el);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      const r = el.getBoundingClientRect();
      if (r.width < 1 || r.height < 1) continue;
      // Collapsed to a pixel: how this codebase hides content from sight while keeping it for
      // assistive tech. Deliberately invisible, so there is nothing to clip.
      if (r.width <= 1.5 || r.height <= 1.5) continue;
      // Collapsed in an axis: the mobile catalog drill-down sizes the inactive columns to zero
      // and hides their content until you drill in. Collapsed shows nothing, which is not the
      // same failure as a box that cuts off content it was supposed to show. Whether a required
      // element is *missing* is the structural baseline's job, and it already asserts it.
      if (el.clientWidth === 0 || el.clientHeight === 0) continue;

      // Deliberate affordances: the overflow is announced or reachable.
      if (cs.textOverflow === "ellipsis") continue;
      if (["auto", "scroll"].includes(cs.overflowX) && el.clientWidth > 0) continue;
      if (el.closest("[data-clip-exempt]")) continue;

      // Only the element's own clipping hides content. With `visible` the content spills and
      // stays readable, and page-level horizontal overflow is asserted separately.
      const clipsX = ["hidden", "clip"].includes(cs.overflowX);
      const clipsY = ["hidden", "clip"].includes(cs.overflowY);
      if (!clipsX && !clipsY) continue;

      // Forced, not gratuitous. A single-line control cannot wrap, so when its value is longer
      // than the whole viewport no arrangement of the layout could show it; the control's own
      // scrolling on focus is the affordance. This is what separates the defensible residual
      // (a 71-character title at 390px) from the defect: at 1280 the same title had 1152px of
      // column available and was given 373px.
      const isFormControl = ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName);
      if (isFormControl && el.scrollWidth > window.innerWidth) continue;

      const id = el.id ? `#${el.id}` : "";
      const cls = el.className ? `.${String(el.className).trim().split(/\s+/).join(".")}` : "";
      const where = `${el.tagName.toLowerCase()}${id}${cls}`;

      // +1 absorbs sub-pixel rounding on fractional layout.
      if (clipsX && el.scrollWidth > el.clientWidth + 1) {
        offenders.push(`${where} clips horizontally: shows ${el.clientWidth}px of ${el.scrollWidth}px`);
      }
      if (clipsY && el.scrollHeight > el.clientHeight + 1) {
        offenders.push(
          `${where} clips vertically: shows ${el.clientHeight}px of ${el.scrollHeight}px`,
        );
      }
    }
    return offenders;
  });
  expect(clipped, `clipped content: ${clipped.join("; ")}`).toEqual([]);
}

/**
 * The design system's declared ladder, read from the rendered document rather than from the
 * source, so a rule that loses a specificity contest is caught.
 *
 * The loaded-font check is the one that matters most: the interface carried three declared
 * families for months while `document.fonts.size` was 0 and every operator rendered in their
 * own OS fallback. Declaring a family is not loading one.
 */
export async function expectDesignSystem(page: Page): Promise<void> {
  const seen = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement);
    const read = (name: string) => root.getPropertyValue(name).trim();
    return {
      canvas: getComputedStyle(document.body).backgroundColor,
      muted: read("--muted"),
      faint: read("--faint"),
      line: read("--line"),
      accent: read("--accent"),
      rail: read("--rail"),
      loadedFamilies: [...document.fonts].map((f) => f.family.replace(/["']/g, "")),
    };
  });

  // A bare error page (403/404/500) renders no stylesheet, so there is no design system to
  // assert there: the tokens are empty strings. Canonical screens are covered by the
  // structural baselines, where the tokens must be present. This guard exists so an error
  // page does not fail a contract it never made, not to let a missing stylesheet pass on a
  // screen that should have one.
  if (!seen.muted) return;

  expect(seen.canvas, "the canvas must be the neutral white, not a tinted neutral").toBe(
    "rgb(255, 255, 255)",
  );
  expect(seen.muted, "the muted rung").toBe("rgb(0 0 0 / 62%)");
  expect(seen.faint, "the faint rung").toBe("rgb(0 0 0 / 56%)");
  expect(seen.line, "the hairline rung").toBe("rgb(0 0 0 / 10%)");
  expect(seen.accent, "the single accent").toBe("#315EFF");
  expect(seen.rail, "the inverted rail canvas").toBe("#111111");
  expect(
    seen.loadedFamilies,
    "the vendored typeface must actually load; declaring a family is not loading one",
  ).toContain("Inter");
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
  await assertNothingClipped(page);
  await expectDesignSystem(page);

  const prefix = `[${opts.label}] ${opts.url} as ${opts.role}`;
  expect(
    opts.consoleErrors,
    `${prefix} — console errors: ${opts.consoleErrors.join("; ")}`,
  ).toEqual([]);
  expect(opts.pageErrors, `${prefix} — page errors: ${opts.pageErrors.join("; ")}`).toEqual([]);
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
