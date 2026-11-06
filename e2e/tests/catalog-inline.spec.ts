import { test, expect, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { base, loginAsSeeded } from "./helpers/auth.js";
import {
  createCategoryInFolder,
  createCategoryViaUi,
  createFolderViaUi,
} from "./helpers/navigation.js";
import { collectObservability } from "./helpers/layout.js";
import { expectPopoverInView } from "./helpers/popover.js";

const row = (page: Page, kind: string, id: string) =>
  page.locator(`[role="treeitem"][data-level="${kind}"][data-catalog-id="${id}"]`);

async function renameFolder(target: import("@playwright/test").Locator) {
  await target.locator(":scope > .catalog-tree-row .catalog-node-name").dblclick();
}

async function openMoveMenu(target: import("@playwright/test").Locator) {
  const line = target.locator(":scope > .catalog-tree-row");
  await line.locator("[data-category-menu]").click();
  await line.getByRole("menuitem", { name: "Move to…", exact: true }).click();
}

const rowGeometry = (page: Page) =>
  page.locator('[role="treeitem"] > .catalog-tree-row').evaluateAll((elements) =>
    elements.map((element) => {
      const { x, y, width, height } = element.getBoundingClientRect();
      return { x, y, width, height };
    }),
  );

async function creatorTreeSnapshot(page: Page) {
  const rows = await page.locator('[role="treeitem"] > .catalog-tree-row').evaluateAll((elements) =>
    elements.map((element) => {
      const box = element.getBoundingClientRect();
      return {
        kind: element.parentElement?.getAttribute("data-level"),
        id: element.parentElement?.getAttribute("data-catalog-id"),
        x: box.x,
        y: box.y,
        width: box.width,
        height: box.height,
      };
    }),
  );
  expect(rows.length).toBeGreaterThan(0);
  return {
    rows,
    scroll: await page.evaluate(() => ({
      x: scrollX,
      y: scrollY,
      tree: document.querySelector('[role="tree"]')?.scrollTop,
    })),
  };
}

async function expectButtonCreator(page: Page, trigger: import("@playwright/test").Locator) {
  const popup = page.locator("[data-catalog-create-row]");
  await expect(popup).toHaveCount(1);
  await expect(popup).toHaveAttribute("role", "dialog");
  await expect(popup).toHaveCSS("position", "fixed");
  await expectPopoverInView(popup);
  const anchor = await trigger.boundingBox();
  const box = await popup.boundingBox();
  if (!anchor || !box) throw new Error(`Missing creator or trigger at ${page.url()}`);
  expect(Math.abs(box.x - anchor.x)).toBeLessThanOrEqual(box.width + anchor.width + 16);
  expect(Math.abs(box.y - anchor.y)).toBeLessThanOrEqual(box.height + anchor.height + 16);
}

test.describe("inline catalog moves", () => {
  test("canvas desktop density is compact without hiding production content", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Canvas compact folder");
    const line = row(page, "folder", id).locator(":scope > .catalog-tree-row");
    await line.scrollIntoViewIfNeeded();
    expect((await line.boundingBox())?.height).toBe(36);
    const before = await rowGeometry(page);
    await line.hover();
    expect(await rowGeometry(page)).toEqual(before);
    await page.setViewportSize({ width: 1100, height: 900 });
    const longName = "Readable folder name with enough text to wrap on narrow screens";
    const longId = await createFolderViaUi(page, longName);
    const longLine = row(page, "folder", longId).locator(":scope > .catalog-tree-row");
    await expect(longLine.getByText(longName, { exact: true })).toBeVisible();
    const category = await createCategoryViaUi(page, "Canvas described category");
    await row(page, "category", category).focus();
    const inspector = page.getByRole("region", { name: /properties/i });
    const description = "This production description must remain readable. ".repeat(12).trim();
    await inspector.getByLabel("Description", { exact: true }).fill(description);
    await inspector.getByRole("button", { name: "Save changes" }).click();
    const described = row(page, "category", category).locator(":scope > .catalog-tree-row");
    await expect(described.getByText(description, { exact: true })).toBeVisible();
    expect((await described.boundingBox())?.height).toBeGreaterThan(36);
    for (const [content, text] of [
      [longLine, longName],
      [described, description],
    ] as const) {
      const bounds = await content.boundingBox();
      if (!bounds) throw new Error("Missing readable production row");
      const rectangles = await content.getByText(text, { exact: true }).evaluate((element) => {
        const range = document.createRange();
        range.selectNodeContents(element);
        return Array.from(range.getClientRects(), ({ x, y, width, height }) => ({
          x,
          y,
          width,
          height,
        }));
      });
      for (const rect of rectangles) {
        expect(rect.x).toBeGreaterThanOrEqual(bounds.x);
        expect(rect.x + rect.width).toBeLessThanOrEqual(bounds.x + bounds.width + 1);
        expect(rect.y + rect.height).toBeLessThanOrEqual(bounds.y + bounds.height + 1);
      }
    }
  });

  test("canvas narrow and coarse controls retain full tap targets", async ({ page, browser }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Canvas tap targets");
    await createFolderViaUi(page, "Canvas tap child", parent);
    const context = await browser.newContext({
      hasTouch: true,
      storageState: await page.context().storageState(),
    });
    try {
      const touch = await context.newPage();
      for (const width of [390, 1440]) {
        await touch.setViewportSize({ width, height: 900 });
        await touch.goto(base() + "/categories");
        expect(await touch.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
        const line = row(touch, "folder", parent).locator(":scope > .catalog-tree-row");
        // Beside the permanent inspector the pencil is hidden on purpose (S21);
        // every control that remains must still be a full tap target.
        const pen = line.locator("[data-catalog-pen]");
        if (width >= 1100) await expect(pen).toBeHidden();
        else await expect(pen).toBeVisible();
        const controls = line.locator(
          width >= 1100
            ? "[data-catalog-disclosure], .catalog-row-actions .category-menu-button:not([data-catalog-pen])"
            : "[data-catalog-disclosure], .catalog-row-actions .category-menu-button",
        );
        expect(await controls.count()).toBeGreaterThan(1);
        const centers: number[] = [];
        for (const control of await controls.all()) {
          await expect(control).toBeVisible();
          const box = await control.boundingBox();
          if (!box) throw new Error("Missing touch control");
          expect(box.width).toBeGreaterThanOrEqual(44);
          expect(box.height).toBeGreaterThanOrEqual(44);
          centers.push(box.y + box.height / 2);
        }
        expect(Math.max(...centers) - Math.min(...centers)).toBeLessThanOrEqual(1);
      }
    } finally {
      await context.close();
    }
  });

  test("nested mobile names remain readable in both themes", async ({ page }) => {
    await loginAsSeeded(page);
    const rootCategory = await createCategoryViaUi(page, "UX487 Root printer");
    const parent = await createFolderViaUi(page, "Readability parent");
    const folder = await createFolderViaUi(page, "Readability nested folder", parent);
    const short = await createCategoryInFolder(page, { name: "UX487 Printer", folderID: folder });
    const longName = "A long nested category name that must remain completely readable";
    const long = await createCategoryInFolder(page, { name: longName, folderID: folder });
    await page.setViewportSize({ width: 390, height: 900 });
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      for (const [kind, id] of [
        ["folder", parent],
        ["folder", folder],
        ["category", rootCategory],
        ["category", short],
        ["category", long],
      ]) {
        const line = row(page, kind, id).locator(":scope > .catalog-tree-row");
        const name = line.locator(".catalog-node-name");
        await expect(name).toBeVisible();
        // 120px accommodates the short production name without one-word-per-line compression.
        expect((await name.boundingBox())?.width).toBeGreaterThanOrEqual(120);
        if (kind === "category") await expect(line.locator(".catalog-badge")).toBeVisible();
        const controls = line.locator(
          ":scope > .catalog-row-actions > button, :scope > .catalog-row-actions > .catalog-row-menu > [data-category-menu]",
        );
        await expect(controls).toHaveCount(kind === "folder" ? 3 : 2);
        for (const control of await controls.all()) {
          await expect(control).toBeVisible();
          const box = await control.boundingBox();
          expect(box?.width).toBeGreaterThanOrEqual(44);
          expect(box?.height).toBeGreaterThanOrEqual(44);
        }
        const clipped = await name.evaluate(
          (element) =>
            element.scrollWidth > element.clientWidth ||
            element.scrollHeight > element.clientHeight,
        );
        expect(clipped).toBe(false);
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    }
  });

  test("generic catalog failures announce without moving the tree", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 900 });
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Canvas failed details");
    await page.goto(base() + "/categories");
    const target = row(page, "folder", id);
    const detailsUrl = await target.getAttribute("data-details-url");
    if (!detailsUrl) throw new Error("Missing public folder details URL");
    await page.route(`**${detailsUrl}`, (route) =>
      route.fulfill({ status: 500, contentType: "text/plain", body: "Synthetic details failure" }),
    );
    await target.scrollIntoViewIfNeeded();
    const before = await rowGeometry(page);
    const scroll = await page.evaluate(() => scrollY);
    let posts = 0;
    page.on("request", (request) => {
      if (request.method() === "POST") posts++;
    });
    const response = page.waitForResponse((response) => response.url().endsWith(detailsUrl));
    await target.locator(":scope > .catalog-tree-row [data-catalog-pen]").click();
    expect((await response).status()).toBe(500);
    const alert = page.getByRole("alert");
    await expect(alert).toHaveText("Unable to save changes. Please try again.");
    for (const theme of ["light", "dark"]) {
      await page.evaluate((theme) => {
        document.documentElement.dataset.theme = theme;
      }, theme);
      const backdropAlpha = await alert.evaluate((element) => {
        const canvas = document.createElement("canvas");
        const paint = canvas.getContext("2d");
        if (!paint) throw new Error("Missing public color rendering context");
        paint.fillStyle = getComputedStyle(element).backgroundColor;
        paint.fillRect(0, 0, 1, 1);
        return paint.getImageData(0, 0, 1, 1).data[3];
      });
      expect.soft(backdropAlpha, `${theme} feedback must hide underlying row text`).toBe(255);
      expect(await rowGeometry(page)).toEqual(before);
      expect(await page.evaluate(() => scrollY)).toBe(scroll);
      await expectPopoverInView(alert);
    }
    expect(posts).toBe(0);
    await page.unroute(`**${detailsUrl}`);
    await page.reload();
    await expect(target).toHaveAttribute("data-name", "Canvas failed details");
  });

  test("explicit creation controls create the requested kind at the top level", async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await expect(page.getByRole("button", { name: /top level/i })).toHaveCount(0);
    for (const kind of ["category", "folder"] as const) {
      const button = page.getByRole("button", { name: `New ${kind}`, exact: true });
      await expect(button).toBeVisible();
      await button.scrollIntoViewIfNeeded();
      await button.click({ trial: true });
      const before = await creatorTreeSnapshot(page);
      await button.focus();
      await button.press(kind === "category" ? "Enter" : "Space");
      const create = page.locator(`[data-catalog-create-row="${kind}"]`);
      await expect(create).toBeVisible();
      await expectButtonCreator(page, button);
      expect(await creatorTreeSnapshot(page)).toEqual(before);
      await page.keyboard.press("Escape");
      await expect(button).toBeFocused();
      expect(await creatorTreeSnapshot(page)).toEqual(before);
      await button.click();
      const name = `Explicit ${kind} action`;
      await create.getByLabel("Name", { exact: true }).fill(name);
      const background = await page.locator("#categories-background").innerHTML();
      const heading = await page.locator("h1").textContent();
      const url = page.url();
      let navigations = 0;
      const onRequest = (request: import("@playwright/test").Request) => {
        if (request.isNavigationRequest() && request.frame() === page.mainFrame()) navigations++;
      };
      page.on("request", onRequest);
      const response = page.waitForResponse(
        (result) =>
          new URL(result.url()).pathname ===
            (kind === "folder" ? "/categories/folders" : "/categories") &&
          result.request().method() === "POST",
      );
      await create.getByLabel("Name", { exact: true }).press("Enter");
      const saved = await response;
      expect(saved.status()).toBe(200);
      expect(saved.request().postData()).toContain('name="inline"');
      expect(saved.request().postData()).toMatch(/name="inline"\r?\n\r?\n1/);
      await expect(
        page.locator(`[role="treeitem"][data-level="${kind}"][data-name="${name}"]`),
      ).toHaveAttribute("data-parent-id", "0");
      await expect
        .poll(() => page.locator("#categories-background").innerHTML())
        .not.toBe(background);
      expect(page.url()).toBe(url);
      expect(await page.locator("h1").textContent()).toBe(heading);
      expect(navigations).toBe(0);
      page.off("request", onRequest);
      await expect(create).toHaveCount(0);
      await expect(
        page.locator(`[role="treeitem"][data-level="${kind}"][data-name="${name}"]`),
      ).toHaveAttribute("aria-selected", "true");
    }
    await page.reload();
    for (const kind of ["category", "folder"]) {
      await expect(
        page.locator(
          `[role="treeitem"][data-level="${kind}"][data-name="Explicit ${kind} action"]`,
        ),
      ).toHaveAttribute("data-parent-id", "0");
    }
    expect(pageErrors).toEqual([]);
  });

  test("contextual creation is anchored without moving rows and saves sorted in its destination", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Context overlay parent");
    for (const name of ["Alpha overlay", "Middle overlay", "Zulu overlay"]) {
      await createFolderViaUi(page, name, parent);
    }
    await page.goto(base() + "/categories");
    const folder = row(page, "folder", parent);
    const lines = page.locator('[role="treeitem"] > .catalog-tree-row');
    const geometry = () =>
      lines.evaluateAll((elements) =>
        elements.map((element) => {
          const box = element.getBoundingClientRect();
          return { x: box.x, y: box.y, width: box.width, height: box.height };
        }),
      );
    let posts = 0;
    page.on("request", (request) => {
      const path = new URL(request.url()).pathname;
      if (request.method() === "POST" && (path === "/categories" || path === "/categories/folders"))
        posts++;
    });
    await page.setViewportSize({ width: 1440, height: 600 });
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    for (const kind of ["folder", "category"] as const) {
      const point = await page.locator(".catalog-tree").evaluate((area) => {
        const box = area.getBoundingClientRect();
        const x = Math.max(1, Math.min(innerWidth - 1, box.left + box.width / 2));
        for (let y = Math.min(innerHeight - 8, box.bottom - 8); y >= Math.max(0, box.top); y -= 4) {
          const hit = document.elementFromPoint(x, y);
          if (hit?.closest(".catalog-tree") === area && !hit.closest(".catalog-tree-row"))
            return { x, y };
        }
        throw new Error("No exposed blank catalog tree padding in viewport");
      });
      const before = await geometry();
      const scroll = await page.evaluate(() => scrollY);
      const postsBefore = posts;
      await page.mouse.click(point.x, point.y, { button: "right" });
      await page.getByRole("menuitem", { name: `New ${kind}`, exact: true }).click();
      const create = page.locator(`[data-catalog-create-row="${kind}"]`);
      await expect(create).toBeVisible();
      await expect(create).toHaveCSS("position", "fixed");
      const popup = await create.boundingBox();
      if (!popup) throw new Error(`Missing ${kind} creation popover at ${page.url()}`);
      expect(popup.x).toBeGreaterThanOrEqual(0);
      expect(popup.y).toBeGreaterThanOrEqual(0);
      expect(popup.x + popup.width).toBeLessThanOrEqual(1440);
      expect(popup.y + popup.height).toBeLessThanOrEqual(600);
      const hint = create.locator(".catalog-inline-hint");
      await expect(hint).toBeVisible();
      const hintBox = await hint.boundingBox();
      if (!hintBox) throw new Error(`Missing ${kind} creation hint at ${page.url()}`);
      expect(hintBox.x).toBeGreaterThanOrEqual(0);
      expect(hintBox.y).toBeGreaterThanOrEqual(0);
      expect(hintBox.x + hintBox.width).toBeLessThanOrEqual(1440);
      expect(hintBox.y + hintBox.height).toBeLessThanOrEqual(600);
      expect(Math.abs(popup.x - point.x)).toBeLessThanOrEqual(popup.width + 16);
      expect(Math.abs(popup.y - point.y)).toBeLessThanOrEqual(popup.height + 16);
      expect(await geometry()).toEqual(before);
      expect(await page.evaluate(() => scrollY)).toBe(scroll);
      await create.getByLabel("Name", { exact: true }).fill(`Cancelled overlay ${kind}`);
      await page.keyboard.press("Escape");
      await expect(create).toHaveCount(0);
      expect(posts).toBe(postsBefore);
      await expect(
        page.locator(`[role="treeitem"][data-name="Cancelled overlay ${kind}"]`),
      ).toHaveCount(0);
      expect(await geometry()).toEqual(before);
      expect(await page.evaluate(() => scrollY)).toBe(scroll);
      if (kind === "folder") {
        await page.mouse.click(point.x, point.y, { button: "right" });
        await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
        const input = create.getByLabel("Name", { exact: true });
        await input.fill("Context overlay parent");
        const refusal = page.waitForResponse(
          (response) =>
            new URL(response.url()).pathname === "/categories/folders" &&
            response.request().method() === "POST",
        );
        await input.press("Enter");
        expect((await refusal).status()).toBe(409);
        await expect(input).toHaveAttribute("aria-invalid", "true");
        await expect(input).toHaveValue("Context overlay parent");
        const error = create.getByRole("alert");
        await expect(error).toHaveText("A folder with that name already exists here.");
        const errorBox = await error.boundingBox();
        if (!errorBox) throw new Error(`Missing folder refusal at ${page.url()}`);
        expect(errorBox.x).toBeGreaterThanOrEqual(0);
        expect(errorBox.y).toBeGreaterThanOrEqual(0);
        expect(errorBox.x + errorBox.width).toBeLessThanOrEqual(1440);
        expect(errorBox.y + errorBox.height).toBeLessThanOrEqual(600);
        expect(await geometry()).toEqual(before);
        expect(await page.evaluate(() => scrollY)).toBe(scroll);
        expect(posts).toBe(postsBefore + 1);
        await page.keyboard.press("Escape");
      }
    }
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.reload();
    await expect(page.locator('[role="treeitem"][data-name^="Cancelled overlay"]')).toHaveCount(0);
    await expect(
      page.locator('[role="treeitem"][data-level="folder"][data-name="Context overlay parent"]'),
    ).toHaveCount(1);
    await expect(folder).toHaveAttribute("data-parent-id", "0");
    for (const name of ["Alpha overlay", "Middle overlay", "Zulu overlay"]) {
      await expect(folder.locator(`[role="treeitem"][data-name="${name}"]`)).toHaveCount(1);
    }
    for (const kind of ["folder", "category"] as const) {
      const line = folder.locator(":scope > .catalog-tree-row");
      await line.click({ button: "right" });
      await page.getByRole("menuitem", { name: `New ${kind}`, exact: true }).click();
      const create = page.locator(`[data-catalog-create-row="${kind}"]`);
      const name = `Beta overlay ${kind}`;
      await create.getByLabel("Name", { exact: true }).fill(name);
      const before = await page.locator("#categories-background").innerHTML();
      const heading = await page.locator("h1").textContent();
      const url = page.url();
      let navigations = 0;
      const onRequest = (request: import("@playwright/test").Request) => {
        if (request.isNavigationRequest() && request.frame() === page.mainFrame()) navigations++;
      };
      page.on("request", onRequest);
      const response = page.waitForResponse(
        (result) =>
          new URL(result.url()).pathname ===
            (kind === "folder" ? "/categories/folders" : "/categories") &&
          result.request().method() === "POST",
      );
      await create.getByLabel("Name", { exact: true }).press("Enter");
      const saved = await response;
      expect(saved.status()).toBe(200);
      expect(saved.request().postData()).toContain('name="inline"');
      expect(saved.request().postData()).toMatch(/name="inline"\r?\n\r?\n1/);
      const created = page.locator(`[role="treeitem"][data-level="${kind}"][data-name="${name}"]`);
      await expect(created).toHaveAttribute("data-parent-id", parent);
      await expect.poll(() => page.locator("#categories-background").innerHTML()).not.toBe(before);
      expect(page.url()).toBe(url);
      expect(await page.locator("h1").textContent()).toBe(heading);
      expect(navigations).toBe(0);
      page.off("request", onRequest);
      const siblings = await folder
        .locator(`:scope > [role="group"] > [role="treeitem"][data-level="${kind}"]`)
        .evaluateAll((elements) =>
          elements.map((element) => (element as HTMLElement).dataset.name),
        );
      expect(siblings).toEqual(
        [...siblings].sort((a, b) =>
          (a || "").localeCompare(b || "", undefined, { sensitivity: "base" }),
        ),
      );
      await expect(create).toHaveCount(0);
      await expect(created).toHaveAttribute("aria-selected", "true");
    }
    await page.reload();
    for (const kind of ["folder", "category"]) {
      await expect(
        page.locator(`[role="treeitem"][data-level="${kind}"][data-name="Beta overlay ${kind}"]`),
      ).toHaveAttribute("data-parent-id", parent);
    }
  });

  test("compact inspector matches the desktop pane and preserves the mobile editor", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Compact inspector category");
    await row(page, "category", id).focus();
    const pane = page.getByRole("region", { name: "Properties", exact: true });
    const details = pane.getByRole("region", { name: "Compact inspector category", exact: true });
    await expect(details).toBeVisible();
    await expect(pane).toHaveCSS("width", "340px");
    const metrics = await details.evaluate((element) => {
      const title = element.querySelector("h2");
      const form = element.querySelector("[data-details-save]");
      const input = element.querySelector('input[name="name"]');
      if (!title || !form || !input) throw new Error("Missing inspector fields");
      return {
        background: getComputedStyle(element).backgroundColor,
        surface: getComputedStyle(element).getPropertyValue("--surface").trim(),
        title: getComputedStyle(title).fontSize,
        gap: getComputedStyle(form).rowGap,
        height: input.getBoundingClientRect().height,
      };
    });
    const surface = await page.evaluate(() => {
      const probe = document.createElement("span");
      probe.style.color = "var(--surface)";
      document.body.append(probe);
      const color = getComputedStyle(probe).color;
      probe.remove();
      return color;
    });
    expect(metrics.background).toBe(surface);
    expect(metrics.title).toBe("15px");
    expect(metrics.gap).toBe("12px");
    expect(metrics.height).toBeGreaterThanOrEqual(33);
    expect(metrics.height).toBeLessThanOrEqual(35);
    await expect(details.getByLabel("Keywords for suggestions")).toBeVisible();
    await expect(details.getByRole("link", { name: "Configure workflow" })).toBeVisible();
    await expect(details.getByRole("link", { name: "Configure SLA" })).toBeVisible();
    const description = details.getByLabel("Description", { exact: true });
    await description.fill("Compact draft");
    const original = await details.elementHandle();
    await page.setViewportSize({ width: 390, height: 900 });
    const mobile = page.getByRole("region", { name: "Compact inspector category", exact: true });
    await expect(mobile).toBeVisible();
    await expectPopoverInView(mobile);
    await expect(mobile.getByLabel("Description", { exact: true })).toHaveValue("Compact draft");
    expect(
      await original?.evaluate((element) => element === document.querySelector(".catalog-details")),
    ).toBe(true);
    await page.setViewportSize({ width: 390, height: 400 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await expect(
      row(page, "category", id).locator(":scope > .catalog-tree-row"),
    ).not.toBeInViewport();
    await expectPopoverInView(mobile);
    await expect(mobile.getByLabel("Description", { exact: true })).toHaveValue("Compact draft");
    await mobile.getByRole("button", { name: "Cancel", exact: true }).click();
    await page.reload();
    await row(page, "category", id)
      .locator(":scope > .catalog-tree-row [data-catalog-pen]")
      .click();
    await expect(
      page
        .getByRole("region", { name: "Compact inspector category", exact: true })
        .getByLabel("Description", { exact: true }),
    ).toHaveValue("");
  });
  for (const filtered of [false, true]) {
    test(`clean selected inspector returns after settled responsive resize ${filtered ? "filtered" : "plain"}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await loginAsSeeded(page);
      const id = await createCategoryViaUi(
        page,
        `Resize ${filtered ? "filtered" : "plain"} category`,
      );
      const name = `Resize ${filtered ? "filtered" : "plain"} category`;
      const url = base() + (filtered ? "/categories?q=Resize%20filtered" : "/categories");
      await page.goto(url);
      const target = row(page, "category", id);
      await target.focus();
      const inspector = page.locator("[data-catalog-inspector]");
      await expect(inspector.getByLabel("Name", { exact: true })).toHaveValue(name);
      await expect(target).toHaveAttribute("aria-selected", "true");
      await page.setViewportSize({ width: 390, height: 900 });
      // A clean, passively selected desktop editor is released on mobile.
      await expect(page.locator(".catalog-details")).toHaveCount(0);
      await expect(target).toHaveAttribute("aria-selected", "true");
      await page.setViewportSize({ width: 1440, height: 900 });
      await expect(target).toHaveAttribute("aria-selected", "true");
      await expect(inspector.getByLabel("Name", { exact: true })).toHaveValue(name);
      await expect(inspector.getByLabel("Description", { exact: true })).toHaveValue("");
      await expect(page).toHaveURL(url);
    });
  }

  test("move search opens from the menu and reaches a folder outside the filtered tree", async ({
    page,
  }) => {
    await loginAsSeeded(page);
    const destination = await createFolderViaUi(page, "Search target");
    const id = await createCategoryViaUi(page, "Search source");
    await page.goto(base() + "/categories?q=Search%20source");
    const source = row(page, "category", id);
    const searchField = page.locator("#category-search");
    await searchField.focus();
    await page.keyboard.press("End");
    await page.keyboard.type("m");
    await expect(searchField).toHaveValue("Search sourcem");
    await expect(page.getByRole("dialog", { name: "Move to…" })).toHaveCount(0);
    await searchField.fill("Search source");
    await source.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Move to…" }).click();
    await expect(page.getByRole("dialog", { name: "Move to…" })).toBeVisible();
    await page.keyboard.press("Escape");
    await openMoveMenu(source);
    const dialog = page.getByRole("dialog", { name: /move/i });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("searchbox").fill("Search target");
    await expect(dialog.getByRole("option", { name: "Search target", exact: true })).toBeVisible();
    const moved = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/categories/${id}/move` &&
        response.request().method() === "POST",
    );
    await dialog.getByRole("searchbox").press("Enter");
    expect((await moved).status()).toBe(200);
    await expect(page.locator(".catalog-toast")).toContainText("Moved");
    await page.goto(base() + "/categories");
    await expect(source).toHaveAttribute("data-parent-id", destination);
    const trigger = source.locator(":scope > .catalog-tree-row [data-category-menu]");
    const action = source.locator(":scope > .catalog-tree-row [data-catalog-move-to]");
    for (let i = 0; i < 2; i++) {
      await trigger.click();
      await expect(action).toBeVisible();
      await expect(action).not.toHaveAttribute("hidden");
      await page.keyboard.press("Escape");
    }
    await trigger.click();
    await action.click();
    await expect(page.getByRole("dialog", { name: "Move to…" })).toBeVisible();
    await page.keyboard.press("Escape");
  });
  test("nested Unicode move event and toast match the picker and undo persists", async ({
    page,
  }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Administración");
    const destination = await createFolderViaUi(page, "東京", parent);
    const id = await createCategoryViaUi(page, "Impresión ñ");
    const source = row(page, "category", id);
    await page.evaluate(() => {
      (window as typeof window & { movedEvents: unknown[] }).movedEvents = [];
      document.addEventListener("catalog:moved", (event) => {
        (window as typeof window & { movedEvents: unknown[] }).movedEvents.push(
          (event as CustomEvent).detail,
        );
      });
    });
    await openMoveMenu(source);
    const path = "Administración › 東京";
    const response = page.waitForResponse(
      (result) =>
        new URL(result.url()).pathname === `/categories/${id}/move` &&
        result.request().method() === "POST",
    );
    await page.getByRole("option", { name: path, exact: true }).click();
    expect((await response).status()).toBe(200);
    await expect(source).toHaveAttribute("data-parent-id", destination);
    const message = `Moved 'Impresión ñ' to ${path}`;
    await expect(page.locator(".catalog-toast")).toContainText(message);
    const events = await page.evaluate(
      () =>
        (window as typeof window & { movedEvents: { path: string; message: string }[] })
          .movedEvents,
    );
    expect(events).toHaveLength(1);
    expect(events[0].path).toBe(path);
    expect(events[0].message).toBe(message);
    const undo = page.waitForResponse(
      (result) =>
        new URL(result.url()).pathname === `/categories/${id}/move` &&
        result.request().method() === "POST",
    );
    await page.locator(".catalog-toast").getByRole("button", { name: "Undo" }).click();
    expect((await undo).status()).toBe(200);
    await expect(source).toHaveAttribute("data-parent-id", "0");
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", "0");
  });

  test("folder move search excludes cycles and supports root with undo", async ({ page }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Move parent");
    const child = await createFolderViaUi(page, "Move child", parent);
    const folder = row(page, "folder", parent);
    const trigger = folder.locator(":scope > .catalog-tree-row [data-category-menu]");
    await trigger.click();
    const moveAction = folder.locator(":scope > .catalog-tree-row [data-catalog-move-to]");
    await expect(moveAction).toBeVisible();
    await expect(moveAction).not.toHaveAttribute("hidden");
    await page.keyboard.press("Escape");
    await trigger.click();
    await expect(moveAction).not.toHaveAttribute("hidden");
    await moveAction.click();
    const dialog = page.getByRole("dialog", { name: "Move to…" });
    await expect(dialog.getByRole("option", { name: "Move parent", exact: true })).toHaveCount(0);
    await expect(
      dialog.getByRole("option", { name: "Move parent › Move child", exact: true }),
    ).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(folder).toBeFocused();
    const nested = row(page, "folder", child);
    await openMoveMenu(nested);
    await page.getByRole("option", { name: "Top level", exact: true }).click();
    await expect(nested).toHaveAttribute("data-parent-id", "0");
    await page.locator(".catalog-toast").getByRole("button", { name: "Undo" }).click();
    await expect(nested).toHaveAttribute("data-parent-id", parent);
  });
  test("move search distinguishes duplicate leaf paths on a narrow viewport", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await loginAsSeeded(page);
    const left = await createFolderViaUi(page, "Move path A");
    const right = await createFolderViaUi(page, "Move path B");
    await createFolderViaUi(page, "Shared leaf", left);
    await page.goto(base() + `/categories/folders/new?parent_id=${right}`);
    const drawer = page.getByRole("dialog", { name: /New folder/i });
    await drawer.getByLabel("Name", { exact: true }).fill("Shared leaf");
    await drawer.getByRole("button", { name: "Create folder" }).click();
    await expect(drawer).toHaveCount(0);
    await page.goto(base() + "/categories");
    const child = row(page, "folder", right).locator(
      ':scope > [role="group"] > [role="treeitem"][data-name="Shared leaf"]',
    );
    await expect(child).toHaveCount(1);
    const destination = await child.getAttribute("data-catalog-id");
    expect(destination).toBeTruthy();
    const id = await createCategoryViaUi(page, "Move path category");
    const source = row(page, "category", id);
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      await source.focus();
      const treeTop = await page
        .locator('[role="tree"]')
        .evaluate((el) => el.getBoundingClientRect().top + scrollY);
      await openMoveMenu(source);
      const dialog = page.getByRole("dialog", { name: "Move to…" });
      await expectPopoverInView(dialog);
      expect(
        await page
          .locator('[role="tree"]')
          .evaluate((el) => el.getBoundingClientRect().top + scrollY),
      ).toBe(treeTop);
      await dialog.getByRole("searchbox").fill("Shared leaf");
      await expect(dialog.getByRole("option")).toHaveCount(2);
      await expect(dialog.getByRole("option", { name: "Move path A › Shared leaf" })).toBeVisible();
      await expect(dialog.getByRole("option", { name: "Move path B › Shared leaf" })).toBeVisible();
      if (theme === "light") {
        await page.keyboard.press("Escape");
        await expect(source).toBeFocused();
      } else await dialog.getByRole("option", { name: "Move path B › Shared leaf" }).click();
    }
    await expect(source).toHaveAttribute("data-parent-id", destination);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", destination);
  });
  test("move search refuses a duplicate folder without changing its parent", async ({ page }) => {
    await loginAsSeeded(page);
    const destination = await createFolderViaUi(page, "Refusal destination");
    const origin = await createFolderViaUi(page, "Refusal origin");
    await createFolderViaUi(page, "Refusal duplicate", destination);
    const id = await createFolderViaUi(page, "Refusal temporary", origin);
    const source = row(page, "folder", id);
    await renameFolder(source);
    await source.locator(".catalog-rename-input").fill("Refusal duplicate");
    await source.locator(".catalog-rename-input").press("Enter");
    await expect(source).toHaveAttribute("data-name", "Refusal duplicate");
    await openMoveMenu(source);
    const dialog = page.getByRole("dialog", { name: "Move to…" });
    const response = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/folders/${id}/move` &&
        r.request().method() === "POST",
    );
    await dialog.getByRole("option", { name: "Refusal destination", exact: true }).click();
    expect((await response).status()).toBe(409);
    await expect(page.locator(".catalog-toast")).toContainText(
      "A folder with that name already exists here.",
    );
    await expect(source).toHaveAttribute("data-parent-id", origin);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", origin);
  });
  test("folder plus keeps a switch that preserves the draft; header creators are fixed-kind", async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Switch parent");
    const folder = row(page, "folder", id);
    const add = folder.locator(":scope > .catalog-tree-row [data-catalog-add]");
    await add.scrollIntoViewIfNeeded();
    await add.click({ trial: true });
    const beforeFolder = await creatorTreeSnapshot(page);
    await add.click();
    const create = page.locator("[data-catalog-create-row]");
    await expect(create).toHaveAttribute("data-folder-id", id);
    await expectButtonCreator(page, add);
    expect(await creatorTreeSnapshot(page)).toEqual(beforeFolder);
    await expect(create).toHaveAttribute("data-catalog-create-row", "category");
    await expect(create.locator(".catalog-inline-hint")).toHaveText(
      "Enter to create · Esc to cancel",
    );
    const input = create.locator('input[name="name"]');
    await input.fill("Switch draft");
    await input.press("Control+a");
    await input.press("Control+x");
    await expect(input).toHaveValue("");
    await input.press("Control+v");
    await expect(input).toHaveValue("Switch draft");
    await create.getByRole("button", { name: "Folder", exact: true }).click();
    await expect(input).toHaveValue("Switch draft");
    await expect(create).toHaveAttribute("data-catalog-create-row", "folder");
    await expect(create.locator(".catalog-inline-hint")).toHaveText(
      "Enter to create · Esc to cancel",
    );
    await create.getByRole("button", { name: "Category", exact: true }).click();
    await expect(create.locator(".catalog-inline-hint")).toHaveText(
      "Enter to create · Esc to cancel",
    );
    await expect(input).toHaveValue("Switch draft");
    expect(await creatorTreeSnapshot(page)).toEqual(beforeFolder);
    await page.keyboard.press("Escape");
    await expect(add).toBeFocused();
    expect(await creatorTreeSnapshot(page)).toEqual(beforeFolder);
    const rootButton = page.getByRole("button", { name: "New category", exact: true });
    await rootButton.scrollIntoViewIfNeeded();
    await rootButton.click({ trial: true });
    const beforeRoot = await creatorTreeSnapshot(page);
    await rootButton.focus();
    await rootButton.press("Enter");
    const rootCreate = page.locator("[data-catalog-create-row]");
    await expect(rootCreate).toHaveAttribute("data-folder-id", "");
    await expectButtonCreator(page, rootButton);
    expect(await creatorTreeSnapshot(page)).toEqual(beforeRoot);
    await expect(rootCreate).toHaveAttribute("data-catalog-create-row", "category");
    await expect(rootCreate.locator(".catalog-inline-hint")).toBeVisible();
    await expect(rootCreate.locator(".catalog-create-switch")).toHaveCount(0);
    await expect(rootCreate.getByRole("button", { name: "Folder", exact: true })).toHaveCount(0);
    await rootCreate.locator("input").fill("Root category switch");
    await rootCreate.locator("input").press("Enter");
    await expect(
      page.locator('[role="treeitem"][data-name="Root category switch"]'),
    ).toHaveAttribute("data-parent-id", "0");
    await expect(rootCreate).toHaveCount(0);
    await expect(
      page.locator('[role="treeitem"][data-level="category"][data-name="Root category switch"]'),
    ).toHaveAttribute("aria-selected", "true");
    const folderButton = page.getByRole("button", { name: "New folder", exact: true });
    await folderButton.focus();
    await folderButton.press("Enter");
    await expect(rootCreate).toHaveAttribute("data-catalog-create-row", "folder");
    await expect(rootCreate.locator(".catalog-create-switch")).toHaveCount(0);
    await expect(rootCreate.locator(".catalog-inline-hint")).toBeVisible();
    await rootCreate.locator("input").fill("Root folder switch");
    await rootCreate.locator("input").press("Enter");
    await expect(rootCreate).toHaveCount(0);
    const createdFolder = page.locator(
      '[role="treeitem"][data-level="folder"][data-name="Root folder switch"]',
    );
    await expect(createdFolder).toHaveAttribute("data-parent-id", "0");
    await expect(createdFolder).toHaveAttribute("aria-selected", "true");
    await rootButton.press("Space");
    await expect(rootCreate.locator(".catalog-inline-hint")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(rootButton).toBeFocused();

    // Use the existing per-user preference rather than changing instance language.
    await page.goto(base() + "/preferences");
    const language = page.locator('select[name="language"]');
    const previousLanguage = await language.inputValue();
    try {
      await language.selectOption("es");
      await page.locator('form[action="/preferences/language"] button').click();
      await expect(page.locator("html")).toHaveAttribute("lang", "es");
      await page.goto(base() + "/categories");
      await expect(
        page.getByRole("button", { name: "Nueva categoría", exact: true }),
      ).toBeVisible();
      await expect(page.getByRole("button", { name: /nivel superior/i })).toHaveCount(0);
      await page.getByRole("button", { name: "Nueva carpeta", exact: true }).click();
      await expect(rootCreate.locator(".catalog-inline-hint")).toHaveText(
        "Enter para crear · Esc para cancelar",
      );
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: "Nueva categoría", exact: true }).click();
      await expect(rootCreate.locator(".catalog-inline-hint")).toHaveText(
        "Enter para crear · Esc para cancelar",
      );
      await page.keyboard.press("Escape");
    } finally {
      await page.goto(base() + "/preferences");
      await page.locator('select[name="language"]').selectOption(previousLanguage);
      await page.locator('form[action="/preferences/language"] button').click();
    }
    expect(pageErrors).toEqual([]);
  });

  test("shortcut hints live in the row menus and the shortcuts link is gone", async ({ page }) => {
    await loginAsSeeded(page);
    const folderID = await createFolderViaUi(page, "Hint folder");
    const categoryID = await createCategoryViaUi(page, "Hint category");
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(base() + "/categories");
    await expect(page.getByRole("button", { name: "Keyboard shortcuts" })).toHaveCount(0);
    const folder = row(page, "folder", folderID);
    const category = row(page, "category", categoryID);

    const hints = [{ name: "Cut", hint: "Ctrl+X", keys: "Control+X" }];
    for (const target of [folder, category]) {
      const line = target.locator(":scope > .catalog-tree-row");
      await line.hover();
      await line.locator("[data-category-menu]").click();
      const menu = line.locator('.catalog-row-menu [role="menu"]');
      for (const { name, hint, keys } of hints) {
        const item = menu.getByRole("menuitem", { name, exact: true });
        await expect(item).toBeVisible();
        await expect(item).toHaveAttribute("aria-keyshortcuts", keys);
        await expect(item.locator("kbd")).toHaveText(hint);
        await expect(item.locator("kbd")).toHaveAttribute("aria-hidden", "true");
      }
      const move = menu.getByRole("menuitem", { name: "Move to…", exact: true });
      await expect(move).toBeVisible();
      await expect(move).not.toHaveAttribute("aria-keyshortcuts");
      await expect(move.locator("kbd")).toHaveCount(0);
      await expect(menu.getByRole("menuitem", { name: /Ctrl|Control/ })).toHaveCount(0);
      await page.keyboard.press("Escape");
    }

    // Paste here only exists once something is cut.
    await folder.focus();
    await page.keyboard.press("Control+x");
    const folderLine = folder.locator(":scope > .catalog-tree-row");
    await folderLine.hover();
    await folderLine.locator("[data-category-menu]").click();
    const paste = folderLine
      .locator('.catalog-row-menu [role="menu"]')
      .getByRole("menuitem", { name: "Paste here", exact: true });
    await expect(paste).toHaveAttribute("aria-keyshortcuts", "Control+V");
    await expect(paste.locator("kbd")).toHaveText("Ctrl+V");
    await page.keyboard.press("Escape");

    // The context menu carries the same hints.
    await category.locator(":scope > .catalog-tree-row").click({ button: "right" });
    const context = page.locator(".catalog-context-menu");
    const contextMove = context.getByRole("menuitem", { name: "Move to…", exact: true });
    await expect(contextMove).not.toHaveAttribute("aria-keyshortcuts");
    await expect(contextMove.locator("kbd")).toHaveCount(0);
    await expect(context.getByRole("menuitem", { name: "Cut", exact: true })).toHaveAttribute(
      "aria-keyshortcuts",
      "Control+X",
    );
    await page.keyboard.press("Escape");
  });

  test("shortcut hints fit at 390 and hide on touch", async ({ page, browser }) => {
    await loginAsSeeded(page);
    const folderID = await createFolderViaUi(page, "Hint narrow folder");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(base() + "/categories");
    const line = row(page, "folder", folderID).locator(":scope > .catalog-tree-row");
    await line.locator("[data-category-menu]").click();
    const menu = line.locator('.catalog-row-menu [role="menu"]');
    await expect(menu.locator("kbd").first()).toBeVisible();
    await expectPopoverInView(menu);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    for (const item of await menu.getByRole("menuitem").all()) {
      const box = await item.boundingBox();
      expect(box && box.height < 48).toBe(true);
    }
    await page.keyboard.press("Escape");

    const context = await browser.newContext({
      hasTouch: true,
      viewport: { width: 390, height: 844 },
      storageState: await page.context().storageState(),
    });
    try {
      const touch = await context.newPage();
      await touch.goto(base() + "/categories");
      expect(await touch.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);
      const touchLine = row(touch, "folder", folderID).locator(":scope > .catalog-tree-row");
      await touchLine.locator("[data-category-menu]").click();
      const touchMenu = touchLine.locator('.catalog-row-menu [role="menu"]');
      const move = touchMenu.getByRole("menuitem", { name: "Move to…", exact: true });
      await expect(move).toBeVisible();
      await expect(move).not.toHaveAttribute("aria-keyshortcuts");
      await expect(move.locator("kbd")).toHaveCount(0);
      await expect(touchMenu.locator("kbd").first()).toBeHidden();
    } finally {
      await context.close();
    }
  });
  test("duplicate category creation keeps the tree unchanged", async ({ page }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Duplicate category parent");
    const folder = row(page, "folder", parent);
    const add = folder.locator(":scope > .catalog-tree-row [data-catalog-add]");
    await add.scrollIntoViewIfNeeded();
    await add.click({ trial: true });
    const before = await creatorTreeSnapshot(page);
    await add.click();
    const create = page.locator("[data-catalog-create-row]");
    await expect(create).toHaveAttribute("data-folder-id", parent);
    expect(await creatorTreeSnapshot(page)).toEqual(before);
    const input = create.locator('input[name="name"]');
    await expect(create.locator(".catalog-inline-hint")).toBeVisible();
    await input.fill("General");
    const refused = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/categories" &&
        response.request().method() === "POST",
    );
    await input.press("Enter");
    expect((await refused).status()).toBe(409);
    await expect(input).toHaveAttribute("aria-invalid", "true");
    await expect(input).toHaveValue("General");
    await expect(create.locator(".catalog-inline-hint")).toBeHidden();
    await expect(input).toHaveAttribute("aria-describedby", "catalog-create-error");
    await expect(create.locator("#catalog-create-error")).toHaveText(
      "A category with that name already exists.",
    );
    expect(await creatorTreeSnapshot(page)).toEqual(before);
    await expect(
      page.locator('[role="treeitem"][data-level="category"][data-name="General"]'),
    ).toHaveCount(1);
    await page.keyboard.press("Escape");
    await expect(add).toBeFocused();
    expect(await creatorTreeSnapshot(page)).toEqual(before);
    await page.reload();
    await expect(
      page.locator('[role="treeitem"][data-level="category"][data-name="General"]'),
    ).toHaveAttribute("data-parent-id", "0");
    await expect(folder.locator('[role="treeitem"][data-name="General"]')).toHaveCount(0);
  });
  test("no-script creation routes preserve the selected folder", async ({ page, browser }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "No script parent");
    const context = await browser.newContext({
      javaScriptEnabled: false,
      storageState: await page.context().storageState(),
    });
    try {
      const plain = await context.newPage();
      await plain.goto(base() + `/categories/folders/new?parent_id=${parent}`);
      await plain.locator('input[name="name"]').fill("No script child");
      await plain.getByRole("button", { name: "Create folder" }).click();
      await expect(
        plain.locator('[data-level="folder"][data-name="No script child"]'),
      ).toHaveAttribute("data-parent-id", parent);
      await plain.goto(base() + `/categories/new?folder_id=${parent}`);
      await plain.locator('input[name="name"]').fill("No script category");
      await plain.getByRole("button", { name: "Create category" }).click();
      await expect(
        plain.locator('[data-level="category"][data-name="No script category"]'),
      ).toHaveAttribute("data-parent-id", parent);
    } finally {
      await context.close();
    }
  });
  test("the row pencil is hidden beside the permanent inspector and kept for the popover", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Pencil folder");
    const folder = row(page, "folder", id);
    const category = page.locator('[role="treeitem"][data-level="category"][data-name="General"]');
    const inspector = page.locator("[data-catalog-inspector]");
    for (const target of [folder, category]) {
      const line = target.locator(":scope > .catalog-tree-row");
      const pen = line.locator("button[data-catalog-pen]");
      await line.hover();
      await expect(pen).toBeHidden();
      await pen.evaluate((el) => (el as HTMLElement).focus());
      expect(
        await page.evaluate(() => document.activeElement?.hasAttribute("data-catalog-pen")),
      ).toBe(false);
      await target.focus();
      for (let i = 0; i < 6; i++) {
        await page.keyboard.press("Tab");
        expect(
          await page.evaluate(() => document.activeElement?.hasAttribute("data-catalog-pen")),
        ).toBe(false);
      }
      await target.focus();
      const name = await target.getAttribute("data-name");
      await expect(inspector.getByLabel("Name", { exact: true })).toHaveValue(name ?? "");
    }
    await renameFolder(folder);
    const input = folder.locator('input[name="name"]');
    await expect(input).toHaveValue("Pencil folder");
    await expect(input).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(input).toHaveCount(0);

    await page.setViewportSize({ width: 390, height: 900 });
    for (const target of [folder, category]) {
      const pen = target.locator(":scope > .catalog-tree-row button[data-catalog-pen]");
      await expect(pen).toBeVisible();
      await pen.click();
      const details = target.locator(".catalog-details-popover");
      await expect(details).toBeVisible();
      await expectPopoverInView(details);
      await page.keyboard.press("Escape");
      await expect(details).toHaveCount(0);
    }
  });
  test("row actions reveal without shifting and open details for either kind", async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 900 });
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Cluster folder");
    const folder = row(page, "folder", id);
    const category = page.locator('[role="treeitem"][data-level="category"][data-name="General"]');
    for (const target of [folder, category]) {
      const line = target.locator(":scope > .catalog-tree-row");
      const height = await line.evaluate((el) => el.getBoundingClientRect().height);
      await line.hover();
      const pen = line.locator("button[data-catalog-pen]");
      await expect(pen).toBeVisible();
      expect(await line.evaluate((el) => el.getBoundingClientRect().height)).toBe(height);
      await pen.click();
      const details = target.locator(".catalog-details-popover");
      await expect(details).toBeVisible();
      if (target === category) {
        await expect(details.getByRole("link", { name: "Configure workflow" })).toBeVisible();
        await expect(details.getByRole("link", { name: "Configure SLA" })).toBeVisible();
      }
      await page.keyboard.press("Escape");
      await target.focus();
      await page.keyboard.press("Enter");
      await expect(details).toBeVisible();
      await page.keyboard.press("Escape");
      await line.locator("[data-category-menu]").click();
      const menu = line.locator('.catalog-row-menu [role="menu"]');
      await expect(menu.getByRole("menuitem", { name: /Rename|Details|Configure/ })).toHaveCount(0);
      await expect(menu.getByRole("menuitem", { name: "Cut", exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
    }
    await folder.focus();
    await folder.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    await expect(
      page
        .locator("[data-catalog-create-row]")
        .getByRole("button", { name: "Folder", exact: true }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
  });
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  for (const kind of ["category", "folder"] as const) {
    test(`drag ${kind} by name without navigating`, async ({ page }) => {
      const obs = collectObservability(page);
      await loginAsSeeded(page);
      const folder = await createFolderViaUi(page, `Name destination ${kind}`);
      const name = `Name move ${kind}`;
      const id =
        kind === "category"
          ? await createCategoryViaUi(page, name)
          : await createFolderViaUi(page, name);
      const source = row(page, kind, id);
      const dest = row(page, "folder", folder);
      const label = source.locator(":scope > .catalog-tree-row .catalog-node-name");
      await label.scrollIntoViewIfNeeded();
      const start = await label.boundingBox();
      if (!start) throw new Error(`missing ${kind} name at ${page.url()}`);
      await page.mouse.move(start.x + start.width / 2, start.y + start.height / 2);
      await page.mouse.down();
      await page.mouse.move(start.x + start.width / 2 + 10, start.y + start.height / 2, {
        steps: 4,
      });
      await expect(page.locator(".catalog-drag-ghost")).toHaveText(name);
      const destinationLine = dest.locator(":scope > .catalog-tree-row");
      await destinationLine.scrollIntoViewIfNeeded();
      const end = await destinationLine.boundingBox();
      if (!end) throw new Error(`missing destination for ${kind} at ${page.url()}`);
      const point = { x: end.x + end.width / 2, y: end.y + end.height / 2 };
      const hit = await dest.evaluate((element, p) => {
        const target = document.elementFromPoint(p.x, p.y);
        return {
          x: p.x,
          y: p.y,
          width: innerWidth,
          height: innerHeight,
          inside: !!target && element.querySelector(":scope > .catalog-tree-row")?.contains(target),
        };
      }, point);
      console.log(`drop geometry ${kind}: ${JSON.stringify(hit)}`);
      expect(hit.x).toBeGreaterThanOrEqual(0);
      expect(hit.x).toBeLessThan(hit.width);
      expect(hit.y).toBeGreaterThanOrEqual(0);
      expect(hit.y).toBeLessThan(hit.height);
      expect(hit.inside).toBe(true);
      await page.mouse.move(point.x, point.y, { steps: 10 });
      await expect(destinationLine).toHaveClass(/catalog-drop-valid/);
      expect(await page.evaluate(() => window.getSelection()?.toString())).toBe("");
      await page.mouse.up();
      await expect(source).toHaveAttribute("data-parent-id", folder);
      await expect(page.locator(".catalog-toast")).toContainText(`Moved '${name}'`);
      await expect(
        page.locator(".catalog-toast").getByRole("button", { name: "Undo" }),
      ).toBeVisible();
      await expect(page).toHaveURL(base() + "/categories");
      await page.reload();
      await expect(source).toHaveAttribute("data-parent-id", folder);
      expect(obs).toEqual({
        consoleErrors: [],
        pageErrors: [],
        failedRequests: [],
        failedResponses: [],
      });
    });
  }

  for (const kind of ["category", "folder"] as const) {
    test(`drag nested ${kind} onto blank tree space and undo`, async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 2000 });
      await loginAsSeeded(page);
      const parent = await createFolderViaUi(page, `Root parent ${kind}`);
      const id =
        kind === "category"
          ? await createCategoryViaUi(page, `Root item ${kind}`)
          : await createFolderViaUi(page, `Root item ${kind}`, parent);
      const source = row(page, kind, id);
      if (kind === "category") {
        await source.locator(":scope > .catalog-tree-row").hover();
        await source
          .locator(".catalog-drag-grip")
          .dragTo(row(page, "folder", parent).locator(":scope > .catalog-tree-row"));
        const setupToast = page.locator(".catalog-toast");
        await expect(setupToast).toContainText(`Moved 'Root item category'`);
        await setupToast.getByRole("button", { name: "Dismiss confirmation" }).click();
        await expect(setupToast).toBeHidden();
      }
      await expect(source).toHaveAttribute("data-parent-id", parent);
      const tree = page.locator(".catalog-tree");
      await source.locator(":scope > .catalog-tree-row").scrollIntoViewIfNeeded();
      await source.locator(":scope > .catalog-tree-row").hover();
      const grip = await source.locator(".catalog-drag-grip").boundingBox();
      if (!grip) throw new Error("missing grip");
      await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
      await page.mouse.down();
      await page.mouse.move(grip.x + grip.width / 2 + 10, grip.y + grip.height / 2, { steps: 4 });
      await expect(page.locator(".catalog-drag-ghost")).toBeVisible();
      await tree.evaluate((el) =>
        window.scrollBy(0, el.getBoundingClientRect().bottom - innerHeight + 30),
      );
      const hit = await tree.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        const root = element.querySelector('[role="tree"]')?.getBoundingClientRect();
        const top = Math.max(0, rect.bottom - parseFloat(getComputedStyle(element).paddingBottom));
        const bottom = Math.min(innerHeight, rect.bottom);
        const candidates = [];
        for (const fraction of [0.25, 0.5, 0.75]) {
          const x = Math.max(1, Math.min(innerWidth - 1, rect.left + rect.width * fraction));
          const y = (top + bottom) / 2;
          const target = document.elementFromPoint(x, y);
          const candidate = {
            x,
            y,
            tag: target?.tagName,
            id: target?.id,
            classes: target?.className,
            overlay: target?.closest('.catalog-toast, .tabbar, [role="menu"], .catalog-tree-row')
              ?.className,
            root: target?.matches('.catalog-tree, [role="tree"]') === true,
          };
          candidates.push(candidate);
        }
        return {
          tree: rect.toJSON(),
          root: root?.toJSON(),
          top,
          bottom,
          scrollY,
          viewport: { width: innerWidth, height: innerHeight },
          candidates,
        };
      });
      console.log(`root drop ${kind}: ${JSON.stringify(hit)}`);
      expect(hit.bottom, JSON.stringify(hit)).toBeGreaterThan(hit.top);
      const point = hit.candidates.find((candidate) => candidate.root);
      expect(point, JSON.stringify(hit)).toBeDefined();
      if (!point) throw new Error(`No exposed root padding: ${JSON.stringify(hit)}`);
      expect(
        await page.evaluate(
          ({ x, y }) => document.elementFromPoint(x, y)?.matches('.catalog-tree, [role="tree"]'),
          point,
        ),
      ).toBe(true);
      await page.mouse.move(point.x, point.y, { steps: 10 });
      await expect(tree).toHaveClass(/catalog-drop-valid/);
      await expect(page.locator(".catalog-drop-top")).toHaveCount(0);
      await page.mouse.up();
      await expect(source).toHaveAttribute("data-parent-id", "0");
      await page.locator(".catalog-toast").getByRole("button", { name: "Undo" }).click();
      await expect(source).toHaveAttribute("data-parent-id", parent);
      await page.reload();
      await expect(source).toHaveAttribute("data-parent-id", parent);
    });
  }

  test("dropping on non-targets never moves a nested category", async ({ page }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Non-target parent");
    const id = await createCategoryViaUi(page, "Non-target source");
    const other = await createCategoryViaUi(page, "Non-target category");
    const source = row(page, "category", id);
    const folder = row(page, "folder", parent);
    await source.locator(":scope > .catalog-tree-row").hover();
    await source.locator(".catalog-drag-grip").dragTo(folder.locator(":scope > .catalog-tree-row"));
    await expect(source).toHaveAttribute("data-parent-id", parent);
    await renameFolder(folder);
    let moves = 0;
    page.on("request", (request) => {
      if (
        request.method() === "POST" &&
        new URL(request.url()).pathname === `/categories/${id}/move`
      )
        moves++;
    });
    for (const target of [
      row(page, "category", other).locator(":scope > .catalog-tree-row"),
      folder.locator(".catalog-rename-input"),
      page.getByRole("heading", { level: 1 }),
    ]) {
      await source.locator(":scope > .catalog-tree-row").hover();
      const grip = await source.locator(".catalog-drag-grip").boundingBox();
      const box = await target.boundingBox();
      if (!grip || !box) throw new Error("missing non-target");
      await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
      await page.mouse.down();
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 10 });
      await expect(page.locator(".catalog-drop-valid")).toHaveCount(0);
      await page.mouse.up();
      await expect(source).toHaveAttribute("data-parent-id", parent);
    }
    expect(moves).toBe(0);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", parent);
  });

  test("cutting a row shows a floating chip and never shifts the tree", async ({ page }) => {
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Quiet cut category");
    const item = row(page, "category", id);
    const top = () =>
      page
        .locator('[role="tree"]')
        .evaluate((el) => el.getBoundingClientRect().top + window.scrollY);
    const before = await top();
    await item.focus();
    await page.keyboard.press("Control+x");
    await expect(item).toHaveClass(/catalog-cut/);
    await expect(page.locator("#catalog-live")).toHaveText(
      "Cut: Quiet cut category — paste into a folder (Esc to cancel)",
    );
    // What is cut stays visible in a floating chip that does not move the tree.
    const chip = page.locator(".catalog-cut-chip");
    await expect(chip).toBeVisible();
    await expect(chip).toContainText("Cut: Quiet cut category");
    await expect(chip).toHaveCSS("position", "fixed");
    expect(await chip.getAttribute("aria-live")).toBeNull();
    expect(await top()).toBe(before);
    await page.keyboard.press("Escape");
    await expect(item).not.toHaveClass(/catalog-cut/);
    await expect(chip).toBeHidden();
    expect(await top()).toBe(before);
    // Its Cancel clears the cut and returns focus to the row.
    await item.focus();
    await page.keyboard.press("Control+x");
    await expect(chip).toBeVisible();
    await chip.getByRole("button", { name: "Cancel" }).click();
    await expect(item).not.toHaveClass(/catalog-cut/);
    await expect(chip).toBeHidden();
    await expect(item).toBeFocused();
    // On a phone the chip clears the bottom tab bar and fits the screen, and
    // a move toast still showing sits clear of it.
    const folderId = await createFolderViaUi(page, "Quiet cut folder");
    const folder = row(page, "folder", folderId);
    await page.setViewportSize({ width: 390, height: 844 });
    await item.focus();
    await page.keyboard.press("Control+x");
    await folder.focus();
    await page.keyboard.press("Control+v");
    await expect(item).toHaveAttribute("data-parent-id", folderId);
    await expect(chip).toBeHidden();
    await item.focus();
    await page.keyboard.press("Control+x");
    await expect(page.locator(".catalog-toast")).toBeVisible();
    const [chipBox, tabbarBox] = await Promise.all([
      chip.boundingBox(),
      page.locator(".tabbar").boundingBox(),
    ]);
    expect(chipBox!.y + chipBox!.height).toBeLessThanOrEqual(tabbarBox!.y);
    expect(chipBox!.x).toBeGreaterThanOrEqual(0);
    expect(chipBox!.x + chipBox!.width).toBeLessThanOrEqual(390);
    const toastBox = await page.locator(".catalog-toast").boundingBox();
    expect(toastBox!.y + toastBox!.height).toBeLessThanOrEqual(chipBox!.y);
  });

  test("catalog clipboard restores Cut and conditional Paste", async ({ page }) => {
    await loginAsSeeded(page);
    const folderId = await createFolderViaUi(page, "Clipboard folder");
    const id = await createCategoryViaUi(page, "Clipboard category");
    const item = row(page, "category", id);
    const folder = row(page, "folder", folderId);
    let moves = 0;
    page.on("request", (request) => {
      if (request.method() === "POST" && new URL(request.url()).pathname.endsWith("/move")) moves++;
    });
    await item.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Cut", exact: true }).click();
    await expect(item).toHaveClass(/catalog-cut/);
    await expect(page.locator("#catalog-live")).toHaveText(
      "Cut: Clipboard category — paste into a folder (Esc to cancel)",
    );
    await folder.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Paste here", exact: true }).click();
    await expect(item).toHaveAttribute("data-parent-id", folderId);
    await expect(item).not.toHaveClass(/catalog-cut/);
    await expect(page.locator(".catalog-toast")).toHaveCSS("position", "fixed");
    await item.locator(":scope > .catalog-tree-row [data-category-menu]").click();
    await item.getByRole("menuitem", { name: "Cut", exact: true }).click();
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Shift+F10");
    await page.getByRole("menuitem", { name: "Paste here", exact: true }).click();
    await expect(item).toHaveAttribute("data-parent-id", "0");
    await folder.locator(":scope > .catalog-tree-row [data-category-menu]").click();
    await folder
      .locator(":scope > .catalog-tree-row")
      .getByRole("menuitem", { name: "Cut", exact: true })
      .click();
    await expect(folder).toHaveClass(/catalog-cut/);
    await page.keyboard.press("Escape");
    await expect(folder).not.toHaveClass(/catalog-cut/);
    await folder.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Cut", exact: true }).click();
    await page.keyboard.press("Escape");
    await item.focus();
    await page.keyboard.press("Control+x");
    await folder.focus();
    await page.keyboard.press("Control+v");
    await expect(item).toHaveAttribute("data-parent-id", folderId);
    await item.focus();
    await page.keyboard.press("Control+x");
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Control+v");
    await expect(item).toHaveAttribute("data-parent-id", "0");
    expect(moves).toBe(4);
    await item.focus();
    await page.keyboard.press("Meta+x");
    await expect(item).toHaveClass(/catalog-cut/);
    await folder.focus();
    await page.keyboard.press("Meta+v");
    await expect(item).toHaveAttribute("data-parent-id", folderId);
    await item.focus();
    await page.keyboard.press("Meta+x");
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Meta+v");
    await expect(item).toHaveAttribute("data-parent-id", "0");
    expect(moves).toBe(6);
    const destinationId = await createFolderViaUi(page, "Clipboard destination");
    const destination = row(page, "folder", destinationId);
    await folder.locator(":scope > .catalog-tree-row [data-category-menu]").click();
    await folder
      .locator(":scope > .catalog-tree-row")
      .getByRole("menuitem", { name: "Cut", exact: true })
      .click();
    await folder.focus();
    await page.keyboard.press("Control+v");
    await expect(page.locator("#catalog-live")).toHaveText("Can't move a folder into itself");
    await expect(folder).toHaveAttribute("data-parent-id", "0");
    await destination.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await page.getByRole("menuitem", { name: "Paste here", exact: true }).click();
    await expect(folder).toHaveAttribute("data-parent-id", destinationId);
    await folder.focus();
    await page.keyboard.press("Control+x");
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Control+v");
    await expect(folder).toHaveAttribute("data-parent-id", "0");
    await item.focus();
    await page.keyboard.press("Enter");
    const input = page.locator(".catalog-details").getByLabel("Name", { exact: true });
    await input.fill("Native clipboard");
    await input.press("Control+a");
    await input.press("Control+x");
    await expect(input).toHaveValue("");
    await input.press("Control+v");
    await expect(input).toHaveValue("Native clipboard");
    await input.fill("Clipboard category");
    await input.press("Escape");
    await page.reload();
    await expect(item).toHaveAttribute("data-parent-id", "0");
  });

  test("move toast keeps Undo reachable from the keyboard and announces once", async ({ page }) => {
    await page.clock.install();
    await loginAsSeeded(page);
    const folderId = await createFolderViaUi(page, "Toast folder");
    const id = await createCategoryViaUi(page, "Toast category");
    const item = row(page, "category", id);
    const folder = row(page, "folder", folderId);
    const moves: string[] = [];
    page.on("request", (request) => {
      const path = new URL(request.url()).pathname;
      if (request.method() === "POST" && path.endsWith("/move")) moves.push(path);
    });
    await item.focus();
    await page.keyboard.press("Control+x");
    await folder.focus();
    await page.keyboard.press("Control+v");
    await expect(item).toHaveAttribute("data-parent-id", folderId);
    const toast = page.locator(".catalog-toast");
    const undo = toast.getByRole("button", { name: "Undo" });
    await expect(toast).toContainText("Moved 'Toast category'");
    // The tree's live region announces the move; the toast is not a second one.
    await expect(page.locator("#catalog-live")).toContainText("Moved 'Toast category'");
    expect(await toast.getAttribute("role")).toBeNull();
    expect(await toast.getAttribute("aria-live")).toBeNull();
    await expect(undo).toHaveAttribute("aria-keyshortcuts", "Control+Z");
    // Undo outlives the old five-second window and waits while focused.
    await page.clock.fastForward(6000);
    await expect(toast).toBeVisible();
    await undo.focus();
    await page.clock.fastForward(30000);
    await expect(toast).toBeVisible();
    // Ctrl+Z on the focused tree undoes the move the toast offers, once.
    await folder.focus();
    await page.keyboard.press("Control+z");
    await expect(item).toHaveAttribute("data-parent-id", "0");
    await expect(toast.getByRole("button", { name: "Undo" })).toHaveCount(0);
    await page.keyboard.press("Control+z");
    expect(moves).toEqual([`/categories/${id}/move`, `/categories/${id}/move`]);
    await page.reload();
    await expect(item).toHaveAttribute("data-parent-id", "0");
    // On a phone the toast clears the bottom tab bar.
    await page.setViewportSize({ width: 390, height: 844 });
    await item.focus();
    await page.keyboard.press("Control+x");
    await folder.focus();
    await page.keyboard.press("Control+v");
    await expect(toast).toBeVisible();
    const [toastBox, tabbarBox] = await Promise.all([
      toast.boundingBox(),
      page.locator(".tabbar").boundingBox(),
    ]);
    expect(toastBox && tabbarBox && toastBox.y + toastBox.height).toBeLessThanOrEqual(tabbarBox!.y);
    expect(toastBox!.x).toBeGreaterThanOrEqual(0);
    expect(toastBox!.x + toastBox!.width).toBeLessThanOrEqual(390);
    await folder.focus();
    await page.clock.fastForward(10001);
    await expect(toast).toBeHidden();
  });

  test("plain category name click still opens its workflow", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Name click category");
    await row(page, "category", id)
      .locator(":scope > .catalog-tree-row .catalog-node-name")
      .click();
    await expect(page).toHaveURL(base() + `/categories/${id}/workflow`);
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("drag into folder, undo and cancel drag", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const folder = await createFolderViaUi(page, "Move destination");
    const id = await createCategoryViaUi(page, "Move category");
    const source = row(page, "category", id);
    const dest = row(page, "folder", folder);
    await source.locator(":scope > .catalog-tree-row").hover();
    await source
      .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
      .dragTo(dest.locator(":scope > .catalog-tree-row"));
    await expect(source).toHaveAttribute("data-parent-id", folder);
    await expect(page.locator(".catalog-toast")).toContainText("Moved 'Move category'");
    await page.locator(".catalog-toast").getByRole("button", { name: "Undo" }).click();
    await expect(source).toHaveAttribute("data-parent-id", "0");
    await source.locator(":scope > .catalog-tree-row").hover();
    await source
      .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
      .dragTo(dest.locator(":scope > .catalog-tree-row"));
    await expect(source).toHaveAttribute("data-parent-id", folder);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", folder);
    await source.locator(":scope > .catalog-tree-row").hover();
    const grip = await source
      .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
      .boundingBox();
    if (!grip) throw new Error("missing grip");
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    await page.mouse.move(grip.x + grip.width / 2 + 10, grip.y + grip.height / 2, { steps: 4 });
    await expect(page.locator(".catalog-drop-top")).toHaveCount(0);
    await expect(page.locator(".catalog-drag-ghost")).toBeVisible();
    await page.keyboard.press("Escape");
    await page.mouse.up();
    await expect(page.locator(".catalog-drag-ghost")).toHaveCount(0);
    await expect(source).toHaveAttribute("data-parent-id", folder);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", folder);
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("folder cycle refused and hover expands collapsed folder", async ({ page }) => {
    const obs = collectObservability(page);
    const originalViewport = page.viewportSize();
    await page.setViewportSize({ width: 390, height: 900 });
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Cycle parent");
    const child = await createFolderViaUi(page, "Cycle child", parent);
    const other = await createFolderViaUi(page, "Expand target");
    await createFolderViaUi(page, "Expand child", other);
    await page.goto(base() + "/categories");
    const source = row(page, "folder", parent),
      dest = row(page, "folder", child),
      expanded = row(page, "folder", other);
    await page.evaluate(() => {
      document.documentElement.dataset.theme = "light";
    });
    await source.focus();
    const label = source.locator(":scope > .catalog-tree-row .catalog-node-name");
    await label.scrollIntoViewIfNeeded();
    const start = await label.evaluate((element) => {
      for (const box of element.getClientRects()) {
        const point = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
        if (element.contains(document.elementFromPoint(point.x, point.y))) return point;
      }
      throw new Error("Folder name has no hittable drag start");
    });
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await page.mouse.move(start.x + 10, start.y, { steps: 4 });
    await expect(page.locator(".catalog-drag-ghost")).toHaveText("Cycle parent");
    await expect(source).toHaveAttribute("aria-selected", "true");
    await expect(source.locator(":scope > .catalog-tree-row")).toHaveCSS(
      "background-color",
      "rgba(180, 35, 24, 0.1)",
    );
    const cycleLine = dest.locator(":scope > .catalog-tree-row");
    await cycleLine.evaluate((element) => element.scrollIntoView({ block: "center" }));
    const before = await rowGeometry(page);
    const point = await cycleLine.evaluate((line) => {
      const box = line.getBoundingClientRect();
      const y = box.top + box.height / 2;
      for (let x = box.left + 8; x < box.right - 8; x += 8) {
        const hit = document.elementFromPoint(x, y);
        if (
          hit?.closest(".catalog-tree-row") === line &&
          !hit.closest(
            "button,input,select,textarea,a,[contenteditable],.catalog-details,[role=menu]",
          )
        )
          return { x, y };
      }
      throw new Error("No non-interactive cycle row target in view");
    });
    await page.mouse.move(point.x, point.y, { steps: 10 });
    await expect(cycleLine).toHaveClass(/catalog-drop-invalid/);
    await expect(page.locator(".catalog-drop-reason")).toHaveText(
      "Can't move a folder into itself",
    );
    expect(await rowGeometry(page)).toEqual(before);
    await page.mouse.up();
    await expect(page.locator("#catalog-live")).toHaveText("Can't move a folder into itself");
    if (originalViewport) await page.setViewportSize(originalViewport);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", "0");
    await expanded.locator(":scope > .catalog-tree-row [data-catalog-disclosure]").click();
    await expect(expanded).toHaveAttribute("aria-expanded", "false");
    await source.locator(":scope > .catalog-tree-row").hover();
    const grip = await source
      .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
      .boundingBox();
    const box = await expanded.locator(":scope > .catalog-tree-row").boundingBox();
    if (!grip || !box) throw new Error("missing drag target");
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 10 });
    await expect(expanded).toHaveAttribute("aria-expanded", "true", { timeout: 3000 });
    await page.mouse.up();
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("drag refuses duplicate folder names without moving", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const a = await createFolderViaUi(page, "Keyboard A"),
      b = await createFolderViaUi(page, "Keyboard B");
    await createFolderViaUi(page, "Duplicate child", a);
    const duplicate = await createFolderViaUi(page, "Other child", b);
    await page.goto(base() + "/categories");
    const moved = row(page, "folder", duplicate);
    await renameFolder(moved);
    await moved.locator('input[name="name"]').fill("Duplicate child");
    await moved.locator('input[name="name"]').press("Enter");
    await expect(moved).toHaveAttribute("data-name", "Duplicate child");
    await moved.locator(":scope > .catalog-tree-row").hover();
    const refusal = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/folders/${duplicate}/move` &&
        r.request().method() === "POST",
    );
    await moved
      .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
      .dragTo(row(page, "folder", a).locator(":scope > .catalog-tree-row"));
    expect((await refusal).status()).toBe(409);
    await expect(page.locator(".catalog-toast")).toContainText(
      "A folder with that name already exists here.",
    );
    await page.reload();
    await expect(moved).toHaveAttribute("data-parent-id", b);
    expect(obs.consoleErrors).toEqual([
      "Failed to load resource: the server responded with a status of 409 (Conflict)",
    ]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });
});

