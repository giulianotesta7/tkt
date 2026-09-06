/**
 * Categories + Workflow Builder journeys.
 *
 * The published workflow version CAN be observed via #workflow-pending + .workflow-instruction
 * on the ticket detail page — no product changes needed.
 */

import { test, expect, type Page, type Request, type Response } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, base } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { assertHtmxNoSwap, assertHtmxSwap } from "./helpers/htmx.js";
import { createCategoryViaUi, createTicketViaUi, openWorkflowBuilder } from "./helpers/navigation.js";

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
    const responsePromise = page.waitForResponse((response) =>
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

  test("categories index shows seeded category with workflow badge", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories");
    await expect(page.locator("h1").filter({ hasText: "Categories" })).toBeVisible();
    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    const seededDesk = page.locator(".category-level-desks .category-structure-row").filter({
      has: page.getByText("General Support", { exact: true }),
    });
    await expect(seededDesk).toHaveCount(1);
    await expect(seededDesk).not.toContainText("Department:");
    await expect(seededDesk).not.toContainText(/\d+\s+categor(?:y|ies)/i);
    await expect(seededDesk.locator(".category-count")).toHaveCount(0);
    await expect(seededDesk).toHaveAttribute("href", /department_id=unassigned&desk_id=\d+/);
    await expect(page.locator(".category-level-categories")).toBeVisible();
    await expect(page.locator(".category-level-categories .category-empty")).toContainText("Select a desk");
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

  test("Unassigned drawer edits push reloadable legacy context", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    const unassignedContext = "/categories?view=structure&department_id=unassigned";
    await page.goto(base() + unassignedContext);

    const legacyDeskName = "General Support";
    const legacyDesk = page.locator(".category-level-desks .category-structure-item").filter({
      has: page.getByText(legacyDeskName, { exact: true }),
    });
    await expect(legacyDesk).toHaveCount(1);
    const legacyDeskHref = await legacyDesk.locator("a.category-structure-row").getAttribute("href");
    const legacyDeskID = legacyDeskHref?.match(/desk_id=(\d+)/)?.[1];
    if (!legacyDeskID) {
      throw new Error(`Could not resolve ${legacyDeskName} from ${legacyDeskHref ?? "missing href"} at ${page.url()}`);
    }

    await legacyDesk.getByRole("button", { name: `Actions for ${legacyDeskName}`, exact: true }).click();
    const legacyDeskMenu = legacyDesk.locator(".category-overflow-menu:not([hidden])");
    const legacyDeskEdit = legacyDeskMenu.getByRole("menuitem", { name: "Edit desk", exact: true });
    const legacyDeskURL = new RegExp(`/categories/desks/${legacyDeskID}/edit\\?view=structure&department_id=unassigned&desk_id=${legacyDeskID}$`);
    await assertDrawerHtmxSwap(page, async () => {
      await legacyDeskEdit.click();
    }, {
      endpoint: (url) => {
        const requestURL = new URL(url);
        return requestURL.pathname === `/categories/desks/${legacyDeskID}/edit` &&
          requestURL.searchParams.get("view") === "structure" &&
          requestURL.searchParams.get("department_id") === "unassigned" &&
          requestURL.searchParams.get("desk_id") === legacyDeskID;
      },
      expectedUrl: legacyDeskURL,
    });
    const legacyDeskDrawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(legacyDeskDrawer.locator("#category-name")).toHaveValue(legacyDeskName);
    await page.reload();
    await expect(page).toHaveURL(legacyDeskURL);
    await expect(page.getByRole("dialog", { name: /Edit desk/i }).locator("#category-name")).toHaveValue(legacyDeskName);
    await expect(page.locator("body")).not.toContainText("invalid identifier");

    await page.goto(base() + `${unassignedContext}&desk_id=${legacyDeskID}`);
    const legacyCategoryName = "Legacy Support Category";
    const legacyCategory = page.locator(".category-level-categories .category-structure-item").filter({
      has: page.getByText(legacyCategoryName, { exact: true }),
    });
    await expect(legacyCategory).toHaveCount(1);
    const legacyCategoryEditHref = await legacyCategory.locator('a[href*="/edit"]').getAttribute("href");
    const legacyCategoryID = legacyCategoryEditHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
    if (!legacyCategoryID) {
      throw new Error(`Could not resolve ${legacyCategoryName} from ${legacyCategoryEditHref ?? "missing href"} at ${page.url()}`);
    }

    await legacyCategory.getByRole("button", { name: `Actions for ${legacyCategoryName}`, exact: true }).click();
    const legacyCategoryMenu = legacyCategory.locator(".category-overflow-menu:not([hidden])");
    const legacyCategoryEdit = legacyCategoryMenu.getByRole("menuitem", { name: "Edit category", exact: true });
    const legacyCategoryURL = new RegExp(`/categories/${legacyCategoryID}/edit\\?view=structure&department_id=unassigned&desk_id=${legacyDeskID}$`);
    await assertDrawerHtmxSwap(page, async () => {
      await legacyCategoryEdit.click();
    }, {
      endpoint: (url) => {
        const requestURL = new URL(url);
        return requestURL.pathname === `/categories/${legacyCategoryID}/edit` &&
          requestURL.searchParams.get("view") === "structure" &&
          requestURL.searchParams.get("department_id") === "unassigned" &&
          requestURL.searchParams.get("desk_id") === legacyDeskID;
      },
      expectedUrl: legacyCategoryURL,
    });
    const legacyCategoryDrawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(legacyCategoryDrawer.locator("#category-name")).toHaveValue(legacyCategoryName);
    await page.reload();
    await expect(page).toHaveURL(legacyCategoryURL);
    await expect(page.getByRole("dialog", { name: /Edit category/i }).locator("#category-name")).toHaveValue(legacyCategoryName);
    await expect(page.locator("body")).not.toContainText("invalid identifier");
  });

  test("Structure separates contextual actions and preserves selection after an HTMX desk rename", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    await expect(page.locator(".category-level-categories")).toBeVisible();
    await expect(page.getByRole("link", { name: "New category", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "New department", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "New desk", exact: true })).toBeVisible();

    const assignedDepartment = page.locator(".category-level-departments .category-structure-row").filter({
      has: page.getByText("General", { exact: true }),
    });
    await expect(assignedDepartment).toHaveCount(1);
    const assignedDepartmentHref = await assignedDepartment.getAttribute("href");
    const assignedDepartmentID = assignedDepartmentHref?.match(/department_id=(\d+)/)?.[1];
    if (!assignedDepartmentID) {
      throw new Error(`Could not resolve the General department from ${assignedDepartmentHref ?? "missing href"} at ${page.url()}`);
    }
    await assignedDepartment.click();
    await expect(page).toHaveURL(new RegExp(`view=structure&department_id=${assignedDepartmentID}$`));
    await expect(page.getByRole("link", { name: "New desk", exact: true })).toBeVisible();

    const assignedDesk = page.locator(".category-level-desks .category-structure-item").filter({
      has: page.getByText("General", { exact: true }),
    });
    await expect(assignedDesk).toHaveCount(1);
    const assignedDeskHref = await assignedDesk.locator("a.category-structure-row").getAttribute("href");
    const assignedDeskID = assignedDeskHref?.match(/desk_id=(\d+)/)?.[1];
    if (!assignedDeskID) {
      throw new Error(`Could not resolve the General desk from ${assignedDeskHref ?? "missing href"} at ${page.url()}`);
    }
    await assignedDesk.locator("a.category-structure-row").click();
    await expect(page).toHaveURL(new RegExp(`view=structure&department_id=${assignedDepartmentID}&desk_id=${assignedDeskID}$`));
    await expect(page.getByRole("link", { name: "New category", exact: true })).toBeVisible();
    await expect(page.locator(".category-mobile-back").first()).toBeHidden();

    const deskMenu = assignedDesk.getByRole("button", { name: "Actions for General", exact: true });
    const focusKey = await deskMenu.getAttribute("data-focus-key");
    await deskMenu.click();
    await assertDrawerHtmxSwap(page, async () => {
      await assignedDesk.locator(".category-overflow-menu:not([hidden])").getByRole("menuitem", { name: "Edit desk", exact: true }).click();
    }, {
      endpoint: (url) => {
        const requestURL = new URL(url);
        return requestURL.pathname === `/categories/desks/${assignedDeskID}/edit` &&
          requestURL.searchParams.get("view") === "structure" &&
          requestURL.searchParams.get("department_id") === assignedDepartmentID &&
          requestURL.searchParams.get("desk_id") === assignedDeskID;
      },
      expectedUrl: new RegExp(`/categories/desks/${assignedDeskID}/edit\\?view=structure&department_id=${assignedDepartmentID}&desk_id=${assignedDeskID}$`),
    });
    const drawer = page.getByRole("dialog", { name: /Edit desk/i });
    await expect(drawer).toBeVisible();
    const renamed = "Support desk " + Date.now();
    await drawer.locator("#category-name").fill(renamed);
    await drawer.getByRole("button", { name: /save changes/i }).click();

    await expect(page).toHaveURL(/\/categories\?department_id=\d+&desk_id=\d+&view=structure/);
    await expect(page.locator(".category-level-desks .category-structure-row").filter({ hasText: renamed })).toBeVisible();
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

  test("Structure uses progressive drill-down and Back controls only on mobile", async ({ page }) => {
    await loginAsSeeded(page);
    await page.setViewportSize({ width: 390, height: 800 });
    await page.goto(base() + "/categories?view=structure");
    await expect(page.locator(".category-level-departments")).toBeVisible();
    await expect(page.locator(".category-level-desks")).toBeHidden();
    await expect(page.locator(".category-level-categories")).toBeHidden();
    await page.locator(".category-level-departments .category-structure-row").first().click();
    await expect(page.locator(".category-level-departments")).toBeHidden();
    await expect(page.locator(".category-level-desks")).toBeVisible();
    await expect(page.getByRole("link", { name: "← Departments", exact: true })).toBeVisible();
    await page.locator(".category-level-desks .category-structure-row").first().click();
    await expect(page.locator(".category-level-desks")).toBeHidden();
    await expect(page.locator(".category-level-categories")).toBeVisible();
    await expect(page.getByRole("link", { name: "← Desks", exact: true })).toBeVisible();
  });

  test("lowest desk overflow menu keeps Edit desk reachable inside the catalog card", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.goto(base() + "/categories?view=structure");

    const department = page.locator(".category-level-departments .category-structure-row").filter({
      has: page.getByText("General", { exact: true }),
    });
    await expect(department).toHaveCount(1);
    const departmentHref = await department.getAttribute("href");
    const departmentID = departmentHref?.match(/department_id=(\d+)/)?.[1];
    if (!departmentID) {
      throw new Error(`Could not resolve the General department from ${departmentHref ?? "missing href"} at ${page.url()}`);
    }
    await department.click();

    // Additional rows put the final desk action near the bottom of the 800px viewport.
    for (let index = 0; index < 10; index += 1) {
      const name = `Overflow desk ${Date.now()}-${index}`;
      await page.locator(".category-level-desks").getByRole("link", { name: "New desk", exact: true }).click();
      const drawer = page.getByRole("dialog", { name: /New desk/i });
      await expect(drawer).toBeVisible();
      await drawer.locator("select[name=department_id]").selectOption(departmentID);
      await drawer.locator("#category-name").fill(name);
      await drawer.getByRole("button", { name: /create desk/i }).click();
      await expect(page.locator(".category-level-desks .category-structure-item").filter({ hasText: name })).toHaveCount(1);
    }

    const targetName = `Overflow desk ${Date.now()}-unreachable`;
    await page.locator(".category-level-desks").getByRole("link", { name: "New desk", exact: true }).click();
    const drawer = page.getByRole("dialog", { name: /New desk/i });
    await expect(drawer).toBeVisible();
    await drawer.locator("select[name=department_id]").selectOption(departmentID);
    await drawer.locator("#category-name").fill(targetName);
    await drawer.getByRole("button", { name: /create desk/i }).click();

    const deskItems = page.locator(".category-level-desks .category-structure-item");
    await expect(deskItems.last()).toContainText(targetName);
    const targetRow = deskItems.filter({ hasText: targetName });
    await expect(targetRow).toHaveCount(1);
    const menuButton = targetRow.getByRole("button", { name: `Actions for ${targetName}`, exact: true });
    const menu = targetRow.locator(".category-overflow-menu");
    await menuButton.click();
    await expect(menu).toHaveClass(/up/);

    const edit = menu.getByRole("menuitem", { name: "Edit desk", exact: true });
    await expect(edit).toBeVisible();
    const editBox = await edit.boundingBox();
    if (!editBox) throw new Error(`Edit desk menuitem has no bounding box at ${page.url()}`);
    expect(editBox.y).toBeGreaterThanOrEqual(0);
    expect(editBox.y + editBox.height).toBeLessThanOrEqual(800);
    const hitTarget = await page.evaluate(({ x, y }) => {
      const element = document.elementFromPoint(x, y);
      return element?.closest('[role="menuitem"]')?.textContent?.trim() ?? null;
    }, { x: editBox.x + editBox.width / 2, y: editBox.y + editBox.height / 2 });
    expect(hitTarget).toBe("Edit desk");
  });

  test("categories show a neutral status for categories without workflows", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    const name = "Unconfigured " + Date.now();
    await createCategoryViaUi(page, name);
    const row = page.locator(".category-level-categories .category-structure-item").filter({ hasText: name });
    await expect(row.getByText("Not configured", { exact: true })).toBeVisible();
    await expect(row.locator(".category-status-inline")).toBeVisible();
  });

  test("create, rename, and delete a category", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const catName = "Probe Cat " + Date.now();
    await createCategoryViaUi(page, catName);
    await expect(page.locator(".category-level-categories .category-structure-item").filter({ hasText: catName })).toBeVisible();

    const row = page.locator(".category-level-categories .category-structure-item").filter({ hasText: catName });
    await expect(row).toHaveCount(1);
    await row.getByRole("button", { name: /actions for/i }).click();
    await row.getByRole("menuitem", { name: "Edit category", exact: true }).click();
    const drawer = page.getByRole("dialog", { name: /Edit category/i });
    await expect(drawer).toBeVisible();
    const renamed = catName + " Renamed";
    await drawer.locator("#category-name").fill(renamed);
    await drawer.getByRole("button", { name: /save changes/i }).click();
    await expect(page).toHaveURL(/\/categories/);
    await expect(page.locator(".category-level-categories .category-structure-item").filter({ hasText: renamed })).toBeVisible();

    const delRow = page.locator(".category-level-categories .category-structure-item").filter({ hasText: renamed });
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

  test("workflow builder integrated journey: create category, add step, publish, reload, create ticket, verify published workflow in ticket", async ({ page }) => {
    test.setTimeout(60000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // 1) create a category
    const catName = "FlowCat " + Date.now().toString(36).slice(2, 8);
    await createCategoryViaUi(page, catName);
    await expect(page.locator(".category-level-categories .category-structure-item").filter({ hasText: catName })).toBeVisible();

    // 2) open its workflow
    const catRow = page.locator(".category-level-categories .category-structure-item").filter({ hasText: catName });
    await expect(catRow).toHaveCount(1);
    const editHref = await catRow.locator('a[href*="/edit"]').getAttribute("href");
    const m = editHref?.match(/\/categories\/(\d+)\/edit/);
    if (!m) throw new Error("cannot extract category id for " + catName);
    const categoryId = m[1];
    const workflowPath = `/categories/${categoryId}/workflow`;
    await page.goto(base() + workflowPath);
    await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
    await expect(page.locator("h2#workflow-builder-title")).toContainText(/workflow steps/i);
    await expect(page.locator(".workflow-step-rail")).toBeVisible();

    // Ensure workflow builder form carries HTMX contract (complementary evidence)
    await expect(page.locator("#workflow-builder form")).toHaveAttribute("hx-post", /\/workflow/);
    await expect(page.locator("#workflow-builder form")).toHaveAttribute("hx-target", "#workflow-builder");

    // 3) add a VALID step — Manual task with instructions is valid by default
    const cards = page.locator(".workflow-step-card");
    const countBeforeAdd = await cards.count();
    const addSummary = page.locator(".workflow-add-step summary").first();
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    const addBtn = page.locator(".workflow-add-options button").filter({ hasText: "Manual task" }).first();
    await expect(addBtn).toBeVisible();

    await assertHtmxSwap(page, async () => {
      await addBtn.click();
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === `/categories/${categoryId}/workflow` && parsedURL.searchParams.get("add_step_type") === "manual_task";
      },
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    });
    await expect(cards).toHaveCount(countBeforeAdd + 1);
    await expect(page.locator("[data-workflow-live]")).toContainText(/added a step/i);

    // Configure the newly added manual_task step — instructions are required for publish
    const instructionsInput = page.getByLabel(/instructions/i);
    await expect(instructionsInput).toBeVisible({ timeout: 10000 });
    await expect(instructionsInput).toHaveAttribute("hx-trigger", "input changed delay:600ms");
    await expect(instructionsInput).toHaveAttribute("hx-swap", "none");
    await assertHtmxNoSwap(page, async () => {
      await instructionsInput.fill("Handle the ticket");
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === `/categories/${categoryId}/workflow` && parsedURL.search === "";
      },
      method: "POST",
      expectedStatus: 200,
    });
    await expect(page.locator("#workflow-builder")).toBeVisible();

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
    await assertHtmxSwap(page, async () => {
      await removeBtn.click();
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === `/categories/${categoryId}/workflow` &&
          parsedURL.searchParams.get("step_index") === String(countBeforeRemove - 1) &&
          !parsedURL.searchParams.has("action");
      },
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    });
    await expect(cards).toHaveCount(countBeforeRemove - 1);

    // Re-add a step so we have at least one to publish (workflow must be non-empty)
    const countBeforeReAdd = await cards.count();
    expect(countBeforeReAdd).toBe(0);
    await expect(addSummary).toBeVisible();
    await addSummary.click();
    await expect(addBtn).toBeVisible();
    await assertHtmxSwap(page, async () => {
      await addBtn.click();
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === `/categories/${categoryId}/workflow` && parsedURL.searchParams.get("add_step_type") === "manual_task";
      },
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    });
    await expect(cards).toHaveCount(1);
    const instr = page.getByLabel(/instructions/i);
    await expect(instr).toBeVisible();
    await expect(instr).toHaveAttribute("hx-trigger", "input changed delay:600ms");
    await expect(instr).toHaveAttribute("hx-swap", "none");
    if (await instr.inputValue() === "Handle the ticket") {
      await assertHtmxNoSwap(page, async () => {
        await instr.fill("Handle the ticket draft");
      }, {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return parsedURL.pathname === `/categories/${categoryId}/workflow` && parsedURL.search === "";
        },
        method: "POST",
        expectedStatus: 200,
      });
    }
    await assertHtmxNoSwap(page, async () => {
      await instr.fill("Handle the ticket");
    }, {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return parsedURL.pathname === `/categories/${categoryId}/workflow` && parsedURL.search === "";
      },
      method: "POST",
      expectedStatus: 200,
    });

    // 4) PUBLISH — must execute publication, not just check button exists
    const publishBtn = page.getByRole("button", { name: /publish/i });
    await expect(publishBtn).toBeVisible();
    const publishResp = await assertHtmxSwap(page, async () => {
      await publishBtn.click();
    }, {
      endpoint: `/categories/${categoryId}/workflow`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    });
    expect(publishResp.status()).toBe(200);
    // After publish, no inline errors
    await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);

    // 5) reload and verify persistence — step count survives reload
    const countAfterPublish = await cards.count();
    await page.reload();
    await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
    await expect(page.locator(".workflow-step-card")).toHaveCount(countAfterPublish);
    // Badge on /categories should now show Published for this category
    await page.goto(base() + "/categories?view=structure");
    await page.locator(".category-level-departments .category-structure-row").first().click();
    await page.locator(".category-level-desks .category-structure-row").first().click();
    await expect(page.locator(".category-level-categories .category-structure-item").filter({ hasText: catName }).locator(".category-status-inline")).toContainText(/published/i);

    // 6) create a ticket using that category
    const ticketTitle = "FlowTicket " + Date.now().toString(36).slice(2, 8);
    const ticketId = await createTicketViaUi(page, {
      title: ticketTitle,
      description: "workflow published probe",
      category: catName,
      priority: "high",
    });

    // 7) verify the published workflow IS observable in the ticket — no product changes needed
    await page.goto(base() + `/tickets/${ticketId}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#ticket-category-value")).toContainText(catName);
    // workflow-pending is present when the ticket has a pending workflow step
    await expect(page.locator("#workflow-pending")).toBeVisible({ timeout: 10_000 });
    // The instruction text from the published Manual task appears
    await expect(page.locator(".workflow-instruction")).toContainText("Handle the ticket");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "workflow integrated journey",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
