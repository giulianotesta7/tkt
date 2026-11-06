// Inline catalog tree editor for the Categories screen (issue #487).
(() => {
  const root = document.querySelector(".categories-root");
  if (!root) return;
  const t = (key) => window.tktI18n.t(key);
  const tree = () => root.querySelector('[role="tree"]');
  const rows = () => [...root.querySelectorAll('[role="treeitem"]')];
  const visible = () => rows().filter((row) => !row.closest("[hidden]"));
  const rowFor = (kind, id) =>
    rows().find((row) => row.dataset.level === kind && row.dataset.catalogId === String(id));
  let lastRow = null;
  // True once the user chose a row; the automatic first-row selection does not
  // count, so "New folder/category" only follow a selection that was picked.
  let picked = false;
  let toastTimer;
  // Undo of the move the visible toast offers, for Ctrl/Meta+Z on the tree.
  let toastUndo = null;
  let undoing = false;
  let detailsRequest = 0;
  let cut = null;
  let dragging = null;
  let suppressDragClick = false;
  let hoverTimer = null;
  let hoverTarget = null;
  let contextMenu = null;
  let contextRow = null;
  let creator = null;
  let pointerSelection = null;
  let pointerEditorField = null;
  // Visual key hint, right-aligned and hidden from assistive tech so the item
  // keeps one accessible name; aria-keyshortcuts exposes the key instead.
  function hintKey(item, label, aria) {
    const kbd = document.createElement("kbd");
    kbd.className = "catalog-key-hint";
    kbd.setAttribute("aria-hidden", "true");
    kbd.textContent = label;
    item.append(kbd);
    item.setAttribute("aria-keyshortcuts", aria);
  }
  function closeCreator() {
    if (!creator) return;
    creator.observer?.disconnect();
    creator.dispose?.();
    creator.row.remove();
    creator = null;
    placeEditor();
  }
  function buttonAnchor(button, folderID) {
    if (button.hasAttribute("data-catalog-add"))
      return { resolve: () => rowFor("folder", folderID)?.querySelector("[data-catalog-add]") };
    if (button.hasAttribute("data-catalog-header-create")) {
      const kind = button.dataset.catalogHeaderCreate;
      return {
        resolve: () => root.querySelector(`[data-catalog-header-create="${kind}"]`),
        destination: true,
      };
    }
    const kind = button.dataset.catalogCreate;
    return {
      resolve: () =>
        (folderID ? rowFor("folder", folderID) : root)?.querySelector(
          `[data-catalog-create="${kind}"]`,
        ),
    };
  }
  document.addEventListener(
    "pointerdown",
    (event) => {
      if (movePanel && !movePanel.panel.contains(event.target)) closeMove(false);
    },
    true,
  );

  function closeContextMenu(restore = true) {
    if (!contextMenu) return;
    contextMenu.remove();
    contextMenu = null;
    const row = contextRow;
    contextRow = null;
    if (restore) {
      if (row?.isConnected) focusRow(row);
      else tree()?.focus();
    }
  }

  function hideRowMenu(menu, selector = "[data-category-menu]", required = false) {
    menu.hidden = true;
    if (required) menu.parentElement.querySelector(selector).setAttribute("aria-expanded", "false");
    else menu.parentElement?.querySelector(selector)?.setAttribute("aria-expanded", "false");
  }

  function openContextMenu(row, x, y) {
    closeContextMenu(false);
    root.querySelectorAll(".category-overflow-menu:not([hidden])").forEach((menu) => {
      hideRowMenu(menu, "[aria-expanded]");
    });
    contextRow = row;
    if (row) {
      picked = true;
      focusRow(row, false);
    }
    const menu = document.createElement("div");
    menu.className = "catalog-context-menu";
    menu.setAttribute("role", "menu");
    contextMenu = menu;
    const line = row?.querySelector(":scope > .catalog-tree-row");
    function add(text, action, options = {}) {
      const item = document.createElement(options.href ? "a" : "button");
      if (options.href) item.href = options.href;
      else item.type = "button";
      item.setAttribute("role", "menuitem");
      item.tabIndex = -1;
      item.textContent = text;
      if (options.hint) hintKey(item, options.hint, options.keys);
      if (options.danger) item.className = "category-menu-danger";
      if (options.disabled) {
        item.setAttribute("aria-disabled", "true");
        item.title = options.reason || "";
      }
      item.addEventListener("click", (event) => {
        if (item.getAttribute("aria-disabled") === "true") {
          event.preventDefault();
          if (item.title) announce(item.title);
          return;
        }
        closeContextMenu(false);
        if (action) {
          event.preventDefault();
          action();
        }
      });
      menu.append(item);
      return item;
    }
    if (!row || row.dataset.level === "folder") {
      const destination = row?.dataset.catalogId || "";
      const anchor = { x, y, owner: row };
      add(t("js.catalog.context_new_folder"), () => createRow("folder", destination, anchor));
      add(t("js.catalog.context_new_category"), () => createRow("category", destination, anchor));
    }
    if (row) add(t("js.catalog.move_to"), () => openMove(row));
    if (row)
      add(t("js.catalog.context_cut"), () => startCut(row), { hint: "Ctrl+X", keys: "Control+X" });
    if (cut && (!row || row.dataset.level === "folder"))
      add(t("js.catalog.context_paste"), () => moveItem(rowFor(cut.kind, cut.id), row), {
        hint: "Ctrl+V",
        keys: "Control+V",
      });
    if (row) {
      const original = line.querySelector("[data-category-delete]");
      const folder = row.dataset.level === "folder";
      // Children already rendered prove the folder isn't empty, so say so now;
      // otherwise only the server knows (filters or collapsing can hide rows).
      const hasChildren =
        folder && row.querySelector(':scope > [role="group"] [role="treeitem"]') !== null;
      const deletion = add(original.textContent.trim(), () => original.click(), {
        danger: true,
        disabled: folder,
        reason: hasChildren ? t("js.catalog.folder_not_empty") : "",
      });
      if (hasChildren) {
        deletion.setAttribute(
          "aria-label",
          `${deletion.textContent}: ${t("js.catalog.folder_not_empty")}`,
        );
      } else if (folder) {
        fetch(row.dataset.detailsUrl, { credentials: "same-origin" })
          .then((response) => (response.ok ? response.text() : Promise.reject()))
          .then((markup) => {
            if (contextMenu !== menu) return;
            const details = new DOMParser().parseFromString(markup, "text/html");
            const button = details.querySelector("[data-details-delete] button");
            if (!button) return;
            if (button.disabled) {
              deletion.title = button.title;
              deletion.setAttribute("aria-label", `${deletion.textContent}: ${button.title}`);
            } else deletion.removeAttribute("aria-disabled");
          })
          .catch(() => failure());
      }
    }
    root.append(menu);
    const box = menu.getBoundingClientRect();
    const left = Math.max(0, Math.min(x, window.innerWidth - box.width));
    const top = Math.max(0, Math.min(y, window.innerHeight - box.height));
    // Remember where the menu sits on the page, not in the viewport, so a
    // scroll keeps it beside the row that opened it instead of closing it.
    menuAnchor = { left: left + window.scrollX, top: top + window.scrollY };
    placeContextMenu();
    menu.querySelector('[role="menuitem"]')?.focus({ preventScroll: true });
  }

  let menuAnchor = null;
  function placeContextMenu() {
    if (!contextMenu || !menuAnchor) return;
    const top = menuAnchor.top - window.scrollY;
    contextMenu.style.left = `${menuAnchor.left - window.scrollX}px`;
    contextMenu.style.top = `${top}px`;
    // Once the menu has scrolled out of sight it has nothing left to anchor to.
    if (top + contextMenu.offsetHeight < 0 || top > window.innerHeight) closeContextMenu(false);
  }

  root.addEventListener("contextmenu", (event) => {
    if (
      !event.target.closest?.(".catalog-tree") ||
      event.target.closest('input, textarea, select, [contenteditable="true"]')
    )
      return;
    const line = event.target.closest(".catalog-tree-row");
    const row = line?.parentElement.matches('[role="treeitem"]') ? line.parentElement : null;
    event.preventDefault();
    openContextMenu(row, event.clientX, event.clientY);
  });
  document.addEventListener(
    "keydown",
    (event) => {
      if (contextMenu && contextMenu.contains(event.target)) {
        const items = [...contextMenu.querySelectorAll('[role="menuitem"]')];
        const index = items.indexOf(document.activeElement);
        let next;
        if (event.key === "Escape") closeContextMenu();
        else if (event.key === "ArrowDown") next = items[(index + 1) % items.length];
        else if (event.key === "ArrowUp") next = items[(index + items.length - 1) % items.length];
        else if (event.key === "Home") next = items[0];
        else if (event.key === "End") next = items.at(-1);
        else if (event.key === "Enter" || event.key === " ") document.activeElement.click();
        else return;
        event.preventDefault();
        event.stopImmediatePropagation();
        next?.focus();
        return;
      }
      const row = event.target.closest?.('[role="treeitem"]');
      if (
        row &&
        event.target === row &&
        root.contains(row) &&
        (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10"))
      ) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const box = row.querySelector(":scope > .catalog-tree-row").getBoundingClientRect();
        openContextMenu(row, box.left, box.bottom);
      }
      // On the tree itself (no row focused) the same keys open the top-level
      // menu, the keyboard path to what a right-click on empty space offers.
      else if (
        event.target === tree() &&
        (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10"))
      ) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const box = tree().getBoundingClientRect();
        openContextMenu(null, box.left + 16, box.top + 8);
      }
    },
    true,
  );
  document.addEventListener(
    "pointerdown",
    (event) => {
      suppressDragClick = false;
      if (contextMenu && !contextMenu.contains(event.target)) closeContextMenu();
    },
    true,
  );
  document.addEventListener(
    "scroll",
    (event) => {
      if (contextMenu && !contextMenu.contains(event.target)) placeContextMenu();
    },
    true,
  );
  window.addEventListener("blur", () => closeContextMenu());
  function closeBeforeReplacement() {
    closeContextMenu();
    closeMove(false);
    closeCreator();
  }
  document.addEventListener("htmx:beforeSwap", (event) => {
    if (event.detail?.target?.id === "categories-background") closeBeforeReplacement();
  });

  const inspectorMedia = window.matchMedia("(min-width: 1100px)");
  let editor = null;
  // Remember ownership before responsive CSS can hide the current host and
  // cause the browser to blur the field before the resize handler runs.
  document.addEventListener("focusin", (event) => {
    if (!editor?.panel) return;
    if (editor.panel.contains(event.target)) editor.lastFocused = event.target;
    else if (event.target !== document.body && event.target !== document.documentElement)
      editor.lastFocused = null;
  });
  document.addEventListener(
    "pointerdown",
    (event) => {
      if (editor?.panel && !editor.panel.contains(event.target)) editor.lastFocused = null;
    },
    true,
  );
  function editorDirty() {
    const form = editor?.panel?.querySelector("[data-details-save]");
    return !!form && JSON.stringify([...new FormData(form)]) !== editor.baseline;
  }
  function permitDiscard() {
    return !editorDirty() || window.confirm(t("js.catalog.discard_prompt"));
  }
  function releaseEditor() {
    ++detailsRequest;
    editor?.panel?.remove();
    editor = null;
    const host = root.querySelector("[data-catalog-inspector]");
    if (host) host.replaceChildren();
  }
  function focusRow(row, focus = true, showDetails = true, returnField = null) {
    if (!row) return false;
    const changing =
      editor &&
      (inspectorMedia.matches || !showDetails) &&
      (editor.kind !== row.dataset.level || editor.id !== row.dataset.catalogId);
    if (changing && (editor.saving || !permitDiscard())) {
      const field =
        returnField && editor.panel?.contains(returnField)
          ? returnField
          : editor.panel?.querySelector("input:not([type=hidden]), textarea");
      field?.focus();
      return false;
    }
    if (changing) releaseEditor();
    rows().forEach((item) => {
      item.tabIndex = item === row ? 0 : -1;
      item.setAttribute("aria-selected", String(item === row));
    });
    lastRow = { kind: row.dataset.level, id: row.dataset.catalogId };
    if (focus) row.focus();
    if (showDetails && inspectorMedia.matches) openDetails(row, false);
    return true;
  }

  // Paste here exists in a folder menu only while something is cut: the menu
  // keyboard walks every menuitem in the DOM, so a hidden one would swallow focus.
  function syncPaste() {
    root.querySelectorAll(".catalog-tree-folder .catalog-row-menu [role=menu]").forEach((menu) => {
      const paste = menu.querySelector("[data-catalog-paste]");
      if (!cut) {
        paste?.remove();
        return;
      }
      if (paste) return;
      const button = document.createElement("button");
      button.type = "button";
      button.setAttribute("role", "menuitem");
      button.dataset.catalogPaste = "";
      button.textContent = t("js.catalog.context_paste");
      hintKey(button, "Ctrl+V", "Control+V");
      menu.append(button);
    });
  }
  function syncTree() {
    root.classList.add("catalog-inspector-enabled");
    if (tree()) tree().tabIndex = 0;
    root.querySelectorAll("[data-catalog-header-create][hidden]").forEach((button) => {
      button.hidden = false;
    });
    // Keep no-script links in the server markup; shorten only enhanced menus.
    root
      .querySelectorAll(
        ".catalog-row-menu [role=menuitem]:not([data-catalog-cut]):not([data-category-delete]):not([data-catalog-paste]):not([data-catalog-move-to]):not([data-catalog-properties])",
      )
      .forEach((item) => {
        if (!item.hidden) item.hidden = true;
      });
    syncPaste();
    root.querySelectorAll('.catalog-row-menu [role="menu"]').forEach((menu) => {
      if (!menu.querySelector("[data-catalog-properties]")) {
        const properties = document.createElement("button");
        properties.type = "button";
        properties.setAttribute("role", "menuitem");
        properties.dataset.catalogProperties = "";
        properties.textContent = t("js.catalog.properties");
        menu.prepend(properties);
      }
      const existing = menu.querySelector("[data-catalog-move-to]");
      if (existing) {
        if (existing.hidden) existing.hidden = false;
        return;
      }
      const button = document.createElement("button");
      button.type = "button";
      button.setAttribute("role", "menuitem");
      button.dataset.catalogMoveTo = "";
      button.textContent = t("js.catalog.move_to");
      menu.prepend(button);
    });
    root.querySelectorAll("a[data-catalog-cut]").forEach((link) => {
      const button = document.createElement("button");
      button.type = "button";
      button.setAttribute("role", "menuitem");
      button.dataset.catalogCut = "";
      button.textContent = t("js.catalog.context_cut");
      hintKey(button, "Ctrl+X", "Control+X");
      link.replaceWith(button);
    });
    rows().forEach((row) => {
      row.draggable = false;
      const line = row.querySelector(":scope > .catalog-tree-row");
      if (line && !line.querySelector(".catalog-drag-grip")) {
        const grip = document.createElement("span");
        grip.className = "catalog-drag-grip";
        grip.setAttribute("aria-hidden", "true");
        grip.title = t("js.catalog.drag_to_move");
        grip.textContent = "⠿";
        line.prepend(grip);
      }
      const disclosure = row.querySelector(
        ":scope > .catalog-tree-row > [data-catalog-disclosure]",
      );
      if (
        disclosure &&
        row.getAttribute("aria-expanded") !== disclosure.getAttribute("aria-expanded")
      )
        row.setAttribute("aria-expanded", disclosure.getAttribute("aria-expanded"));
    });
    const available = visible();
    if (editor && editorDirty() && !available.includes(rowFor(editor.kind, editor.id))) return;
    const selected = lastRow && rowFor(lastRow.kind, lastRow.id);
    if (!selected || !available.includes(selected)) picked = false;
    focusRow(selected && available.includes(selected) ? selected : available[0], false);
  }
  const observer = new MutationObserver(syncTree);
  observer.observe(root, {
    subtree: true,
    attributes: true,
    attributeFilter: ["aria-expanded", "hidden"],
    childList: true,
  });

  function announce(message) {
    const live = root.querySelector("#catalog-live");
    if (live) live.textContent = message || "";
  }
  function failure() {
    let banner = root.querySelector(".catalog-request-error");
    if (!banner) {
      banner = document.createElement("div");
      banner.className = "error-banner catalog-request-error";
      banner.setAttribute("role", "alert");
      root.prepend(banner);
    }
    banner.textContent = t("js.save.generic_failure");
  }
  async function catalogPost(url, fields = {}, savingEditor = false) {
    const owner = editor && rowFor(editor.kind, editor.id);
    const touchesOwner =
      owner &&
      [owner.dataset.deleteUrl, owner.dataset.renameUrl, owner.dataset.moveUrl].some(
        (path) => path && new URL(url, location.href).pathname === path,
      );
    if (!savingEditor && touchesOwner && editorDirty()) {
      if (!permitDiscard()) return { cancelled: true };
      resetEditor();
    }
    const form = new FormData();
    Object.entries(fields).forEach(([key, value]) =>
      form.set(key, value == null ? "" : String(value)),
    );
    form.set("inline", "1");
    const search = root.querySelector("#category-search");
    if (search?.value) form.set("q", search.value);
    const attention = root.querySelector('input[name="attention"]');
    if (attention) form.set("attention", attention.value);
    const folder = root.querySelector('input[name="folder"]');
    if (folder) form.set("folder", folder.value);
    // The application protects POSTs with its same-origin gate, not a CSRF token.
    try {
      const response = await fetch(url, { method: "POST", body: form, credentials: "same-origin" });
      if (!response.ok) {
        if (
          response.status >= 400 &&
          response.status < 500 &&
          response.headers.get("content-type")?.includes("application/json")
        ) {
          const refusal = (await response.json()).error;
          if (refusal) {
            announce(refusal.message);
            return { error: refusal };
          }
        }
        failure();
        return { failed: true };
      }
      const markup = await response.text();
      const doc = new DOMParser().parseFromString(markup, "text/html");
      const replacement = doc.querySelector("#categories-background");
      const old = root.querySelector("#categories-background");
      if (!replacement || !old) {
        failure();
        return { failed: true };
      }
      const active = document.activeElement?.closest?.('[role="treeitem"]');
      const focus = active ? { kind: active.dataset.level, id: active.dataset.catalogId } : lastRow;
      closeBeforeReplacement();
      const retained = editor;
      const activeField = retained?.panel?.contains(document.activeElement)
        ? document.activeElement
        : null;
      retained?.panel?.remove();
      old.replaceWith(replacement);
      if (retained && (savingEditor || !editorDirty())) releaseEditor();
      else if (retained?.panel) {
        placeEditor();
        if (retained.kind === "folder") refreshFolderDelete(retained);
      }
      window.htmx?.process(replacement);
      document.dispatchEvent(
        new CustomEvent("htmx:afterSettle", { detail: { target: replacement } }),
      );
      syncTree();
      if (focus) focusRow(rowFor(focus.kind, focus.id), !activeField);
      if (activeField?.isConnected) activeField.focus();
      let events = {};
      try {
        events = JSON.parse(response.headers.get("HX-Trigger") || "{}");
      } catch {
        /* Ignore malformed metadata. */
      }
      Object.entries(events).forEach(([name, detail]) =>
        document.dispatchEvent(new CustomEvent(name, { detail })),
      );
      return { ok: true, events };
    } catch {
      failure();
      return { failed: true };
    }
  }
  window.catalogPost = catalogPost;

  function hideToast() {
    const box = root.querySelector(".catalog-toast");
    if (box) box.hidden = true;
    toastUndo = null;
  }
  // The toast waits while pointed at or focused, so its Undo stays reachable
  // by pointer and keyboard (WCAG 2.2.1); otherwise it leaves after 10s.
  function scheduleToastHide() {
    clearTimeout(toastTimer);
    const box = root.querySelector(".catalog-toast");
    if (!box || box.hidden || box.matches(":hover, :focus-within")) return;
    toastTimer = setTimeout(hideToast, 10000);
  }
  function toast(name, detail) {
    if (!detail?.message) return;
    // #catalog-live announces it; the toast is not a second live region.
    announce(detail.message);
    let box = root.querySelector(".catalog-toast");
    if (!box) {
      box = document.createElement("div");
      box.className = "catalog-toast";
      box.addEventListener("pointerenter", () => clearTimeout(toastTimer));
      box.addEventListener("focusin", () => clearTimeout(toastTimer));
      box.addEventListener("pointerleave", () => setTimeout(scheduleToastHide));
      box.addEventListener("focusout", () => setTimeout(scheduleToastHide));
      root.append(box);
    }
    toastUndo = null;
    box.replaceChildren(document.createTextNode(detail.message));
    if (name === "catalog:moved" && !undoing) {
      box.append(document.createTextNode(" · "));
      const button = document.createElement("button");
      button.type = "button";
      button.className = "catalog-toast-action";
      button.textContent = t("js.catalog.undo");
      button.setAttribute("aria-keyshortcuts", "Control+Z");
      button.title = "Ctrl+Z";
      toastUndo = () => {
        if (!button.disabled && !box.hidden) button.click();
      };
      button.addEventListener("click", async () => {
        const row = rowFor(detail.kind, detail.id);
        if (!row) return;
        button.disabled = true;
        undoing = true;
        await catalogPost(row.dataset.moveUrl, { folder_id: detail.fromFolderID });
        undoing = false;
      });
      box.append(button);
    }
    const dismiss = document.createElement("button");
    dismiss.type = "button";
    dismiss.className = "catalog-toast-action";
    dismiss.textContent = t("js.feedback.dismiss");
    dismiss.setAttribute("aria-label", t("js.feedback.dismiss_label"));
    dismiss.addEventListener("click", hideToast);
    box.append(dismiss);
    box.hidden = false;
    scheduleToastHide();
  }
  ["moved", "created", "renamed", "saved", "deleted"].forEach((action) => {
    const name = `catalog:${action}`;
    document.addEventListener(name, (event) => toast(name, event.detail));
  });

  function expand(folder) {
    const disclosure = folder?.querySelector(
      ":scope > .catalog-tree-row > [data-catalog-disclosure]",
    );
    if (disclosure?.getAttribute("aria-expanded") === "false") disclosure.click();
  }
  function clearCut() {
    cut = null;
    syncPaste();
    root.querySelectorAll(".catalog-cut").forEach((row) => row.classList.remove("catalog-cut"));
    const chip = root.querySelector(".catalog-cut-chip");
    if (chip) chip.hidden = true;
  }
  // A floating chip keeps what is cut visible without moving the tree; the
  // live region already announces it, so the chip is not a live region.
  function showCutChip(row, message) {
    let chip = root.querySelector(".catalog-cut-chip");
    if (!chip) {
      chip = document.createElement("div");
      chip.className = "catalog-cut-chip";
      root.append(chip);
      // The move toast stacks above the chip, whose height grows when its
      // text wraps on a phone.
      new ResizeObserver(() => {
        root.style.setProperty("--catalog-cut-chip-height", `${chip.offsetHeight}px`);
      }).observe(chip);
    }
    const text = document.createElement("span");
    text.textContent = message;
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "catalog-toast-action";
    cancel.textContent = t("js.catalog.cancel");
    cancel.addEventListener("click", () => {
      const cutRow = cut && rowFor(cut.kind, cut.id);
      clearCut();
      announce("");
      if (cutRow) focusRow(cutRow);
    });
    chip.replaceChildren(text, cancel);
    chip.hidden = false;
  }
  function startCut(row) {
    if (!row) return;
    clearCut();
    cut = { kind: row.dataset.level, id: row.dataset.catalogId };
    row.classList.add("catalog-cut");
    syncPaste();
    const message = window.tktI18n.t("js.catalog.cut", { name: row.dataset.name });
    announce(message);
    showCutChip(row, message);
    focusRow(row);
  }
  function invalidMove(source, target) {
    if (
      target &&
      source.dataset.level === "folder" &&
      (source === target || source.contains(target))
    )
      return t("js.catalog.move_cycle");
    const parent = source.dataset.parentId || "0";
    if (parent === (target?.dataset.catalogId || "0")) return t("js.catalog.move_same");
    return "";
  }
  async function moveItem(source, target, destinationID = target?.dataset.catalogId || "") {
    if (!source) return;
    const reason =
      target || !destinationID
        ? invalidMove(source, target)
        : (source.dataset.parentId || "0") === destinationID
          ? t("js.catalog.move_same")
          : "";
    if (reason) {
      announce(reason);
      toast("catalog:refused", { message: reason });
      return;
    }
    const kind = source.dataset.level,
      id = source.dataset.catalogId;
    focusRow(source, false);
    const result = await catalogPost(source.dataset.moveUrl, {
      folder_id: destinationID,
    });
    if (result.ok) {
      clearCut();
      focusRow(rowFor(kind, id));
    } else if (result.error) toast("catalog:refused", result.error);
  }
  let movePanel = null;
  function closeMove(restore = true) {
    if (!movePanel) return;
    const { panel, source, dispose } = movePanel;
    dispose();
    panel.remove();
    movePanel = null;
    if (restore && source.isConnected) focusRow(source);
  }
  function openMove(source) {
    closeMove(false);
    const options = [
      ...root
        .querySelector("#catalog-move-folders")
        .content.querySelectorAll("[data-move-folder-id]"),
    ].map((item) => ({
      id: item.dataset.moveFolderId,
      parent: item.dataset.parentId,
      label: item.textContent,
    }));
    const parents = new Map(options.map((item) => [item.id, item.parent]));
    const forbidden = (id) => {
      if (source.dataset.level !== "folder") return false;
      for (let current = id; current && current !== "0"; current = parents.get(current))
        if (current === source.dataset.catalogId) return true;
      return false;
    };
    const panel = document.createElement("div");
    panel.className = "catalog-move-popover";
    panel.setAttribute("role", "dialog");
    panel.setAttribute("aria-label", t("js.catalog.move_to"));
    const search = document.createElement("input");
    search.type = "search";
    search.setAttribute("aria-label", t("js.catalog.move_search"));
    const list = document.createElement("div");
    list.setAttribute("role", "listbox");
    list.setAttribute("aria-label", t("js.catalog.move_destinations"));
    panel.append(search, list);
    root.append(panel);
    function render() {
      list.replaceChildren();
      for (const option of [{ id: "", label: t("js.catalog.move_root") }, ...options]) {
        if (
          forbidden(option.id) ||
          !option.label.toLocaleLowerCase().includes(search.value.toLocaleLowerCase())
        )
          continue;
        const button = document.createElement("button");
        button.type = "button";
        button.setAttribute("role", "option");
        button.textContent = option.label;
        button.disabled = (source.dataset.parentId || "0") === (option.id || "0");
        button.addEventListener("click", () => {
          closeMove(false);
          moveItem(source, rowFor("folder", option.id) || null, option.id);
        });
        list.append(button);
      }
    }
    search.addEventListener("input", render);
    panel.addEventListener("keydown", (event) => {
      const enabled = [...list.querySelectorAll("button:not(:disabled)")];
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        closeMove();
      } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        const index = enabled.indexOf(document.activeElement);
        const delta = event.key === "ArrowDown" ? 1 : -1;
        const next =
          index < 0
            ? delta > 0
              ? 0
              : enabled.length - 1
            : (index + delta + enabled.length) % enabled.length;
        enabled[next]?.focus();
      } else if (event.key === "Enter" && event.target === search) {
        event.preventDefault();
        enabled[0]?.click();
      }
    });
    render();
    const anchor = source.querySelector(":scope > .catalog-tree-row");
    const dispose = window.tktPositionMenu(anchor, panel);
    movePanel = { panel, source, dispose };
    search.focus();
  }
  let disposeDropReason;
  function clearDrop() {
    disposeDropReason?.();
    disposeDropReason = null;
    clearTimeout(hoverTimer);
    hoverTimer = null;
    hoverTarget = null;
    root
      .querySelectorAll(".catalog-drop-valid,.catalog-drop-invalid")
      .forEach((el) => el.classList.remove("catalog-drop-valid", "catalog-drop-invalid"));
    root.querySelector(".catalog-drop-reason")?.remove();
  }
  // Pointer dragging avoids Chromium's native drag stall and also supports touch.
  function pointerTarget(event) {
    const element = document.elementFromPoint(event.clientX, event.clientY);
    if (
      element?.closest(
        'button, input, select, textarea, a, [contenteditable="true"], .catalog-inline-create, .catalog-details, [role="menu"]',
      )
    )
      return null;
    const line = element?.closest(".catalog-tree-row");
    const row = line?.parentElement;
    if (line) return row?.dataset.level === "folder" ? { row, line } : null;
    const area = root.querySelector(".catalog-tree");
    if (element === area || element === tree()) return { row: null, line: area };
    return null;
  }
  function endDrag() {
    dragging = null;
    clearDrop();
    root.querySelector(".catalog-drag-ghost")?.remove();
  }
  root.addEventListener("pointerdown", (event) => {
    const grip = event.target.closest?.(".catalog-drag-grip");
    const name = event.target.closest?.(".catalog-node-name");
    const fromName =
      name &&
      name.closest('[role="tree"]') &&
      event.pointerType !== "touch" &&
      !event.ctrlKey &&
      !event.metaKey &&
      !event.altKey &&
      !event.shiftKey &&
      !event.target.closest("input, .catalog-inline-create");
    const row = (grip || (fromName ? name : null))?.closest('[role="treeitem"]');
    if (!row || event.button !== 0) return;
    // Names keep their normal focus and click behavior until movement starts.
    if (grip) event.preventDefault();
    dragging = {
      kind: row.dataset.level,
      id: row.dataset.catalogId,
      pointer: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      started: false,
    };
  });
  root.addEventListener("dragstart", (event) => {
    if (event.target.closest?.('[role="tree"] .catalog-node-name')) event.preventDefault();
  });
  document.addEventListener("selectstart", (event) => {
    if (dragging?.started) event.preventDefault();
  });
  document.addEventListener(
    "click",
    (event) => {
      if (!suppressDragClick) return;
      suppressDragClick = false;
      event.preventDefault();
      event.stopPropagation();
    },
    true,
  );
  document.addEventListener("pointermove", (event) => {
    if (!dragging || event.pointerId !== dragging.pointer) return;
    if (!dragging.started) {
      if (Math.hypot(event.clientX - dragging.x, event.clientY - dragging.y) < 5) return;
      dragging.started = true;
      const ghost = document.createElement("div");
      ghost.className = "catalog-drag-ghost";
      ghost.textContent = rowFor(dragging.kind, dragging.id)?.dataset.name || "";
      root.append(ghost);
    }
    event.preventDefault();
    window.getSelection()?.removeAllRanges();
    const ghost = root.querySelector(".catalog-drag-ghost");
    if (ghost) {
      ghost.style.left = `${event.clientX + 12}px`;
      ghost.style.top = `${event.clientY + 12}px`;
    }
    const dest = pointerTarget(event);
    if (!dest) {
      clearDrop();
      return;
    }
    const source = rowFor(dragging.kind, dragging.id);
    if (!source) return;
    const reason = invalidMove(source, dest.row);
    if (hoverTarget !== dest.line) {
      clearDrop();
      hoverTarget = dest.line;
      dest.line.classList.add(reason ? "catalog-drop-invalid" : "catalog-drop-valid");
      if (reason) {
        const hint = document.createElement("span");
        hint.className = "catalog-drop-reason";
        hint.textContent = reason;
        dest.line.append(hint);
        disposeDropReason = window.tktPositionMenu(dest.line, hint);
        announce(reason);
      } else if (dest.row?.getAttribute("aria-expanded") === "false") {
        hoverTimer = setTimeout(() => {
          expand(dest.row);
          hoverTimer = null;
        }, 600);
      }
    }
  });
  document.addEventListener("pointerup", (event) => {
    if (!dragging || event.pointerId !== dragging.pointer) return;
    const source = rowFor(dragging.kind, dragging.id);
    const dest = dragging.started && pointerTarget(event);
    // Swallow only the release click; a new press restores ordinary clicks.
    suppressDragClick = dragging.started;
    if (dest) moveItem(source, dest.row);
    endDrag();
  });
  document.addEventListener("pointercancel", endDrag);
  document.addEventListener("keydown", (event) => {
    if (dragging && event.key === "Escape") {
      event.preventDefault();
      endDrag();
    }
  });

  // The folder the header buttons create in: the selected folder, the folder of
  // the selected category, or the top level ("") when nothing was picked.
  function createDestination() {
    const row = picked && lastRow ? rowFor(lastRow.kind, lastRow.id) : null;
    if (!row) return "";
    const id = row.dataset.level === "folder" ? row.dataset.catalogId : row.dataset.parentId;
    return id && id !== "0" ? id : "";
  }
  function createRow(kind, folderID, anchor = null) {
    closeCreator();
    let folder = folderID ? rowFor("folder", folderID) : null;
    if (folderID && !folder) return;
    if (!anchor) expand(folder);
    let list = anchor
      ? root
      : folder?.querySelector(':scope > [role="group"]') || (!folder && tree());
    if (!list && folder) {
      list = document.createElement("ul");
      list.className = "catalog-tree-list";
      list.setAttribute("role", "group");
      folder.append(list);
    }
    if (!list) return;
    const row = document.createElement("li");
    row.className = "catalog-inline-create";
    row.dataset.catalogCreateRow = kind;
    row.dataset.folderId = folderID || "";
    // Header creators fix the kind by the button that opened them; only the
    // others (folder "+", context menu) let the user switch it.
    if (!anchor?.destination) {
      const switcher = document.createElement("span");
      switcher.className = "catalog-create-switch";
      switcher.setAttribute("role", "group");
      switcher.setAttribute("aria-label", t("js.catalog.create_kind"));
      for (const value of ["folder", "category"]) {
        const button = document.createElement("button");
        button.type = "button";
        button.textContent =
          value === "folder" ? t("js.catalog.kind_folder") : t("js.catalog.kind_category");
        button.setAttribute("aria-pressed", String(kind === value));
        button.addEventListener("click", () => {
          kind = value;
          row.dataset.catalogCreateRow = kind;
          switcher
            .querySelectorAll("button")
            .forEach((item) => item.setAttribute("aria-pressed", String(item === button)));
          input.focus();
        });
        switcher.append(button);
      }
      row.append(switcher);
    }
    if (anchor?.destination) {
      const where = document.createElement("span");
      where.className = "catalog-create-destination";
      const label = document.createElement("span");
      label.setAttribute("aria-live", "polite");
      const reset = document.createElement("button");
      reset.type = "button";
      reset.textContent = "✕";
      reset.setAttribute("aria-label", t("js.catalog.create_top_instead"));
      const paint = () => {
        label.textContent = folder
          ? window.tktI18n.t("js.catalog.create_in", { name: folder.dataset.name })
          : t("js.catalog.create_top");
        reset.hidden = !folder;
      };
      reset.addEventListener("click", () => {
        folderID = "";
        folder = null;
        row.dataset.folderId = "";
        paint();
        input.focus({ preventScroll: true });
      });
      paint();
      where.append(label, reset);
      row.append(where);
    }
    const input = document.createElement("input");
    input.name = "name";
    input.required = true;
    input.placeholder = t("js.catalog.name");
    input.setAttribute("aria-label", t("js.catalog.name"));
    row.append(input);
    const hint = document.createElement("span");
    hint.className = "catalog-inline-hint";
    hint.textContent = t("js.catalog.create_hint");
    row.append(hint);
    const error = document.createElement("span");
    error.className = "catalog-inline-error";
    error.id = "catalog-create-error";
    error.setAttribute("role", "alert");
    input.setAttribute("aria-describedby", error.id);
    error.hidden = true;
    row.append(error);
    let dispose;
    const placeCreate = () => {
      if (!anchor) return;
      if (anchor.resolve) {
        dispose?.();
        const button = anchor.resolve();
        if (button) dispose = window.tktPositionMenu(button, row);
        if (creator?.row === row) creator.dispose = dispose;
        return;
      }
      const gap = 8;
      row.style.left = `${Math.max(gap, Math.min(anchor.x, innerWidth - row.offsetWidth - gap))}px`;
      row.style.top = `${Math.max(gap, Math.min(anchor.y, innerHeight - row.offsetHeight - gap))}px`;
    };
    if (anchor) {
      row.classList.add("catalog-create-popover");
      row.setAttribute("role", "dialog");
      row.setAttribute("aria-label", t("js.catalog.create_kind"));
      list.append(row);
      creator = { row };
      placeCreate();
      const observer = new MutationObserver(() => placeEditor());
      observer.observe(row, { attributes: true, attributeFilter: ["style"] });
      creator.observer = observer;
      placeEditor();
    } else {
      list.prepend(row);
      creator = { row };
    }
    input.focus({ preventScroll: !!anchor });
    // Clicking the popover's padding focuses the dialog itself, so Escape still
    // reaches this row.
    row.tabIndex = -1;
    creator.dismiss = () => {
      closeCreator();
      if (anchor?.resolve) anchor.resolve()?.focus({ preventScroll: true });
      else if (anchor && !folder) tree()?.focus({ preventScroll: true });
      else
        focusRow(
          (folderID && rowFor("folder", folderID)) ||
            (lastRow && rowFor(lastRow.kind, lastRow.id)) ||
            visible()[0],
        );
    };
    row.addEventListener("keydown", async (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        if (creator?.row === row) creator.dismiss();
        return;
      }
      if (event.target !== input || event.key !== "Enter" || event.isComposing || row.dataset.busy)
        return;
      event.preventDefault();
      row.dataset.busy = "1";
      const url =
        kind === "folder"
          ? (folder || tree()).dataset.createFolderUrl
          : (folder || tree()).dataset.createCategoryUrl;
      const result = await catalogPost(url, {
        name: input.value,
        folder_id: folderID || "",
      });
      delete row.dataset.busy;
      if (result.ok) {
        if (anchor?.resolve && folderID) expand(rowFor("folder", folderID));
        const created = result.events["catalog:created"];
        if (created?.kind === kind) focusRow(rowFor(created.kind, created.id));
      } else if (result.error) {
        input.setAttribute("aria-invalid", "true");
        error.textContent = result.error.message;
        error.hidden = false;
        placeCreate();
        input.focus({ preventScroll: !!anchor });
      }
    });
  }

  // Focus can be on the page body (after clicking a non-focusable part of the
  // page); Escape still closes an open creator. Focus inside the creator is
  // handled by its own keydown listener.
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || event.defaultPrevented || !creator?.dismiss) return;
    if (event.target !== document.body && event.target !== document.documentElement) return;
    event.preventDefault();
    creator.dismiss();
  });

  async function refreshFolderDelete(current) {
    const form = current.panel.querySelector("[data-details-delete]");
    const button = form?.querySelector("button");
    if (!button) return;
    const request = (current.deleteRequest || 0) + 1;
    current.deleteRequest = request;
    button.disabled = true;
    try {
      const response = await fetch(current.detailsUrl, { credentials: "same-origin" });
      if (!response.ok) throw new Error("details request failed");
      const markup = await response.text();
      if (editor !== current || current.deleteRequest !== request) return;
      const fresh = new DOMParser()
        .parseFromString(markup, "text/html")
        .querySelector("[data-details-delete]");
      if (!fresh?.querySelector("button")) throw new Error("missing delete control");
      form.replaceWith(fresh);
    } catch {
      if (editor === current && current.deleteRequest === request) failure();
    }
  }

  function updateEditor() {
    if (!editor?.panel) return;
    const form = editor.panel.querySelector("[data-details-save]");
    form.querySelector('[type="submit"]').disabled = !editorDirty() || !!editor.saving;
  }
  function resetEditor() {
    if (!editor?.panel) return;
    editor.panel.querySelector("[data-details-save]").reset();
    editor.panel
      .querySelectorAll("[aria-invalid]")
      .forEach((field) => field.removeAttribute("aria-invalid"));
    const error = editor.panel.querySelector("[data-details-error]");
    error.hidden = true;
    error.textContent = "";
    updateEditor();
  }
  function placeEditor() {
    const panel = editor?.panel;
    if (!panel) return;
    // A passive desktop selection is not a request to open a mobile overlay.
    if (
      !inspectorMedia.matches &&
      !editor.focus &&
      !editorDirty() &&
      !panel.contains(document.activeElement)
    ) {
      releaseEditor();
      return;
    }
    const row = rowFor(editor.kind, editor.id);
    const host = root.querySelector("[data-catalog-inspector]");
    const desktop = inspectorMedia.matches;
    panel.classList.toggle("catalog-details-popover", !desktop);
    const cancel = panel.querySelector("[data-details-cancel]");
    const cancelLabel = desktop ? t("js.catalog.discard") : t("js.catalog.cancel");
    if (cancel.textContent !== cancelLabel) cancel.textContent = cancelLabel;
    if (desktop) {
      if (panel.parentElement !== host) {
        const active = panel.contains(document.activeElement) ? document.activeElement : null;
        host.replaceChildren(panel);
        active?.focus({ preventScroll: true });
      }
      panel.style.cssText = "";
      return;
    }
    const line =
      row?.querySelector(":scope > .catalog-tree-row") ||
      root.querySelector('[data-catalog-header-create="category"]');
    if (!line) return;
    const active = panel.contains(document.activeElement) ? document.activeElement : null;
    if (row) {
      if (panel.parentElement !== row) line.after(panel);
    } else if (panel.parentElement !== root) root.append(panel);
    if (active && document.activeElement !== active) active.focus({ preventScroll: true });
    const rect = line.getBoundingClientRect();
    const gap = 8;
    const height = panel.scrollHeight + panel.offsetHeight - panel.clientHeight;
    const capacity = Math.max(0, innerHeight - 2 * gap);
    const below = Math.max(0, Math.min(capacity, innerHeight - rect.bottom - 2 * gap));
    const above = Math.max(0, Math.min(capacity, rect.top - 2 * gap));
    let top;
    let room = null;
    // Keep a folder's children reachable: use the space above its row, even
    // when that requires the editor itself to scroll.
    const hasChildren =
      row?.dataset.level === "folder" && row.querySelector(':scope > [role="group"]');
    const firstChild =
      hasChildren && hasChildren.querySelector(':scope > [role="treeitem"] > .catalog-tree-row');
    const childRect = firstChild ? firstChild.getBoundingClientRect() : null;
    if (hasChildren && above > 0) {
      room = above;
      top = Math.max(gap, rect.top - gap - Math.min(height, above));
    } else if (
      childRect &&
      rect.bottom <= gap &&
      childRect.top > 2 * gap &&
      childRect.top < innerHeight
    ) {
      room = childRect.top - 2 * gap;
      top = Math.max(gap, childRect.top - gap - Math.min(height, room));
    } else if (height <= below) top = rect.bottom + gap;
    else if (height <= above) top = rect.top - gap - height;
    else if (below >= above) {
      top = rect.bottom + gap;
      room = below;
    } else {
      top = gap;
      room = above;
    }
    const width = panel.offsetWidth;
    const left = Math.max(gap, Math.min(rect.left + 80, innerWidth - width - gap));
    let maxHeight = room === null ? "" : `${Math.max(room, 0)}px`;
    let boxHeight = Math.min(height, room === null ? capacity : Math.max(room, 0));
    top = Math.max(gap, Math.min(top, innerHeight - boxHeight - gap));
    const overlay = creator?.row;
    if (overlay?.isConnected && !overlay.hidden) {
      const popup = overlay.getBoundingClientRect();
      if (
        left + width > popup.left &&
        left < popup.right &&
        top + boxHeight + gap > popup.top &&
        top < popup.bottom + gap
      ) {
        const tabbar = document.querySelector(".tabbar");
        const nav =
          tabbar && getComputedStyle(tabbar).display !== "none"
            ? tabbar.getBoundingClientRect()
            : null;
        const bottom =
          nav && nav.width && nav.height
            ? Math.min(innerHeight - gap, nav.top - gap)
            : innerHeight - gap;
        const bands = [
          { start: gap, end: Math.max(gap, Math.min(bottom, popup.top - gap)) },
          { start: Math.min(bottom, Math.max(gap, popup.bottom + gap)), end: bottom },
        ];
        const band = bands.reduce((best, candidate) =>
          candidate.end - candidate.start > best.end - best.start ? candidate : best,
        );
        const bandHeight = Math.max(0, band.end - band.start);
        maxHeight = `${bandHeight}px`;
        boxHeight = Math.min(height, bandHeight);
        top = Math.max(band.start, Math.min(top, band.end - boxHeight));
      }
    }
    panel.style.maxHeight = maxHeight;
    panel.style.left = `${left}px`;
    panel.style.top = `${top}px`;
  }
  window.addEventListener("resize", placeEditor);
  window.addEventListener("scroll", placeEditor, true);
  inspectorMedia.addEventListener("change", () => {
    const current = editor;
    placeEditor();
    const active = document.activeElement;
    const otherDialog = [
      ...document.querySelectorAll('dialog[open], [role="dialog"], [aria-modal="true"]'),
    ].some(
      (dialog) =>
        !current?.panel?.contains(dialog) &&
        dialog.getClientRects().length &&
        getComputedStyle(dialog).visibility !== "hidden",
    );
    if (
      current &&
      editor === current &&
      current.lastFocused &&
      current.panel?.contains(current.lastFocused) &&
      current.lastFocused.isConnected &&
      (!active || active === document.body || active === document.documentElement) &&
      document.hasFocus() &&
      !creator &&
      !contextMenu &&
      !movePanel &&
      !otherDialog &&
      !root.querySelector(".category-overflow-menu:not([hidden])")
    )
      current.lastFocused.focus({ preventScroll: true });
    if (inspectorMedia.matches && !editor && lastRow) {
      const selected = rowFor(lastRow.kind, lastRow.id);
      if (selected && visible().includes(selected)) openDetails(selected, false);
    }
  });
  window.addEventListener("beforeunload", (event) => {
    if (editorDirty()) {
      event.preventDefault();
      event.returnValue = "";
    }
  });
  async function openDetails(row, focus = true) {
    if (!row?.dataset.detailsUrl) return;
    if (editor?.kind === row.dataset.level && editor.id === row.dataset.catalogId) {
      editor.focus = editor.focus || focus;
      if (focus) editor.panel?.querySelector("input:not([type=hidden]), textarea")?.focus();
      return;
    }
    if (!focusRow(row, false, false)) return;
    releaseEditor();
    const current = {
      kind: row.dataset.level,
      id: row.dataset.catalogId,
      detailsUrl: row.dataset.detailsUrl,
      focus,
    };
    editor = current;
    const request = ++detailsRequest;
    try {
      const response = await fetch(row.dataset.detailsUrl, { credentials: "same-origin" });
      if (request !== detailsRequest || editor !== current) return;
      if (!response.ok) {
        releaseEditor();
        failure();
        return;
      }
      const markup = await response.text();
      if (request !== detailsRequest || editor !== current) return;
      const panel = new DOMParser()
        .parseFromString(markup, "text/html")
        .querySelector(".catalog-details");
      if (!panel) {
        releaseEditor();
        failure();
        return;
      }
      current.panel = panel;
      current.baseline = JSON.stringify([
        ...new FormData(panel.querySelector("[data-details-save]")),
      ]);
      panel.addEventListener("input", updateEditor);
      placeEditor();
      updateEditor();
      if (current.focus)
        panel.querySelector("input:not([type=hidden]), textarea, select, button")?.focus();
    } catch {
      if (editor === current) {
        releaseEditor();
        failure();
      }
    }
  }
  function leaveMobileEditor() {
    if (inspectorMedia.matches || !editor?.panel) return true;
    if (!permitDiscard()) return false;
    releaseEditor();
    return true;
  }
  function closeDetails(panel, discard = false) {
    if (!editor || editor.panel !== panel) return;
    if (discard) resetEditor();
    else if (!permitDiscard()) return;
    const row = rowFor(editor.kind, editor.id);
    if (inspectorMedia.matches) {
      resetEditor();
      focusRow(row, true, false);
    } else {
      releaseEditor();
      focusRow(row, true, false);
    }
  }
  document.addEventListener("keydown", (event) => {
    const panel = event.target.closest?.(".catalog-details");
    if (panel && event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      closeDetails(panel);
    }
  });
  document.addEventListener(
    "submit",
    async (event) => {
      const form = event.target;
      if (
        !form.matches?.(
          ".catalog-details [data-details-save], .catalog-details [data-details-delete]",
        )
      )
        return;
      event.preventDefault();
      if (form.dataset.busy) return;
      form.dataset.busy = "1";
      const fields = Object.fromEntries(new FormData(form));
      const current = editor;
      if (current) current.saving = true;
      const result = await catalogPost(form.action, fields, true);
      if (current) current.saving = false;
      delete form.dataset.busy;
      if (!result.ok) updateEditor();
      if (result.error) {
        const panel = form.closest(".catalog-details");
        const error = panel.querySelector("[data-details-error]");
        const field = form.elements.namedItem(result.error.field);
        form.querySelectorAll("[aria-invalid]").forEach((el) => el.removeAttribute("aria-invalid"));
        if (field?.setAttribute) field.setAttribute("aria-invalid", "true");
        error.textContent = result.error.message;
        error.hidden = false;
        field?.focus?.();
      }
    },
    true,
  );

  function renameRow(row) {
    const name = row.querySelector(":scope > .catalog-tree-row .catalog-node-name");
    if (!name || row.querySelector(":scope > .catalog-tree-row .catalog-rename-input")) return;
    const input = document.createElement("input");
    input.className = "catalog-rename-input";
    input.name = "name";
    input.value = row.dataset.name;
    input.setAttribute("aria-label", t("js.catalog.name"));
    const error = document.createElement("span");
    error.className = "catalog-inline-error catalog-rename-error";
    error.id = `catalog-rename-error-${row.dataset.level}-${row.dataset.catalogId}`;
    error.setAttribute("role", "alert");
    input.setAttribute("aria-describedby", error.id);
    error.hidden = true;
    focusRow(row, false);
    name.hidden = true;
    name.after(input, error);
    input.focus();
    input.select();
    let closed = false;
    let disposeError;
    const close = (restoreFocus) => {
      closed = true;
      disposeError?.();
      input.remove();
      error.remove();
      name.hidden = false;
      if (restoreFocus) focusRow(row);
    };
    // A successful save swaps the whole tree, which removes this field; a
    // refusal keeps it open with the reason. Focus only comes back to the
    // field when the save started from it (Enter), never after leaving it.
    const save = async (refocus) => {
      input.disabled = true;
      const result = await catalogPost(row.dataset.renameUrl, { name: input.value });
      if (result.ok || !input.isConnected) return;
      input.disabled = false;
      if (result.error) {
        input.setAttribute("aria-invalid", "true");
        error.textContent = result.error.message;
        error.hidden = false;
        disposeError?.();
        disposeError = window.tktPositionMenu(input, error);
      }
      if (refocus) input.focus();
    };
    input.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close(true);
      } else if (event.key === "Enter" && !event.isComposing && !input.disabled) {
        event.preventDefault();
        save(true);
      }
    });
    // Leaving the field ends the rename, as in a file manager: an unchanged
    // name just closes it, a changed one is saved. Starting another rename
    // moves focus, so only one rename field is ever open.
    input.addEventListener("blur", () => {
      if (closed || input.disabled || !input.isConnected) return;
      if (input.value.trim() === row.dataset.name) close(false);
      else save(false);
    });
  }

  document.addEventListener(
    "pointerdown",
    (event) => {
      const line = event.target.closest?.(".catalog-tree-row");
      pointerSelection =
        inspectorMedia.matches &&
        line &&
        root.contains(line) &&
        !event.target.closest("a, button, input, textarea, select")
          ? line.closest('[role="treeitem"]')
          : null;
      const active = document.activeElement;
      pointerEditorField =
        pointerSelection && editor?.panel?.contains(active) && active.matches("input, textarea")
          ? active
          : null;
      if (
        !inspectorMedia.matches &&
        line &&
        root.contains(line) &&
        !event.target.closest("a, button, input, textarea, select")
      )
        picked = true;
    },
    true,
  );
  document.addEventListener("pointercancel", () => {
    pointerSelection = null;
    pointerEditorField = null;
  });
  document.addEventListener("focusin", (event) => {
    const row = event.target.closest?.('[role="treeitem"]');
    // A pointer selection is handled by its click, not both focusin and click.
    if (row && root.contains(row) && event.target === row && row !== pointerSelection)
      focusRow(row, false);
  });
  document.addEventListener(
    "click",
    (event) => {
      const line = event.target.closest?.(".catalog-tree-row");
      const returnField =
        pointerSelection === line?.closest('[role="treeitem"]') ? pointerEditorField : null;
      pointerSelection = null;
      pointerEditorField = null;
      if (
        inspectorMedia.matches &&
        line &&
        root.contains(line) &&
        !event.target.closest("a, button, input, textarea, select")
      ) {
        if (focusRow(line.closest('[role="treeitem"]'), true, true, returnField)) picked = true;
      }
      const properties = event.target.closest?.("[data-catalog-properties]");
      if (properties && root.contains(properties)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        hideRowMenu(properties.closest(".category-overflow-menu"));
        const row = properties.closest('[role="treeitem"]');
        if (focusRow(row, false, false)) {
          picked = true;
          openDetails(row);
        }
        return;
      }
      const moveAction = event.target.closest?.("[data-catalog-move-to]");
      if (moveAction && root.contains(moveAction)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const menu = moveAction.closest('[role="menu"]');
        hideRowMenu(menu, "[data-category-menu]", true);
        openMove(moveAction.closest('[role="treeitem"]'));
        return;
      }
      const pasteAction = event.target.closest?.("button[data-catalog-paste]");
      if (pasteAction && root.contains(pasteAction) && cut) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const menu = pasteAction.closest(".category-overflow-menu");
        hideRowMenu(menu);
        moveItem(rowFor(cut.kind, cut.id), pasteAction.closest('[role="treeitem"]'));
        return;
      }
      const cutAction = event.target.closest?.("button[data-catalog-cut]");
      if (cutAction && root.contains(cutAction)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const menu = cutAction.closest(".category-overflow-menu");
        hideRowMenu(menu);
        startCut(cutAction.closest('[role="treeitem"]'));
        return;
      }
      const cancel = event.target.closest?.("[data-details-cancel]");
      if (cancel && root.contains(cancel)) {
        event.preventDefault();
        closeDetails(cancel.closest(".catalog-details"), true);
        return;
      }
      const details = event.target.closest?.("[data-catalog-pen]");
      if (details && root.contains(details)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const menu = details.closest(".category-overflow-menu");
        if (menu) hideRowMenu(menu);
        openDetails(details.closest('[role="treeitem"]'));
        return;
      }
      const add = event.target.closest?.("[data-catalog-add]");
      if (add && root.contains(add)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const folderID = add.closest('[role="treeitem"]').dataset.catalogId;
        createRow("category", folderID, buttonAnchor(add, folderID));
        return;
      }
      const headerCreate = event.target.closest?.("[data-catalog-header-create]");
      if (headerCreate && root.contains(headerCreate)) {
        event.preventDefault();
        createRow(
          headerCreate.dataset.catalogHeaderCreate,
          createDestination(),
          buttonAnchor(headerCreate, ""),
        );
        return;
      }
      const create = event.target.closest?.("[data-catalog-create]");
      if (!create || !root.contains(create)) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      const folder = create.closest('[role="treeitem"][data-level="folder"]');
      const folderID = folder?.dataset.catalogId || "";
      createRow(create.dataset.catalogCreate, folderID, buttonAnchor(create, folderID));
    },
    true,
  );
  document.addEventListener("dblclick", (event) => {
    const name = event.target.closest?.(".catalog-node-name");
    const row = name?.closest('[role="treeitem"][data-level="folder"]');
    if (!row || !root.contains(row)) return;
    event.preventDefault();
    renameRow(row);
  });
  document.addEventListener("keydown", (event) => {
    if (
      toastUndo &&
      (event.ctrlKey || event.metaKey) &&
      !event.shiftKey &&
      event.key.toLowerCase() === "z" &&
      (event.target === tree() ||
        (event.target.matches?.('[role="treeitem"]') && root.contains(event.target)))
    ) {
      event.preventDefault();
      toastUndo();
      return;
    }
    if (cut && event.target === tree() && event.key === "Escape") {
      event.preventDefault();
      clearCut();
      announce("");
      return;
    }
    if (
      cut &&
      event.target === tree() &&
      (event.ctrlKey || event.metaKey) &&
      event.key.toLowerCase() === "v"
    ) {
      event.preventDefault();
      moveItem(rowFor(cut.kind, cut.id), null);
      return;
    }
    const row = event.target.closest?.('[role="treeitem"]');
    if (!row || event.target !== row || !root.contains(row)) return;
    if (event.key !== "Tab") picked = true;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "x") {
      event.preventDefault();
      startCut(row);
      return;
    }
    if (
      cut &&
      (event.ctrlKey || event.metaKey) &&
      event.key.toLowerCase() === "v" &&
      row.dataset.level === "folder"
    ) {
      event.preventDefault();
      moveItem(rowFor(cut.kind, cut.id), row);
      return;
    }
    if (cut && event.key === "Escape") {
      event.preventDefault();
      clearCut();
      announce("");
      return;
    }
    const items = visible();
    const index = items.indexOf(row);
    let next;
    if (event.key === "ArrowDown") next = items[index + 1];
    else if (event.key === "ArrowUp") next = items[index - 1];
    else if (event.key === "Home") next = items[0];
    else if (event.key === "End") next = items.at(-1);
    else if (event.key === "ArrowRight" && row.dataset.level === "folder") {
      if (row.getAttribute("aria-expanded") === "false") expand(row);
      else next = row.querySelector(':scope > [role="group"] > [role="treeitem"]');
    } else if (event.key === "ArrowLeft") {
      const disclosure = row.querySelector(
        ":scope > .catalog-tree-row > [data-catalog-disclosure]",
      );
      if (disclosure && row.getAttribute("aria-expanded") === "true") disclosure.click();
      else next = row.parentElement.closest('[role="treeitem"]');
    } else if (event.key === "Enter") {
      openDetails(row);
    } else return;
    event.preventDefault();
    if (next) focusRow(next);
  });
  document.addEventListener("htmx:afterSettle", (event) => {
    if (event.detail?.target?.id === "categories-background") {
      syncTree();
      if (lastRow) focusRow(rowFor(lastRow.kind, lastRow.id));
    }
  });
  syncTree();
})();
