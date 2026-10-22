import { expect, type Locator, type Page } from "@playwright/test";

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
      // The sign-in preview is a miniature of the panel at a reduced scale: it clips by
      // design, the way a thumbnail does, and the full panel is the page it previews.
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
        offenders.push(
          `${where} clips horizontally: shows ${el.clientWidth}px of ${el.scrollWidth}px`,
        );
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
    const theme = document.documentElement.dataset.theme ?? "";
    const systemDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    return {
      dark: theme === "dark" || (theme !== "light" && systemDark),
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

  // Two ladders, one contract (issue #378): the theme is resolved the way the
  // stylesheet resolves it (an explicit data-theme wins, otherwise the OS), and
  // every rung is asserted against the ladder that theme publishes.
  const want = seen.dark
    ? {
        canvas: "rgb(21, 24, 29)",
        muted: "rgb(255 255 255 / 66%)",
        faint: "rgb(255 255 255 / 58%)",
        line: "rgb(255 255 255 / 10%)",
        accent: "#7B96FF",
        rail: "#0A0B0D",
      }
    : {
        canvas: "rgb(255, 255, 255)",
        muted: "rgb(0 0 0 / 62%)",
        faint: "rgb(0 0 0 / 56%)",
        line: "rgb(0 0 0 / 10%)",
        accent: "#315EFF",
        rail: "#111111",
      };
  const ladder = seen.dark ? "dark" : "light";
  expect(seen.canvas, `the ${ladder} canvas must be the neutral one, not a tinted neutral`).toBe(
    want.canvas,
  );
  expect(seen.muted, `the ${ladder} muted rung`).toBe(want.muted);
  expect(seen.faint, `the ${ladder} faint rung`).toBe(want.faint);
  expect(seen.line, `the ${ladder} hairline rung`).toBe(want.line);
  expect(seen.accent, `the ${ladder} single accent`).toBe(want.accent);
  expect(seen.rail, `the ${ladder} inverted rail canvas`).toBe(want.rail);
  expect(
    seen.loadedFamilies,
    "the vendored typeface must actually load; declaring a family is not loading one",
  ).toContain("Inter");
}

/**
 * A bare neutral control must draw an edge the eye can actually see. The shared `.btn` paints
 * `background:var(--card)`, and `--card`/`--surface`/`--bg` are all `#FFFFFF`, so the defect this
 * helper exists for — `.btn` drawing `border:1px solid var(--card)` — is a WHITE hairline on the
 * WHITE surface: still `1px`, still not `transparent`, and completely invisible. A check that
 * only rejects `transparent` (and only reads the top side) passes straight through it, so this
 * pins the contract three ways:
 *
 *  - the `--line` token is normalised through a throwaway probe element before comparing: the
 *    authored token serialises as `rgb(0 0 0 / 10%)` while a computed `borderTopColor` never
 *    does, so raw token text must never be compared against a computed value;
 *  - all FOUR sides must equal that normalised colour at exactly `1px`, not just the top;
 *  - each side must DIFFER from the element's own computed `backgroundColor` — the assertion
 *    that actually catches white-on-white, whichever token produced it.
 *
 * Do NOT call this on `.btn.primary`: by design its border and background are BOTH
 * `var(--accent)`, so the differs-from-background rule would fail a correct element.
 *
 * The assertions are soft so one journey run inventories every broken call site instead of
 * stopping at the first button; the test still fails either way. The label names the control in
 * every message.
 */
