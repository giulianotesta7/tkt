# Administration

**English** | [Español](../docs-es/administration.md)

How an instance is run: roles, the catalog, workflows, SLAs, ticket creation, email, and
security. First account: [Getting started](getting-started.md). Container and configuration:
[Deployment](deployment.md).

## Roles

Each role includes everything below it.

| Role | Can do |
| --- | --- |
| **User** | Create tickets, reply publicly on their own tickets, answer the resolution confirmation. |
| **Agent** | Everything a user can do, plus work on tickets assigned to them or waiting on their desks: edit, change state, assign, write public replies and internal notes. |
| **Admin** | Everything an agent can do, plus manage people (except admin and root accounts), desks, categories, workflows, metrics, and every setting except email. |
| **Root** | Everything, plus grant and remove the **Admin** role and manage email settings. |

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

The catalog routes requests:

```text
Department  →  Desk  →  Category
```

- A **department** groups desks that support the same part of the organization.
- A **desk** groups the categories one team handles and holds that team's members.
- A **category** groups requests that follow the same workflow.

Every installation starts with a **General** department and a **General** desk. Names are
unique within their level, and a category always belongs to a desk.

**Catalog** manages the tree in place, and **Needs attention** lists desks with no agents and
categories with no published workflow. Desk members need the agent role or higher. Deleting
any node asks for confirmation and cannot be undone.

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
| **On this server** | Matches the text against category names, descriptions, and **Keywords for suggestions**, plus the names of the desk and department that hold each category. Nothing leaves tkt. |
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
| **TKT address** | empty | Public address of the instance. See [Deployment](deployment.md#the-public-base-url). |

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

## Next steps

- [Back up the database and the key file](backup-and-upgrade.md)
- [Follow the requester and agent journeys](user-guide.md)
