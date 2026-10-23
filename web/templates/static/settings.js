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
      // Discard also puts the live preview back to the values the browser is
      // about to restore, so the mirror never shows discarded input.
      window.setTimeout(function () {
        if (form.querySelector("[data-preview-panel]")) syncSigninPreview();
        syncHighlightRows();
      }, 0);
    },
    true,
  );

  // --- Sign-in page preview (issue #383, S10) -----------------------------

  function signin() {
    return document.querySelector("[data-preview-panel]");
  }

  function text(selector, value) {
    var el = document.querySelector(selector);
    if (el) el.textContent = value;
  }

  function show(selector, visible) {
    var el = document.querySelector(selector);
    if (el) el.hidden = !visible;
  }

  function selectedRadio(name) {
    var checked = document.querySelector('input[name="' + name + '"]:checked');
    return checked ? checked.value : "";
  }

  // syncSigninPreview mirrors every editable input into the panel the preview
  // renders. It reads the DOM and writes the DOM; it owns no copy.
  function syncSigninPreview() {
    var panel = signin();
    if (!panel) return;

    text("[data-preview-title-text]", document.querySelector("[data-preview-title]").value);
    text("[data-preview-subtitle-text]", document.querySelector("[data-preview-subtitle]").value);

    var noticeOn = document.querySelector("[data-preview-notice-toggle]");
    var enabled = Boolean(noticeOn && noticeOn.checked);
    var notice = document.querySelector("[data-preview-notice]");
    if (notice) {
      notice.hidden = !enabled;
      notice.className = "auth-entry__notice auth-entry__notice--" + selectedRadio("notice_kind");
    }
    text(
      "[data-preview-notice-title-text]",
      document.querySelector("[data-preview-notice-title]").value,
    );
    text(
      "[data-preview-notice-body-text]",
      document.querySelector("[data-preview-notice-text]").value,
    );

    var content = selectedRadio("content");
    show('[data-preview-zone="steps"]', content === "steps");
    show('[data-preview-zone="highlights"]', content === "highlights");

    var background = selectedRadio("background");
    panel.className = "auth-entry__presentation auth-entry__presentation--" + background;
    var color = document.querySelector("[data-preview-brand]");
    if (background === "brand" && color && color.value) {
      panel.style.setProperty("--signin-brand", color.value);
    }

    ["0", "1", "2"].forEach(function (index) {
      var title = document.querySelector('[data-preview-hl-title="' + index + '"]');
      var body = document.querySelector('[data-preview-hl-text="' + index + '"]');
      var icon = document.querySelector('[data-preview-hl-icon="' + index + '"]');
      if (!title || !body || !icon) return;
      var row = document.querySelector('[data-preview-hl-row="' + index + '"]');
      if (row) row.hidden = title.value.trim() === "";
      text('[data-preview-hl-title-text="' + index + '"]', title.value);
      text('[data-preview-hl-text-text="' + index + '"]', body.value);
      var mark = document.querySelector('[data-preview-hl-mark="' + index + '"]');
      var iconTemplate =
        icon && document.querySelector('template[data-icon-template="' + icon.value + '"]');
      if (mark && iconTemplate) {
        mark.replaceChildren(iconTemplate.content.cloneNode(true));
      }
    });

    var phone = document.querySelector("[data-preview-phone]");
    var hours = document.querySelector("[data-preview-hours]");
    if (phone) {
      show("[data-preview-phone-block]", phone.value.trim() !== "");
      text("[data-preview-phone-text]", phone.value);
    }
    if (hours) {
      show("[data-preview-hours-block]", hours.value.trim() !== "");
      text("[data-preview-hours-text]", hours.value);
    }
  }

  // syncHighlightRows keeps the add/remove controls honest: remove clears a row
  // and hides it, add reveals the first hidden one, and the add control
  // disappears once every row is used.
  function syncHighlightRows() {
    var rows = Array.prototype.slice.call(document.querySelectorAll("[data-highlight-row]"));
    if (!rows.length) return;
    var add = document.querySelector("[data-highlight-add]");
    if (add) {
      add.hidden = rows.every(function (row) {
        return !row.hidden;
      });
    }
    rows.forEach(function (row) {
      var remove = row.querySelector("[data-highlight-remove]");
      if (remove) remove.hidden = false;
    });
  }

  function editSignin(event) {
    var node = event.target;
    if (!node) return;
    if (node.closest("[data-highlight-add]")) {
      var rows = Array.prototype.slice.call(document.querySelectorAll("[data-highlight-row]"));
      for (var i = 0; i < rows.length; i += 1) {
        if (rows[i].hidden) {
          rows[i].hidden = false;
          break;
        }
      }
      syncHighlightRows();
      return;
    }
    var removeButton = node.closest("[data-highlight-remove]");
    if (removeButton) {
      var index = removeButton.getAttribute("data-highlight-remove");
      var row = document.querySelector('[data-highlight-row="' + index + '"]');
      if (row) {
        Array.prototype.forEach.call(row.querySelectorAll("input"), function (input) {
          input.value = "";
        });
        row.hidden = true;
      }
      syncHighlightRows();
      syncSigninPreview();
      return;
    }
    if (!signin()) return;
    if (node.closest("form[data-settings-form]")) syncSigninPreview();
  }

  document.addEventListener("input", editSignin);
  document.addEventListener("change", editSignin);
  document.addEventListener("click", editSignin);

  // Progressive disclosure: a block marked
  // data-show-when="name=value" shows only while the named radio holds that
  // value, or, with "name=checked", while the named checkbox is ticked, or, with
  // "name!=value", for every value but that one. It reacts on every change, so
  // switching an option reveals its fields at once instead of after a save.
  // Hidden fields keep their values.
  function syncDisclosure() {
    Array.prototype.forEach.call(document.querySelectorAll("[data-show-when]"), function (block) {
      var rule = block.getAttribute("data-show-when");
      var form = block.closest("form");
      if (!form) return;
      // The negated rule is parsed first: splitting on "=" alone would read
      // "name!=value" as the control "name!" holding "value", which no form
      // has, and the block would never be revealed.
      var negated = rule.indexOf("!=");
      var name = negated === -1 ? rule.split("=")[0] : rule.slice(0, negated);
      var want = negated === -1 ? rule.slice(name.length + 1) : rule.slice(negated + 2);
      var control = form.querySelector('[name="' + name + '"]:checked');
      if (negated !== -1) {
        block.hidden = Boolean(control) && control.value === want;
        return;
      }
      if (want === "checked") {
        block.hidden = !control;
        return;
      }
      block.hidden = !control || control.value !== want;
    });
  }
  document.addEventListener("change", syncDisclosure);

  syncDisclosure();
  syncSigninPreview();
  syncHighlightRows();

  // --- Field help toggletips (issue #383, S11) ----------------------------

  // closeTips collapses every open tip. Hover and keyboard focus are the CSS
  // side of the same control; this only owns the clicked state.
  function closeTips(except) {
    Array.prototype.forEach.call(document.querySelectorAll("[data-help-tip]"), function (tip) {
      if (tip !== except) tip.setAttribute("aria-expanded", "false");
    });
  }

  document.addEventListener("click", function (event) {
    var tip = event.target && event.target.closest ? event.target.closest("[data-help-tip]") : null;
    if (tip) {
      var open = tip.getAttribute("aria-expanded") === "true";
      closeTips(tip);
      tip.setAttribute("aria-expanded", open ? "false" : "true");
      return;
    }
    // An outside click closes whatever is open, so a tap on a phone never
    // leaves a bubble covering the form.
    closeTips(null);
  });

  // Keyboard focus opens the bubble too. The pointer flag keeps a mouse click
  // from opening it in focusin before its own click handler toggles it: with a
  // mouse the button is already hovered, and the click is what pins it.
  var pointerFocus = false;
  document.addEventListener(
    "pointerdown",
    function () {
      pointerFocus = true;
    },
    true,
  );
  document.addEventListener("keydown", function () {
    pointerFocus = false;
  });

  document.addEventListener("focusin", function (event) {
    var tip = event.target && event.target.closest ? event.target.closest("[data-help-tip]") : null;
    if (tip && !pointerFocus) tip.setAttribute("aria-expanded", "true");
  });

  document.addEventListener("focusout", function (event) {
    var tip = event.target && event.target.closest ? event.target.closest("[data-help-tip]") : null;
    if (tip) tip.setAttribute("aria-expanded", "false");
  });

  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") return;
    var open = document.querySelector('[data-help-tip][aria-expanded="true"]');
    if (!open) return;
    open.setAttribute("aria-expanded", "false");
  });

  // --- Category suggestions: the Try it box (issue #416, S6) --------------

  // The Try it field sits in the card's save form, so Enter would submit the save.
  // Enter runs the test instead: the field exists to be tried, and a test never
  // changes the configuration a save changes.
  document.addEventListener("keydown", function (event) {
    if (event.key !== "Enter") return;
    var input = event.target;
    if (!input || !input.closest || !input.closest("[data-suggestion-try-input]")) return;
    var button = document.querySelector("[data-suggestion-try]");
    if (!button) return;
    event.preventDefault();
    button.click();
  });
})();
