/*
 * Ticket creation behaviour (issue #391).
 *
 * Two progressive enhancements, one file, because they are the two halves of
 * the same flow:
 *
 *  1. The category and priority fields of the create form are NATIVE SELECTS.
 *     Without a script they submit as they are — grouped <optgroup> for the
 *     category, plain options for the priority — so creating a ticket never
 *     depends on JavaScript. This file replaces each one with the ticket
 *     detail's listbox look: a button showing the current value and a popover
 *     of options, which is the same component the detail uses for its own
 *     properties.
 *
 *     Nothing here submits. A closed picker has no key handler at all, and a
 *     pick only writes the select's value and fires `change` on it, so the
 *     native field is still the single source of the submission and no request
 *     is ever issued by a change (#232's contract, kept by #391).
 *
 *  2. The two-step catalog's search box filters the cards live, by the same
 *     rule the server applies for `?q` (category, desk, or department name),
 *     hiding the departments left with no match. With no script the server has
 *     already rendered exactly that filter.
 *
 *  3. Category suggestions (issue #416, S2) are shortcuts for the pickers that
 *     are already here: a suggested chip writes its category into the form's own
 *     select, and a suggested tile is an ordinary link into the same form. A
 *     suggested category is offered once, so its department card stays hidden
 *     while the Suggested group names it.
 */
