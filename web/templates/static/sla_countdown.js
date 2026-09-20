/**
 * SLA live countdown (issue #211, PR 6).
 *
 * The ticket detail page renders each PENDING milestone's due instant as a
 * server-truthful <time datetime> (the ABSOLUTE instant) and stamps the SLA
 * panel with the instant the projection was taken (data-server-now). The
 * server never computes a live countdown, so once the page is loaded the
 * browser's clock is the only source of "now"; this script derives the
 * server/client offset from data-server-now so a client whose clock is wrong
 * still shows the correct remaining time instead of trusting itself.
 *
 * ONE interval drives every countdown element on the page. Each tick
 * recomputes the remaining time from the absolute due instant — it never
 * decrements a counter — so a throttled or suspended tab cannot drift.
 *
 * Accessibility: the fast, per-second value is written into a ticker span
 * that is marked aria-hidden; the coarse, server-computed text next to it
 * stays the accessible reading and is refreshed far less often (once a
 * minute). The visible absolute instant never leaves the markup: it stays in
 * the <time datetime> attribute.
 *
 * Loaded only by base.html on the ticket detail page
 * (pageData.SLACountdownAssets). Like the other external head scripts it
 * registers its listeners once per page load: the detail panel is re-rendered
 * through HTMX outerHTML swaps, so an htmx:afterSwap listener re-ticks the
 * freshly swapped elements rather than starting a second interval.
 */
(() => {
  const COUNTDOWN_SELECTOR = "[data-sla-countdown]";
  const SERVER_NOW_SELECTOR = "[data-server-now]";
  const TICKER_SELECTOR = ".sla-countdown-ticker";
  const COARSE_SELECTOR = ".sla-countdown-coarse";
  const TICK_MS = 1000;
  const COARSE_MS = 60000;
  const OVERDUE = "overdue";

  // parseInstant returns epoch milliseconds, or null for a missing or
  // malformed instant. Callers skip such an element instead of throwing.
  const parseInstant = (value) => {
    if (typeof value !== "string" || value === "") return null;
    const ms = Date.parse(value);
    return Number.isNaN(ms) ? null : ms;
  };

  // duration mirrors the server's compact h/m/s copy (slaDurationLabel).
  const duration = (totalSeconds) => {
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    if (hours > 0 && seconds > 0) return `${hours}h ${minutes}m ${seconds}s`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    if (minutes > 0 && seconds > 0) return `${minutes}m ${seconds}s`;
    if (minutes > 0) return `${minutes}m`;
    return `${seconds}s`;
  };

  // preciseText is the per-second reading; a passed instant reads a stable
  // "overdue" rather than negative time.
  // preciseText is the fast reading. It keeps the SAME shape as the server's
  // initial value ("in 59m 37s" / "30m overdue") so the number does not jump
  // format when the script takes over.
  const preciseText = (remainingMs) => {
    if (remainingMs <= 0) {
      return `${duration(Math.floor(Math.abs(remainingMs) / 1000))} ${OVERDUE}`;
    }
    return `in ${duration(Math.floor(remainingMs / 1000))}`;
  };

  // coarseText is the slower, accessible reading. It rounds to whole minutes
  // so it genuinely changes far less often than the ticker.
  const coarseText = (remainingMs) => {
    const totalSeconds = Math.floor(Math.abs(remainingMs) / 1000);
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    let label;
    if (hours > 0) label = `${hours}h ${minutes}m`;
    else if (minutes > 0) label = `${minutes}m`;
    else label = "<1m";
    return remainingMs <= 0 ? `${label} ${OVERDUE}` : `in ${label}`;
  };

  // The offset is derived once per panel, not per tick: recomputing it every
  // tick would hold "now" forever at the server instant. It is refreshed only
  // when an HTMX swap replaces the panel with a new stamped instant.
  let anchorValue;
  let skewMs = 0;
  const refreshSkew = () => {
    const anchor = document.querySelector(SERVER_NOW_SELECTOR);
    const raw = anchor ? anchor.getAttribute("data-server-now") : null;
    if (raw === anchorValue) return;
    anchorValue = raw;
    const server = parseInstant(raw);
    skewMs = server === null ? 0 : server - Date.now();
  };

  let lastCoarseAt = 0;

  const tick = () => {
    // A hidden document does no rendering work; the restore tick catches up
    // from the absolute instant.
    if (document.hidden) return;
    const elements = document.querySelectorAll(COUNTDOWN_SELECTOR);
    if (elements.length === 0) return;
    refreshSkew();
    const now = Date.now() + skewMs;
    const writeCoarse = now - lastCoarseAt >= COARSE_MS;
    for (const element of elements) {
      const due = parseInstant(element.getAttribute("datetime"));
      if (due === null) continue;
      const remaining = due - now;
      const ticker = element.querySelector(TICKER_SELECTOR) || element;
      ticker.textContent = preciseText(remaining);
      ticker.setAttribute("aria-hidden", "true");
      if (writeCoarse) {
        const coarse = element.querySelector(COARSE_SELECTOR);
        if (coarse) coarse.textContent = coarseText(remaining);
      }
    }
    if (writeCoarse) lastCoarseAt = now;
  };

  const start = () => {
    // No countdown element on this page: no work, and no interval.
    if (document.querySelectorAll(COUNTDOWN_SELECTOR).length === 0) return;
    tick();
    window.setInterval(tick, TICK_MS);
    document.addEventListener("visibilitychange", () => {
      if (!document.hidden) tick();
    });
    // The interval and the listeners survive a swap; the swapped elements
    // only need an immediate recompute.
    document.addEventListener("htmx:afterSwap", tick);
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
