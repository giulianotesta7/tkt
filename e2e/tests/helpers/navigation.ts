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
  await expect(page.locator("h2")).toContainText(/ticket details/i);

  await page.getByLabel(/title/i).fill(title);
  await page.getByLabel(/description/i).fill(description);
  await expect(page.getByLabel(/category/i)).toBeVisible();

  // Ensure the desired category option is present before selecting
  // (handles empty-base published case where category may not yet be available)
  const categorySelect = page.getByLabel(/category/i);
  await expect(categorySelect.locator(`option:has-text("${category}")`).first()).toBeAttached({
    timeout: 10000,
  });
  await categorySelect.selectOption({ label: category });

  await page.getByLabel(/priority/i).selectOption(priority);
  await page.getByRole("button", { name: /create ticket/i }).click();

  await expect(page).toHaveURL(/\/tickets/);
  await expect(page.getByText(title)).toBeVisible({ timeout: 10_000 });

  // Extract ticket ID from the visible ticket link
  const href = await page.getByText(title).first().getAttribute("href");
  if (href) {
    const m = href.match(/\/tickets\/(\d+)/);
    if (m) return m[1];
  }

  const fallbackHref = await page.locator('a[href*="/tickets/"]').first().getAttribute("href");
  const m2 = fallbackHref?.match(/\/tickets\/(\d+)/);
  if (m2) return m2[1];

  throw new Error("could not extract ticket id for " + title + " at " + page.url());
}

/**
 * Resolve the workflow builder href for the "General" category.
 * Throws if the General row or its workflow link is not found.
 */
export async function resolveWorkflowHref(page: Page): Promise<string> {
  const generalRow = page.locator("tr").filter({ hasText: "General" });
  if (await generalRow.count() === 0) {
    throw new Error("General category row not found on /categories");
  }

  const workflowLink = generalRow.locator('a[href*="/workflow"]').first();
  if (await workflowLink.count() === 0) {
    throw new Error("Workflow link not found for General category row");
  }

  const href = await workflowLink.getAttribute("href");
  if (!href) {
    throw new Error("Workflow link href is empty for General category row");
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

  const row = page.locator("tr").filter({ hasText: name });
  if (await row.count() === 0) {
    throw new Error(`Category row "${name}" not found on /categories`);
  }

  const editLink = row.locator('a[href*="/edit"]').first();
  if (await editLink.count() === 0) {
    throw new Error(`Edit link not found for category "${name}"`);
  }

  const href = await editLink.getAttribute("href");
  if (!href) {
    throw new Error(`Edit href is empty for category "${name}"`);
  }
  return href;
}

/**
 * Resolve the user edit href from the users list.
 * Prefers the user-launcher anchor (drawer) which is the primary edit entry.
 * Throws if no edit link is found.
 */
export async function resolveUserEditHref(page: Page): Promise<string> {
  await expect(page.locator("#users-list-title")).toBeVisible({ timeout: 10_000 });

  // Prefer the user-launcher anchor (drawer) which is the primary edit entry
  const launcher = page.locator('a.user-launcher').first();
  if (await launcher.count() > 0) {
    const href = await launcher.getAttribute("href");
    if (href) return href.split("?")[0];
  }

  const anyLink = page.locator('a[href*="/users/"][href*="/edit"]').first();
  if (await anyLink.count() > 0) {
    const href = await anyLink.getAttribute("href");
    if (href) return href.split("?")[0];
  }

  throw new Error("No user edit href found on /users");
}