# MCP server

**English** | [Español](../docs-es/mcp.md)

AI assistants such as Claude, Claude Code, VS Code, and Cursor can work in tkt through the
Model Context Protocol (MCP). Each person connects their own assistant, signs in, and approves
it once. From then on the assistant acts **as that person**: it sees and does only what the
person could in tkt, limited further by the permissions they approved. The endpoint is
`<public URL>/mcp`; root turns it on in **Settings → API**. It is off by default.

## Quick path

1. Root sets the public URL and turns on **MCP clients** ([Enable MCP](#enable-mcp)).
2. Each person adds `<public URL>/mcp` to their assistant ([Connect a client](#connect-a-client)).
3. The assistant opens tkt in the browser. Sign in, review the request, and choose **Allow access**.
4. Ask the assistant something like "list my open tickets". It calls the `search_tickets` tool.

## How it works

| Topic | Rule |
| --- | --- |
| Identity | The assistant acts as the person who approved it. Tickets, comments, and audit entries name that person. Service users can never approve a client. |
| Permissions | Effective permissions are **role ∩ approved scopes**. A scope never grants more than the person's role allows, and a role permission without its scope is refused. |
| What it can do | Ten tools that run the matching [API v1](api.md) endpoint inside tkt, with the same rules and errors. Nothing administrative is available. |
| What it cannot do | No resources, prompts, or notifications. No queue-wide `tickets:all` access. |
| Transport | Streamable HTTP without sessions: each request is a `POST` to `/mcp` carrying a Bearer token. |
| Authorization | tkt is its own OAuth 2.1 authorization server. Clients register themselves, use PKCE, and receive tokens valid only for `/mcp`. |

The MCP switch is separate from the API switch: `/mcp` works with the API off, and the API
does not depend on MCP.

## Enable MCP

Only root can turn MCP clients on or off. Administrators see the state.

1. Set the **TKT address** in **Settings → Instance** to the address people use,
   for example `https://tkt.example.com`. See [The public base URL](deployment.md#the-public-base-url).
2. Open **Settings → API** and turn on **MCP clients**. Without a TKT address, tkt refuses and points to
   **Settings → Instance**; the address cannot be cleared while MCP clients are on.
3. Share the connection URL shown there, `<public URL>/mcp`, with the people who will connect.

The public URL is the OAuth issuer and every token's audience. **Changing it disconnects every
client**: tokens issued for the old `/mcp` address are refused, and each person connects again.

Behind a [reverse proxy](deployment.md#reverse-proxy-with-https), these paths must reach tkt
unchanged:

| Path | Used by |
| --- | --- |
| `/mcp` | The MCP endpoint. |
| `/oauth/register`, `/oauth/token` | Client registration and tokens, called by the assistant. |
| `/oauth/authorize` | The consent page, opened in the person's browser. |
| `/.well-known/oauth-protected-resource`, `/.well-known/oauth-protected-resource/mcp`, `/.well-known/oauth-authorization-server` | Discovery documents. |

While the switch is off, or the public URL is empty, all of these answer 404 and existing
tokens stop working. Approvals are not revoked: turning the switch back on lets unexpired
tokens work again. To cut a client off for good, revoke it ([Revoke access](#revoke-access)).

## Connect a client

Use the connection URL, `<public URL>/mcp`, in place of `https://tkt.example.com/mcp` below.
No token, client ID, or secret is configured by hand: the assistant registers itself the
first time it connects and opens tkt in the browser for approval.

| Client | How to add tkt |
| --- | --- |
| Claude (claude.ai and the desktop app) | Add a custom connector with the URL `https://tkt.example.com/mcp`. |
| Claude Code | `claude mcp add --transport http tkt https://tkt.example.com/mcp` |
| VS Code | Add the server to `.vscode/mcp.json` (below). |
| Cursor | Add the server to `~/.cursor/mcp.json` (below). |

VS Code, `.vscode/mcp.json`:

```json
{"servers":{"tkt":{"type":"http","url":"https://tkt.example.com/mcp"}}}
```

Cursor, `~/.cursor/mcp.json`:

```json
{"mcpServers":{"tkt":{"url":"https://tkt.example.com/mcp"}}}
```

> [!NOTE]
> Where each client keeps these settings belongs to its vendor and can change between
> versions. tkt only requires that the client supports remote MCP over Streamable HTTP with
> OAuth and Dynamic Client Registration.

## The consent screen

When the assistant connects, the browser opens tkt at `/oauth/authorize`. A person who is not
signed in goes through the sign-in page first, with a password or a company account, and
returns to the consent screen afterwards.

The screen asks **"Allow <client name> to act as you?"** and shows:

- the person who is signed in;
- **Return address**: where the browser goes after the decision. Allow only an application you
  started connecting yourself;
- **What it can do**: the requested scopes.

**Allow access** returns the browser to the assistant with a single-use code. **Deny** returns
it with `error=access_denied` and nothing is granted.

| Scope | What the person sees |
| --- | --- |
| `catalog:read` | See the categories and their forms |
| `tickets:read` | Read and search the tickets you can see |
| `tickets:write` | Create tickets and work on them as you |
| `comments:write` | Write public comments as you |
| `comments:internal` | Write internal comments as you |

A client that requests no scope asks for all five. When the person approves, scopes their role
cannot use are dropped; today that is `comments:internal` for the **User** role.

If the client or its return address is not registered, or the request is invalid (for
example no PKCE `S256` challenge, no `state`, an unknown scope, or another resource), tkt
shows **"This connection cannot continue"** with the error code and never sends the browser
back to the client. Registration is open, so tkt returns to a client only after the person
approves or denies (RFC 9700 §4.11.2). Connect the assistant again from scratch.

## Tools

Each tool runs its [API v1](api.md) endpoint as the person. Inputs mirror the endpoint's body
or query parameters; `ticket_id` and the step `position` move out of the path into the input.
The API reference holds the full field rules and the response shapes.

| Tool | Scope | API v1 endpoint | Input |
| --- | --- | --- | --- |
| `whoami` | none | `GET /api/v1/me` | none |
| `list_categories` | `catalog:read` | `GET /api/v1/categories` | none |
| `search_tickets` | `tickets:read` | `GET /api/v1/tickets` | optional `q`, `state` (array), `cursor` |
| `get_ticket` | `tickets:read` | `GET /api/v1/tickets/{id}` | `ticket_id` |
| `create_ticket` | `tickets:write` | `POST /api/v1/tickets` | `category_id`, `title`, `priority`; optional `description` |
| `complete_workflow_step` | `tickets:write` | `POST /api/v1/tickets/{id}/workflow/steps/{position}/complete` | `ticket_id`, `position`; optional `answers`, `solution` |
| `transition_ticket` | `tickets:write` | `POST /api/v1/tickets/{id}/transition` | `ticket_id`, `state`; optional `reason` |
| `add_comment` | `comments:write`, or `comments:internal` when `internal` is true | `POST /api/v1/tickets/{id}/comments` | `ticket_id`, `body`; optional `internal` (boolean) |
| `confirm_resolution` | `tickets:write` | `POST /api/v1/tickets/{id}/confirm` | `ticket_id` |
| `reject_resolution` | `tickets:write` | `POST /api/v1/tickets/{id}/reject` | `ticket_id` |

- `ticket_id` is the ticket ID returned by `search_tickets`, not its display number.
- `whoami` answers the person: `id`, `name`, `role`, and the approved `scopes`.
- `search_tickets` returns pages of ten; pass `next_cursor` back as `cursor` with the same filters.
- `add_comment` sends `internal: true` as `visibility: "internal"`; otherwise the comment is public.
- A successful tool returns the endpoint's JSON response, both as text and as structured content.

## Errors

Errors at the HTTP level stop the request before any tool runs:

| Status | Body | When |
| --- | --- | --- |
| 404 | `{"error":"not_found"}` | MCP clients are off, or the public URL is empty. |
| 403 | `{"error":"forbidden"}` | An `Origin` header is present and its scheme and host differ from the public URL. |
| 401 | `{"error":"unauthorized"}` | No Bearer token. |
| 401 | `{"error":"invalid_token"}` | The token is malformed, unknown, expired, revoked, issued for another address, or its person is deactivated. |
| 429 | `{"error":"rate_limited"}` | The connection used its request budget; wait `Retry-After` seconds. |
| 403 | `{"error":"insufficient_scope"}` | A `tools/call` needs a scope the approval does not include. |

Both 401 responses carry the challenge that starts discovery; an invalid token adds
`error="invalid_token"`:

```http
WWW-Authenticate: Bearer resource_metadata="https://tkt.example.com/.well-known/oauth-protected-resource/mcp", scope="catalog:read tickets:read tickets:write comments:write comments:internal"
```

The 403 `insufficient_scope` challenge asks for the scopes already approved plus the missing
one, so approving again does not drop a scope:

```http
WWW-Authenticate: Bearer error="insufficient_scope", scope="tickets:read tickets:write", resource_metadata="https://tkt.example.com/.well-known/oauth-protected-resource/mcp"
```

When the endpoint itself refuses, the tool result has `isError: true` and its text is the
[problem](api.md#problem-details) title, status, detail, and any field errors:

```text
Invalid request (422): unknown ticket state
- state: unknown ticket state
```

## Tokens and sessions

| Item | Lifetime | Notes |
| --- | --- | --- |
| Authorization code | 60 seconds | Single use. Bound to the client, the return address, the PKCE verifier, and `/mcp`. |
| Access token (`tkta_…`) | 1 hour | Sent as `Authorization: Bearer` on `/mcp` only. |
| Refresh token (`tktr_…`) | 30 days of inactivity | Replaced on every use; each refresh restarts the 30 days. |

A refresh token works once. Presenting one that was already used revokes the whole approval:
tkt treats it as stolen or replayed, and the person connects the assistant again. tkt stores
only SHA-256 hashes of codes and tokens.

## Revoke access

| Action | Effect |
| --- | --- |
| **Preferences → Connected apps**, **Revoke** | Ends one approval at once. Each person sees and revokes only their own connections: client name, scopes, created, last used. |
| Deactivate the person | Revokes every approval they hold. Reactivating them does not restore it. |
| Turn **MCP clients** off | Every request answers 404 until it is turned back on; approvals stay. |
| Change the public URL | Every token is refused; each person connects again. |

There is no administrator view of everyone's connections.

## Security notes

- **Registration.** Clients register themselves with Dynamic Client Registration (RFC 7591) as
  public clients, with no secret. Client ID metadata documents and pre-registered clients are
  not supported. Registration is anonymous, so it is limited for the whole instance to a burst
  of 30, then one every two seconds (429 with `Retry-After`), and a registered client that
  nobody approves within 24 hours is deleted.
- **Return addresses.** A client may register up to 10: `https` with a host, `http` on
  `localhost`, `127.0.0.1`, or `[::1]`, or an app scheme such as `cursor://`. Web, script, and
  file schemes (`javascript:`, `data:`, `file:` and similar) and fragments are refused. For
  `http` on a loopback host the port is ignored when matching, because desktop clients pick
  a free port each time.
- **PKCE and state.** Only `S256` is accepted, and `state` is required. The redirect after a
  decision carries `iss` (RFC 9207) so the client can confirm it came from this tkt.
- **Audience.** Tokens are bound to `<public URL>/mcp`. The API refuses them with 401, and
  `/mcp` refuses API keys.
- **Audit.** Changes made through MCP record the person as actor and the approval as
  provenance (`oauth_grant_id`), as API changes record `api_key_id`.
- **Rate limit.** Each approval has a **60 requests/minute** bucket on `/mcp` (burst 60, one
  more per second), counted per HTTP request. The limit is local to the process and resets
  when the server restarts.

## Discovery endpoints

Clients find everything from the `/mcp` challenge; these are listed for troubleshooting.
Every endpoint answers 404 while MCP clients are off.

| Endpoint | Content |
| --- | --- |
| `GET /.well-known/oauth-protected-resource/mcp` (also without `/mcp`) | RFC 9728: `resource` (`<public URL>/mcp`), `authorization_servers`, `scopes_supported`, `bearer_methods_supported: ["header"]`. |
| `GET /.well-known/oauth-authorization-server` | RFC 8414: `issuer` (the public URL), the authorization, token, and registration endpoints, `code_challenge_methods_supported: ["S256"]`, `token_endpoint_auth_methods_supported: ["none"]`, `grant_types_supported: ["authorization_code","refresh_token"]`. |
| `POST /oauth/register` | JSON registration; 201 with `client_id`. |
| `POST /oauth/token` | Form-encoded `authorization_code` and `refresh_token` grants; responses use `Cache-Control: no-store`. |

## Next steps

- [Read the API v1 reference](api.md) for each tool's fields and responses
- [Administer MCP clients](administration.md#mcp-clients)
