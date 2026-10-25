/*
 * Ticket detail behaviour (issue #386).
 *
 * Every listener is delegated from the document and every binding is
 * idempotent, because this file loads once while #ticket-detail is re-rendered
 * on every HTMX swap (outerHTML) and on every page load. The inline scripts
 * this replaced re-ran per swap; nothing here registers a second listener for
 * the same element.
 *
 * Contracts this file must never break:
 *  - #232: nothing here mutates a ticket on `change`. The property pickers are
 *    submit buttons of their own form, and a CLOSED picker has no key or wheel
 *    handler at all, so the arrows and the wheel cannot pick anything.
 *  - #352: a refused hold (422/409) still swaps the re-rendered detail in, so
 *    the error and the typed reason survive.
 *  - #261: the composer draft is never touched by the swaps this file triggers.
 */
(() => {
  "use strict";

  const detail = () => document.getElementById("ticket-detail");

  /** Opens one of the detail's <dialog> elements and focuses a field. */
  const showDialog = (dialog, focus) => {
    if (!dialog) return;
    if (dialog.showModal) dialog.showModal();
    else dialog.setAttribute("open", "");
    if (focus) focus.focus();
  };

  /** Closes a <dialog> through either the modern or the fallback path. */
  const hideDialog = (dialog) => {
    if (!dialog) return;
    if (dialog.open) dialog.close();
    else dialog.removeAttribute("open");
  };

  // ---------------------------------------------------------------------
  // Property pickers (S5, maintainer decision L2)
  //
  // The current value is a button and every option is a submit button of the
  // surrounding form, so a pick is a native POST and Enter on a focused option
  // activates it natively. This script only opens the popover, moves the
  // highlight between the option buttons and filters the assignee list.
  // ---------------------------------------------------------------------
  let openPicker = null;

  const pickerOptions = (root) => Array.from(root.querySelectorAll(".listbox-option"));

  const closePicker = (restoreFocus) => {
    if (!openPicker) return;
    const root = openPicker;
    const button = root.querySelector(".listbox-button");
    const popover = root.querySelector(".listbox-popover");
    if (popover) delete popover.dataset.open;
    if (button) {
      button.setAttribute("aria-expanded", "false");
      if (restoreFocus) button.focus();
    }
    openPicker = null;
  };

  const filterPickerOptions = (root) => {
    const search = root.querySelector(".listbox-search");
    if (!search) return;
    const query = search.value.trim().toLowerCase();
    for (const option of pickerOptions(root)) {
      option.hidden = query !== "" && !option.textContent.toLowerCase().includes(query);
    }
  };

  const openPickerFor = (root) => {
    const button = root.querySelector(".listbox-button");
    const popover = root.querySelector(".listbox-popover");
    if (!button || !popover) return;
    closePicker(false);
    openPicker = root;
    button.setAttribute("aria-expanded", "true");
    popover.dataset.open = "1";
    const search = root.querySelector(".listbox-search");
    if (search) {
      search.value = "";
      filterPickerOptions(root);
      search.focus();
      return;
    }
    const selected = popover.querySelector('[aria-selected="true"]') || pickerOptions(root)[0];
    if (selected) selected.focus();
  };

  // moveInPicker walks the visible option buttons, so the highlight follows the
  // filtered list. It never chooses anything: Enter activates the focused
  // option button natively, which is the explicit act.
  const moveInPicker = (root, step) => {
    const visible = pickerOptions(root).filter((option) => !option.hidden);
    if (visible.length === 0) return;
    const current = visible.indexOf(document.activeElement);
    const next = current < 0 ? 0 : (current + step + visible.length) % visible.length;
    visible[next].focus();
  };

  // ---------------------------------------------------------------------
  // Title guard (issue #232)
  //
  // A whole-detail swap destroys a half-typed title, so a sibling pick or
  // transition is intercepted while the title is dirty and the operator chooses
  // whether to save or discard the title first. The two are separate requests on
  // separate endpoints; the title form only ever carries its own field.
  // ---------------------------------------------------------------------
  let titleState = null;

  const titleGuard = () => {
    const input = document.getElementById("ticket-title");
    const root = detail();
    const dialog = document.getElementById("ticket-title-dialog");
    if (!input || !input.form || !root || !dialog) return null;
    if (titleState && titleState.input === input) return titleState;
    titleState = {
      input,
      form: input.form,
      dialog,
      saveButton: input.form.querySelector(".title-save"),
      discardButton: dialog.querySelector("[data-title-discard]"),
      saveContinueButton: dialog.querySelector("[data-title-save-continue]"),
      initial: input.value,
      pending: null,
      returnFocus: null,
    };
    const syncSave = () => {
      if (titleState && titleState.saveButton) {
        titleState.saveButton.hidden = titleState.input.value === titleState.initial;
      }
    };
    input.addEventListener("input", syncSave);
    syncSave();
    return titleState;
  };

  const titleDirty = () => {
    const state = titleGuard();
    return state ? state.input.value !== state.initial : false;
  };

  const closeTitleDialog = (restoreFocus) => {
    const state = titleGuard();
    if (!state) return;
    hideDialog(state.dialog);
    const target = state.returnFocus;
    state.returnFocus = null;
    if (restoreFocus && target && target.isConnected) target.focus();
  };

  const openTitleDialog = () => {
    const state = titleGuard();
    if (!state) return;
    const active = document.activeElement;
    const root = detail();
    state.returnFocus =
      active && root && root.contains(active) && active !== root ? active : state.input;
    showDialog(state.dialog, state.discardButton);
  };

  // formAnchor names the sibling action a dirty title is holding back, so the
  // replay after a successful title save can resolve the SAME action in the
  // freshly swapped fragment: a picker by its opener id, a header transition by
  // the state it moves to.
  const formAnchor = (form) => {
    for (const id of ["ticket-priority", "assign-user"]) {
      if (form.querySelector("#" + id)) return { picker: id };
    }
    const to = form.querySelector('input[name="to"]');
    if (to) return { transition: to.value };
    return null;
  };

  const resolveAnchor = (anchor) => {
    if (!anchor) return null;
    let control = null;
    if (anchor.picker) {
      control = document.getElementById(anchor.picker);
    } else if (anchor.transition) {
      control = Array.from(document.querySelectorAll('#ticket-detail input[name="to"]')).find(
        (candidate) => candidate.value === anchor.transition,
      );
    }
    return control ? control.closest("form") : null;
  };

  // guardedForm reports whether a form is one of the actions the title guard
  // holds back: the property pickers and the header transition buttons, the
  // direct replacements of the three Apply buttons #232 introduced the guard
  // for. Dialog forms carry copy the operator typed deliberately, so they are
  // not intercepted.
  const guardedForm = (form) =>
    form instanceof HTMLFormElement && form.closest("#ticket-detail") && !form.closest("dialog");

  // guardSubmit remembers the held-back action: which form (by anchor), every
  // named field it would have sent, and WHICH submit button was pressed. The
  // button matters because a picker's field lives on the option button itself,
  // so a replay that only filled inputs would post an empty body.
  const guardSubmit = (form, submitter) => {
    if (!titleDirty() || !guardedForm(form)) return false;
    const state = titleGuard();
    state.pending = {
      anchor: formAnchor(form),
      entries: collectEntries(form),
      submitter: submitter ? { name: submitter.name, value: submitter.value } : null,
    };
    openTitleDialog();
    return true;
  };

  // submitterFor resolves the equivalent button in the freshly swapped
  // fragment, so the replay submits exactly the field the operator picked.
  // replayPending submits the held-back action in the freshly swapped fragment:
  // every named field is restored, then the equivalent submit button is clicked
  // (a pick carries its field on the option button, and htmx reads it from that
  // click; a plain submit button falls back to the form's own submit, where the
  // field is a hidden input).
  const replayPending = (pending) => {
    const form = resolveAnchor(pending && pending.anchor);
    if (!form) return;
    for (const [name, value] of pending.entries) {
      const control = form.elements.namedItem(name);
      if (control instanceof Element && "value" in control) control.value = value;
    }
    if (window.htmx && typeof window.htmx.process === "function") window.htmx.process(form);
    const submitter = submitterFor(form, pending);
    if (submitter) submitter.click();
    else form.requestSubmit();
  };

  const submitterFor = (form, pending) => {
    const pressed = pending && pending.submitter;
    if (!pressed || !pressed.name) return undefined;
    const match = form.querySelector(
      `button[name="${pressed.name}"][value="${pressed.value}"], input[type="submit"][name="${pressed.name}"][value="${pressed.value}"]`,
    );
    return match || undefined;
  };

  // ---------------------------------------------------------------------
  // Dialogs the header opens: the reason dialog and the hold dialog.
  // ---------------------------------------------------------------------
  const syncTransitionDialog = (openTo) => {
    const dialog = document.getElementById("ticket-transition-dialog");
    if (!dialog) return null;
    const hidden = dialog.querySelector('input[name="to"]');
    if (openTo && hidden) hidden.value = openTo;
    const opener = hidden
      ? document.querySelector(`#ticket-detail [data-transition-open="${hidden.value}"]`)
      : null;
    const submit = dialog.querySelector('button[type="submit"]');
    if (opener && submit) submit.textContent = opener.textContent.trim();
    return dialog;
  };

  // syncAfterSwap re-syncs the freshly swapped fragment: a dialog the server
  // marked "open on load" (a refused submission) opens with its error, and the
  // dirty-title guard re-reads the new input.
  const syncAfterSwap = () => {
    for (const dialog of document.querySelectorAll("#ticket-detail dialog[data-open-on-load]")) {
      showDialog(dialog, dialog.querySelector("textarea, input"));
    }
    syncTransitionDialog();
    // titleGuard() keyed the state to the input element: a detail swap renders a
    // new one and rebuilds the baseline, while an unrelated swap (the shell's
    // notification bell, for instance) must NOT reset it — that would silently
    // forget a half-typed title and let the next pick destroy it.
    titleGuard();
  };

  // A refused hold or transition response re-renders the detail carrying the
  // error and the typed reason. htmx does not swap error responses by default,
  // so the detail admits them for the dialog forms only.
  document.addEventListener("htmx:beforeSwap", (event) => {
    const config = event.detail && event.detail.requestConfig;
    const source = config && config.elt;
    if (!(source instanceof Element)) return;
    const form = source.closest("form");
    if (!form || !form.closest("dialog")) return;
    const status = event.detail.xhr ? event.detail.xhr.status : 0;
    if (status === 422 || status === 409) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  });
  document.addEventListener("htmx:afterSettle", syncAfterSwap);

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;

    const pickerButton = target.closest("[data-listbox] > .listbox-button");
    if (pickerButton) {
      const root = pickerButton.closest("[data-listbox]");
      if (openPicker === root) closePicker(true);
      else openPickerFor(root);
      return;
    }
    if (openPicker && !openPicker.contains(target)) closePicker(false);

    const transition = target.closest("[data-transition-open]");
    if (transition) {
      const dialog = syncTransitionDialog(transition.dataset.transitionOpen);
      const field = dialog && dialog.querySelector("#state-reason");
      if (field) field.value = "";
      showDialog(dialog, field);
      return;
    }
    if (target.closest("[data-transition-cancel]")) {
      const dialog = target.closest("dialog");
      const to = dialog && dialog.querySelector('input[name="to"]');
      hideDialog(dialog);
      const opener =
        to && document.querySelector(`#ticket-detail [data-transition-open="${to.value}"]`);
      if (opener) opener.focus();
      return;
    }

    if (target.closest("[data-hold-open]")) {
      const dialog = document.getElementById("ticket-hold-dialog");
      showDialog(dialog, dialog && dialog.querySelector("#hold-reason"));
      return;
    }
    if (target.closest("[data-hold-cancel]")) {
      hideDialog(target.closest("dialog"));
      const opener = document.querySelector("#ticket-detail [data-hold-open]");
      if (opener) opener.focus();
      return;
    }

    // The title guard intercepts a sibling pick or transition while the title is
    // dirty. The dialog itself and the title form are never intercepted.
    if (target.closest("#ticket-title-dialog") || target.closest(".title-edit")) return;
    const submit = target.closest("button[type=submit]");
    if (!submit || !submit.form) return;
    if (!titleDirty() || !guardedForm(submit.form)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    guardSubmit(submit.form, submit);
  });

  // A picker option is a submit button, so a dirty title holds back the
  // keyboard's Enter too — the same interception, one event earlier.
  document.addEventListener(
    "submit",
    (event) => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      if (form.id === "ticket-title") return;
      if (!titleDirty() || !guardedForm(form)) return;
      event.preventDefault();
      guardSubmit(form, event.submitter);
    },
    true,
  );

  const collectEntries = (form) => {
    const entries = [];
    for (const control of form.elements) {
      if (control.name) entries.push([control.name, control.value]);
    }
    return entries;
  };

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const state = titleGuard();
    if (!state) return;

    if (target.closest("[data-title-discard]")) {
      state.input.value = state.initial;
      if (state.saveButton) state.saveButton.hidden = true;
      const pending = state.pending;
      state.pending = null;
      closeTitleDialog(false);
      replayPending(pending);
      return;
    }
    if (!target.closest("[data-title-save-continue]")) return;
    const pending = state.pending;
    state.pending = null;
    // Validate BEFORE arming the replay listener: requestSubmit() runs
    // constraint validation, so an invalid title would issue no request and
    // leave the one-shot listener armed for a later, unrelated save.
    if (!state.form.checkValidity()) {
      closeTitleDialog(false);
      state.input.focus();
      state.form.reportValidity();
      return;
    }
    closeTitleDialog(false);
    const replay = (requestEvent) => {
      state.form.removeEventListener("htmx:afterRequest", replay);
      if (!requestEvent.detail || requestEvent.detail.successful !== true) return;
      replayPending(pending);
    };
    state.form.addEventListener("htmx:afterRequest", replay);
    state.form.requestSubmit();
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && openPicker) {
      closePicker(true);
      event.preventDefault();
      return;
    }
    if (!openPicker || !(event.target instanceof Element)) return;
    if (event.key === "ArrowDown") {
      moveInPicker(openPicker, 1);
      event.preventDefault();
    } else if (event.key === "ArrowUp") {
      moveInPicker(openPicker, -1);
      event.preventDefault();
    }
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof Element) || !target.classList.contains("listbox-search")) return;
    const root = target.closest("[data-listbox]");
    if (root) filterPickerOptions(root);
  });

  // Typing in the assignee search must never submit the form: the search is a
  // script-only filter, and Enter there would post the form's default option —
  // the accidental mutation #232 closed. Enter moves to the first visible
  // option instead, so the pick stays explicit.
  document.addEventListener(
    "keydown",
    (event) => {
      const target = event.target;
      if (!(target instanceof Element) || !target.classList.contains("listbox-search")) return;
      if (event.key !== "Enter" && event.key !== "Escape") return;
      event.preventDefault();
      if (event.key === "Escape") {
        closePicker(true);
        return;
      }
      const root = target.closest("[data-listbox]");
      const first = root ? pickerOptions(root).find((option) => !option.hidden) : null;
      if (first) first.focus();
    },
    true,
  );

  // ---------------------------------------------------------------------
  // The properties panel folds on phones (S7), the way the queue's filters do:
  // the server renders it open so a wide screen never hides a control, and this
  // closes it under 640px until the operator opens it themselves.
  // ---------------------------------------------------------------------
  const narrow = window.matchMedia("(max-width: 640px)");
  const foldProperties = () => {
    for (const details of document.querySelectorAll("details.detail-properties")) {
      if (narrow.matches && !details.dataset.userOpened) details.open = false;
    }
  };
  document.addEventListener(
    "toggle",
    (event) => {
      const details = event.target;
      if (
        details instanceof HTMLDetailsElement &&
        details.classList.contains("detail-properties") &&
        details.open
      ) {
        details.dataset.userOpened = "1";
      }
    },
    true,
  );
  window.addEventListener("htmx:afterSettle", foldProperties);

  // ---------------------------------------------------------------------
  // The attachment drop zone (S4): the label wraps the same file input, so a
  // click already opens the picker. This only reflects the chosen names beside
  // it and accepts a drop, without touching the form's fields or limits — the
  // server still validates every file.
  // ---------------------------------------------------------------------
  const dropzoneFiles = (input) => {
    const zone = input.closest(".comment-dropzone");
    if (!zone) return;
    const names = zone.querySelector("[data-dropzone-files]");
    if (!names) return;
    const chosen = Array.from(input.files || []).map((file) => file.name);
    names.textContent = chosen.join(", ");
  };

  document.addEventListener("change", (event) => {
    const target = event.target;
    if (target instanceof HTMLInputElement && target.type === "file") dropzoneFiles(target);
  });
  for (const type of ["dragover", "drop"]) {
    document.addEventListener(type, (event) => {
      const zone =
        event.target instanceof Element ? event.target.closest(".comment-dropzone") : null;
      if (!zone) return;
      event.preventDefault();
      if (type !== "drop") return;
      const input = zone.querySelector('input[type="file"]');
      if (!input || !event.dataTransfer || !event.dataTransfer.files.length) return;
      input.files = event.dataTransfer.files;
      dropzoneFiles(input);
    });
  }

  // ---------------------------------------------------------------------
  // Saved replies in the composer (issue #418).
  //
  // The button opens a list the server already filled for this ticket, so
  // inserting a reply costs no request: the text lands at the caret of the
  // comment box and stays editable. The list is placed from the button's own
  // box and clamped to the viewport, because the tools row wraps and a fixed
  // offset would push the popover off a phone screen.
  //
  // Contracts this section keeps: #261 — it only writes into #comment-body,
  // which keeps its preserve handle, so a swap never loses the draft; and the
  // form's native multipart submission — nothing here submits, and Enter in the
  // search field is intercepted before the browser's implicit submission.
  // ---------------------------------------------------------------------
  const savedRepliesRoot = () => {
    const root = document.querySelector("[data-saved-replies]");
    return root instanceof Element ? root : null;
  };

  const savedRepliesPopover = (root) => {
    const popover = root.querySelector(".comment-saved-replies-popover");
    return popover instanceof HTMLElement ? popover : null;
  };

  const savedRepliesIsOpen = (root) => {
    const popover = root ? savedRepliesPopover(root) : null;
    return Boolean(popover && !popover.hidden);
  };

  const savedRepliesButtons = (root) =>
    Array.from(root.querySelectorAll(".comment-saved-replies-option")).filter(
      (option) => !option.hidden,
    );

  const closeSavedReplies = (restoreFocus) => {
    const root = savedRepliesRoot();
    if (!root) return;
    const popover = savedRepliesPopover(root);
    const toggle = root.querySelector("[data-saved-replies-open]");
    if (popover) popover.hidden = true;
    if (toggle) toggle.setAttribute("aria-expanded", "false");
    if (!restoreFocus) return;
    const body = document.getElementById("comment-body");
    if (body instanceof HTMLTextAreaElement) body.focus();
  };

  // placeSavedReplies positions the popover under the button, or above it when
  // there is no room below, and keeps it inside the viewport at any width.
  const placeSavedReplies = (root, toggle, popover) => {
    const margin = 8;
    const gap = 6;
    const button = toggle.getBoundingClientRect();
    const width = Math.min(360, window.innerWidth - margin * 2);
    popover.style.width = width + "px";
    popover.style.left =
      Math.min(Math.max(margin, button.left), window.innerWidth - width - margin) + "px";
    const height = popover.getBoundingClientRect().height;
    const below = button.bottom + gap;
    popover.style.top =
      (below + height + margin <= window.innerHeight
        ? below
        : Math.max(margin, button.top - gap - height)) + "px";
  };

  const filterSavedReplies = (root) => {
    const search = root.querySelector("[data-saved-replies-search]");
    const query = search instanceof HTMLInputElement ? search.value.trim().toLowerCase() : "";
    let visible = 0;
    for (const group of root.querySelectorAll(".comment-saved-replies-group")) {
      let shown = 0;
      for (const option of group.querySelectorAll(".comment-saved-replies-option")) {
        option.hidden = query !== "" && !option.textContent.toLowerCase().includes(query);
        if (!option.hidden) shown++;
      }
      group.hidden = shown === 0;
      visible += shown;
    }
    const empty = root.querySelector("[data-saved-replies-empty]");
    if (empty instanceof HTMLElement) empty.hidden = visible !== 0;
  };

  const openSavedReplies = (root) => {
    const popover = savedRepliesPopover(root);
    const toggle = root.querySelector("[data-saved-replies-open]");
    if (!popover || !toggle) return;
    closePicker(false);
    closeMentions(false);
    toggle.setAttribute("aria-expanded", "true");
    popover.hidden = false;
    const search = root.querySelector("[data-saved-replies-search]");
    if (search instanceof HTMLInputElement) {
      search.value = "";
      filterSavedReplies(root);
      placeSavedReplies(root, toggle, popover);
      search.focus();
      return;
    }
    placeSavedReplies(root, toggle, popover);
    const first = savedRepliesButtons(root)[0];
    if (first) first.focus();
  };

  // insertSavedReply writes the reply at the caret and leaves the caret after
  // it, so a second insertion continues where the first ended. The text is the
  // preview's own content — the server already replaced the placeholders — and
  // the input event is dispatched so anything listening to the draft sees it.
  const insertSavedReply = (option) => {
    const body = document.getElementById("comment-body");
    const preview = option.querySelector(".comment-saved-replies-preview");
    if (!(body instanceof HTMLTextAreaElement) || !preview) return;
    const text = preview.textContent;
    const start = typeof body.selectionStart === "number" ? body.selectionStart : body.value.length;
    const end = typeof body.selectionEnd === "number" ? body.selectionEnd : start;
    body.value = body.value.slice(0, start) + text + body.value.slice(end);
    closeSavedReplies(false);
    body.focus();
    const caret = start + text.length;
    body.setSelectionRange(caret, caret);
    body.dispatchEvent(new Event("input", { bubbles: true }));
  };

  // moveSavedReplies walks the visible options, so the highlight follows the
  // filter. Enter on an option is never intercepted: activating the focused
  // button is the explicit act, exactly like the property pickers.
  const moveSavedReplies = (root, step) => {
    const visible = savedRepliesButtons(root);
    if (visible.length === 0) return;
    const current = visible.indexOf(document.activeElement);
    const next = current < 0 ? 0 : (current + step + visible.length) % visible.length;
    visible[next].focus();
  };

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const root = savedRepliesRoot();
    if (!root) return;
    const toggle = target.closest("[data-saved-replies-open]");
    if (toggle) {
      if (savedRepliesIsOpen(root)) closeSavedReplies(true);
      else openSavedReplies(root);
      return;
    }
    const option = target.closest(".comment-saved-replies-option");
    if (option && root.contains(option)) {
      insertSavedReply(option);
      return;
    }
    if (savedRepliesIsOpen(root) && !root.contains(target)) closeSavedReplies(false);
  });

  document.addEventListener(
    "keydown",
    (event) => {
      const target = event.target;
      const root = savedRepliesRoot();
      if (!(target instanceof Element) || !root || !savedRepliesIsOpen(root)) return;
      if (!root.contains(target)) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        closeSavedReplies(true);
        return;
      }
      const inSearch = target.classList.contains("comment-saved-replies-search");
      if (event.key === "ArrowDown") {
        event.preventDefault();
        moveSavedReplies(root, 1);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        if (inSearch) return;
        moveSavedReplies(root, -1);
        return;
      }
      // Enter in the search field filters, it never submits the composer.
      if (event.key === "Enter" && inSearch) {
        event.preventDefault();
        const first = savedRepliesButtons(root)[0];
        if (first) first.focus();
      }
    },
    true,
  );

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof Element) || !target.classList.contains("comment-saved-replies-search"))
      return;
    const root = savedRepliesRoot();
    if (root) filterSavedReplies(root);
  });

  // A whole-detail swap replaces the composer, so an open popover must not
  // survive it: the freshly rendered one starts hidden, and a resize (a phone
  // turned sideways) re-clamps the one that is open.
  document.addEventListener("htmx:afterSettle", () => closeSavedReplies(false));
  window.addEventListener("resize", () => {
    const root = savedRepliesRoot();
    if (!root || !savedRepliesIsOpen(root)) return;
    const popover = savedRepliesPopover(root);
    const toggle = root.querySelector("[data-saved-replies-open]");
    if (popover && toggle) placeSavedReplies(root, toggle, popover);
  });

  // ---------------------------------------------------------------------
  // Mentions in the composer (issue #418).
  //
  // One candidate list backs both inputs: the typed "@" completion and the
  // Mention button. The list is the server's answer for this ticket, so this
  // script never decides who may be mentioned — it only helps people type a
  // name the server will match, and it always writes "@<display name> " with the
  // trailing space the mention grammar needs to close the name.
  //
  // The highlight moves without taking focus, because the person is still
  // typing in the comment box: the arrows move it, Enter and a click insert it,
  // and Escape closes the list. Contracts this section keeps: #261 — it only
  // writes into #comment-body, which keeps its preserve handle; and the form's
  // native multipart submission — Enter in the search field never submits the
  // composer.
  // ---------------------------------------------------------------------
  let mentionTrigger = null;
  let mentionActive = 0;
  let mentionSuppress = false;

  const mentionsRoot = () => {
    const root = document.querySelector("[data-mentions]");
    return root instanceof Element ? root : null;
  };

  const mentionsPopover = (root) => {
    const popover = root.querySelector(".comment-mentions-popover");
    return popover instanceof HTMLElement ? popover : null;
  };

  const mentionsIsOpen = (root) => {
    const popover = root ? mentionsPopover(root) : null;
    return Boolean(popover && !popover.hidden);
  };

  // mentionOptions walks the candidates the server rendered, in list order.
  const mentionOptions = (root) => Array.from(root.querySelectorAll(".comment-mentions-option"));

  const visibleMentions = (root) => mentionOptions(root).filter((option) => !option.hidden);

  // foldText makes the filter ignore case and accents, so "lopez" finds
  // "Dana López". It only ever compares text the server already chose.
  const foldText = (text) =>
    (text || "")
      .normalize("NFD")
      .replace(/[\u0300-\u036f]/g, "")
      .toLowerCase();

  // highlightMention marks one visible candidate as the one Enter would insert.
  const highlightMention = (root, index) => {
    const visible = visibleMentions(root);
    mentionActive = visible.length === 0 ? 0 : (index + visible.length) % visible.length;
    for (const option of mentionOptions(root)) {
      option.setAttribute("aria-selected", option === visible[mentionActive] ? "true" : "false");
    }
  };

  const filterMentions = (root, query) => {
    const needle = foldText(query.trim());
    for (const option of mentionOptions(root)) {
      option.hidden = needle !== "" && !foldText(option.textContent).includes(needle);
    }
    const empty = root.querySelector("[data-mentions-empty]");
    if (empty instanceof HTMLElement) empty.hidden = visibleMentions(root).length !== 0;
    highlightMention(root, 0);
  };

  // caretPoint measures where the caret sits inside the comment box, so the list
  // opens under the text being typed instead of under the whole field. A mirror
  // carries the same type metrics and wrapping, which is what makes the
  // measurement match what the browser paints.
  const caretPoint = (field) => {
    const rect = field.getBoundingClientRect();
    const style = window.getComputedStyle(field);
    const mirror = document.createElement("div");
    for (const property of [
      "fontFamily",
      "fontSize",
      "fontWeight",
      "fontStyle",
      "letterSpacing",
      "lineHeight",
      "paddingTop",
      "paddingRight",
      "paddingBottom",
      "paddingLeft",
      "borderTopWidth",
      "borderRightWidth",
      "borderBottomWidth",
      "borderLeftWidth",
      "boxSizing",
    ]) {
      mirror.style[property] = style[property];
    }
    mirror.style.position = "absolute";
    mirror.style.top = "0";
    mirror.style.left = "0";
    mirror.style.width = rect.width + "px";
    mirror.style.visibility = "hidden";
    mirror.style.whiteSpace = "pre-wrap";
    mirror.style.overflowWrap = "break-word";
    const caret = typeof field.selectionStart === "number" ? field.selectionStart : 0;
    mirror.textContent = field.value.slice(0, caret);
    const marker = document.createElement("span");
    marker.textContent = field.value.slice(caret) || ".";
    mirror.appendChild(marker);
    document.body.appendChild(mirror);
    const point = {
      left: rect.left + marker.offsetLeft - field.scrollLeft,
      top: rect.top + marker.offsetTop - field.scrollTop,
      height: marker.offsetHeight,
    };
    mirror.remove();
    return point;
  };

  // placeMentions keeps the list inside the viewport at any width: under the
  // anchor when it fits, above it otherwise.
  const placeMentions = (popover, anchor) => {
    const margin = 8;
    const gap = 6;
    const width = Math.min(300, window.innerWidth - margin * 2);
    popover.style.width = width + "px";
    popover.style.left =
      Math.min(Math.max(margin, anchor.left), window.innerWidth - width - margin) + "px";
    const height = popover.getBoundingClientRect().height;
    const below = anchor.top + anchor.height + gap;
    popover.style.top =
      (below + height + margin <= window.innerHeight
        ? below
        : Math.max(margin, anchor.top - gap - height)) + "px";
  };

  const closeMentions = (restoreFocus) => {
    const root = mentionsRoot();
    mentionTrigger = null;
    if (!root) return;
    const popover = mentionsPopover(root);
    const toggle = root.querySelector("[data-mentions-open]");
    if (popover) popover.hidden = true;
    if (toggle) toggle.setAttribute("aria-expanded", "false");
    if (!restoreFocus) return;
    const body = document.getElementById("comment-body");
    if (body instanceof HTMLTextAreaElement) body.focus();
  };

  const showMentions = (root, query, anchor) => {
    const popover = mentionsPopover(root);
    const toggle = root.querySelector("[data-mentions-open]");
    if (!popover || !toggle) return;
    closePicker(false);
    closeSavedReplies(false);
    toggle.setAttribute("aria-expanded", "true");
    popover.hidden = false;
    filterMentions(root, query);
    placeMentions(popover, anchor);
  };

  const openMentionsFromButton = (root) => {
    const toggle = root.querySelector("[data-mentions-open]");
    if (!toggle) return;
    const search = root.querySelector("[data-mentions-search]");
    if (search instanceof HTMLInputElement) search.value = "";
    showMentions(root, "", toggle.getBoundingClientRect());
    if (search instanceof HTMLInputElement) search.focus();
  };

  // mentionBefore returns the "@query" being typed immediately before the
  // caret, or null when the caret is not inside one. The "@" must start a word,
  // which is the boundary the server's grammar matches on, and two spaces in a
  // row end the mention.
  const mentionBefore = (field) => {
    const caret = typeof field.selectionStart === "number" ? field.selectionStart : 0;
    const before = field.value.slice(0, caret);
    const at = before.lastIndexOf("@");
    if (at < 0) return null;
    const query = before.slice(at + 1);
    if (query.includes("\n") || /\s{2}/.test(query)) return null;
    if (at > 0 && /[\p{L}\p{N}]/u.test(before.slice(at - 1, at))) return null;
    return { start: at, query };
  };

  // mentionIsSettled tells a finished mention from one still being typed. Once
  // the query holds a space, it is still a search only while some candidate
  // matches it, and a full name followed by a space is a chosen person. Without
  // this, the words written after a chosen name would reopen the list to say
  // nobody matches.
  const mentionIsSettled = (root, query) => {
    if (!/\s/.test(query)) return false;
    const needle = foldText(query.trim());
    const options = mentionOptions(root);
    if (
      /\s$/.test(query) &&
      options.some((option) => foldText(option.dataset.mentionName) === needle)
    )
      return true;
    return !options.some((option) => foldText(option.textContent).includes(needle));
  };

  const openMentionsWhileTyping = (field) => {
    const root = mentionsRoot();
    if (!root) return;
    const trigger = mentionBefore(field);
    if (!trigger || mentionIsSettled(root, trigger.query)) {
      closeMentions(false);
      return;
    }
    mentionTrigger = trigger;
    showMentions(root, trigger.query, caretPoint(field));
  };

  // insertMention writes "@name " at the caret and replaces the "@query" being
  // typed, so both inputs store the same text. The caret ends after the
  // trailing space, ready for the next word.
  const insertMention = (option) => {
    const body = document.getElementById("comment-body");
    const name = option instanceof HTMLElement ? option.dataset.mentionName : "";
    if (!(body instanceof HTMLTextAreaElement) || !name) return;
    const text = "@" + name + " ";
    const caret = typeof body.selectionStart === "number" ? body.selectionStart : body.value.length;
    const end = typeof body.selectionEnd === "number" ? body.selectionEnd : caret;
    const start = mentionTrigger ? mentionTrigger.start : caret;
    body.value = body.value.slice(0, start) + text + body.value.slice(Math.max(caret, end));
    closeMentions(false);
    body.focus();
    const position = start + text.length;
    body.setSelectionRange(position, position);
    mentionSuppress = true;
    body.dispatchEvent(new Event("input", { bubbles: true }));
    mentionSuppress = false;
  };

  const insertHighlightedMention = (root) => {
    const visible = visibleMentions(root);
    if (visible.length === 0) return false;
    insertMention(visible[mentionActive]);
    return true;
  };

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const root = mentionsRoot();
    if (!root) return;
    const toggle = target.closest("[data-mentions-open]");
    if (toggle) {
      if (mentionsIsOpen(root)) closeMentions(false);
      else openMentionsFromButton(root);
      return;
    }
    const option = target.closest(".comment-mentions-option");
    if (option && root.contains(option)) {
      insertMention(option);
      return;
    }
    if (mentionsIsOpen(root) && !root.contains(target)) closeMentions(false);
  });

  document.addEventListener(
    "keydown",
    (event) => {
      const target = event.target;
      const root = mentionsRoot();
      if (!(target instanceof Element) || !root || !mentionsIsOpen(root)) return;
      const inBody = target instanceof HTMLTextAreaElement && target.id === "comment-body";
      const inList = target.closest(".comment-mentions") !== null;
      if (!inBody && !inList) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        closeMentions(true);
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        highlightMention(root, mentionActive + 1);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        highlightMention(root, mentionActive - 1);
        return;
      }
      // Enter picks the highlighted person, and in the search field it can never
      // reach the browser's implicit submission of the comment form.
      if (
        event.key === "Enter" &&
        (inBody || target.classList.contains("comment-mentions-search"))
      ) {
        if (!inBody) event.preventDefault();
        if (visibleMentions(root).length === 0) return;
        event.preventDefault();
        insertHighlightedMention(root);
      }
    },
    true,
  );

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || target.id !== "comment-body") return;
    if (mentionSuppress) return;
    openMentionsWhileTyping(target);
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof Element) || !target.classList.contains("comment-mentions-search"))
      return;
    const root = mentionsRoot();
    if (root) filterMentions(root, target.value);
  });

  // A whole-detail swap replaces the composer, so an open list must not survive
  // it, and a resize (a phone turned sideways) re-clamps the one that is open.
  document.addEventListener("htmx:afterSettle", () => closeMentions(false));
  window.addEventListener("resize", () => {
    const root = mentionsRoot();
    const popover = root ? mentionsPopover(root) : null;
    if (!root || !popover || !mentionsIsOpen(root)) return;
    const toggle = root.querySelector("[data-mentions-open]");
    if (toggle) placeMentions(popover, toggle.getBoundingClientRect());
  });

  const initialize = () => {
    syncAfterSwap();
    foldProperties();
  };
  foldProperties();
  narrow.addEventListener("change", foldProperties);
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initialize);
  else initialize();
})();
