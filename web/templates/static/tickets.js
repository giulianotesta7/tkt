document.addEventListener("htmx:beforeSwap", (event) => {
  const detail = event.detail;
  const xhr = detail.xhr;
  const requestElement = detail.requestConfig?.elt;
  if (
    xhr.status !== 422 ||
    xhr.getResponseHeader("HX-Retarget") !== "#agent-ticket-list" ||
    xhr.getResponseHeader("HX-Reswap") !== "outerHTML" ||
    !requestElement?.closest('section[aria-labelledby="claimable-tickets-title"]')
  ) return;

  detail.shouldSwap = true;
  detail.isError = false;
});
