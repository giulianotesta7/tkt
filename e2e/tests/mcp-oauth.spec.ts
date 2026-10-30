/**
 * MCP clients over OAuth (issue #493, S2/S5/S8/S10).
 *
 * One canonical journey from the root switch to a revoked connection: root
 * points the public URL at this test server and turns MCP clients on; a client
 * registers itself (RFC 7591) with a loopback redirect; the signed-in person
 * approves it on the consent page; the client exchanges the code with its PKCE
 * verifier and lists the ten tools on `/mcp`; the person finds it under
 * Preferences → Connected apps, revokes it, and the same token is refused.
 * A second journey proves the signed-out path: the authorize URL lands on the
 * sign-in page carrying `next`, and signing in returns to the consent page.
 *
 * The callback is a real loopback listener on a free port, as a desktop client
 * runs one: `page.route` cannot fulfil it, because Playwright routes only the
 * first request of a redirect chain and the callback is the target of the
 * consent page's 303. The client registers `http://127.0.0.1/callback` with no
 * port and authorizes with the listener's port, so the journey also proves the
 * port-insensitive loopback match (RFC 8252). The URL the browser lands on is
 * the evidence (`code`, `state`, `iss`). Every pane here is a native form, so the assertions are ordinary
 * navigations. Validation of every OAuth parameter, token rotation, scopes per
 * role and the tools' behaviour are Go's (`oauth_test.go`,
 * `oauth_handlers_test.go`, `mcp_test.go`).
 */

import { createHash, randomBytes } from "node:crypto";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { startServer, stopServer } from "../server-lifecycle.js";
import { loginAsSeeded, logout, base, seededCredentials } from "./helpers/auth.js";
import { collectObservability, expectNoConsoleOrPageErrors } from "./helpers/layout.js";
import { waitForExactPost } from "./helpers/network.js";

const REGISTERED_REDIRECT_URI = "http://127.0.0.1/callback";
const MCP_PROTOCOL_VERSION = "2025-11-25";
const SCOPES = ["catalog:read", "tickets:read", "tickets:write", "comments:write"];
const TOOLS = [
  "add_comment",
  "complete_workflow_step",
  "confirm_resolution",
  "create_ticket",
  "get_ticket",
  "list_categories",
  "reject_resolution",
  "search_tickets",
  "transition_ticket",
  "whoami",
];

/**
 * Turn MCP clients on as root, the way root does it: the public URL is the TKT
 * address of Email notifications, and here it is this server's own base URL, so
 * the issuer and the `/mcp` resource are addresses the test can reach. The
 * switch is instance-wide; when it is already on, nothing is saved.
 */
async function enableMCP(page: Page): Promise<void> {
  await page.goto(base() + "/settings/api");
  const section = page.locator('section[aria-labelledby="mcp-title"]');
  await expect(section.getByRole("heading", { name: "MCP clients", exact: true })).toBeVisible();
  if ((await section.locator(".api-access-state").textContent())?.trim() === "On") return;

  await page.goto(base() + "/settings/email");
  await page.getByLabel("TKT address", { exact: true }).fill(base());
  await page.getByRole("button", { name: "Save email notifications" }).click();
  await expect(page).toHaveURL(/\/settings\/email$/);
  await expect(page.locator("#save-feedback")).toContainText("Saved");

  await page.goto(base() + "/settings/api");
  const posted = waitForExactPost(page, "/settings/api/mcp");
  await section.getByRole("button", { name: "Turn on MCP clients", exact: true }).click();
  expect((await posted).status()).toBe(303);
  await expect(page).toHaveURL(/\/settings\/api$/);
  await expect(section.locator(".api-access-state")).toHaveText("On");
  await expect(section.getByRole("textbox", { name: "MCP connection URL" })).toHaveValue(
    base() + "/mcp",
  );
}

async function registerClient(request: APIRequestContext, name: string): Promise<string> {
  const res = await request.post(base() + "/oauth/register", {
    data: {
      client_name: name,
      redirect_uris: [REGISTERED_REDIRECT_URI],
      token_endpoint_auth_method: "none",
    },
  });
  expect(res.status(), await res.text()).toBe(201);
  const body = await res.json();
  expect(body.client_name).toBe(name);
  expect(body.redirect_uris).toEqual([REGISTERED_REDIRECT_URI]);
  expect(body.client_id).toMatch(/^tktcl_[0-9a-f]{32}$/);
  return body.client_id as string;
}

