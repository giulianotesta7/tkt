/**
 * Ticket journeys: creation, list, detail, search/filter, public comment, transition.
 *
 * Logout and auth gate are covered in auth.spec.ts (auth domain).
 * Search filter is the canonical HTMX swap test for #tickets-screen (this file).
 * Public comment is the canonical comment journey (this file).
 * Transitions use assertHtmxSwap for swap verification.
 * htmx.spec.ts covers transversal swaps only (users tabs, workflow builder).
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import {
  assertCanonicalScreen,
  collectObservability,
  expectHairlineBorder,
} from "./helpers/layout.js";
import {
  catalogCategoryRow,
  catalogNode,
  catalogNodeId,
  chooseCreationPriority,
  createTicketViaUi,
  creationCategoryPicker,
  creationPriorityPicker,
  openCatalogCard,
  pickCreationCategory,
} from "./helpers/navigation.js";
import {
  applyTransition,
  openPicker,
  pickerButton,
  pickerOption,
  transitionAction,
} from "./helpers/ticket.js";
import { waitForExactPost } from "./helpers/network.js";
import { assertHtmxSwap } from "./helpers/htmx.js";
import { loginAsSeeded, setSLAEnabled, setTicketCreationMode } from "./helpers/auth.js";
import {
  catalogSuggestionSelector,
  createSuggestableCategory,
  setCategorySuggestionEngine,
  singlePageSuggestionSelector,
} from "./helpers/suggestions.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

async function effectivePaintedBackground(
  locator: import("@playwright/test").Locator,
): Promise<string> {
  return locator.evaluate((element) => {
    let current: Element | null = element;
    while (current) {
      const backgroundColor = getComputedStyle(current).backgroundColor;
      const alpha = backgroundColor.match(/^rgba\(\d+,\s*\d+,\s*\d+,\s*([\d.]+)\)$/)?.[1];
      if (backgroundColor !== "transparent" && alpha !== "0") {
        return backgroundColor;
      }
      current = current.parentElement;
    }
    return getComputedStyle(document.documentElement).backgroundColor;
  });
}

type PublishedHierarchyFixture = {
  department: string;
  departmentID: string;
  desk: string;
  category: string;
  categoryID: string;
};

/** Open the Catalog's one New menu and choose one of its three entries. */
async function openCatalogNewMenu(page: import("@playwright/test").Page, item: string) {
  await page.locator("[data-catalog-new-toggle]").click();
  await page
    .locator(".catalog-new .category-overflow-menu:not([hidden])")
    .getByRole("link", { name: item, exact: true })
    .click();
}

async function createPublishedHierarchyFixture(
  page: import("@playwright/test").Page,
): Promise<PublishedHierarchyFixture> {
  const suffix = Date.now().toString(36);
  const fixture = {
    department: `Technology Infrastructure Operations Department ${suffix}`,
    departmentID: "",
    desk: `Enterprise Network Endpoint Support Desk ${suffix}`,
    category: `Connectivity Device Assistance Category ${suffix}`,
    categoryID: "",
  };

  await page.goto(base() + "/categories?view=structure");
  // The Catalog's three creation entries live in one New menu since issue #387:
  // the menu opens first, then the entry inside it.
  await openCatalogNewMenu(page, "New department");
  const departmentDrawer = page.getByRole("dialog", {
    name: /New department/i,
  });
  await expect(departmentDrawer).toBeVisible();
  await departmentDrawer.locator("#category-name").fill(fixture.department);
  await departmentDrawer.getByRole("button", { name: /create department/i }).click();

  // The Catalog is one tree since issue #387: the new department is a row, and
  // its id is on the row itself instead of in a drill-down link.
  const departmentRow = catalogNode(page, "department", fixture.department);
  await expect(departmentRow).toHaveCount(1);
  const departmentID = await catalogNodeId(departmentRow, `department ${fixture.department}`);
  fixture.departmentID = departmentID;

  await openCatalogNewMenu(page, "New desk");
  const deskDrawer = page.getByRole("dialog", { name: /New desk/i });
  await expect(deskDrawer).toBeVisible();
  await deskDrawer.locator("select[name=department_id]").selectOption(departmentID);
  await deskDrawer.locator("#category-name").fill(fixture.desk);
  await deskDrawer.getByRole("button", { name: /create desk/i }).click();

  const deskRow = catalogNode(page, "desk", fixture.desk);
  await expect(deskRow).toHaveCount(1);
  const deskID = await catalogNodeId(deskRow, `desk ${fixture.desk}`);

  await openCatalogNewMenu(page, "New category");
  const categoryDrawer = page.getByRole("dialog", { name: /New category/i });
  await expect(categoryDrawer).toBeVisible();
  // The generic New menu preselects nothing, so the drawer is driven the way a
  // person does: department first, then the desk inside it.
  await categoryDrawer.locator("select[name=department_id]").selectOption(departmentID);
  await categoryDrawer.locator("select[name=desk_id]").selectOption(deskID);
  await categoryDrawer.locator("#category-name").fill(fixture.category);
  await categoryDrawer.getByRole("button", { name: /create category/i }).click();

  const categoryRow = catalogCategoryRow(page, fixture.category);
  await expect(categoryRow).toHaveCount(1);
  fixture.categoryID = await catalogNodeId(categoryRow, `category ${fixture.category}`);
  const categoryID = fixture.categoryID;

  await page.goto(base() + `/categories/${categoryID}/workflow`);
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
          parsedURL.pathname === `/categories/${categoryID}/workflow` &&
          parsedURL.searchParams.get("add_step_type") === "manual_task"
        );
      },
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    },
  );
  const instructions = page.getByLabel(/instructions/i);
  await expect(instructions).toBeVisible();
  // Explicit save: field edits send no workflow POST; the Publish below persists the draft.
  let draftPost = false;
  const onDraftPost = (request: import("@playwright/test").Request) => {
    draftPost ||=
      request.method() === "POST" &&
      new URL(request.url()).pathname === `/categories/${categoryID}/workflow`;
  };
  page.on("request", onDraftPost);
  await instructions.fill("Handle the published hierarchy ticket");
  await page.waitForTimeout(700);
  page.off("request", onDraftPost);
  expect(draftPost, "editing must not issue a workflow POST").toBe(false);
  await assertHtmxSwap(
    page,
    async () => {
      await page.getByRole("button", { name: /publish/i }).click();
    },
    {
      endpoint: `/categories/${categoryID}/workflow`,
      method: "POST",
      expectedStatus: 200,
      hxTarget: "#workflow-builder",
    },
  );
  await expect(page.locator(".error-banner, [role='alert']")).toHaveCount(0);

  return fixture;
}

