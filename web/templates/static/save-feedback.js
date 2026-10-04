(() => {
  const duration = 5000;
  const exitMs = 220;
  let timer;
  let exitTimer;
  let activeRegion;
  const generations = new Map();
  const requests = new WeakMap();
  // A 403 is an impossible action, not a transient failure: the generic
  // "try again" copy would invite a retry that can only fail. Every other
  // failure (transport, validation, 5xx) keeps the retry-flavoured copy.
  const GENERIC_FAILURE_MESSAGE = "Unable to save changes. Please try again.";
  const FORBIDDEN_MESSAGE = "You do not have permission to make that change.";
  // A drawer panel load is a read, not a mutation. Its 4xx copy must not invite
  // a retry the server will keep rejecting, and it stays distinct from the
  // retry-flavoured copy used for a transient transport failure.
  const PANEL_LOAD_FAILURE_MESSAGE = "This panel can't be opened. It may no longer be available.";
  const PANEL_LOAD_RETRY_MESSAGE = "Unable to open this panel. Please try again.";

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

  const showFailure = (target, message) => {
    retire();
    if (hasUsefulFailure()) return;
    const region = regionFor(target);
    const messageNode = region.querySelector(".save-feedback-message");
    if (!messageNode) return;
    region.className = "error-banner save-feedback-error";
    messageNode.textContent = message;
    region.dataset.feedbackMessage = message;
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
  // A drawer launcher is an htmx GET whose response replaces a drawer host. It
  // is a read, so it never enters the save/generation tracking below; only its
  // failure copy is owned here.
  const panelLoadSource = (event) => {
    const element = event.detail?.elt;
    if (!(element instanceof Element)) return false;
    const source = element.closest("[hx-get]");
    if (!source) return false;
    const target = source.closest("[hx-target]")?.getAttribute("hx-target");
    return typeof target === "string" && target.includes("drawer-host");
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
    return action !== "select_step";
  };
  const beforeRequest = (event) => {
    const xhr = event.detail?.xhr;
    if (!xhr) return;
    if (panelLoadSource(event)) {
      requests.set(xhr, { panelLoad: true, target: feedbackTarget(event) });
      return;
    }
    if (!mutationSource(event)) return;
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
  const failureMessage = (event) =>
    event.type === "htmx:responseError" && event.detail?.xhr?.status === 403
      ? FORBIDDEN_MESSAGE
      : GENERIC_FAILURE_MESSAGE;
  const mutationFailure = (event) => {
    const request = requests.get(event.detail?.xhr);
    if (!isCurrent(request)) return;
    retire();
    const message = failureMessage(event);
    setTimeout(() => {
      if (isCurrent(request)) showFailure(request.target, message);
    }, 0);
  };
  // A panel load has no stale-response race to guard, so it reports immediately.
  // A 4xx is an impossible action; only a transport or 5xx failure is retryable.
  const panelLoadFailure = (event) => {
    const request = requests.get(event.detail?.xhr);
    if (!request?.panelLoad) return;
    const status = event.detail?.xhr?.status;
    const message =
      event.type === "htmx:responseError" && status === 403
        ? FORBIDDEN_MESSAGE
        : event.type === "htmx:responseError" && status >= 400 && status < 500
          ? PANEL_LOAD_FAILURE_MESSAGE
          : PANEL_LOAD_RETRY_MESSAGE;
    retire();
    showFailure(request.target, message);
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
  document.addEventListener("htmx:responseError", panelLoadFailure);
  document.addEventListener("htmx:sendError", panelLoadFailure);
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
