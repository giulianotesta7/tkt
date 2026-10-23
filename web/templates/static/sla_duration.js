// Written SLA durations (issue #395): a live reading and validity for each
// field. The server repeats every check, so the form also works without it.
(() => {
  const grid = document.querySelector("[data-sla-durations]");
  if (!grid) return;
  const maximum = BigInt(grid.dataset.durationMax);
  const parse = (raw) => {
    const text = raw.trim().toLowerCase();
    if (!/^\d+\s*[hms](?:\s*\d+\s*[hms])*$/.test(text)) return null;
    const seen = new Set();
    let total = 0n;
    for (const match of text.matchAll(/(\d+)\s*([hms])/g)) {
      if (seen.has(match[2])) return null;
      seen.add(match[2]);
      total += BigInt(match[1]) * { h: 3600n, m: 60n, s: 1n }[match[2]];
      if (total > maximum) return null;
    }
    return total;
  };
  const reading = (seconds) => {
    const parts = [
      [seconds / 3600n, "h"],
      [(seconds % 3600n) / 60n, "min"],
      [seconds % 60n, "s"],
    ];
    return (
      parts
        .filter(([n]) => n !== 0n)
        .map(([n, unit]) => `${n} ${unit}`)
        .join(" ") || "0 s"
    );
  };
  const validate = (input) => {
    const seconds = parse(input.value);
    const error =
      seconds === null
        ? grid.dataset.durationInvalid
        : seconds < 60n
          ? grid.dataset.durationMinimum
          : "";
    input.setCustomValidity(error);
    input.setAttribute("aria-invalid", String(Boolean(error)));
    const output = document.getElementById(`${input.id}-reading`);
    output.textContent = error || reading(seconds);
    output.classList.toggle("is-error", Boolean(error));
  };
  const inputs = grid.querySelectorAll(".sla-duration-input");
  inputs.forEach((input) => {
    validate(input);
    input.addEventListener("input", () => validate(input));
  });
  // Discard is a native reset: the browser restores the values after the
  // event, so the readings and validity follow on the next task.
  const form = grid.closest("form");
  if (form) {
    form.addEventListener("reset", () => {
      window.setTimeout(() => inputs.forEach(validate), 0);
    });
  }
})();
