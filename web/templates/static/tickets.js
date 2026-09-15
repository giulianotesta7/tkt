/**
 * Ticket role-view search sync and claim-conflict gate (issue #122).
 *
 * History sync: the agent/user role search input is hx-preserved, so live
 * typing keeps focus, value, and caret across swaps. On a history restore
 * (Back/Forward), htmx moves the preserved input into the restored screen
 * with its live value, which can disagree with the restored URL's q. This
 * syncs the input to the URL after each restore without firing events (no
 * extra search loop) and without stealing focus; when the input is focused,
 * the caret moves to the end of the synced value so it stays usable.
 *
 * Ticket-scoped by design: base.html loads this script only on ticket
 * screens for roles that render the role search (agent/user). Admin/root
 * keep the server-rendered filter bar. The listeners are registered once per
 * page load (this external head script never re-runs across HTMX swaps).
 *
 * Claim-conflict gate: the server answers a list-origin claim that lost
 * availability/race/eligibility with 422 plus the refreshed
 * #agent-ticket-list fragment and exact retarget/reswap headers. htmx treats
 * every 4xx as an error and skips the swap, so this admits ONLY that exact
 * claim-conflict response — never a generic 4xx and never an unrelated 422
 * (detail errors carry no retarget header and non-claim origins fail the
 * section check). History sync stays unchanged.
 */
(() => {
  const CLAIM_LIST_SECTION =
    'section[aria-labelledby="claimable-tickets-title"]';

  function syncSearchFromLocation() {
    const input = document.getElementById("role-ticket-search");
    if (!input) return;
    let q = "";
    try {
      q = new URL(window.location.href).searchParams.get("q") ?? "";
    } catch {
      return;
    }
    if (input.value === q) return;
    input.value = q;
    if (document.activeElement === input) {
      try {
        input.setSelectionRange(q.length, q.length);
      } catch {
        /* selection API unavailable for this input state */
      }
    }
  }

  document.body.addEventListener("htmx:historyRestore", syncSearchFromLocation);

  // Admit ONLY the exact claim-conflict 422: the response must retarget the
  // agent queue wrapper with outerHTML, and the request must have originated
  // from the claim list section. Everything else — 403/404/409, any other
  // 4xx, and 422s without the exact headers or origin — stays a normal htmx
  // error with no swap.
  document.body.addEventListener("htmx:beforeSwap", (event) => {
    const detail = event.detail;
    const xhr = detail.xhr;
    if (
      xhr.status !== 422 ||
      xhr.getResponseHeader("HX-Retarget") !== "#agent-ticket-list" ||
      xhr.getResponseHeader("HX-Reswap") !== "outerHTML"
    ) return;
    const elt = detail.requestConfig?.elt;
    if (!elt?.closest?.(CLAIM_LIST_SECTION)) return;
    detail.shouldSwap = true;
    detail.isError = false;
  });
})();
