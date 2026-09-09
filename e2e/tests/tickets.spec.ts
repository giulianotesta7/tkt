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
} from "./helpers/layout.js";
import { createTicketViaUi } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";
import { assertHtmxNoSwap, assertHtmxSwap } from "./helpers/htmx.js";

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
      const alpha = backgroundColor.match(
        /^rgba\(\d+,\s*\d+,\s*\d+,\s*([\d.]+)\)$/,
      )?.[1];
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
  desk: string;
  category: string;
  categoryID: string;
};

async function createPublishedHierarchyFixture(
  page: import("@playwright/test").Page,
): Promise<PublishedHierarchyFixture> {
  const suffix = Date.now().toString(36);
  const fixture = {
    department: `Technology Infrastructure Operations Department ${suffix}`,
    desk: `Enterprise Network Endpoint Support Desk ${suffix}`,
    category: `Connectivity Device Assistance Category ${suffix}`,
    categoryID: "",
  };

  await page.goto(base() + "/categories?view=structure");
  await page
    .getByLabel("Catalog creation")
    .getByRole("link", { name: "New department", exact: true })
    .click();
  const departmentDrawer = page.getByRole("dialog", {
    name: /New department/i,
  });
  await expect(departmentDrawer).toBeVisible();
  await departmentDrawer.locator("#category-name").fill(fixture.department);
  await departmentDrawer
    .getByRole("button", { name: /create department/i })
    .click();

  const departmentRow = page
    .locator(".category-level-departments .category-structure-row")
    .filter({ has: page.getByText(fixture.department, { exact: true }) });
  await expect(departmentRow).toHaveCount(1);
  const departmentHref = await departmentRow.getAttribute("href");
  const departmentID = departmentHref?.match(/department_id=(\d+)/)?.[1];
  if (!departmentID) {
    throw new Error(
      `Cannot resolve fixture department ${fixture.department} from ${departmentHref ?? "missing href"} at ${page.url()}`,
    );
  }
  await departmentRow.click();

  await page
    .getByLabel("Catalog creation")
    .getByRole("link", { name: "New desk", exact: true })
    .click();
  const deskDrawer = page.getByRole("dialog", { name: /New desk/i });
  await expect(deskDrawer).toBeVisible();
  await deskDrawer
    .locator("select[name=department_id]")
    .selectOption(departmentID);
  await deskDrawer.locator("#category-name").fill(fixture.desk);
  await deskDrawer.getByRole("button", { name: /create desk/i }).click();

  const deskRow = page
    .locator(".category-level-desks .category-structure-item")
    .filter({ has: page.getByText(fixture.desk, { exact: true }) });
  await expect(deskRow).toHaveCount(1);
  const deskHref = await deskRow
    .locator("a.category-structure-row")
    .getAttribute("href");
  const deskID = deskHref?.match(/desk_id=(\d+)/)?.[1];
  if (!deskID) {
    throw new Error(
      `Cannot resolve fixture desk ${fixture.desk} from ${deskHref ?? "missing href"} at ${page.url()}`,
    );
  }
  await deskRow.locator("a.category-structure-row").click();

  await page
    .getByLabel("Catalog creation")
    .getByRole("link", { name: "New category", exact: true })
    .click();
  const categoryDrawer = page.getByRole("dialog", { name: /New category/i });
  await expect(categoryDrawer).toBeVisible();
  await categoryDrawer.locator("select[name=desk_id]").selectOption(deskID);
  await categoryDrawer.locator("#category-name").fill(fixture.category);
  await categoryDrawer
    .getByRole("button", { name: /create category/i })
    .click();

  const categoryRow = page
    .locator(".category-level-categories .category-structure-item")
    .filter({ has: page.getByText(fixture.category, { exact: true }) });
  await expect(categoryRow).toHaveCount(1);
  const categoryHref = await categoryRow
    .locator('a[href*="/edit"]')
    .getAttribute("href");
  const categoryID = categoryHref?.match(/\/categories\/(\d+)\/edit/)?.[1];
  if (!categoryID) {
    throw new Error(
      `Cannot resolve fixture category ${fixture.category} from ${categoryHref ?? "missing href"} at ${page.url()}`,
    );
  }
  fixture.categoryID = categoryID;

  await page.goto(base() + `/categories/${categoryID}/workflow`);
  const addStep = page.locator(".workflow-add-step summary").first();
  await expect(addStep).toBeVisible();
  await addStep.click();
  const manualTask = page
    .locator(".workflow-add-options button")
    .filter({ hasText: "Manual task" })
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
  await assertHtmxNoSwap(
    page,
    async () => {
      await instructions.fill("Handle the published hierarchy ticket");
    },
    {
      endpoint: `/categories/${categoryID}/workflow`,
      method: "POST",
      expectedStatus: 200,
    },
  );
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

test.describe("Ticket Lifecycle", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });

  test.afterAll(async () => {
    await stopServer();
  });

  test("create ticket, verify in list, detail, and navigate from index", async ({
    page,
  }) => {
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
    await expect(page.getByText(ticketNumberText!)).toBeVisible();

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

  test("catalog supports hierarchy selection, search, keyboard focus, and mobile drill-down", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto(base() + "/login");
    await page.getByLabel(/email/i).fill("alice@example.com");
    await page.getByLabel(/password/i).fill("SuperSecret42!");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    const ticketTitleSizes = new Map<number, string>();
    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await page.goto(base() + "/tickets");
      const ticketsTitle = page
        .locator("#tickets-screen")
        .getByRole("heading", { name: "Tickets", exact: true });
      await expect(ticketsTitle).toBeVisible();
      ticketTitleSizes.set(
        width,
        await ticketsTitle.evaluate(
          (element) => getComputedStyle(element).fontSize,
        ),
      );
    }
    const fixture = await createPublishedHierarchyFixture(page);
    await page.goto(base() + "/tickets/new?category_id=0");
    const newTicketTitle = page
      .locator(".ticket-create-page .page-header")
      .getByRole("heading", { name: "New ticket", exact: true });
    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect(newTicketTitle).toHaveCSS(
        "font-size",
        ticketTitleSizes.get(width)!,
      );
    }
    await page.goto(base() + "/tickets/new");
    const catalogTitle = page
      .locator(".catalog-page")
      .getByRole("heading", { name: "Create a ticket", exact: true });
    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect(catalogTitle).toHaveCSS(
        "font-size",
        ticketTitleSizes.get(width)!,
      );
    }
    await page.setViewportSize({ width: 1280, height: 800 });
    await expect(catalogTitle).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "DEPARTMENTS" }),
    ).toBeVisible();
    await expect(page.getByRole("heading", { name: "DESKS" })).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "CATEGORIES" }),
    ).toBeVisible();
    await expect(
      page.getByPlaceholder(/search categories, desks, or departments/i),
    ).toBeVisible();
    await expect(
      page.getByText("Choose a category to get started.", { exact: true }),
    ).toBeVisible();
    await page
      .getByPlaceholder(/search categories, desks, or departments/i)
      .fill(fixture.category);
    const neutralCatalogInk = await page
      .locator(".catalog-category")
      .first()
      .evaluate((element) => getComputedStyle(element).color);
    const catalogBreadcrumb = page.locator(".catalog-breadcrumb");
    await expect(catalogBreadcrumb).toHaveCount(1);
    await expect(catalogBreadcrumb).toHaveCSS("color", neutralCatalogInk);
    await page
      .getByPlaceholder(/search categories, desks, or departments/i)
      .press("Enter");
    const catalogResult = page.locator(".catalog-result").filter({
      has: page.getByText(fixture.category, { exact: true }),
    });
    await expect(catalogResult).toHaveCount(1);
    await expect(catalogResult.locator("small")).toHaveCSS(
      "color",
      neutralCatalogInk,
    );
    await catalogResult.click();
    const selectedPath = page.locator(".selected-catalog-path");
    await expect(selectedPath).toContainText(fixture.department);
    await expect(selectedPath).toContainText(fixture.desk);
    await expect(selectedPath).toContainText(fixture.category);
    await expect(
      page.getByRole("heading", { name: "Create a ticket" }),
    ).toBeVisible();
    await expect(
      page.getByText("Describe your request.", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Change", exact: true }),
    ).toHaveAttribute("href", "/tickets/new");
    await expect(
      page.getByRole("link", { name: "Change", exact: true }),
    ).toHaveCount(1);
    await expect(page.getByText("Ticket details", { exact: true })).toHaveCount(
      0,
    );
    const createForm = page.locator(".ticket-create-detail");
    const selectedCreateTitle = createForm.getByRole("heading", {
      name: "Create a ticket",
      exact: true,
    });
    const descriptionCard = page.locator(".ticket-create-description");
    const timeline = page.locator(".ticket-create-timeline");
    const properties = page.locator("aside.evidence");
    await expect(createForm).toHaveCount(1);
    await expect(createForm.locator(".cards")).toHaveCount(1);
    await expect(properties).toContainText("Properties");
    await expect(properties).toContainText("Requester");
    await expect(properties).toContainText("Alice Admin");
    await expect(properties).toContainText(fixture.department);
    await expect(properties).toContainText(fixture.desk);
    await expect(properties).toContainText(fixture.category);
    await expect(properties).toContainText("Assignment");
    await expect(properties).toContainText("Not assigned yet");
    await expect(properties).not.toHaveClass(/card/);
    await expect(timeline).toContainText("No activity yet.");
    await expect(
      createForm.getByText("Add comment", { exact: true }),
    ).toHaveCount(0);
    await expect(
      createForm.locator("[name=to], [name=user_id], [name=assignee_id]"),
    ).toHaveCount(0);
    await expect(createForm.locator("input[name^=requester]")).toHaveCount(0);
    await page.setViewportSize({ width: 1536, height: 900 });
    const titleBox = await page.getByLabel("Title").boundingBox();
    const descriptionBox = await descriptionCard.boundingBox();
    const propertiesBox = await properties.boundingBox();
    expect(titleBox && descriptionBox && propertiesBox).not.toBeNull();
    expect(titleBox!.y).toBeLessThan(descriptionBox!.y);
    expect(propertiesBox!.x - (descriptionBox!.x + descriptionBox!.width)).toBe(
      16,
    );
    expect(descriptionBox!.width).toBeGreaterThan(800);
    await page.setViewportSize({ width: 1280, height: 800 });
    const description1280 = await descriptionCard.boundingBox();
    const properties1280 = await properties.boundingBox();
    expect(description1280 && properties1280).not.toBeNull();
    expect(
      properties1280!.x - (description1280!.x + description1280!.width),
    ).toBe(16);
    const mainCanvas = page.getByRole("main");
    const bodyCanvas = page.locator("body");
    const titleInput = page.getByLabel("Title");
    await expect(mainCanvas).toHaveCount(1);
    await expect(bodyCanvas).toHaveCount(1);
    await expect(titleInput).toHaveCount(1);
    await titleInput.evaluate((element) => (element as HTMLElement).blur());
    const creationGeometry = new Map<
      number,
      {
        main: { x: number; y: number; width: number; height: number };
        rail: { x: number; y: number; width: number; height: number };
        railStyle: string;
        canvas: { body: string; main: string; rail: string };
      }
    >();

    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await expect(selectedCreateTitle).toHaveCSS(
        "font-size",
        ticketTitleSizes.get(width)!,
      );
      const main = await descriptionCard.evaluate((element) =>
        element.getBoundingClientRect().toJSON(),
      );
      const rail = await properties.evaluate((element) =>
        element.getBoundingClientRect().toJSON(),
      );
      const railStyle = await properties.evaluate((element) => {
        const style = getComputedStyle(element);
        return [style.backgroundColor, style.border, style.borderRadius].join(
          "|",
        );
      });
      const title = await titleInput.evaluate((element) => {
        const style = getComputedStyle(element);
        const box = element.getBoundingClientRect();
        return {
          backgroundColor: style.backgroundColor,
          borderTopColor: style.borderTopColor,
          borderTopWidth: style.borderTopWidth,
          right: box.right,
          width: box.width,
        };
      });
      const descriptionBackground = await descriptionCard.evaluate(
        (element) => getComputedStyle(element).backgroundColor,
      );
      expect(title.borderTopWidth).toBe("1px");
      expect(title.borderTopColor).not.toBe("transparent");
      expect(title.borderTopColor).not.toBe("rgba(0, 0, 0, 0)");
      expect(title.backgroundColor).toBe(descriptionBackground);
      expect(title.backgroundColor).not.toBe("transparent");
      expect(title.backgroundColor).not.toBe("rgba(0, 0, 0, 0)");
      expect(title.width).toBeLessThanOrEqual(main.width);
      if (width >= 1280) {
        expect(title.width).toBeGreaterThanOrEqual(540);
        expect(title.width).toBeLessThanOrEqual(560);
      } else {
        expect(title.right).toBeLessThanOrEqual(main.right);
        await expect(page.locator("body")).toHaveJSProperty(
          "scrollWidth",
          width,
        );
      }
      creationGeometry.set(width, {
        main,
        rail,
        railStyle,
        canvas: {
          body: await effectivePaintedBackground(bodyCanvas),
          main: await effectivePaintedBackground(mainCanvas),
          rail: await effectivePaintedBackground(properties),
        },
      });
    }
    await page.setViewportSize({ width: 1280, height: 800 });
    let priorityRequests = 0;
    let priorityNavigations = 0;
    const requestObserver = (request: import("@playwright/test").Request) => {
      if (
        request.url().startsWith(base()) &&
        ["GET", "POST"].includes(request.method())
      ) {
        priorityRequests += 1;
      }
    };
    const navigationObserver = (frame: import("@playwright/test").Frame) => {
      if (frame === page.mainFrame()) priorityNavigations += 1;
    };
    page.on("request", requestObserver);
    page.on("framenavigated", navigationObserver);
    await page.getByLabel("Priority").selectOption("high");
    await expect(page.getByLabel("Priority")).toHaveValue("high");
    page.off("request", requestObserver);
    page.off("framenavigated", navigationObserver);
    expect(priorityRequests).toBe(0);
    expect(priorityNavigations).toBe(0);
    await page.getByLabel("Title").fill("   ");
    await page
      .getByLabel("Description")
      .fill("Retained validation description");
    const validationResponse = waitForExactPost(page, "/tickets");
    await page
      .getByRole("button", { name: "Create ticket", exact: true })
      .click();
    expect((await validationResponse).status()).toBe(422);
    await expect(page.getByRole("alert")).toContainText(/title is required/i);
    await expect(page.getByLabel("Title")).toHaveValue("   ");
    await expect(page.getByLabel("Description")).toHaveValue(
      "Retained validation description",
    );
    await expect(page.getByLabel("Priority")).toHaveValue("high");
    await expect(selectedPath).toContainText(fixture.department);
    await expect(selectedPath).toContainText(fixture.desk);
    await expect(selectedPath).toContainText(fixture.category);
    await expect(titleInput).toBeFocused();
    const focusedTitleStyle = await titleInput.evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        borderBottomColor: style.borderBottomColor,
        borderTopColor: style.borderTopColor,
        boxShadow: style.boxShadow,
      };
    });
    expect(focusedTitleStyle.borderTopColor).toBe(
      focusedTitleStyle.borderBottomColor,
    );
    expect(focusedTitleStyle.boxShadow).not.toBe("none");
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(titleInput).toBeVisible();
    await expect(page.getByLabel("Description")).toBeVisible();
    await expect(page.getByLabel("Priority")).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Change", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Create ticket", exact: true }),
    ).toBeVisible();
    await expect(properties).toBeVisible();
    const mobileDescriptionBox = await descriptionCard.boundingBox();
    const mobilePropertiesBox = await properties.boundingBox();
    expect(mobileDescriptionBox && mobilePropertiesBox).not.toBeNull();
    expect(mobilePropertiesBox!.y).toBeGreaterThan(mobileDescriptionBox!.y);
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
    const routeBox = await selectedPath.boundingBox();
    expect(
      routeBox,
      `selected hierarchy has no geometry at ${page.url()}`,
    ).not.toBeNull();
    expect(
      routeBox!.height,
      "long hierarchy must wrap across multiple lines",
    ).toBeGreaterThan(42);
    const longRetainedTitle = `Published hierarchy ticket ${"that stays within the title input ".repeat(12)}`;
    await titleInput.fill(longRetainedTitle);
    await expect(titleInput).toHaveValue(longRetainedTitle);
    const longTitleBox = await titleInput.boundingBox();
    expect(longTitleBox).not.toBeNull();
    expect(longTitleBox!.x + longTitleBox!.width).toBeLessThanOrEqual(390);
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
    await titleInput.fill("Published hierarchy ticket");
    await page
      .getByRole("button", { name: "Create ticket", exact: true })
      .click();
    await expect(page).toHaveURL(/\/tickets$/);
    const publishedTicketLink = page.getByRole("link", {
      name: "Published hierarchy ticket",
      exact: true,
    });
    await expect(publishedTicketLink).toHaveCount(1);
    await expect(publishedTicketLink).toBeVisible();
    const publishedTicketRow =
      publishedTicketLink.locator("xpath=ancestor::tr");
    await expect(publishedTicketRow).toHaveCount(1);
    const publishedTicketPriority = publishedTicketRow.locator(
      "td.priority .ticket-priority-value",
    );
    await expect(publishedTicketPriority).toHaveCount(1);
    await expect(publishedTicketPriority).toHaveText("High");
    await publishedTicketLink.click();

    await expect(page.locator("#ticket-detail")).toBeVisible();
    for (const [width, height] of [
      [1536, 900],
      [1280, 800],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      const creation = creationGeometry.get(width);
      const detailDescription = page
        .locator("#ticket-detail .conversation > .card")
        .first();
      const detailRail = page.locator("#ticket-detail .evidence");
      const detailMain = await detailDescription.evaluate((element) =>
        element.getBoundingClientRect().toJSON(),
      );
      const detailRailBox = await detailRail.evaluate((element) =>
        element.getBoundingClientRect().toJSON(),
      );
      const detailRailStyle = await detailRail.evaluate((element) => {
        const style = getComputedStyle(element);
        return [style.backgroundColor, style.border, style.borderRadius].join(
          "|",
        );
      });
      const detailCanvas = {
        body: await effectivePaintedBackground(bodyCanvas),
        main: await effectivePaintedBackground(mainCanvas),
        rail: await effectivePaintedBackground(detailRail),
      };
      expect(creation, `missing creation geometry at ${width}px`).toBeDefined();
      expect(detailCanvas.body).toBe(creation!.canvas.body);
      expect(detailCanvas.main).toBe(creation!.canvas.main);
      expect(detailCanvas.rail).toBe(creation!.canvas.rail);
      expect(detailMain.x).toBe(creation!.main.x);

      expect(detailMain.width).toBe(creation!.main.width);
      expect(detailRailBox.x).toBe(creation!.rail.x);
      expect(detailRailBox.width).toBe(creation!.rail.width);
      expect(detailRailStyle).toBe(creation!.railStyle);
      if (width > 900) {
        expect(detailRailBox.x - (detailMain.x + detailMain.width)).toBe(16);
      } else {
        expect(detailRailBox.y).toBeGreaterThan(detailMain.y);
      }
    }
    await page.goto(base() + "/tickets/new");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(
      page.locator(".catalog-mobile-departments .catalog-departments"),
    ).toBeVisible();
    const generalDepartment = page
      .locator(".catalog-departments .catalog-item")
      .filter({ has: page.getByText("General", { exact: true }) });
    await expect(generalDepartment).toHaveCount(1);
    await generalDepartment.click();
    await expect(
      page.locator(".catalog-mobile-desks .catalog-desks"),
    ).toBeVisible();
    const generalDesk = page
      .locator(".catalog-desks .catalog-item")
      .filter({ has: page.getByText("General", { exact: true }) });
    await expect(generalDesk).toHaveCount(1);
    await generalDesk.click();
    await expect(
      page.locator(".catalog-mobile-categories .catalog-categories"),
    ).toBeVisible();
    await expect(page.locator("body")).toHaveJSProperty("scrollWidth", 390);
    const generalCategory = page.locator(".catalog-category").filter({
      has: page.getByText("General", { exact: true }),
    });
    await expect(generalCategory).toHaveCount(1);
    await generalCategory.focus();
    await expect(generalCategory).toBeFocused();
    await expect(obs.consoleErrors).toEqual([
      "Failed to load resource: the server responded with a status of 422 (Unprocessable Entity)",
    ]);
    await expect(obs.pageErrors).toEqual([]);
  });

  test("search filter shows filtered results and empty state", async ({
    page,
  }) => {
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
    const distractorTitle =
      "Distractor " + Date.now().toString(36).slice(2, 10);
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
    await expect(
      page.locator("#ticket-list").getByText(uniqueTitle),
    ).toBeVisible();
    await expect(
      page.locator("#ticket-list").getByText(distractorTitle),
    ).toBeVisible();

    const searchInput = page.getByPlaceholder(/search by id or title/i);
    await expect(searchInput).toBeVisible();

    // 1. Search for the unique title — filtered result visible.
    // Fill and submit inside the trigger so the interceptor is armed before
    // the form's HTMX GET is dispatched.
    await assertHtmxSwap(
      page,
      async () => {
        await searchInput.fill(uniqueTitle);
        await page.getByLabel("State").selectOption("new");
        await page.getByLabel("Priority").selectOption("low");
        await page.getByLabel("Category").selectOption({ label: "General" });
        await page.getByRole("button", { name: "Apply", exact: true }).click();
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
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(
      page.locator("#ticket-list").getByText(uniqueTitle),
    ).toBeVisible({ timeout: 10_000 });
    await expect(page.getByLabel("Search tickets")).toHaveValue(uniqueTitle);
    await expect(page.getByLabel("State")).toHaveValue("new");
    await expect(page.getByLabel("Priority")).toHaveValue("low");

    // 2. Search for an impossible term — empty state
    const impossibleTerm =
      "zzz_no_match_" + Date.now().toString(36).replace(/[0-9]/g, "x");
    await assertHtmxSwap(
      page,
      async () => {
        await searchInput.fill(impossibleTerm);
        await page.getByRole("button", { name: "Apply", exact: true }).click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === impossibleTerm
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(
      page.locator("#ticket-list").getByText(/no tickets match/i),
    ).toBeVisible();
    await expect(page.getByLabel("Search tickets")).toHaveValue(impossibleTerm);

    // 3. Clear resets every toolbar control and returns page one.
    await assertHtmxSwap(
      page,
      async () => {
        await page
          .getByRole("link", { name: "Clear filters", exact: true })
          .click();
      },
      {
        endpoint: "/tickets",
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets$/,
      },
    );
    await expect(
      page.locator("#ticket-list").getByText(uniqueTitle),
    ).toBeVisible({ timeout: 10_000 });
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
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(
      page.locator("#ticket-list").getByText(/no tickets match/i),
    ).toBeVisible({ timeout: 10_000 });
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

  test("pagination preserves its query across HTMX, reload, and mobile rows", async ({
    page,
  }) => {
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
          .locator(
            `#ticket-list td.priority.${priority} .ticket-priority-value`,
          )
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
        title:
          index === 10
            ? longTitle
            : `${prefix} entry ${String.fromCharCode(97 + index)}`,
        description: "pagination probe",
        category: "General",
        priority: priorities[index % priorities.length],
      });
    }

    await page.goto(base() + `/tickets?q=${encodeURIComponent(prefix)}`);
    await expect(page.getByText(longTitle, { exact: true })).toBeVisible();
    await expect(page.locator(".page-subtitle")).toHaveText("11 tickets");
    await assertPriorityIndicators();
    await expect(
      page.getByRole("link", { name: "Next page", exact: true }),
    ).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await page
          .getByRole("link", { name: "Next page", exact: true })
          .click();
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

    await assertHtmxSwap(
      page,
      async () => {
        await page.getByRole("button", { name: "Apply", exact: true }).click();
      },
      {
        endpoint: (url) => {
          const parsedURL = new URL(url);
          return (
            parsedURL.pathname === "/tickets" &&
            parsedURL.searchParams.get("q") === prefix &&
            !parsedURL.searchParams.has("page")
          );
        },
        method: "GET",
        expectedStatus: 200,
        hxTarget: "#tickets-screen",
        expectedUrl: /\/tickets\?q=/,
      },
    );
    await expect(page.getByText("Page 1 of 2", { exact: false })).toBeVisible();
    await assertHtmxSwap(
      page,
      async () => {
        await page
          .getByRole("link", { name: "Next page", exact: true })
          .click();
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
        await page
          .getByRole("link", { name: "Clear filters", exact: true })
          .click();
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
    const metadataCells = rows.locator("td[data-label]");
    await expect(metadataCells).toHaveCount(50);
    for (const label of ["ID", "Title", "State", "Priority", "Updated"]) {
      const cells = rows.locator(`td[data-label="${label}"]`);
      await expect(cells).toHaveCount(10);
      for (let index = 0; index < 10; index += 1) {
        const box = await cells.nth(index).boundingBox();
        expect(
          box,
          `${label} row ${index} has no visible geometry`,
        ).not.toBeNull();
        expect(
          box!.width,
          `${label} row ${index} is constrained to a desktop column`,
        ).toBeGreaterThan(250);
      }
    }
    expect(
      new Set(
        await rows.locator('td[data-label="Priority"]').allTextContents(),
      ),
    ).toEqual(new Set(["Critical", "High", "Medium", "Low"]));
    await assertPriorityIndicators();
    await expect(
      rows.locator('td[data-label="Title"] a').first(),
    ).toBeVisible();

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

  test("public comment is persisted and appears in timeline", async ({
    page,
  }) => {
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
    await page.getByRole("button", { name: /add comment/i }).click();
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

  test("real transition with visible result (new → in_progress)", async ({
    page,
  }) => {
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
    const moveSelect = page.locator("#ticket-state");
    await expect(moveSelect).toBeVisible();

    await assertHtmxSwap(
      page,
      async () => {
        await moveSelect.selectOption("in_progress");
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