export async function expectHairlineBorder(locator: Locator, label: string): Promise<void> {
  const edge = await locator.evaluate((el) => {
    const token = getComputedStyle(document.documentElement).getPropertyValue("--line").trim();
    // Resolve the token through a real border: reading it back from a computed style is the
    // only form that is safe to compare against another computed colour.
    const probe = document.createElement("span");
    probe.style.borderTop = `1px solid ${token}`;
    document.body.append(probe);
    const line = getComputedStyle(probe).borderTopColor;
    probe.remove();

    const style = getComputedStyle(el);
    return {
      line,
      background: style.backgroundColor,
      colors: {
        top: style.borderTopColor,
        right: style.borderRightColor,
        bottom: style.borderBottomColor,
        left: style.borderLeftColor,
      },
      widths: {
        top: style.borderTopWidth,
        right: style.borderRightWidth,
        bottom: style.borderBottomWidth,
        left: style.borderLeftWidth,
      },
    };
  });

  for (const side of ["top", "right", "bottom", "left"] as const) {
    expect.soft(edge.widths[side], `${label}: the ${side} edge must be a 1px hairline`).toBe("1px");
    expect
      .soft(edge.colors[side], `${label}: the ${side} edge must be the --line hairline`)
      .toBe(edge.line);
    expect
      .soft(
        edge.colors[side],
        `${label}: the ${side} edge must differ from the element's own background; a matching edge is invisible`,
      )
      .not.toBe(edge.background);
  }
}

/**
 * A keyboard user must be able to skip the sidebar, and assistive technology must be able to
 * say where the user is (#343, S5). Both are invisible until someone needs them, so they are
 * asserted by behaviour: the skip link is off-screen until it takes focus and then lands in
 * the viewport, and the sidebar link for the current section carries `aria-current="page"` —
 * exactly once per navigation, on the active link, never on an inactive sibling. Since #377
 * the mobile tab bar is a second navigation that may repeat the current section, so the
 * count is asserted per navigation, not per page.
 *
 * The auth screens ship the split shell: no sidebar, and no skip target, so the check returns
 * unless the sidebar is present.
 */
export async function expectAccessibleNavigation(page: Page, label: string): Promise<void> {
  if ((await page.locator(".sidebar").count()) === 0) return;

  const skip = page.locator("a.skip-link");
  await expect(skip, `${label}: the shell must ship a skip link`).toHaveCount(1);
  await expect(skip, `${label}: the skip link must target the main region`).toHaveAttribute(
    "href",
    "#main-content",
  );
  await expect(page.locator("main#main-content"), `${label}: main region`).toHaveCount(1);
  await expect(
    skip,
    `${label}: the skip link stays out of sight until it takes focus`,
  ).not.toBeInViewport();
  await skip.focus();
  const focusedBox = await skip.boundingBox();
  expect(focusedBox, `${label}: the skip link must be visible once focused`).not.toBeNull();
  expect(
    focusedBox?.height ?? 0,
    `${label}: a focused skip link must render a readable box`,
  ).toBeGreaterThan(8);
  await expect(skip, `${label}: a focused skip link must be on screen`).toBeInViewport();
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());

  for (const [nav, link] of [
    [".sidebar-nav", "a.sidebar-link"],
    [".tabbar", "a.tabbar-item"],
  ]) {
    const active = page.locator(`${nav} ${link}.active`);
    const activeCount = await active.count();
    expect(activeCount, `${label}: ${nav} marks at most one section active`).toBeLessThanOrEqual(1);
    for (let index = 0; index < activeCount; index += 1) {
      await expect(active.nth(index), `${label}: the active ${nav} link`).toHaveAttribute(
        "aria-current",
        "page",
      );
    }
    await expect(
      page.locator(`${nav} ${link}[aria-current="page"]`),
      `${label}: only the current section may carry aria-current in ${nav}`,
    ).toHaveCount(activeCount);
  }
}

/**
 * WCAG contrast measured from computed styles (issue #378): the text colour is
 * alpha-composited over the first painted ancestor background, so an alpha-black
 * or alpha-white rung is judged on the surface it actually lands on, not on the
 * page canvas. Returns the ratio and both resolved colours for the message.
 */
