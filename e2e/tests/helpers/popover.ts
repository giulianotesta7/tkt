import { expect, type Locator } from "@playwright/test";

/** Bounds alone miss clipping by an ancestor; each inner corner must also be hittable. */
export async function expectPopoverInView(menu: Locator): Promise<void> {
  await expect(menu).toBeVisible();
  await expect
    .poll(() =>
      menu.evaluate((element) => {
        const box = element.getBoundingClientRect();
        const inset = 6; // Inside the rounded border, not its transparent corner.
        return {
          inView:
            box.left >= 8 &&
            box.top >= 8 &&
            box.right <= innerWidth - 8 &&
            box.bottom <= innerHeight - 8,
          corners: [
            [box.left + inset, box.top + inset],
            [box.right - inset, box.top + inset],
            [box.left + inset, box.bottom - inset],
            [box.right - inset, box.bottom - inset],
          ].map(([x, y]) => element.contains(document.elementFromPoint(x, y))),
        };
      }),
    )
    .toEqual({ inView: true, corners: [true, true, true, true] });
}
