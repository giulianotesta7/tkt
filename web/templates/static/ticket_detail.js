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

  const initialize = () => {
    syncAfterSwap();
    foldProperties();
  };
  foldProperties();
  narrow.addEventListener("change", foldProperties);
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initialize);
  else initialize();
})();
