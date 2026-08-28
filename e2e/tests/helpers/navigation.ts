import { expect, type Page } from "@playwright/test";
import { base } from "./auth.js";
export async function createTicketViaUi(page: Page, o: { title: string; description?: string; category?: string; priority?: string }): Promise<string> {
  const t=o.title,d=o.description??"probe",c=o.category??"General",p=o.priority??"high";
  await page.goto(base()+"/tickets/new");await expect(page.locator("h2")).toContainText(/ticket details/i);
  await page.getByLabel(/title/i).fill(t);await page.getByLabel(/description/i).fill(d);
  await expect(page.getByLabel(/category/i)).toBeVisible();
  // Ensure the desired category option is present before selecting (handles empty-base published case)
  const categorySelect = page.getByLabel(/category/i);
  await expect(categorySelect.locator(`option:has-text("${c}")`).first()).toBeAttached({ timeout: 10000 });
  await categorySelect.selectOption({label:c});
  await page.getByLabel(/priority/i).selectOption(p);
  await page.getByRole("button",{name:/create ticket/i}).click();
  await expect(page).toHaveURL(/\/tickets/);await expect(page.getByText(t)).toBeVisible({timeout:10_000});
  const h=await page.getByText(t).first().getAttribute("href");if(h){const m=h.match(/\/tickets\/(\d+)/);if(m) return m[1];}
  const h2=await page.locator('a[href*="/tickets/"]').first().getAttribute("href");const m2=h2?.match(/\/tickets\/(\d+)/);if(m2) return m2[1];
  throw new Error("could not extract ticket id for "+t+" at "+page.url());
}
export async function resolveWorkflowHref(page: Page): Promise<string|null> {
  const r=page.locator("tr").filter({hasText:"General"});
  if(await r.count()){const w=r.locator('a[href*="/workflow"]').first();if(await w.count()){const h=await w.getAttribute("href");if(h) return h;}
  const e=await r.locator('a[href*="/edit"]').getAttribute("href");const m=e?.match(/\/categories\/(\d+)\/edit/);if(m) return `/categories/${m[1]}/workflow`;}
  const w2=page.locator('a[href*="/workflow"]').first();if(await w2.count()){const h=await w2.getAttribute("href");if(h) return h;}
  const e2=page.locator('a[href*="/edit"]').first();if(await e2.count()){const h=await e2.getAttribute("href");const m=h?.match(/\/categories\/(\d+)\/edit/);if(m) return `/categories/${m[1]}/workflow`;}
  return null;
}
export async function openWorkflowBuilder(page: Page): Promise<string> {
  const h=await resolveWorkflowHref(page);if(!h) throw new Error("could not find workflow href");
  await page.goto(base()+h);await expect(page.locator("#workflow-builder")).toBeVisible({timeout:10_000});return h;
}

export async function resolveCategoryEditHref(page: Page, name: string = "General"): Promise<string|null> {
  await expect(page.locator('h1:has-text("Categories")')).toBeVisible({ timeout: 10000 });
  const row = page.locator("tr").filter({ hasText: name });
  if (await row.count()) {
    const e = row.locator('a[href*="/edit"]').first();
    if (await e.count()) {
      const h = await e.getAttribute("href");
      if (h) return h;
    }
  }
  const any = page.locator('a[href*="/categories/"][href*="/edit"]').first();
  if (await any.count()) {
    const h = await any.getAttribute("href");
    if (h) return h;
  }
  return null;
}

export async function resolveUserEditHref(page: Page): Promise<string|null> {
  await expect(page.locator("#users-list-title")).toBeVisible({ timeout: 10000 });
  // Prefer the user-launcher anchor (drawer) which is the primary edit entry
  const launcher = page.locator('a.user-launcher').first();
  if (await launcher.count()) {
    const h = await launcher.getAttribute("href");
    if (h) return h.split("?")[0];
  }
  const any = page.locator('a[href*="/users/"][href*="/edit"]').first();
  if (await any.count()) {
    const h = await any.getAttribute("href");
    if (h) return h.split("?")[0];
  }
  return null;
}