// Choose one category in the two-step service catalog (issue #391, S4) by its
// exact name. The catalog is one page of cards grouped by department since
// #391, so the search box is what narrows a long catalog and the card is what
// opens the form — no drill-down exists any more.
async function selectCatalogCategory(
  page: import("@playwright/test").Page,
  name: string,
): Promise<void> {
  await expect(page.locator(".create-catalog")).toBeVisible();
  const search = page.locator("[data-create-catalog-search]");
  await search.fill(name);
  const card = page.locator(".create-card").filter({
    has: page.locator(".create-card-name", {
      hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`),
    }),
  });
  await expect(card).toHaveCount(1);
  await card.click();
  await expect(page.locator(".ticket-create-detail .selected-catalog-path")).toContainText(name);
}

// The create form's priority reading: with a script it is the detail's listbox,
// so the value lives on its button rather than on a native select.
async function expectCreationPriority(
  page: import("@playwright/test").Page,
  label: string,
): Promise<void> {
  await expect(creationPriorityPicker(page).locator(".listbox-button")).toContainText(label);
}

test.describe("Ticket Lifecycle", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });

  test.afterAll(async () => {
    await stopServer();
  });

  test("create ticket, verify in list, detail, and navigate from index", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const title = "Login issue " + Date.now();
    await createTicketViaUi(page, {
      title,
      description: "Cannot log in",
      category: "General",
      priority: "high",
    });
    await expect(page.getByText(title)).toBeVisible({ timeout: 5000 });
    await expect(page.getByRole("cell", { name: "High" })).toBeVisible();
    await expect(page.getByText("New").first()).toBeVisible();
    const ticketNumberText = await page.getByText(/TKT-\d+/).textContent();
    expect(ticketNumberText).not.toBeNull();
    expect(ticketNumberText).toMatch(/TKT-\d+/);

    await page.goto(base() + "/tickets");
    await expect(page.getByText(title)).toBeVisible({ timeout: 5000 });
    await page.getByText(title).first().click();
    await page.waitForURL(/\/tickets\/\d+/);
    // The number appears twice by design (the header's crumb and the Details
    // card, issue #386 S5), so resolve the first one.
    await expect(page.getByText(ticketNumberText!).first()).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets create/list/detail",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("single-page creation keeps its shape, its picker and its values", async ({ page }) => {
    // Issue #391, S2/S3/S6/S7. The create form is the detail's own anatomy: a
    // title field that reads as the heading field, a description card with the
    // composer's drop zone, and a properties panel holding Requester, Category
    // and Priority. The category is a native select grouped one optgroup per
    // "Department › Desk", enhanced into the detail's listbox.
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    const fixture = await createPublishedHierarchyFixture(page);
    await page.goto(base() + "/tickets");
    const ticketsTitle = page
      .locator(".tickets-header")
      .getByRole("heading", { name: "Tickets", exact: true });
    await expect(ticketsTitle).toBeVisible();
    const titleSizes = new Map<number, string>();
    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect(ticketsTitle).toBeVisible();
      titleSizes.set(width, await ticketsTitle.evaluate((el) => getComputedStyle(el).fontSize));
    }
    await page.setViewportSize({ width: 1280, height: 800 });

    await page.goto(base() + "/tickets/new");
    const form = page.locator(".ticket-create-detail");
    const heading = form.getByRole("heading", { name: "Create a ticket" });
    await expect(heading).toBeVisible();
    // Issue #380: the subtitle repeated what the heading and fields say.
    await expect(page.getByText("Describe your request.", { exact: true })).toHaveCount(0);
    const titleInput = page.getByLabel("Title");
    await expect(titleInput).toBeVisible();
    const descriptionCard = form.locator(".ticket-create-description");
    const properties = form.locator("aside.evidence");
    await expect(descriptionCard).toHaveCount(1);
    await expect(properties).toContainText("Properties");
    await expect(properties).toContainText("Requester");
    await expect(properties).toContainText("Alice Admin");
    await expect(properties).toContainText("Category");
    await expect(properties).toContainText("Priority");
    // S2: no empty Timeline card and no Assignment section.
    await expect(form.locator(".ticket-create-timeline")).toHaveCount(0);
    await expect(properties).not.toContainText("Assignee");
    await expect(properties).not.toContainText("Not assigned yet");
    await expect(form.locator("[name=to], [name=user_id], [name=assignee_id]")).toHaveCount(0);
    await expect(form.locator("input[name^=requester]")).toHaveCount(0);
    // The attachment control is the composer's drop zone, wrapping one file input.
    const dropzone = form.locator(".comment-dropzone.ticket-create-dropzone");
    await expect(dropzone).toHaveCount(1);
    await expect(dropzone.locator('input[type="file"][name="attachments"]')).toHaveCount(1);

    // The title field reads as the page's heading field at the desktop measure
    // the create page has always had, and shares the card's surface.
    const titleBox = await titleInput.boundingBox();
    const descriptionBox = await descriptionCard.boundingBox();
    const propertiesBox = await properties.boundingBox();
    expect(titleBox && descriptionBox && propertiesBox).not.toBeNull();
    expect(titleBox!.y).toBeLessThan(descriptionBox!.y);
    expect(propertiesBox!.x - (descriptionBox!.x + descriptionBox!.width)).toBe(16);
    expect(titleBox!.width).toBeGreaterThanOrEqual(540);
    expect(titleBox!.width).toBeLessThanOrEqual(560);

    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect(heading).toHaveCSS("font-size", titleSizes.get(width)!);
      await expect(page.locator("body")).toHaveJSProperty("scrollWidth", width);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    const mobileDescription = await descriptionCard.boundingBox();
    const mobileProperties = await properties.boundingBox();
    expect(mobileDescription && mobileProperties).not.toBeNull();
    expect(mobileProperties!.y).toBeGreaterThan(mobileDescription!.y);
    // A long title must never push the phone layout sideways.
    const longTitle = `Published hierarchy ticket ${"that stays within the title input ".repeat(12)}`;
    await titleInput.fill(longTitle);
    await expect(titleInput).toHaveValue(longTitle);
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
    await titleInput.fill("Published hierarchy ticket");
    await page.setViewportSize({ width: 1280, height: 800 });

    // S3: the picker never submits and never saves on change. Choosing another
    // category keeps the title, the description and the priority already typed.
    await titleInput.fill("Published hierarchy ticket");
    await page.getByLabel("Description").fill("Kept description");
    await chooseCreationPriority(page, "high");
    let requests = 0;
    let navigations = 0;
    const onRequest = (request: import("@playwright/test").Request) => {
      if (request.url().startsWith(base()) && ["GET", "POST"].includes(request.method())) {
        requests += 1;
      }
    };
    const onNavigation = (frame: import("@playwright/test").Frame) => {
      if (frame === page.mainFrame()) navigations += 1;
    };
    page.on("request", onRequest);
    page.on("framenavigated", onNavigation);
    await pickCreationCategory(page, fixture.category);
    page.off("request", onRequest);
    page.off("framenavigated", onNavigation);
    expect(requests, "changing the category must not issue a request").toBe(0);
    expect(navigations, "changing the category must not navigate").toBe(0);
    await expect(titleInput).toHaveValue("Published hierarchy ticket");
    await expect(page.getByLabel("Description")).toHaveValue("Kept description");
    await expect(creationPriorityPicker(page).locator(".listbox-button")).toContainText("High");

    // S6: a rejected submit keeps every submitted value, including the category,
    // and focuses the field that has to be fixed.
    await titleInput.fill("   ");
    const validationResponse = waitForExactPost(page, "/tickets");
    await page.getByRole("button", { name: "Create ticket", exact: true }).click();
    expect((await validationResponse).status()).toBe(422);
    await expect(page.getByRole("alert")).toContainText(/title is required/i);
    await expect(page.getByLabel("Title")).toHaveValue("   ");
    await expect(page.getByLabel("Description")).toHaveValue("Kept description");
    await expect(creationPriorityPicker(page).locator(".listbox-button")).toContainText("High");
    await expect(creationCategoryPicker(page).locator(".listbox-button")).toContainText(
      fixture.category,
    );
    await expect(page.getByLabel("Title")).toBeFocused();

    // The picker searches by category, desk AND department name, and it keeps
    // the group headings of the matches.
    await titleInput.fill("Published hierarchy ticket");
    const pickerButton = creationCategoryPicker(page).locator(".listbox-button");
    await pickerButton.click();
    await expect(pickerButton).toHaveAttribute("aria-expanded", "true");
    const pickerSearch = creationCategoryPicker(page).locator(".listbox-search");
    await expect(pickerSearch).toBeFocused();
    const groupHeadings = creationCategoryPicker(page).locator(".listbox-group-label");
    const groupCount = await groupHeadings.count();
    expect(groupCount).toBeGreaterThan(1);
    await pickerSearch.fill(fixture.department);
    // A department name matches every group beneath it, and the groups left
    // with no match are hidden — the same rule the two-step catalog applies.
    const visibleHeadings = creationCategoryPicker(page).locator(".listbox-group-label:visible");
    await expect(visibleHeadings).toHaveCount(1);
    await expect(visibleHeadings.first()).toContainText(fixture.department);
    await expect(
      creationCategoryPicker(page).locator(".listbox-option").filter({ hasText: fixture.category }),
    ).toBeVisible();
    // Escape closes without changing anything.
    await page.keyboard.press("Escape");
    await expect(pickerButton).toHaveAttribute("aria-expanded", "false");
    await expect(pickerButton).toContainText(fixture.category);
    await page.getByRole("button", { name: "Create ticket", exact: true }).click();
    await expect(page).toHaveURL(/\/tickets$/);
    const created = page.getByRole("link", { name: "Published hierarchy ticket", exact: true });
    await expect(created).toHaveCount(1);
    const row = created.locator("xpath=ancestor::tr");
    await expect(row.locator("td.priority .ticket-priority-value")).toHaveText("High");
    await created.click();
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.locator("#ticket-detail .detail-meta-category")).toContainText(
      fixture.category,
    );
    await expect(page.locator("#ticket-detail .ticket-description")).toContainText(
      "Kept description",
    );

    await expect(obs.pageErrors).toEqual([]);
    await expect(obs.failedResponses).toEqual([]);
  });
  test("changing the category preserves the ticket-creation draft", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);

    // A second runnable category gives the draft a different scope it must not
    // leak into. The seeded "General" category is the scope it belongs to.
    // The catalog fixture is created through the mobile action rail.
    await page.setViewportSize({ width: 390, height: 844 });
    const other = await createPublishedHierarchyFixture(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    const draftTitle = "Draft survives the picker " + Date.now();

    // The draft exists because changing category is a NAVIGATION: that is the
    // two-step flow (#302), so this journey runs the instance in that mode and
    // restores the default afterwards.
    await setTicketCreationMode(page, "two_step");
    try {
      await page.goto(base() + "/tickets/new");
      await selectCatalogCategory(page, "General");
      await page.getByLabel("Title").fill(draftTitle);
      await page.getByLabel("Description").fill("Draft description");
      await chooseCreationPriority(page, "high");

      // Change the category through the link back to the catalog and re-select
      // the SAME one.
      await page.getByRole("link", { name: "Change", exact: true }).click();
      await expect(page.locator(".create-catalog")).toBeVisible();
      await selectCatalogCategory(page, "General");
      await expect(page.getByLabel("Title")).toHaveValue(draftTitle);
      await expect(page.getByLabel("Description")).toHaveValue("Draft description");
      await expectCreationPriority(page, "High");

      // Back/Forward must not lose it either.
      await page.goBack();
      await expect(page.locator(".create-catalog")).toBeVisible();
      await page.goForward();
      await expect(page.getByLabel("Title")).toHaveValue(draftTitle);
      await expect(page.getByLabel("Description")).toHaveValue("Draft description");
      await expectCreationPriority(page, "High");

      // A different category inherits the compatible values (issue #302): the
      // handoff opened from this form, so the target pulls the draft forward.
      await page.getByRole("link", { name: "Change", exact: true }).click();
      await selectCatalogCategory(page, other.category);
      await expect(page.getByLabel("Title")).toHaveValue(draftTitle);
      await expect(page.getByLabel("Description")).toHaveValue("Draft description");
      await expectCreationPriority(page, "High");

      // Back on the original category the draft is still there; creating the
      // ticket then clears it so the next creation does not inherit the text.
      await page.getByRole("link", { name: "Change", exact: true }).click();
      await selectCatalogCategory(page, "General");
      await expect(page.getByLabel("Title")).toHaveValue(draftTitle);
      await page.getByRole("button", { name: "Create ticket", exact: true }).click();
      await expect(page).toHaveURL(/\/tickets$/);

      await page.goto(base() + "/tickets/new");
      await selectCatalogCategory(page, "General");
      await expect(page.getByLabel("Title")).toHaveValue("");
      await expect(page.getByLabel("Description")).toHaveValue("");
      await expectCreationPriority(page, "Medium");
    } finally {
      await setTicketCreationMode(page, "single");
    }
  });

  test("a draft value the target category cannot take is confirmed before it is dropped", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.setViewportSize({ width: 390, height: 844 });
    const other = await createPublishedHierarchyFixture(page);
    await page.setViewportSize({ width: 1280, height: 800 });

    const draftTitle = "Incompatible priority draft " + Date.now();
    await setTicketCreationMode(page, "two_step");
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    const generalKey = `tktTicketDraft:${await page.locator('input[name="category_id"]').inputValue()}`;
    await page.getByLabel("Title").fill(draftTitle);
    await page.getByLabel("Description").fill("Kept description");
    await chooseCreationPriority(page, "high");
    // Defocus before tampering: blurring a dirty field fires `change`, which
    // would re-save the live form values over the payload under test.
    await page.evaluate(() => {
      const active = document.activeElement;
      if (active instanceof HTMLElement) active.blur();
    });

    // The stored priority is untrusted input. A value the server did not render
    // as an option for the target is the incompatible case the guard owns.
    await page.evaluate((key) => {
      const payload = JSON.parse(sessionStorage.getItem(key) as string);
      payload.priority = "extreme";
      sessionStorage.setItem(key, JSON.stringify(payload));
    }, generalKey);
    const tampered = await page.evaluate(
      (key) => JSON.parse(sessionStorage.getItem(key) as string).priority,
      generalKey,
    );
    expect(tampered).toBe("extreme");

    // Switching prompts and explains the priority that would be dropped.
    const prompts: string[] = [];
    page.once("dialog", (dialog) => {
      prompts.push(dialog.message());
      void dialog.dismiss();
    });
    await page.getByRole("link", { name: "Change", exact: true }).click();
    await selectCatalogCategory(page, other.category);
    expect(prompts).toHaveLength(1);
    expect(prompts[0]).toContain("extreme");
    // Cancelling preserves the draft and leaves the target untouched.
    await expect(page.getByLabel("Title")).toHaveValue("");
    await expect(page.getByLabel("Description")).toHaveValue("");
    await expectCreationPriority(page, "Medium");

    // The source category still holds the text.
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    await expect(page.getByLabel("Title")).toHaveValue(draftTitle);
    await expect(page.getByLabel("Description")).toHaveValue("Kept description");

    // Confirming carries the compatible values and drops only the priority.
    page.once("dialog", (dialog) => void dialog.accept());
    await page.getByRole("link", { name: "Change", exact: true }).click();
    await selectCatalogCategory(page, other.category);
    await expect(page.getByLabel("Title")).toHaveValue(draftTitle);
    await expect(page.getByLabel("Description")).toHaveValue("Kept description");
    await expectCreationPriority(page, "Medium");
    await setTicketCreationMode(page, "single");
  });

  test("the target category's own draft wins a switch and says so", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.setViewportSize({ width: 390, height: 844 });
    const other = await createPublishedHierarchyFixture(page);
    await page.setViewportSize({ width: 1280, height: 800 });

    const existing = "Target draft " + Date.now();
    const incoming = "Incoming draft " + Date.now();

    await setTicketCreationMode(page, "two_step");
    // The target gets its own draft first.
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, other.category);
    await page.getByLabel("Title").fill(existing);
    await page.getByLabel("Description").fill("Target description");
    await chooseCreationPriority(page, "low");

    // A different draft is typed in General, then switched to the target.
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    await page.getByLabel("Title").fill(incoming);
    await page.getByLabel("Description").fill("Incoming description");
    await chooseCreationPriority(page, "high");
    await page.getByRole("link", { name: "Change", exact: true }).click();
    await selectCatalogCategory(page, other.category);

    // The target's own draft stays; the notice says the source is still saved.
    await expect(page.getByLabel("Title")).toHaveValue(existing);
    await expect(page.getByLabel("Description")).toHaveValue("Target description");
    await expectCreationPriority(page, "Low");
    await expect(page.locator("#ticket-draft-notice")).toContainText("General");
    await expect(page.locator("#ticket-draft-notice")).toContainText("still saved");
    // It reads as part of the header, right under the page title (issue #380).
    await expect(page.locator(".command-header .command-title + #ticket-draft-notice")).toHaveCount(
      1,
    );

    // The source draft was not consumed by the collision.
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    await expect(page.getByLabel("Title")).toHaveValue(incoming);
    await expect(page.getByLabel("Description")).toHaveValue("Incoming description");
    await setTicketCreationMode(page, "single");
  });

  test("a tampered cross-category draft is refused by the storage guards", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsSeeded(page);
    await page.setViewportSize({ width: 390, height: 844 });
    const other = await createPublishedHierarchyFixture(page);
    await page.setViewportSize({ width: 1280, height: 800 });

    const draftTitle = "Guard draft " + Date.now();
    await setTicketCreationMode(page, "two_step");
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    const generalKey = `tktTicketDraft:${await page.locator('input[name="category_id"]').inputValue()}`;
    await page.getByLabel("Title").fill(draftTitle);
    await page.evaluate(() => {
      const active = document.activeElement;
      if (active instanceof HTMLElement) active.blur();
    });

    // A payload whose version is not the one this script wrote is ignored.
    await page.evaluate((key) => {
      const payload = JSON.parse(sessionStorage.getItem(key) as string);
      payload.v = 999;
      sessionStorage.setItem(key, JSON.stringify(payload));
    }, generalKey);
    await page.getByRole("link", { name: "Change", exact: true }).click();
    await selectCatalogCategory(page, other.category);
    await expect(page.getByLabel("Title")).toHaveValue("");

    // A payload whose scope names another category is ignored on the switch.
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    await page.getByLabel("Title").fill(draftTitle);
    await page.evaluate(() => {
      const active = document.activeElement;
      if (active instanceof HTMLElement) active.blur();
    });
    await page.evaluate((key) => {
      const payload = JSON.parse(sessionStorage.getItem(key) as string);
      payload.scope = "99999";
      sessionStorage.setItem(key, JSON.stringify(payload));
    }, generalKey);
    await page.getByRole("link", { name: "Change", exact: true }).click();
    await selectCatalogCategory(page, other.category);
    await expect(page.getByLabel("Title")).toHaveValue("");

    // Over-long text is capped, never handed to the server raw.
    await page.goto(base() + "/tickets/new");
    await selectCatalogCategory(page, "General");
    await page.getByLabel("Title").fill(draftTitle);
    await page.evaluate(() => {
      const active = document.activeElement;
      if (active instanceof HTMLElement) active.blur();
    });
    await page.evaluate((key) => {
      const payload = JSON.parse(sessionStorage.getItem(key) as string);
      payload.title = "x".repeat(600);
      sessionStorage.setItem(key, JSON.stringify(payload));
    }, generalKey);
    await page.getByRole("link", { name: "Change", exact: true }).click();
    await selectCatalogCategory(page, other.category);
    const carried = await page.getByLabel("Title").inputValue();
    expect(carried).toBe("x".repeat(512));

    // The handoff payload is untrusted too: a source that is not a category id
    // is refused even when a matching draft exists.
    await page.evaluate(
      (key) => sessionStorage.removeItem(key),
      `tktTicketDraft:${other.categoryID}`,
    );
    await page.evaluate(() => {
      sessionStorage.setItem(
        "tktTicketDraftHandoff",
        JSON.stringify({ v: 1, from: "not-a-number", name: "General" }),
      );
    });
    await page.goto(base() + `/tickets/new?category_id=${other.categoryID}`);
    await expect(page.getByLabel("Title")).toHaveValue("");
    await setTicketCreationMode(page, "single");
  });

  test("two-step creation: the catalog searches, a card opens the form, and Change goes back", async ({
    page,
  }) => {
    // Issue #391, S4/S5. Two-step mode opens on a service catalog: a search box,
    // then one section per department holding category cards captioned with their
    // desk. A card opens the shared form with the category fixed, Change returns
    // to the catalog, and a published category that cannot run stays visible with
    // its reason instead of walking the requester into a refused submit.
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setTicketCreationMode(page, "two_step");
    try {
      await page.goto(base() + "/tickets/new");
      const catalog = page.locator(".create-catalog");
      await expect(catalog).toBeVisible();
      await expect(page.getByRole("heading", { name: "Create a ticket" })).toBeVisible();
      // Issue #380: the search box and the cards already say what to do.
      await expect(
        page.getByText("Choose a category to get started.", { exact: true }),
      ).toHaveCount(0);
      const search = page.locator("[data-create-catalog-search]");
      await expect(search).toBeVisible();

      // One section per department, holding name + desk caption cards.
      const sections = catalog.locator(".create-catalog-department-group");
      expect(await sections.count()).toBeGreaterThan(0);
      const generalCard = page
        .locator(".create-card")
        .filter({ has: page.locator(".create-card-name", { hasText: /^General$/ }) });
      await expect(generalCard).toHaveCount(1);
      await expect(generalCard.locator(".create-card-desk")).toHaveText("General");

      // The search filters the cards live, by category, desk or department, and
      // hides the departments left with no match.
      await search.fill("Unrunnable");
      await expect(generalCard).toBeHidden();
      const unavailableCard = catalog.locator(".create-card-unavailable");
      await expect(unavailableCard).toHaveCount(1);
      await expect(unavailableCard).toBeVisible();
      await expect(unavailableCard).toContainText("Unrunnable Requests");
      await expect(unavailableCard).toContainText(
        "Can't run · Unstaffed Desk has no active members",
      );
      // A card that cannot run is never a link into the form.
      await expect(unavailableCard).toHaveJSProperty("tagName", "DIV");
      await expect(unavailableCard).toHaveAttribute("aria-disabled", "true");
      await expect(unavailableCard).toHaveCSS("cursor", "not-allowed");
      await expect(catalog.locator("a.create-card-unavailable")).toHaveCount(0);

      // The server-side half of the same rule: ?q keeps working with no script.
      await page.goto(base() + "/tickets/new?q=Unrunnable");
      await expect(page.locator(".create-card-unavailable")).toBeVisible();
      await expect(generalCard).toBeHidden();
      await page.goto(base() + "/tickets/new?q=nothing-here");
      await expect(page.locator(".create-catalog-empty")).toContainText(
        "No categories match your search.",
      );

      // A card opens the shared form with the category fixed.
      await page.goto(base() + "/tickets/new");
      await openCatalogCard(page, "General");
      const form = page.locator(".ticket-create-detail");
      await expect(form).toHaveCount(1);
      await expect(form.locator(".selected-catalog-path")).toContainText("General");
      await expect(form.locator('input[name="category_id"]')).toHaveCount(1);
      await expect(form.getByLabel("Title")).toBeVisible();
      await expect(form.locator(".ticket-create-timeline")).toHaveCount(0);

      // Change goes back to the catalog, not to a category picker.
      await form.getByRole("link", { name: "Change", exact: true }).click();
      await expect(page).toHaveURL(/\/tickets\/new$/);
      await expect(page.locator(".create-catalog")).toBeVisible();

      // The catalog holds its layout at 390px with no horizontal scroll.
      await page.setViewportSize({ width: 390, height: 844 });
      await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
      await expect(page.locator(".create-catalog-grid").first()).toHaveCSS(
        "grid-template-columns",
        /.+/,
      );
      await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);

      await expect(obs.pageErrors).toEqual([]);
    } finally {
      await page.setViewportSize({ width: 1280, height: 800 });
      await setTicketCreationMode(page, "single");
    }
  });

  test("search filter shows filtered results and empty state", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    // Create a uniquely titled ticket for search, plus a distractor ticket
    // so the full list (2+ tickets) is visibly different from the filtered result (1).
    // Without the distractor the filtered HTML is identical to the full list.
    const uniqueTitle = "FilterProbe " + Date.now().toString(36).slice(2, 10);
    const distractorTitle = "Distractor " + Date.now().toString(36).slice(2, 10);
    await createTicketViaUi(page, {
      title: uniqueTitle,
      description: "filter probe",
      category: "General",
      priority: "low",
    });
    await createTicketViaUi(page, {
      title: distractorTitle,
      description: "filter distractor",
      category: "General",
      priority: "low",
    });

    await page.goto(base() + "/tickets");
    await expect(page.locator("#tickets-screen")).toBeVisible();
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible();
    await expect(page.locator("#ticket-list").getByText(distractorTitle)).toBeVisible();

    const searchInput = page.getByPlaceholder(/search by id or title/i);
    await expect(searchInput).toBeVisible();
    const ticketSearchIcon = page.locator(".ticket-search .search-icon");
    for (const [name, value] of [
      ["viewBox", "0 0 24 24"],
      ["width", "18"],
      ["height", "18"],
      ["aria-hidden", "true"],
      ["focusable", "false"],
    ]) {
      await expect(ticketSearchIcon).toHaveAttribute(name, value);
    }

    // The queue filters live (issue #382): every select applies on change and
    // the search on a debounced input, with no Apply button at all.
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);

    // 1. Narrow by state, priority and category, then type the unique title:
    // the debounced search request carries every control.
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByLabel("State").selectOption("new");
        await page.getByLabel("Priority").selectOption("low");
        await page.getByLabel("Category").selectOption({ label: "General" });
        await searchInput.fill(uniqueTitle);
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === uniqueTitle &&
            parsedURL.searchParams.get("state") === "new" &&
            parsedURL.searchParams.get("priority") === "low" &&
            parsedURL.searchParams.has("category_id") &&
            parsedURL.searchParams.has("user_id")
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByLabel("Search tickets")).toHaveValue(uniqueTitle);
    await expect(page.getByLabel("State")).toHaveValue("new");
    await expect(page.getByLabel("Priority")).toHaveValue("low");
    // Every active filter is a removable chip.
    const chips = page.locator(".queue-chips .queue-chip");
    await expect(chips).toHaveCount(4);
    await expect(chips.filter({ hasText: `Search: ${uniqueTitle}` })).toHaveCount(1);
    await expect(chips.filter({ hasText: /^New/ })).toHaveCount(1);
    await expect(chips.filter({ hasText: /^Low/ })).toHaveCount(1);
    await expect(chips.filter({ hasText: /^General/ })).toHaveCount(1);
    // Removing one chip drops exactly that key and keeps the others.
    await assertHtmxSwap(
      page,
      async () => {
        await chips.filter({ hasText: /^Low/ }).click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === uniqueTitle &&
            parsedURL.searchParams.get("state") === "new" &&
            !parsedURL.searchParams.has("priority")
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?/,
      },
    );
    await expect(page.getByLabel("Priority")).toHaveValue("");
    await expect(page.locator(".queue-chips .queue-chip")).toHaveCount(3);

    // 2. Search for an impossible term — empty state
    const impossibleTerm = "zzz_no_match_" + Date.now().toString(36).replace(/[0-9]/g, "x");
    await assertHtmxSwap(
      page,
      async () => {
        await searchInput.fill(impossibleTerm);
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" && parsedURL.searchParams.get("q") === impossibleTerm
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(page.locator("#ticket-list").getByText(/no tickets match/i)).toBeVisible();
    await expect(page.getByLabel("Search tickets")).toHaveValue(impossibleTerm);

    // 3. Clear resets every toolbar control and returns page one.
    await assertHtmxSwap(
      page,
      async () => {
        await page.locator(".queue-chips-clear").click();
      },
      {
        endpoint: "/tickets",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets$/,
      },
    );
    await expect(page.locator("#ticket-list").getByText(uniqueTitle)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByLabel("Search tickets")).toHaveValue("");
    await expect(page.getByLabel("State")).toHaveValue("");
    await expect(page.getByLabel("Priority")).toHaveValue("");

    // 4. Enter preserves a nonempty assignee filter and applies AND semantics.
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByLabel("Search tickets").fill(uniqueTitle);
        await page.getByLabel("Assigned user").selectOption("1");
        await page.getByLabel("Search tickets").press("Enter");
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === uniqueTitle &&
            parsedURL.searchParams.get("user_id") === "1"
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(page.locator("#ticket-list").getByText(/no tickets match/i)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByLabel("Assigned user")).toHaveValue("1");

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets search filter swap",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("a filtered queue returns with its search, filters and order from the ticket detail", async ({
    page,
  }) => {
    test.setTimeout(90_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // One uniquely titled probe, assigned to the seeded operator so the queue
    // can also filter by assigned user with a visible result.
    const title = `ReturnProbe ${Date.now().toString(36).slice(2, 10)}`;
    const ticketID = await createTicketViaUi(page, {
      title,
      description: "filtered queue return probe",
      category: "General",
      priority: "low",
    });
    const ticketPath = `/tickets/${ticketID}`;
    const openProbe = async () => {
      await page.locator("#ticket-list").getByRole("link", { name: title, exact: true }).click();
      await expect(page).toHaveURL(base() + ticketPath);
      await expect(page.locator("#ticket-detail")).toBeVisible();
    };
    const applyProperty = async (property: "priority" | "assignee", value: string) => {
      const selectID = property === "priority" ? "ticket-priority" : "assign-user";
      const endpoint = ticketPath + (property === "priority" ? "/edit" : "/assign");
      const button = pickerButton(page, selectID as "ticket-priority" | "assign-user");
      // The picker's options are submit buttons; `value` is the field value
      // (a priority level, or the user id, "" for Unassigned).
      const option = pickerOption(page, selectID as "ticket-priority" | "assign-user", value);
      const queueHref = await page.getByRole("link", { name: "Queue" }).getAttribute("href");
      if (!queueHref) throw new Error(`Missing Queue href at ${page.url()}`);
      // Issue #343 S2: the editable detail names the ticket in exactly one h1;
      // the apply swap must keep it, the visible title and the sidebar chrome.
      await expect(page.locator("h1")).toHaveCount(1);
      await expect(page.locator("h1")).toHaveText(title);
      // The notification bell refreshes itself (its own count poll), so the
      // chrome compared here is the sidebar without it.
      const railChrome = () =>
        page.locator(".sidebar").evaluate((rail) => {
          const copy = rail.cloneNode(true) as HTMLElement;
          copy.querySelector("#notification-bell")?.remove();
          return copy.innerHTML;
        });
      const chrome = await railChrome();
      const response = await assertHtmxSwap(
        page,
        async () => {
          await openPicker(page, selectID as "ticket-priority" | "assign-user");
          await option.click();
        },
        { endpoint, method: "POST", expectedStatus: 200, hxTarget: "#ticket-detail" },
      );
      expect(response.request().headers()["x-tkt-queue-return"]).toBe(queueHref);
      await expect(option).toHaveAttribute("aria-selected", "true");
      await expect(button).not.toBeEmpty();
      await expect(page.getByLabel("Ticket title")).toBeVisible();
      await expect(page.getByLabel("Ticket title")).toHaveValue(title);
      await expect(page.getByRole("link", { name: "Queue" })).toHaveAttribute("href", queueHref);
      await expect(page.locator("h1")).toHaveCount(1);
      await expect(page.locator("h1")).toHaveText(title);
      expect(await railChrome()).toBe(chrome);
    };
    await openProbe();
    await applyProperty("assignee", "1");

    // Build the filtered queue through the real toolbar: search + state +
    // priority + category + assigned user + order.
    await page.goto(base() + "/tickets");
    await expect(page.locator("#tickets-screen")).toBeVisible();
    const categoryID = await page
      .locator('.filter-bar select[name="category_id"] option')
      .filter({ hasText: /^General$/ })
      .getAttribute("value");
    if (!categoryID) {
      throw new Error(`cannot resolve the General category id at ${page.url()}`);
    }
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByLabel("Search tickets").fill(title);
        await page.getByLabel("State").selectOption("new");
        await page.getByLabel("Priority").selectOption("low");
        await page.getByLabel("Category").selectOption(categoryID);
        await page.getByLabel("Assigned user").selectOption("1");
        await page.getByLabel("Order by").selectOption("priority");
      },
      {
        endpoint: (url) => {
          const u = new URL(url);
          return (
            u.pathname === "/tickets" &&
            u.searchParams.get("q") === title &&
            u.searchParams.get("state") === "new" &&
            u.searchParams.get("priority") === "low" &&
            u.searchParams.get("category_id") === categoryID &&
            u.searchParams.get("user_id") === "1" &&
            u.searchParams.get("sort") === "priority"
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?.*sort=priority/,
      },
    );
    await expect(page.locator("#ticket-list").getByText(title)).toBeVisible();

    // Open the ticket by an ordinary navigation; the browser sends the filtered
    // list URL as the Referer, and the detail's Queue link mirrors it.
    await page.locator("#ticket-list").getByRole("link", { name: title, exact: true }).click();
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const queueHref = await page.getByRole("link", { name: "Queue" }).getAttribute("href");
    const queueURL = new URL(queueHref ?? "", base());
    expect(queueURL.pathname).toBe("/tickets");
    for (const [key, value] of [
      ["q", title],
      ["state", "new"],
      ["priority", "low"],
      ["category_id", categoryID],
      ["user_id", "1"],
      ["sort", "priority"],
    ]) {
      expect(queueURL.searchParams.get(key), `Queue href parameter ${key}`).toBe(value);
    }

    // Return via Queue: the list comes back filtered, ordered, and populated.
    await page.getByRole("link", { name: "Queue" }).click();
    await expect(page).toHaveURL(/\/tickets\?.*sort=priority/);
    const returnedURL = new URL(page.url());
    expect(returnedURL.searchParams.get("q")).toBe(title);
    expect(returnedURL.searchParams.get("state")).toBe("new");
    expect(returnedURL.searchParams.get("priority")).toBe("low");
    expect(returnedURL.searchParams.get("category_id")).toBe(categoryID);
    expect(returnedURL.searchParams.get("user_id")).toBe("1");
    expect(returnedURL.searchParams.get("sort")).toBe("priority");
    await expect(page.getByLabel("Search tickets")).toHaveValue(title);
    await expect(page.getByLabel("Order by")).toHaveValue("priority");
    await expect(page.locator("#ticket-list").getByText(title)).toBeVisible();

    // Browser Back/Forward round-trips the detail and the filtered queue.
    await page.goBack();
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await page.goForward();
    await expect(page.locator("#ticket-list").getByText(title)).toBeVisible();
    expect(new URL(page.url()).searchParams.get("sort")).toBe("priority");

    const returnToExcludedProbe = async (sourceQueue: URL) => {
      await page.getByRole("link", { name: "Queue" }).click();
      await expect(page).toHaveURL(sourceQueue.href);
      await expect(page.getByLabel("Search tickets")).toHaveValue(title);
      await expect(page.getByLabel("Order by")).toHaveValue("priority");
      await expect(page.getByLabel("Priority")).toHaveValue(
        sourceQueue.searchParams.get("priority") ?? "",
      );
      await expect(page.getByLabel("Assigned user")).toHaveValue("1");
      // The changed priority/assignee legitimately excludes the probe.
      await expect(page.locator("#ticket-list").getByText(/no tickets match/i)).toBeVisible();
      await expect(
        page.locator("#ticket-list").getByRole("link", { name: title, exact: true }),
      ).toHaveCount(0);
    };

    await test.step("priority updates preserve the filtered queue across repeated detail swaps", async () => {
      await openProbe();
      await applyProperty("priority", "critical");
      await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");
      await applyProperty("priority", "high");
      await returnToExcludedProbe(returnedURL);
      await page.goto(base() + ticketPath);
      await expect(pickerButton(page, "ticket-priority")).toContainText("High");
    });

    await test.step("assignee updates preserve the filtered queue across repeated detail swaps", async () => {
      // Keep the original search/order, but drop the now-stale priority filter.
      const assignmentQueue = new URL(returnedURL.href);
      assignmentQueue.searchParams.delete("priority");
      await page.goto(assignmentQueue.href);
      await openProbe();
      await applyProperty("assignee", "");
      await expect(pickerButton(page, "assign-user")).toContainText("Unassigned");
      await applyProperty("assignee", "1");
      await expect(pickerButton(page, "assign-user")).toContainText("Alice Admin");
      await applyProperty("assignee", "");
      await returnToExcludedProbe(assignmentQueue);
      await page.goto(base() + ticketPath);
      await expect(pickerButton(page, "assign-user")).toContainText("Unassigned");
    });

    await test.step("ordinary queue gains no search after priority and assignment swaps", async () => {
      await page.goto(base() + "/tickets");
      await expect(page.getByLabel("Search tickets")).toHaveValue("");
      await openProbe();
      await applyProperty("priority", "critical");
      await applyProperty("assignee", "1");
      await expect(page.getByRole("link", { name: "Queue" })).toHaveAttribute("href", "/tickets");
      await page.getByRole("link", { name: "Queue" }).click();
      await expect(page).toHaveURL(base() + "/tickets");
      await expect(page.getByLabel("Search tickets")).toHaveValue("");
      await openProbe();
      await expect(pickerButton(page, "ticket-priority")).toContainText("Critical");
      await expect(pickerButton(page, "assign-user")).toContainText("Alice Admin");
      await page.getByRole("link", { name: "Queue" }).click();
      await expect(page).toHaveURL(base() + "/tickets");
    });

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets filtered queue return",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("pagination preserves its query across HTMX, reload, and mobile rows", async ({ page }) => {
    test.setTimeout(90_000);
    const obs = collectObservability(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();

    const alphaSuffix = Date.now()
      .toString(36)
      .replace(/\d/g, (digit) => String.fromCharCode(97 + Number(digit)));
    const prefix = `PageProbe${alphaSuffix}`;
    const longTitle = `${prefix} long title ${"that wraps without losing ticket metadata ".repeat(4)}`;
    const priorities = ["critical", "high", "medium", "low"];
    const priorityIndicators = [
      ["low", "Low", "rgb(24, 115, 77)"],
      ["medium", "Medium", "rgb(234, 179, 8)"],
      ["high", "High", "rgb(180, 35, 24)"],
      ["critical", "Critical", "rgb(122, 39, 26)"],
    ] as const;
    const assertPriorityIndicators = async () => {
      const neutralTextColor = await page
        .locator("#ticket-list td.cell-title")
        .first()
        .evaluate((element) => getComputedStyle(element).color);
      for (const [priority, label, color] of priorityIndicators) {
        const value = page
          .locator(`#ticket-list td.priority.${priority} .ticket-priority-value`)
          .first();
        await expect(value).toBeVisible();
        await expect(value).toHaveText(label);
        const indicator = await value.evaluate((element) => {
          const style = getComputedStyle(element);
          const marker = getComputedStyle(element, "::before");
          return {
            color: style.color,
            display: style.display,
            flexDirection: style.flexDirection,
            gap: style.gap,
            content: marker.content,
            background: marker.backgroundColor,
            width: marker.width,
            height: marker.height,
            radius: marker.borderRadius,
            shrink: marker.flexShrink,
          };
        });
        expect(["flex", "inline-flex"]).toContain(indicator.display);
        expect(indicator).toMatchObject({
          color: neutralTextColor,
          flexDirection: "row",
          gap: "6px",
          content: '""',
          background: color,
          width: "7px",
          height: "7px",
          radius: "50%",
          shrink: "0",
        });
      }
    };
    for (let index = 0; index < 11; index += 1) {
      await createTicketViaUi(page, {
        title: index === 10 ? longTitle : `${prefix} entry ${String.fromCharCode(97 + index)}`,
        description: "pagination probe",
        category: "General",
        priority: priorities[index % priorities.length],
      });
    }

    await page.goto(base() + `/tickets?q=${encodeURIComponent(prefix)}`);
    await expect(page.getByText(longTitle, { exact: true })).toBeVisible();
    await expect(page.locator(".page-subtitle")).toHaveText("11 tickets");
    await assertPriorityIndicators();
    await expect(page.getByRole("link", { name: "Next page", exact: true })).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("link", { name: "Next page", exact: true }).click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === prefix &&
            parsedURL.searchParams.get("page") === "2"
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?.*page=2/,
      },
    );
    await expect(page.getByText("Page 2 of 2", { exact: false })).toBeVisible();
    await expect(page.getByLabel("Search tickets")).toHaveValue(prefix);
    await page.reload();
    await expect(page.getByLabel("Search tickets")).toHaveValue(prefix);
    await expect(page.getByText("Page 2 of 2", { exact: false })).toBeVisible();

    // A filter change (here the sort) drops the page and keeps the search.
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByLabel("Order by").selectOption("priority");
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === prefix &&
            parsedURL.searchParams.get("sort") === "priority" &&
            !parsedURL.searchParams.has("page")
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(page.getByText("Page 1 of 2", { exact: false })).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("link", { name: "Next page", exact: true }).click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === prefix &&
            parsedURL.searchParams.get("page") === "2"
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?.*page=2/,
      },
    );
    await assertHtmxSwap(
      page,
      async () => {
        await page.locator(".queue-chips-clear").click();
      },
      {
        endpoint: "/tickets",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets$/,
      },
    );
    await expect(page.getByLabel("Search tickets")).toHaveValue("");
    await expect(page.getByText("Page 1 of", { exact: false })).toBeVisible();

    await page.goto(base() + `/tickets?q=${encodeURIComponent(prefix)}`);
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
    const rows = page.locator("#ticket-list tbody tr");
    await expect(rows).toHaveCount(10);
    await expect(page.getByText(longTitle, { exact: true })).toBeVisible();
    // The triage queue (issue #382) carries seven metadata cells per row while
    // SLA is off (the SLA column appears only with a commitment or the setting
    // on). Assert the WHOLE current geometry: every label below has one cell
    // per row, and the total is rows x labels, so a future column is a visible
    // change here rather than a silent gap.
    const metadataLabels = ["ID", "Ticket", "Category", "Assignee", "Priority", "State", "Updated"];
    const metadataCells = rows.locator("td[data-label]");
    await expect(metadataCells).toHaveCount(10 * metadataLabels.length);
    for (const label of metadataLabels) {
      const cells = rows.locator(`td[data-label="${label}"]`);
      await expect(cells).toHaveCount(10);
      for (let index = 0; index < 10; index += 1) {
        const box = await cells.nth(index).boundingBox();
        expect(box, `${label} row ${index} has no visible geometry`).not.toBeNull();
        expect(
          box!.width,
          `${label} row ${index} is constrained to a desktop column`,
        ).toBeGreaterThan(250);
      }
    }
    expect(new Set(await rows.locator('td[data-label="Priority"]').allTextContents())).toEqual(
      new Set(["Critical", "High", "Medium", "Low"]),
    );
    await assertPriorityIndicators();
    await expect(rows.locator('td[data-label="Ticket"] a').first()).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 390,
      label: "tickets pagination query and mobile metadata",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("public comment is persisted and appears in timeline", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const title = "Comment probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "comment probe",
      category: "General",
      priority: "medium",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    const commentBody = "Public comment " + Date.now().toString(36).slice(2, 6);
    await page.getByLabel(/comment body/i).fill(commentBody);
    // Comment form is a native POST (no hx-post) — submit and wait for navigation
    const responsePromise = waitForExactPost(page, `/tickets/${id}/comments`);
    await page.getByRole("button", { name: "Reply", exact: true }).click();
    const response = await responsePromise;
    expect(response.status()).toBe(303);
    expect(new URL(page.url()).pathname).toBe(`/tickets/${id}`);
    await expect(page.locator("#timeline")).toContainText(commentBody);
    // Reload persistence
    await page.reload();
    await expect(page.locator("#timeline")).toContainText(commentBody);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets public comment",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("real transition with visible result (new → in_progress)", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    const title = "Transition probe " + Date.now().toString(36).slice(2, 8);
    const id = await createTicketViaUi(page, {
      title,
      description: "transition probe",
      category: "General",
      priority: "high",
    });
    await page.goto(base() + `/tickets/${id}`);
    await expect(page.locator("#ticket-detail")).toBeVisible();
    await expect(page.getByText("New").first()).toBeVisible();
    await expect(transitionAction(page, "in_progress")).toBeVisible();

    await assertHtmxSwap(
      page,
      async () => {
        await applyTransition(page, "in_progress");
      },
      {
        endpoint: `/tickets/${id}/transition`,
        method: "POST",
        expectedStatus: 200,
        hxTarget: "#ticket-detail",
      },
    );

    await expect(page.getByText("In Progress").first()).toBeVisible({
      timeout: 10_000,
    });
    // Timeline should contain transition event
    await expect(page.locator("#timeline")).toContainText(/in.progress/i);
    // Persistence
    await page.reload();
    await expect(page.getByText("In Progress").first()).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets transition new→in_progress",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});

/**
 * Ticket list SLA visibility (issue #211, PR 4).
 *
 * SLA is instance-wide: a commitment is frozen ONLY when a ticket is created
 * while the switch is ON. Every journey here enables the switch, creates its
 * own committed fixtures, and disables it again in afterEach (setSLAEnabled)
 * so no later journey inherits it. A ticket created while the switch is OFF
 * carries no commitment — that is the real "empty cell" absence case (this
 * suite's seeder creates no tickets, so the absence ticket is created through
 * the real UI before enabling, not fabricated).
 */
test.describe("Ticket list SLA visibility (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });
  test.afterEach(async ({ page }) => {
    // Restore the instance-wide switch whatever this test left behind, so the
    // next journey (here or in another describe on the same database) sees the
    // state it expects. Do not rely on declaration order for correctness.
    await page.context().clearCookies();
    await loginAsSeeded(page);
    await setSLAEnabled(page, false);
  });

  test("staff list names an on_track commitment and keeps an uncommitted ticket empty", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    const suffix = Date.now().toString(36).slice(2, 8);
    const uncommittedTitle = `SLA uncommitted ${suffix}`;
    const committedTitle = `SLA committed ${suffix}`;

    // Absence case: created while SLA is still OFF, so no commitment is
    // frozen. This is the same shape a pre-enable seeded row would have.
    await createTicketViaUi(page, {
      title: uncommittedTitle,
      description: "created before SLA was enabled",
      category: "General",
      priority: "high",
    });

    await setSLAEnabled(page, true);

    // Committed case: created while SLA is ON, so the category's frozen
    // policy rides along.
    await createTicketViaUi(page, {
      title: committedTitle,
      description: "created while SLA was enabled",
      category: "General",
      priority: "high",
    });

    await page.goto(base() + "/tickets");
    await expect(page.locator("#ticket-list")).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "SLA", exact: true })).toBeVisible();

    const rowFor = (title: string) =>
      page
        .locator("#ticket-list tbody tr")
        .filter({ has: page.getByRole("link", { name: title, exact: true }) });

    const committedRow = rowFor(committedTitle);
    await expect(committedRow).toHaveCount(1);
    const committedCell = committedRow.locator('td[data-label="SLA"]');
    // on_track is the quiet default: the cell shows the pending deadline, with
    // no pill and no state dot at all.
    // Every state speaks: an on_track commitment names itself. An empty cell
    // means the ticket has no commitment at all, and the two must not look
    // alike. The milestone and its instant live in the ticket's own panel.
    await expect(committedCell.locator(".badge, .sla-dot")).toHaveCount(1);
    await expect(committedCell).toContainText("On Track");
    await expect(committedCell).not.toContainText("First response");
    await expect(committedCell.locator("time")).toHaveCount(0);

    const uncommittedRow = rowFor(uncommittedTitle);
    await expect(uncommittedRow).toHaveCount(1);
    const uncommittedCell = uncommittedRow.locator('td[data-label="SLA"]');
    await expect(uncommittedCell).toHaveText("");
    await expect(uncommittedCell.locator(".badge, time")).toHaveCount(0);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets list SLA badge and empty cell",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });

  test("Order by applies urgency and priority and keeps the choice across a reload", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setSLAEnabled(page, true);

    const suffix = Date.now().toString(36).slice(2, 8);
    // The older ticket is HIGH (1h response target) and the newer is LOW (8h),
    // so urgency (earliest outstanding deadline first) is the REVERSE of the
    // default newest-first order. Priority (high > low) is likewise reversed.
    const urgentTitle = `SLA urgent ${suffix}`;
    const calmTitle = `SLA calm ${suffix}`;
    await createTicketViaUi(page, {
      title: urgentTitle,
      description: "older, high priority",
      category: "General",
      priority: "high",
    });
    await createTicketViaUi(page, {
      title: calmTitle,
      description: "newer, low priority",
      category: "General",
      priority: "low",
    });

    const expectOrder = async (firstTitle: string, secondTitle: string) => {
      const titles = await page.locator("#ticket-list td.cell-title a").allTextContents();
      const first = titles.indexOf(firstTitle);
      const second = titles.indexOf(secondTitle);
      expect(first, `"${firstTitle}" missing from [${titles.join(", ")}]`).toBeGreaterThanOrEqual(
        0,
      );
      expect(second, `"${secondTitle}" missing from [${titles.join(", ")}]`).toBeGreaterThanOrEqual(
        0,
      );
      expect(first).toBeLessThan(second);
    };

    await page.goto(base() + "/tickets");
    await expect(page.getByLabel("Order by")).toHaveValue("newest");
    await expectOrder(calmTitle, urgentTitle);

    await assertHtmxSwap(
      page,
      async () => {
        await page.getByLabel("Order by").selectOption("urgency");
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" && parsedURL.searchParams.get("sort") === "urgency"
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?.*sort=urgency/,
      },
    );
    await expect(page.getByLabel("Order by")).toHaveValue("urgency");
    await expectOrder(urgentTitle, calmTitle);

    // The pushed URL keeps the choice across a reload.
    await page.reload();
    await expect(page.getByLabel("Order by")).toHaveValue("urgency");
    await expectOrder(urgentTitle, calmTitle);

    await assertHtmxSwap(
      page,
      async () => {
        await page.getByLabel("Order by").selectOption("priority");
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" && parsedURL.searchParams.get("sort") === "priority"
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#queue-results",
        expectedUrl: /\/tickets\?.*sort=priority/,
      },
    );
    await expect(page.getByLabel("Order by")).toHaveValue("priority");
    await expectOrder(urgentTitle, calmTitle);
    await page.reload();
    await expect(page.getByLabel("Order by")).toHaveValue("priority");
    await expectOrder(urgentTitle, calmTitle);

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "tickets order by urgency and priority",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});

