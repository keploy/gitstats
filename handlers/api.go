package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	cu "github.com/sonichigo/gitstats/common"
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

	// Make token optional
	var config *cu.Config
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		config = &cu.Config{GithubToken: token}
	}

	releases, err := getAllReleases(owner, repo, config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	// Make token optional
	var config *cu.Config
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		config = &cu.Config{GithubToken: token}
	}

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

		history, err := getStarHistory(owner, repo, config)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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

	// Make token optional
	var config *cu.Config
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		config = &cu.Config{GithubToken: token}
	}

	stats, err := getOrgContributors(org, config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	var config *cu.Config

	// If repoURL is provided, handle single repository
	if repoURL != "" {
		owner, repo, err := extractRepoInfo(repoURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid repository URL: %v", err), http.StatusBadRequest)
			return
		}
		handleSingleRepo(w, owner, repo, config)
		return
	}

	// Handle organization-wide contributors
	handleOrganization(w, orgName, config)
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

	// Get token from Authorization header
	var token string
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token = strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
	}

	// Fetch stargazers
	stargazers, hasMore, total, err := fetchStargazers(owner, repo, token, page)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