test.describe("explorer drop geometry and touch", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("blank root target is reachable and highlighted at mobile and desktop in both themes", async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Geometry parent");
    const id = await createFolderViaUi(page, "Geometry source", parent);
    const source = row(page, "folder", id);
    const tree = page.locator(".catalog-tree");
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 844 });
      if (width === 390) {
        // The sidebar animates off-canvas after crossing the mobile breakpoint.
        await expect
          .poll(() => page.locator("#sidebar").evaluate((el) => el.getBoundingClientRect().right))
          .toBeLessThanOrEqual(0);
      }
      for (const theme of ["light", "dark"]) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        await source.locator(":scope > .catalog-tree-row").scrollIntoViewIfNeeded();
        await tree.evaluate((el) => {
          const bottom = el.getBoundingClientRect().bottom;
          if (bottom > innerHeight - 10) window.scrollBy(0, bottom - innerHeight + 10);
        });
        const grip = await source
          .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
          .boundingBox();
        const box = await tree.boundingBox();
        if (!grip || !box) throw new Error("missing drag geometry");
        // The fixed mobile tab bar covers the lowest part of the tree; use
        // the exposed blank tree space above it, never the navigation itself.
        const tabbar = width === 390 ? await page.locator(".tabbar").boundingBox() : null;
        if (width === 390 && !tabbar) throw new Error("missing mobile tab bar");
        const point = {
          x: box.x + box.width / 2,
          y: Math.min(box.y + box.height - 40, tabbar ? tabbar.y - 8 : 844),
        };
        expect(point.y).toBeLessThan(844);
        expect(
          await page.evaluate(
            ({ x, y }) => document.elementFromPoint(x, y)?.matches('.catalog-tree, [role="tree"]'),
            point,
          ),
        ).toBe(true);
        await source.locator(":scope > .catalog-tree-row").hover();
        await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
        await page.mouse.down();
        await page.mouse.move(point.x, point.y, { steps: 10 });
        await expect(tree).toHaveClass(/catalog-drop-valid/);
        const styles = await tree.evaluate((el) => ({
          shadow: getComputedStyle(el).boxShadow,
          accent: getComputedStyle(el).getPropertyValue("--accent").trim(),
        }));
        expect(styles.shadow).not.toBe("none");
        expect(styles.accent).not.toBe("");
        await page.screenshot({ path: testInfo.outputPath(`root-drop-${width}-${theme}.png`) });
        await page.mouse.up();
        await expect(source).toHaveAttribute("data-parent-id", "0");
        await page.locator(".catalog-toast").getByRole("button", { name: "Undo" }).click();
        await expect(source).toHaveAttribute("data-parent-id", parent);
        // The undo feedback is fixed over the mobile tree's bottom edge.
        await page.locator(".catalog-toast").getByRole("button", { name: "Dismiss" }).click();
      }
    }
  });

  test("touch swipe on a folder name scrolls; synthetic touch on its grip moves it", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 450 });
    await loginAsSeeded(page);
    const sourceId = await createFolderViaUi(page, "Touch source");
    const destinationId = await createFolderViaUi(page, "Touch destination");
    const source = row(page, "folder", sourceId);
    const destination = row(page, "folder", destinationId);
    await source.locator(":scope > .catalog-tree-row .catalog-node-name").scrollIntoViewIfNeeded();
    const label = await source
      .locator(":scope > .catalog-tree-row .catalog-node-name")
      .boundingBox();
    if (!label) throw new Error("missing touch name");
    const cdp = await page.context().newCDPSession(page);
    await cdp.send("Emulation.setTouchEmulationEnabled", { enabled: true });
    const x = label.x + label.width / 2;
    const y = label.y + label.height / 2;
    const before = await page.evaluate(() => scrollY);
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
    for (let step = 1; step <= 5; step++) {
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchMove",
        touchPoints: [{ x, y: y - step * 25 }],
      });
    }
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await expect.poll(() => page.evaluate(() => scrollY)).toBeGreaterThan(before);
    await expect(page.locator(".catalog-drag-ghost")).toHaveCount(0);
    await expect(source).toHaveAttribute("data-parent-id", "0");
    await page.setViewportSize({ width: 390, height: 1200 });
    await page.evaluate(() => window.scrollTo(0, 0));
    const grip = source.locator(":scope > .catalog-tree-row > .catalog-drag-grip");
    const target = destination.locator(":scope > .catalog-tree-row");
    const start = await grip.boundingBox();
    const end = await target.boundingBox();
    if (!start || !end) throw new Error("missing synthetic touch geometry");
    await grip.evaluate(
      (el, p) =>
        el.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            pointerId: 17,
            pointerType: "touch",
            button: 0,
            clientX: p.x,
            clientY: p.y,
          }),
        ),
      { x: start.x + start.width / 2, y: start.y + start.height / 2 },
    );
    await page.evaluate(
      (p) =>
        document.dispatchEvent(
          new PointerEvent("pointermove", {
            bubbles: true,
            cancelable: true,
            pointerId: 17,
            pointerType: "touch",
            clientX: p.x,
            clientY: p.y,
          }),
        ),
      { x: end.x + end.width / 2, y: end.y + end.height / 2 },
    );
    await expect(target).toHaveClass(/catalog-drop-valid/);
    await page.evaluate(
      (p) =>
        document.dispatchEvent(
          new PointerEvent("pointerup", {
            bubbles: true,
            pointerId: 17,
            pointerType: "touch",
            clientX: p.x,
            clientY: p.y,
          }),
        ),
      { x: end.x + end.width / 2, y: end.y + end.height / 2 },
    );
    await expect(source).toHaveAttribute("data-parent-id", destinationId);
    await page.reload();
    await expect(source).toHaveAttribute("data-parent-id", destinationId);
    await cdp.detach();
  });
});

