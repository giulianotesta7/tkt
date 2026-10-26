// Package templates is the go:embed bridge for the HTTP adapter (design
// "Package Layout"): //go:embed cannot cross package dirs, so this tiny
// package owns the embedded template FS and the http adapter imports it.
//
// The embed patterns grow with the template tree: the shell roots
// (base.html, auth.html) and the partials set exist from the first render
// task; the pages directory joins when the first page lands.
package templates

import "embed"

// FS is the embedded template tree: shell roots, full pages (pages/*.html),
// swap fragments (partials/*.html) and the vendored htmx script
// (static/htmx.min.js, BSD-2-Clause, htmx.org v2.0.4, and tickets.js).
// static/ticket_form.js is the create-form draft (issue #262),
// static/notifications.js is the notification drawer's focus handling (issue
// #344), and static/ticket_detail.js is the ticket detail's own behaviour
// (issue #386): the title guard, the property pickers, the hold and reason
// dialogs, and the phone fold of the properties panel. static/saved_replies.js
// is the saved-reply manager's behaviour (issue #418): the placeholder chips in
// the editor and the delete confirmation.
//
// The interface typeface is vendored rather than fetched: Inter Variable
// (static/InterVariable.woff2, SIL Open Font License 1.1, rsms/inter v4.1,
// license text beside the font as static/InterVariable-LICENSE.txt). Local
// hosting keeps the font working on installations with no outbound network and
// gives every operator the same rendering instead of the system fallback.
//
//go:embed base.html auth.html pages/*.html partials/*.html static/htmx.min.js static/InterVariable.woff2 static/users.css static/users.js static/tickets.js static/ticket_form.js static/workflow.js static/sla_duration.js static/categories.js static/save-feedback.js static/notifications.js static/shell.js static/queue.js static/ticket_metrics.css static/ticket_metrics.js static/sla_countdown.js static/ticket_detail.js static/saved_replies.js static/ticket_create.js static/settings.css static/settings.js
var FS embed.FS
