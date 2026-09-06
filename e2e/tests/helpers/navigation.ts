import { expect, type Page } from "@playwright/test";
import { base } from "./auth.js";

/**
 * Create a ticket via the UI and return its numeric ID.
 * Navigates to /tickets/new, fills the form, submits, and extracts the ID
 * from the resulting page URL or ticket link.
 */
export async function createTicketViaUi(
  page: Page,
  o: { title: string; description?: string; category?: string; priority?: string },
): Promise<string> {
  const title = o.title;
  const description = o.description ?? "probe";
  const category = o.category ?? "General";
  const priority = o.priority ?? "high";

  await page.goto(base() + "/tickets/new");
  await expect(page.getByRole("heading", { name: "Create a ticket" })).toBeVisible();

  // Select the exact catalog category. Search is the deterministic fallback
  // for categories outside the first department/desk.
  let categoryLink = page.locator(".catalog-category").filter({ hasText: category }).first();
  if (await categoryLink.count() === 0) {
    const search = page.getByPlaceholder(/search categories, desks, or departments/i);
    await search.fill(category);
    await search.press("Enter");
    categoryLink = page.locator(".catalog-result").filter({ hasText: category }).first();
  }
  if (await categoryLink.count() !== 1) {
    throw new Error(`Expected exactly one catalog category "${category}" at ${page.url()}`);
  }
  await categoryLink.click();
  await expect(page.locator(".selected-catalog-path")).toContainText(category);

  await page.getByLabel(/title/i).fill(title);
  await page.getByLabel(/description/i).fill(description);
  await page.getByLabel(/priority/i).selectOption(priority);
  await page.getByRole("button", { name: /create ticket/i }).click();

  await expect(page).toHaveURL(/\/tickets/);
  await expect(page.getByText(title)).toBeVisible({ timeout: 10_000 });

  // Extract ticket ID exclusively from the visible link matching the created title
  const titleSelector = `a[href*="/tickets/"]:has-text("${title}")`;
  const href = await page.getByRole("link", { name: title, exact: true }).getAttribute("href");
  if (href) {
    const m = href.match(/\/tickets\/(\d+)/);
    if (m) return m[1];
  }

  throw new Error(
    `could not extract ticket id for "${title}" using ${titleSelector} at ${page.url()} — ` +
    `href was ${href ?? "null"}`,
  );
}

/**
 * Create a category through the catalog drawer and return its stable ID.
 * The drawer requires an explicit department and desk selection.
 */
export async function createCategoryViaUi(page: Page, name: string): Promise<string> {
  await page.goto(base() + "/categories/new");
  const drawer = page.getByRole("dialog", { name: /New category/i });
  await expect(drawer).toBeVisible();
  const department = drawer.locator("select[name=department_id]");
  await department.selectOption({ index: 1 });
  const desk = drawer.locator("select[name=desk_id]");
  const deskValue = await desk.locator("option:not([value=''])").first().getAttribute("value");
  if (!deskValue) throw new Error("No desk is available for the selected department");
  await desk.selectOption(deskValue);
  await drawer.locator("#category-name").fill(name);
  await drawer.getByRole("button", { name: /create category/i }).click();
  await expect(page).toHaveURL(/\/categories/);
  const row = page.locator(".category-level-categories .category-structure-item").filter({ hasText: name });
  await expect(row).toHaveCount(1);
  const editHref = await row.locator('a[href*="/edit"]').first().getAttribute("href");
  const id = editHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
  if (!id) throw new Error(`cannot resolve category id for ${name} at ${page.url()}`);
  return id;
}

    async function selectFirstStructureDesk(page: Page): Promise<void> {
      await page.goto(base() + "/categories?view=structure");
      const department = page.locator(".category-level-departments .category-structure-row").first();
      if (await department.count() !== 1) {
        throw new Error(`No department is available for structure selection at ${page.url()}`);
      }
      await department.click();
      const desk = page.locator(".category-level-desks .category-structure-row").first();
      if (await desk.count() !== 1) {
        throw new Error(`No desk is available for structure selection at ${page.url()}`);
      }
      await desk.click();
    }

    /**
     * Resolve the workflow builder href for the "General" category.
     * Throws if the General row or its workflow link is not found.
     */
    export async function resolveWorkflowHref(page: Page): Promise<string> {
      await selectFirstStructureDesk(page);
      const rowSelector = '.category-level-categories .category-structure-item:has-text("General")';
      const generalRow = page.locator(".category-level-categories .category-structure-item").filter({ has: page.getByText("General", { exact: true }) });
      if (await generalRow.count() !== 1) {
        throw new Error(`Expected exactly one category "General" using ${rowSelector} at ${page.url()}`);
      }

      const workflowLink = generalRow.locator('a[href*="/workflow"]').first();
      if (await workflowLink.count() === 0) {
        throw new Error(`Workflow link not found for category "General" using ${rowSelector} at ${page.url()}`);
      }

      const href = await workflowLink.getAttribute("href");
      if (!href) {
        throw new Error(`Workflow href is empty for category "General" using ${rowSelector} at ${page.url()}`);
      }
      return href;
    }

