# API v1 reference

**English** | [Español](../docs-es/api.md)

Use the JSON API to connect other systems to tkt: discover categories, create tickets,
complete requester forms, follow progress, and reply. The base path is `/api/v1`.
Root enables it in **Settings → API**; it is off by default. Create a service user
and a key as described in [API access](administration.md#api-access).

## Authentication and access

Send one header on every request:

```http
Authorization: Bearer tkt_<id>_<secret>
```

Use the complete token shown when the key was created. The API authenticates with
Bearer tokens, not browser cookies; it does not use browser sessions or CSRF tokens.
Success responses use `application/json` and `Cache-Control: no-store`.
POST bodies use `Content-Type: application/json`. Unknown body fields, malformed JSON,
and multiple JSON values are rejected with 400; an unsupported content type gets 415.

- While the API is off, every `/api/*` request returns 404, even with invalid credentials.
- Missing, malformed, unknown, expired, or revoked credentials, and keys whose owner is
  inactive, receive the same 401 response with `WWW-Authenticate: Bearer`.
- A missing endpoint scope returns 403 `scope-missing`; a role or identity restriction
  returns 403 `forbidden`. Scopes do not override the application's authorization rules.
- A ticket outside the caller's ticket scope returns the same 404 as a missing or malformed
  ticket identifier. The response does not reveal whether the ticket exists.
- Each key has a **60 requests/minute** token bucket: burst capacity 60, replenished at one
  request per second. Exhaustion returns 429 with `Retry-After` in seconds. This limit is
  process-local and resets on server restart, not a shared limit across server processes.
- `last_used_at` means **presented with valid credentials**, not “successful request”. It is
  recorded at most once per minute, even if routing, scope, body validation, or rate limiting
  later refuses the request. Rejected credentials do not update it.

### Scopes

Effective permission is **role ∩ scopes**: an endpoint checks the key's scope and the
application checks the service user's role, ticket access, and any identity requirements.

| Scope | Allows |
| --- | --- |
| `catalog:read` | List runnable categories and requester forms. |
| `tickets:read` | List, search, and read tickets within the caller's ticket scope. |
| `tickets:write` | Create tickets, transition state, complete workflow steps, confirm or reject resolution, subject to role and identity rules. |
| `comments:write` | Add public comments. |
| `comments:internal` | Add internal comments; agent service users only. |
| `tickets:all` | Agent service users only: expand ticket access to the whole queue for reads and writes. The matching endpoint scope is still required. |

A user-role service user reads its own tickets. An agent service user has no assignments
or desk memberships, so without `tickets:all` its ticket read scope is empty. It can still
create a ticket, but cannot read it back. With `tickets:all`, it remains an agent: no user
management or settings privileges, no bypass of requester/assignee identity checks, and
no ability to claim tickets or become an assignee. Agent assignment restrictions remain.
Internal comments are included in agent detail reads with `tickets:read`; writing them
requires `comments:internal`.

> [!WARNING]
> `tickets:all` exposes the whole queue. A leaked key with matching write scopes can change
> every ticket; grant it only to integrations that need queue-wide access.

## Problem details

Errors use RFC 9457 `application/problem+json`, with `Cache-Control: no-store`:

| Field | Type | Meaning |
| --- | --- | --- |
| `type` | string | Stable problem URI; use this to distinguish errors. |
| `title` | string | Short problem title. |
| `status` | integer | HTTP status. |
| `detail` | string, optional | Explanation in English; omitted when empty. Do not depend on its wording. |
| `instance` | string, optional | Request path (included for these endpoints). |
| `errors` | array, optional | Validation extension: objects with string `field` and `detail`. Included when a validation error names a field. |

For example, `GET /api/v1/tickets?state=unknown` returns 422:

```json
{
  "type": "urn:tkt:problem:validation",
  "title": "Invalid request",
  "status": 422,
  "detail": "unknown ticket state",
  "instance": "/api/v1/tickets",
  "errors": [{"field": "state", "detail": "unknown ticket state"}]
}
```

The complete problem vocabulary is:

| Type | Status | Meaning |
| --- | --- | --- |
| `urn:tkt:problem:bad-request` | 400 | Malformed JSON, invalid cursor, or invalid transport input. |
| `urn:tkt:problem:unauthorized` | 401 | Invalid credentials. |
| `urn:tkt:problem:forbidden` | 403 | Role, identity, or operation not permitted. |
| `urn:tkt:problem:scope-missing` | 403 | Key lacks the endpoint's required scope. |
| `urn:tkt:problem:not-found` | 404 | API disabled, unknown route/resource, or ticket outside scope. |
| `urn:tkt:problem:method-not-allowed` | 405 | Route does not accept this method; see the `Allow` header. |
| `urn:tkt:problem:conflict` | 409 | Resource conflict, a workflow that cannot advance, or a concurrent hold change. |
| `urn:tkt:problem:request-too-large` | 413 | Request exceeds the transport body limit. |
| `urn:tkt:problem:unsupported-media-type` | 415 | Body content type is not `application/json`. |
| `urn:tkt:problem:validation` | 422 | Invalid field input. |
| `urn:tkt:problem:rejected` | 422 | Domain rule rejects the operation, including an invalid transition or stale workflow position. |
| `urn:tkt:problem:rate-limited` | 429 | Per-key request bucket exhausted. |
| `urn:tkt:problem:internal` | 500 | Internal failure; implementation details are not exposed. |

All endpoints below can return the authentication, rate, routing, and internal errors above;
POST endpoints also share the JSON/body errors. Endpoint sections highlight additional errors.

## GET /api/v1/me

**Scope:** none beyond a valid key. No body or query parameters.
**Response:** 200, the service user's integer `id`, string `name` and `role`, and string-array `scopes`.

```json
{"id":12,"name":"Monitoring","role":"user","scopes":["catalog:read","tickets:read","tickets:write","comments:write"]}
```

## GET /api/v1/categories

**Scope:** `catalog:read`. No body or query parameters. **Response:** 200.
Only categories with a published, runnable workflow are listed; publication alone is not enough.
This list is not paginated (`next_cursor` is null).

```json
{
  "items": [{
    "id": 3, "name": "Network", "desk": "Support", "department": "IT",
    "requester_form": {
      "position": 1,
      "fields": [{"name":"location","label":"Location","type":"short_text","required":true,"options":[]}]
    }
  }],
  "next_cursor": null
}
```

`id` is an integer; names, desk, and department are strings. `requester_form` is null
when absent, otherwise the **first requester form** in the published workflow. Its integer
`position` is one-based. Each field has string `name`, `label`, and `type`, boolean `required`,
and string-array `options`. Types are `short_text`, `long_text`, `checkbox`, and `single_select`.
Missing scope: 403.

## GET /api/v1/tickets

**Scope:** `tickets:read`. **Response:** 200. No body.

| Query parameter | Type | Meaning |
| --- | --- | --- |
| `q` | string, optional | Search title or ticket number, for example `TKT-42`; not description or comments. |
| `state` | string, repeatable | `new`, `in_progress`, `resolved`, `closed`, or `cancelled`. |
| `cursor` | string, optional | Opaque next-page token from the preceding response; send exactly one. |

Pages contain at most **10** tickets. `next_cursor` is a string when another page exists,
otherwise null. Keep the same filters and pass the returned token unchanged; do not decode
or construct cursors. There is no page-size parameter.

For **user-role service users**, state values are validated but the requester's section rules
choose the listed states instead of the supplied filter: this endpoint lists their `new` and
`in_progress` tickets (including held tickets). It does not expose a section selector.
Detail access to an owned ticket is not limited to those listed states.

```json
{
  "items": [{
    "id":42,"number":42,"title":"Network unavailable","state":"new","priority":"medium",
    "category":{"id":3,"name":"Network"},"assignee":null,
    "requester":{"id":12,"name":"Monitoring"},
    "created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z"
  }],
  "next_cursor": null
}
```

This is the **ticket summary** shape, also returned by creation. `id` and `number` are
integers; `title`, `state`, and `priority` are strings. References contain integer `id`
and string `name`; `assignee` and `requester` may be null. Timestamps are UTC RFC 3339 strings.
Invalid cursor: 400; unknown state: 422 `validation`; missing scope: 403.

## GET /api/v1/tickets/{id}

**Scope:** `tickets:read`. `id` is a positive integer ticket ID, not the display number.
No body or query parameters. **Response:** 200, a ticket summary extended with string
`description`, arrays `comments` and `timeline`, and nullable object `workflow`.

```json
{
  "id":42,"number":42,"title":"Network unavailable","state":"new","priority":"medium",
  "category":{"id":3,"name":"Network"},"assignee":null,
  "requester":{"id":12,"name":"Monitoring"},
  "created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z",
  "description":"The office network is unavailable.",
  "comments":[{"id":7,"author":{"id":12,"name":"Monitoring"},"visibility":"public","body":"Only the second floor is affected.","created_at":"2026-10-23T09:01:00Z"}],
  "timeline":[{"action":"created","field":null,"from":null,"to":null,"actor":{"id":12,"name":"Monitoring"},"at":"2026-10-23T09:00:00Z"}],
  "workflow":{"current_position":1,"pending_step":{"position":1,"kind":"form","fields":[{"name":"location","label":"Location","type":"short_text","required":true,"options":[]}]}}
}
```

Comments have integer `id`, author `{id, name}` (historical `id` may be null), string
`visibility` (`public` or `internal`), string `body`, and UTC RFC 3339 `created_at`.
Timeline entries contain string `action`, nullable strings `field`, `from`, `to`, nullable
`actor` (same historical identity shape), and UTC RFC 3339 `at`. Comments are separate from
the audit timeline; automatic events may have null actors. This projection does not include
an audit key ID.

`workflow` is null without a workflow execution. Otherwise `current_position` is one-based;
`pending_step` is null unless the caller can act on the current active step. A pending step
has integer `position`, string `kind` (`form` or `manual_task`), and `fields` in the category
field shape. Queue-wide access does not make the caller the requester or assignee.
Missing scope: 403; missing, malformed, or out-of-scope ID: 404.

## POST /api/v1/tickets

**Scope:** `tickets:write`. Create through the category's workflow. The service user is
always the requester; caller-supplied requester or assignee fields are not supported.

| Body field | Type | Meaning |
| --- | --- | --- |
| `category_id` | integer | Existing category with a published runnable workflow. |
| `title` | string | Required, nonblank; trimmed. |
| `description` | string, optional | Description; defaults to empty. |
| `priority` | string | Required: `low`, `medium`, `high`, or `critical`. |

```json
{"category_id":3,"title":"Network unavailable","description":"The office network is unavailable.","priority":"medium"}
```

**Response:** 201 with `Location: /api/v1/tickets/42` and a ticket summary, for example:

```json
{"id":42,"number":42,"title":"Network unavailable","state":"new","priority":"medium","category":{"id":3,"name":"Network"},"assignee":null,"requester":{"id":12,"name":"Monitoring"},"created_at":"2026-10-23T09:00:00Z","updated_at":"2026-10-23T09:00:00Z"}
```

State and assignee depend on the workflow's initial automatic steps. Creation does not take
form answers: read the detail and complete its pending requester form separately.
An agent service user **without `tickets:all` can create but cannot read back the ticket**;
201 is a creation receipt, not a grant of read access.
Missing category: 404; blank title or unavailable published workflow: 422 `validation`;
invalid priority: 422 `rejected`; a workflow whose first automatic step assigns to a desk with no
members by least load: 409 `conflict`, nothing created.

`GET /api/v1/categories` lists only categories that can run now, but creation does not check that
list. A category whose first desk step is a claim on a desk with no members still accepts the
ticket, exactly as the browser form does, and the ticket waits until the desk has someone to
claim it. List the categories first to create only in runnable ones.

## POST /api/v1/tickets/{id}/workflow/steps/{position}/complete

**Scope:** `tickets:write`. `id` is a positive integer; path `position` is the **one-based**
current workflow step. Use `workflow.pending_step` from the detail, not a guessed position.

| Body field | Type | Meaning |
| --- | --- | --- |
| `answers` | array, optional | Objects with integer `position` and string-array `values`. Answer positions are **zero-based field indexes**, not field names or step positions. |
| `solution` | string or null, optional | Manual-task solution only; trimmed, at most 2,000 bytes. A nonempty solution on another step type is rejected. |

```json
{"answers":[{"position":0,"values":["Second floor"]}]}
```

Duplicate, unknown, or ambiguous answer positions (more than one value) are rejected.
For text fields, send one string; for a selection, send an exact `options` value. For a
checkbox, send `"on"` or `"true"` to check it; omit it or send an empty value to leave it
unchecked. A required checkbox must be checked. The workflow validates required fields
and their types. Only the persisted requester can
complete a requester form; assignee actions require the persisted assignee. Service users
cannot claim tickets, and `tickets:all` does not bypass these identity checks.
**Response:** 200, the full ticket detail shape shown above, reflecting the advanced workflow
and updated timeline; no additional `tickets:read` scope is required for this response.
Missing/out-of-scope ticket: 404; wrong actor: 403; invalid answers: 422 `validation`;
stale, invalid, or inactive step position: 422 `rejected` (not 409).

## POST /api/v1/tickets/{id}/transition

**Scope:** `tickets:write`, with an agent role and ticket access. Positive integer `id`.
Body: string `state` (target state) and optional string `reason`.

```json
{"state":"in_progress","reason":"Investigating the network outage"}
```

The same state machine as the UI applies. Reopening `closed` to `in_progress` requires a
nonblank reason. A requester-owned `resolved` ticket cannot be manually transitioned to
`closed`: its requester must use `/confirm`.
**Response:** 200, full ticket detail as above with the new state and timeline; no additional
`tickets:read` scope required. User-role caller or forbidden closure: 403; out-of-scope ID:
404; invalid transition or missing reopen reason: 422 `rejected`.

## POST /api/v1/tickets/{id}/comments

**Scope:** `comments:write` for public comments, `comments:internal` for internal comments.
Positive integer `id`. Body: required nonblank string `body`, optional string `visibility`
(`public`, the default, or `internal`).

```json
{"body":"Only the second floor is affected.","visibility":"public"}
```

**Response:** 201, the new comment (not the ticket detail):

```json
{"id":7,"author":{"id":12,"name":"Monitoring"},"visibility":"public","body":"Only the second floor is affected.","created_at":"2026-10-23T09:01:00Z"}
```

The body is trimmed. Internal comments require an agent. Closed/cancelled tickets reject
comments; a resolved ticket permits only its requester to comment while awaiting confirmation.
A public requester reply resumes a held ticket. Missing selected scope or forbidden comment:
403; out-of-scope ID: 404; blank body or invalid visibility: 422 `validation`.

## POST /api/v1/tickets/{id}/confirm

**Scope:** `tickets:write`. Positive integer `id`. Only the ticket's requester may confirm
its resolution. Send an empty JSON object:

```json
{}
```

**Response:** 200, full ticket detail as above, now `state: "closed"` with an updated timeline.
No additional `tickets:read` scope is required. Readable ticket but wrong requester: 403;
out-of-scope ID: 404; invalid state transition: 422 `rejected`.

## POST /api/v1/tickets/{id}/reject

**Scope:** `tickets:write`. Positive integer `id`. Only the ticket's requester may reject
its resolution. Send an empty JSON object:

```json
{}
```

**Response:** 200, full ticket detail as above, now `state: "in_progress"`. Rejection detaches
its workflow, so `workflow` is null and the ticket continues manually. No additional
`tickets:read` scope is required. Readable ticket but wrong requester: 403; out-of-scope ID:
404; invalid state transition: 422 `rejected`.

## Audit

Actions through the API are attributed to the service user. Audit events record the API
key ID (`api_key_id`) as well as the actor. Ordinary comments create **no audit event**:
their provenance is their author, the service user. If a comment produces an audit event,
for example by resuming a hold, that event records the key ID. The detail timeline is a
limited projection, not a dump of every stored audit field.

## Curl walkthrough

Use a user-role service key with `catalog:read`, `tickets:read`, `tickets:write`, and
`comments:write`. Set `TKT_TOKEN` to the issued token in the local environment; do not
commit it. Replace the URL and example IDs with those returned by the instance.

```sh
TKT_URL='https://tkt.example.com'

curl -sS "$TKT_URL/api/v1/me" -H "Authorization: Bearer $TKT_TOKEN"
curl -sS "$TKT_URL/api/v1/categories" -H "Authorization: Bearer $TKT_TOKEN"

curl -sS -i "$TKT_URL/api/v1/tickets" \
  -H "Authorization: Bearer $TKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"category_id":3,"title":"Network unavailable","description":"The office network is unavailable.","priority":"medium"}'

curl -sS "$TKT_URL/api/v1/tickets/42/comments" \
  -H "Authorization: Bearer $TKT_TOKEN" -H 'Content-Type: application/json' \
  -d '{"body":"Only the second floor is affected.","visibility":"public"}'
```

If the category asks for requester information, read the new ticket and complete its pending
form with the workflow endpoint before waiting for the team to act.