test.describe("inline catalog details", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("dirty folder inspector refreshes delete eligibility after first child", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Eligibility parent");
    const folder = row(page, "folder", id);
    await folder.focus();
    const details = page.locator(".catalog-details");
    const description = details.getByLabel("Description", { exact: true });
    await description.fill("Unsaved folder draft");
    const original = await details.elementHandle();
    await expect(details.getByRole("button", { name: "Delete folder" })).toBeEnabled();
    await folder.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    const create = page.locator("[data-catalog-create-row]");
    await expect(create).toHaveAttribute("data-folder-id", id);
    await create.getByRole("button", { name: "Folder", exact: true }).click();
    await create.locator("input").fill("Eligibility child");
    await create.locator("input").press("Enter");
    await expect(
      page.locator('[data-level="folder"][data-name="Eligibility child"]'),
    ).toHaveAttribute("data-parent-id", id);
    await expect(description).toHaveValue("Unsaved folder draft");
    expect(
      await original?.evaluate((element) => element === document.querySelector(".catalog-details")),
    ).toBe(true);
    await expect(details.getByRole("button", { name: "Delete folder" })).toBeDisabled();
    await expect(
      details.getByText(
        "This folder isn't empty. Move or delete its folders and categories first.",
        { exact: true },
      ),
    ).toBeVisible();
    await details.getByRole("button", { name: "Discard" }).click();
    await page.reload();
    await folder.focus();
    await expect(
      page.locator(".catalog-details").getByLabel("Description", { exact: true }),
    ).toHaveValue("");
  });

  test("filtered dirty inspector remains reachable on mobile", async ({ page }) => {
    const dialogs: string[] = [];
    page.on("dialog", async (dialog) => {
      dialogs.push(dialog.message());
      await dialog.dismiss();
    });
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Filtered owner category");
    await row(page, "category", id).focus();
    const details = page.locator(".catalog-details");
    const description = details.getByLabel("Description", { exact: true });
    await description.fill("Filtered owner draft");
    const original = await details.elementHandle();
    await page.locator("#category-search").fill("Matching root folder");
    await page.locator('[data-catalog-header-create="folder"]').click();
    const create = page.locator("[data-catalog-create-row]");
    await expect(create).toHaveAttribute("data-catalog-create-row", "folder");
    await create.locator("input").fill("Matching root folder");
    await create.locator("input").press("Enter");
    await expect(row(page, "category", id)).toHaveCount(0);
    await expect(details).toBeVisible();
    await expect(description).toHaveValue("Filtered owner draft");
    const rootButton = page.locator('[data-catalog-header-create="folder"]');
    await expect(create).toHaveCount(0);
    expect(dialogs).toEqual(["Discard unsaved changes to these properties?"]);
    await rootButton.click();
    const creationInput = create.locator('input[name="name"]');
    await creationInput.fill("Unsent creation draft");
    const creatorOriginal = await create.elementHandle();
    for (const height of [900, 600]) {
      await page.setViewportSize({ width: 390, height });
      await page.evaluate(() => window.scrollBy(0, 30));
      const baseline = await creatorTreeSnapshot(page);
      await expectButtonCreator(page, rootButton);
      await expectPopoverInView(details);
      const popup = await create.boundingBox();
      const inspector = await details.boundingBox();
      if (!popup || !inspector) throw new Error("Missing creator or inspector");
      expect(
        popup.x + popup.width <= inspector.x ||
          inspector.x + inspector.width <= popup.x ||
          popup.y + popup.height <= inspector.y ||
          inspector.y + inspector.height <= popup.y,
      ).toBe(true);
      await expect(creationInput).toHaveValue("Unsent creation draft");
      await expect(create).toHaveAttribute("data-catalog-create-row", "folder");
      await expect(create.locator(".catalog-inline-hint")).toBeVisible();
      await expect(description).toHaveValue("Filtered owner draft");
      expect(
        await original?.evaluate(
          (element) => element === document.querySelector(".catalog-details"),
        ),
      ).toBe(true);
      expect(
        await creatorOriginal?.evaluate(
          (element) => element === document.querySelector("[data-catalog-create-row]"),
        ),
      ).toBe(true);
      const scroll = await details.evaluate(async (element) => {
        element.scrollTop = element.scrollHeight;
        const requested = element.scrollTop;
        await new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        );
        return { requested, retained: element.scrollTop };
      });
      expect(scroll.requested).toBeGreaterThan(0);
      expect(scroll.retained).toBe(scroll.requested);
      for (const name of ["Save changes", "Cancel"]) {
        const action = details.getByRole("button", { name, exact: true });
        await expect(action).toBeVisible();
        await expect
          .poll(() =>
            action.evaluate((element) => {
              const box = element.getBoundingClientRect();
              return element.contains(
                document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2),
              );
            }),
          )
          .toBe(true);
      }
      expect(await creatorTreeSnapshot(page)).toEqual(baseline);
    }
    await creationInput.focus();
    await page.keyboard.press("Escape");
    await expect(create).toHaveCount(0);
    await expect(rootButton).toBeFocused();
    await expect(description).toHaveValue("Filtered owner draft");
    await expectPopoverInView(details);
    await details.getByRole("button", { name: "Cancel", exact: true }).click();
    await page.goto(base() + "/categories");
    await row(page, "category", id)
      .locator(":scope > .catalog-tree-row [data-catalog-pen]")
      .click();
    await expect(
      page.locator(".catalog-details").getByLabel("Description", { exact: true }),
    ).toHaveValue("");
  });

  test("properties menu opens the exact folder and category without navigation", async ({
    page,
  }) => {
    await loginAsSeeded(page);
    const folder = await createFolderViaUi(page, "Menu properties folder");
    const category = await createCategoryViaUi(page, "Menu properties category");
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      for (const [kind, id, name] of [
        ["folder", folder, "Menu properties folder"],
        ["category", category, "Menu properties category"],
      ]) {
        const target = row(page, kind, id);
        const line = target.locator(":scope > .catalog-tree-row");
        const url = page.url();
        let navigations = 0;
        const onRequest = (request: import("@playwright/test").Request) => {
          if (request.isNavigationRequest() && request.frame() === page.mainFrame()) navigations++;
        };
        page.on("request", onRequest);
        await line.locator("[data-category-menu]").click();
        await line.getByRole("menuitem", { name: "Properties", exact: true }).click();
        await expect(
          page.locator(".catalog-details").getByLabel("Name", { exact: true }),
        ).toHaveValue(name);
        await expect(target).toHaveAttribute("aria-selected", "true");
        expect(page.url()).toBe(url);
        expect(navigations).toBe(0);
        page.off("request", onRequest);
        if (width === 390) {
          await page.keyboard.press("Escape");
          await expect(page.locator(".catalog-details")).toHaveCount(0);
        }
      }
      if (width === 1440) {
        await page.setViewportSize({ width: 390, height: 900 });
        await expect(page.locator(".catalog-details")).toHaveClass(/catalog-details-popover/);
        await expect(
          page.locator(".catalog-details").getByLabel("Name", { exact: true }),
        ).toBeFocused();
        await page.keyboard.press("Escape");
        await expect(page.locator(".catalog-details")).toHaveCount(0);
      }
    }
    await expect(row(page, "category", category).locator(".catalog-node-name")).toHaveAttribute(
      "href",
      `/categories/${category}/workflow`,
    );
  });

  for (const language of ["en", "es"] as const) {
    test(`properties menu supports keyboard in ${language}`, async ({ page }) => {
      await loginAsSeeded(page);
      const folder = await createFolderViaUi(page, `Keyboard properties folder ${language}`);
      const category = await createCategoryViaUi(page, `Keyboard properties category ${language}`);
      await page.goto(base() + "/preferences");
      const preference = page.locator('select[name="language"]');
      const previous = await preference.inputValue();
      try {
        await preference.selectOption(language);
        await page.locator('form[action="/preferences/language"] button').click();
        await expect(page.locator("html")).toHaveAttribute("lang", language);
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: 900 });
          await page.goto(base() + "/categories");
          for (const [kind, id, name] of [
            ["folder", folder, `Keyboard properties folder ${language}`],
            ["category", category, `Keyboard properties category ${language}`],
          ]) {
            const target = row(page, kind, id);
            const line = target.locator(":scope > .catalog-tree-row");
            const trigger = line.locator("[data-category-menu]");
            await trigger.focus();
            await trigger.press("ArrowDown");
            const item = line.getByRole("menuitem", {
              name: language === "es" ? "Propiedades" : "Properties",
              exact: true,
            });
            await expect(item).toBeVisible();
            // Navigate through the real keyboard sequence, without assuming menu item order.
            const count = await line.getByRole("menuitem").count();
            for (
              let i = 0;
              i <= count && !(await item.evaluate((element) => element === document.activeElement));
              i++
            ) {
              await page.keyboard.press("ArrowDown");
            }
            await expect(item).toBeFocused();
            const url = page.url();
            await page.keyboard.press("Enter");
            const label = language === "es" ? "Nombre" : "Name";
            await expect(
              page.locator(".catalog-details").getByLabel(label, { exact: true }),
            ).toHaveValue(name);
            await expect(target).toHaveAttribute("aria-selected", "true");
            expect(page.url()).toBe(url);
            if (width === 390) await page.keyboard.press("Escape");
          }
        }
      } finally {
        await page.goto(base() + "/preferences");
        await page.locator('select[name="language"]').selectOption(previous);
        await page.locator('form[action="/preferences/language"] button').click();
      }
    });
  }

  test("inspector protects a draft before changing selection", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const first = await createCategoryViaUi(page, "Guarded inspector first");
    const second = await createCategoryViaUi(page, "Guarded inspector second");
    await row(page, "category", first).focus();
    const inspector = page.locator("[data-catalog-inspector]");
    const description = inspector.getByLabel("Description", { exact: true });
    await expect(inspector.getByLabel("Name", { exact: true })).toHaveValue(
      "Guarded inspector first",
    );
    await description.fill("Keep my unsaved draft");
    const dialogs: string[] = [];
    let accept = false;
    const onDialog = async (dialog: import("@playwright/test").Dialog) => {
      dialogs.push(dialog.message());
      if (accept) await dialog.accept();
      else await dialog.dismiss();
    };
    const writes: string[] = [];
    page.on("dialog", onDialog);
    page.on("request", (request) => {
      if (request.method() === "POST") writes.push(new URL(request.url()).pathname);
    });
    await row(page, "category", second).locator(":scope > .catalog-tree-row").click();
    expect(dialogs).toEqual(["Discard unsaved changes to these properties?"]);
    await expect(description).toHaveValue("Keep my unsaved draft");
    await expect(description).toBeFocused();
    await expect(row(page, "category", first)).toHaveAttribute("aria-selected", "true");
    expect(writes).toEqual([]);
    accept = true;
    await row(page, "category", second).locator(":scope > .catalog-tree-row").click();
    expect(dialogs).toEqual([
      "Discard unsaved changes to these properties?",
      "Discard unsaved changes to these properties?",
    ]);
    expect(writes).toEqual([]);
    page.off("dialog", onDialog);
    await expect(inspector.getByLabel("Name", { exact: true })).toHaveValue(
      "Guarded inspector second",
    );
    await page.reload();
    await row(page, "category", first).focus();
    await expect(description).toHaveValue("");
  });
  test("one inspector draft survives responsive transfer and an unrelated tree write", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Responsive inspector draft");
    await row(page, "category", id).focus();
    const details = page.locator(".catalog-details");
    await expect(details.getByLabel("Name", { exact: true })).toHaveValue(
      "Responsive inspector draft",
    );
    await details.getByLabel("Description", { exact: true }).fill("Draft across containers");
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(details).toHaveClass(/catalog-details-popover/);
    await expect(details.getByLabel("Description", { exact: true })).toHaveValue(
      "Draft across containers",
    );
    await expect(details).toHaveCount(1);
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(page.locator("[data-catalog-inspector] .catalog-details")).toHaveCount(1);
    await page.locator('[data-catalog-header-create="folder"]').click();
    const create = page.locator("[data-catalog-create-row]");
    await expect(create).toHaveAttribute("data-catalog-create-row", "folder");
    await create.locator("input").fill("Unrelated inspector folder");
    await create.locator("input").press("Enter");
    await expect(
      page.locator('[data-level="folder"][data-name="Unrelated inspector folder"]'),
    ).toHaveCount(1);
    await expect(details.getByLabel("Description", { exact: true })).toHaveValue(
      "Draft across containers",
    );
    await details.getByRole("button", { name: "Discard", exact: true }).click();
    await expect(details.getByLabel("Description", { exact: true })).toHaveValue("");
  });
  test("a late details response cannot replace the selected inspector", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const first = await createCategoryViaUi(page, "Slow inspector category");
    const second = await createCategoryViaUi(page, "Fast inspector category");
    let release!: () => void;
    const delayed = new Promise<void>((resolve) => {
      release = resolve;
    });
    let entered!: () => void;
    const requested = new Promise<void>((resolve) => {
      entered = resolve;
    });
    await page.route(`**/categories/${first}/details`, async (route) => {
      entered();
      await delayed;
      await route.continue();
    });
    await row(page, "category", first).focus();
    await requested;
    await row(page, "category", second).focus();
    const name = page.locator("[data-catalog-inspector]").getByLabel("Name", { exact: true });
    await expect(name).toHaveValue("Fast inspector category");
    const completed = page.waitForResponse((response) =>
      response.url().endsWith(`/categories/${first}/details`),
    );
    release();
    await completed;
    await expect(name).toHaveValue("Fast inspector category");
    await expect(row(page, "category", second)).toHaveAttribute("aria-selected", "true");
  });
  test("desktop selection opens an inspector with explicit save and discard", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Inspector editable");
    const target = row(page, "category", id);
    await target.focus();
    const inspector = page.getByRole("region", { name: /properties/i });
    await expect(inspector).toBeVisible();
    await expect(inspector.getByLabel("Name", { exact: true })).toHaveValue("Inspector editable");
    for (const [theme, fill] of [
      ["light", "rgba(49, 94, 255, 0.1)"],
      ["dark", "rgba(123, 150, 255, 0.16)"],
    ] as const) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      await inspector.getByLabel("Name", { exact: true }).focus();
      await expect(target).toHaveAttribute("aria-selected", "true");
      await expect(target.locator(":scope > .catalog-tree-row")).toHaveCSS(
        "background-color",
        fill,
      );
    }
    const save = inspector.getByRole("button", { name: "Save changes" });
    await expect(save).toBeDisabled();
    await inspector.getByLabel("Description", { exact: true }).fill("Draft description");
    await expect(save).toBeEnabled();
    await inspector.getByRole("button", { name: "Discard", exact: true }).click();
    await expect(inspector.getByLabel("Description", { exact: true })).toHaveValue("");
    await expect(save).toBeDisabled();
    await inspector.getByLabel("Description", { exact: true }).fill("Saved description");
    await save.click();
    await expect(inspector.getByLabel("Description", { exact: true })).toHaveValue(
      "Saved description",
    );
    await expect(target).toHaveAttribute("aria-selected", "true");
  });
  test("category details save without a desk and discard unsaved values", async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 900 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Details editable");
    const target = row(page, "category", id);
    await target.focus();
    const get = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/${id}/details` && r.request().method() === "GET",
    );
    await page.keyboard.press("Enter");
    expect((await get).status()).toBe(200);
    const panel = target.getByRole("region", { name: "Details editable" });
    await expect(panel).toBeVisible();
    await expect(panel).toHaveClass(/catalog-details-popover/);
    await expect(panel.getByLabel("Name", { exact: true })).toHaveValue("Details editable");
    expect(await panel.evaluate((el) => getComputedStyle(el).position)).toBe("fixed");
    // The popover never covers the row it edits, at any viewport height.
    for (const height of [900, 600, 420]) {
      await page.setViewportSize({ width: 1024, height });
      await target.scrollIntoViewIfNeeded();
      await expect
        .poll(
          () =>
            target.evaluate((el) => {
              const rowBox = el
                .querySelector(":scope > .catalog-tree-row")
                ?.getBoundingClientRect();
              const pop = el.querySelector(".catalog-details-popover")?.getBoundingClientRect();
              if (!rowBox || !pop) return "missing";
              return pop.top < rowBox.bottom - 1 && pop.bottom > rowBox.top + 1;
            }),
          { message: `popover covers its row at height ${height}` },
        )
        .toBe(false);
    }
    await page.setViewportSize({ width: 1024, height: 720 });
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      expect(await panel.evaluate((el) => getComputedStyle(el).backgroundColor)).not.toBe(
        "rgba(0, 0, 0, 0)",
      );
    }
    await page.setViewportSize({ width: 390, height: 700 });
    // The popover follows the resize event, so wait for it to settle.
    await expect
      .poll(async () => {
        const b = await panel.boundingBox();
        return b !== null && b.x >= 0 && b.x + b.width <= 390 && b.y + b.height <= 700;
      })
      .toBe(true);
    await page.setViewportSize({ width: 1024, height: 900 });
    await expect(panel.getByLabel("Name", { exact: true })).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(panel.getByLabel("Description", { exact: true })).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(panel.getByLabel("Keywords for suggestions")).toBeFocused();
    await target.focus();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator('[role="treeitem"]:focus')).toHaveCount(1);
    expect(await page.locator('[role="treeitem"]:focus').getAttribute("data-catalog-id")).not.toBe(
      id,
    );
    await panel.getByLabel("Description", { exact: true }).fill("Edited description");
    await panel.getByLabel("Keywords for suggestions").fill("printer, paper");
    // Workflow steps name the desk (issue #491, S1), so the panel has no desk.
    await expect(panel.locator("select")).toHaveCount(0);
    const save = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/${id}/edit` && r.request().method() === "POST",
    );
    await panel.getByRole("button", { name: "Save changes" }).click();
    expect((await save).status()).toBe(200);
    await expect(panel).toHaveCount(0);
    await expect(page.locator(".catalog-toast")).toContainText("Saved 'Details editable'");
    await page.reload();
    await target.locator(":scope > .catalog-tree-row [data-catalog-pen]").click();
    await expect(panel.getByLabel("Description", { exact: true })).toHaveValue(
      "Edited description",
    );
    await expect(panel.getByLabel("Keywords for suggestions")).toHaveValue("printer, paper");
    await panel.getByLabel("Description", { exact: true }).fill("Unsaved description");
    page.once("dialog", (dialog) => dialog.accept());
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(target).toBeFocused();
    await page.reload();
    await target.focus();
    await page.keyboard.press("Enter");
    await expect(panel.getByLabel("Description", { exact: true })).toHaveValue(
      "Edited description",
    );
    await panel.getByRole("button", { name: "Cancel" }).click();
    await expect(panel).toHaveCount(0);
    await expect(target).toBeFocused();
    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });

  test("folder details explain non-empty deletion then allow deletion when empty", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1024, height: 900 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Details parent");
    const child = await createFolderViaUi(page, "Details child", parent);
    await page.goto(base() + "/categories");
    const folder = row(page, "folder", parent);
    const open = async (id: string) => {
      const f = row(page, "folder", id);
      await f.locator(":scope > .catalog-tree-row [data-catalog-pen]").click();
    };
    await open(parent);
    const panel = folder.getByRole("region", { name: "Details parent" });
    await expect(panel).toHaveClass(/catalog-details-popover/);
    await expect(panel.getByRole("heading", { name: "Details parent" })).toBeVisible();
    expect(await panel.evaluate((el) => getComputedStyle(el).position)).toBe("fixed");
    for (const width of [1024, 390]) {
      await page.setViewportSize({ width, height: 600 });
      await expect
        .poll(async () =>
          folder.evaluate((el) => {
            const line = el.querySelector(":scope > .catalog-tree-row")!.getBoundingClientRect();
            const pop = el.querySelector(".catalog-details")!.getBoundingClientRect();
            return (
              pop.left >= 0 &&
              pop.right <= innerWidth &&
              (pop.top >= line.bottom || pop.bottom <= line.top)
            );
          }),
        )
        .toBe(true);
    }
    const description = "Descripción <strong>literal</strong>";
    const subline = folder.locator(
      ":scope > .catalog-tree-row > .catalog-node-main > .catalog-node-sub",
    );
    await panel.getByLabel("Description", { exact: true }).fill(description);
    await panel.getByRole("button", { name: "Save changes" }).click();
    await expect(panel).toHaveCount(0);
    await expect(subline).toBeVisible();
    await expect(subline).toHaveText(description);
    await expect(subline.locator("strong")).toHaveCount(0);
    await page.reload();
    await expect(subline).toBeVisible();
    await expect(subline).toHaveText(description);
    await expect(subline.locator("strong")).toHaveCount(0);
    await folder.locator(":scope > .catalog-tree-row [data-catalog-pen]").click();
    await expect(panel.getByLabel("Description", { exact: true })).toHaveValue(description);
    await expect(panel.getByRole("button", { name: "Delete folder" })).toBeDisabled();
    await expect(panel).toContainText("This folder isn't empty");
    await panel.getByLabel("Description", { exact: true }).fill("");
    await panel.getByRole("button", { name: "Save changes" }).click();
    await expect(panel).toHaveCount(0);
    await expect(subline).toHaveCount(0);
    await expect(folder.locator(":scope > .catalog-tree-row .catalog-node-name")).toHaveText(
      "Details parent",
    );
    await open(child);
    await expect(panel).toHaveCount(0);
    const childPanel = row(page, "folder", child).getByRole("region", { name: "Details child" });
    await childPanel.getByRole("button", { name: "Delete folder" }).click();
    await page.locator("[data-category-delete-confirm]").click();
    await expect(row(page, "folder", child)).toHaveCount(0);
    await open(parent);
    await expect(panel.getByRole("button", { name: "Delete folder" })).toBeEnabled();
    await panel.getByRole("button", { name: "Delete folder" }).click();
    await page.locator("[data-category-delete-confirm]").click();
    await expect(folder).toHaveCount(0);
    await expect(page.locator(".catalog-toast")).toContainText("Deleted 'Details parent'");
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("category delete confirmation can cancel or accept", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createCategoryViaUi(page, "Details removable");
    const target = row(page, "category", id);
    await target.focus();
    await page.keyboard.press("Enter");
    const panel = page.getByRole("region", { name: "Details removable", exact: true });
    await panel.getByRole("button", { name: "Delete category" }).click();
    await page.locator("[data-category-delete-cancel]").click();
    await expect(target).toHaveCount(1);
    await panel.getByRole("button", { name: "Delete category" }).click();
    await page.locator("[data-category-delete-confirm]").click();
    await expect(target).toHaveCount(0);
    await expect(page.locator(".catalog-toast")).toContainText("Deleted 'Details removable'");
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });
});

