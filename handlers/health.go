package handlers

import "net/http"

// Healthz is a liveness probe: it returns 200 as long as the HTTP server is
// running and able to serve requests. It performs no I/O and has no external
// dependencies, so it stays cheap and never restarts the pod for a slow
// upstream (gitstats talks to the GitHub API only per user request).
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readyz is a readiness probe. gitstats has no hard startup dependency — it
// serves static pages and fetches GitHub data lazily per request — so being
// ready is equivalent to the HTTP server accepting connections.
func Readyz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}
