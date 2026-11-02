import { test, expect } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { base, loginAsSeeded, createUserAsAdmin, grantUserRole, loginAs } from "./helpers/auth.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { waitForExactPost } from "./helpers/network.js";

// #496, S1–S4: public configuration lives in one root-owned section.
// Language propagation remains canonically owned by settings.spec.ts.
test.describe("Instance settings", () => {
  test.setTimeout(60_000);
  // A fresh seeded database per case keeps rejected-write assertions independent.
  test.beforeEach(async () => {
    await startServer({ seed: true });
  });
  test.afterEach(async () => {
    await stopServer();
  });

  test("root saves a path-bearing address once, and consumers render it read-only", async ({
    page,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/organization");
    await page
      .locator("nav.settings-nav")
      .getByRole("link", { name: "Instance", exact: true })
      .click();
    await expect(page).toHaveURL(base() + "/settings/instance");
    await expect(page.locator('.settings-nav-link[aria-current="page"]')).toHaveText("Instance");
    await expect(page.locator('nav.settings-nav a[href="/settings/language"]')).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: "Interface language", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("radio", { name: "English", exact: true })).toBeChecked();
    const about = page
      .locator("section")
      .filter({ has: page.getByRole("heading", { name: "About", exact: true }) });
    await expect(about.getByRole("heading", { name: "tkt version", exact: true })).toBeVisible();
    // The lifecycle uses an untagged go build: VCS revision, or dev for an archive.
    await expect(about.locator("p")).toHaveText(/^(dev|[0-9a-f]{40,64})$/);
    await expect(about.locator("input, button, select")).toHaveCount(0);

    const address = page.getByLabel("TKT address", { exact: true });
    const form = page.locator('form[action="/settings/instance"]');
    const language = page.locator('form[action="/settings/instance/language"]');
    const stored = await address.inputValue();
    await expect(form.locator("[data-settings-savebar]")).toBeHidden();
    await address.fill("https://tkt.example.com/help");
    await expect(language.locator("[data-settings-savebar]")).toBeHidden();
    await form.getByRole("button", { name: "Discard", exact: true }).click();
    await expect(address).toHaveValue(stored);
    await expect(form.locator("[data-settings-savebar]")).toBeHidden();
    await address.fill("https://tkt.example.com/help");
    const saved = waitForExactPost(page, "/settings/instance");
    const navigation = page.waitForEvent("framenavigated", (frame) => frame === page.mainFrame());
    await form.getByRole("button", { name: "Save TKT address", exact: true }).click();
    expect((await saved).status()).toBe(303);
    await navigation;
    await expect(page).toHaveURL(base() + "/settings/instance");
    await expect(page.locator("#save-feedback")).toContainText("Saved");
    await page.reload();
    await expect(address).toHaveValue("https://tkt.example.com/help");
    await expect(form.locator("[data-settings-savebar]")).toBeHidden();

    for (const theme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: theme });
      for (const width of [1280, 390]) {
        await page.setViewportSize({ width, height: 844 });
        // The structural helper requires navigation at the target viewport, not a live resize.
        await page.goto(base() + "/settings/instance");
        await expect(address).toHaveValue("https://tkt.example.com/help");
        await assertCanonicalScreen(page, {
          viewport: width,
          label: `Instance ${theme}`,
          url: page.url(),
          role: "root",
          consoleErrors: obs.consoleErrors,
          pageErrors: obs.pageErrors,
          failedRequests: obs.failedRequests,
          failedResponses: obs.failedResponses,
        });
      }
    }
    for (const path of ["/settings/email", "/settings/security"]) {
      await page.goto(base() + path);
      await expect(address).toHaveValue("https://tkt.example.com/help");
      await expect(address).toHaveAttribute("readonly", "");
      await expect(address).not.toHaveAttribute("name");
      await page.getByRole("link", { name: "Configure in Instance", exact: true }).click();
      await expect(page).toHaveURL(base() + "/settings/instance");
    }
    await page.goto(base() + "/settings/security");
    await expect(page.locator("#google_redirect_uri")).toHaveValue(
      "https://tkt.example.com/help/auth/google/callback",
    );
    await expect(page.locator("#microsoft_redirect_uri")).toHaveValue(
      "https://tkt.example.com/help/auth/microsoft/callback",
    );
    expect(obs.consoleErrors).toEqual([]);
    expect(obs.pageErrors).toEqual([]);
    expect(obs.failedRequests).toEqual([]);
    expect(obs.failedResponses).toEqual([]);
  });

  test("an invalid address is echoed with an error without changing the saved address", async ({
    page,
  }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/instance");
    const address = page.getByLabel("TKT address", { exact: true });
    const stored = await address.inputValue();
    await address.fill("not-a-url");
    const rejected = waitForExactPost(page, "/settings/instance");
    await page.getByRole("button", { name: "Save TKT address", exact: true }).click();
    expect((await rejected).status()).toBe(422);
    await expect(page.locator('.error-banner[role="alert"]')).toContainText(
      "the TKT address must be an http or https URL, like https://tkt.example.com",
    );
    await expect(address).toHaveValue("not-a-url");
    await page.goto(base() + "/settings/instance");
    await expect(address).toHaveValue(stored);
    await expect(page.getByRole("radio", { name: "English", exact: true })).toBeChecked();
  });

  test("administrators retain ordinary settings but cannot read or write Instance", async ({
    page,
    browser,
  }) => {
    await loginAsSeeded(page);
    await page.goto(base() + "/settings/instance");
    const stored = await page.getByLabel("TKT address", { exact: true }).inputValue();
    await createUserAsAdmin(page, {
      name: "Instance Admin",
      email: "instance-admin@example.com",
      password: "SuperSecret42!",
    });
    await grantUserRole(page, "Instance Admin", "admin");
    const context = await browser.newContext();
    try {
      const admin = await context.newPage();
      await loginAs(admin, "instance-admin@example.com", "SuperSecret42!");
      await admin.goto(base() + "/settings/organization");
      await expect(admin.getByRole("heading", { name: "Organization", exact: true })).toBeVisible();
      await expect(admin.locator('nav.settings-nav a[href="/settings/instance"]')).toHaveCount(0);
      await admin.goto(base() + "/settings/security");
      await expect(admin.getByLabel("TKT address", { exact: true })).toHaveAttribute(
        "readonly",
        "",
      );
      await expect(
        admin.getByText("Ask root to configure the address in Settings → Instance.", {
          exact: true,
        }),
      ).toBeVisible();
      const denied = await admin.goto(base() + "/settings/instance");
      expect(denied?.status()).toBe(403);
      await expect(admin.getByLabel("TKT address", { exact: true })).toHaveCount(0);
      for (const [path, form] of [
        ["/settings/instance", { instance_address: "https://unauthorized.example.com" }],
        ["/settings/instance/language", { interface_language: "es" }],
      ] as const) {
        const response = await admin.request.post(base() + path, { form });
        expect(response.status()).toBe(403);
      }
      await page.goto(base() + "/settings/instance");
      await expect(page.getByLabel("TKT address", { exact: true })).toHaveValue(stored);
      await expect(page.getByRole("radio", { name: "English", exact: true })).toBeChecked();
      await expect(page.locator("html")).toHaveAttribute("lang", "en");
    } finally {
      await context.close();
    }
  });
});
