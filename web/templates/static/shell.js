/*
 * Shell behaviour (issue #377): the collapsible sidebar, the user menu and
 * the mobile drawer. Everything here is progressive: the server renders the
 * stored sidebar state on <body> (class sidebar-collapsed) so the page never
 * flashes, and this script only flips it, persists the choice and wires the
 * keyboard. Nothing below reads or writes the page content.
 */
(() => {
  const body = document.body;
  const sidebar = document.getElementById("sidebar");
  if (!sidebar) return;

  const collapseButton = sidebar.querySelector("[data-sidebar-collapse]");
  const collapseLabel = collapseButton ? collapseButton.querySelector(".sidebar-label") : null;
  const userToggle = sidebar.querySelector("[data-user-menu-toggle]");
  const userMenu = document.getElementById("user-menu");
  const scrim = document.querySelector(".sidebar-scrim");
  const openers = [...document.querySelectorAll("[data-sidebar-open]")];
  const closers = [...document.querySelectorAll("[data-sidebar-close]")];
  const mobile = window.matchMedia("(max-width: 640px)");

  // ---- collapse (desktop) ----
  const isCollapsed = () => body.classList.contains("sidebar-collapsed");

  const renderCollapse = () => {
    if (!collapseButton) return;
    const collapsed = isCollapsed();
    collapseButton.setAttribute("aria-pressed", collapsed ? "true" : "false");
    const label = collapsed
      ? collapseButton.dataset.labelExpand
      : collapseButton.dataset.labelCollapse;
    if (collapseLabel) collapseLabel.textContent = label;
    collapseButton.setAttribute("data-tip", label);
  };

  const persistCollapse = (collapsed) => {
    const form = new URLSearchParams({ sidebar: collapsed ? "collapsed" : "expanded" });
    fetch("/preferences/sidebar", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: form,
      credentials: "same-origin",
    }).catch(() => {});
  };

  const toggleCollapse = () => {
    body.classList.toggle("sidebar-collapsed");
    closeUserMenu();
    renderCollapse();
    persistCollapse(isCollapsed());
  };

  if (collapseButton) {
    collapseButton.addEventListener("click", toggleCollapse);
    renderCollapse();
  }

  // ---- user menu ----
  const closeUserMenu = () => {
    if (!userMenu || userMenu.hidden) return;
    userMenu.hidden = true;
    if (userToggle) userToggle.setAttribute("aria-expanded", "false");
  };

  const openUserMenu = () => {
    if (!userMenu) return;
    userMenu.hidden = false;
    if (userToggle) userToggle.setAttribute("aria-expanded", "true");
    const first = userMenu.querySelector("[role=menuitem]");
    if (first) first.focus();
  };

  if (userToggle && userMenu) {
    userToggle.addEventListener("click", () => {
      if (userMenu.hidden) openUserMenu();
      else closeUserMenu();
    });
    document.addEventListener("click", (event) => {
      if (userMenu.hidden) return;
      if (userMenu.contains(event.target) || userToggle.contains(event.target)) return;
      closeUserMenu();
    });
  }

  // ---- mobile drawer ----
  const isDrawerOpen = () => body.classList.contains("sidebar-open");

  const setOpenerState = (open) => {
    for (const opener of openers) opener.setAttribute("aria-expanded", open ? "true" : "false");
  };

  const openDrawer = () => {
    body.classList.add("sidebar-open");
    if (scrim) scrim.hidden = false;
    setOpenerState(true);
    const first = sidebar.querySelector(".sidebar-close, .sidebar-link");
    if (first) first.focus();
  };

  const closeDrawer = (restoreFocus) => {
    if (!isDrawerOpen()) return;
    body.classList.remove("sidebar-open");
    if (scrim) scrim.hidden = true;
    setOpenerState(false);
    closeUserMenu();
    if (restoreFocus && openers[0]) openers[0].focus();
  };

  for (const opener of openers) opener.addEventListener("click", openDrawer);
  for (const closer of closers) closer.addEventListener("click", () => closeDrawer(true));
  mobile.addEventListener("change", () => {
    if (!mobile.matches) closeDrawer(false);
  });

  // ---- keyboard ----
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      if (userMenu && !userMenu.hidden) {
        closeUserMenu();
        if (userToggle) userToggle.focus();
        return;
      }
      if (isDrawerOpen()) closeDrawer(true);
      return;
    }
    if (event.key !== "[" || event.altKey || event.ctrlKey || event.metaKey) return;
    const target = event.target;
    if (
      target instanceof HTMLElement &&
      (target.isContentEditable || target.matches("input, textarea, select, [contenteditable]"))
    ) {
      return;
    }
    if (mobile.matches) return;
    event.preventDefault();
    toggleCollapse();
  });
})();
