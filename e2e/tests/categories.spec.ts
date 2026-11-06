/**
 * Categories + Workflow Builder journeys.
 *
 * The published workflow version CAN be observed on the ticket detail page via the
 * step checklist (#workflow-pending) — requester-owned tickets read the same
 * checklist, with the current step's control withheld when the viewer cannot act.
 */

import {
  test,
  expect,
  type Locator,
  type Page,
  type Request,
  type Response,
  type Route,
} from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base, seededCredentials } from "./helpers/auth.js";
import {
  assertNoHorizontalOverflow,
  assertCanonicalScreen,
  collectObservability,
} from "./helpers/layout.js";
import { assertHtmxNoSwap, assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";
import {
  catalogCategoryRow,
  catalogNode,
  catalogNodeId,
  createCategoryInFolder,
  createCategoryViaUi,
  createDeskViaUi,
  createFolderViaUi,
  createTicketViaUi,
  selectCatalogDesk,
  toggleCatalogNode,
} from "./helpers/navigation.js";
import { applyAssignee, currentAssignee, pickerButton } from "./helpers/ticket.js";

/** The row-level menu trigger of one catalog row (table row or tree node). */
function rowMenu(node: Locator): Locator {
  return node.locator("[data-category-menu]").first();
}

/**
 * Steps left Activity (issue #438, S9): the published run's checklist is its own
 * card that reads before the Activity card, and it is no longer a timeline
 * entry. The workflow progression checks below it stay exactly as they were.
 */
async function expectStepsCardBeforeActivity(page: Page) {
  await expect(page.locator(".detail-steps #workflow-pending")).toHaveCount(1);
  await expect(page.locator("#timeline #workflow-pending")).toHaveCount(0);
  const stepsFirst = await page.evaluate(() => {
    const stepsCard = document.querySelector(".detail-steps");
    const activityCard = document.querySelector(".detail-activity");
    if (!stepsCard || !activityCard) return false;
    return Boolean(
      stepsCard.compareDocumentPosition(activityCard) & Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });
  expect(stepsFirst).toBe(true);
}

/**
 * The Steps card's measured accent highlight (issue #438, S4): the outline is
 * decoration a screenshot cannot be trusted for at ten percent ink, so the
 * settled computed border and ring are compared against the served `--accent`
 * token itself. `plain` asserts the opposite: an ordinary hairline card.
 */
async function stepsHighlight(page: Page): Promise<{ border: string; ring: string }> {
  return page.evaluate(() => {
    const card = document.querySelector(".detail-steps");
    if (!card) throw new Error("no Steps card at " + window.location.pathname);
    const style = getComputedStyle(card);
    const probe = document.createElement("div");
    probe.style.color = getComputedStyle(document.documentElement)
      .getPropertyValue("--accent")
      .trim();
    document.body.appendChild(probe);
    const accent = getComputedStyle(probe).color;
    probe.remove();
    return { border: style.borderTopColor, ring: `${accent}|${style.boxShadow}` };
  });
}

async function expectStepsHighlighted(page: Page) {
  const highlight = await stepsHighlight(page);
  const accent = highlight.ring.split("|")[0];
  expect(highlight.border, "the Steps card border must be the accent token").toBe(accent);
  expect(highlight.ring, "the Steps card must carry the accent ring").not.toBe(`${accent}|none`);
}

async function expectStepsPlain(page: Page) {
  const highlight = await stepsHighlight(page);
  const accent = highlight.ring.split("|")[0];
  expect(highlight.border, "a passive Steps card keeps the plain hairline").not.toBe(accent);
  expect(highlight.ring, "a passive Steps card carries no accent ring").toBe(`${accent}|none`);
}

/** The open menu panel of one tree node. */
function openRowMenu(node: Locator): Locator {
  return node.locator(".category-overflow-menu:not([hidden])");
}

async function assertDrawerHtmxSwap(
  page: Page,
  trigger: () => Promise<void>,
  options: { endpoint: (url: string) => boolean; expectedUrl: RegExp },
): Promise<Response> {
  const target = page.locator("#category-drawer-host");
  await expect(target).toHaveCount(1);
  const targetBefore = await target.innerHTML();
  const h1 = page.locator("h1").first();
  const h1Before = await h1.textContent();
  const navigations: Array<{ method: string; url: string }> = [];
  const navigationHandler = (request: Request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
      navigations.push({ method: request.method(), url: request.url() });
    }
  };

  try {
    page.on("request", navigationHandler);
    const responsePromise = page.waitForResponse(
      (response) =>
        response.request().headers()["hx-request"] === "true" &&
        response.request().method() === "GET" &&
        options.endpoint(response.url()),
    );
    await trigger();
    const response = await responsePromise;
    expect(response.status()).toBe(200);
    expect(response.headers()["hx-retarget"]).toBe("#category-drawer-host");
    await expect.poll(() => target.innerHTML()).not.toBe(targetBefore);
    await expect(page).toHaveURL(options.expectedUrl);
    expect(navigations).toEqual([]);
    expect(await h1.textContent()).toBe(h1Before);
    return response;
  } finally {
    page.removeListener("request", navigationHandler);
  }
}

/** Open the exact category's details, waiting for a fetch only when not cached. */
// The permanent inspector (>= 1100px) has no row pencil: selecting the row and
// pressing Enter is the public way to open and focus its properties.
async function openRowDetails(row: Locator): Promise<void> {
  await row.focus();
  await row.press("Enter");
}

async function openCategoryDetails(
  page: Page,
  row: Locator,
  expected: { name: string; description: string },
): Promise<Locator> {
  const id = await catalogNodeId(row, `category ${expected.name}`);
  const details = page.getByRole("region", { name: expected.name, exact: true });
  const responsePromise = (await details.count())
    ? null
    : page.waitForResponse(
        (response) =>
          response.request().method() === "GET" &&
          new URL(response.url()).pathname === `/categories/${id}/details`,
      );
  await openRowDetails(row);
  if (responsePromise)
    expect((await responsePromise).status(), `the details for ${expected.name}`).toBe(200);
  const drawer = details;
  await expect(drawer).toBeVisible();
  const name = drawer.getByLabel("Name", { exact: true });
  await expect(name).toBeFocused();
  await expect(name).toHaveValue(expected.name);
  await expect(drawer.getByLabel("Description", { exact: true })).toHaveValue(expected.description);
  return drawer;
}

test.describe("Categories", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  // ---------------------------------------------------------------------------
  // The catalog folder tree (issue #484). Folders nest without limit and hold
  // categories. A category's workflow steps decide which desk handles it, so a
  // row shows the workflow, not a desk.
  // ---------------------------------------------------------------------------

  test("the catalog opens in the tree with categories and their workflows", async ({ page }) => {
    await loginAsSeeded(page);
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      const obs = collectObservability(page);
      await page.goto(base() + "/categories");
      await expect(page.locator("h1")).toHaveText("Catalog");
      // The seed's catalog: migration 0045 removed the empty General › General
      // folders the old department and desk left behind (S17), so the seeded
      // categories sit at the top level.
      await expect(catalogNode(page, "folder", "General")).toHaveCount(0);
      const general = catalogCategoryRow(page, "General");
      await expect(general).toHaveCount(1);
      await expect(general.locator(".catalog-desk-chip")).toHaveCount(0);
      await expect(general.locator(".catalog-badge").first()).toContainText("Workflow v1");
      await expect(page.locator(".catalog-folder-tree")).toBeVisible();
      await assertCanonicalScreen(page, {
        viewport: width,
        label: `catalog tree @ ${width}`,
        url: page.url(),
        role: "root",
        consoleErrors: obs.consoleErrors,
        pageErrors: obs.pageErrors,
        failedRequests: obs.failedRequests,
        failedResponses: obs.failedResponses,
      });
    }
  });

  test("folders are created and nested in the tree", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    const top = "Browse " + Date.now().toString(36);

    await page.locator('[role="tree"]').focus();
    await page.keyboard.press("Shift+F10");
    await page.getByRole("menuitem", { name: "New folder", exact: true }).click();
    const input = page.locator('.catalog-inline-create input[name="name"]');
    await input.fill(top);
    await input.press("Enter");
    const topRow = catalogNode(page, "folder", top);
    await expect(topRow).toHaveCount(1);
    await expect(page.locator("[data-catalog-create-row]")).toHaveCount(0);
    await expect(topRow).toHaveAttribute("aria-selected", "true");
    await topRow.locator(":scope > .catalog-tree-row").hover();
    await topRow.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    const nestedCreator = page.locator("[data-catalog-create-row]");
    await expect(nestedCreator).toHaveAttribute("data-folder-id", await catalogNodeId(topRow, top));
    await nestedCreator.getByRole("button", { name: "Folder", exact: true }).click();
    await input.fill("Hardware");
    await input.press("Enter");
    const child = catalogNode(page, "folder", "Hardware");
    await expect(child).toHaveCount(1);
    await expect(child).toHaveAttribute("data-parent-id", await catalogNodeId(topRow, top));
    await expect(nestedCreator).toHaveCount(0);
    await expect(child).toHaveAttribute("aria-selected", "true");
    await topRow.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    await expect(nestedCreator).toHaveAttribute("data-folder-id", await catalogNodeId(topRow, top));
    await nestedCreator.getByRole("button", { name: "Folder", exact: true }).click();
    await input.fill("HARDWARE");
    const refusal = page.waitForResponse(
      (response) => response.url().endsWith("/categories/folders") && response.status() === 409,
    );
    await input.press("Enter");
    await refusal;
    await expect(page.locator(".catalog-inline-create .catalog-inline-error")).toHaveText(
      "A folder with that name already exists here.",
    );
    await expect(input).toHaveAttribute("aria-invalid", "true");
    await input.press("Escape");
    await expect(child).toHaveCount(1);
    await expect(page).toHaveURL(/\/categories$/);
    await page.reload();
    await expect(topRow).toHaveCount(1);
    await expect(child).toHaveAttribute("data-parent-id", await catalogNodeId(topRow, top));
  });

  test("a folder never moves into itself and a folder with contents is not deleted", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36);
    const parentID = await createFolderViaUi(page, "Parent " + suffix);
    const childID = await createFolderViaUi(page, "Child " + suffix, parentID);

    // The move drawer offers neither the folder nor anything below it.
    await page.goto(base() + `/categories/folders/${parentID}/move`);
    const move = page.getByRole("dialog", { name: /Move/i });
    await expect(move).toBeVisible();
    await expect(move.locator(`select[name=folder_id] option[value="${parentID}"]`)).toHaveCount(0);
    await expect(move.locator(`select[name=folder_id] option[value="${childID}"]`)).toHaveCount(0);
    await expect(move).toContainText("Moving never changes workflows or existing tickets.");

    // Deleting the parent asks first and is refused while it holds the child.
    await page.goto(base() + `/categories?folder=${parentID}`);
    await catalogNode(page, "folder", "Parent " + suffix)
      .locator(":scope > .catalog-tree-row [data-category-menu]")
      .click();
    await page.getByRole("menuitem", { name: "Delete folder" }).click();
    const confirm = page.locator("#category-delete-dialog");
    await expect(confirm.locator("#category-delete-title")).toHaveText(
      `Delete the folder “Parent ${suffix}”?`,
    );
    await confirm.locator("[data-category-delete-confirm]").click();
    await expect(page.locator(".error-banner")).toHaveText(
      "This folder isn't empty. Move or delete its folders and categories first.",
    );
    await expect(page).toHaveURL(new RegExp(`/categories\\?folder=${parentID}$`));
    await expect(catalogNode(page, "folder", "Child " + suffix)).toHaveCount(1);
  });

  test("a category is created in a folder without a desk and moved without touching its workflow", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 2000 });
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36);
    const folderID = await createFolderViaUi(page, "Access " + suffix);
    const name = "Password reset " + suffix;

    await page.goto(base() + `/categories?folder=${folderID}`);
    const folder = page.locator(`li[data-level="folder"][data-catalog-id="${folderID}"]`);
    await folder.locator(":scope > .catalog-tree-row").hover();
    await folder.locator(":scope > .catalog-tree-row [data-catalog-add]").click();
    const create = page.locator(".catalog-inline-create");
    await expect(create).toHaveAttribute("data-folder-id", folderID);
    // Workflow steps name the desk (issue #491, S1), so the row asks only for a name.
    await expect(create.locator("select")).toHaveCount(0);
    await create.locator('input[name="name"]').fill(name);
    await create.locator('input[name="name"]').press("Enter");
    await expect(catalogCategoryRow(page, name)).toHaveCount(1);
    await expect(create).toHaveCount(0);
    await expect(catalogCategoryRow(page, name)).toHaveAttribute("aria-selected", "true");
    await expect(page).toHaveURL(new RegExp(`/categories\\?folder=${folderID}$`));
    const row = catalogCategoryRow(page, name);
    await expect(row.locator(".catalog-desk-chip")).toHaveCount(0);

    // Editing shows the folder the category has, and no desk.
    const editDrawer = await openCategoryDetails(page, row, { name, description: "" });
    await expect(editDrawer.locator("input[name=folder_id]")).toHaveValue(folderID);
    await expect(editDrawer.locator("select[name=desk_id]")).toHaveCount(0);
    await editDrawer.getByRole("button", { name: "Discard" }).click();
    await expect(editDrawer).toBeVisible();
    await expect(editDrawer.getByRole("button", { name: "Save changes" })).toBeDisabled();
    await expect(row).toBeFocused();

    // Moving to the top level keeps the category's id and workflow link.
    const id = await catalogNodeId(row, `category ${name}`);
    const tree = page.locator(".catalog-tree");
    const box = await tree.boundingBox();
    if (!box) throw new Error("missing catalog tree");
    await row.locator(":scope > .catalog-tree-row").hover();
    await row.locator(":scope > .catalog-tree-row > .catalog-drag-grip").dragTo(tree, {
      targetPosition: { x: box.width / 2, y: box.height - 20 },
    });
    await expect(row).toHaveAttribute("data-parent-id", "0");
    await page.goto(base() + "/categories");
    const moved = catalogCategoryRow(page, name);
    await expect(moved).toHaveCount(1);
    expect(await catalogNodeId(moved, name)).toBe(id);
    await expect(moved.getByRole("link", { name })).toHaveAttribute(
      "href",
      `/categories/${id}/workflow`,
    );
  });

  test("search and Needs attention filter the tree in place", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36);
    const folder = "Searchable " + suffix;
    const folderID = await createFolderViaUi(page, folder);
    await createCategoryInFolder(page, {
      name: "Inside " + suffix,
      folderID,
    });

    {
      await page.goto(base() + "/categories");
      const search = page.getByRole("searchbox", { name: "Search folders and categories" });
      await search.fill(folder);
      await search.press("Enter");
      await expect(page).toHaveURL(/q=Searchable/);
      // A folder's name reaches the categories below it.
      await expect(catalogCategoryRow(page, "Inside " + suffix)).toHaveCount(1);
      await expect(page.locator("mark", { hasText: folder })).toHaveCount(1);
      await expect(catalogCategoryRow(page, "General")).toHaveCount(0);
      await page.getByRole("link", { name: "Clear search" }).click();
      await expect(page).toHaveURL(/\/categories$/);

      const chip = page.locator(".catalog-attention-chip");
      await expect(chip).toContainText("Needs attention");
      await chip.click();
      await expect(page).toHaveURL(/attention=1/);
      await expect(chip).toHaveAttribute("aria-current", "true");
      await expect(catalogCategoryRow(page, "Inside " + suffix)).toBeVisible();
      await expect(catalogNode(page, "folder", folder)).toBeVisible();
      await expect(page.locator(".catalog-tree-row-folder .catalog-badge")).toHaveCount(0);
      await chip.click();
      await expect(page).toHaveURL(/\/categories$/);
    }
  });

  test("the Tree layout remembers collapsed folders and caps the indent past level four", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await loginAsSeeded(page);
    const suffix = Date.now().toString(36);
    let parent = "";
    const ids: string[] = [];
    for (let level = 1; level <= 6; level++) {
      parent = await createFolderViaUi(page, `Deep${level} ${suffix}`, parent);
      ids.push(parent);
    }
    await page.goto(base() + "/categories");
    const level5 = page.locator(`li[data-catalog-id="${ids[4]}"]`);
    await expect(level5.locator(":scope > .catalog-tree-row .catalog-level")).toHaveText("Level 5");
    const indent = (id: string) =>
      page
        .locator(`li[data-catalog-id="${id}"] > .catalog-tree-row`)
        .evaluate((row) => row.getBoundingClientRect().left);
    expect(await indent(ids[4])).toBe(await indent(ids[3]));
    expect(await indent(ids[3])).toBeGreaterThan(await indent(ids[2]));

    const top = page.locator(`li[data-catalog-id="${ids[0]}"]`);
    await toggleCatalogNode(top);
    await expect(level5).toBeHidden();
    await page.reload();
    await expect(
      top.locator(":scope > .catalog-tree-row [data-catalog-disclosure]"),
    ).toHaveAttribute("aria-expanded", "false");
    await toggleCatalogNode(top);
    await expect(level5).toBeVisible();
    await assertNoHorizontalOverflow(page, 1280);
  });

  test("a drawer launcher that receives a 4xx shows non-retry feedback and opens no panel", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    // A launcher for a folder that does not exist answers a plain 404 with no
    // HX-Retarget, the case htmx leaves unswapped and silent.
    const failedURL = "/categories/folders/999999/edit";
    await page.evaluate((href) => {
      const probe = document.createElement("a");
      probe.id = "drawer-load-4xx-probe";
      probe.className = "category-drawer-launcher";
      probe.href = href;
      probe.setAttribute("hx-get", href);
      probe.setAttribute("hx-target", "#category-drawer-host");
      probe.setAttribute("hx-swap", "outerHTML");
      probe.setAttribute("hx-push-url", href);
      probe.textContent = "Rename folder";
      document.querySelector("#categories-background")?.prepend(probe);
      (window as unknown as { htmx: { process: (el: Element) => void } }).htmx.process(probe);
    }, failedURL);

    const drawerHost = page.locator("#category-drawer-host");
    const navigations: string[] = [];
    const navigationHandler = (request: Request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push(request.url());
      }
    };
    page.on("request", navigationHandler);
    try {
      const responsePromise = page.waitForResponse(
        (response) =>
          response.request().headers()["hx-request"] === "true" &&
          new URL(response.url()).pathname === failedURL,
      );
      const urlBefore = page.url();
      await page.locator("#drawer-load-4xx-probe").click();
      const response = await responsePromise;
      expect(response.status()).toBe(404);
      expect(response.headers()["hx-retarget"]).toBeUndefined();
      await expect(drawerHost.locator(".category-drawer")).toHaveCount(0);
      const feedback = page.locator("#save-feedback");
      await expect(feedback).toBeVisible();
      await expect(feedback).toHaveAttribute("role", "alert");
      await expect(feedback.locator(".save-feedback-message")).toHaveText(
        "This panel can't be opened. It may no longer be available.",
      );
      expect(page.url()).toBe(urlBefore);
      expect(navigations).toEqual([]);
    } finally {
      page.removeListener("request", navigationHandler);
    }
  });

  test("a drawer launcher that fails in transport shows the retry copy, distinct from the 4xx copy", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const deskID = await createDeskViaUi(page, "Transport retry desk");
    const row = page.locator(`tr[data-desk-id="${deskID}"]`);
    const launcher = row.locator(".desks-launcher");
    const launcherPath = new URL((await launcher.getAttribute("hx-get")) ?? "", base()).pathname;
    const launcherMatcher = (url: URL) => url.pathname === launcherPath;
    let aborted = false;
    const abortLauncher = async (route: Route) => {
      if (route.request().headers()["hx-request"] === "true") {
        aborted = true;
        await route.abort("failed");
        return;
      }
      await route.continue();
    };
    await page.route(launcherMatcher, abortLauncher);
    try {
      await launcher.click();
      await expect.poll(() => aborted).toBe(true);
      const feedback = page.locator("#save-feedback");
      await expect(feedback).toBeVisible();
      await expect(feedback.locator(".save-feedback-message")).toHaveText(
        "Unable to open this panel. Please try again.",
      );
      await expect(page.locator("#category-drawer-host .category-drawer")).toHaveCount(0);
    } finally {
      await page.unroute(launcherMatcher, abortLauncher);
    }
  });

  test("categories show a neutral status for categories without workflows", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Unconfigured " + Date.now();
    await createCategoryViaUi(page, name);
    const row = catalogCategoryRow(page, name);
    await expect(row.getByText("No workflow", { exact: true })).toBeVisible();
    await expect(row.locator(".catalog-badge-none")).toBeVisible();
  });

  test("create, clear description, and delete a category", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const catName = "Probe Cat " + Date.now();
    await createCategoryViaUi(page, catName);
    await expect(catalogCategoryRow(page, catName)).toBeVisible();

    const row = catalogCategoryRow(page, catName);
    await expect(row).toHaveCount(1);
    const drawer = await openCategoryDetails(page, row, {
      name: catName,
      description: "",
    });
    const description = drawer.getByLabel("Description", { exact: true });
    await expect(description).toHaveValue("");
    const renamed = catName + " Renamed";
    await drawer.getByLabel("Name", { exact: true }).fill(renamed);
    await description.fill("Temporary description");
    await drawer.getByRole("button", { name: /save changes/i }).click();

    await expect(page).toHaveURL(/\/categories/);
    await expect(catalogCategoryRow(page, renamed)).toBeVisible();

    const editedRow = catalogCategoryRow(page, renamed);
    const clearedDrawer = await openCategoryDetails(page, editedRow, {
      name: renamed,
      description: "Temporary description",
    });
    await clearedDrawer.getByLabel("Description", { exact: true }).fill("");
    await clearedDrawer.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(catalogCategoryRow(page, renamed)).toBeVisible();

    const delRow = catalogCategoryRow(page, renamed);
    await expect(delRow).toHaveCount(1);
    await delRow.getByRole("button", { name: /actions for/i }).click();
    await delRow.getByRole("menuitem", { name: /delete category/i }).click();
    await page.locator("#category-delete-dialog [data-category-delete-confirm]").click();
    await expect(catalogCategoryRow(page, renamed)).toHaveCount(0);
    await page.reload();
    await expect(catalogCategoryRow(page, renamed)).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "categories create/rename/delete",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a category's suggestion keywords are edited in the drawer and shown again", async ({
    page,
  }) => {
    // Issue #416, S4: the local engine matches what a requester writes against,
    // among other fields, the words an administrator lists per category. The
    // field belongs to the category drawer and to nobody else, and it is whoever
    // manages the catalog who fills it — the same edit the rest of the drawer has.
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const name = "Keyword Category " + Date.now().toString(36);
    await createCategoryViaUi(page, name);

    const openEditDrawer = async () => {
      const row = catalogCategoryRow(page, name);
      await expect(row).toHaveCount(1);
      await openRowDetails(row);
      const drawer = page.getByRole("region", { name, exact: true });
      await expect(drawer).toBeVisible();
      return drawer;
    };

    const drawer = await openEditDrawer();
    const keywords = drawer.getByLabel("Keywords for suggestions", { exact: true });
    await expect(keywords).toHaveValue("");
    await expect(drawer.locator("[id^=details-keywords-help-]")).toHaveText(
      "Words people use for this category. Comma separated; accents and capitals do not matter.",
    );
    await keywords.fill("impresora, tóner");
    await drawer.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(catalogCategoryRow(page, name)).toHaveCount(1);

    // Re-opening the drawer reads the stored words back, which is what an
    // administrator needs before editing them again. Closing it leaves the plain
    // catalog for the canonical screen assertion below.
    await page.reload();
    const reopened = await openEditDrawer();
    await expect(reopened.getByLabel("Keywords for suggestions", { exact: true })).toHaveValue(
      "impresora, tóner",
    );
    await reopened.getByRole("button", { name: "Discard", exact: true }).click();
    await expect(reopened).toBeVisible();
    await expect(reopened.getByRole("button", { name: "Save changes" })).toBeDisabled();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "categories suggestion keywords",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("category details rename refuses duplicates and keeps its folder", async ({ page }) => {
    await loginAsSeeded(page);
    const folderID = await createFolderViaUi(page, "Details rename folder");
    await createCategoryInFolder(page, { name: "Details rename taken", folderID });
    const id = await createCategoryInFolder(page, { name: "Details rename original", folderID });
    const target = page.locator(
      `[role="treeitem"][data-level="category"][data-catalog-id="${id}"]`,
    );
    await openRowDetails(target);
    const panel = page.locator("[data-catalog-inspector] .catalog-details");
    const name = panel.getByLabel("Name", { exact: true });
    await expect(name).toHaveValue("Details rename original");
    await name.fill("Details rename taken");
    const rejected = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/${id}/edit` && r.request().method() === "POST",
    );
    await panel.getByRole("button", { name: "Save changes" }).click();
    expect((await rejected).status()).toBe(409);
    await expect(name).toHaveAttribute("aria-invalid", "true");
    await expect(panel.locator("[data-details-error]")).toHaveText(
      "A category with that name already exists.",
    );
    await expect(name).toHaveValue("Details rename taken");
    await expect(target).toHaveAttribute("data-name", "Details rename original");
    await expect(target).toHaveAttribute("data-parent-id", folderID);
    await name.fill("Details rename saved");
    const saved = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === `/categories/${id}/edit` && r.request().method() === "POST",
    );
    await panel.getByRole("button", { name: "Save changes" }).click();
    expect((await saved).status()).toBe(200);
    await expect(target).toHaveAttribute("data-name", "Details rename saved");
    await expect(target).toHaveAttribute("data-parent-id", folderID);
    await page.reload();
    await expect(target).toHaveAttribute("data-name", "Details rename saved");
    await expect(target).toHaveAttribute("data-parent-id", folderID);
  });

  test("category duplicate marks only name, keeps the draft, and saves after Stay", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories/new");

    const drawer = page.getByRole("dialog", { name: /New category/i });
    const name = drawer.getByLabel("Name", { exact: true });
    const description = drawer.getByLabel("Description", { exact: true });
    await expect(drawer).toHaveAttribute("aria-describedby", "category-drawer-description");
    await expect(drawer.locator("#category-drawer-description")).toHaveText(
      "Use categories to group requests that follow the same workflow.",
    );
    await expect(name).toHaveAttribute("aria-describedby", "category-name-help");
    await expect(drawer.locator("#category-name-help")).toHaveText(
      "Category names must be globally unique.",
    );
    await name.fill("General");
    await description.fill("Duplicate category draft");

    await assertHtmxSwap(
      page,
      async () => {
        await drawer.getByRole("button", { name: /create category/i }).click();
      },
      {
        endpoint: "/categories",
        method: "POST",
        expectedStatus: 409,
        hxTarget: "#category-drawer-host",
      },
    );

    await expect(name).toHaveAttribute("aria-invalid", "true");
    await expect(name).toHaveAttribute("aria-describedby", "category-name-help");
    await expect(drawer.getByLabel("Folder", { exact: true })).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await expect(description).not.toHaveAttribute("aria-invalid", "true");
    await expect(name).toHaveValue("General");
    await expect(description).toHaveValue("Duplicate category draft");
    await expect(name).toBeFocused();

    await description.fill("Corrected category description");
    await drawer.getByRole("button", { name: "Close catalog details", exact: true }).click();
    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
    await expect(description).toHaveValue("Corrected category description");
    await expect(name).toHaveValue("General");

    const categoryName = "Validated category " + Date.now();
    await name.fill(categoryName);
    await assertHtmxSwap(
      page,
      async () => {
        await drawer.getByRole("button", { name: /create category/i }).click();
      },
      {
        endpoint: "/categories",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#categories-background",
        expectedUrl: /\/categories$/,
      },
    );
    await expect(drawer).toHaveCount(0);
    await expect(catalogCategoryRow(page, categoryName)).toBeVisible();
  });

  test("dirty category drawer backdrop Stay restores the prior form control", async ({ page }) => {
    await loginAsSeeded(page);
    const drawerURL = base() + "/categories/new";
    await page.goto(drawerURL);
    const drawer = page.getByRole("dialog", { name: /New category/i });
    const name = drawer.getByLabel("Name", { exact: true });
    await name.fill("Unsaved category");
    await page.locator(".category-drawer-backdrop").click({ position: { x: 5, y: 5 } });

    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    await expect(confirmation).toBeVisible();
    await expect(confirmation.getByRole("heading")).toHaveText("Leave without saving?");
    await expect(confirmation).toContainText("Your changes will be lost if you leave this drawer.");
    const discard = confirmation.getByRole("button", {
      name: "Discard changes",
      exact: true,
    });
    await expect(discard).toHaveCSS("background-color", "rgb(180, 35, 24)");
    await expect(discard).toHaveCSS("border-color", "rgb(180, 35, 24)");
    await expect(discard).toHaveCSS("color", "rgb(255, 255, 255)");
    await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
    await expect(confirmation).toBeHidden();
    await expect(name).toBeFocused();
    await expect(page).toHaveURL(drawerURL);
  });

  test("dirty category drawer keeps values and URL until discard", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    // The seed has no folder since 0045 (S17), so the test brings its own.
    const folderID = await createFolderViaUi(page, "Drawer " + Date.now().toString(36).slice(2, 7));
    const listURL = new RegExp(`/categories\\?folder=${folderID}$`);
    const drawerPath = new RegExp(`/categories/new\\?folder_id=${folderID}&folder=${folderID}$`);
    await page.goto(base() + `/categories?folder=${folderID}`);
    const addCategory = async () => {
      await page.goto(base() + `/categories/new?folder_id=${folderID}&folder=${folderID}`);
    };
    await addCategory();
    const drawer = page.getByRole("dialog", { name: /New category/i });
    const name = drawer.getByLabel("Name", { exact: true });
    const close = drawer.getByRole("button", {
      name: "Close catalog details",
      exact: true,
    });
    await expect(page).toHaveURL(drawerPath);
    let drawerURL = page.url();
    await name.fill("Unsaved category");

    await close.click();
    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    const stay = confirmation.getByRole("button", { name: "Stay", exact: true });
    await expect(confirmation).toBeVisible();
    await expect(stay).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(confirmation).toBeHidden();
    await expect(name).toHaveValue("Unsaved category");
    await expect(page).toHaveURL(drawerURL);
    await expect(close).toBeFocused();

    await name.fill("");
    await close.click();
    await expect(drawer).toHaveCount(0);
    await expect(page).toHaveURL(listURL);
    await addCategory();
    await expect(drawer).toBeVisible();
    await expect(page).toHaveURL(drawerPath);
    drawerURL = page.url();
    await name.fill("Unsaved category");
    await page.goBack();
    await expect(confirmation).toBeVisible();
    await expect(page).toHaveURL(drawerURL);
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(page).toHaveURL(listURL);
    await expect(page.locator('[role="tree"]')).toBeFocused();
  });

  test("clean category drawer close restores the launcher for Cancel, Escape and backdrop", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Focus restore " + Date.now().toString(36).slice(2, 8);
    const deskID = await createDeskViaUi(page, name);
    const structureURL = page.url();

    for (const mechanism of ["Cancel", "Escape", "backdrop"] as const) {
      await page.goto(structureURL);
      const row = page.locator(`tr[data-desk-id="${deskID}"]`);
      await expect(row).toHaveCount(1);
      const launcher = row.locator(".desks-launcher");
      const focusKey = await launcher.getAttribute("data-focus-key");
      expect(focusKey).toBeTruthy();
      await launcher.click();
      const drawer = page.getByRole("dialog", { name: /Edit desk/i });
      await expect(drawer).toBeVisible();

      if (mechanism === "Cancel") {
        await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
      } else if (mechanism === "Escape") {
        await page.keyboard.press("Escape");
      } else {
        await page.locator(".category-drawer-backdrop").click({ position: { x: 5, y: 5 } });
      }
      await expect(drawer).toHaveCount(0);
      await expect(page.locator(`[data-focus-key="${focusKey}"]`)).toBeFocused();
    }
  });

  test("a clean drawer close never takes focus back once it moved elsewhere", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Focus backstop " + Date.now().toString(36).slice(2, 8);
    const deskID = await createDeskViaUi(page, name);
    await page.goto(page.url());
    const row = page.locator(`tr[data-desk-id="${deskID}"]`);
    const launcher = row.locator(".desks-launcher");
    const focusKey = await launcher.getAttribute("data-focus-key");
    await launcher.click();
    const drawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(drawer).toBeVisible();

    // Hold the close's delayed focus backstop so the test, not the machine's
    // speed, decides that focus moves before it runs (the CI race on #343).
    await page.evaluate(() => {
      const w = window as unknown as { __heldBackstops: Array<() => void> };
      const original = window.setTimeout;
      w.__heldBackstops = [];
      window.setTimeout = ((handler: () => void, delay?: number, ...rest: unknown[]) => {
        if (delay === 100) {
          w.__heldBackstops.push(handler);
          return 0;
        }
        return original(handler, delay, ...rest);
      }) as typeof window.setTimeout;
    });
    await page.keyboard.press("Escape");
    await expect(drawer).toHaveCount(0);
    await expect(page.locator(`[data-focus-key="${focusKey}"]`)).toBeFocused();

    const skip = page.locator("a.skip-link");
    await skip.focus();
    const held = await page.evaluate(() => {
      const w = window as unknown as { __heldBackstops: Array<() => void> };
      for (const handler of w.__heldBackstops) handler();
      return w.__heldBackstops.length;
    });
    expect(held, "the close schedules its delayed backstop").toBeGreaterThan(0);
    await expect(skip, "the backstop must not pull focus back from where it moved").toBeFocused();
  });

  test("clean close restores focus for every drawer launcher", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const deskID = await createDeskViaUi(page, "Focus launchers " + Date.now().toString(36));
    const deskRow = () => page.locator(`tr[data-desk-id="${deskID}"]`);
    const openers = [
      async () => {
        const launcher = deskRow().locator(".desks-launcher");
        const focusKey = await launcher.getAttribute("data-focus-key");
        await launcher.click();
        return { dialog: page.getByRole("dialog", { name: /Edit desk/i }), focusKey };
      },
      async () => {
        const menu = deskRow().locator("[data-category-menu]");
        const focusKey = await menu.getAttribute("data-focus-key");
        await menu.click();
        await openRowMenu(deskRow())
          .getByRole("menuitem", { name: "Edit desk", exact: true })
          .click();
        return { dialog: page.getByRole("dialog", { name: /Edit desk/i }), focusKey };
      },
    ];
    for (const open of openers) {
      await page.goto(base() + "/desks");
      const { dialog, focusKey } = await open();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
      await expect(dialog).toHaveCount(0);
      await expect(page.locator(`[data-focus-key="${focusKey}"]`)).toBeFocused();
    }
  });

  test("direct edit without a launcher falls back to the tree, never body", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Vanished trigger " + Date.now().toString(36).slice(2, 8);
    const id = await createCategoryViaUi(page, name);
    // Direct edit navigation is the supported entry with no opener to restore.
    await page.goto(base() + `/categories/${id}/edit`);
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();

    await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(page.locator('[role="tree"]')).toBeFocused();
    await expect(page.locator("body")).not.toBeFocused();
  });

  test("direct edit without a tree falls back to the named region", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Vanished region " + Date.now().toString(36).slice(2, 8);
    const id = await createCategoryViaUi(page, name);
    await page.goto(base() + `/categories/${id}/edit`);
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();
    // No launcher exists for direct navigation; simulate a tree removed during the edit.
    await page.locator('[role="tree"]').evaluate((tree) => tree.remove());

    await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(
      page.locator('section.catalog-tree-region[aria-labelledby="catalog-tree-title"]'),
    ).toBeFocused();
    await expect(page.locator("body")).not.toBeFocused();
  });

  test("dirty folder and desk drawers require an explicit discard", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    // The seed has no folder since 0045 (S17), so the test brings its own.
    const folderID = await createFolderViaUi(page, "Drawer " + Date.now().toString(36).slice(2, 7));
    await page.goto(base() + `/categories/folders/${folderID}/edit`);

    const folder = page.getByRole("dialog", { name: /Edit folder/i });
    const folderName = folder.getByLabel("Name", { exact: true });
    await expect(folder).toHaveAttribute("aria-describedby", "folder-drawer-description");
    await expect(folder.locator("#folder-drawer-description")).toHaveText(
      "Folders group categories for requesters. Nest them as deep as you need.",
    );
    await expect(folderName).toHaveAttribute("aria-describedby", "folder-name-help");
    await expect(folder.locator("#folder-name-help")).toHaveText(
      "Names are unique inside the same folder.",
    );
    await folderName.fill("Unsaved folder");
    await folder.getByRole("button", { name: "Close catalog details" }).click();

    const confirmation = page.getByRole("dialog", { name: "Leave without saving?" });
    await expect(confirmation).toBeVisible();
    await expect(confirmation.getByRole("button", { name: "Stay", exact: true })).toBeFocused();
    await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
    await expect(folderName).toHaveValue("Unsaved folder");
    await expect(page).toHaveURL(new RegExp(`/categories/folders/${folderID}/edit`));
    await folder.getByRole("button", { name: "Close catalog details" }).click();
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(folder).toHaveCount(0);

    const deskID = await selectCatalogDesk(page, "General");
    await page.goto(base() + `/desks/${deskID}/edit`);
    const desk = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(desk.locator("#desk-drawer-description")).toHaveText(
      "A desk is a team: its members take the tickets of the categories that name it.",
    );
    await desk.getByLabel("Description", { exact: true }).fill("Unsaved desk description");
    await desk.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(desk).toHaveCount(0);
    await expect(page).toHaveURL(/\/desks$/);
  });

  test("history restoration does not revive a stale category save confirmation", async ({
    page,
  }) => {
    test.setTimeout(30_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    // The seed has no folder since 0045 (S17), so the test brings its own.
    const folderID = await createFolderViaUi(page, "Drawer " + Date.now().toString(36).slice(2, 7));
    const listPath = `/categories?folder=${folderID}`;
    await page.goto(base() + listPath);

    // Navigate directly to the still-supported drawer URL.
    const addCategory = async () => {
      await page.goto(base() + `/categories/new?folder_id=${folderID}&folder=${folderID}`);
    };
    await addCategory();
    const drawer = page.getByRole("dialog", { name: /New category/i });
    await expect(drawer).toBeVisible();
    const categoryName = "History confirmation " + Date.now().toString(36).slice(2, 8);
    await drawer.getByLabel("Name", { exact: true }).fill(categoryName);
    await assertHtmxSwap(
      page,
      () => drawer.getByRole("button", { name: /create category/i }).click(),
      {
        endpoint: "/categories",
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#categories-background",
        expectedUrl: new RegExp(`/categories\\?folder=${folderID}$`),
      },
    );
    const feedback = page.locator("#save-feedback");
    await expect(feedback).toBeVisible();

    await addCategory();
    await expect(page).toHaveURL(
      new RegExp(`/categories/new\\?folder_id=${folderID}&folder=${folderID}$`),
    );
    await expect(drawer).toBeVisible();
    await page.waitForTimeout(5_100);

    await page.goBack();
    await expect.poll(() => new URL(page.url()).pathname).toBe("/categories");
    expect([...new URL(page.url()).searchParams.entries()].sort()).toEqual(
      [...new URL(base() + listPath).searchParams.entries()].sort(),
    );
    await expect(page.locator("h1").filter({ hasText: "Catalog" })).toBeVisible();
    await expect(page.getByRole("dialog", { name: /New category/i })).toHaveCount(0);
    await expect(catalogCategoryRow(page, categoryName)).toBeVisible();
    await expect(feedback).toBeHidden();
  });

  test("an aborted read-only workflow step selection cannot suppress a concurrent save failure", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const categoryID = await createCategoryViaUi(
      page,
      "Selection feedback " + Date.now().toString(36).slice(2, 8),
    );
    const workflowPath = `/categories/${categoryID}/workflow`;
    await page.goto(base() + workflowPath);

    for (let step = 0; step < 2; step += 1) {
      await page.locator(".workflow-add-popover summary").last().click();
      const response = await assertHtmxSwap(
        page,
        () =>
          page.getByRole("button", { name: "Give the agent a task", exact: true }).last().click(),
        {
          endpoint: (url) => {
            const requestURL = new URL(url);
            return (
              requestURL.pathname === workflowPath &&
              requestURL.searchParams.get("add_step_type") === "manual_task"
            );
          },
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#workflow-builder",
        },
      );
      expect(new URLSearchParams(response.request().postData() ?? "").get("action")).toBe(
        "add_step",
      );
    }

    const feedback = page.locator("#save-feedback");
    await expect(feedback).toContainText("Saved");
    await feedback.getByRole("button", { name: "Dismiss confirmation" }).click();
    await expect(feedback).toBeHidden();

    const instructions = page.getByLabel("Instructions", { exact: true });
    await expect(instructions).toBeVisible();
    // The editor normally queues same-form requests. Remove that one
    // transport guard here to exercise the feedback listener's own
    // per-request ordering with a concurrent read-only POST.
    await page.locator("#workflow-form").evaluate((form) => form.removeAttribute("hx-sync"));
    let releaseSaveAbort: () => void = () => undefined;
    let markSaveRequestSeen!: () => void;
    const saveRequestSeen = new Promise<void>((resolve) => {
      markSaveRequestSeen = resolve;
    });
    let saveAborted = false;
    let selectionAborted = false;
    const abortConcurrentRequests = async (route: Route) => {
      const request = route.request();
      if (
        isHtmxPost(request, { path: workflowPath, action: "save", target: "#workflow-builder" })
      ) {
        markSaveRequestSeen();
        await new Promise<void>((resolve) => {
          releaseSaveAbort = resolve;
        });
        await route.abort("failed");
        saveAborted = true;
        return;
      }
      if (
        isHtmxPost(request, {
          path: workflowPath,
          action: "select_step",
          target: "#workflow-builder",
        })
      ) {
        await route.abort("failed");
        selectionAborted = true;
        return;
      }
      await route.continue();
    };
    await page.route((url) => url.pathname === workflowPath, abortConcurrentRequests);
    try {
      const attemptedValue = "This save fails after a read-only selection";
      const saveRequest = page.waitForRequest((request) =>
        isHtmxPost(request, { path: workflowPath, action: "save", target: "#workflow-builder" }),
      );
      await instructions.fill(attemptedValue);
      await page.locator('.page-actions button[name="action"][value="save"]').click();
      await saveRequest;
      await saveRequestSeen;

      const selectionRequest = page.waitForRequest((request) =>
        isHtmxPost(request, {
          path: workflowPath,
          action: "select_step",
          target: "#workflow-builder",
        }),
      );
      await page.locator(".workflow-step-card-link").nth(0).click();
      const request = await selectionRequest;
      expect(request.headers()["hx-request"]).toBe("true");
      expect(request.headers()["hx-target"]).toBe("workflow-builder");
      expect(new URLSearchParams(request.postData() ?? "").get("action")).toBe("select_step");
      await expect.poll(() => selectionAborted).toBe(true);

      releaseSaveAbort();
      await expect.poll(() => saveAborted).toBe(true);
      await expect(feedback.locator(".save-feedback-message")).toHaveText(
        "Unable to save changes. Please try again.",
      );
      await expect(feedback).toHaveAttribute("role", "alert");
      await expect(instructions).toHaveValue(attemptedValue);
    } finally {
      releaseSaveAbort();
      await page.unroute((url) => url.pathname === workflowPath, abortConcurrentRequests);
    }
  });

  test("workflow feedback retires success for validation and a read-only selection cannot suppress a transport failure", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const categoryName = "Feedback flow " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, categoryName);
    const category = catalogCategoryRow(page, categoryName);
    const categoryID = await catalogNodeId(category, `category ${categoryName}`);
    const workflowPath = `/categories/${categoryID}/workflow`;
    await openRowDetails(category);
    await page
      .getByRole("region", { name: categoryName, exact: true })
      .getByRole("link", { name: "Configure workflow" })
      .click();

    await page.locator(".workflow-add-step summary").first().click();
    const addResponse = await assertHtmxSwap(
      page,
      () =>
        page.getByRole("button", { name: "Give the agent a task", exact: true }).first().click(),
      {
        endpoint: (url) => {
          const requestURL = new URL(url);
          return (
            requestURL.pathname === workflowPath &&
            requestURL.searchParams.get("add_step_type") === "manual_task"
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    expect(addResponse.headers()["x-save-feedback"]).toContain('"message":"Saved"');
    const instructions = page.getByLabel("Instructions", { exact: true });
    await expect(instructions).toBeVisible();

    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");
    await expect(instructions).toHaveValue("");
    const invalidPublish = await assertHtmxSwap(
      page,
      () => page.getByRole("button", { name: "Publish", exact: true }).click(),
      {
        endpoint: workflowPath,
        method: "POST",
        expectedStatus: 422,
        hxTarget: "#workflow-builder",
      },
    );
    expect(invalidPublish.headers()["x-save-feedback"]).toBeUndefined();
    const validationAlert = page.locator(".error-banner[role='alert']");
    await expect(validationAlert).toBeVisible();
    await expect(validationAlert).toHaveClass(/error-banner/);
    await expect(instructions).toHaveValue("");
    await expect(instructions).toBeFocused();
    await expect(page.locator("#save-feedback")).toBeHidden();
    await page.waitForTimeout(5_100);
    await expect(validationAlert).toBeVisible();
    await expect(validationAlert).toHaveAttribute("role", "alert");
    await expect(page.locator("#save-feedback")).toBeHidden();

    const recoveredInstructions = "Recover this workflow";
    const recoveryRequest = page.waitForRequest((request) => {
      if (request.method() !== "POST" || request.headers()["hx-request"] !== "true") return false;
      const requestURL = new URL(request.url());
      if (requestURL.pathname !== workflowPath || requestURL.search !== "") return false;
      const form = new URLSearchParams(request.postData() ?? "");
      return (
        form.get("action") === "save" && form.get("step_0_instructions") === recoveredInstructions
      );
    });
    await assertHtmxSwap(
      page,
      async () => {
        await instructions.fill(recoveredInstructions);
        await page.locator('.page-actions button[name="action"][value="save"]').click();
      },
      {
        endpoint: workflowPath,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await recoveryRequest;
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");
    await page.reload();
    await expect(instructions).toHaveValue(recoveredInstructions);
    await expect(page.locator(".error-banner[role='alert']")).toHaveCount(0);

    const abortMatcher = (url: URL) => url.pathname === workflowPath;
    let aborted = false;
    const abortRequest = async (route: Route) => {
      if (route.request().method() === "POST") {
        await route.abort("failed");
        aborted = true;
        return;
      }
      await route.continue();
    };
    await page.route(abortMatcher, abortRequest);
    try {
      await instructions.fill("This value stays in the editor");
      await page.locator('.page-actions button[name="action"][value="save"]').click();
      await expect.poll(() => aborted).toBe(true);
      const feedback = page.locator("#save-feedback");
      await expect(feedback.locator(".save-feedback-message")).toHaveText(
        "Unable to save changes. Please try again.",
      );
      await expect(feedback).toHaveAttribute("role", "alert");
      await expect(feedback).toHaveAttribute("aria-live", "assertive");
      await expect(instructions).toHaveValue("This value stays in the editor");
      await page.waitForTimeout(5_100);
      await expect(feedback).toBeVisible();
    } finally {
      await page.unroute(abortMatcher, abortRequest);
    }
  });

  for (const width of [1280, 390]) {
    test(`workflow save and publish keep the coalesced toast clear of actions at ${width}px`, async ({
      browser,
    }) => {
      test.setTimeout(45_000);
      const context = await browser.newContext({
        viewport: { width, height: 844 },
        hasTouch: width === 390,
        isMobile: width === 390,
      });
      const page = await context.newPage();
      const obs = collectObservability(page);
      try {
        await loginAsSeeded(page);
        const categoryName = `Coalesced toast ${width} ${Date.now().toString(36)}`;
        const categoryID = await createCategoryViaUi(page, categoryName);
        const workflowPath = `/categories/${categoryID}/workflow`;
        await page.goto(base() + workflowPath);
        await page.locator(".workflow-add-step summary").first().click();
        const addResponse = await assertHtmxSwap(
          page,
          () =>
            page
              .getByRole("button", { name: "Give the agent a task", exact: true })
              .first()
              .click(),
          {
            endpoint: (url) => {
              const parsed = new URL(url);
              return (
                parsed.pathname === workflowPath &&
                parsed.searchParams.get("add_step_type") === "manual_task"
              );
            },
            method: "POST",
            expectedStatus: 200,
            hxTarget: "#workflow-builder",
          },
        );
        expect(addResponse.headers()["x-save-feedback"]).toContain('"message":"Saved"');
        const toast = page.locator("#save-feedback");
        await expect(toast).toBeVisible();
        await expect(toast.locator(".save-feedback-message")).toHaveText("Saved");
        await expect(toast).toHaveAttribute("role", "status");
        await expect(toast).toHaveAttribute("aria-live", "polite");
        await expect(toast).toHaveAttribute("aria-atomic", "true");
        await toast.evaluate(async (element) => {
          await Promise.all(element.getAnimations().map((animation) => animation.finished));
        });
        const publish = page.locator('.page-actions button[value="publish"]');
        const measurePublish = () =>
          publish.evaluate((button) => {
            const bounds = button.getBoundingClientRect();
            const feedback = document.querySelector("#save-feedback");
            if (!feedback) throw new Error("Saved feedback is missing");
            const toastBounds = feedback.getBoundingClientRect();
            const x = bounds.x + bounds.width / 2;
            const y = bounds.y + bounds.height / 2;
            const hit = document.elementFromPoint(x, y);
            return {
              x,
              y,
              overlaps:
                bounds.left < toastBounds.right &&
                bounds.right > toastBounds.left &&
                bounds.top < toastBounds.bottom &&
                bounds.bottom > toastBounds.top,
              targetsPublish: hit === button || button.contains(hit),
              savedVisible: feedback.classList.contains("is-visible"),
            };
          });
        const initialGeometry = await measurePublish();
        expect(initialGeometry.savedVisible).toBe(true);
        expect(initialGeometry.overlaps, "Saved must not cover any part of Publish").toBe(false);
        expect(initialGeometry.targetsPublish).toBe(true);

        // Dismiss retires feedback only: no mutation, swap or publication.
        const mutations: Request[] = [];
        const onMutation = (request: Request) => {
          if (request.method() === "POST" && new URL(request.url()).pathname === workflowPath) {
            mutations.push(request);
          }
        };
        page.on("request", onMutation);
        const builderBefore = await page.locator("#workflow-builder").innerHTML();
        const revisionBefore = await page.locator('input[name="draft_revision"]').inputValue();
        const urlBefore = page.url();
        const headingBefore = await page.locator("h1").textContent();
        await expect(page.locator("[data-workflow-version]")).toHaveCount(0);
        await toast.getByRole("button", { name: "Dismiss confirmation" }).click();
        await expect(toast).toBeHidden();
        expect(mutations).toEqual([]);
        expect(await page.locator("#workflow-builder").innerHTML()).toBe(builderBefore);
        expect(page.url()).toBe(urlBefore);
        expect(await page.locator("h1").textContent()).toBe(headingBefore);
        await page.reload();
        await expect(page.locator("[data-workflow-version]")).toHaveCount(0);
        await expect(page.locator('input[name="draft_revision"]')).toHaveValue(revisionBefore);
        expect(mutations).toEqual([]);
        page.off("request", onMutation);

        const instructions = page.getByLabel("Instructions", { exact: true });
        const save = async (value: string) =>
          assertHtmxSwap(
            page,
            async () => {
              await instructions.fill(value);
              await page.locator('.page-actions button[value="save"]').click();
            },
            {
              endpoint: workflowPath,
              method: "POST",
              expectedStatus: 200,
              hxTarget: "#workflow-builder",
            },
          );
        await save("Coalesced save first");
        await expect(toast.locator(".save-feedback-message")).toHaveText("Saved");
        await page.waitForTimeout(3_000);
        await save("Coalesced save second");
        await expect(toast.locator(".save-feedback-message")).toHaveText("Saved");
        await expect(toast).toHaveClass(/(?:^| )is-visible(?: |$)/);
        await expect(toast).toBeVisible();
        await expect(page.locator("#save-feedback")).toHaveCount(1);
        // The second Save restarts the existing five-second timer.
        await page.waitForTimeout(2_500);
        await expect(toast).toBeVisible();
        await toast.evaluate(async (element) => {
          await Promise.all(element.getAnimations().map((animation) => animation.finished));
        });
        const geometry = await measurePublish();
        expect(geometry.savedVisible).toBe(true);
        expect(geometry.overlaps, "Saved must not cover Publish after editing and saving").toBe(
          false,
        );
        expect(geometry.targetsPublish).toBe(true);
        const publishResponse = await assertHtmxSwap(
          page,
          async () => {
            await expect(toast.locator(".save-feedback-message")).toHaveText("Saved");
            // Real coordinates never auto-wait for the toast to disappear.
            if (width === 390) await page.touchscreen.tap(geometry.x, geometry.y);
            else await page.mouse.click(geometry.x, geometry.y);
          },
          {
            endpoint: workflowPath,
            method: "POST",
            expectedStatus: 200,
            hxTarget: "#workflow-builder",
          },
        );
        expect(new URLSearchParams(publishResponse.request().postData() ?? "").get("action")).toBe(
          "publish",
        );
        expect(publishResponse.headers()["x-save-feedback"]).toContain("Published");
        await expect(page.locator("[data-workflow-version]")).toContainText("Published v1");
        await expect(toast.locator(".save-feedback-message")).toHaveText("Published");
        await expect(page.locator("#save-feedback")).toHaveCount(1);
        await page.waitForTimeout(5_100);
        await expect(toast).toBeHidden();
        await page.reload();
        await expect(page.locator("[data-workflow-version]")).toContainText("Published v1");
        await expect(instructions).toHaveValue("Coalesced save second");
        await assertNoHorizontalOverflow(page, width);
        expect(obs.consoleErrors).toEqual([]);
        expect(obs.pageErrors).toEqual([]);
        expect(obs.failedRequests).toEqual([]);
        expect(obs.failedResponses).toEqual([]);
      } finally {
        await context.close();
      }
    });
  }

  test("workflow builder integrated journey: create category, add step, publish, reload, create ticket, verify published workflow in ticket", async ({
    page,
  }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // 1) create a category
    const catName = "FlowCat " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, catName);
    await expect(catalogCategoryRow(page, catName)).toBeVisible();

    // 2) open its workflow
    const catRow = catalogCategoryRow(page, catName);
    await expect(catRow).toHaveCount(1);
    const categoryId = await catalogNodeId(catRow, `category ${catName}`);
    const workflowPath = `/categories/${categoryId}/workflow`;
    await openRowDetails(catRow);
    await page
      .getByRole("region", { name: catName, exact: true })
      .getByRole("link", { name: "Configure workflow" })
      .click();
    await expect(page.locator("#workflow-builder")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator("h2#workflow-builder-title")).toContainText(/workflow steps/i);
    await expect(page.locator(".workflow-step-rail")).toBeVisible();

    // Ensure workflow builder form carries HTMX contract (complementary evidence)
    await expect(page.locator("#workflow-builder #workflow-form")).toHaveAttribute(
      "hx-post",
      /\/workflow/,
    );
    await expect(page.locator("#workflow-builder #workflow-form")).toHaveAttribute(
      "hx-target",
      "#workflow-builder",
    );

    // 3) add a VALID step — a Give the agent a task step is valid by default
    const cards = page.locator(".workflow-step-card");
    const countBeforeAdd = await cards.count();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const addBtn = page
      .locator(".workflow-add-options button")
      .filter({ hasText: "Give the agent a task" })
      .first();
    await expect(addBtn).toBeVisible();

    await assertHtmxSwap(
      page,
      async () => {
        await addBtn.click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("add_step_type") === "manual_task"
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(countBeforeAdd + 1);
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // Editing must not autosave: no workflow POST may fire from input alone
    const instructionsInput = page.getByLabel(/instructions/i);
    await expect(instructionsInput).toBeVisible({ timeout: 10000 });
    let autosaved = false;
    const onAutosave = (request: Request) => {
      autosaved ||=
        request.method() === "POST" &&
        new URL(request.url()).pathname === `/categories/${categoryId}/workflow`;
    };
    page.on("request", onAutosave);
    await instructionsInput.fill("Handle the ticket");
    await page.waitForTimeout(700);
    page.off("request", onAutosave);
    expect(autosaved, "editing must not issue autosave requests").toBe(false);

    // Explicit Save submits the complete draft and confirms persistence
    const saveButton = page.locator('.page-actions button[name="action"][value="save"]');
    await expect(saveButton).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await saveButton.click();
      },
      {
        endpoint: `/categories/${categoryId}/workflow`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // Remove step asks through the confirmation dialog before it posts.
    const countBeforeRemove = await cards.count();
    const lastCard = cards.last();
    const menuSummary = lastCard.locator(".workflow-trigger").first();
    await expect(menuSummary).toBeVisible();
    await menuSummary.click();
    const removeBtn = lastCard.getByRole("button", { name: /remove step/i });
    await expect(removeBtn).toBeVisible();
    await removeBtn.click();
    const removeStepDialog = page.locator("#workflow-remove-step-dialog");
    await expect(removeStepDialog).toBeVisible();
    await expect(cards).toHaveCount(countBeforeRemove);

    // Remove-step POST goes to /categories/{id}/workflow?step_index=... —
    // the action=remove_step lives in the form body, not the query string.
    await assertHtmxSwap(
      page,
      async () => {
        await removeStepDialog.getByRole("button", { name: "Remove step" }).click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("step_index") === String(countBeforeRemove - 1) &&
            !parsedURL.searchParams.has("action")
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(countBeforeRemove - 1);

    // Re-add a step so we have at least one to publish (workflow must be non-empty)
    const countBeforeReAdd = await cards.count();
    expect(countBeforeReAdd).toBe(0);
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    await expect(addBtn).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await addBtn.click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("add_step_type") === "manual_task"
          );
        },
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#workflow-builder",
      },
    );
    await expect(cards).toHaveCount(1);
    const instr = page.getByLabel(/instructions/i);
    await expect(instr).toBeVisible();
    await instr.fill("Handle the ticket");

    // 4) PUBLISH — must execute publication, not just check button exists
    const publishBtn = page.getByRole("button", { name: /publish/i });
    await expect(publishBtn).toBeVisible();
    // Publishing re-renders the same draft, so the builder HTML is
    // byte-identical; assert the exact POST contract and read the visible
    // result from the feedback toast instead of a target mutation.
    const publishResp = await assertHtmxNoSwap(
      page,
      async () => {
        await publishBtn.click();
      },
      {
        endpoint: `/categories/${categoryId}/workflow`,
        method: "POST",
        expectedStatus: 200,
      },
    );
    expect(publishResp.headers()["x-save-feedback"]).toContain("Published");
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Published");
    // After publish, no inline errors
    await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);

    // 5) reload and verify persistence — step count survives reload
    const countAfterPublish = await cards.count();
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator(".workflow-step-card")).toHaveCount(countAfterPublish);
    // Badge on /categories should now show Published for this category
    await page.goto(base() + "/categories");
    await expect(catalogCategoryRow(page, catName).locator(".catalog-badge").first()).toContainText(
      /workflow v\d+/i,
    );

    // 6) create a ticket using that category
    const ticketTitle = "FlowTicket " + Date.now().toString(36).slice(2, 8);
    const ticketId = await createTicketViaUi(page, {
      title: ticketTitle,
      description: "workflow published probe",
      category: catName,
      priority: "high",
    });

    // 7) verify the published workflow appears as a passive checklist
    // because the newly created ticket has no assigned agent yet.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText(catName);
    const checklist = page.locator("#workflow-pending");
    await expect(checklist).toBeVisible({
      timeout: 10_000,
    });
    await expect(checklist).toHaveClass(/workflow-pending-info/);
    await expect(checklist.locator("h3")).toHaveText("Steps");
    await expect(checklist.locator(".workflow-checklist-count")).toHaveText("0 of 1 done");
    await expect(checklist.locator('[role="progressbar"]')).toHaveAttribute("aria-valuenow", "0");
    await expect(checklist.locator('[role="progressbar"]')).toHaveAttribute("aria-valuemax", "1");
    const passiveStep = checklist.locator(".workflow-checklist-step.is-current");
    await expect(passiveStep.locator(".workflow-checklist-name")).toHaveText("Handle the ticket");
    await expect(passiveStep.locator(".workflow-checklist-meta")).toHaveText(
      "Nobody is assigned yet.",
    );
    await expect(passiveStep.locator(".workflow-checklist-blocked")).toBeVisible();
    // Issue #438 (S4, S8): nobody can act here, so the card is badged for
    // nobody and keeps its plain hairline instead of the accent highlight.
    await expect(checklist.locator(".workflow-checklist-badge")).toHaveCount(0);
    await expectStepsPlain(page);
    // Issue #438 (S9): the checklist is its own Steps card before Activity.
    await expectStepsCardBeforeActivity(page);
    await expect(page.locator("#timeline .timeline-entry").first()).not.toHaveClass(
      /workflow-pending/,
    );

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "workflow passive timeline item",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.locator("#workflow-pending")).toHaveClass(/workflow-pending-info/);
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "workflow passive timeline item mobile",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    await page.setViewportSize({ width: 1280, height: 800 });
    await page.reload();
    await expect(pickerButton(page, "assign-user")).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await applyAssignee(page, "Alice Admin");
      },
      {
        endpoint: `/tickets/${ticketId}/assign`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    const pending = page.locator("#workflow-pending");
    await expect(pending).toHaveClass(/workflow-pending-action/);
    await expect(pending.locator("h3")).toHaveText("Steps");
    await expect(pending.locator(".workflow-checklist-count")).toHaveText("0 of 1 done");
    const assignedStep = pending.locator(".workflow-checklist-step.is-current");
    await expect(assignedStep.locator(".workflow-checklist-name")).toHaveText("Handle the ticket");
    await expect(assignedStep.locator(".workflow-checklist-meta")).toHaveText(
      "Assigned to Alice Admin · you",
    );
    // Issue #438 (S4, S8): the assignee is the one actor who can complete this
    // step, so their Steps card is the one that is badged and outlined.
    await expect(pending.locator(".workflow-checklist-badge")).toHaveText("Your turn");
    await expectStepsHighlighted(page);
    await expect(pending.getByLabel("Solution (optional)")).toBeVisible();
    await expect(pending.getByRole("button", { name: "Complete" })).toBeVisible();
    // Issue #438 (S9): the actionable checklist keeps its card before Activity.
    await expectStepsCardBeforeActivity(page);
    await expect(page.locator("#timeline .timeline-entry").first()).not.toHaveClass(
      /workflow-pending/,
    );

    await assertHtmxSwap(
      page,
      async () => {
        await pending.getByRole("button", { name: "Complete" }).click();
      },
      {
        endpoint: `/tickets/${ticketId}/workflow/steps/1/complete`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.locator("#workflow-pending")).toHaveCount(0);
    const assertCompletedManualStatic = async () => {
      const completedManual = page.locator("#timeline .timeline-entry.timeline-manual").first();
      await expect(completedManual).toHaveCount(1);
      await expect(completedManual).toBeVisible();
      // One grammar (issue #386, S3): the entry carries its head line and the
      // rail dot, not a per-event glyph. Issue #438 (S4): this reader completed
      // the step, so the feed is viewer-relative and names the actor as You.
      await expect(completedManual.locator(".timeline-entry-head .narrative")).toHaveText(
        "You completed the task",
      );
      await expect(completedManual.locator(".event-icon")).toHaveCount(0);
      await expect(completedManual.getByText("Task", { exact: true })).toHaveCount(1);
      await expect(completedManual.locator("dd").first()).toHaveText("Handle the ticket");
      await expect(completedManual.locator("dl")).toBeVisible();
      await expect(completedManual.locator(".when")).toBeVisible();
      await expect(completedManual.getByText("Solution", { exact: true })).toHaveCount(0);
      await expect(
        completedManual.locator(
          "details, summary, button, .timeline-event-summary, [open], [aria-expanded], [aria-controls], [tabindex]",
        ),
      ).toHaveCount(0);
      await expect(completedManual.locator(".when")).not.toContainText("Alice Admin");
      await expect(completedManual).not.toHaveCSS("cursor", "pointer");
      await expect(completedManual.locator(".timeline-entry-head")).not.toHaveCSS(
        "cursor",
        "pointer",
      );
      // The rail draws the dot: exactly one marker per entry, never a glyph.
      const dot = await completedManual.evaluate((element) => {
        const style = getComputedStyle(element, "::before");
        return { content: style.content, radius: style.borderTopLeftRadius };
      });
      expect(dot.radius).toBe("50%");
    };

    // Desktop: the completed task and both definition-list rows are visible
    // without a disclosure control; this completion has no solution.
    await assertCompletedManualStatic();
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "workflow completed timeline",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });

    // Mobile keeps the same static, always-visible event markup.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await assertCompletedManualStatic();
    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "workflow completed timeline mobile",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a claim step offers Claim ticket in Steps to a desk member, and the same route takes the ticket", async ({
    page,
  }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const uniq = Date.now().toString(36).slice(2, 8);
    const deskName = `Steps desk ${uniq}`;
    const catName = `Steps claim ${uniq}`;

    // A desk staffed with the reader: membership is what makes the claim theirs.
    const deskId = await createDeskViaUi(page, deskName);
    await page.goto(base() + `/desks/${deskId}/edit`);
    const editDrawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(editDrawer).toBeVisible();
    await editDrawer.locator("select#desk-member").selectOption({ label: seededCredentials.name });
    await editDrawer.getByRole("button", { name: "Add member" }).click();
    await expect(
      editDrawer.getByRole("listitem").filter({ hasText: seededCredentials.name }),
    ).toHaveCount(1);

    // A category whose first step is the claim this desk answers.
    const catId = await createCategoryInFolder(page, { name: catName });
    await page.goto(base() + `/categories/${catId}/workflow`);
    await page.locator(".workflow-add-popover summary").click();
    await page.getByRole("button", { name: "Send to a desk" }).click();
    const stepCard = page.locator(".workflow-step-card").first();
    await expect(stepCard).toHaveCount(1);
    // The newly added step is the server's selection, so its editor is already
    // open: reading the desk control here cannot race a pending step swap.
    const deskSelect = page.locator('select[name="step_0_desk"]');
    await expect(deskSelect).toBeVisible();
    await deskSelect.selectOption(deskId);
    await expect(deskSelect).toHaveValue(deskId);
    await expect(page.locator('select[name="step_0_strategy"]')).toHaveValue("claim");
    await page.locator('.page-actions button[name="action"][value="save"]').click();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");
    // The saved draft really is routed to the desk this reader belongs to.
    await expect(stepCard.locator(".workflow-step-summary")).toContainText(deskName);
    await page.getByRole("button", { name: /publish/i }).click();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Published");

    const ticketTitle = `Claimable in Steps ${uniq}`;
    const ticketId = await createTicketViaUi(page, {
      title: ticketTitle,
      description: "claim from Steps probe",
      category: catName,
      priority: "high",
    });

    // The member reads the pending claim as theirs: badge, accent highlight and
    // the claim control, with the Assignment panel's own claim still offered.
    await page.goto(base() + `/tickets/${ticketId}`);
    const pending = page.locator("#workflow-pending");
    await expect(pending).toHaveClass(/workflow-pending-action/);
    await expect(pending.locator(".workflow-checklist-badge")).toHaveText("Your desk");
    const currentStep = pending.locator(".workflow-checklist-step.is-current");
    await expect(currentStep.locator(".workflow-checklist-name")).toHaveText(
      `A member of ${deskName} takes it`,
    );
    await expect(currentStep.locator(".workflow-checklist-meta")).toHaveText(
      `${deskName} has been asked to take it`,
    );
    await expect(currentStep.getByRole("button", { name: "Complete" })).toHaveCount(0);
    await expectStepsHighlighted(page);
    await expect(page.getByRole("button", { name: "Assign to me" })).toBeVisible();
    await expectStepsCardBeforeActivity(page);

    await assertHtmxSwap(
      page,
      async () => {
        await currentStep.getByRole("button", { name: "Claim ticket" }).click();
      },
      {
        endpoint: `/tickets/${ticketId}/workflow/steps/1/complete`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );
    await expect(page.locator("#workflow-pending")).toHaveCount(0);
    await expect(page.locator("#timeline")).toContainText(
      `assigned the ticket to ${seededCredentials.name}`,
    );
    await expect(page.locator("#timeline")).toContainText(deskName);
    expect(await currentAssignee(page)).toBe(seededCredentials.name);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "claim from the Steps card",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("the delete confirmation cancels on Escape and returns focus to its trigger", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Delete confirm " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, name);
    const structureURL = page.url();
    await page.goto(structureURL);

    const row = catalogCategoryRow(page, name);
    await expect(row).toHaveCount(1);
    const menu = row.getByRole("button", { name: `Actions for ${name}`, exact: true });
    const focusKey = await menu.getAttribute("data-focus-key");
    expect(focusKey).toBeTruthy();

    await menu.click();
    await row
      .locator(".category-overflow-menu:not([hidden])")
      .getByRole("menuitem", { name: "Delete category", exact: true })
      .click();
    const confirmation = page.locator("#category-delete-dialog");
    await expect(confirmation).toBeVisible();
    await expect(confirmation.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
    // The confirmation replaces the menu, so nothing is left open behind it.
    await expect(row.locator(".category-overflow-menu:not([hidden])")).toHaveCount(0);
    await expect(menu).toHaveAttribute("aria-expanded", "false");

    await page.keyboard.press("Escape");
    await expect(confirmation, "one Escape answers the question").toBeHidden();
    await expect(page.locator(`[data-focus-key="${focusKey}"]`)).toBeFocused();
    await expect(row).toHaveCount(1);

    await menu.click();
    await row
      .locator(".category-overflow-menu:not([hidden])")
      .getByRole("menuitem", { name: "Delete category", exact: true })
      .click();
    await confirmation.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(row).toHaveCount(0);
  });

  test.describe("Add step popover dismissal", () => {
    async function openBuilder(page: Page): Promise<void> {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
      const categoryName = "Popover " + Date.now().toString(36).slice(2, 8);
      await createCategoryViaUi(page, categoryName);
      const category = catalogCategoryRow(page, categoryName);
      await openRowDetails(category);
      await page
        .getByRole("region", { name: categoryName, exact: true })
        .getByRole("link", { name: "Configure workflow" })
        .click();
      await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
    }

    test("Escape closes the Add step popover and returns focus to its trigger", async ({
      page,
    }) => {
      await openBuilder(page);
      const addSummary = page.locator(".workflow-add-popover summary");
      const addOptions = page.locator(".workflow-add-options");

      await addSummary.click();
      await expect(addOptions).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(addOptions).not.toBeVisible();
      await expect(addSummary).toBeFocused();
    });

    test("an outside click closes the Add step popover, option selection still adds a step, and reopening does not stack menus", async ({
      page,
    }) => {
      await openBuilder(page);
      const workflowPath = new URL(page.url()).pathname;
      const addSummary = page.locator(".workflow-add-popover summary");
      const addOptions = page.locator(".workflow-add-options");

      // Choosing an option still works: the popover adds a step and swaps the
      // builder, so the popover is replaced in its closed state.
      await addSummary.click();
      await expect(addOptions).toBeVisible();
      await assertHtmxSwap(
        page,
        () =>
          addOptions.getByRole("button", { name: "Give the agent a task", exact: true }).click(),
        {
          endpoint: (url) => {
            const requestURL = new URL(url);
            return (
              requestURL.pathname === workflowPath &&
              requestURL.searchParams.get("add_step_type") === "manual_task"
            );
          },
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#workflow-builder",
        },
      );
      await expect(page.locator(".workflow-step-card")).toHaveCount(1);
      await expect(addOptions).not.toBeVisible();

      // A click outside the open popover closes it.
      await addSummary.click();
      await expect(addOptions).toBeVisible();
      await page.locator("#workflow-builder-title").click();
      await expect(addOptions).not.toBeVisible();

      // Reopening while another menu is open dismisses that menu instead of
      // stacking two dropdowns, and the reopened popover is repositioned.
      const stepMenu = page.locator(".workflow-step-menu").first();
      const stepMenuBody = stepMenu.locator(".workflow-step-menu-actions");
      await stepMenu.locator("summary").click();
      await expect(stepMenuBody).toBeVisible();
      await addSummary.click();
      await expect(addOptions).toBeVisible();
      await expect(addOptions).toHaveCSS("position", "fixed");
      await expect(stepMenuBody).not.toBeVisible();
      await expect(page.locator("#workflow-builder details[open]")).toHaveCount(1);
    });
  });

  // #251: once the workflow has an ending, the add-step menu used to silently
  // drop the Mark the ticket resolved and Close the ticket kinds. They must stay visible,
  // disabled, and carrying the reason, so an admin can learn the rule.
  test("add-step menu keeps the terminal kinds visible, disabled, and explained once the workflow has an ending", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const categoryId = await createCategoryViaUi(
      page,
      "Ending " + Date.now().toString(36).slice(2, 8),
    );
    await page.goto(base() + `/categories/${categoryId}/workflow`);
    await expect(page.locator("#workflow-builder")).toBeVisible();

    const addOptions = page.locator(".workflow-add-options");
    const option = (label: RegExp) =>
      addOptions.locator("button").filter({ hasText: label }).first();
    const openAddMenu = async () => {
      await page.locator(".workflow-add-step summary").first().click();
      await expect(addOptions).toBeVisible();
    };

    // No ending yet: all five kinds are offered and enabled.
    await openAddMenu();
    await expect(addOptions.locator("button")).toHaveCount(5);
    await expect(option(/^mark the ticket resolved/i)).toBeEnabled();
    await expect(option(/^close the ticket/i)).toBeEnabled();

    // Add a Close the ticket ending through the real menu.
    await option(/^close the ticket/i).click();
    await expect(addOptions).not.toBeVisible();
    await expect(page.locator(".workflow-final-badge")).toHaveCount(1);

    // The ending does not remove the terminal kinds: they stay visible, disabled,
    // and each states why it cannot be added.
    await openAddMenu();
    await expect(addOptions.locator("button")).toHaveCount(5);
    for (const label of [/^mark the ticket resolved/i, /^close the ticket/i]) {
      const terminal = option(label);
      await expect(terminal).toBeVisible();
      await expect(terminal).toBeDisabled();
      await expect(terminal).toContainText("Can't add · this workflow already has an ending");
      // Measured, not looked at: the option uses the repository's disabled
      // convention (.btn[disabled] values), not the browser default.
      await expect(terminal).toHaveCSS("opacity", "0.5");
      await expect(terminal).toHaveCSS("cursor", "not-allowed");
    }
    // Only the terminal kinds are blocked; the other three stay usable and
    // keep their enabled appearance (the disabled rule is scoped to
    // button[disabled]).
    for (const label of [/^give the agent a task/i, /^send to a desk/i, /^ask for information$/i]) {
      await expect(option(label)).toBeEnabled();
      await expect(option(label)).toHaveCSS("opacity", "1");
    }
  });

  // Issue #249: every builder node names who acts and the state outcome it
  // produces, and the summary is rendered in full instead of clipped behind
  // the CSS ellipsis.
  test("workflow builder nodes name their actor and outcome without truncating", async ({
    page,
  }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const categoryId = await createCategoryViaUi(
      page,
      "Nodes " + Date.now().toString(36).slice(2, 8),
    );
    await page.goto(base() + `/categories/${categoryId}/workflow`);
    await expect(page.locator("#workflow-builder")).toBeVisible();

    const addSummary = page.locator(".workflow-add-step summary").first();
    const option = (label: RegExp) =>
      page.locator(".workflow-add-options button").filter({ hasText: label }).first();
    // Longer than the 41-rune cap the server used to cut the summary at.
    const instruction =
      "Provision the account, confirm the welcome email, and schedule the follow-up call";

    // Save a Give the agent a task step whose summary exceeds the old cap.
    await addSummary.click();
    await option(/^give the agent a task/i).click();
    const instructions = page.getByLabel(/instructions/i);
    await expect(instructions).toBeVisible();
    await instructions.fill(instruction);
    await page.locator('.page-actions button[name="action"][value="save"]').click();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // Add the automatic terminal so a state outcome exists in the rail.
    await addSummary.click();
    await option(/^mark the ticket resolved/i).click();
    const terminalCard = page.locator(".workflow-step-card").last();
    await expect(terminalCard.locator(".workflow-step-actor")).toHaveText("Automatic");
    await expect(terminalCard.locator(".workflow-step-outcome")).toHaveText("→ Resolved");

    // Reload so the assertions read the persisted draft, not a transient swap.
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible();

    const manualCard = page.locator(".workflow-step-card").first();
    await expect(manualCard.locator(".workflow-step-actor")).toHaveText("Assignee");
    await expect(manualCard.locator(".workflow-step-summary")).toHaveText(instruction);
    await expect(terminalCard.locator(".workflow-step-actor")).toHaveText("Automatic");
    await expect(terminalCard.locator(".workflow-step-outcome")).toHaveText("→ Resolved");

    // Measured, not looked at: the summary must not draw an ellipsis, must not
    // forbid wrapping, and its box must show everything it holds.
    const summary = manualCard.locator(".workflow-step-summary");
    const box = await summary.evaluate((el) => {
      const style = getComputedStyle(el);
      return {
        textOverflow: style.textOverflow,
        whiteSpace: style.whiteSpace,
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
      };
    });
    expect(box.textOverflow).not.toBe("ellipsis");
    expect(box.whiteSpace).not.toBe("nowrap");
    expect(box.scrollWidth).toBeLessThanOrEqual(box.clientWidth + 1);
    await assertNoHorizontalOverflow(page, 1280);
  });

  test("a checkbox field renders a Must-be-ticked required control and persists it", async ({
    page,
  }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const categoryId = await createCategoryViaUi(
      page,
      "Checkbox field " + Date.now().toString(36).slice(2, 8),
    );
    await page.goto(base() + `/categories/${categoryId}/workflow`);
    await expect(page.locator("#workflow-builder")).toBeVisible();

    // Add an Ask-for-information step and give it one field.
    await page.locator(".workflow-add-step summary").first().click();
    await page
      .locator(".workflow-add-options button")
      .filter({ hasText: /^ask for information$/i })
      .first()
      .click();
    await page.getByRole("button", { name: "+ Add field" }).click();
    await expect(page.locator(".workflow-field-row")).toHaveCount(1);

    // Author the field as a checkbox carrying the acknowledgment constraint.
    // The field-kind control's own live re-render is a pre-existing interaction
    // outside #317's surfaces, so this journey sets the kind and saves; the
    // visible copy is asserted from the persisted draft after the reload.
    await page.evaluate(() => {
      const select = document.querySelector('select[name="step_0_field_0_kind"]');
      if (!(select instanceof HTMLSelectElement)) throw new Error("Missing kind select");
      select.value = "checkbox";
    });
    await page.locator('input[name="step_0_field_0_required"]').check();
    await expect(page.locator('select[name="step_0_field_0_kind"]')).toHaveValue("checkbox");
    await expect(page.locator('input[name="step_0_field_0_required"]')).toBeChecked();
    // Feedback may already say Saved while this request is still pending.
    const nextSave = page.waitForRequest(
      (request) =>
        request.method() === "POST" &&
        new URL(request.url()).pathname === `/categories/${categoryId}/workflow` &&
        new URLSearchParams(request.postData() || "").get("action") === "save",
    );
    await page.locator('.page-actions button[name="action"][value="save"]').click();
    const saveRequest = await nextSave;
    const response = await saveRequest.response();
    expect(response?.status()).toBe(200);
    expect(await response?.finished()).toBeNull();
    const fields = new URLSearchParams(saveRequest.postData() || "");
    expect({
      kind: fields.get("step_0_field_0_kind"),
      required: fields.get("step_0_field_0_required"),
    }).toEqual({ kind: "checkbox", required: "on" });
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // Reload and re-select the step: the persisted draft renders "Must be
    // ticked" (not the bare "Required") and keeps the control ticked.
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible();
    await expect(page.locator('select[name="step_0_field_0_kind"]')).toHaveValue("checkbox");
    const selected = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        new URL(response.url()).pathname === `/categories/${categoryId}/workflow` &&
        (new URLSearchParams(response.request().postData() || "").get("action") ||
          new URL(response.url()).searchParams.get("action")) === "select_step",
    );
    await page.locator(".workflow-step-card .workflow-step-card-link").first().click();
    const selection = await selected;
    expect(selection.status()).toBe(200);
    expect(await selection.finished()).toBeNull();
    await expect(page.getByLabel("Must be ticked")).toBeVisible();
    await expect(page.locator('input[name="step_0_field_0_required"]')).toBeChecked();
  });

  test("single-select options are edited one per line and keep semicolons", async ({ page }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const categoryId = await createCategoryViaUi(
      page,
      "Single select " + Date.now().toString(36).slice(2, 8),
    );
    const stepPath = `/categories/${categoryId}/workflow?selected_step_index=0`;
    await page.goto(base() + `/categories/${categoryId}/workflow`);
    await expect(page.locator("#workflow-builder")).toBeVisible();

    // Add an Ask-for-information step and give it one field.
    await page.locator(".workflow-add-step summary").first().click();
    await page
      .locator(".workflow-add-options button")
      .filter({ hasText: /^ask for information$/i })
      .first()
      .click();
    await page.getByRole("button", { name: "+ Add field" }).click();
    await expect(page.locator(".workflow-field-row")).toHaveCount(1);

    // The field-kind control's own live re-render is a pre-existing interaction
    // outside this change, so this journey sets the kind and saves; the options
    // editor appears from the persisted draft on the next load.
    await page.evaluate(() => {
      const select = document.querySelector('select[name="step_0_field_0_kind"]');
      if (!(select instanceof HTMLSelectElement)) throw new Error("Missing kind select");
      select.value = "single_select";
    });
    await page.locator('.page-actions button[name="action"][value="save"]').click();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // The single-select editor is a one-per-line textarea with the new help copy.
    await page.goto(base() + stepPath);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    const options = page.locator('textarea[name="step_0_field_0_options"]');
    await expect(options).toBeVisible();
    await expect(page.locator(".workflow-field-options .help")).toHaveText("One option per line.");
    // A semicolon is now an ordinary option character, not a separator.
    await options.fill("North; South\nBuenos Aires, Argentina");
    await expect(options).toHaveValue("North; South\nBuenos Aires, Argentina");
    await page.locator('.page-actions button[name="action"][value="save"]').click();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // Reload: the stored option keeps its semicolon, one per line, and the
    // forbidden-delimiter copy is gone.
    await page.goto(base() + stepPath);
    await expect(page.locator("#workflow-builder")).toBeVisible();
    const reloaded = page.locator('textarea[name="step_0_field_0_options"]');
    await expect(reloaded).toBeVisible();
    await expect(reloaded).toHaveValue("North; South\nBuenos Aires, Argentina");
    await expect(page.locator(".workflow-field-options .help")).toHaveText("One option per line.");
    await expect(page.locator("#workflow-builder")).not.toContainText("Semicolons cannot be used");
  });

  test.describe("Workflow dirty structural guard", () => {
    function trackPosts(page: Page): string[] {
      const actions: string[] = [];
      page.on("request", (request) => {
        if (
          request.method() !== "POST" ||
          !/\/categories\/\d+\/workflow/.test(new URL(request.url()).pathname)
        )
          return;
        actions.push(request.postData()?.match(/(?:^|&)action=([^&]*)/)?.[1] ?? "");
      });
      return actions;
    }
    async function seedWorkflow(page: Page, steps: string[]): Promise<string> {
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(base() + "/categories/new");
      if (page.url().includes("/login")) await loginAsSeeded(page);
      const categoryId = await createCategoryViaUi(
        page,
        "Guard " + Date.now().toString(36) + steps.length,
      );
      await page.goto(base() + `/categories/${categoryId}/workflow`);
      await expect(page.locator("#workflow-builder")).toBeVisible();
      for (const step of steps) {
        await page.locator(".workflow-add-step summary").first().click();
        const filter =
          step === "manual_task"
            ? /give the agent a task/i
            : step === "form"
              ? /^ask for information$/i
              : step === "close_ticket"
                ? /close the ticket/i
                : /mark the ticket resolved/i;
        await page
          .locator(".workflow-add-options button")
          .filter({ hasText: filter })
          .first()
          .click();
        await expect(page.locator(".workflow-add-options")).not.toBeVisible();
      }
      return categoryId;
    }
    async function selectCard(page: Page, index: number): Promise<void> {
      const responsePromise = page.waitForResponse(
        (r) =>
          r.request().method() === "POST" &&
          r.url().includes("/workflow") &&
          (r.request().postData() ?? "").includes("action=select_step"),
      );
      await page.locator(".workflow-step-card .workflow-step-card-link").nth(index).click();
      await expect((await responsePromise).status()).toBe(200);
      await page.waitForTimeout(200);
    }
    async function openMenu(menu: ReturnType<Page["locator"]>): Promise<void> {
      if (await menu.isVisible().catch(() => false)) return;
      await menu.locator("..").locator("summary").click();
    }
    const triggers: Record<string, (page: Page, categoryId: string) => Promise<void>> = {
      add_step: async (page) => {
        const manualButton = page
          .locator(".workflow-add-options button")
          .filter({ hasText: /give the agent a task/i })
          .first();
        if (!(await manualButton.isVisible().catch(() => false)))
          await page.locator(".workflow-add-step summary").first().click();
        await manualButton.click();
      },
      remove_step: async (page) => {
        const card = page.locator(".workflow-step-card").last();
        await openMenu(card.locator(".workflow-step-menu-actions"));
        await card.getByRole("button", { name: /remove step/i }).click();
        await page
          .locator("#workflow-remove-step-dialog")
          .getByRole("button", { name: "Remove step" })
          .click();
      },
      move_up: async (page) => {
        const card = page.locator(".workflow-step-card").nth(1);
        await openMenu(card.locator(".workflow-step-menu-actions"));
        await card.getByRole("button", { name: /move up/i }).click();
      },
      move_down: async (page) => {
        const card = page.locator(".workflow-step-card").first();
        await openMenu(card.locator(".workflow-step-menu-actions"));
        await card.getByRole("button", { name: /move down/i }).click();
      },
      reorder: async (page) => {
        // The drag listeners funnel the drop into the hidden reorder submitter.
        // The sequence is vertical (issue #395, option A), so the pointer crosses
        // the second card's lower half on the Y axis.
        await page.evaluate(() => {
          const transfer = new DataTransfer();
          const handle = document.querySelectorAll(".workflow-drag-handle")[0];
          const target = document.querySelectorAll(".workflow-step-card")[1];
          const at = {
            bubbles: true,
            dataTransfer: transfer,
            clientY: target.getBoundingClientRect().bottom - 10,
          };
          handle.dispatchEvent(
            new DragEvent("dragstart", { bubbles: true, dataTransfer: transfer }),
          );
          target.dispatchEvent(new DragEvent("dragover", at));
          target.dispatchEvent(new DragEvent("drop", at));
        });
      },
      add_field: async (page) => {
        await page.getByRole("button", { name: "+ Add field" }).click();
      },
      remove_field: async (page) => {
        await openMenu(page.locator(".workflow-field-menu-actions"));
        await page.locator(".workflow-field-menu-actions button").click();
        await page
          .locator("#workflow-remove-field-dialog")
          .getByRole("button", { name: "Remove field" })
          .click();
      },
      change_type: async (page, categoryId) => {
        // change_type's Apply is no-JS-only; htmx carries the action of the last
        // clicked submit button, so a clicked change_type submitter (actual
        // submit grammar, non-rendered control) proves the request-level guard.
        await page.evaluate((id) => {
          const form = document.querySelector("#workflow-form");
          const submitter = document.createElement("button");
          submitter.type = "submit";
          submitter.name = "action";
          submitter.value = "change_type";
          submitter.hidden = true;
          submitter.setAttribute("formaction", `/categories/${id}/workflow?step_index=1`);
          form.append(submitter);
          submitter.click();
        }, categoryId);
      },
    };
    const fixtures: Array<{
      action: string;
      steps: string[];
      select: number;
      dirty: (page: Page) => Promise<void>;
    }> = [
      {
        action: "add_step",
        steps: ["manual_task", "form"],
        select: 0,
        dirty: async (page) => {
          await page.getByLabel(/^instructions/i).fill("DIRTY-add_step");
        },
      },
      {
        action: "remove_step",
        steps: ["manual_task", "form"],
        select: 0,
        dirty: async (page) => {
          await page.getByLabel(/^instructions/i).fill("DIRTY-remove_step");
        },
      },
      {
        action: "move_up",
        steps: ["manual_task", "form"],
        select: 0,
        dirty: async (page) => {
          await page.getByLabel(/^instructions/i).fill("DIRTY-move_up");
        },
      },
      {
        action: "move_down",
        steps: ["manual_task", "form"],
        select: 0,
        dirty: async (page) => {
          await page.getByLabel(/^instructions/i).fill("DIRTY-move_down");
        },
      },
      {
        action: "reorder",
        steps: ["manual_task", "form"],
        select: 0,
        dirty: async (page) => {
          await page.getByLabel(/^instructions/i).fill("DIRTY-reorder");
        },
      },
      {
        action: "add_field",
        steps: ["form"],
        select: 0,
        dirty: async (page) => {
          await page.locator(".workflow-field-required input").check();
        },
      },
      {
        action: "remove_field",
        steps: ["form"],
        select: 0,
        dirty: async (page) => {
          await page.getByLabel(/^label$/i).fill("DIRTY-remove_field");
        },
      },
      {
        action: "change_type",
        steps: ["manual_task", "close_ticket"],
        select: 1,
        dirty: async (page) => {
          await page.getByLabel(/^final type$/i).selectOption("resolve_ticket");
        },
      },
    ];

    test("Remove step and Remove field ask through a dialog first: Cancel and Escape persist nothing, confirm posts once", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      const categoryId = await seedWorkflow(page, ["form"]);
      await triggers.add_field(page, categoryId);
      await expect(page.locator(".workflow-field-row")).toHaveCount(1);
      const posts = trackPosts(page);
      const stepDialog = page.locator("#workflow-remove-step-dialog");
      const fieldDialog = page.locator("#workflow-remove-field-dialog");
      const cards = page.locator(".workflow-step-card");

      // Remove field: the menu click opens the field dialog and removes nothing.
      const removeField = page.locator(".workflow-field-menu-actions button").first();
      await openMenu(page.locator(".workflow-field-menu-actions"));
      await removeField.click();
      await expect(fieldDialog).toBeVisible();
      await expect(stepDialog).not.toBeVisible();
      await expect(fieldDialog.getByRole("heading", { name: "Remove this field?" })).toBeVisible();
      await expect(fieldDialog).toContainText(/removed from this step/i);
      await expect(fieldDialog.getByRole("button", { name: "Remove field" })).toHaveCSS(
        "background-color",
        "rgb(180, 35, 24)",
      );
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "remove_field")).toEqual([]);
      await expect(page.locator(".workflow-field-row")).toHaveCount(1);

      // Cancel restores focus to the trigger and persists nothing.
      await fieldDialog.getByRole("button", { name: "Cancel" }).click();
      await expect(fieldDialog).not.toBeVisible();
      await expect(removeField).toBeFocused();
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "remove_field")).toEqual([]);
      await expect(page.locator(".workflow-field-row")).toHaveCount(1);

      // Escape behaves like Cancel, with focus restored to the trigger.
      await removeField.click();
      await expect(fieldDialog).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(fieldDialog).not.toBeVisible();
      await expect(removeField).toBeFocused();
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "remove_field")).toEqual([]);
      await expect(page.locator(".workflow-field-row")).toHaveCount(1);

      // Confirm posts the same remove_field request exactly once.
      await removeField.click();
      await expect(fieldDialog).toBeVisible();
      await assertHtmxSwap(
        page,
        async () => {
          await fieldDialog.getByRole("button", { name: "Remove field" }).click();
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === `/categories/${categoryId}/workflow` &&
              parsedURL.searchParams.get("field_index") === "0" &&
              !parsedURL.searchParams.has("action")
            );
          },
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#workflow-builder",
        },
      );
      await expect(fieldDialog).not.toBeVisible();
      await expect(page.locator(".workflow-field-row")).toHaveCount(0);
      expect(posts.filter((a) => a === "remove_field")).toEqual(["remove_field"]);

      // Remove step: a distinct question about the step and its fields, and it
      // too persists nothing until confirmed.
      await selectCard(page, 0);
      const stepCount = await cards.count();
      const removeStep = cards.last().getByRole("button", { name: /remove step/i });
      await openMenu(cards.last().locator(".workflow-step-menu-actions"));
      await removeStep.click();
      await expect(stepDialog).toBeVisible();
      await expect(fieldDialog).not.toBeVisible();
      await expect(stepDialog.getByRole("heading", { name: "Remove this step?" })).toBeVisible();
      await expect(stepDialog).toContainText(/fields/i);
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "remove_step")).toEqual([]);
      await expect(cards).toHaveCount(stepCount);
      await stepDialog.getByRole("button", { name: "Cancel" }).click();
      await expect(stepDialog).not.toBeVisible();
      await expect(removeStep).toBeFocused();
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "remove_step")).toEqual([]);
      await expect(cards).toHaveCount(stepCount);

      // Confirm posts the same remove_step request exactly once.
      await removeStep.click();
      await expect(stepDialog).toBeVisible();
      await assertHtmxSwap(
        page,
        async () => {
          await stepDialog.getByRole("button", { name: "Remove step" }).click();
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === `/categories/${categoryId}/workflow` &&
              parsedURL.searchParams.get("step_index") === String(stepCount - 1) &&
              !parsedURL.searchParams.has("action")
            );
          },
          method: "POST",
          expectedStatus: 200,
          hxTarget: "#workflow-builder",
        },
      );
      await expect(cards).toHaveCount(stepCount - 1);
      expect(posts.filter((a) => a === "remove_step")).toEqual(["remove_step"]);
    });

    test("all 8 structural actions prompt when dirty and Cancel sends no POST", async ({
      page,
    }) => {
      test.setTimeout(240_000);
      for (const fixture of fixtures) {
        const categoryId = await seedWorkflow(page, fixture.steps);
        if (["add_field", "remove_field"].includes(fixture.action)) {
          await triggers.add_field(page, categoryId);
          await expect(page.locator(".workflow-field-row")).toHaveCount(1);
        }
        await selectCard(page, fixture.select);
        const posts = trackPosts(page);
        const dialog = page.locator("#workflow-dirty-dialog");
        await fixture.dirty(page);
        await triggers[fixture.action](page, categoryId);
        await expect(dialog).toBeVisible();
        for (const choice of ["Save and continue", "Discard and continue", "Cancel"]) {
          await expect(dialog.getByRole("button", { name: choice })).toBeVisible();
        }
        await page.waitForTimeout(300);
        expect(posts.filter((a) => a === fixture.action)).toEqual([]);
        await dialog.getByRole("button", { name: "Cancel" }).click();
        await expect(dialog).not.toBeVisible();
        await page.waitForTimeout(300);
        expect(posts.filter((a) => a === fixture.action)).toEqual([]);
      }
    });

    test("Save and continue persists first, then the action runs once", async ({ page }) => {
      const categoryId = await seedWorkflow(page, ["manual_task"]);
      await selectCard(page, 0);
      const posts = trackPosts(page);
      const dialog = page.locator("#workflow-dirty-dialog");
      await page.getByLabel(/^instructions/i).fill("SAVED-FIRST");
      await triggers.add_step(page, categoryId);
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Save and continue" }).click();
      await expect.poll(() => posts).toEqual(["save", "add_step"]);
      await expect(page.locator(".workflow-step-card")).toHaveCount(2);
      await page.reload();
      await selectCard(page, 0);
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("SAVED-FIRST");
    });

    test("carried dirty survives select_step and Discard+continue restores persisted values", async ({
      page,
    }) => {
      const categoryId = await seedWorkflow(page, ["manual_task", "form"]);
      await triggers.add_field(page, categoryId);
      await expect(page.locator(".workflow-field-row")).toHaveCount(1);
      await selectCard(page, 0);
      const posts = trackPosts(page);
      const dialog = page.locator("#workflow-dirty-dialog");
      await page.getByLabel(/^instructions/i).fill("CARRIED-EDIT");
      await selectCard(page, 1);
      await expect(page.getByLabel(/^label$/i)).toHaveValue("");
      await triggers.reorder(page, categoryId);
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Discard and continue" }).click();
      await expect.poll(() => posts).toEqual(["select_step", "reorder"]);
      await expect(page.locator(".workflow-step-card").first()).toContainText(
        /ask for information/i,
      );
      await page.reload();
      await expect(page.locator(".workflow-step-card").first()).toContainText(
        /ask for information/i,
      );
      await selectCard(page, 1);
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("");
    });

    test("Escape restores focus and the dialog is styled at 390px", async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      await seedWorkflow(page, ["manual_task"]);
      await selectCard(page, 0);
      const dialog = page.locator("#workflow-dirty-dialog");
      await page.getByLabel(/^instructions/i).fill("DIRTY-escape");
      const addManual = page
        .locator(".workflow-add-options button")
        .filter({ hasText: /give the agent a task/i })
        .first();
      await page.locator(".workflow-add-step summary").first().click();
      await addManual.click();
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveCSS("border-radius", "12px");
      await expect(dialog.getByRole("button", { name: "Discard and continue" })).toHaveCSS(
        "background-color",
        "rgb(180, 35, 24)",
      );
      await page.keyboard.press("Escape");
      await expect(dialog).not.toBeVisible();
      await expect(addManual).toBeFocused();
    });

    test("a failed save stays dirty and never replays the action", async ({ page }) => {
      await seedWorkflow(page, ["manual_task"]);
      await selectCard(page, 0);
      await page.route("**/workflow", async (route) => {
        const save =
          route.request().method() === "POST" &&
          (route.request().postData() ?? "").includes("action=save");
        return save ? route.abort() : route.continue();
      });
      const posts = trackPosts(page);
      const dialog = page.locator("#workflow-dirty-dialog");
      await page.getByLabel(/^instructions/i).fill("KEPT-DIRTY");
      await triggers.add_step(page, "0");
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Save and continue" }).click();
      await expect(dialog).not.toBeVisible();
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "add_step")).toEqual([]);
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("KEPT-DIRTY");
      await triggers.add_step(page, "0");
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Cancel" }).click();
      await page.waitForTimeout(300);
      expect(posts.filter((a) => a === "add_step")).toEqual([]);
    });
  });

  test.describe("Workflow exit guards", () => {
    async function openDirtyBuilder(page: Page, instructions: string): Promise<string> {
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(base() + "/categories/new");
      if (page.url().includes("/login")) await loginAsSeeded(page);
      const categoryId = await createCategoryViaUi(page, "Exit " + Date.now().toString(36));
      await page.goto(base() + `/categories/${categoryId}/workflow`);
      await expect(page.locator("#workflow-builder")).toBeVisible();
      await page.locator(".workflow-add-step summary").first().click();
      await page
        .locator(".workflow-add-options button")
        .filter({ hasText: /give the agent a task/i })
        .first()
        .click();
      await expect(page.getByLabel(/^instructions/i)).toBeVisible();
      await page.getByLabel(/^instructions/i).fill(instructions);
      return categoryId;
    }

    test("dirty in-app link: Stay keeps edits, Escape stays, Discard leaves without persisting (390px)", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      const categoryId = await openDirtyBuilder(page, "EXIT-DIRTY");
      const dialog = page.locator("#workflow-leave-dialog");
      const sidebarLink = page.locator('a.sidebar-link[href="/categories"]');
      const breadcrumbLink = page.locator(".page-breadcrumb a");
      const workflowUrl = new RegExp(`/categories/${categoryId}/workflow$`);
      // Stay: URL and values preserved, focus returns to the trigger.
      await sidebarLink.click();
      await expect(dialog).toBeVisible();
      await expect(page).toHaveURL(workflowUrl);
      await dialog.getByRole("button", { name: "Stay" }).click();
      await expect(dialog).not.toBeVisible();
      await expect(sidebarLink).toBeFocused();
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("EXIT-DIRTY");
      await expect(page).toHaveURL(workflowUrl);
      // Escape means Stay too, and the dialog reuses the product dialog styling.
      await breadcrumbLink.click();
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveCSS("border-radius", "12px");
      await expect(dialog.getByRole("button", { name: "Discard changes" })).toHaveCSS(
        "background-color",
        "rgb(180, 35, 24)",
      );
      await page.keyboard.press("Escape");
      await expect(dialog).not.toBeVisible();
      await expect(breadcrumbLink).toBeFocused();
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("EXIT-DIRTY");
      // Discard navigates without persisting anything.
      await breadcrumbLink.click();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Discard changes" }).click();
      await expect(page).not.toHaveURL(/\/workflow/);
      await page.goto(base() + `/categories/${categoryId}/workflow`);
      await expect(page.locator(".workflow-step-card")).toHaveCount(1);
      await page.locator(".workflow-step-card .workflow-step-card-link").first().click();
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("");
    });

    test("dirty browser Back: Stay remains on builder, Discard performs exactly one back", async ({
      page,
    }) => {
      const categoryId = await openDirtyBuilder(page, "BACK-DIRTY");
      const dialog = page.locator("#workflow-leave-dialog");
      const workflowUrl = new RegExp(`/categories/${categoryId}/workflow$`);
      await page.goBack();
      await expect(dialog).toBeVisible();
      await expect(page).toHaveURL(workflowUrl);
      await dialog.getByRole("button", { name: "Stay" }).click();
      await expect(dialog).not.toBeVisible();
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("BACK-DIRTY");
      await expect(page).toHaveURL(workflowUrl);
      await page.goBack();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Discard changes" }).click();
      // Exactly one back: the immediate previous entry, with no second prompt.
      await expect(page).toHaveURL(/\/categories(\?.*)?$/);
      await expect(dialog).not.toBeVisible();
    });

    test("reload while dirty uses native beforeunload only, never the in-app dialog", async ({
      page,
    }) => {
      await openDirtyBuilder(page, "RELOAD-DIRTY");
      const dialogTypes: string[] = [];
      page.on("dialog", (nativeDialog) => {
        dialogTypes.push(nativeDialog.type());
        return nativeDialog.accept();
      });
      await page.reload();
      await expect(page.locator("#workflow-builder")).toBeVisible();
      await expect(page.locator("#workflow-leave-dialog")).not.toBeVisible();
      expect(dialogTypes).toEqual(["beforeunload"]);
      // The reloaded page is clean: a second reload must not prompt at all.
      await page.reload();
      await expect(page.locator("#workflow-builder")).toBeVisible();
      expect(dialogTypes).toEqual(["beforeunload"]);
    });

    test("reverted edits and a successful save clear the exit guard", async ({ page }) => {
      const categoryId = await openDirtyBuilder(page, "CLEAN-ME");
      const dialog = page.locator("#workflow-leave-dialog");
      const sidebarLink = page.locator('a.sidebar-link[href="/categories"]');
      // Guard is active while dirty, then a reverted field navigates freely.
      await sidebarLink.click();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Stay" }).click();
      await expect(dialog).not.toBeVisible();
      await page.getByLabel(/^instructions/i).fill("");
      await sidebarLink.click();
      await expect(page).toHaveURL(/\/categories(\?.*)?$/);
      await expect(dialog).not.toBeVisible();
      // Still dirty before saving, cleared after a successful save.
      await page.goto(base() + `/categories/${categoryId}/workflow`);
      await expect(page.locator("#workflow-builder")).toBeVisible();
      const selectResponse = page.waitForResponse(
        (r) =>
          r.request().method() === "POST" &&
          r.url().includes("/workflow") &&
          (r.request().postData() ?? "").includes("action=select_step"),
      );
      await page.locator(".workflow-step-card .workflow-step-card-link").first().click();
      await expect((await selectResponse).status()).toBe(200);
      await page.waitForTimeout(200);
      await expect(page.getByLabel(/^instructions/i)).toBeVisible();
      await page.getByLabel(/^instructions/i).fill("SAVED-EXIT");
      await sidebarLink.click();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Stay" }).click();
      await expect(dialog).not.toBeVisible();
      const saveResponse = page.waitForResponse(
        (r) =>
          r.request().method() === "POST" &&
          r.url().includes("/workflow") &&
          (r.request().postData() ?? "").includes("action=save"),
      );
      await page.locator('.page-actions button[name="action"][value="save"]').click();
      await expect((await saveResponse).status()).toBe(200);
      await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");
      await sidebarLink.click();
      await expect(page).toHaveURL(/\/categories(\?.*)?$/);
      await expect(dialog).not.toBeVisible();
    });

    test("failed publish keeps invalid edits dirty without false success", async ({ page }) => {
      const categoryId = await openDirtyBuilder(page, "   ");
      const publishResponse = page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          response.url().includes(`/categories/${categoryId}/workflow`) &&
          (response.request().postData() ?? "").includes("action=publish"),
      );
      await page.getByRole("button", { name: "Publish" }).click();
      await expect((await publishResponse).status()).toBe(422);
      await expect(page.getByRole("alert")).toBeVisible();
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("   ");
      await expect(page.locator("[data-workflow-live]")).toHaveCount(0);
      await page.locator(".page-breadcrumb a").click();
      await expect(page.locator("#workflow-leave-dialog")).toBeVisible();
      await expect(page).toHaveURL(new RegExp(`/categories/${categoryId}/workflow$`));
    });
  });

  // Issue #395, option A: the approved composition puts the whole step sequence
  // in a vertical list beside the selected step's editor on a wide viewport, and
  // stacks the sequence above the editor on a phone. Go freezes the markup and
  // the stylesheet; only a browser can prove the two panes actually land in
  // those positions, so this journey measures them.
  test("workflow builder keeps the step sequence beside the editor and stacks it on a phone (issue #395)", async ({
    page,
  }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const categoryId = await createCategoryViaUi(
      page,
      "Lateral " + Date.now().toString(36).slice(2, 8),
    );
    const workflowPath = `/categories/${categoryId}/workflow`;
    await page.goto(base() + workflowPath);
    await expect(page.locator("#workflow-builder")).toBeVisible();

    // Two steps make the vertical ordering observable.
    for (let i = 0; i < 2; i++) {
      await page.locator(".workflow-add-step summary").first().click();
      await page
        .locator(".workflow-add-options button")
        .filter({ hasText: "Give the agent a task" })
        .first()
        .click();
      await expect(page.locator(".workflow-add-options")).not.toBeVisible();
    }

    const rail = page.locator(".workflow-rail-wrap");
    const editor = page.locator(".workflow-editor-panel");
    await expect(rail).toBeVisible();
    await expect(editor).toBeVisible();
    await expect(page.locator(".workflow-pane-count")).toHaveCount(0);
    await expect(page.locator("#workflow-builder")).toHaveCSS("border-left-width", "0px");
    await expect(page.locator("#theme")).toHaveCount(0);
    for (const colorScheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme });
      const selectedStyle = await page.locator(".workflow-step-card.selected").evaluate((el) => {
        const probe = document.createElement("span");
        probe.style.color = "var(--accent)";
        probe.style.backgroundColor = "var(--accent-soft)";
        document.body.appendChild(probe);
        const actual = getComputedStyle(el);
        const token = getComputedStyle(probe);
        const result = {
          border: actual.borderTopColor,
          background: actual.backgroundColor,
          accent: token.color,
          soft: token.backgroundColor,
        };
        probe.remove();
        return result;
      });
      expect(selectedStyle.border).toBe(selectedStyle.accent);
      expect(selectedStyle.background).toBe(selectedStyle.soft);
    }
    await page.emulateMedia({ colorScheme: "light" });

    const railBox = await rail.boundingBox();
    const editorBox = await editor.boundingBox();
    if (!railBox || !editorBox) throw new Error("both builder panes must have a box");
    expect(
      railBox.x + railBox.width,
      "the sequence must sit left of the editor",
    ).toBeLessThanOrEqual(editorBox.x);
    expect(Math.abs(railBox.y - editorBox.y), "both panes must share a row").toBeLessThan(40);

    // The sequence itself reads top to bottom: the second step starts below the
    // first, and both start at the same left edge.
    const cards = page.locator(".workflow-step-card");
    await expect(cards).toHaveCount(2);
    const first = await cards.nth(0).boundingBox();
    const second = await cards.nth(1).boundingBox();
    if (!first || !second) throw new Error("both step cards must have a box");
    expect(second.y, "the second step must sit below the first").toBeGreaterThan(
      first.y + first.height - 1,
    );
    expect(Math.abs(first.x - second.x), "the sequence must share a left edge").toBeLessThan(2);

    // The published/draft facts and the clone control share the version bar
    // above both panes.
    const versionBar = page.locator(".workflow-version-bar");
    await expect(versionBar).toBeVisible();
    const versionBox = await versionBar.boundingBox();
    if (!versionBox) throw new Error("the version bar must have a box");
    expect(versionBox.y + versionBox.height).toBeLessThanOrEqual(railBox.y);
    await expect(versionBar.locator(".workflow-clone")).toBeVisible();

    // Choosing a clone source is not an edit of this workflow: it must not arm
    // the unsaved-changes guard.
    await versionBar.locator(".workflow-clone summary").click();
    const cloneOptions = page.locator("#clone-source option:not([value=''])");
    if ((await cloneOptions.count()) > 0) {
      await page.locator("#clone-source").selectOption({ index: 1 });
      await page.keyboard.press("Escape");
      // Choosing a source must not arm the unsaved-changes guard: the link leaves
      // the builder with no dialog, exactly as it would with nothing edited.
      await page.locator(".page-breadcrumb a").click();
      await expect(page.locator("#workflow-leave-dialog")).not.toBeVisible();
      await expect(page).toHaveURL(/\/categories$/);
      await page.goto(base() + workflowPath);
      await expect(page.locator("#workflow-builder")).toBeVisible();
    } else {
      await page.keyboard.press("Escape");
    }

    // A phone stacks the same two panes: sequence first, editor after it, and
    // nothing spills sideways from the page.
    await page.setViewportSize({ width: 390, height: 844 });
    const stackedRail = await rail.boundingBox();
    const stackedEditor = await editor.boundingBox();
    if (!stackedRail || !stackedEditor) throw new Error("both panes must keep a box at 390px");
    expect(
      stackedEditor.y,
      "on a phone the editor must follow the sequence",
    ).toBeGreaterThanOrEqual(stackedRail.y + stackedRail.height - 1);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow, "the builder must not overflow the page at 390px").toBeLessThanOrEqual(0);

    await page.setViewportSize({ width: 1280, height: 800 });
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "workflow lateral layout",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("category SLA screen shows the materialized matrix and persists an edited target", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");

    // Resolve the exact category under test in the tree. Earlier journeys in
    // this file rename the seeded desk, so nothing here depends on desk names:
    // the category row carries its own id (issue #387 replaced the separate
    // results list with the filtered tree).
    const categoryName = "General";
    const search = page.getByRole("searchbox", {
      name: "Search folders and categories",
    });
    await search.fill(categoryName);
    await search.press("Enter");
    await expect(catalogCategoryRow(page, categoryName)).toHaveCount(1);

    const rowSelector = "the category row";
    const categoryRow = catalogCategoryRow(page, categoryName);
    if ((await categoryRow.count()) !== 1) {
      throw new Error(
        `Expected exactly one category "${categoryName}" using ${rowSelector} at ${page.url()}, found ${await categoryRow.count()}`,
      );
    }

    const categoryId = await catalogNodeId(categoryRow, `category ${categoryName}`);

    // The details popover's Configure SLA link points at THIS category.
    const slaPath = `/categories/${categoryId}/sla`;
    await openRowDetails(categoryRow);
    const configureSla = page
      .getByRole("region", { name: categoryName, exact: true })
      .getByRole("link", { name: "Configure SLA", exact: true });
    await expect(configureSla).toHaveAttribute("href", slaPath);
    await configureSla.click();

    await expect(page).toHaveURL(new RegExp(`${slaPath}$`));
    await expect(page.locator("h1")).toHaveText("Category SLA");
    await expect(page.locator(".page-breadcrumb")).toContainText(categoryName);

    // Compact inputs keep all eight exact targets; Settings retains h/m/s.
    const gridRows = page.locator(".sla-grid-row:not(.sla-grid-head)");
    await expect(gridRows).toHaveCount(4);
    await expect(page.locator(".sla-duration-input")).toHaveCount(8);
    const seededMatrix = [
      { priority: "Critical", firstResponse: "30m", resolve: "4h" },
      { priority: "High", firstResponse: "1h", resolve: "8h" },
      { priority: "Medium", firstResponse: "4h", resolve: "24h" },
      { priority: "Low", firstResponse: "8h", resolve: "72h" },
    ];
    for (const [index, row] of seededMatrix.entries()) {
      await expect(gridRows.nth(index).locator(".sla-grid-priority")).toHaveText(row.priority);
      await expect(page.getByLabel(`${row.priority} first response`, { exact: true })).toHaveValue(
        row.firstResponse,
      );
      await expect(page.getByLabel(`${row.priority} resolve`, { exact: true })).toHaveValue(
        row.resolve,
      );
    }
    const resolve = page.getByLabel("Medium resolve", { exact: true });
    await resolve.fill("1h 2m 3s");
    await expect(page.locator("#resolve_duration_medium-reading")).toHaveText("1 h 2 min 3 s");
    let posts = 0;
    const countPost = (request: import("@playwright/test").Request) => {
      if (new URL(request.url()).pathname === slaPath && request.method() === "POST") posts++;
    };
    page.on("request", countPost);
    await resolve.fill("1h junk");
    await expect(resolve).toHaveAttribute("aria-invalid", "true");
    await page.getByRole("button", { name: "Save SLA targets" }).click();
    expect(posts).toBe(0);
    await resolve.fill("59s");
    await expect(page.locator("#resolve_duration_medium-reading")).toHaveText(
      "The target must be at least 60 seconds.",
    );
    await resolve.fill("1h 2m 3s");
    await page.getByRole("button", { name: "Save SLA targets" }).click();
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    expect(posts).toBe(1);
    page.off("request", countPost);
    await page.reload();
    await expect(resolve).toHaveValue("1h 2m 3s");
    await expect(page.getByLabel("Medium first response", { exact: true })).toHaveValue("4h");
    await expect(page.getByLabel("Critical first response", { exact: true })).toHaveValue("30m");
    await expect(page.getByLabel("Low resolve", { exact: true })).toHaveValue("72h");
    await expect(page.locator(".category-sla-note")).toContainText(
      "Reset replaces this category's targets with the instance defaults.",
    );
    // Reset bypasses invalid client fields, then uses the unchanged server reset.
    await resolve.fill("not a duration");
    await page.getByRole("button", { name: "Reset to the instance defaults" }).click();
    await expect(page).toHaveURL(new RegExp(`${slaPath}$`));
    await page.reload();
    await expect(resolve).toHaveValue("24h");
    await expect(page.locator(".sla-diverges")).toHaveCount(0);
    // No-script rejection preserves raw text and all other fields, atomically.
    const noScriptContext = await page
      .context()
      .browser()!
      .newContext({
        javaScriptEnabled: false,
        storageState: await page.context().storageState(),
      });
    try {
      const noScript = await noScriptContext.newPage();
      await noScript.goto(base() + slaPath);
      await noScript.getByLabel("Medium resolve", { exact: true }).fill("2h 3m 4s");
      await noScript.getByLabel("Critical first response", { exact: true }).fill("bad");
      const rejected = noScript.waitForResponse(
        (r) => r.url() === base() + slaPath && r.request().method() === "POST",
      );
      await noScript.getByRole("button", { name: "Save SLA targets" }).click();
      expect((await rejected).status()).toBe(422);
      await expect(noScript.getByLabel("Critical first response", { exact: true })).toHaveValue(
        "bad",
      );
      await expect(noScript.getByLabel("Medium resolve", { exact: true })).toHaveValue("2h 3m 4s");
      await page.reload();
      await expect(resolve).toHaveValue("24h");
      await noScript.getByLabel("Critical first response", { exact: true }).fill("90m");
      await noScript.getByRole("button", { name: "Save SLA targets" }).click();
      await noScript.reload();
      await expect(noScript.getByLabel("Critical first response", { exact: true })).toHaveValue(
        "1h 30m",
      );
      await expect(noScript.getByLabel("Medium resolve", { exact: true })).toHaveValue("2h 3m 4s");
    } finally {
      await noScriptContext.close();
    }
    await page.reload();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(resolve).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(
      false,
    );
    await page.setViewportSize({ width: 1280, height: 800 });

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "category SLA persist",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("clone a published workflow into a category's draft and refuse to overwrite it", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const targetName = "CloneTarget " + Date.now().toString(36).slice(2, 8);
    const targetId = await createCategoryViaUi(page, targetName);
    const targetPath = `/categories/${targetId}/workflow`;
    await page.goto(base() + targetPath);

    // The target's builder offers the seeded published category as a clone
    // source; since issue #395 the control is a disclosure in the version bar,
    // so the source selector becomes reachable once it is opened.
    const cloneForm = page.locator('form[action$="/workflow/clone"]');
    const cloneTrigger = page.locator(".workflow-clone summary");
    await expect(cloneTrigger).toBeVisible();
    await cloneTrigger.click();
    await expect(cloneForm).toBeVisible();
    const sourceSelect = cloneForm.getByLabel("Clone a published workflow from");
    await expect(sourceSelect.locator("option", { hasText: "General" })).toHaveCount(1);

    // Cloning is a native form submit: it redirects to the target's builder.
    await sourceSelect.selectOption({ label: "General" });
    await cloneForm.getByRole("button", { name: "Clone into this category" }).click();
    await expect(page).toHaveURL(new RegExp(`${targetPath}$`));
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // The cloned published step is now the target's DRAFT.
    const clonedCards = page.locator(".workflow-step-card");
    await expect(clonedCards).toHaveCount(1);
    await expect(clonedCards.first()).toContainText("Handle the ticket");

    // A second clone must be REFUSED and must not overwrite the target's draft.
    await page.reload();
    await cloneTrigger.click();
    await cloneForm
      .getByLabel("Clone a published workflow from")
      .selectOption({ label: "General" });
    await cloneForm.getByRole("button", { name: "Clone into this category" }).click();
    await expect(page.locator(".error-banner[role='alert']")).toContainText("already has a draft");
    await expect(page.locator(".workflow-step-card").first()).toContainText("Handle the ticket");
  });

  // Issue #268: the overflow menus carry role="menu", so they must behave like
  // one: a single open menu, dismissal after choosing, and keyboard traversal.
  // The regression these journeys pin was measured at 678cf3b, where an action
  // left its menu open behind the drawer, opening a second trigger stacked two
  // menus, Escape only worked with a drawer present, and Arrow Down from the
  // trigger did nothing.
  test.describe("overflow menu consistency", () => {
    const structureURL = "/categories";

    // Every tree row owns a menu: the folder row on top,
    // then the category rows. The keys keep the walk upward: "folder" is the
    // top folder row, "desk" the first category row, "category" the last one.
    const menuTargets = (page: Page) => ({
      folder: page
        .locator('li[data-level="folder"] > .catalog-tree-row [data-category-menu]')
        .first(),
      desk: page
        .locator('li[data-level="category"] > .catalog-tree-row [data-category-menu]')
        .first(),
      category: page
        .locator('li[data-level="category"] > .catalog-tree-row [data-category-menu]')
        .last(),
    });

    const menuOf = async (page: Page, trigger: Locator) => {
      const id = await trigger.getAttribute("aria-controls");
      if (!id) throw new Error(`overflow trigger has no aria-controls at ${page.url()}`);
      return page.locator(`#${id}`);
    };

    test("a menu opens alone and Cut closes it on every row", async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
      // The seed has no folder, so the walk brings its own.
      await createFolderViaUi(page, "Menus " + Date.now().toString(36).slice(2, 7));
      await page.goto(base() + structureURL);

      const targets = menuTargets(page);
      const folderMenu = await menuOf(page, targets.folder);
      const deskMenu = await menuOf(page, targets.desk);
      const categoryMenu = await menuOf(page, targets.category);

      // Opening one menu dismisses the previously open one: never two at once.
      // The tree stacks its rows, so the order walks UPWARD (category, desk,
      // folder): a menu always opens below its trigger, and a trigger the
      // previous menu covers could not be clicked.
      await targets.category.click();
      await expect(categoryMenu).toBeVisible();
      await expect(targets.category).toHaveAttribute("aria-expanded", "true");
      await targets.desk.click();
      await expect(deskMenu).toBeVisible();
      await expect(categoryMenu).toBeHidden();
      await expect(targets.category).toHaveAttribute("aria-expanded", "false");
      await targets.folder.click();
      await expect(folderMenu).toBeVisible();
      await expect(deskMenu).toBeHidden();
      await expect(targets.desk).toHaveAttribute("aria-expanded", "false");

      // Click-outside still closes the single open menu.
      await page.locator("h1").first().click();
      await expect(categoryMenu).toBeHidden();
      await expect(targets.category).toHaveAttribute("aria-expanded", "false");

      // Cut is a retained action for every row; it closes the menu and selects the row.
      for (const trigger of Object.values(targets)) {
        const menu = await menuOf(page, trigger);
        const row = trigger.locator('xpath=ancestor::li[@role="treeitem"][1]');
        await trigger.click();
        await expect(menu.getByRole("menuitem", { name: "Move…", exact: true })).toHaveCount(0);
        await menu.getByRole("menuitem", { name: "Cut", exact: true }).click();
        await expect(menu).toBeHidden();
        await expect(trigger).toHaveAttribute("aria-expanded", "false");
        await expect(row).toHaveClass(/catalog-cut/);
        await expect(row).toBeFocused();
        await page.keyboard.press("Escape");
        await expect(row).not.toHaveClass(/catalog-cut/);
      }
    });

    test("keyboard navigation matches role=menu on every entity across repeated cycles", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
      await createFolderViaUi(page, "Keyboard menus " + Date.now().toString(36).slice(2, 7));
      await page.goto(base() + structureURL);

      const targets = menuTargets(page);
      for (const trigger of Object.values(targets)) {
        const menu = await menuOf(page, trigger);
        const items = menu.getByRole("menuitem");
        for (let cycle = 0; cycle < 2; cycle += 1) {
          await trigger.focus();
          await expect(trigger).toBeFocused();
          await page.keyboard.press("ArrowDown");
          await expect(menu).toBeVisible();
          await expect(trigger).toHaveAttribute("aria-expanded", "true");
          await expect(items.first()).toBeFocused();

          await page.keyboard.press("ArrowDown");
          await expect(items.nth(1)).toBeFocused();
          await page.keyboard.press("ArrowUp");
          await expect(items.first()).toBeFocused();
          await page.keyboard.press("End");
          await expect(items.last()).toBeFocused();
          await page.keyboard.press("Home");
          await expect(items.first()).toBeFocused();

          await page.keyboard.press("Escape");
          await expect(menu).toBeHidden();
          await expect(trigger).toBeFocused();
          await expect(trigger).toHaveAttribute("aria-expanded", "false");
        }
      }
    });

    test("Enter and Space activate the focused menu option and close the menu", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
      await page.goto(base() + structureURL);

      const { category } = menuTargets(page);
      const menu = await menuOf(page, category);
      const row = category.locator('xpath=ancestor::li[@role="treeitem"][1]');
      for (const key of [" ", "Enter"]) {
        await category.focus();
        await page.keyboard.press("ArrowDown");
        const cut = menu.getByRole("menuitem", { name: "Cut", exact: true });
        await cut.focus();
        await expect(cut).toBeFocused();
        await page.keyboard.press(key);
        await expect(menu).toBeHidden();
        await expect(category).toHaveAttribute("aria-expanded", "false");
        await expect(row).toHaveClass(/catalog-cut/);
        await expect(row).toBeFocused();
        await page.keyboard.press("Escape");
        await expect(row).not.toHaveClass(/catalog-cut/);
      }
    });
  });
});
