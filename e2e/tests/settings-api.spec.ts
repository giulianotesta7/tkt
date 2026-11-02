import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { base, loginAsSeeded } from "./helpers/auth.js";

test.describe("API key creation popover", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("copies once, handles unavailable clipboard, and closes with Escape", async ({
    page,
    context,
  }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/api");
    await page.locator(".api-create-user summary").click();
    await page.locator("#service_user_name").fill("Popover owner");
    await page.locator(".api-create-user-form button").click();
    await page.locator(".api-service-choice").filter({ hasText: "Popover owner" }).click();
    await page.locator(".api-create-key summary:visible").click();
    await page.locator('.api-key-form:visible input[name="name"]').fill("Popover key");
    await page.locator('.api-key-form:visible input[value="tickets:read"]').check();
    const response = page.waitForResponse(
      (r) => r.url().endsWith("/settings/api/keys") && r.request().method() === "POST",
    );
    await page.locator(".api-key-form:visible button").click();
    expect((await response).headers()["cache-control"]).toBe("no-store");
    const panel = page.getByRole("dialog", { name: "Key Popover key created" });
    const input = panel.locator("[data-api-token]");
    await expect(panel).toBeVisible();
    await expect(input).toBeFocused();
    const token = await input.inputValue();
    expect(token).toMatch(/^tkt_[0-9a-f]{12}_[0-9a-f]{64}$/);
    expect(page.url()).not.toContain(token);
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await panel.getByRole("button", { name: "Copy" }).click();
    await expect(panel.getByRole("status")).toHaveText("Copied");
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(token);
    await page.evaluate(() =>
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined }),
    );
    await panel.locator("[data-api-token-copy]").click();
    await expect(panel.getByRole("status")).toContainText("copy it manually");
    await expect(input).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
    await expect(page.locator('.api-service-choice[aria-current="true"]')).toBeFocused();
    await page.goto(base() + "/settings/api");
    await expect(page.locator("[data-api-token]")).toHaveCount(0);
    expect(await page.locator("body").innerText()).not.toContain(token);
  });
});

