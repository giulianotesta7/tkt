# Administration

**English** | [Español](../docs-es/administration.md)

How an instance is run: roles, the catalog, workflows, SLAs, ticket creation, email,
security, API access, and MCP clients. First account: [Getting started](getting-started.md). Container and configuration:
[Deployment](deployment.md).

## Roles

Each role includes everything below it.

| Role | Can do |
| --- | --- |
| **User** | Create tickets, reply publicly on their own tickets, answer the resolution confirmation. |
| **Agent** | Everything a user can do, plus work on tickets assigned to them or waiting on their desks: edit, change state, assign, write public replies and internal notes. |
| **Admin** | Everything an agent can do, plus manage people (except admin and root accounts), desks, categories, workflows, metrics, service users and API keys, and every setting except Instance, email and the API and MCP switches. |
| **Root** | Everything, plus grant and remove the **Admin** role, manage Instance and email settings, and turn the API and MCP clients on or off. |

- Claimable tickets come from the desks a person belongs to, so add each agent to the desk
  that owns the category. Assigned tickets appear either way.
- The root account is protected: nobody else can edit, deactivate, or manage it, and root
  changes its own password in **Preferences**.
- Only root grants or removes **Admin**. An administrator cannot create, manage, or touch
  another administrator or the root account.
- Anyone who manages people can set a new password for an account below their level, within
  the instance minimum.

## People

1. Open **Users**. Filter by **Active** or **Deactivated**, or search by name or email.
2. **New user** asks for a name, an email address, and a password; the account starts with the
   **User** role.
3. **Deactivate user** keeps the account and its history, blocks sign-in, and ends active
   sessions on their next request. **Reactivate user** reverses it.

There are no invitations and no self-registration: the first account is created at `/setup`
and becomes root, and everyone else is created here.

## Desks and the catalog

The catalog is a tree of folders and categories, and desks are the teams that handle them:

```text
Folder  →  Folder  →  …  →  Category  ⇢  Workflow  ⇢  Desk
```

- A **folder** groups categories and other folders. Folders nest to any depth, and a category
  sits in one folder or at the top level.
- A **category** groups requests that follow the same workflow. Its workflow's **Send to a
  desk** steps decide which **desk** handles its tickets; the category itself names no desk.
- A **desk** is a team: it holds members and handles the tickets workflows send to it.

Every installation starts with a **General** desk. Folder names are unique among the folders
that share a parent, ignoring case.

**Categories** shows the whole catalog as one tree and edits it in place, without leaving it. Right-click a folder, a category or empty tree space (or press the menu key or Shift+F10 on a row, or on the tree for the top level) for the same actions:

| To | Do this |
| --- | --- |
| Create | Use **New folder** or **New category** in the heading: they create where you have selected (inside the selected folder, or next to the selected category), or at the top level when nothing is selected. The popover shows **Create in: <folder>** with a ✕ (**Create at the top level instead**) to switch to the top level, and the tree expands to the new item. A folder's **+** creates inside that folder. Each opens a popover anchored to its button without moving rows. Right-click a folder or empty tree space and choose either action to open a popover anchored where you pointed. The heading buttons fix the kind; a folder's **+** (and the right-click menu) also offers a **Folder** / **Category** switch that keeps the name. Enter saves and readies another; Esc closes the popover from any control inside it. |
| Rename | Edit the name in properties and save, or double-click a folder's name for inline editing. Enter saves the inline edit, Esc cancels. |
| Move | Use **Move to…** in a row or context menu to search destinations, including the top level. Drag a row by its name (or by its handle on touch) onto a folder, or onto blank space in the catalog tree to move it to the top level. Use **Cut** in a row or context menu, then **Paste here** in a folder or blank-space context menu; alternatively use Ctrl+X (Cmd+X on macOS) on a row and Ctrl+V on a folder or the focused tree. A floating message at the bottom shows what is cut; Esc or its **Cancel** cancels. **Move…** is available in the row menu only without scripts. Every move offers **Undo** in a message that stays while you point at or focus it; Ctrl+Z (Cmd+Z on macOS) on the tree does the same while it is visible. |
| Edit details | On a wide screen, select a folder or category to edit its properties in the permanent right inspector. Enter focuses its fields; the row has no pen here. **Save changes** keeps the inspector open; **Discard** restores the saved values. On a narrow screen, Enter or the row's pen opens an anchored popover above or below the row. On either screen, **Properties** in the row's ⋯ menu opens that row's editor; a category name remains a link to its workflow. Both views edit name and description, plus category suggestion keywords, and link to workflow and SLA configuration. Changing selection asks before discarding a draft; resizing preserves that draft. |

