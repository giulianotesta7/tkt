(() => {
  const KEY = "tktUsers";
  let mounted = false,
    launcher = null,
    baseline = null,
    baselineUserID = "",
    busy = false,
    savePending = false,
    pendingScreenSave = false,
    createPending = false,
    discard = false,
    locked = false,
    dialogReturnFocus = null,
    pendingFocusID = "",
    savedCloseURL = "";
  const q = (s, el = document) => el.querySelector(s);
  const byID = (id, el) => (id ? q(`#${CSS.escape(id)}`, el) : null);
  const root = () => document.getElementById("users-root");
  const drawer = () => q(".users-drawer");
  const form = () => q("#user-drawer-form");
  const same = (a, b) => a && b && a[KEY] === b[KEY];
  const editable = (f) => {
    const values = { name: f?.elements.name?.value || "", email: f?.elements.email?.value || "" };
    if (f?.elements.role) values.role = f.elements.role.value;
    if (f?.elements.active) values.active = f.elements.active.value;
    if (f?.elements.password) values.password = f.elements.password.value;
    return values;
  };
  const dirty = () => {
    if (drawer()?.dataset.serverError === "true") return true;
    const f = form();
    const v = editable(f);
    return !!baseline && Object.keys(v).some((k) => v[k] !== baseline[k]);
  };
  const drawerState = (d) => ({
    [KEY]: 1,
    kind: "drawer",
    editURL: d.dataset.editUrl,
    closeURL: d.dataset.closeUrl,
    userID: d.dataset.userId,
    launcherID: launcher?.id || "",
  });
  const baseState = (url) => ({ [KEY]: 1, kind: "base", url });
  function focusables(el) {
    return [
      ...el.querySelectorAll(
        "a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])",
      ),
    ].filter((x) => x.offsetParent !== null);
  }
  function focusFallback() {
    const r = root();
    const target = q(".users-filters a.is-selected", r) || q("#users-list-title", r);
    if (target) {
      if (!target.matches("a,button,input,select,textarea")) target.tabIndex = -1;
      target.focus();
    }
  }
  function lock(open) {
    const r = root();
    const bg = q("#users-background", r);
    const rail = q(".rail");
    if (open && !locked) {
      document.body.dataset.usersOverflow = document.body.style.overflow;
      document.body.dataset.usersPadding = document.body.style.paddingRight;
      document.body.style.overflow = "hidden";
      document.body.style.paddingRight = `${window.innerWidth - document.documentElement.clientWidth}px`;
      locked = true;
    }
    if (bg) bg.inert = open;
    if (rail) rail.inert = open;
    if (!open && locked) {
      document.body.style.overflow = document.body.dataset.usersOverflow || "";
      document.body.style.paddingRight = document.body.dataset.usersPadding || "";
      delete document.body.dataset.usersOverflow;
      delete document.body.dataset.usersPadding;
      locked = false;
    }
  }
  function updateDescription() {
    const f = form();
    const select = f && f.elements.role;
    const help = q(".users-help", f);
    const option = select?.selectedOptions[0];
    if (help && option?.dataset.description) help.textContent = option.dataset.description;
  }
  function openDrawer() {
    const d = drawer();
    const f = form();
    if (!d || !f) return;
    pendingFocusID = "";
    const url = location.pathname + location.search;
    const close = d.dataset.closeUrl || "/users";
    const state = history.state;
    if (!same(state, { [KEY]: 1 })) {
      if (launcher || (state && location.pathname.includes("/edit")))
        history.replaceState(drawerState(d), "", url);
      else {
        history.replaceState(baseState(close), "", close);
        history.pushState(drawerState(d), "", url);
      }
    } else if (state.kind !== "drawer") history.pushState(drawerState(d), "", url);
    launcher = byID(history.state?.launcherID, root());
    if ((!baseline || baselineUserID !== d.dataset.userId) && d.dataset.serverError !== "true") {
      baseline = editable(f);
      baselineUserID = d.dataset.userId;
    }
    lock(true);
    updateDescription();
    const invalid = q("[aria-invalid=true]", f);
    (invalid || f.elements.name || d).focus();
  }
  function finishClose() {
    const d = drawer();
    const id = launcher?.id || history.state?.launcherID;
    const host = q("#users-drawer-host", root());
    if (d) launcher = byID(id, root());
    if (host) host.innerHTML = "";
    lock(false);
    baseline = null;
    baselineUserID = "";
    busy = false;
    pendingFocusID = id || "";
    if (launcher) launcher.focus();
    else focusFallback();
    setTimeout(() => {
      if (drawer()) return;
      const target = byID(id, root());
      if (target) target.focus();
      else focusFallback();
    }, 100);
  }
  function restorePendingFocus() {
    if (!pendingFocusID) return;
    launcher = byID(pendingFocusID, root());
    pendingFocusID = "";
    if (launcher) launcher.focus();
    else focusFallback();
  }
  function showDialog(id) {
    const d = drawer();
    const dialog = q(id, d);
    if (!dialog) return;
    const active = document.activeElement;
    dialogReturnFocus =
      active && d.contains(active) && active !== d ? active : form()?.elements.name || d;
    if (dialog.showModal) dialog.showModal();
    else dialog.setAttribute("open", "");
    q("button", dialog)?.focus();
  }
  function closeDialog(id, restoreFocus = false) {
    const dialog = q(id, drawer());
    if (dialog?.open) dialog.close();
    else dialog?.removeAttribute("open");
    const target = dialogReturnFocus;
    dialogReturnFocus = null;
    if (restoreFocus && target?.isConnected) target.focus();
  }
  function requestClose() {
    if (busy) return;
    if (dirty()) {
      showDialog("#users-dirty-dialog");
      return;
    }
    const state = history.state;
    if (same(state, { [KEY]: 1 }) && state.kind === "drawer") {
      discard = false;
      history.back();
    } else finishClose();
  }
  function discardClose() {
    closeDialog("#users-dirty-dialog");
    discard = true;
    history.back();
  }
  function handlePop(event) {
    const state = event.state;
    if (savedCloseURL && same(state, { [KEY]: 1 }) && state.kind === "base") {
      event.stopImmediatePropagation();
      const url = savedCloseURL;
      savedCloseURL = "";
      history.replaceState(baseState(url), "", url);
      window.htmx?.ajax("GET", url, { target: "#users-root", swap: "outerHTML" });
      setTimeout(focusFallback, 0);
      return;
    }
    const d = drawer();
    if (!d) {
      if (same(state, { [KEY]: 1 }) && state.kind === "drawer" && root()) {
        event.stopImmediatePropagation();
        const url = root().dataset.usersListUrl || "/users";
        history.replaceState(baseState(url), "", url);
        history.back();
      }
      return;
    }
    if (dirty() && !discard) {
      event.stopImmediatePropagation();
      history.pushState(drawerState(d), "", d.dataset.editUrl);
      showDialog("#users-dirty-dialog");
      return;
    }
    if (same(state, { [KEY]: 1 }) && state.kind === "base") {
      event.stopImmediatePropagation();
      discard = false;
      finishClose();
    }
  }
  // Pending row menu (issue #419, S12): one trigger owns aria-expanded, opening
  // one closes the rest, Escape returns focus to its trigger, and the arrows walk
  // the items the way the catalog overflow menus do. The panel is fixed-positioned
  // next to its trigger because the list panel clips whatever overflows it, so a
  // last-row menu would otherwise open out of sight.
  const rowMenuPanels = () => [...document.querySelectorAll(".users-row-menu-panel:not([hidden])")];
  const rowMenuFor = (button) =>
    document.getElementById(button?.getAttribute("aria-controls") || "");
  const rowMenuItems = (menu) => [...menu.querySelectorAll("[role='menuitem']")];
  const rowMenuTrigger = (menu) =>
    menu?.id ? document.querySelector(`[aria-controls="${CSS.escape(menu.id)}"]`) : null;
  function closeRowMenu(menu, restoreFocus = false) {
    if (!menu || menu.hidden) return;
    menu.hidden = true;
    menu.removeAttribute("style");
    const trigger = rowMenuTrigger(menu);
    if (trigger) trigger.setAttribute("aria-expanded", "false");
    if (restoreFocus && trigger) trigger.focus();
  }
  function closeRowMenus(except = null) {
    rowMenuPanels().forEach((menu) => {
      if (menu !== except) closeRowMenu(menu);
    });
  }
  function positionRowMenu(button, menu) {
    const box = button.getBoundingClientRect();
    menu.hidden = false;
    menu.style.top = "0px";
    menu.style.left = "0px";
    const panel = menu.getBoundingClientRect();
    const gap = 6;
    const openUp = box.bottom + gap + panel.height > window.innerHeight;
    const top = openUp ? box.top - gap - panel.height : box.bottom + gap;
    const left = Math.min(
      Math.max(8, box.right - panel.width),
      window.innerWidth - panel.width - 8,
    );
    menu.style.top = `${Math.round(Math.max(8, top))}px`;
    menu.style.left = `${Math.round(left)}px`;
  }
  function openRowMenu(button, menu) {
    closeRowMenus(menu);
    positionRowMenu(button, menu);
    button.setAttribute("aria-expanded", "true");
    return true;
  }
  function moveRowMenuFocus(menu, delta) {
    const items = rowMenuItems(menu);
    if (!items.length) return;
    const current = items.indexOf(document.activeElement);
    if (current === -1) {
      (delta < 0 ? items.at(-1) : items[0]).focus();
      return;
    }
    items[(current + delta + items.length) % items.length].focus();
  }
  // After a pending screen save the swapped document leaves no focused control
  // behind: the refusal sentence takes focus when there is one, and otherwise
  // focus returns to the selected tab, the same stable landmark focusFallback
  // answers for the drawer.
  function restorePendingScreenFocus() {
    const error = q(".users-error", root());
    if (error) {
      error.tabIndex = -1;
      error.focus();
      return;
    }
    focusFallback();
  }
  function handleClick(event) {
    const menuTrigger = event.target.closest?.("[data-users-pending-menu]");
    if (menuTrigger) {
      event.preventDefault();
      const menu = rowMenuFor(menuTrigger);
      if (!menu) return;
      if (menu.hidden) openRowMenu(menuTrigger, menu);
      else closeRowMenu(menu);
      return;
    }
    if (!event.target.closest?.(".users-row-menu")) closeRowMenus();
    const t = event.target.closest("button,a,select,.users-drawer-backdrop");
    if (!t) return;
    if (t.matches(".users-new-launcher")) launcher = t;
    if (t.matches(".user-launcher,.user-actions")) {
      launcher = t.matches(".user-launcher") ? t : t.closest("tr")?.querySelector(".user-launcher");
      const editURL = t.dataset.editUrl;
      if (editURL && !(same(history.state, { [KEY]: 1 }) && history.state.kind === "drawer"))
        history.pushState(
          {
            [KEY]: 1,
            kind: "drawer",
            editURL,
            closeURL: root()?.dataset.usersListUrl || "/users",
            launcherID: launcher?.id || "",
          },
          "",
          editURL,
        );
    }
    if (t.matches(".users-drawer-close,.users-cancel,.users-drawer-backdrop")) {
      event.preventDefault();
      requestClose();
    } else if (t.matches("[data-stay]")) closeDialog("#users-dirty-dialog", true);
    else if (t.matches("[data-discard]")) discardClose();
    else if (t.matches("[data-deactivate]")) {
      event.preventDefault();
      showDialog("#users-deactivate-dialog");
    } else if (t.matches("[data-cancel-deactivate]")) closeDialog("#users-deactivate-dialog", true);
    else if (t.matches("[data-confirm-deactivate]")) {
      closeDialog("#users-deactivate-dialog");
      const f = form();
      if (f) {
        f.elements.active.value = "false";
        f.requestSubmit();
      }
    } else if (t.matches("[data-users-pending-clear]")) {
      event.preventDefault();
      clearPendingSelection();
    } else if (t.matches("select[name=role]")) updateDescription();
  }
  function handleKey(event) {
    const panel = event.target.closest?.(".users-row-menu-panel:not([hidden])");
    if (panel) {
      if (event.key === "Escape") {
        event.preventDefault();
        closeRowMenu(panel, true);
        return;
      }
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        if (event.key === "ArrowDown") moveRowMenuFocus(panel, 1);
        else if (event.key === "ArrowUp") moveRowMenuFocus(panel, -1);
        else if (event.key === "Home") rowMenuItems(panel)[0]?.focus();
        else rowMenuItems(panel).at(-1)?.focus();
      }
      return;
    }
    const trigger = event.target.closest?.("[data-users-pending-menu]");
    if (trigger) {
      const menu = rowMenuFor(trigger);
      if (menu && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
        event.preventDefault();
        if (openRowMenu(trigger, menu)) {
          moveRowMenuFocus(menu, event.key === "ArrowDown" ? 1 : -1);
        }
        return;
      }
      if (menu && event.key === "Escape" && !menu.hidden) {
        event.preventDefault();
        closeRowMenu(menu, true);
        return;
      }
    }
    if (event.key === "Escape") {
      const open = rowMenuPanels()[0];
      if (open) {
        event.preventDefault();
        closeRowMenu(open, true);
        return;
      }
    }
    const d = drawer();
    if (!d) return;
    const dialog = q("dialog[open]", d);
    if (event.key === "Escape") {
      event.preventDefault();
      if (dialog?.id === "users-dirty-dialog") closeDialog("#users-dirty-dialog", true);
      else if (dialog) closeDialog(`#${dialog.id}`, true);
      else requestClose();
      return;
    }
    if (event.key !== "Tab") return;
    const scope = dialog || d;
    const list = focusables(scope);
    if (!list.length) {
      event.preventDefault();
      d.focus();
      return;
    }
    const first = list[0],
      last = list[list.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
  function handleBeforeSwap(event) {
    const xhr = event.detail?.xhr;
    // A refused save answers its own reason at a known host: the drawer's field
    // errors, and the Pending tab's batch refusal, which replaces the whole
    // screen. Both are swapped so the specific sentence reaches the page instead
    // of only the generic transport notice.
    const target = xhr?.getResponseHeader("HX-Retarget");
    const expected =
      [403, 404, 409, 422].includes(xhr?.status) &&
      (target === "#users-drawer-host" || target === "#users-root");
    if (expected) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  }
  function applySearch(input) {
    const r = root();
    if (!r) return;
    const tbody = q(".users-list tbody", r);
    if (!tbody) return;
    const query = input.value.trim().toLowerCase();
    const rows = [...tbody.querySelectorAll("tr[data-user-name]")];
    let visible = 0;
    rows.forEach((row) => {
      const searchable =
        `${row.dataset.userName || ""} ${row.dataset.userEmail || ""}`.toLowerCase();
      const matches = !query || searchable.includes(query);
      row.hidden = !matches;
      if (matches) visible++;
    });
    const empty = q(".users-search-empty", tbody);
    if (empty) empty.hidden = !query || !rows.length || visible > 0;
  }
  function handleSearch(event) {
    if (event.target?.matches?.(".users-search input[type=search]")) applySearch(event.target);
  }
  function handleChange(event) {
    const target = event.target;
    if (target.matches?.("[data-users-pending-all]")) {
      const boxes = pendingCheckboxes();
      boxes.forEach((box) => {
        box.checked = target.checked;
      });
      syncPendingCount();
    }
    if (target.matches?.("input[name=user_ids]")) syncPendingCount();
    if (target.matches("select[name=role]")) updateDescription();
  }
  // The bulk bar is server-rendered so the Pending form works without script;
  // this only keeps the live count and the select-all box in step with the
  // checkboxes, and clears the selection on demand.
  function pendingForm() {
    return q("#users-pending-form", root());
  }
  // Table checkboxes belong to the bulk form through their form attribute,
  // not DOM ancestry. The form's elements collection includes both shapes.
  function pendingCheckboxes() {
    const f = pendingForm();
    return f
      ? [...f.elements].filter((el) => el.matches("input[type=checkbox][name=user_ids]"))
      : [];
  }
  function syncPendingCount() {
    const f = pendingForm();
    if (!f) return;
    const boxes = pendingCheckboxes();
    const checked = boxes.filter((box) => box.checked).length;
    const count = q("[data-users-bulk-count]", f);
    if (count) count.textContent = String(checked);
    const all = q("[data-users-pending-all]", root());
    if (all) {
      all.checked = boxes.length > 0 && checked === boxes.length;
      all.indeterminate = checked > 0 && checked < boxes.length;
    }
  }
  function clearPendingSelection() {
    const f = pendingForm();
    if (!f) return;
    pendingCheckboxes().forEach((box) => (box.checked = false));
    syncPendingCount();
  }
  function handleSaved() {
    const r = root();
    savedCloseURL = r?.dataset.usersListUrl || "/users";
    pendingFocusID = launcher?.id || history.state?.launcherID || "";
    baseline = null;
    baselineUserID = "";
    busy = false;
    savePending = false;
    if (createPending) {
      history.replaceState(baseState(savedCloseURL), "", savedCloseURL);
      createPending = false;
      lock(false);
      setTimeout(restorePendingFocus, 0);
      return;
    }
    history.back();
  }
  function handleSubmit(event) {
    if (event.target.matches("#users-pending-form, .users-row-menu form")) {
      // The bulk form is the drawer's sibling: the same in-flight guard stops a
      // double click from posting the same non-idempotent batch twice.
      if (busy) {
        event.preventDefault();
        return;
      }
      busy = true;
      pendingScreenSave = true;
      return;
    }
    if (!event.target.matches("#user-drawer-form")) return;
    if (busy) {
      event.preventDefault();
      return;
    }
    busy = true;
    savePending = true;
    createPending = drawer()?.dataset.userId === "0";
    const d = drawer();
    if (d) d.setAttribute("aria-busy", "true");
  }
  function sync() {
    const r = root();
    if (!r) return;
    syncPendingCount();
    if (pendingScreenSave) {
      pendingScreenSave = false;
      busy = false;
      restorePendingScreenFocus();
    }
    const d = drawer();
    if (d) {
      if (d.dataset.serverError === "true") {
        busy = false;
        savePending = false;
        d.removeAttribute("aria-busy");
      }
      openDrawer();
      return;
    }
    if (savePending) {
      handleSaved();
      return;
    }
    if (same(history.state, { [KEY]: 1 }) && history.state.kind === "drawer" && !busy) {
      history.replaceState(
        baseState(r.dataset.usersListUrl || "/users"),
        "",
        r.dataset.usersListUrl || "/users",
      );
      lock(false);
      focusFallback();
    } else if (!same(history.state, { [KEY]: 1 }))
      history.replaceState(
        baseState(r.dataset.usersListUrl || "/users"),
        "",
        r.dataset.usersListUrl || "/users",
      );
    if (pendingFocusID) setTimeout(restorePendingFocus, 0);
  }
  function mount() {
    if (!root()) return;
    sync();
    if (mounted) return;
    mounted = true;
    document.addEventListener("click", handleClick, true);
    document.addEventListener("keydown", handleKey, true);
    document.addEventListener("htmx:beforeSwap", handleBeforeSwap, true);
    document.addEventListener("change", handleChange, true);
    document.addEventListener("input", handleSearch, true);
    document.addEventListener("submit", handleSubmit, true);
    window.addEventListener("popstate", handlePop, true);
    // A fixed-positioned panel does not travel with its trigger, so any scroll
    // or resize closes it instead of leaving it pointing at another row.
    window.addEventListener("scroll", () => closeRowMenus(), true);
    window.addEventListener("resize", () => closeRowMenus());
    // The rail's notification bell polls with htmx on every page; its settle
    // must not re-run the drawer sync (#344: each poll pulled focus back to
    // the drawer's first field).
    document.addEventListener("htmx:afterSettle", (event) => {
      const elt = event.detail?.elt;
      if (elt instanceof Element && elt.closest("#notification-bell, #notification-panel-host"))
        return;
      setTimeout(sync, 0);
    });
    document.addEventListener("htmx:historyRestore", () => setTimeout(restorePendingFocus, 0));
    document.addEventListener("htmx:afterRequest", (event) => {
      // A pending request that never swapped (a transport failure) must release
      // the guard too, or the tab would stay unusable until a reload.
      if (event.detail?.elt?.matches("#users-pending-form, .users-row-menu form") && !event.detail?.successful)
        busy = false;
      if (event.detail?.elt?.matches("#user-drawer-form")) {
        const d = drawer();
        if (d) {
          busy = false;
          savePending = false;
          d.removeAttribute("aria-busy");
          sync();
        }
      }
    });
  }
  window.__tktUsersController = window.__tktUsersController || { mount };
  mount();
})();
