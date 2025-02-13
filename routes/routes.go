package routes

import (
	"net/http"

	handler "github.com/sonichigo/gitstats/handlers"
)

func SetupRoutes() {
	http.Handle("/images/", http.StripPrefix("/images", http.FileServer(http.Dir("./images"))))

	// Serve static files
	http.HandleFunc("/", handler.ServerIndex)
	http.HandleFunc("/orgs", handler.ServerOrgPage)
	http.HandleFunc("/starhistory", handler.ServerStartPage)
	http.HandleFunc("/participants", handler.ServerParticipantPage)
	http.HandleFunc("/stargazers", handler.ServerStargazersPage)

	// API endpoint
	http.HandleFunc("/repo-stats", handler.HandleRepoStats)
	http.HandleFunc("/org-contributors", handler.HandleOrgContributors)
	http.HandleFunc("/star-history", handler.HandleStarHistory)
	http.HandleFunc("/active-contributors", handler.HandleActiveContributors)
	http.HandleFunc("/github-stargazers", handler.HandleStargazers)

}
