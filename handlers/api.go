package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	cu "github.com/keploy/gitstats/common"
)

func HandleRepoStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	repoURL := r.URL.Query().Get("repo")
	if repoURL == "" {
		http.Error(w, "Repository URL is required", http.StatusBadRequest)
		return
	}

	owner, repo, err := extractRepoInfo(repoURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid repository URL: %v", err), http.StatusBadRequest)
		return
	}

	// Token from the Authorization header, falling back to the GITHUB_TOKEN
	// env var, raises the GitHub rate limit from 60 to 5000 requests/hour.
	config := configFromRequest(r)

	releases, err := getAllReleases(r.Context(), owner, repo, config)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	stats := calculateDownloadStats(releases)
	stats.RepoName = fmt.Sprintf("%s/%s", owner, repo)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
func HandleStarHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get repositories from query parameter
	repos := r.URL.Query()["repo"]
	if len(repos) == 0 {
		http.Error(w, "At least one repository URL is required", http.StatusBadRequest)
		return
	}

	config := configFromRequest(r)

	// Fetch star history for all repositories
	result := cu.MultiRepoStarHistory{
		Repositories: make([]cu.StarHistory, 0, len(repos)),
	}

	for _, repoURL := range repos {
		owner, repo, err := extractRepoInfo(repoURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid repository URL: %v", err), http.StatusBadRequest)
			return
		}

		history, err := getStarHistory(r.Context(), owner, repo, config)
		if err != nil {
			writeAPIError(w, err)
			return
		}

		result.Repositories = append(result.Repositories, *history)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func HandleOrgContributors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	org := r.URL.Query().Get("org")
	if org == "" {
		http.Error(w, "Organization name is required", http.StatusBadRequest)
		return
	}

	config := configFromRequest(r)

	stats, err := getOrgContributors(r.Context(), org, config)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func HandleActiveContributors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	repoURL := r.URL.Query().Get("repo")
	orgName := r.URL.Query().Get("org")

	if orgName == "" && repoURL == "" {
		http.Error(w, "Either organization name or repository URL is required", http.StatusBadRequest)
		return
	}

	config := configFromRequest(r)

	// If repoURL is provided, handle single repository
	if repoURL != "" {
		owner, repo, err := extractRepoInfo(repoURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid repository URL: %v", err), http.StatusBadRequest)
			return
		}
		handleSingleRepo(r.Context(), w, owner, repo, config)
		return
	}

	// Handle organization-wide contributors
	handleOrganization(r.Context(), w, orgName, config)
}

func HandleStargazers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get query parameters
	owner := r.URL.Query().Get("owner")
	repo := r.URL.Query().Get("repo")

	if owner == "" || repo == "" {
		http.Error(w, "Both owner and repository name are required", http.StatusBadRequest)
		return
	}

	// Parse page number
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}

	// Token from the Authorization header, falling back to the GITHUB_TOKEN
	// env var, raises the GitHub rate limit from 60 to 5000 requests/hour.
	token := resolveToken(r)

	// Fetch stargazers
	stargazers, hasMore, total, err := fetchStargazers(r.Context(), owner, repo, token, page)
	if err != nil {
		// Rate-limit exhaustion becomes 429 + Retry-After with a clear
		// message; anything else stays a 500.
		writeAPIError(w, err)
		return
	}

	// Prepare response data
	response := struct {
		Stargazers  []cu.Stargazer `json:"stargazers"`
		HasMore     bool           `json:"has_more"`
		TotalCount  int            `json:"total_count"`
		CurrentPage int            `json:"current_page"`
		NextPage    int            `json:"nextzz_page,omitempty"`
	}{
		Stargazers:  stargazers,
		HasMore:     hasMore,
		TotalCount:  total,
		CurrentPage: page,
	}

	if hasMore {
		response.NextPage = page + 1
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	// Encode and send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}
}