export async function measureContrast(
  locator: Locator,
): Promise<{ ratio: number; fg: string; bg: string }> {
  return locator.evaluate((el) => {
    const parse = (value: string): [number, number, number, number] => {
      const m = value.match(/rgba?\(([^)]+)\)/);
      if (!m) return [0, 0, 0, 1];
      const parts = m[1]
        .split(/[\s,/]+/)
        .filter(Boolean)
        .map(Number);
      const [r, g, b] = parts;
      let a = parts.length > 3 ? parts[3] : 1;
      if (m[1].includes("%") && parts.length > 3) a = parts[3] / 100;
      return [r, g, b, Number.isFinite(a) ? a : 1];
    };
    const over = (
      top: [number, number, number, number],
      under: [number, number, number],
    ): [number, number, number] => {
      const a = top[3];
      return [
        Math.round(top[0] * a + under[0] * (1 - a)),
        Math.round(top[1] * a + under[1] * (1 - a)),
        Math.round(top[2] * a + under[2] * (1 - a)),
      ];
    };
    let bg: [number, number, number] = [255, 255, 255];
    const chain: Element[] = [];
    for (let node: Element | null = el; node; node = node.parentElement) chain.unshift(node);
    for (const node of chain) {
      const c = parse(getComputedStyle(node).backgroundColor);
      if (c[3] > 0) bg = over(c, bg);
    }
    const fgRaw = parse(getComputedStyle(el).color);
    const fg = over(fgRaw, bg);
    const lum = ([r, g, b]: [number, number, number]) => {
      const f = (v: number) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const l1 = lum(fg);
    const l2 = lum(bg);
    const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
    return {
      ratio: Math.round(ratio * 100) / 100,
      fg: `rgb(${fg.join(", ")})`,
      bg: `rgb(${bg.join(", ")})`,
    };
  });
}

/**
 * The shared shell (#377) at both viewports, measured rather than looked at: on a wide
 * viewport the sidebar is on-canvas at one of its two widths (232px expanded, 72px collapsed),
 * the top bar is visible and the mobile tab bar is not; on a narrow viewport the sidebar is
 * off-canvas until opened, the compact top bar and the tab bar are visible, and the main
 * region starts right under the top bar instead of under a block of chrome.
 */
export async function expectShellChrome(
  page: Page,
  viewport: number,
  label: string,
): Promise<void> {
  if ((await page.locator(".sidebar").count()) === 0) return;
  const chrome = await page.evaluate(() => {
    const rect = (selector: string) => {
      const el = document.querySelector(selector);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right };
    };
    return {
      sidebar: rect(".sidebar"),
      topbar: rect(".topbar"),
      tabbar: rect(".tabbar"),
      main: rect("main#main-content"),
      tabbarDisplay: getComputedStyle(document.querySelector(".tabbar") as Element).display,
      collapsed: document.body.classList.contains("sidebar-collapsed"),
    };
  });
  expect(chrome.sidebar, `${label}: sidebar`).not.toBeNull();
  expect(chrome.topbar, `${label}: top bar`).not.toBeNull();
  expect(chrome.main, `${label}: main`).not.toBeNull();
  if (viewport <= 640) {
    expect(
      chrome.sidebar!.right,
      `${label}: the sidebar stays off-canvas until opened`,
    ).toBeLessThanOrEqual(0);
    expect(chrome.tabbarDisplay, `${label}: the tab bar is visible on a phone`).not.toBe("none");
    expect(chrome.topbar!.height, `${label}: compact top bar`).toBeLessThanOrEqual(48);
    expect(chrome.main!.y, `${label}: main starts under the compact top bar`).toBeLessThanOrEqual(
      chrome.topbar!.height + 1,
    );
  } else {
    expect(chrome.sidebar!.x, `${label}: the sidebar is on-canvas`).toBe(0);
    expect(chrome.sidebar!.width, `${label}: the sidebar is expanded (232) or collapsed (72)`).toBe(
      chrome.collapsed ? 72 : 232,
    );
    expect(chrome.topbar!.height, `${label}: top bar`).toBe(52);
    expect(chrome.tabbarDisplay, `${label}: no tab bar on a wide viewport`).toBe("none");
  }
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
  await expectAccessibleNavigation(page, `[${opts.label}] ${opts.url} as ${opts.role}`);
  await expectShellChrome(page, opts.viewport, `[${opts.label}] ${opts.url} as ${opts.role}`);

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
