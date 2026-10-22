import { expect, type Locator, type Page } from "@playwright/test";
import { base } from "./auth.js";

/**
 * Create a ticket via the UI and return its numeric ID.
 * Navigates to /tickets/new, fills the form, submits, and extracts the ID
 * from the resulting page URL or ticket link.
 */
export async function createTicketViaUi(
  page: Page,
  o: {
    title: string;
    description?: string;
    category?: string;
    priority?: string;
    /**
     * Files put on the creation form before the single submit (issue #366).
     * The form is multipart, so attaching is the same native POST the plain
     * journey makes, with the files in the body.
     */
    attachments?: { name: string; mimeType: string; buffer: Buffer }[];
  },
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
  if ((await categoryLink.count()) === 0) {
    const search = page.getByPlaceholder(/search categories, desks, or departments/i);
    await search.fill(category);
    await search.press("Enter");
    categoryLink = page.locator(".catalog-result").filter({ hasText: category }).first();
  }
  if ((await categoryLink.count()) !== 1) {
    throw new Error(`Expected exactly one catalog category "${category}" at ${page.url()}`);
  }
  await categoryLink.click();
  await expect(page.locator(".selected-catalog-path")).toContainText(category);

  await page.getByLabel(/title/i).fill(title);
  await page.getByLabel(/description/i).fill(description);
  await page.getByLabel(/priority/i).selectOption(priority);
  if (o.attachments?.length) {
    await page.locator('input[type="file"][name="attachments"]').setInputFiles(o.attachments);
  }
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
 * The Catalog is one expandable tree (issue #387): departments › desks ›
 * categories, every level on the page at once. These helpers are the only place
 * that knows the tree's markup, so a journey names the entity it needs and never
 * the DOM shape around it.
 */
export function catalogTree(page: Page): Locator {
  return page.locator(".catalog-tree");
}

/** An anchored matcher, so "General" never resolves "General Support". */
function exact(name: string): RegExp {
  return new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
}

/** One tree node by level and exact name. */
export function catalogNode(page: Page, level: "department" | "desk", name: string): Locator {
  return catalogTree(page)
    .locator(`[data-level="${level}"]`)
    .filter({ has: page.locator(".catalog-node-name", { hasText: exact(name) }) });
}

/** One category row by exact name. */
export function catalogCategoryRow(page: Page, name: string): Locator {
  return catalogTree(page)
    .locator('[data-level="category"]')
    .filter({ has: page.locator(".catalog-category-link", { hasText: exact(name) }) });
}

/** The numeric id the server put on a tree node, so a journey can build a URL. */
export async function catalogNodeId(node: Locator, what: string): Promise<string> {
  const value = await node.getAttribute("data-catalog-id");
  if (!value) throw new Error(`Cannot resolve the id of ${what}`);
  return value;
}

/** Toggle one disclosure and prove the state flipped. */
export async function toggleCatalogNode(node: Locator): Promise<void> {
  const button = node.locator(".catalog-tree-row").first().locator("[data-catalog-disclosure]");
  const before = await button.getAttribute("aria-expanded");
  await button.click();
  await expect(button).toHaveAttribute("aria-expanded", before === "false" ? "true" : "false");
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
  const row = catalogCategoryRow(page, name);
  await expect(row).toHaveCount(1);
  return catalogNodeId(row, `category ${name}`);
}

/**
 * Resolve the workflow builder href for the "General" category.
 * The tree renders every category, so no drill-down is needed; the row's own
 * workflow link is the answer. Throws if the General row or its link is missing.
 */
export async function resolveWorkflowHref(page: Page): Promise<string> {
  await page.goto(base() + "/categories?view=structure");
  const rowSelector = '[data-level="category"]:has-text("General")';
  const generalRow = catalogCategoryRow(page, "General");
  if ((await generalRow.count()) !== 1) {
    throw new Error(
      `Expected exactly one category "General" using ${rowSelector} at ${page.url()}`,
    );
  }

  const workflowLink = generalRow.locator('a[href*="/workflow"]').first();
  if ((await workflowLink.count()) === 0) {
    throw new Error(
      `Workflow link not found for category "General" using ${rowSelector} at ${page.url()}`,
    );
  }

  const href = await workflowLink.getAttribute("href");
  if (!href) {
    throw new Error(
      `Workflow href is empty for category "General" using ${rowSelector} at ${page.url()}`,
    );
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
export async function resolveCategoryEditHref(
  page: Page,
  name: string = "General",
): Promise<string> {
  if (!page.url().includes("/categories")) {
    await page.goto(base() + "/categories?view=structure");
  }
  await expect(page.locator('h1:has-text("Catalog")')).toBeVisible({ timeout: 10_000 });

  const rowSelector = `[data-level="category"]:has-text("${name}")`;
  const row = catalogCategoryRow(page, name);
  if ((await row.count()) !== 1) {
    throw new Error(
      `Expected exactly one category "${name}" using ${rowSelector} at ${page.url()}`,
    );
  }

  const editLink = row.locator('a[href*="/edit"]').first();
  if ((await editLink.count()) === 0) {
    throw new Error(
      `Edit link not found for category "${name}" using ${rowSelector} at ${page.url()}`,
    );
  }

  const href = await editLink.getAttribute("href");
  if (!href) {
    throw new Error(
      `Edit href is empty for category "${name}" using ${rowSelector} at ${page.url()}`,
    );
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
  const row = page
    .locator("tr[data-user-name]")
    .filter({ has: page.getByText(userName, { exact: true }) });
  if ((await row.count()) !== 1) {
    throw new Error(
      `Expected exactly one user "${userName}" using ${rowSelector} at ${page.url()}`,
    );
  }

  const launcher = row.locator("a.user-launcher").first();
  if ((await launcher.count()) > 0) {
    const href = await launcher.getAttribute("href");
    if (href) return href.split("?")[0];
  }

  const editLink = row.locator('a[href*="/users/"][href*="/edit"]').first();
  if ((await editLink.count()) > 0) {
    const href = await editLink.getAttribute("href");
    if (href) return href.split("?")[0];
  }

  throw new Error(
    `No edit href found for user "${userName}" using ${rowSelector} at ${page.url()}`,
  );
}

/**
 * Reach Preferences the way a person does since issue #377: the entry lives in
 * the user menu at the foot of the sidebar, not in the section navigation. The
 * menu is a real popup (aria-haspopup/aria-expanded), so the helper proves the
 * toggle state flips before it follows the link.
 */
export async function openPreferencesFromUserMenu(page: Page): Promise<void> {
  const toggle = page.locator("[data-user-menu-toggle]");
  await expect(toggle).toBeVisible();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  const link = page.locator('#user-menu a[href="/preferences"]');
  await expect(link).toBeVisible();
  await link.click();
  await expect(page).toHaveURL(/\/preferences$/);
}