test.describe("inline catalog rename", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("folder double-click rename sorts and restores focus, Escape cancels", async ({ page }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const first = await createFolderViaUi(page, "Rename Alpha");
    const second = await createFolderViaUi(page, "Rename Middle");
    const target = row(page, "folder", first);
    await target.focus();
    const height = await target
      .locator(":scope > .catalog-tree-row")
      .evaluate((el) => el.getBoundingClientRect().height);
    await renameFolder(target);
    const input = target.locator('input[name="name"]');
    await expect(input).toHaveValue("Rename Alpha");
    await expect(input).toBeFocused();
    expect(await input.evaluate((el) => (el as HTMLInputElement).selectionStart)).toBe(0);
    expect(await input.evaluate((el) => (el as HTMLInputElement).selectionEnd)).toBe(
      "Rename Alpha".length,
    );
    expect(
      await target
        .locator(":scope > .catalog-tree-row")
        .evaluate((el) => el.getBoundingClientRect().height),
    ).toBe(height);
    await input.fill("Rename Zulu");
    const response = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/folders/${first}/rename` &&
        r.request().method() === "POST",
    );
    await input.press("Enter");
    expect((await response).status()).toBe(200);
    await expect(target).toHaveAttribute("data-name", "Rename Zulu");
    await expect(target).toBeFocused();
    await expect(page.locator("#catalog-live")).toHaveText("Renamed to 'Rename Zulu'");
    const order = async () =>
      page
        .locator('[role="tree"] > [role="treeitem"][data-level="folder"]')
        .evaluateAll((els) => els.map((el) => el.getAttribute("data-catalog-id")));
    expect((await order()).indexOf(second)).toBeLessThan((await order()).indexOf(first));
    await page.reload();
    expect((await order()).indexOf(second)).toBeLessThan((await order()).indexOf(first));
    await renameFolder(target);
    await target.locator('input[name="name"]').fill("Cancelled");
    await page.keyboard.press("Escape");
    await expect(target).toBeFocused();
    await expect(target).toHaveAttribute("data-name", "Rename Zulu");
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("duplicate and blank folder names are refused without changing stored name", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await page.setViewportSize({ width: 390, height: 400 });
    await loginAsSeeded(page);
    await createFolderViaUi(page, "Refusal Existing");
    const id = await createFolderViaUi(page, "Refusal Original");
    const target = row(page, "folder", id);
    await renameFolder(target);
    const input = target.locator('input[name="name"]');
    for (const [value, status, reason] of [
      ["Refusal Existing", 409, "A folder with that name already exists here."],
      ["", 422, "Enter a folder name."],
    ] as const) {
      await input.fill(value);
      await input.evaluate((element) => {
        window.scrollBy(0, element.getBoundingClientRect().bottom - innerHeight + 12);
      });
      const before = await rowGeometry(page);
      const scroll = await page.evaluate(() => scrollY);
      const response = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === `/categories/folders/${id}/rename` &&
          r.request().method() === "POST",
      );
      await input.press("Enter");
      expect((await response).status()).toBe(status);
      await expect(input).toHaveAttribute("aria-invalid", "true");
      const error = target.locator(".catalog-inline-error");
      await expect(error).toHaveText(reason);
      expect(await rowGeometry(page)).toEqual(before);
      expect(await page.evaluate(() => scrollY)).toBe(scroll);
      await expect(error).toHaveAttribute("role", "alert");
      await expectPopoverInView(error);
      await expect(error).not.toHaveCSS("height", "0px");
      await expect(input).toHaveValue(value);
      await expect(input).toBeFocused();
    }
    await page.reload();
    await expect(target).toHaveAttribute("data-name", "Refusal Original");
    expect(obs.consoleErrors).toEqual([
      "Failed to load resource: the server responded with a status of 409 (Conflict)",
      "Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)",
    ]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });

  test("folder double-click and category properties allow editing without navigation", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const id = await createFolderViaUi(page, "Menu Folder");
    const folder = row(page, "folder", id);
    await folder.locator(":scope > .catalog-tree-row .catalog-node-name").dblclick();
    await expect(folder.locator('input[name="name"]')).toHaveValue("Menu Folder");
    await page.keyboard.press("Escape");
    const category = page.locator('[role="treeitem"][data-level="category"][data-name="General"]');
    await expect(category).toHaveCount(1);
    await category.focus();
    await page.keyboard.press("Enter");
    const name = page.locator(".catalog-details").getByLabel("Name", { exact: true });
    await expect(name).toHaveValue("General");
    await expect(name).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page).toHaveURL(/\/categories$/);
    expect(obs).toEqual({
      consoleErrors: [],
      pageErrors: [],
      failedRequests: [],
      failedResponses: [],
    });
  });

  test("leaving the rename field saves a changed name, closes an unchanged one and keeps a refusal open", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await createFolderViaUi(page, "Blur Taken");
    const id = await createFolderViaUi(page, "Blur Original");
    const folder = row(page, "folder", id);
    const renames: string[] = [];
    page.on("request", (r) => {
      if (r.method() === "POST" && new URL(r.url()).pathname.endsWith("/rename")) {
        renames.push(new URL(r.url()).pathname);
      }
    });
    const outside = page.getByRole("heading", { level: 1 });

    // Unchanged: leaving closes the field and sends nothing.
    await renameFolder(folder);
    await expect(folder.locator(".catalog-rename-input")).toHaveCount(1);
    await outside.click();
    await expect(page.locator(".catalog-rename-input")).toHaveCount(0);
    expect(renames).toEqual([]);

    // Changed: leaving saves it, like a file manager.
    await renameFolder(folder);
    await folder.locator(".catalog-rename-input").fill("Blur Saved");
    const saved = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/folders/${id}/rename` &&
        r.request().method() === "POST",
    );
    await outside.click();
    expect((await saved).status()).toBe(200);
    await expect(page.locator(".catalog-rename-input")).toHaveCount(0);
    await expect(folder).toHaveAttribute("data-name", "Blur Saved");

    // Refused: the field stays open with the reason, nothing is stored.
    await renameFolder(folder);
    const input = folder.locator(".catalog-rename-input");
    await input.fill("Blur Taken");
    const refused = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/folders/${id}/rename` &&
        r.request().method() === "POST",
    );
    await outside.click();
    expect((await refused).status()).toBe(409);
    await expect(input).toHaveAttribute("aria-invalid", "true");
    await expect(folder.locator(".catalog-inline-error")).toHaveText(
      "A folder with that name already exists here.",
    );
    await expect(input).not.toBeFocused();
    await input.press("Escape");
    await expect(page.locator(".catalog-rename-input")).toHaveCount(0);

    // Only one rename at a time: starting another closes the first.
    const other = page.locator('[role="treeitem"][data-level="folder"][data-name="Blur Taken"]');
    await renameFolder(folder);
    await renameFolder(other);
    await expect(page.locator(".catalog-rename-input")).toHaveCount(1);
    await expect(other.locator(".catalog-rename-input")).toHaveValue("Blur Taken");
    await page.keyboard.press("Escape");

    await page.reload();
    await expect(folder).toHaveAttribute("data-name", "Blur Saved");
    expect(renames).toEqual([
      `/categories/folders/${id}/rename`,
      `/categories/folders/${id}/rename`,
    ]);
    expect(obs.consoleErrors).toEqual([
      "Failed to load resource: the server responded with a status of 409 (Conflict)",
    ]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });
});

test.describe("inline catalog creation and keyboard", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  const folderNamed = (page: Page, name: string) =>
    page.locator('[role="treeitem"][data-level="folder"]').filter({
      has: page.locator(":scope > .catalog-tree-row .catalog-node-name", {
        hasText: new RegExp(`^${name}$`),
      }),
    });

  test("chained create from the tree menu and the + menu, with refusals kept in place", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    const suffix = Date.now().toString(36);

    // The tree menu closes after a folder is created; reopen it for the next folder.
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Shift+F10");
    await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
    const createRow = page.locator("[data-catalog-create-row]");
    await expect(createRow).toHaveCount(1);
    await expect(createRow.locator("input").first()).toBeFocused();
    await expect(createRow.locator(".catalog-inline-hint")).toHaveText(
      "Enter to create · Esc to cancel",
    );
    await expect(createRow.locator(".catalog-inline-hint")).toBeVisible();
    await createRow.locator("input").first().fill(`Chain A ${suffix}`);
    await page.keyboard.press("Enter");
    await expect(folderNamed(page, `Chain A ${suffix}`)).toHaveCount(1);
    await expect(createRow).toHaveCount(0);
    await expect(folderNamed(page, `Chain A ${suffix}`)).toHaveAttribute("aria-selected", "true");
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Shift+F10");
    await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
    await createRow.locator("input").first().fill(`Chain B ${suffix}`);
    await page.keyboard.press("Enter");
    await expect(folderNamed(page, `Chain B ${suffix}`)).toHaveCount(1);
    await expect(createRow).toHaveCount(0);
    await expect(folderNamed(page, `Chain B ${suffix}`)).toHaveAttribute("aria-selected", "true");
    await expect(page.locator("#catalog-live")).toHaveText(`Created folder 'Chain B ${suffix}'`);

    // A duplicate is refused in place: the typed value stays, with the reason.
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Shift+F10");
    await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
    await expect(createRow.locator(".catalog-inline-hint")).toBeVisible();
    await createRow.locator("input").first().fill(`chain a ${suffix}`);
    const treeRowBoxes = () =>
      page.locator('[role="treeitem"] > .catalog-tree-row').evaluateAll((rows) =>
        rows.map((row) => ({
          kind: row.parentElement?.getAttribute("data-level"),
          id: row.parentElement?.getAttribute("data-catalog-id"),
          top: row.getBoundingClientRect().top + scrollY,
          height: row.getBoundingClientRect().height,
        })),
      );
    const beforeRefusal = await treeRowBoxes();
    expect(beforeRefusal.length).toBeGreaterThan(0);
    const refusal = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/categories/folders" &&
        response.request().method() === "POST",
    );
    await page.keyboard.press("Enter");
    expect((await refusal).status()).toBe(409);
    await expect(createRow.locator("input").first()).toHaveAttribute("aria-invalid", "true");
    await expect(createRow.locator("input").first()).toHaveValue(`chain a ${suffix}`);
    await expect(createRow.locator("input").first()).toBeFocused();
    // The error suppresses the fresh creator's hint through CSS, not its hidden attribute.
    await expect(createRow.locator(".catalog-inline-hint")).not.toHaveAttribute("hidden");
    await expect(createRow.locator(".catalog-inline-hint")).toBeHidden();
    await expect(folderNamed(page, `Chain A ${suffix}`)).toHaveCount(1);
    await expect(createRow.locator(".catalog-inline-error")).toHaveText(
      "A folder with that name already exists here.",
    );
    expect(await treeRowBoxes()).toEqual(beforeRefusal);
    await expect(createRow.locator("input").first()).toHaveAttribute(
      "aria-describedby",
      "catalog-create-error",
    );
    await expect(page.locator("#catalog-live")).toHaveText(
      "A folder with that name already exists here.",
    );
    await page.keyboard.press("Escape");
    await expect(createRow).toHaveCount(0);
    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Shift+F10");
    await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
    await expect(createRow.locator(".catalog-inline-hint")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(createRow).toHaveCount(0);
    await page.reload();
    await expect(folderNamed(page, `Chain A ${suffix}`)).toHaveCount(1);
    await expect(folderNamed(page, `Chain B ${suffix}`)).toHaveCount(1);

    // "+" on Chain A → Category: the row sits inside the folder and needs only a name.
    const chainA = folderNamed(page, `Chain A ${suffix}`);
    await chainA.locator(":scope > .catalog-tree-row").hover();
    await chainA.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    const categoryRow = page.locator("[data-catalog-create-row]");
    const chainAId = await chainA.getAttribute("data-catalog-id");
    expect(chainAId).toBeTruthy();
    await expect(categoryRow).toHaveAttribute("data-folder-id", chainAId!);
    await expect(categoryRow).toHaveCount(1);
    await expect(categoryRow.locator("select")).toHaveCount(0);
    await expect(categoryRow.locator(".catalog-inline-hint")).toBeVisible();
    await categoryRow.locator("input").first().fill(`Chained category ${suffix}`);
    await page.keyboard.press("Enter");
    await expect(
      chainA.locator('[role="treeitem"][data-level="category"]', {
        hasText: `Chained category ${suffix}`,
      }),
    ).toHaveCount(1);
    await expect(categoryRow).toHaveCount(0);
    await expect(
      chainA.locator('[role="treeitem"][data-level="category"]', {
        hasText: `Chained category ${suffix}`,
      }),
    ).toHaveAttribute("aria-selected", "true");
    await chainA.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    await categoryRow.locator("input").first().fill(`Chained category next ${suffix}`);
    await page.keyboard.press("Enter");
    await expect(
      chainA.locator('[role="treeitem"][data-level="category"]', {
        hasText: `Chained category next ${suffix}`,
      }),
    ).toHaveCount(1);
    await expect(categoryRow).toHaveCount(0);
    await expect(
      chainA.locator('[role="treeitem"][data-level="category"]', {
        hasText: `Chained category next ${suffix}`,
      }),
    ).toHaveAttribute("aria-selected", "true");
    await chainA.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    await expect(categoryRow.locator(".catalog-inline-hint")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(categoryRow).toHaveCount(0);

    // Stored: both survive a reload in their places.
    await page.reload();
    await expect(folderNamed(page, `Chain B ${suffix}`)).toHaveCount(1);
    await expect(
      folderNamed(page, `Chain A ${suffix}`).locator('[role="treeitem"][data-level="category"]', {
        hasText: `Chained category ${suffix}`,
      }),
    ).toHaveCount(1);
    await expect(
      folderNamed(page, `Chain A ${suffix}`).locator('[role="treeitem"][data-level="category"]', {
        hasText: `Chained category next ${suffix}`,
      }),
    ).toHaveCount(1);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
  });

  for (const width of [1440, 390]) {
    test(`folder + menu stays visible near the viewport bottom at ${width}`, async ({ page }) => {
      test.setTimeout(90_000);
      const height = width === 390 ? 844 : 900;
      await page.setViewportSize({ width, height });
      const obs = collectObservability(page);
      await loginAsSeeded(page);
      let id = "";
      for (let index = 0; index < 20; index++) {
        const created = await createFolderViaUi(
          page,
          `Overlay ${width} ${String(index).padStart(2, "0")}`,
        );
        if (index === 15) id = created;
      }
      const folder = row(page, "folder", id);
      const trigger = folder.locator(":scope > .catalog-tree-row [data-catalog-add]");
      const line = folder.locator(":scope > .catalog-tree-row");
      await line.evaluate((element) => {
        window.scrollBy(0, element.getBoundingClientRect().bottom - (innerHeight - 70));
      });
      await line.hover();
      await trigger.scrollIntoViewIfNeeded();
      await trigger.click({ trial: true });
      const before = await creatorTreeSnapshot(page);
      await trigger.focus();
      await trigger.press("Space");
      const create = page.locator("[data-catalog-create-row]");
      await expect(create).toHaveAttribute("data-folder-id", id);
      await expectButtonCreator(page, trigger);
      expect(await creatorTreeSnapshot(page)).toEqual(before);
      await expect(create.locator("input")).toBeFocused();
      for (const theme of ["light", "dark"]) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        await expect(create.getByRole("button", { name: "Folder", exact: true })).toBeVisible();
        expect(await create.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
      }
      await page.setViewportSize({ width, height: 300 });
      await expect(create.locator("input")).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(create).toHaveCount(0);
      await expect(trigger).toBeFocused();

      // The taller row menu must scroll internally when neither side fits.
      await line.evaluate((element) => {
        window.scrollBy(0, element.getBoundingClientRect().top - 130);
      });
      const rowTrigger = line.locator("[data-category-menu]");
      await rowTrigger.click();
      const rowMenu = line.locator(".catalog-row-menu [role=menu]");
      await expectPopoverInView(rowMenu);
      await expect(rowMenu).toHaveCSS("overflow-y", "auto");
      expect(await rowMenu.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(
        true,
      );
      await rowTrigger.press("ArrowUp");
      await expect(rowMenu.getByRole("menuitem").last()).toBeFocused();
      await expectPopoverInView(rowMenu);
      await page.keyboard.press("Escape");
      await expect(rowMenu).toBeHidden();
      await expect(rowTrigger).toBeFocused();
      await expect(rowMenu).not.toHaveAttribute("style");
      expect(obs).toEqual({
        consoleErrors: [],
        pageErrors: [],
        failedRequests: [],
        failedResponses: [],
      });
    });
  }

  test("context menu creates inside folders and moves categories", async ({ page }) => {
    await loginAsSeeded(page);
    const parent = await createFolderViaUi(page, "Context parent");
    const destination = await createFolderViaUi(page, "Context destination");
    const id = await createCategoryViaUi(page, "Context category");
    const folder = row(page, "folder", parent);
    await folder.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await expect(page.getByRole("menuitem", { name: "Cut", exact: true })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Paste here", exact: true })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await folder.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    await page
      .locator("[data-catalog-create-row]")
      .getByRole("button", { name: "Folder", exact: true })
      .click();
    let create = page.locator('[data-catalog-create-row="folder"]');
    await expect(create).toHaveAttribute("data-folder-id", parent);
    await create.locator("input").fill("Context child");
    await create.locator("input").press("Enter");
    await expect(folder.locator('[data-name="Context child"]')).toHaveCount(1);
    await expect(create).toHaveCount(0);
    await expect(
      folder.locator('[role="treeitem"][data-level="folder"][data-name="Context child"]'),
    ).toHaveAttribute("aria-selected", "true");
    await folder.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    create = page.locator('[data-catalog-create-row="category"]');
    await expect(create).toHaveAttribute("data-folder-id", parent);
    await create.locator("input").fill("Context nested category");
    await create.locator("input").press("Enter");
    await expect(folder.locator('[data-name="Context nested category"]')).toHaveCount(1);
    await expect(create).toHaveCount(0);
    await expect(
      folder.locator(
        '[role="treeitem"][data-level="category"][data-name="Context nested category"]',
      ),
    ).toHaveAttribute("aria-selected", "true");
    // A folder whose children are already in the DOM explains why Delete is
    // unavailable immediately, without waiting for the details fetch.
    const parentDetailsUrl = await folder.getAttribute("data-details-url");
    if (!parentDetailsUrl) throw new Error(`Missing details URL for folder ${parent}`);
    const detailsRequests: string[] = [];
    page.on("request", (request) => {
      if (request.url().endsWith(parentDetailsUrl)) detailsRequests.push(request.url());
    });
    // Below the inspector breakpoint selecting a row opens no details panel, so
    // any details request comes from the menu itself.
    const viewport = page.viewportSize();
    await page.setViewportSize({ width: 1099, height: viewport?.height ?? 900 });
    await page.waitForLoadState("networkidle");
    detailsRequests.length = 0;
    await folder.locator(":scope > .catalog-tree-row").click({ button: "right" });
    const notEmpty = "This folder isn't empty. Move or delete its folders and categories first.";
    const folderDelete = page.locator(".catalog-context-menu .category-menu-danger");
    await expect(folderDelete).toHaveAttribute("aria-disabled", "true");
    await expect(folderDelete).toHaveAttribute("title", notEmpty);
    await expect(folderDelete).toHaveAttribute("aria-label", `Delete folder: ${notEmpty}`);
    expect(detailsRequests).toEqual([]);
    await folderDelete.click({ force: true });
    await expect(folderDelete).toBeVisible();
    await expect(folder).toHaveCount(1);
    expect(detailsRequests).toEqual([]);
    await page.keyboard.press("Escape");
    // An apparently empty folder still waits for the server to confirm.
    const emptyFolder = row(page, "folder", destination);
    const emptyDetailsUrl = await emptyFolder.getAttribute("data-details-url");
    if (!emptyDetailsUrl) throw new Error(`Missing details URL for folder ${destination}`);
    const emptyDetails = page.waitForResponse((response) =>
      response.url().endsWith(emptyDetailsUrl),
    );
    await emptyFolder.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await emptyDetails;
    await expect(folderDelete).not.toHaveAttribute("aria-disabled", "true");
    await page.keyboard.press("Escape");
    if (viewport) await page.setViewportSize(viewport);
    await expect(folder.locator(":scope > .catalog-tree-row > .catalog-drag-grip")).toHaveAttribute(
      "title",
      "Drag to move",
    );
    await folder.locator(":scope > .catalog-tree-row [data-category-menu]").click();
    await expect(folder.locator(":scope > .catalog-tree-row [data-catalog-cut]")).toBeVisible();
    await page.keyboard.press("Escape");
    const treeArea = page.locator(".catalog-tree");
    await treeArea.evaluate((el) => {
      (el as HTMLElement).style.paddingBottom = "40px";
    });
    const treeBox = await treeArea.boundingBox();
    if (!treeBox) throw new Error("missing catalog tree");
    await treeArea.click({ button: "right", position: { x: 10, y: treeBox.height - 20 } });
    await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
    create = page.locator('[data-catalog-create-row="folder"]');
    await create.locator("input").fill("Context top");
    await create.locator("input").press("Enter");
    await expect(page.locator('[data-name="Context top"]')).toHaveAttribute("data-parent-id", "0");
    await expect(create).toHaveCount(0);
    await expect(
      page.locator('[role="treeitem"][data-level="folder"][data-name="Context top"]'),
    ).toHaveAttribute("aria-selected", "true");
    const category = row(page, "category", id);
    await category.focus();
    await page.keyboard.press("Shift+F10");
    await expect(page.getByRole("menuitem", { name: "Cut", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await page.keyboard.press("Shift+F10");
    await page.keyboard.press("Escape");
    await expect(category).toBeFocused();
    await category.locator(":scope > .catalog-tree-row").click({ button: "right" });
    await expect(page.getByRole("menuitem", { name: "Cut", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await category.locator("[data-category-menu]").click();
    await expect(category.locator("[data-catalog-cut]")).toBeVisible();
    await page.keyboard.press("Escape");
    await category.locator(":scope > .catalog-tree-row").hover();
    await category
      .locator(":scope > .catalog-tree-row > .catalog-drag-grip")
      .dragTo(row(page, "folder", destination).locator(":scope > .catalog-tree-row"));
    await expect(category).toHaveAttribute("data-parent-id", destination);
    await expect(
      page.locator(".catalog-toast").getByRole("button", { name: "Undo" }),
    ).toBeVisible();
    await category.focus();
    await page.keyboard.press("Enter");
    await page
      .locator(".catalog-details")
      .getByLabel("Name", { exact: true })
      .click({ button: "right" });
    await expect(page.locator(".catalog-context-menu")).toHaveCount(0);
  });

  test("arrow keys walk the visible rows and expand or collapse folders", async ({ page }) => {
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36);
    const parent = await createFolderViaUi(page, `Keys ${suffix}`);
    await createFolderViaUi(page, `Keys child ${suffix}`, parent);
    await page.goto(base() + "/categories");
    const top = row(page, "folder", parent);
    await expect(page.locator('[role="tree"]')).toHaveCount(1);
    await top.focus();
    await expect(top).toHaveAttribute("tabindex", "0");
    await expect(top).toHaveAttribute("aria-expanded", "true");
    await page.keyboard.press("ArrowLeft");
    await expect(top).toHaveAttribute("aria-expanded", "false");
    await page.keyboard.press("ArrowRight");
    await expect(top).toHaveAttribute("aria-expanded", "true");
    await page.keyboard.press("ArrowRight");
    await expect(page.locator(":focus")).toHaveAttribute("data-name", `Keys child ${suffix}`);
    await page.keyboard.press("ArrowLeft");
    await expect(top).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator(":focus")).toHaveAttribute("data-name", `Keys child ${suffix}`);
    await expect(page.locator('[role="treeitem"][tabindex="0"]')).toHaveCount(1);
    await page.keyboard.press("Home");
    await expect(page.locator('[role="treeitem"]:visible').first()).toBeFocused();
    await page.keyboard.press("End");
    await expect(page.locator('[role="treeitem"]:visible').last()).toBeFocused();
  });
});

test.describe("inline catalog creation destination", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  const viewports = [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
  ];
  const headerButton = (page: Page, kind: "category" | "folder") =>
    page.getByRole("button", { name: `New ${kind}`, exact: true });
  // A real click on the row, the way a user selects it (the name of a folder, the
  // workflow badge of a category, which would otherwise navigate).
  async function selectRow(page: Page, target: import("@playwright/test").Locator) {
    const isCategory = (await target.getAttribute("data-level")) === "category";
    await (isCategory
      ? target.locator(":scope > .catalog-tree-row .catalog-badge")
      : target.locator(":scope > .catalog-tree-row .catalog-node-name")
    ).click();
    await expect(target).toHaveAttribute("aria-selected", "true");
  }
  async function submit(
    page: Page,
    kind: "category" | "folder",
    name: string,
  ): Promise<import("@playwright/test").Locator> {
    const create = page.locator("[data-catalog-create-row]");
    const input = create.locator('input[name="name"]');
    await input.fill(name);
    const response = page.waitForResponse(
      (result) =>
        new URL(result.url()).pathname ===
          (kind === "folder" ? "/categories/folders" : "/categories") &&
        result.request().method() === "POST",
    );
    await input.press("Enter");
    expect((await response).status(), `create ${kind} ${name} at ${page.url()}`).toBe(200);
    return create;
  }
  const destination = (page: Page) =>
    page.locator("[data-catalog-create-row] .catalog-create-destination > span");
  const clear = (page: Page) =>
    page.getByRole("button", { name: "Create at the top level instead", exact: true });

  for (const viewport of viewports) {
    const label = `${viewport.width}px`;

    test(`${label}: the top-level button is gone and the header creates remain`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      await page.goto(base() + "/categories");
      await expect(headerButton(page, "category")).toBeVisible();
      await expect(headerButton(page, "folder")).toBeVisible();
      await expect(page.getByRole("button", { name: /top level/i })).toHaveCount(0);
      await expect(page.locator("[data-catalog-root-create]")).toHaveCount(0);
    });

    test(`${label}: New category creates inside the selected folder and reveals it`, async ({
      page,
    }) => {
      const pageErrors: string[] = [];
      page.on("pageerror", (error) => pageErrors.push(error.message));
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `a${viewport.width}${Date.now().toString(36)}`;
      const parent = await createFolderViaUi(page, `Dest parent ${tag}`);
      await page.goto(base() + "/categories");
      const folder = row(page, "folder", parent);
      await selectRow(page, folder);
      await headerButton(page, "category").click();
      const create = page.locator("[data-catalog-create-row]");
      await expect(create).toHaveAttribute("data-folder-id", parent);
      await expect(create).toHaveAttribute("data-catalog-create-row", "category");
      await expect(destination(page)).toHaveText(`Create in: Dest parent ${tag}`);
      await expectButtonCreator(page, headerButton(page, "category"));
      await submit(page, "category", `Inside ${tag}`);
      const created = page.locator(
        `[role="treeitem"][data-level="category"][data-name="Inside ${tag}"]`,
      );
      await expect(created).toHaveAttribute("data-parent-id", parent);
      await expect(folder).toHaveAttribute("aria-expanded", "true");
      await expect(created).toBeVisible();
      await expect(create).toHaveCount(0);
      await expect(created).toHaveAttribute("aria-selected", "true");
      await page.reload();
      await expect(created).toHaveAttribute("data-parent-id", parent);
      expect(pageErrors).toEqual([]);
    });

    test(`${label}: New folder beside a selected category lands in that category's folder`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `b${viewport.width}${Date.now().toString(36)}`;
      const parent = await createFolderViaUi(page, `Sibling parent ${tag}`);
      await page.goto(base() + "/categories");
      const folder = row(page, "folder", parent);
      await folder.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
      await submit(page, "category", `Anchor ${tag}`);
      const anchor = page.locator(
        `[role="treeitem"][data-level="category"][data-name="Anchor ${tag}"]`,
      );
      await expect(anchor).toHaveAttribute("data-parent-id", parent);
      const creator = page.locator("[data-catalog-create-row]");
      await expect(creator).toHaveCount(0);
      await expect(anchor).toHaveAttribute("aria-selected", "true");
      await selectRow(page, anchor);
      await headerButton(page, "folder").click();
      await expect(page.locator("[data-catalog-create-row]")).toHaveAttribute(
        "data-folder-id",
        parent,
      );
      await expect(destination(page)).toHaveText(`Create in: Sibling parent ${tag}`);
      await submit(page, "folder", `Sibling ${tag}`);
      const sibling = page.locator(
        `[role="treeitem"][data-level="folder"][data-name="Sibling ${tag}"]`,
      );
      await expect(sibling).toHaveAttribute("data-parent-id", parent);
      await expect(sibling).toBeVisible();
      await page.keyboard.press("Escape");
      await page.reload();
      await expect(sibling).toHaveAttribute("data-parent-id", parent);
    });

    test(`${label}: the destination chip switches to the top level from the keyboard`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `c${viewport.width}${Date.now().toString(36)}`;
      const parent = await createFolderViaUi(page, `Switch dest ${tag}`);
      await page.goto(base() + "/categories");
      await selectRow(page, row(page, "folder", parent));
      await headerButton(page, "folder").click();
      const create = page.locator("[data-catalog-create-row]");
      await expect(destination(page)).toHaveText(`Create in: Switch dest ${tag}`);
      const input = create.locator('input[name="name"]');
      await expect(input).toBeFocused();
      await page.keyboard.press("Shift+Tab");
      await expect(clear(page)).toBeFocused();
      if (viewport.width < 1100) {
        expect((await clear(page).boundingBox())?.height).toBeGreaterThanOrEqual(44);
      }
      await page.keyboard.press("Enter");
      await expect(destination(page)).toHaveText("Create in: Top level");
      await expect(clear(page)).toHaveCount(0);
      await expect(create).toHaveAttribute("data-folder-id", "");
      await expect(input).toBeFocused();
      await submit(page, "folder", `Top by switch ${tag}`);
      await expect(
        page.locator(`[role="treeitem"][data-level="folder"][data-name="Top by switch ${tag}"]`),
      ).toHaveAttribute("data-parent-id", "0");
      await page.keyboard.press("Escape");
      await page.reload();
      await expect(
        page.locator(`[role="treeitem"][data-level="folder"][data-name="Top by switch ${tag}"]`),
      ).toHaveAttribute("data-parent-id", "0");
    });

    test(`${label}: reopened creation keeps the selected destination`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `e${viewport.width}${Date.now().toString(36)}`;
      const parent = await createFolderViaUi(page, `Chain dest ${tag}`);
      await page.goto(base() + "/categories");
      await selectRow(page, row(page, "folder", parent));
      await headerButton(page, "category").click();
      const create = page.locator("[data-catalog-create-row]");
      for (const name of [`Chain one ${tag}`, `Chain two ${tag}`]) {
        await expect(create).toHaveAttribute("data-folder-id", parent);
        await expect(destination(page)).toHaveText(`Create in: Chain dest ${tag}`);
        await submit(page, "category", name);
        const created = page.locator(
          `[role="treeitem"][data-level="category"][data-name="${name}"]`,
        );
        await expect(created).toHaveAttribute("data-parent-id", parent);
        await expect(create).toHaveCount(0);
        await expect(created).toHaveAttribute("aria-selected", "true");
        await headerButton(page, "category").click();
      }
      await clear(page).click();
      await expect(create).toHaveAttribute("data-folder-id", "");
      await expect(destination(page)).toHaveText("Create in: Top level");
      await expect(clear(page)).toHaveCount(0);
      await submit(page, "category", `Chain top ${tag}`);
      const top = page.locator(
        `[role="treeitem"][data-level="category"][data-name="Chain top ${tag}"]`,
      );
      await expect(top).toHaveAttribute("data-parent-id", "0");
      await expect(create).toHaveCount(0);
      await expect(top).toHaveAttribute("aria-selected", "true");
    });

    test(`${label}: header creators have no kind switch and create the button's kind`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `g${viewport.width}${Date.now().toString(36)}`;
      await page.goto(base() + "/categories");
      for (const kind of ["category", "folder"] as const) {
        await headerButton(page, kind).click();
        const create = page.locator("[data-catalog-create-row]");
        await expect(create).toHaveAttribute("data-catalog-create-row", kind);
        await expect(create.locator(".catalog-create-switch")).toHaveCount(0);
        await expect(create.getByRole("button", { name: "Folder", exact: true })).toHaveCount(0);
        await expect(create.getByRole("button", { name: "Category", exact: true })).toHaveCount(0);
        await submit(page, kind, `Fixed ${kind} ${tag}`);
        await expect(
          page.locator(`[role="treeitem"][data-level="${kind}"][data-name="Fixed ${kind} ${tag}"]`),
        ).toHaveAttribute("data-parent-id", "0");
        await expect(create).toHaveCount(0);
        await expect(
          page.locator(`[role="treeitem"][data-level="${kind}"][data-name="Fixed ${kind} ${tag}"]`),
        ).toHaveAttribute("aria-selected", "true");
      }
    });

    test(`${label}: a folder's plus creator keeps the kind switch and the draft`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `h${viewport.width}${Date.now().toString(36)}`;
      const parent = await createFolderViaUi(page, `Switch keeps ${tag}`);
      await page.goto(base() + "/categories");
      await row(page, "folder", parent)
        .locator(":scope > .catalog-tree-row [data-catalog-add]")
        .click();
      const create = page.locator("[data-catalog-create-row]");
      await expect(create.locator(".catalog-create-switch")).toHaveCount(1);
      await expect(create).toHaveAttribute("data-catalog-create-row", "category");
      const input = create.locator('input[name="name"]');
      await input.fill(`Kept draft ${tag}`);
      await create.getByRole("button", { name: "Folder", exact: true }).click();
      await expect(create).toHaveAttribute("data-catalog-create-row", "folder");
      await expect(input).toHaveValue(`Kept draft ${tag}`);
      await submit(page, "folder", `Kept draft ${tag}`);
      const created = page.locator(
        `[role="treeitem"][data-level="folder"][data-name="Kept draft ${tag}"]`,
      );
      await expect(created).toHaveAttribute("data-parent-id", parent);
      await expect(create).toHaveCount(0);
      await expect(created).toHaveAttribute("aria-selected", "true");
    });

    test(`${label}: Escape closes the creator from any inner control without a POST`, async ({
      page,
    }) => {
      const posts: string[] = [];
      page.on("request", (request) => {
        if (request.method() === "POST") posts.push(request.url());
      });
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `i${viewport.width}${Date.now().toString(36)}`;
      const parent = await createFolderViaUi(page, `Escape parent ${tag}`);
      await page.goto(base() + "/categories");
      posts.length = 0;
      const create = page.locator("[data-catalog-create-row]");

      await selectRow(page, row(page, "folder", parent));
      const header = headerButton(page, "category");
      await header.click();
      await expect(clear(page)).toBeVisible();
      await clear(page).focus();
      await expect(clear(page)).toBeFocused();
      await page.keyboard.press("Escape");
      await expect(create).toHaveCount(0);
      await expect(header).toBeFocused();

      const add = row(page, "folder", parent).locator(
        ":scope > .catalog-tree-row [data-catalog-add]",
      );
      await add.click();
      const switchButton = create.getByRole("button", { name: "Folder", exact: true });
      await switchButton.focus();
      await expect(switchButton).toBeFocused();
      await page.keyboard.press("Escape");
      await expect(create).toHaveCount(0);
      await expect(add).toBeFocused();

      // A real click on the popover's label, hint or empty padding takes focus
      // off the input; Escape must still close it (reported by the user).
      const clicks: Record<string, () => Promise<void>> = {
        label: () => create.locator(".catalog-create-destination > span").click(),
        hint: () => create.locator(".catalog-inline-hint").click(),
        padding: async () => {
          const box = await create.boundingBox();
          if (!box) throw new Error("Missing creator box");
          await page.mouse.click(box.x + box.width - 6, box.y + box.height - 6);
        },
      };
      for (const click of Object.values(clicks)) {
        await header.click();
        await expect(create).toBeVisible();
        await click();
        await expect(create.locator('input[name="name"]')).not.toBeFocused();
        await page.keyboard.press("Escape");
        await expect(create).toHaveCount(0);
        await expect(header).toBeFocused();
      }
      expect(posts).toEqual([]);
    });

    test(`${label}: a folder's own + still creates in that folder`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await loginAsSeeded(page);
      const tag = `f${viewport.width}${Date.now().toString(36)}`;
      const selected = await createFolderViaUi(page, `Plus selected ${tag}`);
      const target = await createFolderViaUi(page, `Plus target ${tag}`);
      await page.goto(base() + "/categories");
      await selectRow(page, row(page, "folder", selected));
      await row(page, "folder", target)
        .locator(":scope > .catalog-tree-row [data-catalog-add]")
        .click();
      const create = page.locator("[data-catalog-create-row]");
      await expect(create).toHaveAttribute("data-folder-id", target);
      await expect(create.locator(".catalog-create-destination")).toHaveCount(0);
      await submit(page, "category", `Plus child ${tag}`);
      await expect(
        page.locator(`[role="treeitem"][data-level="category"][data-name="Plus child ${tag}"]`),
      ).toHaveAttribute("data-parent-id", target);
      await expect(create).toHaveCount(0);
      await expect(
        page.locator(`[role="treeitem"][data-level="category"][data-name="Plus child ${tag}"]`),
      ).toHaveAttribute("aria-selected", "true");
    });
  }
});
