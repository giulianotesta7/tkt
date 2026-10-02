/**
 * Ticket-creation draft (issue #262).
 *
 * Choosing a category is a full navigation: the "Change" link and every
 * picker row are plain GETs to /tickets/new, so the create form loses its
 * title, description and priority on every picker round trip. This keeps a
 * sessionStorage draft and restores it on the next render of the same
 * category's create form.
 *
 * The draft is scoped to the category it was typed in: the storage key ends
 * in the category id AND the payload repeats that scope, so a draft can never
 * be resurrected onto a different category. Stored bytes are untrusted input
 * (a stale tab, or hand-edited storage), so a payload is honoured only when
 * its version and scope match, its values are strings, its priority is one of
 * the options the server rendered, and its text stays under the request-body
 * cap the server enforces.
 *
 * Submitting clears the draft: a committed ticket must not seed the next
 * creation. A 422 re-render carries the submitted values and re-seeds the
 * draft, so a rejected submit does not lose the operator's text.
 *
 * Loaded only where the create form renders (the ticket_form partial).
 */
(() => {
  const KEY_PREFIX = "tktTicketDraft:";
  const VERSION = 1;
  // The server caps a request body at 1 MiB; these stay well under it while
  // leaving any realistic ticket text intact.
  const TITLE_MAX = 512;
  const DESCRIPTION_MAX = 100000;

  function createForm() {
    for (const form of document.querySelectorAll('form[action="/tickets"]')) {
      if (form.elements.title && form.elements.description && form.elements.category_id) {
        return form;
      }
    }
    return null;
  }

  function scopeOf(form) {
    const category = form.elements.category_id;
    return category ? String(category.value).trim() : "";
  }

  // Anything read back from storage is untrusted: a stale or hand-edited value
  // must not put the form into a state the server would reject. Text fields are
  // type-checked and length-capped, and priority is admitted only when the
  // server actually rendered it as an option.
  function clampText(value, max) {
    return typeof value === "string" ? value.slice(0, max) : "";
  }

  function validPriority(form, value) {
    const select = form.elements.priority;
    if (!select || typeof value !== "string") return null;
    for (const option of select.options) {
      if (option.value === value) return value;
    }
    return null;
  }

  function readDraft(scope) {
    let raw;
    try {
      raw = window.sessionStorage.getItem(KEY_PREFIX + scope);
    } catch {
      return null;
    }
    if (!raw) return null;
    let payload;
    try {
      payload = JSON.parse(raw);
    } catch {
      return null;
    }
    if (!payload || typeof payload !== "object") return null;
    if (payload.v !== VERSION || payload.scope !== scope) return null;
    return payload;
  }

  function writeDraft(scope, payload) {
    try {
      window.sessionStorage.setItem(KEY_PREFIX + scope, JSON.stringify(payload));
    } catch {
      /* storage unavailable (private mode/quota): the form keeps working */
    }
  }

  function clearDraft(scope) {
    try {
      window.sessionStorage.removeItem(KEY_PREFIX + scope);
    } catch {
      /* storage unavailable: nothing to clear */
    }
  }

  function snapshot(form, scope) {
    return {
      v: VERSION,
      scope,
      title: form.elements.title ? form.elements.title.value : "",
      description: form.elements.description ? form.elements.description.value : "",
      priority: form.elements.priority ? form.elements.priority.value : "",
    };
  }

  function save(form) {
    const scope = scopeOf(form);
    const payload = snapshot(form, scope);
    if (
      payload.title === "" &&
      payload.description === "" &&
      payload.priority === defaultPriority
    ) {
      clearDraft(scope);
      return;
    }
    writeDraft(scope, payload);
  }

  function restore(form) {
    const payload = readDraft(scopeOf(form));
    if (!payload) return;
    const priority = validPriority(form, payload.priority);
    form.elements.title.value = clampText(payload.title, TITLE_MAX);
    form.elements.description.value = clampText(payload.description, DESCRIPTION_MAX);
    if (priority !== null) form.elements.priority.value = priority;
  }

  const form = createForm();
  if (!form) return;

  // The default the server rendered: leaving every field untouched must not
  // leave an empty draft behind.
  const defaultPriority = form.elements.priority ? form.elements.priority.value : "";

  if (form.querySelector(".error-banner")) {
    // A rejected submit re-rendered the form with the submitted values; they
    // are authoritative, and re-seeding keeps them across the next picker trip.
    save(form);
  } else {
    restore(form);
  }

  form.addEventListener("input", () => save(form));
  form.addEventListener("change", () => save(form));
  form.addEventListener("submit", () => clearDraft(scopeOf(form)));
})();
