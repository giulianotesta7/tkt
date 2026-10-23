// Ticket metrics (issue #123, view switch issue #393): the summary is always
// visible and carries no hide/show toggle. This script keeps the "View metrics"
// link in sync with the CURRENT list query — the summary sits outside the
// swapped #tickets-screen, so a list HTMX swap (search, pagination) changes the
// URL without re-rendering the link — and flips each dashboard section between
// its chart and its table. The tables are server-rendered next to their charts,
// so their values never depend on this file; the stylesheet reveals the table
// when scripting is off. Chart filters, the workload switch and the attainment
// grouping stay server-driven HTMX swaps with no JavaScript.
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

  // setView flips one section: the pressed button names the visible panel, and
  // every other panel of that section is hidden, so the chart and the table
  // never render at once.
  function setView(section, view) {
    section.querySelectorAll("[data-metrics-view]").forEach((button) => {
      button.setAttribute("aria-pressed", String(button.dataset.metricsView === view));
    });
    section.querySelectorAll("[data-metrics-panel]").forEach((panel) => {
      panel.hidden = panel.dataset.metricsPanel !== view;
    });
  }

  // Delegated from the document because an HTMX filter swap replaces the whole
  // dashboard fragment; a listener bound to the old nodes would go with them.
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-metrics-view]");
    if (!button) return;
    const section = button.closest("[data-metrics-section]");
    if (!section) return;
    setView(section, button.dataset.metricsView);
  });

  document.addEventListener("htmx:afterSwap", syncViewMetricsLink);
  document.addEventListener("htmx:pushedIntoHistory", syncViewMetricsLink);
  document.addEventListener("htmx:historyRestore", syncViewMetricsLink);
  syncViewMetricsLink();
})();
