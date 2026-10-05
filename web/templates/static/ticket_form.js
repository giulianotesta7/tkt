/**
 * Ticket-creation draft (issue #262, extended by #302).
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
 * #302 carries the draft forward when the operator switches category A -> B
 * through the picker. The handoff is explicit: opening the picker from a
 * create form records the source category, and only the NEXT create form
 * render may read that source. Nothing else ever reaches across category
 * keys, and the same untrusted-input guards apply to the transferred values.
 * The target's own draft wins a collision (never silently overwritten), and a
 * value the target cannot represent (a priority it did not render) is
 * explained and confirmed before it is dropped.
 *
 * Submitting clears the draft: a committed ticket must not seed the next
 * creation. A 422 re-render carries the submitted values and re-seeds the
 * draft, so a rejected submit does not lose the operator's text.
 *
 * Loaded only where the create form renders (the ticket_form partial).
 */
(() => {
  const KEY_PREFIX = "tktTicketDraft:";
  // The picker handoff: which category the draft came from. Separate from the
  // draft itself because it describes one picker trip, not stored text.
  const HANDOFF_KEY = "tktTicketDraftHandoff";
  const VERSION = 1;
  // The server caps a request body at 1 MiB; these stay well under it while
  // leaving any realistic ticket text intact.
  const TITLE_MAX = 512;
  const DESCRIPTION_MAX = 100000;
  const NAME_MAX = 200;

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

  // The picker handoff is untrusted too: the source id must be a plain numeric
  // category id (that is the only thing that can address a draft key), the
  // version must match, and the display name is a capped string.
  function readHandoff() {
    let raw;
    try {
      raw = window.sessionStorage.getItem(HANDOFF_KEY);
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
    if (payload.v !== VERSION || typeof payload.from !== "string") return null;
    const from = payload.from.trim();
    if (!/^\d+$/.test(from)) return null;
    return { from, name: clampText(payload.name, NAME_MAX) };
  }

  function writeHandoff(from, name) {
    if (!from) return;
    try {
      window.sessionStorage.setItem(HANDOFF_KEY, JSON.stringify({ v: VERSION, from, name }));
    } catch {
      /* storage unavailable: the switch simply does not carry the draft */
    }
  }

  function clearHandoff() {
    try {
      window.sessionStorage.removeItem(HANDOFF_KEY);
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

  function applyDraft(form, payload) {
    const priority = validPriority(form, payload.priority);
    form.elements.title.value = clampText(payload.title, TITLE_MAX);
    form.elements.description.value = clampText(payload.description, DESCRIPTION_MAX);
    if (priority !== null) form.elements.priority.value = priority;
  }

  function restore(form) {
    const payload = readDraft(scopeOf(form));
    if (!payload) return;
    applyDraft(form, payload);
  }

  function categoryName(form) {
    const path = form.querySelector(".selected-catalog-path span");
    if (!path) return "";
    // The picker renders "Department / Desk / Category"; the notice names the
    // category the operator left, not the whole path.
    const parts = path.textContent.split("/");
    return parts[parts.length - 1].trim();
  }

  // A one-line status next to the form's own subtitle. Created in the DOM
  // rather than rendered server-side so the create-form markup (and its
  // golden) stays frozen; only the browser that performed the switch ever
  // sees it.
  function notice(form, text) {
    let el = form.querySelector("#ticket-draft-notice");
    if (!el) {
      el = document.createElement("p");
      el.id = "ticket-draft-notice";
      el.className = "command-sub";
      el.setAttribute("role", "status");
      const subtitle = form.querySelector(".command-header .command-sub");
      if (subtitle) subtitle.insertAdjacentElement("afterend", el);
      else form.prepend(el);
    }
    el.textContent = text;
  }

  function sourceLabel(handoff) {
    return handoff.name || "the previous category";
  }

  // Carry the source category's draft onto this form. The target's own draft
  // wins a collision (nothing is overwritten silently), and a priority the
  // target did not render is explained and confirmed before it is dropped.
  function transfer(form, handoff) {
    const scope = scopeOf(form);
    const own = readDraft(scope);
    if (own) {
      applyDraft(form, own);
      notice(form, `Your draft from ${sourceLabel(handoff)} is still saved under that category.`);
      return;
    }
    const source = readDraft(handoff.from);
    if (!source) return;
    const priority = validPriority(form, source.priority);
    const dropsPriority =
      typeof source.priority === "string" && source.priority !== "" && priority === null;
    if (dropsPriority) {
      const kept = window.confirm(
        `This category does not offer the priority "${source.priority}" from your ` +
          `${sourceLabel(handoff)} draft. Discard that priority and keep the title and ` +
          "description?",
      );
      // Cancelling preserves the source draft and leaves this form untouched.
      if (!kept) return;
    }
    form.elements.title.value = clampText(source.title, TITLE_MAX);
    form.elements.description.value = clampText(source.description, DESCRIPTION_MAX);
    if (priority !== null) form.elements.priority.value = priority;
    save(form);
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
    clearHandoff();
  } else {
    const handoff = readHandoff();
    if (handoff && handoff.from !== scopeOf(form)) {
      transfer(form, handoff);
    } else {
      restore(form);
    }
    if (handoff) clearHandoff();
  }

  // Opening the picker is the explicit handoff: the NEXT create form may pull
  // this category's draft forward, and nothing else ever does.
  const change = form.querySelector(".selected-catalog-path a");
  if (change) {
    change.addEventListener("click", () => {
      writeHandoff(scopeOf(form), categoryName(form));
    });
  }

  form.addEventListener("input", () => save(form));
  form.addEventListener("change", () => save(form));
  form.addEventListener("submit", () => clearDraft(scopeOf(form)));
})();