/**
 * Create-form category picker (issues #247, #391).
 *
 * The picker is a native select, so it carries the viability facts the whole
 * create flow reads: a published-but-unrunnable category is present, disabled,
 * and says why (#239/#247); a healthy category stays selectable; and a
 * never-published one stays absent. Issue #391 put that select into the form
 * itself — GET /tickets/new?category_id=0 is the request that leaves no category
 * selected — and the enhancement above it must not lose any of it.
 */
test.describe("Create category picker options (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("an unrunnable published category is a disabled picker option that says why", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL(/\/tickets/);

    // No category is selected, so the form renders the picker itself. This is a
    // real GET path the page exposes (`?category_id=0` parses as "none").
    await page.goto(base() + "/tickets/new?category_id=0");
    const select = page.locator("#category_id");
    await expect(select).toBeVisible();

    // "Unrunnable Requests" is published while staffed, then unstaffed: the only
    // way to reach a published category that cannot move a ticket. It must stay
    // visible, disabled, and explain itself rather than be offered.
    const unrunnable = select.locator("option").filter({ hasText: "Unrunnable Requests" });
    await expect(unrunnable).toHaveCount(1);
    await expect(unrunnable).toBeDisabled();
    await expect(unrunnable).toContainText("Can't run · Unstaffed Desk has no active members");

    // A published-and-runnable category stays a normal, selectable option.
    const runnable = select.locator("option").filter({ hasText: /^General$/ });
    await expect(runnable).toHaveCount(1);
    await expect(runnable).toBeEnabled();

    // Never published: absent, exactly as before.
    await expect(
      select.locator("option").filter({ hasText: "Legacy Support Category" }),
    ).toHaveCount(0);

    await expect(obs.pageErrors).toEqual([]);
  });
});

