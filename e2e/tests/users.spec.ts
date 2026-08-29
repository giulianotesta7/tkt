/**
 * Users: single creation+edition journey.
 *
 * Overflow baselines are covered by structural.spec.ts (all canonical screens).
 * Exclusions (remain covered by Go tests):
 *  - password change via /users/{id}/password
 *  - deactivation / reactivation lifecycle and session invalidation (D14)
 *  - deletion
 *  - exhaustive role-change protections and authorization matrix
 * See Go tests: internal/adapters/http/handlers_users*, user_reactivate_test, etc.
 */

import { test, expect } from "@playwright/test";
import { startServer, stopServer, activeServer } from "../server-lifecycle.js";
import { assertCanonicalScreen, collectObservability } from "./helpers/layout.js";
import { resolveUserEditHref } from "./helpers/navigation.js";
import { waitForExactPost } from "./helpers/network.js";

function base(): string {
  if (!activeServer) throw new Error("server not started");
  return activeServer.baseURL;
}

async function login(page: import("@playwright/test").Page) {
  await page.goto(base() + "/login");
  await page.getByLabel(/email/i).fill("alice@example.com");
  await page.getByLabel(/password/i).fill("SuperSecret42!");
  await page.getByRole("button", { name: /log in|sign in/i }).click();
  await expect(page).toHaveURL(/\/tickets/);
}

test.describe("Users", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("creation+edition journey via UI with persistence", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const obs = collectObservability(page);
    await login(page);

    const baseName = "Probe User " + Date.now().toString(36).slice(2, 6);
    const email = `probe-${Date.now().toString(36).slice(2, 8)}@example.com`;
    const password = "ProbeSecret123!";

    // Create
    await page.goto(base() + "/users/new");
    await expect(page.locator('h1:has-text("New user")')).toBeVisible();
    await expect(page.getByRole("button", { name: /create user/i })).toBeVisible();
    await page.getByLabel(/^name$/i).fill(baseName);
    await page.getByLabel(/^email$/i).fill(email);
    await page.getByLabel(/^password$/i).fill(password);
    const createResponsePromise = waitForExactPost(page, "/users");
    await Promise.all([
      createResponsePromise,
      page.getByRole("button", { name: /create user/i }).click(),
    ]);
    const createResponse = await createResponsePromise;
    expect(createResponse.status()).toBe(303);
    expect(new URL(page.url()).pathname).toBe("/users");
    await expect(page.getByText(baseName)).toBeVisible();
    await expect(page.getByText(email)).toBeVisible();

    // Resolve edit href for that user (drawer link)
    const cleanHref = await resolveUserEditHref(page, baseName);
    await page.goto(base() + cleanHref);
    await expect(page.getByRole("heading", { name: /edit user/i })).toBeVisible();

    const renamed = baseName + " Renamed";
    await page.getByLabel(/^name$/i).fill(renamed);
    const roleSelect = page.locator('select[name="role"]');
    await expect(roleSelect).toBeVisible();
    await roleSelect.selectOption("agent");
    const saveResponsePromise = waitForExactPost(page, cleanHref);
    await Promise.all([
      saveResponsePromise,
      page.getByRole("button", { name: /save changes/i }).click(),
    ]);
    const saveResponse = await saveResponsePromise;
    expect(saveResponse.status()).toBe(200);
    await expect.poll(() => new URL(page.url()).pathname, { timeout: 10000 }).toBe("/users");
    await expect(page.getByText(renamed)).toBeVisible();

    // Persistence: reload and verify renamed visible in list
    await page.goto(base() + "/users");
    await expect(page.getByText(renamed)).toBeVisible();
    await expect(page.getByText(email)).toBeVisible();

    await assertCanonicalScreen(page, {
      viewport: 1280,
      label: "users creation+edition",
      url: page.url(),
      role: "root",
      consoleErrors: obs.consoleErrors,
      pageErrors: obs.pageErrors,
      failedRequests: obs.failedRequests,
      failedResponses: obs.failedResponses,
    });
  });
});
