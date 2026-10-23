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
import { loginAsSeeded, base } from "./helpers/auth.js";
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
  createCategoryViaUi,
  createTicketViaUi,
} from "./helpers/navigation.js";
import { applyAssignee, pickerButton } from "./helpers/ticket.js";

/** The row-level menu trigger of one tree node. */
function rowMenu(node: Locator): Locator {
  return node.locator(":scope > .catalog-tree-row").locator("[data-category-menu]");
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

/**
 * Open one category row's edit drawer and wait until it owns the keyboard.
 *
 * The drawer arrives through an HTMX swap targeting #category-drawer-host. Its
 * fields are in the document as soon as that swap lands, but its focus arrives
 * later: htmx processes the swapped nodes and honours the name field's
 * `autofocus`, which moves focus off the drawer's first control. A field filled
 * before that point is typed into whichever element holds focus when the
 * keystroke is delivered, not into the one the journey addressed, so a
 * description typed while the drawer is still settling is saved as part of the
 * category name. Waiting for the name field to take focus is the state the
 * typing depends on, and it also proves the drawer was rendered for the row this
 * journey opened.
 */
async function openCategoryDrawer(
  page: Page,
  row: Locator,
  expected: { name: string; description: string },
): Promise<Locator> {
  const id = await catalogNodeId(row, `category ${expected.name}`);
  const responsePromise = page.waitForResponse((response) => {
    if (response.request().headers()["hx-request"] !== "true") return false;
    if (response.request().method() !== "GET") return false;
    return new URL(response.url()).pathname === `/categories/${id}/edit`;
  });
  await row.getByRole("button", { name: /actions for/i }).click();
  await row.getByRole("menuitem", { name: "Edit category", exact: true }).click();
  const response = await responsePromise;
  expect(response.status(), `the edit drawer for ${expected.name}`).toBe(200);

  const drawer = page.getByRole("dialog", { name: /Edit category/i });
  await expect(drawer).toBeVisible();
  const name = page.locator("#category-name");
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

  test("categories index shows seeded category with workflow badge", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await expect(page.locator("h1").filter({ hasText: "Catalog" })).toBeVisible();
    // One tree holds every level (issue #387), so the seeded hierarchy is visible
    // without a drill-down: department, desk and category rows are all present.
    await expect(page.locator(".catalog-tree")).toBeVisible();
    const seededDepartment = catalogNode(page, "department", "Unassigned");
    await expect(seededDepartment).toHaveCount(1);
    const seededDesk = catalogNode(page, "desk", "General Support");
    await expect(seededDesk).toHaveCount(1);
    // The desk row states its agent count (the seed staffs it) and its open work,
    // which is the per-row health the tree added.
    await expect(
      seededDesk.locator(".catalog-badge-none, .catalog-node-meta").first(),
    ).toContainText(/no agents|agent/i);
    await expect(seededDesk.locator('[data-level="category"]')).toHaveCount(1);
    const seededCategory = catalogCategoryRow(page, "General");
    await expect(seededCategory).toHaveCount(1);
    await expect(seededCategory.locator(".catalog-badge")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Areas" })).toHaveCount(0);
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "categories index",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  /**
   * Search is still a native GET (S3), but it no longer produces a separate
   * results list: the tree filters in place to the matching nodes plus their
   * ancestors, marks the matched run, and offers Clear back to /categories.
   */
  test("categories search filters the tree in place and clears natively", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    const search = page.getByRole("searchbox", {
      name: "Search departments, desks, or categories",
    });
    const icon = page.locator(".category-search .search-icon");
    await expect(icon).toHaveAttribute("viewBox", "0 0 24 24");
    await expect(icon).toHaveAttribute("width", "18");
    await expect(icon).toHaveAttribute("height", "18");
    await expect(icon).toHaveAttribute("aria-hidden", "true");
    await expect(icon).toHaveAttribute("focusable", "false");

    const nativeGet = async (trigger: () => Promise<void>, expectedURL: string) => {
      const responsePromise = page.waitForResponse(
        (response) =>
          response.request().isNavigationRequest() &&
          response.request().method() === "GET" &&
          response.url() === expectedURL,
      );
      const navigationPromise = page.waitForURL(expectedURL);
      await trigger();
      const response = await responsePromise;
      expect(response.status()).toBe(200);
      expect(response.request().headers()).not.toHaveProperty("hx-request");
      await navigationPromise;
    };

    const structureSearchURL = base() + "/categories?q=General&view=structure";
    await search.fill("General");
    await nativeGet(async () => search.press("Enter"), structureSearchURL);
    await expect(search).toHaveValue("General");
    // The match is marked in the tree, and its ancestors stay so the row keeps its
    // place: no results list, no navigation away.
    await expect(page.locator(".catalog-tree mark").first()).toHaveText("General");
    await expect(page.locator('[data-level="department"]').first()).toBeVisible();
    await expect(page.locator("#category-search-results")).toHaveCount(0);

    // Reloading keeps the filter (it is a URL parameter).
    await page.reload();
    await expect(search).toHaveValue("General");
    await expect(page.locator('[data-level="desk"]').first()).toBeVisible();

    // The mobile viewport shows the same filtered tree with indentation.
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator('[data-level="category"]').first()).toBeVisible();
    await assertNoHorizontalOverflow(page, 390);

    // Clear returns to the plain catalog URL, keeping nothing of the query.
    await page.setViewportSize({ width: 1280, height: 800 });
    await nativeGet(
      async () => page.getByRole("link", { name: "Clear search", exact: true }).click(),
      base() + "/categories",
    );
    await expect(search).toHaveValue("");
    await expect(page.locator(".catalog-tree mark")).toHaveCount(0);

    // A query with no match states the empty result and still offers Clear.
    await page.goto(base() + "/categories?q=nothing-matches-this");
    await expect(page.locator(".catalog-tree .category-empty")).toContainText("match your search");
    await expect(page.locator('[data-level="department"]')).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Clear search", exact: true })).toBeVisible();
  });

  /**
   * Needs attention is a native GET chip that composes with the query and keeps
   * the ancestors of every issue it shows.
   */
  test("Needs attention filters the tree and toggles off natively", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");

    const chip = page.locator(".catalog-attention-chip");
    await expect(chip).toBeVisible();
    const label = (await chip.innerText()).trim();
    expect(label).toMatch(/^Needs attention · \d+$/);
    await expect(chip).not.toHaveClass(/is-active/);

    const nativeGet = async (
      trigger: () => Promise<void>,
      expectedURL: string | RegExp | ((url: URL) => boolean),
    ) => {
      const navigationPromise = page.waitForURL(expectedURL);
      await trigger();
      await navigationPromise;
    };

    await nativeGet(async () => chip.click(), /\/categories\?attention=1$/);
    const active = page.locator(".catalog-attention-chip");
    await expect(active).toHaveClass(/is-active/);
    await expect(active).toHaveText(label);
    // Whatever the filtered tree shows, it is a subset of the full tree and every
    // row still has its parent level above it.
    await expect(page.locator('[data-level="desk"]').first()).toBeVisible();
    await expect(page.locator('[data-level="department"]').first()).toBeVisible();

    // Composed with the query: both parameters ride on one URL.
    const search = page.getByRole("searchbox", {
      name: "Search departments, desks, or categories",
    });
    await search.fill("General");
    await search.press("Enter");
    await page.waitForURL(
      (url) => url.searchParams.get("q") === "General" && url.searchParams.get("attention") === "1",
    );
    await expect(page.locator(".catalog-attention-chip")).toHaveClass(/is-active/);

    // The active chip toggles only its own filter off, keeping the query: that is
    // what makes the two controls compose instead of reset each other.
    await nativeGet(
      async () => page.locator(".catalog-attention-chip").click(),
      (url) => url.searchParams.get("q") === "General" && !url.searchParams.has("attention"),
    );
    await expect(page.locator(".catalog-attention-chip")).not.toHaveClass(/is-active/);
  });

  /**
   * The tree is entirely on the page, so its open/closed state is a browser
   * preference, not a navigation: departments and desks carry a disclosure
   * button, the collapsed set lives in localStorage under tkt.catalog.collapsed,
   * and it is re-applied after every HTMX swap of the region (S2).
   */
  test("the tree remembers collapsed departments and desks across reloads and swaps", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    const department = catalogNode(page, "department", "General");
    const departmentButton = department.locator(
      ":scope > .catalog-tree-row [data-catalog-disclosure]",
    );
    const desk = department.locator('[data-level="desk"]').first();
    const deskButton = desk.locator(":scope > .catalog-tree-row [data-catalog-disclosure]");
    const departmentList = page.locator(
      `#catalog-${await department.getAttribute("data-catalog-slug")}`,
    );

    // Everything renders expanded by default, with the accessible name saying
    // what the button will do.
    await expect(departmentButton).toHaveAttribute("aria-expanded", "true");
    await expect(departmentButton).toHaveAttribute("aria-label", "Collapse General");
    await expect(departmentList).toBeVisible();

    await departmentButton.click();
    await expect(departmentButton).toHaveAttribute("aria-expanded", "false");
    await expect(departmentButton).toHaveAttribute("aria-label", "Expand General");
    await expect(departmentList).toBeHidden();
    const stored = await page.evaluate(() => window.localStorage.getItem("tkt.catalog.collapsed"));
    expect(stored).toContain(await department.getAttribute("data-catalog-slug"));

    // A reload re-applies the stored set to the server's expanded markup.
    await page.reload();
    await expect(page.locator(".catalog-tree")).toBeVisible();
    const persisted = catalogNode(page, "department", "General");
    await expect(
      persisted.locator(":scope > .catalog-tree-row [data-catalog-disclosure]"),
    ).toHaveAttribute("aria-expanded", "false");
    await expect(
      page.locator(`#catalog-${await persisted.getAttribute("data-catalog-slug")}`),
    ).toBeHidden();

    // Expand it again and collapse a desk instead.
    await persisted.locator(":scope > .catalog-tree-row [data-catalog-disclosure]").click();
    const innerDesk = persisted.locator('[data-level="desk"]').first();
    const innerButton = innerDesk.locator(":scope > .catalog-tree-row [data-catalog-disclosure]");
    await innerButton.click();
    await expect(innerButton).toHaveAttribute("aria-expanded", "false");

    // An HTMX swap replaces the markup, so the collapsed set has to be applied
    // to the fresh tree: rename the desk and prove the desk stays collapsed.
    const deskName = (await innerDesk.locator(".catalog-node-name").first().innerText()).trim();
    await rowMenu(innerDesk).click();
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await openRowMenu(innerDesk)
          .getByRole("menuitem", { name: "Edit desk", exact: true })
          .click();
      },
      {
        endpoint: (url) => /\/categories\/desks\/\d+\/edit$/.test(new URL(url).pathname),
        expectedUrl: /\/categories\/desks\/\d+\/edit\?/,
      },
    );
    const drawer = page.getByRole("dialog", { name: /Edit desk/i });
    const renamed = `${deskName} ${Date.now().toString(36).slice(-4)}`;
    await drawer.locator("#category-name").fill(renamed);
    await drawer.getByRole("button", { name: /save changes/i }).click();
    const renamedDesk = catalogNode(page, "desk", renamed);
    await expect(renamedDesk).toHaveCount(1);
    await expect(
      renamedDesk.locator(":scope > .catalog-tree-row [data-catalog-disclosure]"),
    ).toHaveAttribute("aria-expanded", "false");

    // "Expand all" / "Collapse all" is one control whose label states the action.
    const toggle = page.locator("[data-catalog-expand-toggle]");
    await expect(toggle).toHaveText("Expand all");
    await toggle.click();
    await expect(toggle).toHaveText("Collapse all");
    await expect(
      renamedDesk.locator(":scope > .catalog-tree-row [data-catalog-disclosure]"),
    ).toHaveAttribute("aria-expanded", "true");
    await toggle.click();
    await expect(toggle).toHaveText("Expand all");
    await expect(
      catalogNode(page, "department", "General").locator(
        ":scope > .catalog-tree-row [data-catalog-disclosure]",
      ),
    ).toHaveAttribute("aria-expanded", "false");

    // A filtered view ignores the stored set: the rows the filter found must be
    // visible even though the user collapsed them.
    await page.goto(base() + "/categories?q=General");
    await expect(
      catalogNode(page, "department", "General").locator(
        ":scope > .catalog-tree-row [data-catalog-disclosure]",
      ),
    ).toHaveAttribute("aria-expanded", "true");
  });

  /**
   * One New menu replaced the three creation launchers (S5): it opens the same
   * three drawers through the same routes and HTMX contract.
   */
  test("the header New menu opens every creation drawer", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    const newMenu = page.locator("[data-catalog-new-toggle]");
    await expect(newMenu).toHaveAttribute("aria-expanded", "false");
    await newMenu.click();
    await expect(newMenu).toHaveAttribute("aria-expanded", "true");
    const menu = page.locator("#catalog-new-menu");
    await expect(menu).toBeVisible();

    for (const tc of [
      { item: "New department", path: "/categories/departments/new", dialog: /New department/i },
      { item: "New desk", path: "/categories/desks/new", dialog: /New desk/i },
      { item: "New category", path: "/categories/new", dialog: /New category/i },
    ]) {
      // The menu is already open on the first pass and closed after each close.
      if ((await newMenu.getAttribute("aria-expanded")) === "false") await newMenu.click();
      await expect(newMenu).toHaveAttribute("aria-expanded", "true");
      await assertDrawerHtmxSwap(
        page,
        async () => {
          await menu.getByRole("link", { name: tc.item, exact: true }).click();
        },
        {
          endpoint: (url) => new URL(url).pathname === tc.path,
          expectedUrl: new RegExp(`${tc.path.replace(/\./g, "\\.")}\\?view=structure$`),
        },
      );
      const drawer = page.getByRole("dialog", { name: tc.dialog });
      await expect(drawer).toBeVisible();
      await drawer.getByRole("button", { name: "Close catalog details", exact: true }).click();
      await expect(drawer).toHaveCount(0);
    }
  });

  /**
   * Row menus keep every action they had and add the two creation shortcuts,
   * preselected on the existing routes (S5).
   */
  test("row menus add a desk and a category preselected on their parent", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    const department = catalogNode(page, "department", "General");
    const departmentID = await catalogNodeId(department, "the General department");
    const desk = department.locator('[data-level="desk"]').first();
    const deskID = await catalogNodeId(desk, "the first General desk");

    await rowMenu(department).click();
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await openRowMenu(department)
          .getByRole("menuitem", { name: "Add desk", exact: true })
          .click();
      },
      {
        endpoint: (url) => new URL(url).pathname === "/categories/desks/new",
        expectedUrl: new RegExp(
          `/categories/desks/new\\?view=structure&department_id=${departmentID}$`,
        ),
      },
    );
    const deskDrawer = page.getByRole("dialog", { name: /New desk/i });
    await expect(deskDrawer.locator("select[name=department_id]")).toHaveValue(departmentID);
    await deskDrawer.getByRole("button", { name: "Close catalog details", exact: true }).click();
    await expect(deskDrawer).toHaveCount(0);

    await rowMenu(desk).click();
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await openRowMenu(desk)
          .getByRole("menuitem", { name: "Add category", exact: true })
          .click();
      },
      {
        endpoint: (url) => new URL(url).pathname === "/categories/new",
        expectedUrl: new RegExp(
          `/categories/new\\?view=structure&department_id=${departmentID}&desk_id=${deskID}$`,
        ),
      },
    );
    const categoryDrawer = page.getByRole("dialog", { name: /New category/i });
    await expect(categoryDrawer.locator("select[name=desk_id]")).toHaveValue(deskID);
    await categoryDrawer
      .getByRole("button", { name: "Close catalog details", exact: true })
      .click();
    await expect(categoryDrawer).toHaveCount(0);
  });

  test("Unassigned drawer edits push reloadable legacy context", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const unassignedContext = "/categories?view=structure&department_id=unassigned";
    await page.goto(base() + unassignedContext);

    // The virtual Unassigned group has no stored Department row, so neither
    // Edit nor Delete can succeed against id -1. The structure page must not
    // offer the impossible action, while real departments keep their menu.
    const unassignedDepartment = catalogNode(page, "department", "Unassigned");
    await expect(unassignedDepartment).toHaveCount(1);
    await expect(rowMenu(unassignedDepartment)).toHaveCount(0);
    await expect(
      unassignedDepartment.locator('a[href*="/categories/departments/-1/edit"]'),
    ).toHaveCount(0);
    await expect(page.locator('form[action="/categories/departments/-1/delete"]')).toHaveCount(0);
    const realDepartment = catalogNode(page, "department", "General");
    await expect(realDepartment).toHaveCount(1);
    await expect(rowMenu(realDepartment)).toHaveCount(1);

    const legacyDeskName = "General Support";
    const legacyDesk = catalogNode(page, "desk", legacyDeskName);
    await expect(legacyDesk).toHaveCount(1);
    const legacyDeskID = await catalogNodeId(legacyDesk, `desk ${legacyDeskName}`);

    await rowMenu(legacyDesk).click();
    const legacyDeskMenu = openRowMenu(legacyDesk);
    const legacyDeskEdit = legacyDeskMenu.getByRole("menuitem", {
      name: "Edit desk",
      exact: true,
    });
    const legacyDeskURL = new RegExp(
      `/categories/desks/${legacyDeskID}/edit\\?view=structure&department_id=unassigned&desk_id=${legacyDeskID}$`,
    );
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await legacyDeskEdit.click();
      },
      {
        endpoint: (url) => {
          const requestURL = new URL(url);
          return (
            requestURL.pathname === `/categories/desks/${legacyDeskID}/edit` &&
            requestURL.searchParams.get("view") === "structure" &&
            requestURL.searchParams.get("department_id") === "unassigned" &&
            requestURL.searchParams.get("desk_id") === legacyDeskID
          );
        },
        expectedUrl: legacyDeskURL,
      },
    );
    const legacyDeskDrawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(legacyDeskDrawer.locator("#category-name")).toHaveValue(legacyDeskName);
    await page.reload();
    await expect(page).toHaveURL(legacyDeskURL);
    await expect(
      page.getByRole("dialog", { name: /Edit desk/i }).locator("#category-name"),
    ).toHaveValue(legacyDeskName);
    await expect(page.locator("body")).not.toContainText("invalid identifier");

    await page.goto(base() + `${unassignedContext}&desk_id=${legacyDeskID}`);
    const legacyCategoryName = "Legacy Support Category";
    const legacyCategory = catalogCategoryRow(page, legacyCategoryName);
    await expect(legacyCategory).toHaveCount(1);
    const legacyCategoryID = await catalogNodeId(legacyCategory, `category ${legacyCategoryName}`);

    await rowMenu(legacyCategory).click();
    const legacyCategoryMenu = openRowMenu(legacyCategory);
    const legacyCategoryEdit = legacyCategoryMenu.getByRole("menuitem", {
      name: "Edit category",
      exact: true,
    });
    const legacyCategoryURL = new RegExp(
      `/categories/${legacyCategoryID}/edit\\?view=structure&department_id=unassigned&desk_id=${legacyDeskID}$`,
    );
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await legacyCategoryEdit.click();
      },
      {
        endpoint: (url) => {
          const requestURL = new URL(url);
          return (
            requestURL.pathname === `/categories/${legacyCategoryID}/edit` &&
            requestURL.searchParams.get("view") === "structure" &&
            requestURL.searchParams.get("department_id") === "unassigned" &&
            requestURL.searchParams.get("desk_id") === legacyDeskID
          );
        },
        expectedUrl: legacyCategoryURL,
      },
    );
    const legacyCategoryDrawer = page.getByRole("dialog", {
      name: /Edit category/i,
    });
    await expect(legacyCategoryDrawer.locator("#category-name")).toHaveValue(legacyCategoryName);
    await page.reload();
    await expect(page).toHaveURL(legacyCategoryURL);
    await expect(
      page.getByRole("dialog", { name: /Edit category/i }).locator("#category-name"),
    ).toHaveValue(legacyCategoryName);
    await expect(page.locator("body")).not.toContainText("invalid identifier");
  });

  // Issue #266: a legacy category lives on a Desk whose department_id is NULL,
  // so it is reachable only through the virtual Unassigned group. Editing it
  // must expose that Desk and that virtual Department, and a plain rename must
  // keep the stored location instead of silently relocating the category to the
  // first real Desk.
  test("legacy category edit keeps its Unassigned desk on save", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const legacyDeskName = "General Support";
    await page.goto(base() + "/categories?view=structure&department_id=unassigned");
    const legacyDesk = catalogNode(page, "desk", legacyDeskName);
    await expect(legacyDesk).toHaveCount(1);
    const legacyDeskID = await catalogNodeId(legacyDesk, `desk ${legacyDeskName}`);

    await page.goto(
      base() + `/categories?view=structure&department_id=unassigned&desk_id=${legacyDeskID}`,
    );
    const legacyCategoryName = "Legacy Support Category";
    const legacyCategory = catalogCategoryRow(page, legacyCategoryName);
    await expect(legacyCategory).toHaveCount(1);
    const legacyCategoryID = await catalogNodeId(legacyCategory, `category ${legacyCategoryName}`);

    await rowMenu(legacyCategory).click();
    await openRowMenu(legacyCategory)
      .getByRole("menuitem", { name: "Edit category", exact: true })
      .click();

    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();
    // The real location is exposed and preselected, not an empty required pair.
    await expect(drawer.locator("#category-department")).toHaveValue("unassigned");
    await expect(drawer.locator("#category-desk")).toHaveValue(legacyDeskID);

    const renamed = `${legacyCategoryName} ${Date.now()}`;
    await drawer.locator("#category-name").fill(renamed);
    await assertHtmxSwap(
      page,
      async () => {
        await drawer.getByRole("button", { name: /save changes/i }).click();
      },
      {
        endpoint: (url) => new URL(url).pathname === `/categories/${legacyCategoryID}/edit`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#categories-background",
        expectedUrl: new RegExp(
          `/categories\\?department_id=unassigned&desk_id=${legacyDeskID}&view=structure$`,
        ),
      },
    );
    await expect(catalogCategoryRow(page, renamed)).toBeVisible();

    // Reloading the drawer reads the persisted location: still the legacy desk.
    await page.goto(
      base() +
        `/categories/${legacyCategoryID}/edit?view=structure&department_id=unassigned&desk_id=${legacyDeskID}`,
    );
    const reloaded = page.getByRole("dialog", { name: /Edit category/i });
    await expect(reloaded.locator("#category-name")).toHaveValue(renamed);
    await expect(reloaded.locator("#category-department")).toHaveValue("unassigned");
    await expect(reloaded.locator("#category-desk")).toHaveValue(legacyDeskID);

    // The real General desk did not gain the category.
    await page.goto(base() + "/categories?view=structure");
    // The tree renders every level at once, so the location is asserted where it
    // actually lives: the renamed row still names the legacy desk it was on, and
    // the General desk does not hold it.
    await page.goto(base() + "/categories?view=structure");
    const renamedRow = catalogCategoryRow(page, renamed);
    await expect(renamedRow).toHaveCount(1);
    await expect(renamedRow).toHaveAttribute("data-desk-id", legacyDeskID);
    const generalDesk = catalogNode(page, "desk", "General");
    const generalDeskID = await catalogNodeId(generalDesk, "the General desk");
    await expect(
      page.locator(
        `[data-level="desk"][data-catalog-id="${generalDeskID}"] [data-level="category"]`,
      ),
    ).not.toHaveAttribute("data-catalog-id", await renamedRow.getAttribute("data-catalog-id"));
  });

  test("a drawer launcher that receives a 4xx shows non-retry feedback and opens no panel", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");
    await expect(page.locator(".catalog-tree")).toBeVisible();

    // #267 removed the virtual Unassigned group's impossible Edit action from the
    // page, but the route still rejects it. This probe reproduces the launcher
    // markup the page used to render so the failure surface is exercised against
    // the real 422, through the real htmx pipeline.
    const failedURL = "/categories/departments/-1/edit?view=structure&department_id=-1";
    await page.evaluate((href) => {
      const probe = document.createElement("a");
      probe.id = "drawer-load-4xx-probe";
      probe.className = "category-drawer-launcher";
      probe.href = href;
      probe.setAttribute("hx-get", href);
      probe.setAttribute("hx-target", "#category-drawer-host");
      probe.setAttribute("hx-swap", "outerHTML");
      probe.setAttribute("hx-push-url", href);
      probe.textContent = "Edit department";
      document.querySelector(".catalog-tree")?.prepend(probe);
      (window as unknown as { htmx: { process: (el: Element) => void } }).htmx.process(probe);
    }, failedURL);

    const drawerHost = page.locator("#category-drawer-host");
    await expect(drawerHost).toHaveCount(1);
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
          response.request().method() === "GET" &&
          new URL(response.url()).pathname === "/categories/departments/-1/edit",
      );
      const urlBefore = page.url();
      await page.locator("#drawer-load-4xx-probe").click();
      const response = await responsePromise;
      // Server-side rejection is unchanged: this fix is client-side only. The
      // plain 4xx carries no HX-Retarget, which is exactly the case htmx leaves
      // unswapped and silent.
      expect(response.status()).toBe(422);
      expect(response.headers()["hx-retarget"]).toBeUndefined();
      await expect(drawerHost.locator(".category-drawer")).toHaveCount(0);
      const feedback = page.locator("#save-feedback");
      await expect(feedback).toBeVisible();
      await expect(feedback).toHaveAttribute("role", "alert");
      await expect(feedback).toHaveAttribute("aria-live", "assertive");
      await expect(feedback.locator(".save-feedback-message")).toHaveText(
        "This panel can't be opened. It may no longer be available.",
      );
      await expect(feedback).not.toContainText("Please try again.");
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
    await page.goto(base() + "/categories?view=structure");
    await expect(page.locator(".catalog-tree")).toBeVisible();

    await page.locator("[data-catalog-new-toggle]").click();
    const launcher = page
      .locator(".catalog-new .category-overflow-menu:not([hidden]) .category-drawer-launcher")
      .first();
    const launcherPath = new URL((await launcher.getAttribute("hx-get")) ?? "", base()).pathname;
    const launcherMatcher = (url: URL) => url.pathname === launcherPath;
    let aborted = false;
    const abortLauncher = async (route: Route) => {
      const request = route.request();
      if (
        request.method() === "GET" &&
        request.headers()["hx-request"] === "true" &&
        new URL(request.url()).pathname === launcherPath
      ) {
        aborted = true;
        await route.abort("failed");
        return;
      }
      await route.continue();
    };
    await page.route(launcherMatcher, abortLauncher);
    try {
      const requestPromise = page.waitForRequest(
        (request) =>
          request.method() === "GET" &&
          request.headers()["hx-request"] === "true" &&
          new URL(request.url()).pathname === launcherPath,
      );
      await launcher.click();
      await requestPromise;
      await expect.poll(() => aborted).toBe(true);
      const feedback = page.locator("#save-feedback");
      await expect(feedback).toBeVisible();
      await expect(feedback.locator(".save-feedback-message")).toHaveText(
        "Unable to open this panel. Please try again.",
      );
      await expect(feedback).not.toContainText("This panel can't be opened.");
      await expect(page.locator("#category-drawer-host .category-drawer")).toHaveCount(0);
    } finally {
      await page.unroute(launcherMatcher, abortLauncher);
    }
  });

  test("Structure keeps the tree whole at every width and preserves the selection after an HTMX desk rename", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // One tree at every width, with the header's one New menu as the creation
    // control. Nothing may overflow, and the header action must keep its label.
    for (const viewport of [1040, 1081, 1120, 390, 1280]) {
      await page.setViewportSize({ width: viewport, height: 800 });
      await page.goto(base() + "/categories?view=structure");
      await page.evaluate(() => document.fonts.ready);
      const geometry = await page.locator(".catalog-tree-row").evaluateAll((rows) => {
        const textRects = (element: Element) => {
          const range = document.createRange();
          range.selectNodeContents(element);
          return Array.from(range.getClientRects()).filter((r) => r.width > 0 && r.height > 0);
        };
        return rows
          .filter((row) => row.getClientRects().length > 0)
          .map((row) => {
            const name = row.querySelector(".catalog-node-name");
            if (!name) throw new Error("Incomplete catalog row");
            const bounds = row.getBoundingClientRect();
            const nameBox = name.getBoundingClientRect();
            return {
              viewport: window.innerWidth,
              row: { left: bounds.left, right: bounds.right },
              name: { left: nameBox.left, right: nameBox.right, width: nameBox.width },
              nameText: textRects(name).length,
            };
          });
      });
      expect(geometry.length, `at ${viewport}px the tree must render rows`).toBeGreaterThan(0);
      for (const g of geometry) {
        const evidence = `catalog row at actual ${g.viewport}px: ${JSON.stringify(g)}`;
        expect(g.viewport).toBe(viewport);
        expect(g.nameText, evidence).toBeGreaterThan(0);
        expect(g.name.left, evidence).toBeGreaterThanOrEqual(g.row.left - 1);
        expect(g.name.right, evidence).toBeLessThanOrEqual(g.row.right + 1);
      }
      await assertNoHorizontalOverflow(page, viewport);
      const newMenu = page.locator("[data-catalog-new-toggle]");
      await expect(newMenu).toBeVisible();
      await expect(newMenu).toBeEnabled();
      const menuBox = await newMenu.boundingBox();
      const labelLines = await newMenu.evaluate((el) => {
        const range = document.createRange();
        range.selectNodeContents(el);
        return Array.from(range.getClientRects()).filter((rect) => rect.width > 0).length;
      });
      expect(menuBox, `at ${viewport}px the New menu must be laid out`).not.toBeNull();
      expect(labelLines, `at ${viewport}px the New label must not wrap apart`).toBeLessThanOrEqual(
        2,
      );
      await assertDrawerHtmxSwap(
        page,
        async () => {
          await newMenu.click();
          await page
            .locator(".catalog-new .category-overflow-menu:not([hidden])")
            .getByRole("link", { name: "New department", exact: true })
            .click();
        },
        {
          endpoint: (url) => url === base() + "/categories/departments/new?view=structure",
          expectedUrl: /\/categories\/departments\/new\?view=structure$/,
        },
      );
      const creationDrawer = page.getByRole("dialog", { name: /New department/i });
      await expect(creationDrawer).toBeVisible();
      await creationDrawer
        .getByRole("button", { name: "Close catalog details", exact: true })
        .click();
      await expect(creationDrawer).toBeHidden();
      await expect(page).toHaveURL(base() + "/categories?view=structure");
    }

    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(base() + "/categories?view=structure");
    const assignedDepartment = catalogNode(page, "department", "General");
    await expect(assignedDepartment).toHaveCount(1);
    const assignedDepartmentID = await catalogNodeId(assignedDepartment, "the General department");
    const assignedDesk = catalogNode(page, "desk", "General");
    await expect(assignedDesk).toHaveCount(1);
    const assignedDeskID = await catalogNodeId(assignedDesk, "the General desk");

    const deskMenu = rowMenu(assignedDesk);
    const focusKey = await deskMenu.getAttribute("data-focus-key");
    await deskMenu.click();
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await openRowMenu(assignedDesk)
          .getByRole("menuitem", { name: "Edit desk", exact: true })
          .click();
      },
      {
        endpoint: (url) => {
          const requestURL = new URL(url);
          return (
            requestURL.pathname === `/categories/desks/${assignedDeskID}/edit` &&
            requestURL.searchParams.get("view") === "structure" &&
            requestURL.searchParams.get("department_id") === assignedDepartmentID &&
            requestURL.searchParams.get("desk_id") === assignedDeskID
          );
        },
        expectedUrl: new RegExp(
          `/categories/desks/${assignedDeskID}/edit[?]view=structure&department_id=${assignedDepartmentID}&desk_id=${assignedDeskID}$`,
        ),
      },
    );
    const drawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(drawer).toBeVisible();
    const renamed = "Support desk " + Date.now();
    await drawer.locator("#category-name").fill(renamed);
    await drawer.getByRole("button", { name: /save changes/i }).click();

    // The saved desk keeps its place in the tree and its row stays current.
    await expect(page).toHaveURL(/\/categories\?department_id=\d+&desk_id=\d+&view=structure/);
    const renamedRow = catalogNode(page, "desk", renamed);
    await expect(renamedRow).toHaveCount(1);
    await expect(renamedRow).toHaveAttribute("data-current", "true");
    await expect(page.locator(`[data-focus-key="${focusKey}"]`)).toBeFocused();
    await expect(page.locator(".category-drawer")).toHaveCount(0);
    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "structure desk rename",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  /**
   * The tree replaced the mobile drill-down: every level is on the page at every
   * width, at mobile with indentation and a category's health wrapped to a second
   * line (S9). Nothing may overflow horizontally or collapse a control to a sliver.
   *
   * Issue #271's long-name case survives as the wrap proof: the shared seed holds
   * only short names and must stay read-only, so the long fixture is created and
   * removed through the public drawers.
   */
  test("Structure keeps the whole tree on mobile with indentation and no overflow", async ({
    page,
  }) => {
    await loginAsSeeded(page);

    const longName =
      "Infrastructure Reliability Escalation Coordination " + Date.now().toString(36);
    const suffix = Date.now().toString(36);
    const emptyName = `Empty hierarchy desk ${suffix}`;

    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(base() + "/categories?view=structure");
    const generalDepartment = catalogNode(page, "department", "General");
    await expect(generalDepartment).toHaveCount(1);
    const departmentID = await catalogNodeId(generalDepartment, "the General department");
    const generalDesk = catalogNode(page, "desk", "General");
    const deskID = await catalogNodeId(generalDesk, "the General desk");

    let longCreated = false;
    let emptyCreated = false;
    try {
      await page.goto(
        base() + `/categories/new?view=structure&department_id=${departmentID}&desk_id=${deskID}`,
      );
      const categoryDrawer = page.getByRole("dialog", { name: /New category/i });
      await expect(categoryDrawer).toBeVisible();
      await categoryDrawer.locator("select[name=department_id]").selectOption(departmentID);
      await categoryDrawer.locator("select[name=desk_id]").selectOption(deskID);
      await categoryDrawer.locator("#category-name").fill(longName);
      await categoryDrawer.getByRole("button", { name: "Create category", exact: true }).click();
      await expect(categoryDrawer).toBeHidden();
      longCreated = true;

      await page.goto(
        base() + `/categories/desks/new?view=structure&department_id=${departmentID}`,
      );
      const deskDrawer = page.getByRole("dialog", { name: /New desk/i });
      await expect(deskDrawer).toBeVisible();
      await deskDrawer.locator("select[name=department_id]").selectOption(departmentID);
      await deskDrawer.locator("#category-name").fill(emptyName);
      await deskDrawer.getByRole("button", { name: "Create desk", exact: true }).click();
      await expect(deskDrawer).toBeHidden();
      emptyCreated = true;

      const longRow = catalogCategoryRow(page, longName);
      const shortRow = catalogCategoryRow(page, "General");
      const emptyRow = catalogNode(page, "desk", emptyName);

      for (const viewport of [390, 640, 1280]) {
        await page.setViewportSize({ width: viewport, height: 800 });
        await page.goto(base() + "/categories?view=structure");
        // No drill-down survives: every level is on the page and there is no Back
        // control to leave it.
        await expect(page.locator('[data-level="department"]').first()).toBeVisible();
        await expect(longRow).toHaveCount(1);
        await expect(longRow).toBeVisible();
        await expect(emptyRow).toHaveCount(1);
        await expect(page.locator(".category-mobile-back")).toHaveCount(0);
        // The New menu is the page action at every width.
        const newMenu = page.locator("[data-catalog-new-toggle]");
        await expect(newMenu).toBeVisible();
        await expect(
          newMenu.evaluate((el) => el.getBoundingClientRect().height),
        ).resolves.toBeGreaterThan(0);
        // The actions control of every visible row keeps a real hit area.
        const actions = longRow.locator(":scope > .catalog-tree-row [data-category-menu]");
        const box = await actions.boundingBox();
        expect(box, `at ${viewport}px the row actions must be laid out`).not.toBeNull();
        expect(box?.width ?? 0, `at ${viewport}px the actions must not collapse`).toBeGreaterThan(
          24,
        );
        expect(box?.height ?? 0, `at ${viewport}px the actions must not collapse`).toBeGreaterThan(
          24,
        );
        // The workflow state and the open count render real text.
        for (const [selector, label] of [
          [".catalog-badge", "workflow state"],
          [".catalog-node-meta", "row metadata"],
        ] as const) {
          const cell = longRow.locator(selector).first();
          await expect(cell, `at ${viewport}px the ${label} must be visible`).toBeVisible();
          expect((await cell.textContent())?.trim(), `at ${viewport}px ${label} text`).not.toBe("");
        }
        // The long name is readable: a real width, and never one character per line.
        const nameBox = await shortRow.locator(".catalog-category-link").boundingBox();
        expect(nameBox, `at ${viewport}px the category name must be laid out`).not.toBeNull();
        // The tree sizes the name to its content instead of a fixed column, so the
        // proof is that the text is not clipped: its scroll width fits its box.
        const clipped = await shortRow.locator(".catalog-category-link").evaluate((el) => {
          const style = getComputedStyle(el);
          const hidden = style.overflow !== "visible" || style.textOverflow === "ellipsis";
          return hidden ? el.scrollWidth > el.clientWidth + 1 : false;
        });
        expect(clipped, `at ${viewport}px the short category name must not be clipped`).toBe(false);
        const longLines = await longRow.locator(".catalog-category-link").evaluate((el) => {
          const range = document.createRange();
          range.selectNodeContents(el);
          return Array.from(range.getClientRects()).filter((rect) => rect.width > 0).length;
        });
        expect(
          longLines,
          `at ${viewport}px the long name must wrap reasonably`,
        ).toBeLessThanOrEqual(Math.ceil(longName.length / 8));
        await assertNoHorizontalOverflow(page, viewport);
      }

      // Below the breakpoint the category row wraps its health to a second line:
      // the name takes the whole first line and the state sits under it.
      await page.setViewportSize({ width: 390, height: 800 });
      await page.goto(base() + "/categories?view=structure");
      const rowBox = await longRow.locator(":scope > .catalog-tree-row").boundingBox();
      const nameBox = await longRow.locator(".catalog-category-link").boundingBox();
      const badgeBox = await longRow.locator(".catalog-badge").boundingBox();
      expect(
        rowBox && nameBox && badgeBox,
        "the mobile category row must be laid out",
      ).toBeTruthy();
      if (rowBox && nameBox && badgeBox) {
        expect(nameBox.width, "at 390px the name takes the row's first line").toBeGreaterThan(
          rowBox.width * 0.8,
        );
        expect(badgeBox.y, "at 390px the health wraps below the name").toBeGreaterThan(
          nameBox.y + nameBox.height - 2,
        );
      }
      // Indentation draws the depth instead of hiding it.
      const departmentPadding = await page
        .locator('[data-level="department"] > .catalog-tree-row')
        .first()
        .evaluate((el) => getComputedStyle(el).paddingLeft);
      const categoryPadding = await longRow
        .locator(":scope > .catalog-tree-row")
        .evaluate((el) => getComputedStyle(el).paddingLeft);
      expect(Number.parseFloat(categoryPadding)).toBeGreaterThan(
        Number.parseFloat(departmentPadding),
      );
    } finally {
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(base() + "/categories?view=structure");
      if (longCreated && (await catalogCategoryRow(page, longName).count()) > 0) {
        const row = catalogCategoryRow(page, longName);
        await row.locator(":scope > .catalog-tree-row [data-category-menu]").click();
        await openRowMenu(row)
          .getByRole("menuitem", { name: "Delete category", exact: true })
          .click();
        await page.locator("#category-delete-dialog [data-category-delete-confirm]").click();
        await expect(catalogCategoryRow(page, longName)).toHaveCount(0);
      }
      if (emptyCreated && (await catalogNode(page, "desk", emptyName).count()) > 0) {
        const row = catalogNode(page, "desk", emptyName);
        await row.locator(":scope > .catalog-tree-row [data-category-menu]").click();
        await openRowMenu(row).getByRole("menuitem", { name: "Delete desk", exact: true }).click();
        await page.locator("#category-delete-dialog [data-category-delete-confirm]").click();
        await expect(catalogNode(page, "desk", emptyName)).toHaveCount(0);
      }
    }
  });

  test("the lowest desk's overflow menu keeps Edit desk reachable inside the tree", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    // The last desk row in the tree is the one whose menu can be pushed off the
    // bottom of the viewport; the menu must open UPWARD there and stay clickable.
    const desks = page.locator('[data-level="desk"]');
    const deskCount = await desks.count();
    expect(deskCount).toBeGreaterThan(0);
    const lowest = desks.nth(deskCount - 1);
    await lowest.scrollIntoViewIfNeeded();
    const menuButton = lowest.locator(":scope > .catalog-tree-row [data-category-menu]");
    await menuButton.click();
    const menu = lowest.locator(".category-overflow-menu:not([hidden])");
    await expect(menu).toBeVisible();
    const edit = menu.getByRole("menuitem", { name: "Edit desk", exact: true });
    await expect(edit).toBeVisible();
    const editBox = await edit.boundingBox();
    expect(editBox, "the Edit desk item must be laid out").not.toBeNull();
    if (editBox) {
      expect(editBox.y).toBeGreaterThanOrEqual(0);
      expect(editBox.y + editBox.height).toBeLessThanOrEqual(
        await page.evaluate(() => window.innerHeight),
      );
      const hitTarget = await page.evaluate(
        ({ x, y }) => {
          const element = document.elementFromPoint(x, y);
          return element?.closest('[role="menuitem"]')?.textContent?.trim() ?? null;
        },
        { x: editBox.x + editBox.width / 2, y: editBox.y + editBox.height / 2 },
      );
      expect(hitTarget).toBe("Edit desk");
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
    const drawer = await openCategoryDrawer(page, row, {
      name: catName,
      description: "",
    });
    const description = drawer.getByLabel("Description", { exact: true });
    await expect(description).toHaveValue("");
    const renamed = catName + " Renamed";
    await drawer.locator("#category-name").fill(renamed);
    await description.fill("Temporary description");
    await drawer.getByRole("button", { name: /save changes/i }).click();

    await expect(page).toHaveURL(/\/categories/);
    await expect(catalogCategoryRow(page, renamed)).toBeVisible();

    const editedRow = catalogCategoryRow(page, renamed);
    const clearedDrawer = await openCategoryDrawer(page, editedRow, {
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
    await expect(page.getByText(renamed)).toHaveCount(0);

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
      await row.getByRole("button", { name: /actions for/i }).click();
      await row.getByRole("menuitem", { name: "Edit category", exact: true }).click();
      const drawer = page.getByRole("dialog", { name: /Edit category/i });
      await expect(drawer).toBeVisible();
      return drawer;
    };

    const drawer = await openEditDrawer();
    const keywords = drawer.getByLabel("Keywords for suggestions", { exact: true });
    await expect(keywords).toHaveValue("");
    await expect(drawer.locator("#category-keywords-help")).toHaveText(
      "Words people use for this category. Comma separated; accents and capitals do not matter.",
    );
    await keywords.fill("impresora, tóner");
    await drawer.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(catalogCategoryRow(page, name)).toHaveCount(1);

    // Re-opening the drawer reads the stored words back, which is what an
    // administrator needs before editing them again. Closing it leaves the plain
    // catalog for the canonical screen assertion below.
    const reopened = await openEditDrawer();
    await expect(reopened.getByLabel("Keywords for suggestions", { exact: true })).toHaveValue(
      "impresora, tóner",
    );
    await reopened.getByRole("button", { name: "Close catalog details", exact: true }).click();
    await expect(page.getByRole("dialog", { name: /Edit category/i })).toHaveCount(0);

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

  test("category duplicate marks only name, keeps the draft, and saves after Stay", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories/new?view=structure&department_id=1&desk_id=1");

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
    await expect(drawer.getByLabel("Department", { exact: true })).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await expect(drawer.getByLabel("Desk", { exact: true })).not.toHaveAttribute(
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
        expectedUrl: /\/categories\?department_id=1&desk_id=1&view=structure$/,
      },
    );
    await expect(drawer).toHaveCount(0);
    await expect(catalogCategoryRow(page, categoryName)).toBeVisible();
  });

  test("dirty category drawer backdrop Stay restores the prior form control", async ({ page }) => {
    await loginAsSeeded(page);
    const drawerURL = base() + "/categories/new?view=structure&department_id=1&desk_id=1";
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
    // The legacy selection URL still marks the department and desk rows. The desk
    // row's own "Add category" shortcut is the creation entry that preselects the
    // desk this journey asserts in the URL (issue #387, S5).
    await page.goto(base() + "/categories?view=structure&department_id=1&desk_id=1");

    const addCategory = async () => {
      const desk = catalogNode(page, "desk", "General");
      await rowMenu(desk).click();
      await openRowMenu(desk).getByRole("menuitem", { name: "Add category", exact: true }).click();
    };
    await addCategory();
    const drawer = page.getByRole("dialog", { name: /New category/i });
    const name = drawer.getByLabel("Name", { exact: true });
    const close = drawer.getByRole("button", {
      name: "Close catalog details",
      exact: true,
    });
    await expect(page).toHaveURL(/\/categories\/new\?view=structure&department_id=1&desk_id=1$/);
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
    await expect(page).toHaveURL(/\/categories\?view=structure&department_id=1&desk_id=1$/);
    await addCategory();
    await expect(drawer).toBeVisible();
    await expect(page).toHaveURL(/\/categories\/new\?view=structure&department_id=1&desk_id=1$/);
    drawerURL = page.url();
    await name.fill("Unsaved category");
    await page.goBack();
    await expect(confirmation).toBeVisible();
    await expect(page).toHaveURL(drawerURL);
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(page).toHaveURL(/\/categories\?view=structure&department_id=1&desk_id=1$/);
    await expect(rowMenu(catalogNode(page, "desk", "General"))).toBeFocused();
  });

  test("clean category drawer close restores the launcher for Cancel, Escape and backdrop", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Focus restore " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, name);
    const structureURL = page.url();

    for (const mechanism of ["Cancel", "Escape", "backdrop"] as const) {
      await page.goto(structureURL);
      const row = catalogCategoryRow(page, name);
      await expect(row).toHaveCount(1);
      const menu = row.getByRole("button", { name: `Actions for ${name}`, exact: true });
      const focusKey = await menu.getAttribute("data-focus-key");
      expect(focusKey).toBeTruthy();
      await menu.click();
      await row
        .locator(".category-overflow-menu:not([hidden])")
        .getByRole("menuitem", { name: "Edit category", exact: true })
        .click();
      const drawer = page.getByRole("dialog", { name: /Edit category/i });
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
    await createCategoryViaUi(page, name);
    await page.goto(page.url());
    const row = catalogCategoryRow(page, name);
    const menu = row.getByRole("button", { name: `Actions for ${name}`, exact: true });
    const focusKey = await menu.getAttribute("data-focus-key");
    await menu.click();
    await row
      .locator(".category-overflow-menu:not([hidden])")
      .getByRole("menuitem", { name: "Edit category", exact: true })
      .click();
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
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

    const openers = [
      {
        label: "New department",
        url: "/categories?view=structure",
        open: async () => {
          await page.locator("[data-catalog-new-toggle]").click();
          const launcher = page
            .locator(
              '.catalog-new .category-overflow-menu:not([hidden]) a[data-focus-key="new-department"]',
            )
            .first();
          const focusKey = await launcher.getAttribute("data-focus-key");
          await launcher.click();
          return {
            dialog: page.getByRole("dialog", { name: /New department/i }),
            focusKey,
            // The launcher lives inside the New menu, which closes with the click,
            // so focus lands on the menu's own visible trigger.
            focusTarget: "[data-catalog-new-toggle]",
          };
        },
      },
      {
        label: "Edit department",
        url: "/categories?view=structure",
        open: async () => {
          const department = page.locator('[data-level="department"]').first();
          const menu = rowMenu(department);
          const focusKey = await menu.getAttribute("data-focus-key");
          await menu.click();
          await openRowMenu(department)
            .getByRole("menuitem", { name: "Edit department", exact: true })
            .click();
          return { dialog: page.getByRole("dialog", { name: /Edit department/i }), focusKey };
        },
      },
      {
        label: "New desk",
        url: "/categories?view=structure&department_id=1",
        open: async () => {
          await page.locator("[data-catalog-new-toggle]").click();
          const launcher = page
            .locator(
              '.catalog-new .category-overflow-menu:not([hidden]) a[data-focus-key="new-desk"]',
            )
            .first();
          const focusKey = await launcher.getAttribute("data-focus-key");
          await launcher.click();
          return {
            dialog: page.getByRole("dialog", { name: /New desk/i }),
            focusKey,
            focusTarget: "[data-catalog-new-toggle]",
          };
        },
      },
      {
        label: "Edit desk",
        url: "/categories?view=structure&department_id=1",
        open: async () => {
          const desk = page.locator('[data-level="desk"]').first();
          const menu = rowMenu(desk);
          const focusKey = await menu.getAttribute("data-focus-key");
          await menu.click();
          await openRowMenu(desk).getByRole("menuitem", { name: "Edit desk", exact: true }).click();
          return { dialog: page.getByRole("dialog", { name: /Edit desk/i }), focusKey };
        },
      },
      {
        label: "New category",
        url: "/categories?view=structure&department_id=1&desk_id=1",
        open: async () => {
          await page.locator("[data-catalog-new-toggle]").click();
          const launcher = page
            .locator(
              '.catalog-new .category-overflow-menu:not([hidden]) a[data-focus-key="new-category"]',
            )
            .first();
          const focusKey = await launcher.getAttribute("data-focus-key");
          await launcher.click();
          return {
            dialog: page.getByRole("dialog", { name: /New category/i }),
            focusKey,
            focusTarget: "[data-catalog-new-toggle]",
          };
        },
      },
    ];

    for (const opener of openers) {
      await page.goto(base() + opener.url);
      const { dialog, focusKey, focusTarget } = await opener.open();
      await expect(dialog).toBeVisible();
      await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
      await expect(dialog).toHaveCount(0);
      if (focusTarget) {
        await expect(page.locator(focusTarget)).toBeFocused();
      } else {
        await expect(page.locator(`[data-focus-key="${focusKey}"]`)).toBeFocused();
      }
    }
  });

  test("a vanished launcher falls back to a named control, never body", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Vanished trigger " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, name);
    const row = catalogCategoryRow(page, name);
    await expect(row).toHaveCount(1);
    const menu = row.getByRole("button", { name: `Actions for ${name}`, exact: true });
    const focusKey = await menu.getAttribute("data-focus-key");
    expect(focusKey).toBeTruthy();
    await menu.click();
    await row
      .locator(".category-overflow-menu:not([hidden])")
      .getByRole("menuitem", { name: "Edit category", exact: true })
      .click();
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();

    // Simulate the background update that removes the trigger while the drawer is open.
    await page.evaluate((key) => {
      document.querySelector(`[data-focus-key="${key}"]`)?.remove();
    }, focusKey);

    await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(page.locator("[data-catalog-new-toggle]")).toBeFocused();
    await expect(page.locator("body")).not.toBeFocused();
  });

  test("a vanished launcher with no level control falls back to the named region", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Vanished region " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, name);
    const row = catalogCategoryRow(page, name);
    await expect(row).toHaveCount(1);
    const menu = row.getByRole("button", { name: `Actions for ${name}`, exact: true });
    const focusKey = await menu.getAttribute("data-focus-key");
    expect(focusKey).toBeTruthy();
    await menu.click();
    await row
      .locator(".category-overflow-menu:not([hidden])")
      .getByRole("menuitem", { name: "Edit category", exact: true })
      .click();
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();

    // Simulate a background update that removes BOTH the trigger and the New
    // menu's own control. With tier one gone, only the tree's named region can
    // receive focus -- never <body>.
    await page.evaluate((key) => {
      document.querySelector(`[data-focus-key="${key}"]`)?.remove();
      document.querySelector("[data-catalog-new-toggle]")?.remove();
    }, focusKey);

    await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(
      page.locator('section.catalog-tree-region[aria-labelledby="catalog-tree-title"]'),
    ).toBeFocused();
    await expect(page.locator("body")).not.toBeFocused();
  });

  test("dirty department and desk drawers require an explicit discard", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/categories/departments/1/edit?view=structure");

    const department = page.getByRole("dialog", { name: /Edit department/i });
    const departmentName = department.getByLabel("Name", { exact: true });
    await expect(department).toHaveAttribute("aria-describedby", "department-drawer-description");
    await expect(department.locator("#department-drawer-description")).toHaveText(
      "Use departments to group desks that support the same part of the organization.",
    );
    await expect(departmentName).toHaveAttribute("aria-describedby", "department-name-help");
    await expect(department.locator("#department-name-help")).toHaveText(
      "Department names must be globally unique.",
    );
    await departmentName.fill("Unsaved department");
    await department.getByRole("button", { name: "Close catalog details" }).click();

    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?",
    });
    await expect(confirmation).toBeVisible();
    await expect(confirmation.getByRole("button", { name: "Stay", exact: true })).toBeFocused();
    await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
    await expect(departmentName).toHaveValue("Unsaved department");
    await expect(page).toHaveURL(/\/categories\/departments\/1\/edit/);
    await department.getByRole("button", { name: "Close catalog details" }).click();
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(department).toHaveCount(0);

    await page.goto(base() + "/categories/desks/1/edit?view=structure&department_id=1&desk_id=1");
    const desk = page.getByRole("dialog", { name: /Edit desk/i });
    const description = desk.getByLabel("Description", { exact: true });
    const deskName = desk.getByLabel("Name", { exact: true });
    await expect(desk).toHaveAttribute("aria-describedby", "desk-drawer-description");
    await expect(desk.locator("#desk-drawer-description")).toHaveText(
      "Use desks to group categories for the team that handles them.",
    );
    await expect(deskName).toHaveAttribute("aria-describedby", "desk-name-help");
    await expect(desk.locator("#desk-name-help")).toHaveText("Desk names must be globally unique.");
    await description.fill("Unsaved desk description");
    await desk.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(desk).toHaveCount(0);
    await expect(page).toHaveURL(
      /\/categories\?(?=.*view=structure)(?=.*department_id=1)(?=.*desk_id=1)/,
    );
  });

  test("history restoration does not revive a stale category save confirmation", async ({
    page,
  }) => {
    test.setTimeout(30_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const listPath = "/categories?view=structure&department_id=1&desk_id=1";
    await page.goto(base() + listPath);

    // The desk row's "Add category" shortcut opens the preselected drawer, which
    // is the URL this journey reloads and restores (issue #387, S5).
    const addCategory = async () => {
      const desk = catalogNode(page, "desk", "General");
      await rowMenu(desk).click();
      await openRowMenu(desk).getByRole("menuitem", { name: "Add category", exact: true }).click();
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
        expectedUrl: /\/categories\?department_id=1&desk_id=1&view=structure$/,
      },
    );
    const feedback = page.locator("#save-feedback");
    await expect(feedback).toBeVisible();

    await assertDrawerHtmxSwap(page, addCategory, {
      endpoint: (url) => new URL(url).pathname === "/categories/new",
      expectedUrl: /\/categories\/new\?view=structure&department_id=1&desk_id=1$/,
    });
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
    const editHref = await category.locator('a[href*="/edit"]').getAttribute("href");
    const categoryID = editHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
    if (!categoryID) throw new Error(`Could not resolve workflow category at ${page.url()}`);
    const workflowPath = `/categories/${categoryID}/workflow`;
    await page.goto(base() + workflowPath);

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
    const editHref = await catRow.locator('a[href*="/edit"]').getAttribute("href");
    const m = editHref?.match(/\/categories\/(\d+)\/edit/);
    if (!m) throw new Error("cannot extract category id for " + catName);
    const categoryId = m[1];
    const workflowPath = `/categories/${categoryId}/workflow`;
    await page.goto(base() + workflowPath);
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
    await page.goto(base() + "/categories?view=structure");
    await expect(catalogCategoryRow(page, catName).locator(".catalog-badge")).toContainText(
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
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(
      /workflow-pending-info/,
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
    await expect(pending.getByLabel("Solution (optional)")).toBeVisible();
    await expect(pending.getByRole("button", { name: "Complete" })).toBeVisible();
    await expect(page.locator("#timeline .timeline-entry").first()).toHaveClass(
      /workflow-pending-action/,
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
      // rail dot, not a per-event glyph.
      await expect(completedManual.locator(".timeline-entry-head .narrative")).toHaveText(
        "Alice Admin completed the task",
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

  test("deleting a desk asks first, and a refused delete keeps the structure selection", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure&department_id=unassigned");

    // The seeded legacy desk still hosts a category, so the store refuses its
    // delete: the fixture the audit's reproduction step names. The desk is
    // resolved by its position under the Unassigned group, never by a name
    // another journey in this file may have renamed.
    const unassigned = catalogNode(page, "department", "Unassigned");
    const deskRow = unassigned.locator('[data-level="desk"]').first();
    await expect(deskRow, `the unassigned desk at ${page.url()}`).toHaveCount(1);
    const deskName = (await deskRow.locator(".catalog-node-name").first().innerText()).trim();
    const deskID = await catalogNodeId(deskRow, "the unassigned desk");
    // Selecting it is a legacy link, not a drill-down: the tree never hides a
    // level (issue #387, S6).
    await page.goto(
      base() + `/categories?view=structure&department_id=unassigned&desk_id=${deskID}`,
    );
    await expect(deskRow).toHaveAttribute("data-current", "true");
    const structureURL = page.url();
    const dialog = page.locator("#category-delete-dialog");

    await rowMenu(deskRow).click();
    await openRowMenu(deskRow).getByRole("menuitem", { name: "Delete desk", exact: true }).click();

    // The confirmation names the row, focuses the safe choice, and the
    // destructive action is the only red control in the dialog.
    await expect(dialog).toBeVisible();
    await expect(dialog.locator("#category-delete-title")).toHaveText(
      `Delete the desk \u201c${deskName}\u201d?`,
    );
    await expect(dialog.locator("[data-category-delete-cancel]")).toBeFocused();
    await expect(dialog.locator("[data-category-delete-confirm]")).toHaveCSS(
      "background-color",
      "rgb(180, 35, 24)",
    );
    await expect(dialog.locator("[data-category-delete-cancel]")).not.toHaveCSS(
      "background-color",
      "rgb(180, 35, 24)",
    );

    // Cancel is the safe exit: nothing is deleted, the menu the dialog
    // replaced is closed, and focus is back on the trigger that opened it.
    await dialog.locator("[data-category-delete-cancel]").click();
    await expect(dialog).not.toBeVisible();
    await expect(deskRow).toHaveCount(1);
    await expect(page).toHaveURL(structureURL);
    await expect(rowMenu(deskRow)).toBeFocused();

    // Confirming a delete the store refuses keeps the structure view, its
    // department and desk selection, and the URL, and reports the error there.
    const navigations: string[] = [];
    const navigationHandler = (request: Request) => {
      if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
        navigations.push(request.url());
      }
    };
    page.on("request", navigationHandler);
    try {
      // Cancelling closed the menu, so the second attempt starts from the
      // trigger again.
      await rowMenu(deskRow).click();
      const deleteItem = openRowMenu(deskRow).getByRole("menuitem", {
        name: "Delete desk",
        exact: true,
      });
      await expect(deleteItem).toBeVisible();
      const refusal = page.waitForResponse(
        (response) =>
          response.request().headers()["hx-request"] === "true" &&
          response.request().method() === "POST" &&
          response.url().endsWith(`/categories/desks/${deskID}/delete`),
      );
      await deleteItem.click();
      await dialog.locator("[data-category-delete-confirm]").click();
      const response = await refusal;
      expect(response.status()).toBe(409);
      expect(response.headers()["hx-retarget"]).toBe("#categories-background");
      expect(response.headers()["hx-reswap"]).toBe("outerHTML");
      await expect(page.locator("#categories-background .error-banner")).toHaveText(
        "desk is referenced and cannot be deleted",
      );
      await expect(page).toHaveURL(structureURL);
      const refusedRow = catalogNode(page, "desk", deskName);
      await expect(refusedRow).toHaveCount(1);
      await expect(refusedRow).toHaveAttribute("data-current", "true");
      await expect(refusedRow.locator(":scope > .catalog-tree-row")).toHaveAttribute(
        "aria-current",
        "true",
      );
    } finally {
      page.off("request", navigationHandler);
    }
    expect(navigations, `a refused delete must not navigate: ${navigations.join("; ")}`).toEqual(
      [],
    );
    expect(obs.pageErrors).toEqual([]);
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
      const editHref = await category.locator('a[href*="/edit"]').getAttribute("href");
      const categoryID = editHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
      if (!categoryID) throw new Error(`Could not resolve workflow category at ${page.url()}`);
      await page.goto(base() + `/categories/${categoryID}/workflow`);
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
    await page.locator('.page-actions button[name="action"][value="save"]').click();
    await expect(page.locator("#save-feedback .save-feedback-message")).toHaveText("Saved");

    // Reload and re-select the step: the persisted draft renders "Must be
    // ticked" (not the bare "Required") and keeps the control ticked.
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible();
    await page.locator(".workflow-step-card .workflow-step-card-link").first().click();
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

    // Two steps, so the vertical ordering and the step count are real.
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
    await expect(page.locator(".workflow-pane-count")).toHaveText("2 steps");

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
      name: "Search departments, desks, or categories",
    });
    await search.fill(categoryName);
    await search.press("Enter");
    await expect(catalogCategoryRow(page, categoryName)).toHaveCount(1);

    const rowSelector = '[data-level="category"]';
    const categoryRow = catalogCategoryRow(page, categoryName);
    if ((await categoryRow.count()) !== 1) {
      throw new Error(
        `Expected exactly one category "${categoryName}" using ${rowSelector} at ${page.url()}, found ${await categoryRow.count()}`,
      );
    }

    const categoryId = await catalogNodeId(categoryRow, `category ${categoryName}`);

    // The overflow menu's Configure SLA item must point at THIS category.
    const slaPath = `/categories/${categoryId}/sla`;
    await rowMenu(categoryRow).click();
    const menu = openRowMenu(categoryRow);
    const configureSla = menu.getByRole("menuitem", { name: "Configure SLA", exact: true });
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
    const structureURL = "/categories?view=structure&department_id=1&desk_id=1";

    // Every level owns a row menu in the one tree: department, desk, category.
    const menuTargets = (page: Page) => ({
      department: page
        .locator('[data-level="department"] > .catalog-tree-row [data-category-menu]')
        .first(),
      desk: page.locator('[data-level="desk"] > .catalog-tree-row [data-category-menu]').first(),
      category: page
        .locator('[data-level="category"] > .catalog-tree-row [data-category-menu]')
        .first(),
    });

    const menuOf = async (page: Page, trigger: Locator) => {
      const id = await trigger.getAttribute("aria-controls");
      if (!id) throw new Error(`overflow trigger has no aria-controls at ${page.url()}`);
      return page.locator(`#${id}`);
    };

    test("a menu opens alone, an action closes it, and the drawer lifecycle clears it", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
      await page.goto(base() + structureURL);

      const targets = menuTargets(page);
      const departmentMenu = await menuOf(page, targets.department);
      const deskMenu = await menuOf(page, targets.desk);
      const categoryMenu = await menuOf(page, targets.category);

      // Opening one menu dismisses the previously open one: never two at once.
      // The tree stacks its rows, so the order walks UPWARD (category, desk,
      // department): a menu always opens below its trigger, and a trigger the
      // previous menu covers could not be clicked.
      await targets.category.click();
      await expect(categoryMenu).toBeVisible();
      await expect(targets.category).toHaveAttribute("aria-expanded", "true");
      await targets.desk.click();
      await expect(deskMenu).toBeVisible();
      await expect(categoryMenu).toBeHidden();
      await expect(targets.category).toHaveAttribute("aria-expanded", "false");
      await targets.department.click();
      await expect(departmentMenu).toBeVisible();
      await expect(deskMenu).toBeHidden();
      await expect(targets.desk).toHaveAttribute("aria-expanded", "false");

      // Click-outside still closes the single open menu.
      await page.locator("h1").first().click();
      await expect(categoryMenu).toBeHidden();
      await expect(targets.category).toHaveAttribute("aria-expanded", "false");

      // Choosing an action closes its own menu on all three entities, and the
      // drawer close path leaves no menu behind.
      for (const entity of [
        { trigger: targets.department, item: "Edit department", dialog: /Edit department/i },
        { trigger: targets.desk, item: "Edit desk", dialog: /Edit desk/i },
        { trigger: targets.category, item: "Edit category", dialog: /Edit category/i },
      ]) {
        const menu = await menuOf(page, entity.trigger);
        await entity.trigger.click();
        await expect(menu).toBeVisible();
        await menu.getByRole("menuitem", { name: entity.item, exact: true }).click();
        const drawer = page.getByRole("dialog", { name: entity.dialog });
        await expect(drawer).toBeVisible();
        await expect(menu).toBeHidden();
        await expect(entity.trigger).toHaveAttribute("aria-expanded", "false");
        await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
        await expect(drawer).toHaveCount(0);
        await expect(page.locator(".category-overflow-menu:not([hidden])")).toHaveCount(0);
        await expect(entity.trigger).toBeFocused();
      }
    });

    test("keyboard navigation matches role=menu on every entity across repeated cycles", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await loginAsSeeded(page);
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
      const drawer = page.getByRole("dialog", { name: /Edit category/i });
      const openFirst = async () => {
        await category.focus();
        await page.keyboard.press("ArrowDown");
        await expect(menu).toBeVisible();
        await expect(menu.getByRole("menuitem").first()).toBeFocused();
      };
      // The drawer close path restores focus via requestAnimationFrame and again
      // on a 100ms backstop; let both settle before driving the trigger again.
      const closeDrawer = async () => {
        await drawer.getByRole("button", { name: "Cancel", exact: true }).click();
        await expect(drawer).toHaveCount(0);
        await page.waitForTimeout(150);
      };

      // Space activates a role=menuitem link, then the menu and the action's
      // own drawer lifecycle leave nothing open.
      await openFirst();
      await page.keyboard.press(" ");
      await expect(drawer).toBeVisible();
      await expect(menu).toBeHidden();
      await closeDrawer();

      // Enter keeps the native anchor activation the role promises.
      await openFirst();
      await page.keyboard.press("Enter");
      await expect(drawer).toBeVisible();
      await expect(menu).toBeHidden();
      await closeDrawer();
    });
  });
});
