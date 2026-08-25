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
// (static/htmx.min.js, BSD-2-Clause, htmx.org v2.0.4).
//
//go:embed base.html auth.html pages/*.html partials/*.html static/htmx.min.js static/users.css static/users.js static/workflow.js
var FS embed.FS
