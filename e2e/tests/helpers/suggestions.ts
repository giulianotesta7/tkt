/**
 * Category suggestions (issue #416, S1/S2/S4/S6).
 *
 * Two fixtures are shared by the journeys that prove the feature in the browser:
 *  - the instance-wide engine choice, which both creation screens and the
 *    Settings card read, so a journey that turns it on MUST restore "off";
 *  - a category a requester can actually choose (published, runnable) that
 *    carries suggestion keywords, because the local engine matches those words
 *    and a category without a published workflow is never offered to anyone.
 *
 * The exhaustive cases stay in Go: the fragment contract and the card in
 * internal/adapters/http/handlers_category_suggestions_test.go, the thresholds
 * and the engine choice in
 * internal/application/category_suggestion_service_test.go, and ranking, typo
 * correction and matched words in
 * internal/adapters/sqlite/category_suggest_index_test.go.
 */

import { expect, type Page } from "@playwright/test";
import { base } from "./auth.js";
import { assertHtmxSwap } from "./htmx.js";
import { catalogCategoryRow, catalogNode, catalogNodeId } from "./navigation.js";

export type SuggestionEngine = "off" | "local" | "model";

/**
 * The three choices in the order the card renders them. The accessible name of
 * each radio is its label followed by the sentence under it, so every matcher is
 * anchored on the label alone.
 */
const engineChoice: Record<SuggestionEngine, RegExp> = {
  off: /^off\b/i,
  local: /^on this server\b/i,
  model: /^decision model\b/i,
};

/**
 * Choose how the instance suggests categories (S1) and prove the stored choice
 * survives a reload. The choice is instance-wide and shared with every later
 * journey in the same database, so a test that changes it MUST set "off" again
 * when it finishes.
 */
export async function setCategorySuggestionEngine(
  page: Page,
  engine: SuggestionEngine,
): Promise<void> {
  await page.goto(base() + "/settings/tickets");
  const choice = page.getByRole("radio", { name: engineChoice[engine] });
  await expect(choice).toBeVisible();
  if (await choice.isChecked()) {
    // Nothing to save: the card's save bar stays hidden until its form is dirty,
    // and the stored choice is already the one requested.
    return;
  }
  await choice.check();
  const save = page.getByRole("button", { name: "Save category suggestions" });
  await expect(save).toBeVisible();
  await save.click();
  await expect(page).toHaveURL(/\/settings\/tickets$/);
  await expect(page.locator("#save-feedback")).toContainText("Saved");
  await page.reload();
  await expect(choice).toBeChecked();
}

export interface SuggestableCategory {
  department: string;
  departmentID: string;
  desk: string;
  deskID: string;
  category: string;
  categoryID: string;
  /** The keywords the category was created with, as the drawer stored them. */
  keywords: string;
}

/**
 * Create a department, a desk, a category carrying suggestion keywords and a
 * published one-step workflow, all through the product's own screens, and return
 * the ids the journey needs to build URLs and to resolve the suggestion.
 *
 * Publishing is part of the fixture because the suggestion engines are only ever
 * offered the categories a requester may choose (runnable and published), so a
 * draft category could be created but could never be suggested.
 */
export async function createSuggestableCategory(
  page: Page,
  keywords: string,
): Promise<SuggestableCategory> {
  const suffix = Date.now().toString(36);
  const fixture: SuggestableCategory = {
    department: `Suggestions Department ${suffix}`,
    departmentID: "",
    desk: `Suggestions Desk ${suffix}`,
    deskID: "",
    category: `Suggestions Category ${suffix}`,
    categoryID: "",
    keywords,
  };

  await page.goto(base() + "/categories/departments/new?view=structure");
  const departmentDrawer = page.getByRole("dialog", { name: /New department/i });
  await expect(departmentDrawer).toBeVisible();
  await departmentDrawer.locator("#category-name").fill(fixture.department);
  await departmentDrawer.getByRole("button", { name: /create department/i }).click();
  const departmentRow = catalogNode(page, "department", fixture.department);
  await expect(departmentRow).toHaveCount(1);
  fixture.departmentID = await catalogNodeId(departmentRow, `department ${fixture.department}`);

  await page.goto(
    base() + `/categories/desks/new?view=structure&department_id=${fixture.departmentID}`,
  );
  const deskDrawer = page.getByRole("dialog", { name: /New desk/i });
  await expect(deskDrawer).toBeVisible();
  await deskDrawer.locator("#category-name").fill(fixture.desk);
  await deskDrawer.getByRole("button", { name: /create desk/i }).click();
  const deskRow = catalogNode(page, "desk", fixture.desk);
  await expect(deskRow).toHaveCount(1);
  fixture.deskID = await catalogNodeId(deskRow, `desk ${fixture.desk}`);

  await page.goto(
    base() +
      `/categories/new?view=structure&department_id=${fixture.departmentID}&desk_id=${fixture.deskID}`,
  );
  const categoryDrawer = page.getByRole("dialog", { name: /New category/i });
  await expect(categoryDrawer).toBeVisible();
  await categoryDrawer.locator("#category-name").fill(fixture.category);
  await categoryDrawer.locator("#category-description").fill(`Requests about ${fixture.category}`);
  await categoryDrawer.locator("#category-keywords").fill(fixture.keywords);
  await categoryDrawer.getByRole("button", { name: /create category/i }).click();
  const categoryRow = catalogCategoryRow(page, fixture.category);
  await expect(categoryRow).toHaveCount(1);
  fixture.categoryID = await catalogNodeId(categoryRow, `category ${fixture.category}`);

  // A published workflow is what makes the category an offer; the single manual
  // task is the smallest draft the builder publishes.
  await page.goto(base() + `/categories/${fixture.categoryID}/workflow`);
  const addStep = page.locator(".workflow-add-step summary").first();
  await expect(addStep).toBeVisible();
  await addStep.click();
  const manualTask = page
    .locator(".workflow-add-options button")
    .filter({ hasText: "Give the agent a task" })
    .first();
  await expect(manualTask).toBeVisible();
  await assertHtmxSwap(
    page,
    async () => {
      await manualTask.click();
    },
    {
      endpoint: (url) => {
        const parsedURL = new URL(url);
        return (
          parsedURL.pathname === `/categories/${fixture.categoryID}/workflow` &&
          parsedURL.searchParams.get("add_step_type") === "manual_task"
        );
      },
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    },
  );
  await expect(page.getByLabel(/instructions/i)).toBeVisible();
  await page.getByLabel(/instructions/i).fill("Handle the ticket");
  await assertHtmxSwap(
    page,
    async () => {
      await page.getByRole("button", { name: /^publish$/i }).click();
    },
    {
      endpoint: `/categories/${fixture.categoryID}/workflow`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    },
  );
  await expect(page.locator("#workflow-builder")).toContainText("Published v1");

  return fixture;
}

/**
 * The single-page form's suggestion host: the element the fragment swaps into.
 * It is the one carrying `hx-get`, because the fragment it receives wraps its own
 * container in the same class, so the plain class matches the host AND its
 * content once a suggestion has been rendered.
 */
export const singlePageSuggestionSelector =
  '.ticket-create-detail .create-suggestions[hx-get="/tickets/suggestions?mode=single"]';

/** The two-step catalog's suggestion host, above the department sections. */
export const catalogSuggestionSelector = "#create-catalog-suggestions";