// Issue #248 asked the requester picker's counts to agree with what it offers.
// The redesigned creation (issue #391) no longer counts anything: the single-page
// picker groups by desk and the two-step catalog shows every card. The invariant
// that still matters — and that #248 protected — is which categories those
// surfaces offer: only published ones, and never a draft. A desk holding one
// published and one draft category must therefore show exactly one of them on
// each requester surface.
test.describe("Requester creation surfaces (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("a draft category is offered by neither creation flow while the published one is", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await expect(
      page.locator(".tickets-header").getByRole("heading", { name: "Tickets", exact: true }),
    ).toBeVisible();

    await page.setViewportSize({ width: 390, height: 844 });
    const fixture = await createPublishedHierarchyFixture(page);
    await page.setViewportSize({ width: 1280, height: 800 });

    // Add a second, UNPUBLISHED category to the same desk: the admin structure
    // lists it, the requester surfaces must not.
    await page.goto(base() + "/categories?view=structure");
    const structureDesk = catalogNode(page, "desk", fixture.desk);
    await expect(structureDesk).toHaveCount(1);
    const deskID = await catalogNodeId(structureDesk, `desk ${fixture.desk}`);
    expect(deskID, `cannot resolve fixture desk ${fixture.desk} at ${page.url()}`).toBeTruthy();
    await openCatalogNewMenu(page, "New category");
    const categoryDrawer = page.getByRole("dialog", { name: /New category/i });
    await expect(categoryDrawer).toBeVisible();
    await categoryDrawer.locator("select[name=department_id]").selectOption(fixture.departmentID);
    await categoryDrawer.locator("select[name=desk_id]").selectOption(deskID);
    await categoryDrawer.locator("#category-name").fill(`${fixture.category} Draft`);
    await categoryDrawer.getByRole("button", { name: /create category/i }).click();
    await expect(
      page.locator(`[data-level="desk"][data-catalog-id="${deskID}"] [data-level="category"]`),
    ).toHaveCount(2);

    // The single-page picker offers exactly one option in that desk's group.
    await page.goto(base() + "/tickets/new");
    const pickerButton = creationCategoryPicker(page).locator(".listbox-button");
    await pickerButton.click();
    const deskGroup = creationCategoryPicker(page)
      .locator(".listbox-group")
      .filter({ has: page.locator(".listbox-group-label", { hasText: fixture.desk }) });
    await expect(deskGroup).toHaveCount(1);
    await expect(deskGroup.locator(".listbox-option")).toHaveCount(1);
    await expect(deskGroup).toContainText(fixture.category);
    await expect(deskGroup).not.toContainText(`${fixture.category} Draft`);
    await page.keyboard.press("Escape");

    // The two-step catalog offers exactly the same single card.
    await setTicketCreationMode(page, "two_step");
    try {
      await page.goto(base() + "/tickets/new");
      const search = page.locator("[data-create-catalog-search]");
      await search.fill(fixture.category);
      const cards = page.locator(".create-card").filter({ hasText: fixture.category });
      await expect(cards).toHaveCount(1);
      await expect(cards).not.toContainText(`${fixture.category} Draft`);
    } finally {
      await setTicketCreationMode(page, "single");
    }

    await expect(obs.pageErrors).toEqual([]);
  });
});

