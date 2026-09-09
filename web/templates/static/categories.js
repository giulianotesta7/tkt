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
  const focusables = (el) => [...el.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])")].filter((item) => item.offsetParent !== null);
  const same = (a, b) => a && b && a[KEY] === b[KEY];
  const listURL = () => {
    const path = location.pathname;
    if (path === "/categories") return path + location.search;
    return "/categories";
  };
  const drawerState = (panel, url = drawerURL || location.pathname + location.search) => ({[KEY]: 1, kind: "drawer", closeURL: panel.dataset.closeUrl || listURL(), drawerURL: url, launcherKey: openerKey || ""});
  const baseState = (url) => ({[KEY]: 1, kind: "base", url});
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
    return panel.querySelector("input:not([disabled]),select:not([disabled]),textarea:not([disabled]),button:not([disabled])") || panel;
  }

  function validDialogReturnFocus(panel, target) {
    return !!panel && target?.isConnected && panel.contains(target) && focusables(panel).includes(target);
  }

  function showDialog(trigger) {
    const panel = drawer();
    const dialog = panel?.querySelector("#category-dirty-dialog");
    if (!dialog) return;
    const active = document.activeElement;
    dialogReturnFocus = validDialogReturnFocus(panel, lastDrawerFocus) ? lastDrawerFocus : validDialogReturnFocus(panel, active) ? active : validDialogReturnFocus(panel, trigger) ? trigger : dialogFocusFallback(panel);
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
    const target = opener?.isConnected ? opener : openerKey ? document.querySelector(`[data-focus-key="${CSS.escape(openerKey)}"]`) : null;
    if (!target) return;
    target.focus();
    if (document.activeElement === target) {
      opener = null;
      openerKey = null;
    }
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
    busy = false;
    discard = false;
    baseline = null;
    baselineIdentity = "";
    pendingDeskValues = null;
    drawerURL = "";
    lastDrawerFocus = null;
    if (panel) panel.removeAttribute("aria-busy");
    const restoreCloseFocus = () => {
      const target = focusKey ? document.querySelector(`[data-focus-key="${CSS.escape(focusKey)}"]`) : opener;
      const fallback = root()?.querySelector(".category-level-categories .category-drawer-launcher") || root()?.querySelector(".category-drawer-launcher");
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
    if (!same(state, {[KEY]: 1})) {
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
    if (same(history.state, {[KEY]: 1}) && history.state.kind === "drawer") {
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
    if (discard) {
      event.stopImmediatePropagation();
      finishClose();
      return;
    }
    if (same(event.state, {[KEY]: 1}) && event.state.kind === "base") {
      event.stopImmediatePropagation();
      finishClose();
    }
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
    ) return;
    pendingDeskValues = {deskID: panel.dataset.id, values: drawerValues(panel), ready: false};
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
    if (!pending?.ready || panel?.dataset.kind !== "desk" || panel.dataset.id !== pending.deskID || panel.dataset.serverError === "true") return;
    const form = categoryForm();
    Object.entries(pending.values).forEach(([key, value]) => {
      if (form?.elements[key]) form.elements[key].value = value;
    });
  }

  function handleBeforeSwap(event) {
    const xhr = event.detail?.xhr;
    captureDeskValues(event);
    const expected = [400, 403, 404, 409, 422].includes(xhr?.status) && xhr.getResponseHeader("HX-Retarget") === "#category-drawer-host" && xhr.getResponseHeader("HX-Reswap") === "outerHTML";
    if (expected) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  }

  document.addEventListener("focusin", (event) => {
    const panel = drawer();
    if (panel?.contains(event.target) && !event.target.closest("#category-dirty-dialog")) lastDrawerFocus = event.target;
  }, true);

  document.addEventListener("click", (event) => {
    const launch = event.target.closest(".category-drawer-launcher");
    if (launch) {
      opener = launch.dataset.focusKey ? launch : launch.closest(".category-overflow")?.querySelector("[data-category-menu]") || launch;
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
    const close = event.target.closest(".category-drawer-close,.category-drawer-cancel,.category-drawer-backdrop");
    if (close) {
      event.preventDefault();
      requestClose(close);
      return;
    }
    const menuButton = event.target.closest("[data-category-menu]");
    if (menuButton) {
      const menu = document.getElementById(menuButton.getAttribute("aria-controls"));
      if (menu) {
        menu.hidden = !menu.hidden;
        menuButton.setAttribute("aria-expanded", String(!menu.hidden));
        if (!menu.hidden) {
          const buttonBox = menuButton.getBoundingClientRect();
          menu.classList.toggle("up", buttonBox.bottom + menu.offsetHeight > window.innerHeight);
        }
      }
    } else if (!event.target.closest(".category-overflow")) {
      document.querySelectorAll(".category-overflow-menu:not([hidden])").forEach((menu) => {
        menu.hidden = true;
        document.querySelector(`[aria-controls="${CSS.escape(menu.id)}"]`)?.setAttribute("aria-expanded", "false");
      });
    }
  }, true);

  function filterDeskOptions(department) {
    const desk = document.getElementById("category-desk");
    if (!desk) return;
    [...desk.options].forEach((option) => {
      option.hidden = option.value !== "" && option.dataset.departmentId !== department;
    });
    if (desk.selectedOptions[0]?.hidden) desk.value = "";
  }

  document.addEventListener("change", (event) => {
    if (event.target.id === "category-department") filterDeskOptions(event.target.value);
  }, true);

  document.addEventListener("submit", (event) => {
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
  }, true);

  document.addEventListener("keydown", (event) => {
    const panel = drawer();
    if (!panel) return;
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
    if (!items.length) { event.preventDefault(); scope.focus(); return; }
    if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items.at(-1).focus(); }
    if (!event.shiftKey && document.activeElement === items.at(-1)) { event.preventDefault(); items[0].focus(); }
  }, true);

  document.addEventListener("cancel", (event) => {
    if (!event.target.matches("#category-dirty-dialog")) return;
    event.preventDefault();
    closeDialog(true);
  }, true);

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
