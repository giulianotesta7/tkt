(() => {
  const KEY = "tktCategories";
  let opener = null;
  let openerKey = null;
  let busy = false;
  let locked = false;
  let baseline = null;
  let baselineIdentity = "";
  let pendingDeskValues = null;
  let discard = false;
  let dialogReturnFocus = null;
  let lastDrawerFocus = null;
  let drawerURL = "";

  const root = () => document.querySelector(".categories-root");
  const background = () => document.getElementById("categories-background");
  const drawer = () => document.querySelector(".category-drawer");
  const focusables = (el) =>
    [
      ...el.querySelectorAll(
        "a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])",
      ),
    ].filter((item) => item.offsetParent !== null);
  const same = (a, b) => a && b && a[KEY] === b[KEY];

  // The overflow menus carry role="menu" semantics, so their state has to be
  // managed as one menu at a time: a single trigger owns aria-expanded, opening
  // one dismisses the rest, and the keyboard walks the items in the same order a
  // native menu does. The panel lifecycle also clears them, so a menu can never
  // survive behind the drawer.
  const menuItems = (menu) => [...menu.querySelectorAll("[role='menuitem']")];
  const menuButtonFor = (menu) =>
    menu?.id ? document.querySelector(`[aria-controls="${CSS.escape(menu.id)}"]`) : null;
  const menuFor = (button) => document.getElementById(button?.getAttribute("aria-controls"));

  function closeMenu(menu, restoreFocus = false) {
    if (!menu || menu.hidden) return;
    menu.hidden = true;
    const button = menuButtonFor(menu);
    if (button) button.setAttribute("aria-expanded", "false");
    if (restoreFocus && button) button.focus();
  }

  function closeMenus(except = null) {
    document.querySelectorAll(".category-overflow-menu:not([hidden])").forEach((menu) => {
      if (menu !== except) closeMenu(menu);
    });
  }

  function openMenu(button, menu) {
    if (!button || !menu) return false;
    closeMenus(menu);
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    const buttonBox = button.getBoundingClientRect();
    menu.classList.toggle("up", buttonBox.bottom + menu.offsetHeight > window.innerHeight);
    return true;
  }

  function moveMenuFocus(menu, delta) {
    const items = menuItems(menu);
    if (!items.length) return;
    const current = items.indexOf(document.activeElement);
    if (current === -1) {
      (delta < 0 ? items.at(-1) : items[0]).focus();
      return;
    }
    items[(current + delta + items.length) % items.length].focus();
  }

  function handleMenuKeydown(event) {
    const menu = event.target.closest?.(".category-overflow-menu:not([hidden])");
    if (menu) {
      if (event.key === "Escape") {
        event.preventDefault();
        closeMenu(menu, true);
        return;
      }
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        if (event.key === "ArrowDown") moveMenuFocus(menu, 1);
        else if (event.key === "ArrowUp") moveMenuFocus(menu, -1);
        else if (event.key === "Home") menuItems(menu)[0]?.focus();
        else menuItems(menu).at(-1)?.focus();
        return;
      }
      // Enter already activates an anchor; Space does not, so a role=menuitem
      // link needs the explicit activation the role promises.
      if (event.key === " " && event.target.matches("a[role='menuitem']")) {
        event.preventDefault();
        event.target.click();
      }
      return;
    }
    const trigger = event.target.closest?.("[data-category-menu]");
    if (trigger) {
      const controlled = menuFor(trigger);
      if (!controlled) return;
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        if (openMenu(trigger, controlled)) {
          moveMenuFocus(controlled, event.key === "ArrowDown" ? 1 : -1);
        }
        return;
      }
      if (event.key === "Escape" && !controlled.hidden) {
        event.preventDefault();
        closeMenu(controlled, true);
      }
      return;
    }
    if (event.key === "Escape") {
      const open = document.querySelector(".category-overflow-menu:not([hidden])");
      if (open) {
        event.preventDefault();
        closeMenu(open, true);
      }
    }
  }
  const listURL = () => {
    const path = location.pathname;
    if (path === "/categories") return path + location.search;
    return "/categories";
  };
  const drawerState = (panel, url = drawerURL || location.pathname + location.search) => ({
    [KEY]: 1,
    kind: "drawer",
    closeURL: panel.dataset.closeUrl || listURL(),
    drawerURL: url,
    launcherKey: openerKey || "",
  });
  const baseState = (url) => ({ [KEY]: 1, kind: "base", url });
  const categoryForm = () => document.getElementById("category-drawer-form");
  const drawerIdentity = (panel) => `${panel?.dataset.kind || ""}:${panel?.dataset.id || ""}`;
  const drawerValues = (panel = drawer(), form = categoryForm()) => {
    const values = {
      name: form?.elements.name?.value || "",
      description: form?.elements.description?.value || "",
    };
    if (panel?.dataset.kind === "category") {
      values.department_id = form?.elements.department_id?.value || "";
      values.desk_id = form?.elements.desk_id?.value || "";
    } else if (panel?.dataset.kind === "desk") {
      values.department_id = form?.elements.department_id?.value || "";
    }
    return values;
  };
  const dirty = () => {
    const panel = drawer();
    if (!panel) return false;
    if (panel.dataset.serverError === "true") return true;
    const values = drawerValues(panel);
    return !!baseline && Object.keys(values).some((key) => values[key] !== baseline[key]);
  };

  function captureBaseline(panel) {
    if (!panel || panel.dataset.serverError === "true") return;
    const identity = drawerIdentity(panel);
    if (baseline && baselineIdentity === identity) return;
    baseline = drawerValues(panel);
    baselineIdentity = identity;
  }

  function dialogFocusFallback(panel) {
    return (
      panel.querySelector(
        "input:not([disabled]),select:not([disabled]),textarea:not([disabled]),button:not([disabled])",
      ) || panel
    );
  }

  function validDialogReturnFocus(panel, target) {
    return (
      !!panel && target?.isConnected && panel.contains(target) && focusables(panel).includes(target)
    );
  }

  function showDialog(trigger) {
    const panel = drawer();
    const dialog = panel?.querySelector("#category-dirty-dialog");
    if (!dialog) return;
    const active = document.activeElement;
    dialogReturnFocus = validDialogReturnFocus(panel, lastDrawerFocus)
      ? lastDrawerFocus
      : validDialogReturnFocus(panel, active)
        ? active
        : validDialogReturnFocus(panel, trigger)
          ? trigger
          : dialogFocusFallback(panel);
    if (dialog.showModal) dialog.showModal();
    else dialog.setAttribute("open", "");
    dialog.querySelector("[data-category-stay]")?.focus();
  }

  function closeDialog(restoreFocus = false) {
    const panel = drawer();
    const dialog = panel?.querySelector("#category-dirty-dialog");
    if (dialog?.open) dialog.close();
    else dialog?.removeAttribute("open");
    const target = dialogReturnFocus;
    dialogReturnFocus = null;
    if (restoreFocus && panel) {
      window.requestAnimationFrame(() => {
        (validDialogReturnFocus(panel, target) ? target : dialogFocusFallback(panel)).focus();
      });
    }
  }

  function lockBackground(value) {
    const page = background();
    const rail = document.querySelector(".rail");
    if (value && !locked) {
      document.body.dataset.categoryOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      locked = true;
    }
    if (page) page.inert = value;
    if (rail) rail.inert = value;
    if (!value && locked) {
      document.body.style.overflow = document.body.dataset.categoryOverflow || "";
      delete document.body.dataset.categoryOverflow;
      locked = false;
    }
  }

  function restoreFocus() {
    const target = opener?.isConnected
      ? opener
      : openerKey
        ? document.querySelector(`[data-focus-key="${CSS.escape(openerKey)}"]`)
        : null;
    if (!target) return;
    target.focus();
    if (document.activeElement === target) {
      opener = null;
      openerKey = null;
    }
  }

  // Accessible fallback for a launcher that vanished during an update. Prefer the
  // drawer's own level control ("New department" / "New desk" / "New category"),
  // which is a real focusable control with a visible accessible name. If even that
  // is gone, focus the level's labelled region (headings "Departments" / "Desks" /
  // "Categories") as a programmatic target. Focus never falls silently to <body>.
  function closeFallbackFocus(panel) {
    const level = {
      department: ".category-level-departments",
      desk: ".category-level-desks",
      category: ".category-level-categories",
    }[panel?.dataset.kind];
    const scope = (level && root()?.querySelector(level)) || root();
    const launcher = scope?.querySelector(".category-level-action.category-drawer-launcher");
    if (launcher) return launcher;
    // The level section IS the labelled region (e.g.
    // <section class="category-level-categories" aria-labelledby="...">), so a
    // descendant-only query would never find it. Match the scope itself first,
    // then fall back to a descendant for the generic root scope.
    const region = scope?.matches("section[aria-labelledby]")
      ? scope
      : scope?.querySelector("section[aria-labelledby]");
    if (!region) return null;
    if (!region.hasAttribute("tabindex")) region.setAttribute("tabindex", "-1");
    return region;
  }

  function finishClose() {
    const panel = drawer();
    const host = document.getElementById("category-drawer-host");
    const focusKey = openerKey;
    if (host) {
      host.innerHTML = "";
      host.style.minHeight = "";
    }
    lockBackground(false);
    closeMenus();
    busy = false;
    discard = false;
    baseline = null;
    baselineIdentity = "";
    pendingDeskValues = null;
    drawerURL = "";
    lastDrawerFocus = null;
    if (panel) panel.removeAttribute("aria-busy");
    const restoreCloseFocus = () => {
      const candidate = focusKey
        ? document.querySelector(`[data-focus-key="${CSS.escape(focusKey)}"]`)
        : opener;
      const target = candidate?.isConnected ? candidate : null;
      const fallback = closeFallbackFocus(panel);
      if (target) target.focus();
      else if (fallback) fallback.focus();
      else restoreFocus();
    };
    window.requestAnimationFrame(restoreCloseFocus);
    window.setTimeout(() => {
      if (!drawer()) restoreCloseFocus();
    }, 100);
  }

  function openDrawer() {
    const panel = drawer();
    if (!panel) return;
    const url = location.pathname + location.search;
    const close = panel.dataset.closeUrl || listURL();
    const state = history.state;
    drawerURL = url;
    if (!same(state, { [KEY]: 1 })) {
      if (opener || (state && location.pathname.includes("/edit"))) {
        history.replaceState(drawerState(panel, url), "", url);
      } else {
        history.replaceState(baseState(close), "", close);
        history.pushState(drawerState(panel, url), "", url);
      }
    } else if (state.kind !== "drawer") {
      history.pushState(drawerState(panel, url), "", url);
    } else if (state.drawerURL !== url) {
      history.replaceState(drawerState(panel, url), "", url);
    }
    filterDeskOptions(panel.querySelector("#category-department")?.value || "");
    captureBaseline(panel);
    lockBackground(true);
    const invalid = panel.querySelector("[aria-invalid='true']");
    (invalid || panel.querySelector("input,select,textarea") || panel).focus();
  }

  function closeDrawer() {
    if (same(history.state, { [KEY]: 1 }) && history.state.kind === "drawer") {
      history.back();
      return;
    }
    const url = panelCloseURL();
    history.replaceState(baseState(url), "", url);
    finishClose();
  }

  function requestClose(trigger) {
    if (busy) return;
    if (dirty()) {
      showDialog(trigger);
      return;
    }
    closeDrawer();
  }

  function discardClose() {
    closeDialog();
    discard = true;
    closeDrawer();
  }

  function panelCloseURL() {
    return drawer()?.dataset.closeUrl || listURL();
  }

  function handlePop(event) {
    const panel = drawer();
    if (!panel) return;
    if (dirty() && !discard) {
      event.stopImmediatePropagation();
      const url = drawerURL || history.state?.drawerURL || location.pathname + location.search;
      history.pushState(drawerState(panel, url), "", url);
      showDialog(document.activeElement);
      return;
    }
    // A clean close and an explicit discard are the same exit: the drawer is
    // leaving, so finishClose owns the teardown and the focus restore. The clean
    // branch used to require a tagged "base" history entry; a launcher-initiated
    // open pops the untagged page entry instead, so the guard missed and HTMX's
    // history restore removed the drawer without restoring focus.
    event.stopImmediatePropagation();
    finishClose();
  }

  function prepareDeskValues(form) {
    const panel = drawer();
    const membershipPath = `/desks/${panel?.dataset.id || ""}/members`;
    const action = new URL(form.action, location.origin);
    if (
      panel?.dataset.kind !== "desk" ||
      !panel.dataset.id ||
      !(form.matches(".desk-add-member") || form.closest(".desk-member-list")) ||
      (action.pathname !== membershipPath && !action.pathname.startsWith(`${membershipPath}/`))
    )
      return;
    pendingDeskValues = { deskID: panel.dataset.id, values: drawerValues(panel), ready: false };
  }

  function captureDeskValues(event) {
    const pending = pendingDeskValues;
    const xhr = event.detail?.xhr;
    const target = event.detail?.target;
    if (!pending) return;
    pending.ready = xhr?.status === 200 && target?.id === "category-drawer-host";
    if (pending.ready) pending.values = drawerValues();
  }

  function restoreDeskValues(panel) {
    const pending = pendingDeskValues;
    pendingDeskValues = null;
    if (
      !pending?.ready ||
      panel?.dataset.kind !== "desk" ||
      panel.dataset.id !== pending.deskID ||
      panel.dataset.serverError === "true"
    )
      return;
    const form = categoryForm();
    Object.entries(pending.values).forEach(([key, value]) => {
      if (form?.elements[key]) form.elements[key].value = value;
    });
  }

  function handleBeforeSwap(event) {
    const xhr = event.detail?.xhr;
    captureDeskValues(event);
    const expected =
      [400, 403, 404, 409, 422].includes(xhr?.status) &&
      xhr.getResponseHeader("HX-Retarget") === "#category-drawer-host" &&
      xhr.getResponseHeader("HX-Reswap") === "outerHTML";
    if (expected) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  }

  document.addEventListener(
    "focusin",
    (event) => {
      const panel = drawer();
      if (panel?.contains(event.target) && !event.target.closest("#category-dirty-dialog"))
        lastDrawerFocus = event.target;
    },
    true,
  );

  document.addEventListener(
    "click",
    (event) => {
      const launch = event.target.closest(".category-drawer-launcher");
      if (launch) {
        opener = launch.dataset.focusKey
          ? launch
          : launch.closest(".category-overflow")?.querySelector("[data-category-menu]") || launch;
        openerKey = opener.dataset.focusKey || null;
      }
      const stay = event.target.closest("[data-category-stay]");
      const discardButton = event.target.closest("[data-category-discard]");
      if (stay) {
        event.preventDefault();
        closeDialog(true);
        return;
      }
      if (discardButton) {
        event.preventDefault();
        discardClose();
        return;
      }
      const close = event.target.closest(
        ".category-drawer-close,.category-drawer-cancel,.category-drawer-backdrop",
      );
      if (close) {
        event.preventDefault();
        requestClose(close);
        return;
      }
      const menuButton = event.target.closest("[data-category-menu]");
      if (menuButton) {
        const menu = menuFor(menuButton);
        if (menu && menu.hidden) openMenu(menuButton, menu);
        else closeMenu(menu);
      } else if (event.target.closest(".category-overflow-menu")) {
        closeMenu(event.target.closest(".category-overflow-menu"));
      } else if (!event.target.closest(".category-overflow")) {
        closeMenus();
      }
    },
    true,
  );

  function filterDeskOptions(department) {
    const desk = document.getElementById("category-desk");
    if (!desk) return;
    [...desk.options].forEach((option) => {
      option.hidden = option.value !== "" && option.dataset.departmentId !== department;
    });
    if (desk.selectedOptions[0]?.hidden) desk.value = "";
  }

  document.addEventListener(
    "change",
    (event) => {
      if (event.target.id === "category-department") filterDeskOptions(event.target.value);
    },
    true,
  );

  document.addEventListener(
    "submit",
    (event) => {
      if (event.target.matches(".desk-add-member") || event.target.closest(".desk-member-list")) {
        prepareDeskValues(event.target);
        return;
      }
      if (!event.target.matches("#category-drawer-form")) return;
      if (busy) {
        event.preventDefault();
        return;
      }
      busy = true;
      drawer()?.setAttribute("aria-busy", "true");
    },
    true,
  );

  document.addEventListener(
    "keydown",
    (event) => {
      const panel = drawer();
      if (!panel) {
        handleMenuKeydown(event);
        return;
      }
      const dialog = panel.querySelector("dialog[open]");
      if (event.key === "Escape") {
        event.preventDefault();
        if (dialog?.id === "category-dirty-dialog") closeDialog(true);
        else requestClose(document.activeElement);
        return;
      }
      if (event.key !== "Tab") return;
      const scope = dialog || panel;
      const items = focusables(scope);
      if (!items.length) {
        event.preventDefault();
        scope.focus();
        return;
      }
      if (event.shiftKey && document.activeElement === items[0]) {
        event.preventDefault();
        items.at(-1).focus();
      }
      if (!event.shiftKey && document.activeElement === items.at(-1)) {
        event.preventDefault();
        items[0].focus();
      }
    },
    true,
  );

  document.addEventListener(
    "cancel",
    (event) => {
      if (!event.target.matches("#category-dirty-dialog")) return;
      event.preventDefault();
      closeDialog(true);
    },
    true,
  );

  document.body.addEventListener("categories:saved", () => {
    const url = listURL();
    history.replaceState(baseState(url), "", url);
    finishClose();
  });
  document.addEventListener("htmx:beforeSwap", handleBeforeSwap, true);
  window.addEventListener("popstate", handlePop, true);
  document.addEventListener("htmx:afterSwap", () => {
    const panel = drawer();
    if (panel) {
      busy = false;
      panel.removeAttribute("aria-busy");
      restoreDeskValues(panel);
      filterDeskOptions(panel.querySelector("#category-department")?.value || "");
      openDrawer();
    }
  });
  openDrawer();
})();
