/*
 * Settings save bar (issue #383, S2): a section's form reveals its sticky save
 * bar as soon as one of its controls changes — file inputs included — and hides
 * it again on discard. The save itself is a native form post that redirects, so
 * the page that renders after a successful save starts with every bar hidden
 * again; nothing here needs to remember a saved state.
 *
 * No user-facing string lives in this script: the bar carries the section's own
 * Save and Discard labels from the catalogue.
 */

(function () {
  function savebar(form) {
    return form.querySelector("[data-settings-savebar]");
  }

  // owner resolves the settings form a control (or a reset event's form)
  // belongs to, or null when the event did not come from one.
  function owner(node) {
    if (!node || typeof node.closest !== "function") return null;
    var control = node.closest("input, select, textarea");
    if (control && control.form && control.form.matches("form[data-settings-form]")) {
      return control.form;
    }
    var form = node.closest("form[data-settings-form]");
    return form;
  }

  document.addEventListener("input", function (event) {
    var form = owner(event.target);
    if (!form) return;
    var bar = savebar(form);
    if (bar) bar.hidden = false;
  });

  // A file input announces its choice through change, never through input.
  document.addEventListener("change", function (event) {
    var form = owner(event.target);
    if (!form) return;
    var bar = savebar(form);
    if (bar) bar.hidden = false;
  });

  // Discard: the bar disappears with the values it would have saved. The reset
  // event fires before the browser resets the controls, so the bar is hidden
  // for the state the form is about to be in.
  document.addEventListener(
    "reset",
    function (event) {
      var form = owner(event.target);
      if (!form) return;
      var bar = savebar(form);
      if (bar) bar.hidden = true;
    },
    true,
  );
})();