Cut and Paste shortcuts are shown in the ⋯ menu (Ctrl+X and Ctrl+V; Cmd on macOS). Arrow keys navigate the tree. Text fields retain normal cut and paste.

Tree rows omit ticket counts and workflow author names. Folder rows have no count or empty-state sub-line; deep rows keep their path.

Folders and categories stay in alphabetical order, so moving means changing the folder, not the
position. A folder can't move into itself or one of its own folders, and can only be deleted
when it is empty. A refused change keeps what you typed and says why. Deleting asks for
confirmation and cannot be undone. **Needs attention** lists categories with no published
workflow and categories whose published workflow can't run.

**Desks** has its own screen: it lists every desk with its agents, the categories whose
published workflow sends tickets to it and its open tickets, and its drawer creates, renames and
staffs a desk. Desk members need the agent role or higher. A desk still referenced by a
workflow or assignment history cannot be deleted.

Requesters only see folders that lead to a published category. A folder that holds a single
folder and nothing else is skipped, and the full path is shown instead.

Upgrading from a release with departments keeps every category where it was: each department
becomes a folder and each desk a folder inside it, with the category in that folder. Folders
copied from the starting **General** department and desk are removed when nothing was put in
them.

A category carries **Keywords for suggestions** for the local engine and **SLA** targets from
the instance or its own. Its workflow must be published before it accepts tickets.

## Workflows

1. Open a category's **Configure workflow** and add its steps.
2. **Save** keeps the draft; **Publish** makes it the version new tickets run on.
3. Create a ticket in the category to confirm it runs the published step.

- A draft never changes existing tickets: each ticket keeps the workflow version it was
  created with.
- Publishing validates the definition: at least one step, unique field keys, and at most one
  ending, which must be the last step.
- Publishing refuses a workflow that can never move a ticket, such as least loaded assignment
  to a desk with no available agents. A workflow that only needs a person to assign the ticket
  raises a warning instead.
- **Reuse a workflow** copies a published workflow from another category into the draft without
  publishing it.

| Step | What it does |
| --- | --- |
| **Send to a desk** | Routes the ticket to a desk. With **Claim**, desk members take it themselves. With **Least loaded**, tkt assigns it to the active member with the fewest open tickets. |
| **Ask for information** | Asks the requester or the assignee for short text, long text, a checkbox, or one choice from a list, each optionally required. |
| **Give the agent a task** | Gives the assignee instructions to carry out and mark done. |
| **Mark the ticket resolved** | Ends the workflow by resolving it. Runs automatically. |
| **Close the ticket** | Ends the workflow by closing it. Runs automatically. |

## Service level agreements

SLA is off until it is enabled in **Settings → Tickets & SLA**, which also holds the instance
defaults.

| Setting | Default | Effect |
| --- | --- | --- |
| **First response** and **Resolution** targets | one per priority, low to critical | Frozen onto each new ticket. |
| **Warning threshold** | 80% | Share of a target elapsed before the ticket is marked at risk. |
| **Automatic close of held tickets** | off | From two working days on, tkt warns the requester one working day before closing a ticket that waits on them, then closes it if they never reply. A one-day window closes without a reminder. |
| **Working calendar** | Monday to Friday, 09:00 to 18:00, UTC | Timezone, working days, and daily window of the clock. |

- Instance defaults never rewrite existing categories: each keeps its own targets or resets
  them to the defaults.
- Edits apply to new tickets only, and a ticket created while SLA was off has no commitment.
- A hold pauses the resolution clock only; the first-response clock keeps running.

## Ticket creation and category suggestions

**Settings → Tickets & SLA** also decides how a requester picks a category: **Single page**
(the default) shows the form with the picker on it, and **Two steps** shows the catalog first,
then the form with the category fixed.