(() => {
  "use strict";

  // ---------------------------------------------------------------------
  // Native select -> listbox picker
  // ---------------------------------------------------------------------

  const isPriority = (select) => select.id === "priority";

  /** The label text a select is named by, so the popover keeps the same name. */
  const labelText = (select) => {
    const labelledBy = select.getAttribute("aria-labelledby");
    const source = labelledBy ? document.getElementById(labelledBy) : null;
    if (source) return source.textContent.trim();
    const label = select.labels && select.labels.length ? select.labels[0] : null;
    return label ? label.textContent.trim() : "";
  };

  const selectedLabel = (select) =>
    select.selectedOptions.length ? select.selectedOptions[0].textContent.trim() : "";

  const kindOf = (select) => (isPriority(select) ? "priority" : "category");

  const markFor = (select, value) => {
    if (isPriority(select)) {
      const dot = document.createElement("span");
      // Priority values ARE the level names the stylesheet paints
      // (.priority-dot.critical and friends), so no map is duplicated here.
      dot.className = "priority-dot " + value;
      dot.setAttribute("aria-hidden", "true");
      return dot;
    }
    return null;
  };

  const buildOption = (select, option, valueID) => {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("role", "option");
    button.className = "listbox-option";
    button.value = option.value;
    button.setAttribute("aria-selected", option.selected ? "true" : "false");
    if (option.disabled) button.setAttribute("aria-disabled", "true");
    const mark = markFor(select, option.value);
    if (mark) button.appendChild(mark);
    const label = document.createElement("span");
    label.className = "listbox-option-label";
    label.id = valueID + "-" + option.value;
    label.textContent = option.textContent.trim();
    button.appendChild(label);
    if (option.selected) button.appendChild(check());
    return button;
  };

  const check = () => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "listbox-check");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "14");
    svg.setAttribute("height", "14");
    svg.setAttribute("fill", "none");
    svg.setAttribute("stroke", "currentColor");
    svg.setAttribute("stroke-width", "2.2");
    svg.setAttribute("stroke-linecap", "round");
    svg.setAttribute("stroke-linejoin", "round");
    svg.setAttribute("aria-hidden", "true");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "m5 12.5 4.5 4.5L19 7");
    svg.appendChild(path);
    return svg;
  };

  const chevron = () => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "listbox-chevron");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "14");
    svg.setAttribute("height", "14");
    svg.setAttribute("fill", "none");
    svg.setAttribute("stroke", "currentColor");
    svg.setAttribute("stroke-width", "1.8");
    svg.setAttribute("stroke-linecap", "round");
    svg.setAttribute("stroke-linejoin", "round");
    svg.setAttribute("aria-hidden", "true");
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", "M6 9l6 6 6-6");
    svg.appendChild(path);
    return svg;
  };

  /** The popover's search field, present only where the field has many values. */
  const searchField = (placeholder) => {
    if (!placeholder) return null;
    const input = document.createElement("input");
    input.type = "search";
    input.className = "listbox-search";
    input.placeholder = placeholder;
    input.setAttribute("aria-label", placeholder);
    input.autocomplete = "off";
    return input;
  };

  // A picker owns: the wrapper, the select it mirrors, the opener button, the
  // popover, the value span the button shows and the option buttons in order.
  const pickers = [];

  const enhance = (wrapper) => {
    const select = wrapper.querySelector("select");
    if (!select || wrapper.dataset.enhanced) return;
    wrapper.dataset.enhanced = "1";

    const kind = kindOf(select);
    const name = labelText(select);
    const id = "create-" + kind + "-picker";
    const placeholder = wrapper.dataset.pickerSearch || "";
    const emptyText = wrapper.dataset.pickerEmpty || "";

    const root = document.createElement("div");
    root.className = "listbox create-listbox";

    const button = document.createElement("button");
    button.type = "button";
    button.id = id;
    button.className = "listbox-button";
    button.setAttribute("aria-haspopup", "listbox");
    button.setAttribute("aria-expanded", "false");
    button.setAttribute("aria-controls", id + "-options");
    button.setAttribute("aria-labelledby", id + "-label " + id + "-value");
    const current = document.createElement("span");
    current.className = "listbox-current";
    current.id = id + "-value";
    current.textContent = selectedLabel(select);
    const label = document.createElement("span");
    label.id = id + "-label";
    label.className = "visually-hidden";
    label.textContent = name;
    const dot = markFor(select, select.value);
    if (dot) button.appendChild(dot);
    button.appendChild(label);
    button.appendChild(current);
    button.appendChild(chevron());

    const popover = document.createElement("div");
    popover.className = "listbox-popover listbox-popover-wide";
    popover.id = id + "-options";
    const search = searchField(placeholder);
    if (search) popover.appendChild(search);

    const list = document.createElement("div");
    list.className = "listbox-options";
    list.setAttribute("role", "listbox");
    list.setAttribute("aria-label", name);
    let groupIndex = 0;
    for (const child of select.children) {
      if (child instanceof HTMLOptGroupElement) {
        groupIndex += 1;
        const group = document.createElement("div");
        group.className = "listbox-group";
        group.setAttribute("role", "group");
        const heading = document.createElement("div");
        heading.className = "listbox-group-label";
        heading.id = id + "-group-" + groupIndex;
        heading.textContent = child.label;
        group.setAttribute("aria-labelledby", heading.id);
        group.appendChild(heading);
        for (const option of child.children) {
          group.appendChild(buildOption(select, option, id));
        }
        list.appendChild(group);
        continue;
      }
      if (!(child instanceof HTMLOptionElement)) continue;
      // The category picker's empty placeholder is a control instruction, not
      // a choosable value, so it never becomes an option.
      if (kind === "category" && child.value === "") continue;
      list.appendChild(buildOption(select, child, id));
    }
    popover.appendChild(list);

    const empty = document.createElement("p");
    empty.className = "listbox-empty";
    empty.hidden = true;
    empty.textContent = emptyText;
    popover.appendChild(empty);

    // The native select stays in the form (it is the submitted field and the
    // no-script control) but leaves the tab order and the accessibility tree to
    // the button and the option buttons that replaced it.
    select.setAttribute("aria-hidden", "true");
    select.tabIndex = -1;
    wrapper.appendChild(root);
    root.appendChild(button);
    root.appendChild(popover);

    pickers.push({ wrapper, select, kind, root, button, popover, search, list, empty, current });
  };

  const visibleOptions = (picker) =>
    Array.from(picker.list.querySelectorAll(".listbox-option")).filter(
      (option) => !option.hidden && option.getAttribute("aria-disabled") !== "true",
    );

  const closePicker = (picker, restoreFocus) => {
    if (!picker || picker.popover.dataset.open !== "1") return;
    delete picker.popover.dataset.open;
    picker.button.setAttribute("aria-expanded", "false");
    if (restoreFocus) picker.button.focus();
    return picker;
  };

  const closeAll = (restoreFocus) => {
    let closed = null;
    for (const picker of pickers) {
      if (picker.popover.dataset.open === "1") {
        closePicker(picker, restoreFocus);
        closed = picker;
      }
    }
    return closed;
  };

  const openPicker = (picker) => {
    closeAll(false);
    picker.popover.dataset.open = "1";
    picker.button.setAttribute("aria-expanded", "true");
    if (picker.search) {
      picker.search.value = "";
      filterPicker(picker);
      picker.search.focus();
      return;
    }
    const selected =
      picker.list.querySelector('[aria-selected="true"]') || visibleOptions(picker)[0];
    if (selected) selected.focus();
  };

  // The filter is the server's own rule (catalogMatches): a category matches
  // when the query appears in its name, in the desk that holds it, or in the
  // department above that desk — which is why a group whose heading matches
  // keeps ALL of its options, exactly as the service catalog does.
  const filterPicker = (picker) => {
    const query = picker.search ? picker.search.value.trim().toLowerCase() : "";
    let visible = 0;
    for (const group of picker.list.querySelectorAll(".listbox-group")) {
      const heading = group.querySelector(".listbox-group-label");
      const groupHit =
        query !== "" && heading !== null && heading.textContent.toLowerCase().includes(query);
      let shown = 0;
      for (const option of group.querySelectorAll(".listbox-option")) {
        const label = option.querySelector(".listbox-option-label");
        const hit =
          query === "" ||
          groupHit ||
          (label !== null && label.textContent.toLowerCase().includes(query));
        option.hidden = !hit;
        if (hit) shown += 1;
      }
      group.hidden = shown === 0;
      visible += shown;
    }
    const loose = Array.from(picker.list.children).filter((child) =>
      child.classList.contains("listbox-option"),
    );
    for (const option of loose) {
      const label = option.querySelector(".listbox-option-label");
      const hit = query === "" || (label && label.textContent.toLowerCase().includes(query));
      option.hidden = !hit;
      if (hit) visible += 1;
    }
    picker.empty.hidden = visible !== 0 || query === "";
  };

  // A pick writes the native select and reports it, so the draft script and the
  // form both see an ordinary value change. It submits nothing.
  const pick = (picker, option) => {
    if (option.getAttribute("aria-disabled") === "true") return;
    picker.select.value = option.value;
    for (const candidate of picker.list.querySelectorAll(".listbox-option")) {
      candidate.setAttribute("aria-selected", candidate === option ? "true" : "false");
      const existing = candidate.querySelector(".listbox-check");
      if (candidate === option && !existing) candidate.appendChild(check());
      if (candidate !== option && existing) existing.remove();
    }
    picker.current.textContent = option.querySelector(".listbox-option-label").textContent.trim();
    if (picker.kind === "priority") {
      const mark = picker.button.querySelector(".priority-dot");
      if (mark) mark.className = "priority-dot " + option.value;
    }
    closePicker(picker, true);
    picker.select.dispatchEvent(new Event("change", { bubbles: true }));
  };

  const moveInPicker = (picker, step) => {
    const options = visibleOptions(picker);
    if (options.length === 0) return;
    const current = options.indexOf(document.activeElement);
    const next = current < 0 ? 0 : (current + step + options.length) % options.length;
    options[next].focus();
  };

  const activePicker = () => pickers.find((picker) => picker.popover.dataset.open === "1") || null;

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const opener = target.closest(".create-listbox > .listbox-button");
    if (opener) {
      const picker = pickers.find((candidate) => candidate.button === opener);
      if (!picker) return;
      event.preventDefault();
      if (picker.popover.dataset.open === "1") closePicker(picker, true);
      else openPicker(picker);
      return;
    }
    const option = target.closest(".create-listbox .listbox-option");
    if (option) {
      const picker = pickers.find((candidate) => candidate.root.contains(option));
      if (picker) {
        event.preventDefault();
        pick(picker, option);
      }
      return;
    }
    if (activePicker()) closeAll(false);
  });

  document.addEventListener("keydown", (event) => {
    const picker = activePicker();
    if (!picker) return;
    if (event.key === "Escape") {
      event.preventDefault();
      closePicker(picker, true);
      return;
    }
    const inList = picker.list.contains(document.activeElement);
    if (event.key === "ArrowDown") {
      event.preventDefault();
      if (inList) moveInPicker(picker, 1);
      else {
        const options = visibleOptions(picker);
        if (options[0]) options[0].focus();
      }
      return;
    }
    if (event.key === "ArrowUp" && inList) {
      event.preventDefault();
      const options = visibleOptions(picker);
      const index = options.indexOf(document.activeElement);
      if (index <= 0) {
        if (picker.search) picker.search.focus();
        else picker.button.focus();
        return;
      }
      options[index - 1].focus();
      return;
    }
    if (event.target instanceof Element && event.target.classList.contains("listbox-search")) {
      // Typing in the search is a filter, never a submission: Enter moves to the
      // first match, which keeps the pick an explicit act.
      if (event.key === "Enter") {
        event.preventDefault();
        const options = visibleOptions(picker);
        if (options[0]) options[0].focus();
      }
      return;
    }
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof Element) || !target.classList.contains("listbox-search")) return;
    const picker = pickers.find((candidate) => candidate.search === target);
    if (picker) filterPicker(picker);
  });

  for (const wrapper of document.querySelectorAll("[data-create-picker]")) enhance(wrapper);

  // The category a catalog card opens, read from the link the card already is:
  // the Suggested group and the departments have to agree on one identity without
  // the markup carrying a second attribute for it.
  const catalogCardID = (card) => {
    if (!(card instanceof HTMLAnchorElement)) return "";
    try {
      return new URL(card.href, window.location.href).searchParams.get("category_id") || "";
    } catch {
      return "";
    }
  };

  // ---------------------------------------------------------------------
  // Two-step catalog: the live half of the search
  // ---------------------------------------------------------------------

  const catalog = document.querySelector("[data-create-catalog]");
  const catalogSearch = document.querySelector("[data-create-catalog-search]");

  if (catalog && catalogSearch) {
    const groups = Array.from(catalog.querySelectorAll(".create-catalog-department-group"));
    const sections = groups.map((group) => ({
      group,
      name: (group.querySelector(".create-catalog-department") || group).textContent.toLowerCase(),
      cards: Array.from(group.querySelectorAll(".create-card")).map((card) => ({
        card,
        id: catalogCardID(card),
        name: (
          card.querySelector(".create-card-name").textContent +
          " " +
          card.querySelector(".create-card-desk").textContent
        ).toLowerCase(),
      })),
    }));
    const empty = document.querySelector(".create-catalog-empty");

    // The categories the Suggested group already names (issue #416, S2). It is
    // read from the DOM on every pass because the search refreshes the group
    // itself, so the set changes while the page stays where it is.
    const suggestedIDs = () => {
      const ids = new Set();
      for (const tile of catalog.querySelectorAll("[data-suggest-category]")) {
        ids.add(tile.getAttribute("data-suggest-category"));
      }
      return ids;
    };

    const filter = () => {
      // The rule is the server's own (catalogMatches): a card matches when the
      // query appears in the category, its desk, or its department name.
      const query = catalogSearch.value.trim().toLowerCase();
      const suggested = suggestedIDs();
      let named = 0;
      for (const section of sections) {
        const sectionHit = query === "" || section.name.includes(query);
        for (const card of section.cards) {
          card.hit = !suggested.has(card.id) && (sectionHit || card.name.includes(query));
          if (card.hit) named += 1;
        }
      }
      // A description that names no card still leaves a usable catalog under the
      // Suggested group: the departments are the manual way to choose, so they
      // stay whole instead of collapsing into the no-match message (S3).
      const keepAll = suggested.size > 0 && named === 0;
      let matches = 0;
      for (const section of sections) {
        let shown = 0;
        for (const card of section.cards) {
          const visible = !suggested.has(card.id) && (keepAll || card.hit);
          card.card.hidden = !visible;
          if (visible) shown += 1;
        }
        section.group.hidden = shown === 0;
        matches += shown;
      }
      if (empty) empty.hidden = matches !== 0 || query === "";
    };

    catalogSearch.addEventListener("input", filter);
    // A swap replaces the Suggested group, so the duplicates it creates move and
    // the filter has to run again: the group and the departments are one view.
    document.body.addEventListener("htmx:afterSwap", (event) => {
      if (event.target instanceof Element && event.target.id === "create-catalog-suggestions") {
        filter();
      }
    });
    filter();
  }

  // ---------------------------------------------------------------------
  // Suggested categories (issue #416, S2)
  // ---------------------------------------------------------------------

  // A suggested category is a shortcut for the picker, never a second way to
  // submit: it writes the category into the native select through the picker's
  // own pick, so the listbox, the change event and the form all behave exactly
  // as they do when the requester chooses from the list below it.
  const chooseCategory = (value) => {
    const select = document.getElementById("category_id");
    if (!select || !value) return;
    const picker = pickers.find((candidate) => candidate.select === select);
    const option =
      picker &&
      Array.from(picker.list.querySelectorAll(".listbox-option")).find(
        (candidate) => candidate.value === value,
      );
    if (picker && option) {
      pick(picker, option);
      return;
    }
    select.value = value;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  };

  // The chip a suggestion was chosen from stays marked, and a refresh of the
  // chips restores the mark from the field that actually submits.
  const markSelectedChip = () => {
    const select = document.getElementById("category_id");
    const value = select ? select.value : "";
    for (const chip of document.querySelectorAll(".create-suggestion-chip")) {
      const on = value !== "" && chip.getAttribute("data-suggest-category") === value;
      chip.setAttribute("aria-pressed", on ? "true" : "false");
    }
  };

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const chip = target.closest(".create-suggestion-chip");
    if (!chip) return;
    event.preventDefault();
    chooseCategory(chip.getAttribute("data-suggest-category"));
    markSelectedChip();
  });

  document.body.addEventListener("htmx:afterSwap", (event) => {
    if (event.target instanceof Element && event.target.closest(".create-suggestions")) {
      markSelectedChip();
    }
  });
})();