function pkce(): { verifier: string; challenge: string } {
  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  return { verifier, challenge };
}

/** The client's loopback listener: it answers every request with a plain page. */
async function startCallbackListener(): Promise<{ server: Server; redirectURI: string }> {
  const server = createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" });
    res.end("You can close this window.");
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return { server, redirectURI: `http://127.0.0.1:${port}/callback` };
}

function authorizePath(
  clientId: string,
  redirectURI: string,
  challenge: string,
  state: string,
): string {
  const query = new URLSearchParams({
    response_type: "code",
    client_id: clientId,
    redirect_uri: redirectURI,
    state,
    code_challenge: challenge,
    code_challenge_method: "S256",
    scope: SCOPES.join(" "),
    resource: base() + "/mcp",
  });
  return "/oauth/authorize?" + query.toString();
}

/** One JSON-RPC call on `/mcp`, as a Streamable HTTP client sends it. */
function mcpCall(
  request: APIRequestContext,
  token: string,
  body: Record<string, unknown>,
): ReturnType<APIRequestContext["post"]> {
  return request.post(base() + "/mcp", {
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: "application/json, text/event-stream",
      "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
    },
    data: { jsonrpc: "2.0", ...body },
  });
}

test.describe("MCP clients — OAuth consent and connected apps (issue #493)", () => {
  test.beforeAll(async () => {
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });

  test("a client the person approves lists the tools on /mcp until the person revokes it", async ({
    page,
    request,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await enableMCP(page);

    const clientName = "Journey <Client>";
    const clientId = await registerClient(request, clientName);
    const { verifier, challenge } = pkce();
    const state = "state-" + randomBytes(6).toString("hex");
    const listener = await startCallbackListener();
    const callbackOrigin = new URL(listener.redirectURI).origin;
    let callback: URL;
    try {
      // S5: the consent page names the client (escaped text, never markup), the
      // signed-in person, the requested scopes and the return host.
      await page.goto(base() + authorizePath(clientId, listener.redirectURI, challenge, state));
      await expect(
        page.getByRole("heading", { level: 1, name: `Allow ${clientName} to act as you?` }),
      ).toBeVisible();
      await expect(page.getByText(`Signed in as ${seededCredentials.name}.`)).toBeVisible();
      const scopes = page.locator('section[aria-labelledby="oauth-consent-scopes"] li code');
      await expect(scopes).toHaveText(SCOPES);
      await expect(
        page
          .locator('section[aria-labelledby="oauth-consent-return"]')
          .getByText(new URL(listener.redirectURI).host, { exact: true }),
      ).toBeVisible();

      const approved = waitForExactPost(page, "/oauth/authorize");
      await page.getByRole("button", { name: "Allow access", exact: true }).click();
      expect((await approved).status()).toBe(303);
      await page.waitForURL((url) => url.origin === callbackOrigin);
      await expect(page.locator("body")).toHaveText("You can close this window.");
      callback = new URL(page.url());
    } finally {
      await new Promise((resolve) => listener.server.close(resolve));
    }
    expect(callback.pathname).toBe("/callback");
    expect(callback.searchParams.get("state")).toBe(state);
    expect(callback.searchParams.get("iss")).toBe(base());
    const code = callback.searchParams.get("code");
    expect(code, `no code in ${page.url()}`).toMatch(/^tktc_[0-9a-f]{64}$/);

    // S6: the code and the PKCE verifier buy an access token for /mcp.
    const tokenRes = await request.post(base() + "/oauth/token", {
      form: {
        grant_type: "authorization_code",
        code: code as string,
        client_id: clientId,
        redirect_uri: listener.redirectURI,
        code_verifier: verifier,
        resource: base() + "/mcp",
      },
    });
    expect(tokenRes.status(), await tokenRes.text()).toBe(200);
    const tokens = await tokenRes.json();
    expect(tokens.token_type).toBe("Bearer");
    expect(tokens.scope).toBe(SCOPES.join(" "));
    const accessToken = tokens.access_token as string;
    expect(accessToken).toMatch(/^tkta_[0-9a-f]{64}$/);

    // S8: initialize, then the ten tools.
    const init = await mcpCall(request, accessToken, {
      id: 1,
      method: "initialize",
      params: {
        protocolVersion: MCP_PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: "tkt-e2e", version: "1" },
      },
    });
    expect(init.status(), await init.text()).toBe(200);
    const initBody = await init.json();
    expect(initBody.result.protocolVersion).toBe(MCP_PROTOCOL_VERSION);
    expect(initBody.result.serverInfo.name).toBe("tkt");

    const list = await mcpCall(request, accessToken, { id: 2, method: "tools/list", params: {} });
    expect(list.status(), await list.text()).toBe(200);
    const names = ((await list.json()).result.tools as { name: string }[]).map((t) => t.name);
    expect(names.sort()).toEqual(TOOLS);

    // S10: the person's connected app, and its revocation.
    await page.goto(base() + "/preferences");
    const apps = page.locator('section[aria-labelledby="connected-apps-title"]');
    await expect(apps.getByRole("heading", { name: "Connected apps", exact: true })).toBeVisible();
    const row = apps.locator("form").filter({ hasText: clientName });
    await expect(
      row,
      `Expected exactly one connected app "${clientName}" at ${page.url()}`,
    ).toHaveCount(1);
    await expect(row.locator(".field-value")).toHaveText(clientName);
    await expect(row.locator(".help")).toHaveText(
      "See the categories and their forms · Read and search the tickets you can see · Create tickets and work on them as you · Write public comments as you",
    );
    await expect(row.locator(".preferences-note")).toContainText("Connected");

    const revokePath = await row.getAttribute("action");
    expect(revokePath).toMatch(/^\/preferences\/connected-apps\/\d+\/revoke$/);
    const revoked = waitForExactPost(page, revokePath as string);
    await row.getByRole("button", { name: `Revoke ${clientName}`, exact: true }).click();
    expect((await revoked).status()).toBe(303);
    await expect(page).toHaveURL(/\/preferences$/);
    await expect(apps.locator("form")).toHaveCount(0);
    await expect(apps.getByText("No connected apps yet.", { exact: true })).toBeVisible();

    // The same token is refused at once, with the bearer challenge.
    const refused = await mcpCall(request, accessToken, {
      id: 3,
      method: "tools/list",
      params: {},
    });
    expect(refused.status()).toBe(401);
    const challengeHeader = refused.headers()["www-authenticate"] ?? "";
    expect(challengeHeader).toContain('error="invalid_token"');
    expect(challengeHeader).toContain(
      `resource_metadata="${base()}/.well-known/oauth-protected-resource/mcp"`,
    );

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
    expect(obs.failedRequests, obs.failedRequests.join("; ")).toEqual([]);
    expect(obs.failedResponses, obs.failedResponses.join("; ")).toEqual([]);
  });

  test("a signed-out person signs in and returns to the consent page", async ({
    page,
    request,
  }) => {
    const obs = collectObservability(page);
    await loginAsSeeded(page);
    await enableMCP(page);
    const clientName = "Signed-out client";
    const clientId = await registerClient(request, clientName);
    await logout(page);

    const path = authorizePath(
      clientId,
      REGISTERED_REDIRECT_URI,
      pkce().challenge,
      "signed-out-state",
    );
    await page.goto(base() + path);
    await expect(page).toHaveURL(base() + "/login?next=" + encodeURIComponent(path));
    await expect(page.locator('form input[type="hidden"][name="next"]')).toHaveValue(path);

    await page.getByLabel(/email/i).fill(seededCredentials.email);
    await page.getByLabel(/password/i).fill(seededCredentials.password);
    const signedIn = waitForExactPost(page, "/login");
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    expect((await signedIn).status()).toBe(303);
    await expect(page).toHaveURL(base() + path);
    await expect(
      page.getByRole("heading", { level: 1, name: `Allow ${clientName} to act as you?` }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Allow access", exact: true })).toBeVisible();

    expectNoConsoleOrPageErrors(obs.consoleErrors, obs.pageErrors);
    expect(obs.failedRequests, obs.failedRequests.join("; ")).toEqual([]);
    expect(obs.failedResponses, obs.failedResponses.join("; ")).toEqual([]);
  });
});