| Engine | Behavior |
| --- | --- |
| **Off** (the default) | No suggestions; the requester uses the catalog. |
| **On this server** | Matches the text against category names, descriptions, and **Keywords for suggestions**, plus the names of the folders on its path. Desk names are not a suggestion source. Nothing leaves tkt. |
| **Decision model** | Sends the text and the category list to an external endpoint that implements `POST /decisions`. |

Suggestions are advisory: up to three, chosen by the requester, never applied automatically.
Without a confident match the picker stays the catalog.

The decision model receives **the text the requester wrote about the problem (title and
description, up to 4,000 characters) and the name and description of each offered category**.
It never receives the requester's name or email, attachments, or ticket history. Responses are
bounded to two seconds; if the model fails, refuses, or answers below its confidence threshold,
that suggestion comes from the local engine.

The endpoint URL, model name, and API key are stored encrypted like the SMTP password. **Try
it** previews a sample text without saving anything.

A benchmark on 23 October 2026 used 52 categories and 226 problem descriptions in Spanish and
English. Accuracy below is the share of the 206 descriptions with an expected category where
that category was suggested first; latency covers the whole suggestion request.

| Engine | First-choice accuracy | Median latency |
| --- | --- | --- |
| **On this server**, with keywords | 48% | 17 ms |
| **On this server**, without keywords | 14% | 9 ms |
| **Decision model** (`gpt-6-luna`) | 95% | 326 ms |

The model pass made 219 requests, covering all 206 categorized cases and 13 of 20 no-match
cases, before stopping after 10 consecutive local fallbacks. Results include those fallbacks
and predate the current no-match handling, so this is not a standalone model score.

