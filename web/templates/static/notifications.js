/**
 * Notification center drawer (issue #344, slice B4).
 *
 * The count, the panel markup and every sentence in it come from the server;
 * this script only moves focus. The bell opens the panel through htmx, so the
 * drawer is swapped into #notification-panel-host and focus has to follow it
 * into the dialog. Closing it — the close button, the backdrop, or Escape —
 * empties the host and returns focus to the bell, and while it is open the page
 * behind it is inert, the same convention the users drawer uses.
 *
 * Nothing here writes copy or fetches anything: polling lives in the rail's
 * hx-trigger, and the copy lives in the catalogues.
 */
(() => {
  const host = () => document.getElementById("notification-panel-host");
  const bell = () => document.getElementById("notification-bell-link");
  const panel = () => {
    const target = host();
    return target ? target.querySelector(".notifications-panel") : null;
  };
  const isOpen = () => panel() !== null;

  function setBackgroundInert(inert) {
    for (const selector of [".rail", ".main"]) {
      const el = document.querySelector(selector);
      if (el) el.inert = inert;
    }
  }

  function close() {
    const target = host();
    if (!target || !isOpen()) return;
    target.innerHTML = "";
    setBackgroundInert(false);
    const trigger = bell();
    if (trigger) trigger.focus();
  }

  document.addEventListener("htmx:afterSwap", (event) => {
    const target = host();
    if (!target || event.target !== target || !isOpen()) return;
    setBackgroundInert(true);
    panel().focus();
  });

  document.addEventListener("click", (event) => {
    if (event.target.closest("[data-notifications-close]")) close();
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && isOpen()) close();
  });
})();
