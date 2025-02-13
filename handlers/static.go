package handlers

import "net/http"

func ServerIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.ServeFile(w, r, "./web/index.html")
		return
	}
	http.NotFound(w, r)
}
func ServerOrgPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/orgs" {
		http.ServeFile(w, r, "./web/org.html")
		return
	}
}
func ServerStartPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/starhistory" {
		http.ServeFile(w, r, "./web/stars.html")
		return
	}
}
func ServerParticipantPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/participants" {
		http.ServeFile(w, r, "./web/participants.html")
		return
	}
}

func ServerStargazersPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/stargazers" {
		http.ServeFile(w, r, "./web/stargazers.html")
		return
	}
	http.NotFound(w, r)
}