/**
 * Open the workflow builder for the General category.
 * Resolves the workflow href, navigates, and waits for #workflow-builder.
 * Throws if any step fails.
 */
export async function openWorkflowBuilder(page: Page): Promise<string> {
  const href = await resolveWorkflowHref(page);
  await page.goto(base() + href);
  await expect(page.locator("#workflow-builder")).toBeVisible({ timeout: 10_000 });
  return href;
}

/**
 * Resolve the category edit href for the given category name.
 * Navigates to /categories first if not already there.
 * Throws if the row or edit link is not found.
 */
export async function resolveCategoryEditHref(page: Page, name: string = "General"): Promise<string> {
  await expect(page.locator('h1:has-text("Categories")')).toBeVisible({ timeout: 10_000 });
  await selectFirstStructureDesk(page);

  const rowSelector = `.category-level-categories .category-structure-item:has-text("${name}")`;
  const row = page.locator(".category-level-categories .category-structure-item").filter({ has: page.getByText(name, { exact: true }) });
  if (await row.count() !== 1) {
    throw new Error(`Expected exactly one category "${name}" using ${rowSelector} at ${page.url()}`);
  }

  const editLink = row.locator('a[href*="/edit"]').first();
  if (await editLink.count() === 0) {
    throw new Error(`Edit link not found for category "${name}" using ${rowSelector} at ${page.url()}`);
  }

  const href = await editLink.getAttribute("href");
  if (!href) {
    throw new Error(`Edit href is empty for category "${name}" using ${rowSelector} at ${page.url()}`);
  }
  return href;
}

/**
 * Resolve the user edit href from the users list.
 * Prefers the user-launcher anchor (drawer) which is the primary edit entry.
 * Throws if no edit link is found.
 */
export async function resolveUserEditHref(page: Page, userName: string): Promise<string> {
  await expect(page.locator("#users-list-title")).toBeVisible({ timeout: 10_000 });

  const rowSelector = `tr:has(td:text-is("${userName}"))`;
  const row = page.locator("tr[data-user-name]").filter({ has: page.getByText(userName, { exact: true }) });
  if (await row.count() !== 1) {
    throw new Error(`Expected exactly one user "${userName}" using ${rowSelector} at ${page.url()}`);
  }

  const launcher = row.locator('a.user-launcher').first();
  if (await launcher.count() > 0) {
    const href = await launcher.getAttribute("href");
    if (href) return href.split("?")[0];
  }

  const editLink = row.locator('a[href*="/users/"][href*="/edit"]').first();
  if (await editLink.count() > 0) {
    const href = await editLink.getAttribute("href");
    if (href) return href.split("?")[0];
  }

  throw new Error(`No edit href found for user "${userName}" using ${rowSelector} at ${page.url()}`);
}
