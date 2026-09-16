// Ticket metrics (issue #123): the summary is always visible and carries no
// hide/show toggle. This script only synchronizes the "View metrics" link
// with the CURRENT list query: the summary sits outside the swapped
// #tickets-screen, so a list HTMX swap (search, pagination) changes the URL
// without re-rendering the link. The server sanitizes the return value on
// read; this only mirrors the visible list URL. Chart filters and the
// workload switch are server-driven HTMX swaps with no JavaScript.
(() => {
  function syncViewMetricsLink() {
    if (window.location.pathname !== "/tickets") return;
    const link = document.querySelector("a.ticket-metrics-link");
    if (!link) return;
    link.setAttribute(
      "href",
      "/tickets/metrics?return=" +
        encodeURIComponent(window.location.pathname + window.location.search),
    );
  }
  document.addEventListener("htmx:afterSwap", syncViewMetricsLink);
  document.addEventListener("htmx:pushedIntoHistory", syncViewMetricsLink);
  document.addEventListener("htmx:historyRestore", syncViewMetricsLink);
  syncViewMetricsLink();
})();