/**
 * Issue #416, S2/S3/S4/S7: the same catalog a requester already has, plus up to
 * three suggested categories built from what they wrote. The engines themselves
 * — ranking, thresholds, typo correction and the decision model with its
 * fallback — are Go's (handlers_category_suggestions_test.go,
 * category_suggestion_service_test.go, category_suggest_index_test.go). These
 * journeys prove the two pickers: the chips under Category on the single page,
 * and the Suggested group above the two-step catalog.
 */
test.describe("Ticket category suggestions (seeded)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("single-page creation suggests a category from the description and the chip picks it", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    // S7: the feature ships off, and with it off the create form is the form it
    // has always been — no suggestion container and no suggestion request wired.
    await page.goto(base() + "/tickets/new");
    await expect(page.locator(".ticket-create-detail")).toBeVisible();
    await expect(page.locator(".create-suggestions")).toHaveCount(0);
    await expect(page.locator('[hx-get^="/tickets/suggestions"]')).toHaveCount(0);

    // A runnable category carrying suggestion keywords is what the local engine
    // matches, and "On this server" is the engine that reads them.
    const fixture = await createSuggestableCategory(page, "impresora, tóner");
    await setCategorySuggestionEngine(page, "local");
    try {
      await page.goto(base() + "/tickets/new");
      const host = page.locator(singlePageSuggestionSelector);
      await expect(host).toHaveCount(1);
      const title = `Broken device ${Date.now().toString(36)}`;
      await page.getByLabel("Title").fill(title);

      // Typing the keyword reaches the fragment over HTMX and swaps the chips
      // under the Category row: exact endpoint, exact query, 200, no navigation,
      // no change to the page's chrome.
      await assertHtmxSwap(
        page,
        async () => {
          await page.getByLabel("Description").fill("the impresora is not working");
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === "/tickets/suggestions" &&
              parsedURL.searchParams.get("mode") === "single" &&
              (parsedURL.searchParams.get("description") ?? "").includes("impresora")
            );
          },
          method: "GET",
          expectedStatus: 200,
          hxTarget: singlePageSuggestionSelector,
        },
      );
      await expect(host.locator(".create-suggestions-title")).toHaveText(
        "Suggested from your description",
      );
      const chip = host.locator(".create-suggestion-chip");
      await expect(chip).toHaveCount(1);
      await expect(chip.locator(".create-suggestion-chip-name")).toHaveText(fixture.category);
      await expect(chip.locator(".create-suggestion-chip-desk")).toHaveText(
        `${fixture.department} · ${fixture.desk}`,
      );

      // A typo of the keyword still reaches the same category: the engine's
      // correction is the reason, and the requester only sees the chip.
      const typoResponse = page.waitForResponse(
        (response) =>
          response.request().headers()["hx-request"] === "true" &&
          response.request().method() === "GET" &&
          new URL(response.url()).pathname === "/tickets/suggestions" &&
          (new URL(response.url()).searchParams.get("description") ?? "").includes("impresroa"),
      );
      await page.getByLabel("Description").fill("the impresroa is not working");
      expect((await typoResponse).status()).toBe(200);
      await expect(page).toHaveURL(base() + "/tickets/new");
      await expect(host.locator(".create-suggestion-chip-name")).toHaveText(fixture.category);

      // One click picks the category in the picker the form already has: the
      // native field that submits holds it, and the listbox reads it back.
      await chip.click();
      await expect(page.locator('.ticket-create-detail select[name="category_id"]')).toHaveValue(
        fixture.categoryID,
      );
      await expect(creationCategoryPicker(page).locator(".listbox-button")).toContainText(
        fixture.category,
      );
      await expect(chip).toHaveAttribute("aria-pressed", "true");
      await expect(page).toHaveURL(base() + "/tickets/new");

      // A description that names no category leaves the catalog as it was: the
      // fragment comes back empty and no suggestion is rendered (S3).
      await assertHtmxSwap(
        page,
        async () => {
          await page.getByLabel("Description").fill("a completely unrelated request about parking");
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === "/tickets/suggestions" &&
              parsedURL.searchParams.get("mode") === "single" &&
              (parsedURL.searchParams.get("description") ?? "").includes("parking")
            );
          },
          method: "GET",
          expectedStatus: 200,
          hxTarget: singlePageSuggestionSelector,
        },
      );
      await expect(host).toHaveText("");
      await expect(page.locator(".create-suggestion-chip")).toHaveCount(0);

      // The chosen category survived the suggestion refreshes, so the ticket is
      // created with the category the chip picked.
      await page.getByRole("button", { name: "Create ticket", exact: true }).click();
      await expect(page).toHaveURL(/\/tickets$/);
      const created = page.getByRole("link", { name: title, exact: true });
      await expect(created).toHaveCount(1);
      await created.click();
      await expect(page.locator("#ticket-detail .detail-meta-category")).toContainText(
        fixture.category,
      );

      await expect(obs.pageErrors).toEqual([]);
      await expect(obs.failedResponses).toEqual([]);
    } finally {
      await setCategorySuggestionEngine(page, "off");
    }
  });

  test("two-step creation suggests a category above the catalog and keeps the departments", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await setTicketCreationMode(page, "two_step");
    const fixture = await createSuggestableCategory(page, "wifi, inalámbrica");
    await setCategorySuggestionEngine(page, "local");
    try {
      await page.goto(base() + "/tickets/new");
      const catalog = page.locator(".create-catalog");
      await expect(catalog).toBeVisible();
      const search = page.locator("[data-create-catalog-search]");
      const suggested = page.locator(catalogSuggestionSelector);
      await expect(suggested).toHaveCount(1);

      // The search box accepts a description, and the Suggested group above the
      // catalog arrives from the same fragment endpoint over HTMX. The host is an
      // empty live region before the first answer, so it has no box yet.
      await assertHtmxSwap(
        page,
        async () => {
          await search.fill("wifi is not working");
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === "/tickets/suggestions" &&
              parsedURL.searchParams.get("mode") === "two_step" &&
              (parsedURL.searchParams.get("q") ?? "").includes("wifi")
            );
          },
          method: "GET",
          expectedStatus: 200,
          hxTarget: catalogSuggestionSelector,
          allowEmptyTarget: true,
        },
      );
      await expect(suggested.getByRole("heading", { name: "Suggested" })).toBeVisible();
      const tile = suggested.locator(".create-card-suggested");
      await expect(tile).toHaveCount(1);
      await expect(tile.locator(".create-card-name")).toHaveText(fixture.category);
      await expect(tile.locator(".create-card-matches")).toHaveText("Matches: wifi");
      // S3: the departments stay whole under the group, because choosing from the
      // catalog is still the way to pick a category the engine did not name.
      expect(
        await page.locator(".create-catalog-department-group:visible").count(),
      ).toBeGreaterThan(0);
      await expect(page.locator(".create-catalog-empty")).toHaveCount(0);

      // A plain category-name search still filters the catalog the way it does
      // today: only the matching card remains, with its own reason when it cannot
      // run. It is never suggested, because only runnable categories are offered.
      await assertHtmxSwap(
        page,
        async () => {
          await search.fill("Unrunnable");
        },
        {
          endpoint: (url) => {
            const parsedURL = new URL(url);
            return (
              parsedURL.pathname === "/tickets/suggestions" &&
              parsedURL.searchParams.get("mode") === "two_step" &&
              (parsedURL.searchParams.get("q") ?? "").includes("Unrunnable")
            );
          },
          method: "GET",
          expectedStatus: 200,
          hxTarget: catalogSuggestionSelector,
        },
      );
      await expect(suggested).toHaveText("");
      const unavailable = page.locator(".create-card-unavailable");
      await expect(unavailable).toBeVisible();
      await expect(unavailable).toContainText("Unrunnable Requests");
      await expect(unavailable).toContainText("Unstaffed Desk has no active members");
      await expect(page.locator(".create-catalog-department-group:visible")).toHaveCount(1);

      // With the feature off the catalog is the catalog: no suggestion group, no
      // suggestion request, and a description filters to nothing exactly as it
      // did before the feature existed (S7).
      await setCategorySuggestionEngine(page, "off");
      await page.goto(base() + "/tickets/new");
      await expect(page.locator(catalogSuggestionSelector)).toHaveCount(0);
      await expect(page.locator('[hx-get^="/tickets/suggestions"]')).toHaveCount(0);
      await page.locator("[data-create-catalog-search]").fill("wifi is not working");
      await expect(page.locator(".create-card:visible")).toHaveCount(0);
      await page.goto(base() + "/tickets/new?q=wifi+is+not+working");
      await expect(page.locator(".create-catalog-empty")).toContainText(
        "No categories match your search.",
      );
      await expect(page.locator(catalogSuggestionSelector)).toHaveCount(0);

      await expect(obs.pageErrors).toEqual([]);
      await expect(obs.failedResponses).toEqual([]);
    } finally {
      await setCategorySuggestionEngine(page, "off");
      await setTicketCreationMode(page, "single");
    }
  });
});