test.describe("API service user inspector without JavaScript", () => {
  test.use({ javaScriptEnabled: false });
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("keyboard selection and rejected key keep the selected user and draft", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/api");
    for (const name of ["Inspector first", "Inspector second"]) {
      await page.locator(".api-create-user summary").click();
      await page.locator("#service_user_name").fill(name);
      await page.locator(".api-create-user-form button").click();
      await expect(page.locator(".api-service-choice").filter({ hasText: name })).toHaveCount(1);
    }
    const second = page.locator(".api-service-choice").filter({ hasText: "Inspector second" });
    await second.focus();
    await page.keyboard.press("Enter");
    await expect(second).toHaveAttribute("aria-current", "true");
    await expect(page.locator(".api-service-inspector article:visible")).toContainText(
      "Inspector second",
    );
    await page.locator(".api-create-key summary:visible").click();
    await page.locator('.api-key-form:visible input[name="name"]').fill("Draft key");
    await page.locator('.api-key-form:visible input[name="expires_on"]').fill("2099-01-01");
    await page.locator(".api-key-form:visible button").click();
    await expect(page.getByRole("alert")).toContainText("choose at least one scope");
    await expect(
      page.locator(".api-service-choice").filter({ hasText: "Inspector second" }),
    ).toHaveAttribute("aria-current", "true");
    await expect(page.locator('.api-key-form:visible input[name="name"]')).toHaveValue("Draft key");
    await expect(page.locator('.api-key-form:visible input[name="expires_on"]')).toHaveValue(
      "2099-01-01",
    );
  });

  test("counts, layout and themes in English and Spanish", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/api");
    for (const name of ["Count zero", "Count one", "Count many"]) {
      await page.locator(".api-create-user summary").click();
      await page.locator("#service_user_name").fill(name);
      await page.locator(".api-create-user-form button").click();
    }
    for (const [name, count] of [
      ["Count one", 1],
      ["Count many", 2],
    ] as const) {
      for (let i = 0; i < count; i++) {
        await page.locator(".api-service-choice").filter({ hasText: name }).click();
        await page.locator(".api-create-key summary:visible").click();
        await page
          .locator('.api-key-form:visible input[name="name"]')
          .fill(`Production synchronization ${i}`);
        await page.locator('.api-key-form:visible input[value="tickets:read"]').check();
        await page.locator(".api-key-form:visible button").click();
        await expect(page.locator("[data-api-token]")).toBeVisible();
      }
    }
    for (const [name, text] of [
      ["Count zero", "0 keys"],
      ["Count one", "1 key"],
      ["Count many", "2 keys"],
    ]) {
      await expect(page.locator(".api-service-choice").filter({ hasText: name })).toContainText(
        text,
      );
    }
    for (const theme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: theme });
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 800 });
        const measurements = await page.evaluate(() => {
          const grid = document.querySelector<HTMLElement>(".api-service-users-grid")!;
          const list = document.querySelector<HTMLElement>(".api-service-user-list")!;
          const detail = document.querySelector<HTMLElement>(".api-service-inspector")!;
          const selected = document.querySelector<HTMLElement>(
            '.api-service-choice[aria-current="true"]',
          )!;
          return {
            viewport: document.documentElement.clientWidth,
            scroll: document.documentElement.scrollWidth,
            columns: getComputedStyle(grid).gridTemplateColumns,
            list: list.getBoundingClientRect().toJSON(),
            detail: detail.getBoundingClientRect().toJSON(),
            canvas: getComputedStyle(document.body).backgroundColor,
            selected: getComputedStyle(selected).boxShadow,
            font: getComputedStyle(selected).fontFamily,
          };
        });
        console.log(`${theme} ${width}: ${JSON.stringify(measurements)}`);
        const keyLayout = await page.locator(".api-service-user:visible").evaluate((inspector) => {
          const container = inspector.querySelector<HTMLElement>(".api-keys-wrap")!;
          const name = container.querySelector<HTMLElement>(".api-key-name, td strong")!;
          const text = name.firstChild!;
          const range = document.createRange();
          range.setStart(text, 0);
          range.setEnd(text, "Production".length);
          return {
            client: container.clientWidth,
            scroll: container.scrollWidth,
            fragments: range.getClientRects().length,
            weight: getComputedStyle(document.querySelector(".api-service-choice strong")!)
              .fontWeight,
          };
        });
        console.log(`key layout ${theme} ${width}: ${JSON.stringify(keyLayout)}`);
        expect(keyLayout.scroll).toBeLessThanOrEqual(keyLayout.client);
        expect(keyLayout.fragments).toBe(1);
        expect(keyLayout.weight).toBe("600");
        expect(measurements.scroll).toBeLessThanOrEqual(measurements.viewport);
        expect(measurements.selected).not.toBe("none");
        if (width === 390)
          expect(measurements.detail.y).toBeGreaterThanOrEqual(
            measurements.list.y + measurements.list.height,
          );
        else
          expect(measurements.detail.x).toBeGreaterThan(
            measurements.list.x + measurements.list.width,
          );
      }
    }
    await page.goto(base() + "/settings/language");
    await page.locator(".settings-segment label").filter({ hasText: "Español" }).click();
    await page.getByRole("button", { name: "Save language" }).click();
    await page.goto(base() + "/settings/api");
    for (const [name, text] of [
      ["Count zero", "0 claves"],
      ["Count one", "1 clave"],
      ["Count many", "2 claves"],
    ]) {
      await expect(page.locator(".api-service-choice").filter({ hasText: name })).toContainText(
        text,
      );
    }
    await page.goto(base() + "/settings/language");
    await page.locator(".settings-segment label").filter({ hasText: "English" }).click();
    await page.locator('form[action="/settings/language"] button[type="submit"]').click();
  });

  test("selection remains on a non-first service user after successful key creation", async ({
    page,
  }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/api");
    for (const name of ["Selection baseline", "Selection target"]) {
      await page.locator(".api-create-user summary").click();
      await page.locator("#service_user_name").fill(name);
      await page.locator(".api-create-user-form button").click();
    }
    await page.locator(".api-service-choice").filter({ hasText: "Selection target" }).click();
    await page.locator(".api-create-key summary:visible").click();
    await page.locator('.api-key-form:visible input[name="name"]').fill("Selection check");
    await page.locator('.api-key-form:visible input[value="tickets:read"]').check();
    await page.locator(".api-key-form:visible button").click();
    await expect(page.locator("[data-api-token]")).toBeVisible();
    await expect(page.locator("[data-api-token-copy]")).toBeHidden();
    const selected = page.locator(".api-service-choice").filter({ hasText: "Selection target" });
    await expect(selected).toHaveAttribute("aria-current", "true");
    await page.getByRole("button", { name: "Revoke Selection check" }).click();
    await expect(selected).toHaveAttribute("aria-current", "true");
    await page.getByRole("button", { name: "Deactivate Selection target" }).click();
    await expect(selected).toHaveAttribute("aria-current", "true");
    await page.getByRole("button", { name: "Reactivate Selection target" }).click();
    await expect(selected).toHaveAttribute("aria-current", "true");
    await expect(page.locator("[data-api-token]")).toHaveCount(0);
  });

  test("a list row is clickable after resizing to a narrow viewport", async ({ page }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/api");
    for (const name of ["Narrow first", "Narrow second", "Narrow third"]) {
      await page.locator(".api-create-user summary").click();
      await page.locator("#service_user_name").fill(name);
      await page.locator(".api-create-user-form button").click();
    }
    await page.setViewportSize({ width: 1280, height: 800 });
    const row = page.locator(".api-service-choice").filter({ hasText: "Narrow third" });
    await row.click();
    await page.setViewportSize({ width: 390, height: 800 });
    // The fixed mobile tab bar can cover a partially visible row. A real user
    // scrolls it clear before clicking; Playwright's automatic scroll does not.
    const before = await row.boundingBox();
    await page.mouse.move(300, 500);
    await page.mouse.wheel(0, 400);
    await expect
      .poll(async () => (await row.boundingBox())?.y ?? 10000)
      .toBeLessThan((before?.y ?? 0) - 50);
    console.log(
      "mobile geometry",
      JSON.stringify(
        await row.evaluate((element) => {
          const rect = (el: Element) => el.getBoundingClientRect().toJSON();
          const ancestors = [];
          for (let el = element.parentElement; el; el = el.parentElement) {
            const style = getComputedStyle(el);
            ancestors.push({
              tag: el.tagName,
              className: el.className,
              overflowY: style.overflowY,
              scrollTop: el.scrollTop,
              scrollHeight: el.scrollHeight,
              clientHeight: el.clientHeight,
            });
          }
          const nav = document.querySelector(".tabbar")!;
          const top = document.querySelector(".topbar")!;
          const center = element.getBoundingClientRect();
          return {
            row: rect(element),
            tabbar: rect(nav),
            topbar: rect(top),
            hit: document
              .elementFromPoint(
                center.x + center.width / 2,
                Math.min(innerHeight - 1, Math.max(0, center.y + center.height / 2)),
              )
              ?.outerHTML.slice(0, 150),
            viewport: { width: innerWidth, height: innerHeight },
            shellPadding: getComputedStyle(document.querySelector(".shell-body")!).paddingBottom,
            ancestors,
          };
        }),
      ),
    );
    await row.click({ timeout: 5000 });
    await expect(row).toHaveAttribute("aria-current", "true");
  });
});
