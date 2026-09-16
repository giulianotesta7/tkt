(() => {
  const duration = 5000;
  const exitMs = 220;
  let timer;
  let exitTimer;
  let activeRegion;
  const generations = new Map();
  const requests = new WeakMap();

  const makeRegion = () => {
    const region = document.createElement("div");
    region.id = "save-feedback";
    region.className = "save-feedback";
    region.setAttribute("role", "status");
    region.setAttribute("aria-live", "polite");
    region.setAttribute("aria-atomic", "true");
    region.innerHTML =
      '<span class="save-feedback-message"></span><button class="save-feedback-dismiss" type="button" aria-label="Dismiss confirmation">Dismiss</button>';
    return region;
  };

  const currentDrawer = () =>
    document.querySelector(".users-drawer:not([inert]), .category-drawer:not([inert])");

  // Success lives on the fixed toast layer appended to <body>; failures are
  // anchored inside the active local context (drawer header or page header).
  // Moving the single #save-feedback element keeps its ID unique.
  const regionFor = (target) => {
    const region = document.getElementById("save-feedback") || makeRegion();
    const drawer = target === "drawer" ? currentDrawer() : null;
    if (drawer) {
      const header = drawer.querySelector(".users-drawer-header");
      if (header) header.after(region);
      else drawer.prepend(region);
    } else {
      const anchor = document.querySelector(".page-header, .command-header, .users-header");
      if (anchor) anchor.after(region);
      else {
        const main = document.querySelector(".main");
        if (main) main.prepend(region);
        else document.body.prepend(region);
      }
    }
    return region;
  };

  const cancelExit = () => {
    if (exitTimer) clearTimeout(exitTimer);
    exitTimer = undefined;
  };

  const dismiss = (region) => {
    if (!region) return;
    region.classList.remove("is-visible", "is-leaving");
    region.hidden = true;
    region.dataset.feedbackMessage = "";
    region.dataset.feedbackKind = "";
    region.setAttribute("role", "status");
    region.setAttribute("aria-live", "polite");
  };

  const retire = () => {
    if (timer) clearTimeout(timer);
    timer = undefined;
    cancelExit();
    dismiss(activeRegion || document.getElementById("save-feedback"));
    activeRegion = undefined;
  };

  // Auto-dismiss and the manual Dismiss button animate the toast out; race and
  // failure paths retire instantly so stale success never lingers.
  const retireAnimated = () => {
    if (timer) clearTimeout(timer);
    timer = undefined;
    const region = activeRegion || document.getElementById("save-feedback");
    activeRegion = undefined;
    if (!region || region.hidden) return;
    if (region.dataset.feedbackKind !== "success") {
      dismiss(region);
      return;
    }
    cancelExit();
    region.classList.remove("is-visible");
    region.classList.add("is-leaving");
    exitTimer = setTimeout(() => {
      exitTimer = undefined;
      region.classList.remove("is-leaving");
      dismiss(region);
    }, exitMs);
  };

  const hasUsefulFailure = () =>
    Array.from(document.querySelectorAll("[role='alert'], .error-banner, .warning-banner")).some(
      (node) => !node.hidden && (node !== activeRegion || node.dataset.feedbackKind !== "success"),
    );

  const show = (data) => {
    if (!data || data.kind !== "success" || !data.message) return;
    if (hasUsefulFailure()) {
      if (activeRegion?.dataset.feedbackKind === "success") retire();
      return;
    }
    if (timer) clearTimeout(timer);
    timer = undefined;
    cancelExit();
    const region = document.getElementById("save-feedback") || makeRegion();
    const message = region.querySelector(".save-feedback-message");
    if (!message) return;
    // A second success while the toast is open coalesces: same element, same
    // entry animation, message updated in place, timer restarted.
    const coalesce =
      region === activeRegion && region.dataset.feedbackKind === "success" && !region.hidden;
    document.body.append(region);
    if (!coalesce) {
      region.className = "save-feedback";
      region.dataset.feedbackKind = data.kind;
      region.setAttribute("role", "status");
      region.setAttribute("aria-live", "polite");
      region.hidden = false;
      // Force a reflow so the unhide is laid out before the enter transition starts.
      void region.offsetWidth;
      region.classList.add("is-visible");
    }
    message.textContent = data.message;
    region.dataset.feedbackMessage = data.message;
    activeRegion = region;
    timer = setTimeout(retireAnimated, duration);
  };

  const showFailure = (target) => {
    retire();
    if (hasUsefulFailure()) return;
    const region = regionFor(target);
    const message = region.querySelector(".save-feedback-message");
    if (!message) return;
    region.className = "error-banner save-feedback-error";
    message.textContent = "Unable to save changes. Please try again.";
    region.dataset.feedbackMessage = message.textContent;
    region.dataset.feedbackKind = "error";
    region.setAttribute("role", "alert");
    region.setAttribute("aria-live", "assertive");
    region.hidden = false;
    activeRegion = region;
  };

  const mutationSource = (event) => {
    const element = event.detail?.elt;
    return (
      element instanceof Element &&
      (element.matches("[hx-post], form[method='post']") ||
        element.closest("[hx-post], form[method='post']"))
    );
  };
  const actionFor = (event) => {
    const action = event.detail?.requestConfig?.parameters?.action;
    return typeof action === "string" ? action : "";
  };
  const sourceFor = (event) => {
    const element = event.detail?.elt;
    return element instanceof Element ? element : null;
  };
  const requestRegion = (event) => {
    const target = event.detail?.target;
    if (target instanceof Element && target.id) return `#${target.id}`;
    const source = sourceFor(event);
    const configuredTarget = source?.closest("[hx-target]")?.getAttribute("hx-target");
    if (configuredTarget) return configuredTarget;
    return source?.closest(".users-drawer, .category-drawer") ? "drawer" : "page";
  };
  const feedbackTarget = (event) =>
    sourceFor(event)?.closest(".users-drawer, .category-drawer") ? "drawer" : undefined;
  const isSave = (event) => {
    const action = actionFor(event);
    return action !== "select_step" && action !== "preview";
  };
  const beforeRequest = (event) => {
    if (!mutationSource(event)) return;
    const xhr = event.detail?.xhr;
    if (!xhr) return;
    const region = requestRegion(event);
    const saves = isSave(event);
    const request = { saves, region, target: feedbackTarget(event) };
    if (saves) {
      request.generation = (generations.get(region) || 0) + 1;
      generations.set(region, request.generation);
    }
    requests.set(xhr, request);
  };
  const beforeSwap = (event) => {
    const request = requests.get(event.detail?.xhr);
    if (!request?.saves) return;
    if (request.generation !== generations.get(request.region)) event.preventDefault();
  };
  const isCurrent = (request) =>
    request?.saves && request.generation === generations.get(request.region);
  const mutationFailure = (event) => {
    const request = requests.get(event.detail?.xhr);
    if (!isCurrent(request)) return;
    retire();
    setTimeout(() => {
      if (isCurrent(request)) showFailure(request.target);
    }, 0);
  };

  document.addEventListener("click", (event) => {
    const button =
      event.target instanceof Element ? event.target.closest(".save-feedback-dismiss") : null;
    if (button) retireAnimated();
  });
  document.addEventListener("htmx:beforeRequest", beforeRequest);
  document.addEventListener("htmx:beforeSwap", beforeSwap);
  document.addEventListener("htmx:afterOnLoad", (event) => {
    const request = requests.get(event.detail.xhr);
    if (!isCurrent(request)) return;
    const raw = event.detail.xhr.getResponseHeader("X-Save-Feedback");
    if (!raw) return;
    try {
      const feedback = JSON.parse(raw)["save-feedback"];
      setTimeout(() => {
        if (isCurrent(request)) show(feedback);
      }, 0);
    } catch {
      // Ignore malformed feedback metadata. The server response remains authoritative.
    }
  });
  document.addEventListener("htmx:responseError", mutationFailure);
  document.addEventListener("htmx:sendError", mutationFailure);
  // HTMX 2.0.4 restores the history element before it synchronously bubbles this event.
  document.addEventListener("htmx:historyRestore", () => {
    if (timer) clearTimeout(timer);
    timer = undefined;
    const region = document.getElementById("save-feedback");
    activeRegion = undefined;
    if (region?.dataset.feedbackKind === "success") dismiss(region);
  });

  const initialize = () => {
    const region = document.getElementById("save-feedback");
    if (!region) return;
    // The server-rendered flash starts hidden and animates in exactly once;
    // htmx:historyRestore dismisses it so back/forward never replays it.
    if (region.dataset.feedbackMessage) {
      region.hidden = true;
      show({ message: region.dataset.feedbackMessage, kind: region.dataset.feedbackKind });
    }
  };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initialize);
  else initialize();
})();
