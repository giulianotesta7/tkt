package httpadapter

import (
	"net/http"
	"net/url"
	"strings"
)

// Return URL after sign-in (issue #493, S5): the OAuth consent page needs the
// person signed in, so the session gate sends an anonymous visitor there to
// /login with the request it made, and both sign-in paths land them back on it.
// The value arrives from the browser, so it is an open-redirect vector: only a
// local absolute path is ever followed, and everything else falls back to
// /tickets.

// returnPathParam is the query and form field that carries the return path.
const returnPathParam = "next"

// defaultLanding is where every sign-in lands without a usable return path.
const defaultLanding = "/tickets"

// authorizeReturnPath is the one path the session gate offers a return to. No
// other page gains return-to behavior.
const authorizeReturnPath = "/oauth/authorize"

// safeReturnPath answers raw when it is a local absolute path, query string
// allowed. It refuses a scheme-relative "//host" and "/\host" (browsers read
// a backslash as a slash), any backslash at all, any ASCII control character
// (header splitting, and browsers strip tabs and newlines before parsing), and
// anything url.Parse reads as carrying a scheme or a host.
func safeReturnPath(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsRune(raw, '\\') {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", false
	}
	return raw, true
}

// landingFor answers where a successful sign-in goes: the return path when it
// is safe, otherwise /tickets.
func landingFor(next string) string {
	if path, ok := safeReturnPath(next); ok {
		return path
	}
	return defaultLanding
}

// authorizeRequest reports whether r is a request the session gate refuses
// with a return path: a GET to the OAuth consent page.
func authorizeRequest(r *http.Request) bool {
	path := r.URL.Path
	return r.Method == http.MethodGet && (path == authorizeReturnPath || strings.HasPrefix(path, authorizeReturnPath+"/"))
}

// loginWithReturn answers the /login target that carries r's request URI.
func loginWithReturn(r *http.Request) string {
	return "/login?" + returnPathParam + "=" + url.QueryEscape(r.URL.RequestURI())
}