At [OpenAI's published Decisions rate](https://developers.openai.com/api/docs/guides/decisions)
of $0.10 per million uncached input tokens and no output charge, the estimated 298,634 input
tokens (characters divided by four) would cost about **$0.03 for the run**, or **$0.14 per 1,000
similar requests**. This is not a billed total; cache writes and regional premiums are excluded.

## Instance

Only root can open **Settings → Instance**:

- **TKT address** is the one public HTTP or HTTPS address used by notification links,
  password recovery and company sign-in callbacks. Deployment paths are supported, such as
  `https://tkt.example.com/help`; do not paste a provider callback URL. Address changes are
  audited. Turn off email and company providers before clearing it.
- **Interface language** selects the default English or Spanish interface. Existing language
  settings are retained. Personal choices in **Preferences** take precedence and are unchanged.
  The old Language page redirects here; its old save endpoint no longer accepts changes.
- **About** shows the running tkt release version, or its source revision when available.
  An unversioned source archive reports `dev`. This value is read-only, not a database setting.

During upgrade, the existing email address wins if both stored addresses differ. A prominent
notice preserves the old company sign-in address so root can check provider registrations.
If only one address was set, it is retained; equal addresses are retained as well.

### Company sign-in

Configure Google Workspace and Microsoft Entra in **Settings → Security**. Administrators
can manage providers, but the public address is read-only there and only root changes it in
Instance. Copy each displayed callback URL into its provider console. Changing the Instance
address changes those callbacks: update both registrations before relying on company sign-in.

MCP is not included in this base yet (#493). Its follow-up integration will read the same
Instance address; it does not introduce another address field or move API switches.

## Email notifications

Only root configures email, in **Settings → Email notifications**: server, port (25 when
empty), encryption, credentials, and sender. Without it the notification bell still
works; email adds a copy of the same notifications.

| Field | Default | Effect |
| --- | --- | --- |
| **Encryption** | none | STARTTLS or TLS is required as soon as a username and password are set, because credentials are never sent unencrypted. |
| **Username** and **Password** | empty | Optional for a server that accepts mail without authentication. A saved password is never shown again; leave the field empty to keep it. |
| **Do not verify the certificate** | off | Keeps the connection encrypted but stops tkt from checking who is on the other end. |
| **Sender address** and **Sender name** | empty | The sender recipients see; the name is optional. |
| **TKT address** | empty | Read-only here; root configures it in **Settings → Instance** before enabling email. See [Deployment](deployment.md#the-public-base-url). |

**Send test email** sends one message to the signed-in root account with the saved
configuration. A background worker delivers notifications every 15 seconds.

Each person can opt out in **Preferences**. The SMTP password is sealed with the key file that
[Backup and upgrade](backup-and-upgrade.md#what-to-back-up) covers.

## Security

**Settings → Security** holds the password policy.

| Setting | Default | Effect |
| --- | --- | --- |
| **Minimum password length** | 12, between 8 and 64 | New passwords only. |
| **Allow people to change their own password from Preferences** | on | Off hides the password section for everyone except the paths an administrator uses. |
| **Offer “Forgot your password?” on the sign-in page** | on | Adds the link to the sign-in page. It works once, expires after one hour, stores the new password, and signs the account out everywhere. One request per account every five minutes. |

Recovery answers the same neutral message whether or not the address belongs to an active
account.

> [!NOTE]
> Password recovery needs both this setting and working email notifications.

**Known limitation:** the throttle and the single-use link are enforced per request, not as one
atomic step across two requests. Two recovery requests sent for the same account at almost the
same moment can both send an email, and the link from the newer request invalidates the earlier
one. Only the latest link works.

## API access

**Settings → API** manages the identities and keys that other systems use. See the
[API v1 reference](api.md) for authentication, endpoints, and examples.

- **The API switch** is off by default. Only root turns it on or off; while it is off, every
  key is refused. Administrators see its state.
- **Service users** are the identities a key acts as, so every change made through the API is
  recorded under a named service user and never under a person. A service user has the
  **User** or **Agent** role only. It never signs in, receives no notifications, cannot be
  mentioned, and does not appear on the **Users** screen or in any assignee or desk picker.
- **Keys** belong to a service user, which can hold several so a key can be rotated without a
  gap. Each key has a name, one or more scopes, and an optional expiry date (it stops working
  at the start of that day, UTC). The token is shown once, right after it is created, and tkt
  keeps only a hash of it. Copy it then; a lost token is replaced by a new key.

| Scope | Allows |
| --- | --- |
| `catalog:read` | Read categories and their request forms. |
| `tickets:read` | Read and search the tickets the service user can see. |
| `tickets:write` | Create and update tickets. |
| `comments:write` | Write public comments. |
| `comments:internal` | Write internal comments. Agent service users only. |
| `tickets:all` | Access the whole ticket queue for reads and writes. Agent service users only; the matching endpoint scopes are still required. |

> [!WARNING]
> A leaked key with `tickets:all` and matching write scopes can change every ticket.
> Grant queue-wide access only when the integration needs it.

A key can never do more than its service user's role allows: a **User** service user sees and
creates only its own tickets, whatever scopes its key carries.

**Revoke** stops one key at once. **Deactivate** a service user to revoke every key it holds;
reactivating it does not bring those keys back. Creating and deactivating service users,
creating and revoking keys, and turning the API on or off are recorded with the person who did
it.

## MCP clients

AI assistants such as Claude, Claude Code, VS Code, and Cursor connect at `<public URL>/mcp` and
act as the person who approves them. The [MCP server](mcp.md) guide covers connecting clients,
the tools, and errors.

- **The MCP switch** is in **Settings → API**, off by default and separate from the API switch.
  Only root turns it on or off; administrators see its state. Turning it on or off is recorded
  with the person who did it.
- **The public URL** is required first: the **TKT address** in **Settings → Email
  notifications**. Changing it later disconnects every client.
- **While it is off**, `/mcp`, `/oauth/*`, and `/.well-known/oauth-*` answer 404 and existing
  tokens stop working. Approvals are kept and work again when it is turned back on.
- **A reverse proxy** must forward those paths to tkt; see
  [Enable MCP](mcp.md#enable-mcp).
- **Connections belong to people.** Each person reviews and revokes their own in
  **Preferences → Connected apps**; there is no view of everyone's connections.
  **Deactivate** a person to revoke every connection they hold.
- **Access is role ∩ scopes.** A connection can never do more than its person's role, never
  gets queue-wide `tickets:all`, and reaches nothing administrative.

## Next steps

- [Back up the database and the key file](backup-and-upgrade.md)
- [Follow the requester and agent journeys](user-guide.md)
