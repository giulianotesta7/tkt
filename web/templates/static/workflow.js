(() => {
  // Horizontal drag reorder for the master-detail rail. Dragging starts only from
  // the per-card grip (.workflow-drag-handle), so selection clicks and the step
  // menu keep working. During a drag the source card dims as the placeholder and
  // a blue insertion indicator marks the target slot; drop submits the existing
  // POST action `reorder` with the presentation-only source/target indexes.
  const rail = () => document.querySelector(".workflow-step-rail");
  const cards = () => [...(rail()?.querySelectorAll(".workflow-step-card") || [])];
  const controls = () => {
    const form = document.querySelector("#workflow-form");
    if (!form) return null;
    return { source: form.elements.namedItem("source_index"), target: form.elements.namedItem("target_index"), button: form.querySelector("[data-workflow-reorder]") };
  };
  const terminalSlots = () => cards().filter(card => !card.querySelector(".workflow-drag-handle")).length;
  let source = -1;
  let destination = -1;
  let indicator = null;
  const ensureIndicator = () => {
    if (indicator || !rail()) return;
    indicator = document.createElement("div");
    indicator.className = "workflow-drag-indicator";
    rail().appendChild(indicator);
  };
  const moveIndicator = () => {
    ensureIndicator();
    const list = cards();
    if (!indicator) return;
    if (destination < 0 || destination >= list.length) indicator.style.display = "none";
    else { indicator.style.display = "block"; indicator.style.left = (list[destination].offsetLeft - 2) + "px"; }
  };
  const clearDrag = () => {
    cards().forEach(card => card.classList.remove("is-dragging"));
    if (indicator) indicator.style.display = "none";
    source = destination = -1;
  };
  // Slot before the card whose midpoint the pointer crossed; rail edges snap to
  // the first/last slot so a drag can land on any reachable step.
  const hoveredSlot = event => {
    const list = cards();
    if (event.clientX < rail().getBoundingClientRect().left) return 0;
    for (let i = 0; i < list.length; i++) {
      const rect = list[i].getBoundingClientRect();
      if (event.clientX <= rect.left + rect.width / 2) return i;
    }
    return list.length;
  };
  document.addEventListener("dragstart", event => {
    const handle = event.target instanceof Element ? event.target.closest(".workflow-drag-handle") : null;
    const card = handle?.closest(".workflow-step-card");
    if (!card) return;
    source = cards().indexOf(card);
    if (source < 0) { clearDrag(); return; }
    destination = source;
    card.classList.add("is-dragging");
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", "workflow-step");
    moveIndicator();
  });
  document.addEventListener("dragover", event => {
    if (source < 0) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    const bounds = rail().getBoundingClientRect();
    if (event.clientX > bounds.right - 48) rail().scrollLeft += 12;
    else if (event.clientX < bounds.left + 48) rail().scrollLeft -= 12;
    const slot = hoveredSlot(event);
    if (slot === source || slot === source + 1) destination = source;
    else destination = slot < source ? slot : slot - 1;
    const movable = cards().length - terminalSlots();
    if (destination > movable - 1) destination = movable - 1;
    if (destination < 0) destination = 0;
    moveIndicator();
  });
  document.addEventListener("drop", event => {
    if (source < 0) return;
    event.preventDefault();
    const named = controls();
    const valid = named && named.source && named.target && named.button && destination >= 0 && destination < cards().length && destination !== source;
    if (valid) {
      named.source.value = String(source);
      named.target.value = String(destination);
      clearDrag();
      named.button.click();
      return;
    }
    clearDrag();
  });
      document.addEventListener("dragend", clearDrag);

      // The 422 builder response replaces the editor after the user submits from
      // the page header. Observe that replacement and return focus to its first
      // editable control so the invalid value can be corrected immediately.
      const focusValidationControl = () => {
        const builder = document.querySelector("#workflow-builder");
        if (!builder?.querySelector(".error-banner[role='alert']")) return;
        builder.querySelector(".workflow-editor-panel textarea, .workflow-editor-panel input:not([type='hidden']), .workflow-editor-panel select")?.focus({ preventScroll: true });
      };
      const validationFocusObserver = new MutationObserver(mutations => {
        const replacedBuilder = mutations.some(({ addedNodes }) =>
          [...addedNodes].some(node =>
            node instanceof Element &&
            node.matches("#workflow-builder") &&
            node.querySelector(".error-banner[role='alert']"),
          ),
        );
        if (replacedBuilder) requestAnimationFrame(focusValidationControl);
      });
      validationFocusObserver.observe(document.body, { childList: true, subtree: true });

      // Dropdowns (node menus, the typed-add popover, field menus) are anchored to
      // the viewport with position:fixed so no ancestor scroll container can clip
      // them; they flip above the trigger when they would exceed the bottom edge.
      // Some browsers do not fire the details "toggle" event on click, so the
      // positioning is driven from the summary click and the toggle event is kept
      // as a fallback for the browsers that do emit it.
      const positionDropdown = details => {
        const body = details.querySelector(".workflow-step-menu-actions, .workflow-add-options, .workflow-field-menu-actions");
        if (!body) return;
        if (!details.open) {
          body.style.position = "";
          body.style.top = "";
          body.style.left = "";
          return;
        }
        const summary = details.querySelector("summary");
        if (!summary) return;
        const rect = summary.getBoundingClientRect();
        const height = body.offsetHeight;
        const width = body.offsetWidth;
        const below = rect.bottom + 5;
        const up = rect.top - height - 5;
        let top;
        if (below + height <= window.innerHeight - 8) top = below;
        else if (up >= 4) top = up;
        else top = Math.max(4, Math.min(below, window.innerHeight - 8 - height));
        body.style.position = "fixed";
        body.style.top = top + "px";
        body.style.left = Math.max(8, rect.right - width) + "px";
        body.style.bottom = "auto";
      };
      document.addEventListener("click", event => {
        const summary = event.target instanceof Element ? event.target.closest("summary") : null;
        const details = summary?.closest(".workflow-step-menu, .workflow-add-popover, .workflow-field-menu");
        if (!details) return;
        requestAnimationFrame(() => positionDropdown(details));
      });
      document.addEventListener("toggle", event => {
        const details = event.target instanceof HTMLDetailsElement ? event.target : null;
        if (details) positionDropdown(details);
      });

      // The summary already provides focus and Enter/Space activation, but
      // browsers do not close an open <details> on Escape. Close the open
      // step/field menu and return focus to its trigger, mirroring native
      // menu behavior without touching the fixed viewport positioning.
      document.addEventListener("keydown", event => {
        if (event.key !== "Escape") return;
        const details = event.target instanceof Element ? event.target.closest(".workflow-step-menu, .workflow-field-menu") : null;
        if (!details || !details.open) return;
        details.open = false;
        const summary = details.querySelector("summary");
        if (summary) summary.focus();
      });
    })();
  // ==== Dirty-state guard for structural actions (issue #139 WU2) ====
  // Nothing persists until explicit Save/Publish or a clean structural action;
  // the baseline resets only on a confirmed persisting 200. carriedDirty pins
  // the guard across non-persisting swaps (select_step/422/abort never adopt
  // the posted snapshot as clean); cleanClone keeps a detached clean builder
  // so Discard can always resume from a known-clean state.
  const STRUCTURAL_ACTIONS = new Set(["add_step", "remove_step", "move_up", "move_down", "reorder", "change_type", "add_field", "remove_field"]);
  const PERSISTING_ACTIONS = new Set([...STRUCTURAL_ACTIONS, "save", "publish"]);
  const UNTRACKED_FIELDS = new Set(["selected_step_index", "source_index", "target_index"]);
  let baseline = new Map();
  let cleanClone = null;
  let carriedDirty = false;
  let pendingStructural = null;
  let saveInFlight = false;
  let saveBypass = false;
  let dialogChoice = null;
  let dialogReturnFocus = null;

  const builderForm = () => document.querySelector("#workflow-form");
  const dirtyDialog = () => document.getElementById("workflow-dirty-dialog");
  const controlValue = (el) => (el.type === "checkbox" ? String(el.checked) : el.value);
  const trackable = (el) => !!el.name && el.type !== "submit" && !el.disabled && !UNTRACKED_FIELDS.has(el.name);
  function mergeBaseline() {
    const form = builderForm();
    if (!form) return;
    for (const el of form.elements) {
      // New control names join with their server-rendered values; existing
      // entries keep persisted values so carried edits stay detectable.
      if (trackable(el) && !baseline.has(el.name)) baseline.set(el.name, controlValue(el));    }
  }
  function captureBaseline() {
    baseline = new Map();
    mergeBaseline();
    const builder = document.getElementById("workflow-builder");
    cleanClone = builder ? builder.cloneNode(true) : null;
    carriedDirty = false;
  }
  function isDirty() {
    const form = builderForm();
    if (!form) return false;
    for (const el of form.elements) {
      if (!trackable(el)) continue;
      if (!baseline.has(el.name) || baseline.get(el.name) !== controlValue(el)) return true;
    }
    return false;
  }
  function restoreBaseline() {
    const form = builderForm();
    if (!form) return;
    for (const el of form.elements) {
      if (!trackable(el) || !baseline.has(el.name)) continue;
      const value = baseline.get(el.name);
      if (el.type === "checkbox") el.checked = value === "true";
      else el.value = value;
    }
  }
      // Replays a pending structural action exactly once against the clean
      // form, restoring the captured untracked params (reorder slots).
      function replayStructural(action, path, params) {
        const form = builderForm();
        if (!form || !window.htmx) return;
        const values = window.htmx.values(form);
        values.action = action;
        for (const name of UNTRACKED_FIELDS) if (params?.[name]) values[name] = params[name];
        window.htmx.ajax("POST", path, { values: values, target: "#workflow-builder", swap: "outerHTML" });
      }
      // Discard resumes from the clean clone: no dirty value reaches the replay.
      function discardToClean() {
        const builder = document.getElementById("workflow-builder");
        const clean = cleanClone?.cloneNode(true);
        if (builder && clean) {
          builder.replaceWith(clean);
          window.htmx?.process(clean);
        } else restoreBaseline();
        captureBaseline();
      }
  let lastWorkflowAction = "";
  let lastWorkflowStatus = 0;
  document.addEventListener("htmx:beforeRequest", (event) => {
    lastWorkflowAction = event.detail?.requestConfig?.parameters?.action ?? "";
    if (event.detail?.xhr) event.detail.xhr.__wfAction = lastWorkflowAction;
    // One decision at a time; a structural request racing the save is held back.
    if ((saveInFlight || dirtyDialog()?.open) && !saveBypass) {
      event.preventDefault();
      return;
    }
    if (isDirty()) carriedDirty = true;
    if (!STRUCTURAL_ACTIONS.has(lastWorkflowAction) || (!isDirty() && !carriedDirty)) return;
    event.preventDefault();
    pendingStructural = { action: lastWorkflowAction, path: event.detail?.pathInfo?.requestPath ?? builderForm()?.action ?? "", params: { ...(event.detail?.requestConfig?.parameters ?? {}) } };
    dialogReturnFocus = document.activeElement;
    dialogChoice = null;
    dirtyDialog()?.showModal();
    dirtyDialog()?.querySelector("[data-workflow-save-continue]")?.focus();
  });
  document.addEventListener("htmx:beforeSwap", (event) => {
    lastWorkflowStatus = event.detail?.xhr?.status ?? 0;
  });
      document.addEventListener("htmx:afterSwap", () => {
        if (lastWorkflowStatus === 200 && PERSISTING_ACTIONS.has(lastWorkflowAction)) captureBaseline();
        else if (!carriedDirty) {
          mergeBaseline();
          cleanClone = document.getElementById("workflow-builder")?.cloneNode(true) ?? cleanClone;
        }
        syncExitGuard();
      });
  document.addEventListener("htmx:afterRequest", (event) => {
    if (!saveInFlight || event.detail?.xhr?.__wfAction !== "save") return;
    const status = event.detail?.xhr?.status ?? 0;
    if (event.detail?.successful === false || status === 0 || status >= 400) {
      // Failure: no replay; the draft stays dirty for an explicit retry.
      saveInFlight = false;
      pendingStructural = null;
    }
  });
  document.addEventListener("htmx:afterSettle", (event) => {
    if (!saveInFlight || event.detail?.xhr?.__wfAction !== "save") return;
    saveInFlight = false;
    if ((event.detail?.xhr?.status ?? 0) !== 200) return;
    captureBaseline();
    const pending = pendingStructural;
    pendingStructural = null;
    if (pending) replayStructural(pending.action, pending.path, pending.params);
  });
  // Cancel/Escape: no request, shared cleanup restores focus. Discard: replay
  // once from the clean clone. Save+continue: save, then replay once against
  // the saved draft.
  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target : null;
    const choices = [["save", "[data-workflow-save-continue]"], ["discard", "[data-workflow-discard-continue]"], ["cancel", "[data-workflow-cancel]"]];
    const chosen = target ? choices.find(([, selector]) => target.closest(selector))?.[0] : null;
    if (!chosen) return;
    event.preventDefault();
    dialogChoice = chosen;
    dirtyDialog()?.close();
    if (dialogChoice === "cancel") return; // shared cleanup runs in the close handler
    if (dialogChoice === "discard") {
      discardToClean();
      const pending = pendingStructural;
      pendingStructural = null;
      if (pending) replayStructural(pending.action, pending.path, pending.params);
      return;
    }
    saveBypass = true;
    saveInFlight = true;
    document.querySelector('.page-actions button[name="action"][value="save"]')?.click();
    saveBypass = false;
  });
  document.addEventListener("cancel", (event) => {
    if (event.target === dirtyDialog()) dialogChoice = "cancel";
  });
  document.addEventListener("close", (event) => {
    if (event.target !== dirtyDialog()) return;
    const choice = dialogChoice;
    dialogChoice = null;
        if (choice && choice !== "cancel") return; // save/discard handle their own flow
        // Cancel via button or Escape: no request, focus restored.
        pendingStructural = null;
        const target = dialogReturnFocus;
        dialogReturnFocus = null;
        if (target?.isConnected) target.focus();
      });

      // ==== Exit guards for unsaved workflow edits (issue #139 WU3) ====
      // One owner per exit path: plain same-tab link clicks and browser Back use
      // the in-app leave dialog; reload/tab close uses browser-native
      // beforeunload only. Dirty state reuses the WU2 isDirty/baseline
      // lifecycle; carriedDirty still counts because unsaved edits are pending.
      const WF_GUARD_STATE = { wfGuard: true };
      let guardArmed = false;
      let leavingByBack = false;
      let bypassBeforeUnload = false;
      let pendingLeaveHref = null;
      let leaveReturnFocus = null;

      const leaveDialog = () => document.getElementById("workflow-leave-dialog");
      const exitGuardActive = () => !!builderForm() && (isDirty() || carriedDirty);
      const syncExitGuard = () => {
        if (exitGuardActive()) {
          // Arming pushes a same-URL sentinel entry so the first Back stays on
          // the document and can be intercepted without a native prompt.
          if (!guardArmed) { guardArmed = true; history.pushState(WF_GUARD_STATE, "", location.href); }
          return;
        }
        if (guardArmed) {
          guardArmed = false;
          if (history.state?.wfGuard) history.back(); // silently drop the sentinel
        }
      };
      function openLeaveDialog(href) {
        leavingByBack = false;
        pendingLeaveHref = href;
        leaveReturnFocus = document.activeElement;
        leaveDialog()?.showModal();
      }
      function leaveByBack() {
        bypassBeforeUnload = true;
        leavingByBack = true;
        history.back();
      }
      document.addEventListener("input", syncExitGuard, true);
      document.addEventListener("change", syncExitGuard, true);
      window.addEventListener("beforeunload", (event) => {
        if (!exitGuardActive() || bypassBeforeUnload) return;
        bypassBeforeUnload = false;
        event.preventDefault();
        event.returnValue = "";
      });
      window.addEventListener("pageshow", () => { bypassBeforeUnload = false; leavingByBack = false; });
      // Back while dirty: the sentinel entry popped to the real page entry, so
      // Stay re-arms the sentinel and Discard walks back off the page exactly
      // once (each popstate triggers one more back until the document exits).
      window.addEventListener("popstate", (event) => {
        if (leavingByBack) { history.back(); return; }
        if (event.state?.wfGuard) return; // forward traversal onto the sentinel
        if (!exitGuardActive() || bypassBeforeUnload) return;
        history.pushState(WF_GUARD_STATE, "", location.href);
        if (leaveDialog()?.open || dirtyDialog()?.open) return; // one decision at a time
        openLeaveDialog(null);
      });
      // Same-tab in-app navigation only. Modified clicks, target=_blank,
      // downloads, hash-only jumps, and HTMX-owned anchors keep native behavior.
      document.addEventListener("click", (event) => {
        if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        const anchor = event.target instanceof Element ? event.target.closest("a[href]") : null;
        if (!anchor) return;
        if (anchor.target && anchor.target !== "_self") return;
        if (anchor.hasAttribute("download")) return;
        if (anchor.closest("[hx-get],[hx-post],[hx-put],[hx-patch],[hx-delete]")) return;
        const href = anchor.getAttribute("href") || "";
        if (!href || href.startsWith("#")) return;
        let url;
        try { url = new URL(anchor.href); } catch { return; }
        if (url.origin !== location.origin) return;
        if (url.pathname === location.pathname && url.search === location.search) return;
        if (!exitGuardActive() || bypassBeforeUnload) return;
        event.preventDefault();
        openLeaveDialog(anchor.href);
      }, true);
      // Stay closes with no navigation and no persistence; Discard leaves
      // without persisting and suppresses beforeunload for that navigation.
      document.addEventListener("click", (event) => {
        const target = event.target instanceof Element ? event.target : null;
        if (!target) return;
        if (target.closest("[data-workflow-leave-stay]")) {
          event.preventDefault();
          leaveDialog()?.close("stay");
        } else if (target.closest("[data-workflow-leave-discard]")) {
          event.preventDefault();
          const href = pendingLeaveHref;
          leaveDialog()?.close("discard");
          if (href) { bypassBeforeUnload = true; location.assign(href); }
          else leaveByBack();
        }
      });
      document.addEventListener("close", (event) => {
        if (event.target !== leaveDialog()) return;
        pendingLeaveHref = null;
        const focusTarget = leaveReturnFocus;
        leaveReturnFocus = null;
        if (focusTarget instanceof Element && focusTarget.isConnected) focusTarget.focus();
      });

      captureBaseline();
      syncExitGuard();
