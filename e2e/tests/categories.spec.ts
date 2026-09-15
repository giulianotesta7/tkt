/**
 * Categories + Workflow Builder journeys.
 *
 * The published workflow version CAN be observed on the ticket detail page via the
 * passive pending status line (#workflow-pending-status + .pending-status-detail) —
 * requester-owned tickets are passive and never render the active current-task card.
 */

import {
  test,
  expect,
  type Page,
  type Request,
  type Response,
  type Route,
} from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import {
  assertCanonicalScreen,
  collectObservability,
} from "./helpers/layout.js";
import { assertHtmxNoSwap, assertHtmxSwap } from "./helpers/htmx.js";
import { isHtmxPost } from "./helpers/save-feedback.js";
import {
  createCategoryViaUi,
  createTicketViaUi,
} from "./helpers/navigation.js";

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

test.describe("Categories", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("categories index shows seeded category with workflow badge", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await expect(
      page.locator("h1").filter({ hasText: "Categories" }),
    ).toBeVisible();
    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    const seededDesk = page
      .locator(".category-level-desks .category-structure-row")
      .filter({
        has: page.getByText("General Support", { exact: true }),
      });
    await expect(seededDesk).toHaveCount(1);
    await expect(seededDesk).not.toContainText("Department:");
    await expect(seededDesk).not.toContainText(/\d+\s+categor(?:y|ies)/i);
    await expect(seededDesk.locator(".category-count")).toHaveCount(0);
    await expect(seededDesk).toHaveAttribute(
      "href",
      /department_id=unassigned&desk_id=\d+/,
    );
    await expect(page.locator(".category-level-categories")).toBeVisible();
    await expect(
      page.locator(".category-level-categories .category-empty"),
    ).toContainText("Select a desk");
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

  test("categories search uses native navigation and keeps hierarchy context", async ({
    page,
  }) => {
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

    const nativeGet = async (
      trigger: () => Promise<void>,
      expectedURL: string,
    ) => {
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
    await expect(page.locator("#category-search-results")).toBeVisible();
    for (const kind of ["department", "desk", "category"]) {
      await expect(
        page
          .locator(`[data-search-result-kind="${kind}"]`)
          .filter({ has: page.getByText("General", { exact: true }) }),
      ).toHaveCount(1);
    }

    await page.reload();
    await expect(search).toHaveValue("General");
    await page.setViewportSize({ width: 390, height: 844 });
    const selectedCategoryResult = page
      .locator(
        'a.category-search-result[data-search-result-kind="category"][href="/categories?department_id=1&desk_id=1&view=structure"]',
      )
      .filter({ has: page.getByText("General", { exact: true }) });
    await expect(selectedCategoryResult).toHaveCount(1);
    await selectedCategoryResult.click();
    await page.waitForURL((url) =>
      url.pathname === "/categories" &&
      url.searchParams.get("view") === "structure" &&
      url.searchParams.get("department_id") === "1" &&
      url.searchParams.get("desk_id") === "1" &&
      !url.searchParams.has("q"),
    );
    await expect(page.locator(".category-level-categories")).toBeVisible();

    const selectedStructureSearchURL =
      base() + "/categories?q=General&view=structure&department_id=1&desk_id=1";
    await search.fill("General");
    await nativeGet(
      async () => search.press("Enter"),
      selectedStructureSearchURL,
    );
    const structureClearURL =
      base() + "/categories?department_id=1&desk_id=1&view=structure";
    await nativeGet(
      async () =>
        page.getByRole("link", { name: "Clear search", exact: true }).click(),
      structureClearURL,
    );
    await expect(search).toHaveValue("");

    const categoriesSearchURL =
      base() + "/categories?department_id=1&desk_id=1&q=General&view=categories";
    await page.goto(categoriesSearchURL);
    await expect(search).toHaveValue("General");
    const categoriesClearURL =
      base() + "/categories?department_id=1&desk_id=1&view=categories";
    await nativeGet(
      async () =>
        page.getByRole("link", { name: "Clear search", exact: true }).click(),
      categoriesClearURL,
    );
    await expect(search).toHaveValue("");
  });

  test("Unassigned drawer edits push reloadable legacy context", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const unassignedContext =
      "/categories?view=structure&department_id=unassigned";
    await page.goto(base() + unassignedContext);

    const legacyDeskName = "General Support";
    const legacyDesk = page
      .locator(".category-level-desks .category-structure-item")
      .filter({
        has: page.getByText(legacyDeskName, { exact: true }),
      });
    await expect(legacyDesk).toHaveCount(1);
    const legacyDeskHref = await legacyDesk
      .locator("a.category-structure-row")
      .getAttribute("href");
    const legacyDeskID = legacyDeskHref?.match(/desk_id=(\d+)/)?.[1];
    if (!legacyDeskID) {
      throw new Error(
        `Could not resolve ${legacyDeskName} from ${legacyDeskHref ?? "missing href"} at ${page.url()}`,
      );
    }

    await legacyDesk
      .getByRole("button", {
        name: `Actions for ${legacyDeskName}`,
        exact: true,
      })
      .click();
    const legacyDeskMenu = legacyDesk.locator(
      ".category-overflow-menu:not([hidden])",
    );
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
    await expect(legacyDeskDrawer.locator("#category-name")).toHaveValue(
      legacyDeskName,
    );
    await page.reload();
    await expect(page).toHaveURL(legacyDeskURL);
    await expect(
      page
        .getByRole("dialog", { name: /Edit desk/i })
        .locator("#category-name"),
    ).toHaveValue(legacyDeskName);
    await expect(page.locator("body")).not.toContainText("invalid identifier");

    await page.goto(base() + `${unassignedContext}&desk_id=${legacyDeskID}`);
    const legacyCategoryName = "Legacy Support Category";
    const legacyCategory = page
      .locator(".category-level-categories .category-structure-item")
      .filter({
        has: page.getByText(legacyCategoryName, { exact: true }),
      });
    await expect(legacyCategory).toHaveCount(1);
    const legacyCategoryEditHref = await legacyCategory
      .locator('a[href*="/edit"]')
      .getAttribute("href");
    const legacyCategoryID = legacyCategoryEditHref?.match(
      /\/categories\/(\d+)\/edit/,
    )?.[1];
    if (!legacyCategoryID) {
      throw new Error(
        `Could not resolve ${legacyCategoryName} from ${legacyCategoryEditHref ?? "missing href"} at ${page.url()}`,
      );
    }

    await legacyCategory
      .getByRole("button", {
        name: `Actions for ${legacyCategoryName}`,
        exact: true,
      })
      .click();
    const legacyCategoryMenu = legacyCategory.locator(
      ".category-overflow-menu:not([hidden])",
    );
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
    await expect(legacyCategoryDrawer.locator("#category-name")).toHaveValue(
      legacyCategoryName,
    );
    await page.reload();
    await expect(page).toHaveURL(legacyCategoryURL);
    await expect(
      page
        .getByRole("dialog", { name: /Edit category/i })
        .locator("#category-name"),
    ).toHaveValue(legacyCategoryName);
    await expect(page.locator("body")).not.toContainText("invalid identifier");
  });

  test("Structure separates contextual actions and preserves selection after an HTMX desk rename", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    await expect(page.locator(".category-level-categories")).toBeVisible();
    await expect(
      page.getByRole("link", { name: "New category", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "New department", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "New desk", exact: true }),
    ).toBeVisible();

    const assignedDepartment = page
      .locator(".category-level-departments .category-structure-row")
      .filter({
        has: page.getByText("General", { exact: true }),
      });
    await expect(assignedDepartment).toHaveCount(1);
    const assignedDepartmentHref =
      await assignedDepartment.getAttribute("href");
    const assignedDepartmentID =
      assignedDepartmentHref?.match(/department_id=(\d+)/)?.[1];
    if (!assignedDepartmentID) {
      throw new Error(
        `Could not resolve the General department from ${assignedDepartmentHref ?? "missing href"} at ${page.url()}`,
      );
    }
    await assignedDepartment.click();
    await expect(page).toHaveURL(
      new RegExp(`view=structure&department_id=${assignedDepartmentID}$`),
    );
    await expect(
      page.getByRole("link", { name: "New desk", exact: true }),
    ).toBeVisible();

    const assignedDesk = page
      .locator(".category-level-desks .category-structure-item")
      .filter({
        has: page.getByText("General", { exact: true }),
      });
    await expect(assignedDesk).toHaveCount(1);
    const assignedDeskHref = await assignedDesk
      .locator("a.category-structure-row")
      .getAttribute("href");
    const assignedDeskID = assignedDeskHref?.match(/desk_id=(\d+)/)?.[1];
    if (!assignedDeskID) {
      throw new Error(
        `Could not resolve the General desk from ${assignedDeskHref ?? "missing href"} at ${page.url()}`,
      );
    }
    await assignedDesk.locator("a.category-structure-row").click();
    await expect(page).toHaveURL(
      new RegExp(
        `view=structure&department_id=${assignedDepartmentID}&desk_id=${assignedDeskID}$`,
      ),
    );
    await expect(
      page.getByRole("link", { name: "New category", exact: true }),
    ).toBeVisible();
    await expect(page.locator(".category-mobile-back").first()).toBeHidden();

    const deskMenu = assignedDesk.getByRole("button", {
      name: "Actions for General",
      exact: true,
    });
    const focusKey = await deskMenu.getAttribute("data-focus-key");
    await deskMenu.click();
    await assertDrawerHtmxSwap(
      page,
      async () => {
        await assignedDesk
          .locator(".category-overflow-menu:not([hidden])")
          .getByRole("menuitem", { name: "Edit desk", exact: true })
          .click();
      },
      {
        endpoint: (url) => {
          const requestURL = new URL(url);
          return (
            requestURL.pathname ===
              `/categories/desks/${assignedDeskID}/edit` &&
            requestURL.searchParams.get("view") === "structure" &&
            requestURL.searchParams.get("department_id") ===
              assignedDepartmentID &&
            requestURL.searchParams.get("desk_id") === assignedDeskID
          );
        },
        expectedUrl: new RegExp(
          `/categories/desks/${assignedDeskID}/edit\\?view=structure&department_id=${assignedDepartmentID}&desk_id=${assignedDeskID}$`,
        ),
      },
    );
    const drawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(drawer).toBeVisible();
    const renamed = "Support desk " + Date.now();
    await drawer.locator("#category-name").fill(renamed);
    await drawer.getByRole("button", { name: /save changes/i }).click();

    await expect(page).toHaveURL(
      /\/categories\?department_id=\d+&desk_id=\d+&view=structure/,
    );
    await expect(
      page
        .locator(".category-level-desks .category-structure-row")
        .filter({ hasText: renamed }),
    ).toBeVisible();
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

  test("Structure uses progressive drill-down and Back controls only on mobile", async ({
    page,
  }) => {
    await loginAsSeeded(page);
    await page.setViewportSize({ width: 390, height: 800 });
    await page.goto(base() + "/categories?view=structure");
    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeHidden();
    await expect(page.locator(".category-level-categories")).toBeHidden();
    await page
      .locator(".category-level-departments .category-structure-row")
      .first()
      .click();
    await expect(page.locator(".category-level-departments")).toBeHidden();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    await expect(
      page.getByRole("link", { name: "← Departments", exact: true }),
    ).toBeVisible();
    await page
      .locator(".category-level-desks .category-structure-row")
      .first()
      .click();
    await expect(page.locator(".category-level-desks")).toBeHidden();
    await expect(page.locator(".category-level-categories")).toBeVisible();
    await expect(
      page.getByRole("link", { name: "← Desks", exact: true }),
    ).toBeVisible();
  });

  test("lowest desk overflow menu keeps Edit desk reachable inside the catalog card", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    const department = page
      .locator(".category-level-departments .category-structure-row")
      .filter({
        has: page.getByText("General", { exact: true }),
      });
    await expect(department).toHaveCount(1);
    const departmentHref = await department.getAttribute("href");
    const departmentID = departmentHref?.match(/department_id=(\d+)/)?.[1];
    if (!departmentID) {
      throw new Error(
        `Could not resolve the General department from ${departmentHref ?? "missing href"} at ${page.url()}`,
      );
    }
    await department.click();

    // Additional rows put the final desk action near the bottom of the 800px viewport.
    for (let index = 0; index < 10; index += 1) {
      const name = `Overflow desk ${Date.now()}-${index}`;
      await page
        .locator(".category-level-desks")
        .getByRole("link", { name: "New desk", exact: true })
        .click();
      const drawer = page.getByRole("dialog", { name: /New desk/i });
      await expect(drawer).toBeVisible();
      await drawer
        .locator("select[name=department_id]")
        .selectOption(departmentID);
      await drawer.locator("#category-name").fill(name);
      await drawer.getByRole("button", { name: /create desk/i }).click();
      await expect(
        page
          .locator(".category-level-desks .category-structure-item")
          .filter({ hasText: name }),
      ).toHaveCount(1);
    }

    const targetName = `Overflow desk ${Date.now()}-unreachable`;
    await page
      .locator(".category-level-desks")
      .getByRole("link", { name: "New desk", exact: true })
      .click();
    const drawer = page.getByRole("dialog", { name: /New desk/i });
    await expect(drawer).toBeVisible();
    await drawer
      .locator("select[name=department_id]")
      .selectOption(departmentID);
    await drawer.locator("#category-name").fill(targetName);
    await drawer.getByRole("button", { name: /create desk/i }).click();

    const deskItems = page.locator(
      ".category-level-desks .category-structure-item",
    );
    await expect(deskItems.last()).toContainText(targetName);
    const targetRow = deskItems.filter({ hasText: targetName });
    await expect(targetRow).toHaveCount(1);
    const menuButton = targetRow.getByRole("button", {
      name: `Actions for ${targetName}`,
      exact: true,
    });
    const menu = targetRow.locator(".category-overflow-menu");
    await menuButton.click();
    await expect(menu).toHaveClass(/up/);

    const edit = menu.getByRole("menuitem", { name: "Edit desk", exact: true });
    await expect(edit).toBeVisible();
    const editBox = await edit.boundingBox();
    if (!editBox)
      throw new Error(
        `Edit desk menuitem has no bounding box at ${page.url()}`,
      );
    expect(editBox.y).toBeGreaterThanOrEqual(0);
    expect(editBox.y + editBox.height).toBeLessThanOrEqual(800);
    const hitTarget = await page.evaluate(
      ({ x, y }) => {
        const element = document.elementFromPoint(x, y);
        return (
          element?.closest('[role="menuitem"]')?.textContent?.trim() ?? null
        );
      },
      { x: editBox.x + editBox.width / 2, y: editBox.y + editBox.height / 2 },
    );
    expect(hitTarget).toBe("Edit desk");
  });

  test("categories show a neutral status for categories without workflows", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Unconfigured " + Date.now();
    await createCategoryViaUi(page, name);
    const row = page
      .locator(".category-level-categories .category-structure-item")
      .filter({ hasText: name });
    await expect(
      row.getByText("Not configured", { exact: true }),
    ).toBeVisible();
    await expect(row.locator(".category-status-inline")).toBeVisible();
  });

  test("create, clear description, and delete a category", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const catName = "Probe Cat " + Date.now();
    await createCategoryViaUi(page, catName);
    await expect(
      page
        .locator(".category-level-categories .category-structure-item")
        .filter({ hasText: catName }),
    ).toBeVisible();

    const row = page
      .locator(".category-level-categories .category-structure-item")
      .filter({ hasText: catName });
    await expect(row).toHaveCount(1);
    await row.getByRole("button", { name: /actions for/i }).click();
    await row
      .getByRole("menuitem", { name: "Edit category", exact: true })
      .click();
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();
    const description = drawer.getByLabel("Description", { exact: true });
    await expect(description).toHaveValue("");
    const renamed = catName + " Renamed";
    await drawer.locator("#category-name").fill(renamed);
    await description.fill("Temporary description");
    await drawer.getByRole("button", { name: /save changes/i }).click();

    await expect(page).toHaveURL(/\/categories/);
    await expect(
      page
        .locator(".category-level-categories .category-structure-item")
        .filter({ hasText: renamed }),
    ).toBeVisible();

    const editedRow = page
      .locator(".category-level-categories .category-structure-item")
      .filter({ hasText: renamed });
    await editedRow.getByRole("button", { name: /actions for/i }).click();
    await editedRow
      .getByRole("menuitem", { name: "Edit category", exact: true })
      .click();
    const clearedDrawer = page.getByRole("dialog", { name: /Edit category/i });
    await clearedDrawer.getByLabel("Description", { exact: true }).fill("");
    await clearedDrawer.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(
      page
        .locator(".category-level-categories .category-structure-item")
        .filter({ hasText: renamed }),
    ).toBeVisible();

    const delRow = page
      .locator(".category-level-categories .category-structure-item")
      .filter({ hasText: renamed });
    await expect(delRow).toHaveCount(1);
    await delRow.getByRole("button", { name: /actions for/i }).click();
    await delRow.getByRole("menuitem", { name: /delete category/i }).click();
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

      test("category duplicate marks only name, keeps the draft, and saves after Stay", async ({ page }) => {
        await page.setViewportSize({ width: 1280, height: 800 });
        await loginAsSeeded(page);
        await page.goto(
          base() + "/categories/new?view=structure&department_id=1&desk_id=1",
        );

        const drawer = page.getByRole("dialog", { name: /New category/i });
            const name = drawer.getByLabel("Name", { exact: true });
            const description = drawer.getByLabel("Description", { exact: true });
            await expect(drawer).toHaveAttribute(
              "aria-describedby",
              "category-drawer-description",
            );
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
        await drawer
          .getByRole("button", { name: "Close catalog details", exact: true })
          .click();
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
            expectedUrl:
              /\/categories\?department_id=1&desk_id=1&view=structure$/,
          },
        );
        await expect(drawer).toHaveCount(0);
        await expect(
          page
            .locator(".category-level-categories .category-structure-item")
            .filter({ hasText: categoryName }),
        ).toBeVisible();
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
    await expect(discard).toHaveCSS("background-color", "rgb(141, 57, 72)");
    await expect(discard).toHaveCSS("border-color", "rgb(141, 57, 72)");
    await expect(discard).toHaveCSS("color", "rgb(255, 255, 255)");
    await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
    await expect(confirmation).toBeHidden();
    await expect(name).toBeFocused();
    await expect(page).toHaveURL(drawerURL);
  });

  test("dirty category drawer keeps values and URL until discard", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");
    await page
      .getByRole("link", { name: /General General ticket requests/i })
      .click();
        await page
          .locator('a[href="/categories?view=structure&department_id=1&desk_id=1"]')
          .click();

    const launcher = page.getByRole("link", {
      name: "New category",
      exact: true,
    });
    await launcher.click();
    const drawer = page.getByRole("dialog", { name: /New category/i });
    const name = drawer.getByLabel("Name", { exact: true });
    const close = drawer.getByRole("button", {
      name: "Close catalog details",
      exact: true,
    });
    await expect(page).toHaveURL(
      /\/categories\/new\?view=structure&department_id=1&desk_id=1$/,
    );
    let drawerURL = page.url();
    await name.fill("Unsaved category");

    await close.click();
    const confirmation = page.getByRole("dialog", {
      name: "Leave without saving?"
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
        await expect(page).toHaveURL(
          /\/categories\?view=structure&department_id=1&desk_id=1$/,
        );
        await launcher.click();
        await expect(drawer).toBeVisible();
        await expect(page).toHaveURL(
          /\/categories\/new\?view=structure&department_id=1&desk_id=1$/,
        );
        drawerURL = page.url();
        await name.fill("Unsaved category");
        await page.goBack();
    await expect(confirmation).toBeVisible();
    await expect(page).toHaveURL(drawerURL);
    await confirmation.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(drawer).toHaveCount(0);
    await expect(page).toHaveURL(
      /\/categories\?view=structure&department_id=1&desk_id=1$/,
    );
    await expect(launcher).toBeFocused();
  });

  test("dirty department and desk drawers require an explicit discard", async ({ page }) => {
        await loginAsSeeded(page);
        await page.goto(base() + "/categories/departments/1/edit?view=structure");

            const department = page.getByRole("dialog", { name: /Edit department/i });
            const departmentName = department.getByLabel("Name", { exact: true });
            await expect(department).toHaveAttribute(
              "aria-describedby",
              "department-drawer-description",
            );
            await expect(department.locator("#department-drawer-description")).toHaveText(
              "Use departments to group desks that support the same part of the organization.",
            );
            await expect(departmentName).toHaveAttribute(
              "aria-describedby",
              "department-name-help",
            );
            await expect(department.locator("#department-name-help")).toHaveText(
              "Department names must be globally unique.",
            );
            await departmentName.fill("Unsaved department");
        await department.getByRole("button", { name: "Close catalog details" }).click();

        const confirmation = page.getByRole("dialog", {
          name: "Leave without saving?",
        });
        await expect(confirmation).toBeVisible();
        await expect(
          confirmation.getByRole("button", { name: "Stay", exact: true }),
        ).toBeFocused();
        await confirmation.getByRole("button", { name: "Stay", exact: true }).click();
        await expect(departmentName).toHaveValue("Unsaved department");
        await expect(page).toHaveURL(/\/categories\/departments\/1\/edit/);
        await department.getByRole("button", { name: "Close catalog details" }).click();
        await confirmation
          .getByRole("button", { name: "Discard changes", exact: true })
          .click();
        await expect(department).toHaveCount(0);

        await page.goto(
          base() + "/categories/desks/1/edit?view=structure&department_id=1&desk_id=1",
        );
            const desk = page.getByRole("dialog", { name: /Edit desk/i });
            const description = desk.getByLabel("Description", { exact: true });
            const deskName = desk.getByLabel("Name", { exact: true });
            await expect(desk).toHaveAttribute(
              "aria-describedby",
              "desk-drawer-description",
            );
            await expect(desk.locator("#desk-drawer-description")).toHaveText(
              "Use desks to group categories for the team that handles them.",
            );
            await expect(deskName).toHaveAttribute("aria-describedby", "desk-name-help");
            await expect(desk.locator("#desk-name-help")).toHaveText(
              "Desk names must be globally unique.",
            );
            await description.fill("Unsaved desk description");
        await desk.getByRole("button", { name: "Cancel", exact: true }).click();
        await expect(confirmation).toBeVisible();
        await confirmation
          .getByRole("button", { name: "Discard changes", exact: true })
          .click();
        await expect(desk).toHaveCount(0);
        await expect(page).toHaveURL(/\/categories\?(?=.*view=structure)(?=.*department_id=1)(?=.*desk_id=1)/);
      });

      test("history restoration does not revive a stale category save confirmation", async ({ page }) => {
        test.setTimeout(30_000);
        await page.setViewportSize({ width: 1280, height: 800 });
        await loginAsSeeded(page);
        const listPath = "/categories?view=structure&department_id=1&desk_id=1";
        await page.goto(base() + listPath);

        const launcher = page.getByRole("link", {
          name: "New category",
          exact: true,
        });
        await launcher.click();
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

        await assertDrawerHtmxSwap(
          page,
          () => launcher.click(),
          {
            endpoint: (url) => new URL(url).pathname === "/categories/new",
            expectedUrl: /\/categories\/new\?view=structure&department_id=1&desk_id=1$/,
          },
        );
        await expect(drawer).toBeVisible();
        await page.waitForTimeout(5_100);

        await page.goBack();
        await expect.poll(() => new URL(page.url()).pathname).toBe("/categories");
        expect([...new URL(page.url()).searchParams.entries()].sort()).toEqual(
          [...new URL(base() + listPath).searchParams.entries()].sort(),
        );
        await expect(page.locator("h1").filter({ hasText: "Categories" })).toBeVisible();
        await expect(page.getByRole("dialog", { name: /New category/i })).toHaveCount(0);
        await expect(
          page
            .locator(".category-level-categories .category-structure-item")
            .filter({ hasText: categoryName }),
        ).toBeVisible();
        await expect(feedback).toBeHidden();
      });

      test("an aborted read-only workflow step selection cannot suppress a concurrent save failure", async ({ page }) => {
        await page.setViewportSize({ width: 1280, height: 800 });
        await loginAsSeeded(page);

        const categoryID = await createCategoryViaUi(page, "Selection feedback " + Date.now().toString(36).slice(2, 8));
        const workflowPath = `/categories/${categoryID}/workflow`;
        await page.goto(base() + workflowPath);

        for (let step = 0; step < 2; step += 1) {
          await page.locator(".workflow-add-popover summary").last().click();
          const response = await assertHtmxSwap(
            page,
            () => page.getByRole("button", { name: "Manual task", exact: true }).last().click(),
            {
              endpoint: (url) => {
                const requestURL = new URL(url);
                return requestURL.pathname === workflowPath && requestURL.searchParams.get("add_step_type") === "manual_task";
              },
              method: "POST",
              expectedStatus: 200,
              hxTarget: "#workflow-builder",
            },
          );
          expect(new URLSearchParams(response.request().postData() ?? "").get("action")).toBe("add_step");
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
              if (isHtmxPost(request, { path: workflowPath, action: "save", target: "#workflow-builder" })) {
                markSaveRequestSeen();
                await new Promise<void>((resolve) => {
                  releaseSaveAbort = resolve;
                });
                await route.abort("failed");
                saveAborted = true;
                return;
              }
              if (isHtmxPost(request, { path: workflowPath, action: "select_step", target: "#workflow-builder" })) {
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
              await page
                .locator('.page-actions button[name="action"][value="save"]')
                .click();
              await saveRequest;
              await saveRequestSeen;

              const selectionRequest = page.waitForRequest((request) =>
                isHtmxPost(request, { path: workflowPath, action: "select_step", target: "#workflow-builder" }),
              );
              await page.locator(".workflow-step-card-link").nth(0).click();
              const request = await selectionRequest;
              expect(request.headers()["hx-request"]).toBe("true");
              expect(request.headers()["hx-target"]).toBe("workflow-builder");
              expect(new URLSearchParams(request.postData() ?? "").get("action")).toBe("select_step");
              await expect.poll(() => selectionAborted).toBe(true);

              releaseSaveAbort();
              await expect.poll(() => saveAborted).toBe(true);
              await expect(feedback.locator(".save-feedback-message")).toHaveText("Unable to save changes. Please try again.");
              await expect(feedback).toHaveAttribute("role", "alert");
              await expect(instructions).toHaveValue(attemptedValue);
            } finally {
              releaseSaveAbort();
              await page.unroute((url) => url.pathname === workflowPath, abortConcurrentRequests);
            }
      });

      test("workflow feedback retires success for validation and a read-only selection cannot suppress a transport failure", async ({ page }) => {
        test.setTimeout(60_000);
        await page.setViewportSize({ width: 1280, height: 800 });
        await loginAsSeeded(page);

        const categoryName = "Feedback flow " + Date.now().toString(36).slice(2, 8);
        await createCategoryViaUi(page, categoryName);
        const category = page
          .locator(".category-level-categories .category-structure-item")
          .filter({ hasText: categoryName });
        const editHref = await category.locator('a[href*="/edit"]').getAttribute("href");
        const categoryID = editHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
        if (!categoryID) throw new Error(`Could not resolve workflow category at ${page.url()}`);
        const workflowPath = `/categories/${categoryID}/workflow`;
        await page.goto(base() + workflowPath);

        await page.locator(".workflow-add-step summary").first().click();
        const addResponse = await assertHtmxSwap(
          page,
          () => page.getByRole("button", { name: "Manual task", exact: true }).first().click(),
          {
            endpoint: (url) => {
              const requestURL = new URL(url);
              return requestURL.pathname === workflowPath && requestURL.searchParams.get("add_step_type") === "manual_task";
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
          { endpoint: workflowPath, method: "POST", expectedStatus: 422, hxTarget: "#workflow-builder" },
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
              return form.get("action") === "save" && form.get("step_0_instructions") === recoveredInstructions;
            });
            await assertHtmxSwap(
              page,
              async () => {
                await instructions.fill(recoveredInstructions);
                await page
                  .locator('.page-actions button[name="action"][value="save"]')
                  .click();
              },
              { endpoint: workflowPath, method: "POST", expectedStatus: 200, hxTarget: "#workflow-builder" },
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
                  await page
                    .locator('.page-actions button[name="action"][value="save"]')
                    .click();
                  await expect.poll(() => aborted).toBe(true);
              const feedback = page.locator("#save-feedback");
              await expect(feedback.locator(".save-feedback-message")).toHaveText("Unable to save changes. Please try again.");
              await expect(feedback).toHaveAttribute("role", "alert");
              await expect(feedback).toHaveAttribute("aria-live", "assertive");
                  await expect(instructions).toHaveValue("This value stays in the editor");
                  await page.waitForTimeout(5_100);
                  await expect(feedback).toBeVisible();
                } finally {
                  await page.unroute(abortMatcher, abortRequest);
                }
          });

          test("workflow save and publish update the same coalesced toast and restart its timer", async ({ page }) => {
            test.setTimeout(30_000);
            await page.setViewportSize({ width: 1280, height: 800 });
            await loginAsSeeded(page);

            const categoryName = "Coalesced toast " + Date.now().toString(36).slice(2, 8);
            await createCategoryViaUi(page, categoryName);
            const category = page
              .locator(".category-level-categories .category-structure-item")
              .filter({ hasText: categoryName });
            const editHref = await category.locator('a[href*="/edit"]').getAttribute("href");
            const categoryID = editHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
            if (!categoryID) throw new Error(`Could not resolve workflow category at ${page.url()}`);
            const workflowPath = `/categories/${categoryID}/workflow`;
            await page.goto(base() + workflowPath);

            await page.locator(".workflow-add-step summary").first().click();
            const addResponse = await assertHtmxSwap(
              page,
              () => page.getByRole("button", { name: "Manual task", exact: true }).first().click(),
              {
                endpoint: (url) => {
                  const requestURL = new URL(url);
                  return requestURL.pathname === workflowPath && requestURL.searchParams.get("add_step_type") === "manual_task";
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

                // An explicit Save arrives before the first toast expires: the same
                // element must update in place (no exit/re-enter, no second region)
                // and restart the timer.
                const instructions = page.getByLabel("Instructions", { exact: true });
                await expect(instructions).toBeVisible();
                await page.waitForTimeout(3_000);
                await assertHtmxSwap(
                  page,
                  async () => {
                    await instructions.fill("Coalesced save");
                    await page
                      .locator('.page-actions button[name="action"][value="save"]')
                      .click();
                  },
                  { endpoint: workflowPath, method: "POST", expectedStatus: 200, hxTarget: "#workflow-builder" },
                );
            await expect(toast.locator(".save-feedback-message")).toHaveText("Saved");
            await expect(toast).toHaveClass(/(?:^|\s)is-visible(?:\s|$)/);
            await expect(toast).toBeVisible();
            await expect(page.locator("#save-feedback")).toHaveCount(1);

            // Past the original 5s window the restarted timer must still hold the toast open.
            await page.waitForTimeout(2_500);
            await expect(toast).toBeVisible();

            // Publishing re-renders the same draft, so the builder HTML is
            // byte-identical; the server contract is the exact POST plus the
            // Published feedback header, and the visible result is the toast.
            const publishResponse = await assertHtmxNoSwap(
              page,
              () => page.getByRole("button", { name: "Publish", exact: true }).click(),
              { endpoint: workflowPath, method: "POST", expectedStatus: 200 },
            );
            expect(publishResponse.headers()["x-save-feedback"]).toContain("Published");
            await expect(toast.locator(".save-feedback-message")).toHaveText("Published");
            await expect(page.locator("#save-feedback")).toHaveCount(1);
            await page.waitForTimeout(5_100);
            await expect(toast).toBeHidden();
          });

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
    await expect(
      page
        .locator(".category-level-categories .category-structure-item")
        .filter({ hasText: catName }),
    ).toBeVisible();

    // 2) open its workflow
    const catRow = page
      .locator(".category-level-categories .category-structure-item")
      .filter({ hasText: catName });
    await expect(catRow).toHaveCount(1);
    const editHref = await catRow
      .locator('a[href*="/edit"]')
      .getAttribute("href");
    const m = editHref?.match(/\/categories\/(\d+)\/edit/);
    if (!m) throw new Error("cannot extract category id for " + catName);
    const categoryId = m[1];
    const workflowPath = `/categories/${categoryId}/workflow`;
    await page.goto(base() + workflowPath);
    await expect(page.locator("#workflow-builder")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator("h2#workflow-builder-title")).toContainText(
      /workflow steps/i,
    );
    await expect(page.locator(".workflow-step-rail")).toBeVisible();

    // Ensure workflow builder form carries HTMX contract (complementary evidence)
    await expect(page.locator("#workflow-builder form")).toHaveAttribute(
      "hx-post",
      /\/workflow/,
    );
    await expect(page.locator("#workflow-builder form")).toHaveAttribute(
      "hx-target",
      "#workflow-builder",
    );

    // 3) add a VALID step — Manual task with instructions is valid by default
    const cards = page.locator(".workflow-step-card");
    const countBeforeAdd = await cards.count();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const addBtn = page
      .locator(".workflow-add-options button")
      .filter({ hasText: "Manual task" })
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
    await expect(
      page.locator("#save-feedback .save-feedback-message"),
    ).toHaveText("Saved");

    // Editing must not autosave: no workflow POST may fire from input alone
    const instructionsInput = page.getByLabel(/instructions/i);
    await expect(instructionsInput).toBeVisible({ timeout: 10000 });
    let autosaved = false;
    const onAutosave = (request: Request) => {
      autosaved ||=
        request.method() === "POST" &&
        new URL(request.url()).pathname ===
          `/categories/${categoryId}/workflow`;
    };
    page.on("request", onAutosave);
    await instructionsInput.fill("Handle the ticket");
    await page.waitForTimeout(700);
    page.off("request", onAutosave);
    expect(autosaved, "editing must not issue autosave requests").toBe(false);

    // Explicit Save submits the complete draft and confirms persistence
    const saveButton = page.locator(
      '.page-actions button[name="action"][value="save"]',
    );
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
    await expect(
      page.locator("#save-feedback .save-feedback-message"),
    ).toHaveText("Saved");

    // Remove step unconditionally (prove removal works)
    const countBeforeRemove = await cards.count();
    const lastCard = cards.last();
    const menuSummary = lastCard.locator(".workflow-trigger").first();
    await expect(menuSummary).toBeVisible();
    await menuSummary.click();
    const removeBtn = lastCard.getByRole("button", { name: /remove step/i });
    await expect(removeBtn).toBeVisible();

    // Remove-step POST goes to /categories/{id}/workflow?step_index=... —
    // the action=remove_step lives in the form body, not the query string.
    await assertHtmxSwap(
      page,
      async () => {
        await removeBtn.click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === `/categories/${categoryId}/workflow` &&
            parsedURL.searchParams.get("step_index") ===
              String(countBeforeRemove - 1) &&
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
    await expect(
      page.locator("#save-feedback .save-feedback-message"),
    ).toHaveText("Published");
    // After publish, no inline errors
    await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);

    // 5) reload and verify persistence — step count survives reload
    const countAfterPublish = await cards.count();
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator(".workflow-step-card")).toHaveCount(
      countAfterPublish,
    );
    // Badge on /categories should now show Published for this category
    await page.goto(base() + "/categories?view=structure");
    await page
      .locator(".category-level-departments .category-structure-row")
      .first()
      .click();
    await page
      .locator(".category-level-desks .category-structure-row")
      .first()
      .click();
    await expect(
      page
        .locator(".category-level-categories .category-structure-item")
        .filter({ hasText: catName })
        .locator(".category-status-inline"),
    ).toContainText(/published/i);

    // 6) create a ticket using that category
    const ticketTitle = "FlowTicket " + Date.now().toString(36).slice(2, 8);
    const ticketId = await createTicketViaUi(page, {
      title: ticketTitle,
      description: "workflow published probe",
      category: catName,
      priority: "high",
    });

    // 7) verify the published workflow appears as a passive timeline item
    // because the newly created ticket has no assigned agent yet.
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText(catName);
    await expect(page.locator("#workflow-pending")).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.locator("#workflow-pending")).toHaveClass(
      /workflow-pending-info/,
    );
    await expect(page.locator("#workflow-pending")).toContainText(
      "IN PROGRESS",
    );
    await expect(page.locator("#workflow-pending")).toContainText(
      "The assigned agent is handling this task.",
    );
    await expect(page.locator("#workflow-pending")).toContainText(
      "Updates will appear here when complete.",
    );
    await expect(
      page.locator("#workflow-pending .workflow-instruction"),
    ).toHaveCount(0);
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
    await expect(page.locator("#workflow-pending")).toHaveClass(
      /workflow-pending-info/,
    );
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
    const assignee = page.locator("#assign-user");
    await expect(assignee).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await assignee.selectOption({ label: "Alice Admin" });
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
    await expect(pending.locator("h3")).toHaveText("CURRENT TASK");
    await expect(pending.locator(".workflow-instruction")).toContainText(
      "Handle the ticket",
    );
    await expect(pending.getByLabel("Solution (optional)")).toBeVisible();
    await expect(
      pending.getByRole("button", { name: "Complete" }),
    ).toBeVisible();
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
      const completedManual = page
        .locator("#timeline .timeline-entry.timeline-manual")
        .first();
      await expect(completedManual).toHaveCount(1);
      await expect(completedManual).toBeVisible();
      const checkIcon = completedManual.locator(
        ".timeline-manual-heading .event-icon",
      );
      await expect(checkIcon.locator("svg")).toBeVisible();
      await expect(checkIcon).toHaveCSS("color", "rgb(24, 115, 77)");
      await expect(
        completedManual.locator(".timeline-manual-heading .main"),
      ).toHaveText("Alice Admin completed the task");
      await expect(
        completedManual.getByText("TASK", { exact: true }),
      ).toHaveCount(1);
      await expect(completedManual.locator("dd").first()).toHaveText(
        "Handle the ticket",
      );
      await expect(completedManual.locator("dl")).toBeVisible();
      await expect(completedManual.locator(".when")).toBeVisible();
      await expect(
        completedManual.getByText("SOLUTION", { exact: true }),
      ).toHaveCount(0);
      await expect(
        completedManual.locator(
          "details, summary, button, .timeline-event-summary, [open], [aria-expanded], [aria-controls], [tabindex]",
        ),
      ).toHaveCount(0);
      await expect(completedManual.locator(".event-icon")).not.toContainText(
        "›",
      );
      await expect(completedManual.locator(".when")).not.toContainText(
        "Alice Admin",
      );
      await expect(completedManual).not.toHaveCSS("cursor", "pointer");
      await expect(
        completedManual.locator(".timeline-manual-heading"),
      ).not.toHaveCSS("cursor", "pointer");
      const iconBefore = await completedManual
        .locator(".event-icon")
        .evaluate((element) => getComputedStyle(element, "::before").content);
      expect(iconBefore).toBe("none");
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

  test.describe("Workflow dirty structural guard", () => {
    function trackPosts(page: Page): string[] {
      const actions: string[] = [];
      page.on("request", (request) => {
        if (request.method() !== "POST" || !/\/categories\/\d+\/workflow/.test(new URL(request.url()).pathname)) return;
        actions.push(request.postData()?.match(/(?:^|&)action=([^&]*)/)?.[1] ?? "");
      });
      return actions;
    }
    async function seedWorkflow(page: Page, steps: string[]): Promise<string> {
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(base() + "/categories/new");
      if (page.url().includes("/login")) await loginAsSeeded(page);
      const categoryId = await createCategoryViaUi(page, "Guard " + Date.now().toString(36) + steps.length);
      await page.goto(base() + `/categories/${categoryId}/workflow`);
      await expect(page.locator("#workflow-builder")).toBeVisible();
      for (const step of steps) {
        await page.locator(".workflow-add-step summary").first().click();
        const filter = step === "manual_task" ? /manual task/i : step === "form" ? /^form$/i : step === "close_ticket" ? /close ticket/i : /resolve ticket/i;
        await page.locator(".workflow-add-options button").filter({ hasText: filter }).first().click();
        await expect(page.locator(".workflow-add-options")).not.toBeVisible();
      }
      return categoryId;
    }
    async function selectCard(page: Page, index: number): Promise<void> {
      const responsePromise = page.waitForResponse((r) => r.request().method() === "POST" && r.url().includes("/workflow") && (r.request().postData() ?? "").includes("action=select_step"));
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
        const manualButton = page.locator(".workflow-add-options button").filter({ hasText: /manual task/i }).first();
        if (!(await manualButton.isVisible().catch(() => false))) await page.locator(".workflow-add-step summary").first().click();
        await manualButton.click();
      },
      remove_step: async (page) => {
        const card = page.locator(".workflow-step-card").last();
        await openMenu(card.locator(".workflow-step-menu-actions"));
        await card.getByRole("button", { name: /remove step/i }).click();
      },
      move_up: async (page) => {
        const card = page.locator(".workflow-step-card").nth(1);
        await openMenu(card.locator(".workflow-step-menu-actions"));
        await card.getByRole("button", { name: /move left/i }).click();
      },
      move_down: async (page) => {
        const card = page.locator(".workflow-step-card").first();
        await openMenu(card.locator(".workflow-step-menu-actions"));
        await card.getByRole("button", { name: /move right/i }).click();
      },
      reorder: async (page) => {
        // The drag listeners funnel the drop into the hidden reorder submitter.
        await page.evaluate(() => {
          const transfer = new DataTransfer();
          const handle = document.querySelectorAll(".workflow-drag-handle")[0];
          const target = document.querySelectorAll(".workflow-step-card")[1];
          const at = { bubbles: true, dataTransfer: transfer, clientX: target.getBoundingClientRect().right - 10 };
          handle.dispatchEvent(new DragEvent("dragstart", { bubbles: true, dataTransfer: transfer }));
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
    const fixtures: Array<{ action: string; steps: string[]; select: number; dirty: (page: Page) => Promise<void> }> = [
      { action: "add_step", steps: ["manual_task", "form"], select: 0, dirty: async (page) => { await page.getByLabel(/^instructions/i).fill("DIRTY-add_step"); } },
      { action: "remove_step", steps: ["manual_task", "form"], select: 0, dirty: async (page) => { await page.getByLabel(/^instructions/i).fill("DIRTY-remove_step"); } },
      { action: "move_up", steps: ["manual_task", "form"], select: 0, dirty: async (page) => { await page.getByLabel(/^instructions/i).fill("DIRTY-move_up"); } },
      { action: "move_down", steps: ["manual_task", "form"], select: 0, dirty: async (page) => { await page.getByLabel(/^instructions/i).fill("DIRTY-move_down"); } },
      { action: "reorder", steps: ["manual_task", "form"], select: 0, dirty: async (page) => { await page.getByLabel(/^instructions/i).fill("DIRTY-reorder"); } },
      { action: "add_field", steps: ["form"], select: 0, dirty: async (page) => { await page.locator(".workflow-field-required input").check(); } },
      { action: "remove_field", steps: ["form"], select: 0, dirty: async (page) => { await page.getByLabel(/^label$/i).fill("DIRTY-remove_field"); } },
      { action: "change_type", steps: ["manual_task", "close_ticket"], select: 1, dirty: async (page) => { await page.getByLabel(/^final type$/i).selectOption("resolve_ticket"); } },
    ];

    test("all 8 structural actions prompt when dirty and Cancel sends no POST", async ({ page }) => {
      test.setTimeout(240_000);
      for (const fixture of fixtures) {
        const categoryId = await seedWorkflow(page, fixture.steps);
        if (["add_field", "remove_field"].includes(fixture.action)) { await triggers.add_field(page, categoryId); await expect(page.locator(".workflow-field-row")).toHaveCount(1); }
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

    test("carried dirty survives select_step and Discard+continue restores persisted values", async ({ page }) => {
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
      await expect(page.locator(".workflow-step-card").first()).toContainText(/form/i);
      await page.reload();
      await expect(page.locator(".workflow-step-card").first()).toContainText(/form/i);
      await selectCard(page, 1);
      await expect(page.getByLabel(/^instructions/i)).toHaveValue("");
    });

    test("Escape restores focus and the dialog is styled at 390px", async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      await seedWorkflow(page, ["manual_task"]);
      await selectCard(page, 0);
      const dialog = page.locator("#workflow-dirty-dialog");
      await page.getByLabel(/^instructions/i).fill("DIRTY-escape");
      const addManual = page.locator(".workflow-add-options button").filter({ hasText: /manual task/i }).first();
      await page.locator(".workflow-add-step summary").first().click();
      await addManual.click();
      await expect(dialog).toBeVisible();
      await expect(dialog).toHaveCSS("border-radius", "12px");
      await expect(dialog.getByRole("button", { name: "Discard and continue" })).toHaveCSS("background-color", "rgb(141, 57, 72)");
      await page.keyboard.press("Escape");
      await expect(dialog).not.toBeVisible();
      await expect(addManual).toBeFocused();
    });

    test("a failed save stays dirty and never replays the action", async ({ page }) => {
      await seedWorkflow(page, ["manual_task"]);
      await selectCard(page, 0);
      await page.route("**/workflow", async (route) => {
        const save = route.request().method() === "POST" && (route.request().postData() ?? "").includes("action=save");
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
    async function openDirtyBuilder(
      page: Page,
      instructions: string,
    ): Promise<string> {
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(base() + "/categories/new");
      if (page.url().includes("/login")) await loginAsSeeded(page);
      const categoryId = await createCategoryViaUi(
        page,
        "Exit " + Date.now().toString(36),
      );
      await page.goto(base() + `/categories/${categoryId}/workflow`);
      await expect(page.locator("#workflow-builder")).toBeVisible();
      await page.locator(".workflow-add-step summary").first().click();
      await page
        .locator(".workflow-add-options button")
        .filter({ hasText: /manual task/i })
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
      const sidebarLink = page.locator('a.rail-link[href="/categories"]');
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
      await expect(
        dialog.getByRole("button", { name: "Discard changes" }),
      ).toHaveCSS("background-color", "rgb(141, 57, 72)");
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
      await page
        .locator(".workflow-step-card .workflow-step-card-link")
        .first()
        .click();
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

    test("reverted edits and a successful save clear the exit guard", async ({
      page,
    }) => {
      const categoryId = await openDirtyBuilder(page, "CLEAN-ME");
      const dialog = page.locator("#workflow-leave-dialog");
      const sidebarLink = page.locator('a.rail-link[href="/categories"]');
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
      await page
        .locator(".workflow-step-card .workflow-step-card-link")
        .first()
        .click();
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
      await page
        .locator('.page-actions button[name="action"][value="save"]')
        .click();
      await expect((await saveResponse).status()).toBe(200);
      await expect(
        page.locator("#save-feedback .save-feedback-message"),
      ).toHaveText("Saved");
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
      await expect(page).toHaveURL(
        new RegExp(`/categories/${categoryId}/workflow$`),
      );
    });
  });
});
