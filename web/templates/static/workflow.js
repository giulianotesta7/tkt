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

      // Transient mutation feedback (e.g. "Added a step.") fades out on its own so
      // it reserves no permanent space in the panel.
      let liveTimer = null;
      const fadeLive = () => {
        const node = document.querySelector("[data-workflow-live]");
        if (!node) return;
        if (liveTimer) clearTimeout(liveTimer);
        node.style.opacity = "1";
        liveTimer = setTimeout(() => { node.style.opacity = "0"; }, 2600);
      };
      document.addEventListener("htmx:afterSwap", () => fadeLive());
      document.addEventListener("htmx:afterRequest", () => fadeLive());
      if (document.querySelector("[data-workflow-live]")) fadeLive();
    })();