/*
 * Saved-reply manager behaviour (issue #418).
 *
 * Two enhancements over the server-rendered screen, both delegated from the
 * document so they survive every re-render:
 *  - the placeholder chips insert their marker at the caret of the reply text,
 *    which is the one edit the textarea cannot do itself;
 *  - Delete asks first, through the same <dialog> confirmation the rest of the
 *    interface uses, and submits the row's own form only when confirmed.
 *
 * The script renders no copy of its own: every string it shows comes from the
 * page, so it needs no catalogue prologue.
 */
(() => {
  "use strict";

  const replyBody = () => {
    const body = document.getElementById("saved-reply-body");
    return body instanceof HTMLTextAreaElement ? body : null;
  };

  // insertPlaceholder writes the marker at the caret and leaves the caret after
  // it, so two chips in a row land in the order they were pressed.
  const insertPlaceholder = (marker) => {
    const body = replyBody();
    if (!body || !marker) return;
    const start = typeof body.selectionStart === "number" ? body.selectionStart : body.value.length;
    const end = typeof body.selectionEnd === "number" ? body.selectionEnd : start;
    body.value = body.value.slice(0, start) + marker + body.value.slice(end);
    body.focus();
    const caret = start + marker.length;
    body.setSelectionRange(caret, caret);
    body.dispatchEvent(new Event("input", { bubbles: true }));
  };

  const deleteDialog = () => document.getElementById("saved-replies-delete-dialog");

  let pending = null;

  const showDeleteDialog = (form, opener) => {
    const dialog = deleteDialog();
    if (!dialog) {
      // Without the dialog element the row still deletes: the confirmation is a
      // progressive enhancement, never the only route to the action.
      form.requestSubmit();
      return;
    }
    pending = { form, opener };
    if (dialog.showModal) dialog.showModal();
    else dialog.setAttribute("open", "");
    const cancel = dialog.querySelector("[data-saved-reply-delete-cancel]");
    if (cancel) cancel.focus();
  };

  const hideDeleteDialog = (restoreFocus) => {
    const dialog = deleteDialog();
    if (dialog) {
      if (dialog.open) dialog.close();
      else dialog.removeAttribute("open");
    }
    const opener = pending ? pending.opener : null;
    pending = null;
    if (restoreFocus && opener && opener.isConnected) opener.focus();
  };

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;

    const chip = target.closest("[data-saved-reply-placeholder]");
    if (chip) {
      event.preventDefault();
      insertPlaceholder(chip.getAttribute("data-saved-reply-placeholder"));
      return;
    }

    const opener = target.closest("[data-saved-reply-delete-open]");
    if (opener) {
      const form = opener.closest("form");
      if (!form) return;
      event.preventDefault();
      showDeleteDialog(form, opener);
      return;
    }

    if (target.closest("[data-saved-reply-delete-cancel]")) {
      event.preventDefault();
      hideDeleteDialog(true);
      return;
    }

    if (target.closest("[data-saved-reply-delete-confirm]")) {
      const form = pending ? pending.form : null;
      pending = null;
      hideDeleteDialog(false);
      if (form) form.requestSubmit();
    }
  });

  document.addEventListener("keydown", (event) => {
    const dialog = deleteDialog();
    if (!dialog || !dialog.open || event.key !== "Escape") return;
    event.preventDefault();
    hideDeleteDialog(true);
  });

  // A dialog closed by the browser itself (Escape, a backdrop rule, or a click
  // on the close control) still hands focus back to the button that opened it.
  document.addEventListener(
    "close",
    (event) => {
      if (event.target !== deleteDialog()) return;
      const opener = pending ? pending.opener : null;
      pending = null;
      if (opener && opener.isConnected) opener.focus();
    },
    true,
  );
})();