/**
 * Issue #391, S8: creating a ticket must not depend on JavaScript in EITHER
 * mode. Both creation surfaces are progressive enhancements of native markup —
 * a grouped <select> and a GET form — so with scripting off the picker is a
 * plain select and the catalog's server-side ?q is the search.
 */
test.describe("Ticket creation without scripting (seeded)", () => {
  test.use({ javaScriptEnabled: false });

  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("single-page mode creates from the native select", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);

    await page.goto(base() + "/tickets/new");
    // No script ran, so the enhancement never replaced the select.
    await expect(page.locator(".create-listbox")).toHaveCount(0);
    const select = page.locator('.ticket-create-detail select[name="category_id"]');
    await expect(select).toBeVisible();
    // One optgroup per "Department › Desk".
    const groups = select.locator("optgroup");
    expect(await groups.count()).toBeGreaterThan(0);
    await select.selectOption({ label: "General" });

    const title = "No-script single page " + Date.now().toString(36);
    await page.getByLabel(/title/i).fill(title);
    await page.getByLabel(/description/i).fill("Created without a script");
    await page.locator('.ticket-create-detail select[name="priority"]').selectOption("low");
    await page.getByRole("button", { name: /create ticket/i }).click();
    await expect(page).toHaveURL(/\/tickets$/);
    const row = page.getByRole("link", { name: title, exact: true });
    await expect(row).toHaveCount(1);
    await expect(row.locator("xpath=ancestor::tr").locator("td.priority")).toContainText("Low");

    await expect(obs.pageErrors).toEqual([]);
  });

  test("two-step mode creates from the server-rendered catalog", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    // The setting is a plain form POST, so it saves with no script either.
    await setTicketCreationMode(page, "two_step");
    try {
      await page.goto(base() + "/tickets/new");
      await expect(page.locator(".create-catalog")).toBeVisible();
      // The server-side search filters the cards, because the live filter needs
      // a script this context does not run.
      const search = page.locator("[data-create-catalog-search]");
      await search.fill("General");
      await search.press("Enter");
      await expect(page).toHaveURL(/\/tickets\/new\?q=General$/);
      const card = page.locator(".create-card").filter({
        has: page.locator(".create-card-name", { hasText: /^General$/ }),
      });
      await expect(card).toHaveCount(1);
      await expect(card).toBeVisible();
      await card.click();

      await expect(page.locator(".ticket-create-detail")).toBeVisible();
      await expect(page.locator(".selected-catalog-path")).toContainText("General");
      const title = "No-script two steps " + Date.now().toString(36);
      await page.getByLabel(/title/i).fill(title);
      await page.getByLabel(/description/i).fill("Created from a catalog card");
      await page.locator('.ticket-create-detail select[name="priority"]').selectOption("medium");
      await page.getByRole("button", { name: /create ticket/i }).click();
      await expect(page).toHaveURL(/\/tickets$/);
      await expect(page.getByRole("link", { name: title, exact: true })).toHaveCount(1);
    } finally {
      await setTicketCreationMode(page, "single");
    }

    await expect(obs.pageErrors).toEqual([]);
  });
});
