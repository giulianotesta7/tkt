/*
 * Staff queue row navigation (issue #382). The row's single link is the
 * title; this makes the rest of the row open the same ticket on a plain
 * click, while leaving links, buttons, form controls, text selections and
 * modified clicks (new tab, context menu) to the browser. It survives HTMX
 * swaps of #tickets-screen because it listens on the document.
 */
(() => {
  // Under 640px the four selects fold behind the Filters disclosure; the
  // server renders it open so a wide screen never hides a control. The
  // form's own requests swap only #queue-results (plus the tabs out of
  // band), so a change made while a request is in flight is never reset.
  const narrow = window.matchMedia("(max-width: 640px)");
  const foldFilters = () => {
    for (const details of document.querySelectorAll("details.queue-filters")) {
      if (narrow.matches && !details.dataset.userOpened) details.open = false;
    }
  };
  document.addEventListener(
    "toggle",
    (event) => {
      const details = event.target;
      if (
        details instanceof HTMLDetailsElement &&
        details.classList.contains("queue-filters") &&
        details.open
      ) {
        details.dataset.userOpened = "1";
      }
    },
    true,
  );
  foldFilters();
  document.addEventListener("htmx:afterSwap", foldFilters);

  document.addEventListener("click", (event) => {
    if (event.defaultPrevented || event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    if (target.closest("a, button, input, select, textarea, label, summary, details")) return;
    const row = target.closest("tr.queue-row[data-href]");
    if (!row) return;
    const selection = window.getSelection();
    if (selection && selection.type === "Range" && String(selection).length > 0) return;
    const href = row.getAttribute("data-href");
    if (href) window.location.assign(href);
  });
})();
