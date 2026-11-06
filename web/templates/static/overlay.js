// Shared fixed positioning; keep overlays in their DOM location for delegated
// actions and focus. Callers retain ownership of open/close and ARIA state.
(() => {
  const cleanups = new WeakMap();
  window.tktPositionMenu = (button, menu, { matchWidth = false, maxHeight = Infinity } = {}) => {
    cleanups.get(menu)?.();
    const originalStyle = menu.getAttribute("style");
    const controller = new AbortController();
    const stop = () => {
      controller.abort();
      observer.disconnect();
      if (originalStyle === null) menu.removeAttribute("style");
      else menu.setAttribute("style", originalStyle);
      cleanups.delete(menu);
    };
    const closed = () => !menu.isConnected || !button.isConnected || menu.closest("[hidden]");
    const position = () => {
      if (closed()) return stop();
      const margin = 8;
      const gap = 6;
      const width = document.documentElement.clientWidth;
      const height = window.innerHeight;
      const box = button.getBoundingClientRect();
      Object.assign(menu.style, {
        position: "fixed",
        right: "auto",
        bottom: "auto",
        boxSizing: "border-box",
        minWidth: "0",
        minHeight: "0",
        maxWidth: `${width - 2 * margin}px`,
        maxHeight: "none",
        overflowY: "auto",
      });
      if (matchWidth) menu.style.width = `${box.width}px`;
      const naturalHeight = Math.min(menu.getBoundingClientRect().height, maxHeight);
      const available = (space) => Math.max(0, Math.min(height - 2 * margin, space));
      const below = available(height - margin - box.bottom - gap);
      const above = available(box.top - gap - margin);
      const up = naturalHeight > below && above > below;
      menu.style.maxHeight = `${Math.min(up ? above : below, maxHeight)}px`;
      const panel = menu.getBoundingClientRect();
      const clamp = (value, size, extent) =>
        Math.max(margin, Math.min(value, extent - size - margin));
      menu.style.left = `${clamp(box.right - panel.width, panel.width, width)}px`;
      menu.style.top = `${clamp(up ? box.top - gap - panel.height : box.bottom + gap, panel.height, height)}px`;
    };
    // Existing menus close via hidden or DOM replacement. Pickers explicitly
    // dispose when their data-open state closes. Filtering can change height.
    const observer = new MutationObserver(position);
    observer.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["hidden"],
    });
    cleanups.set(menu, stop);
    window.addEventListener("scroll", position, { capture: true, signal: controller.signal });
    window.addEventListener("resize", position, { signal: controller.signal });
    position();
    return stop;
  };
})();
