package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ReleaseAsset represents a single asset in a release
type ReleaseAsset struct {
	Name          string `json:"name"`
	DownloadCount int    `json:"download_count"`
}

// Release represents a GitHub release
type Release struct {
	ID        int            `json:"id"`
	TagName   string         `json:"tag_name"`
	CreatedAt time.Time      `json:"created_at"`
	Assets    []ReleaseAsset `json:"assets"`
}

// ReleaseDownloadStats represents download statistics for a single release
type ReleaseDownloadStats struct {
	TagName        string       `json:"tag_name"`
	CreatedAt      time.Time    `json:"created_at"`
	TotalDownloads int          `json:"total_downloads"`
	Assets         []AssetStats `json:"assets"`
}

// AssetStats represents download statistics for a single asset
type AssetStats struct {
	Name          string `json:"name"`
	DownloadCount int    `json:"download_count"`
}

// DownloadStats represents download statistics for all releases
type DownloadStats struct {
	RepoName       string                 `json:"repo_name"`
	TotalDownloads int                    `json:"total_downloads"`
	Releases       []ReleaseDownloadStats `json:"releases"`
}

type Config struct {
	GithubToken string
}

type Contributor struct {
	Login string `json:"login"`
}

type OrganizationStats struct {
	OrgName           string `json:"org_name"`
	TotalRepos        int    `json:"total_repos"`
	TotalContributors int    `json:"total_contributors"`
}

type StarHistory struct {
	RepoName string      `json:"repo_name"`
	History  []StarPoint `json:"history"`
}

// StarPoint represents stars at a specific point in time
type StarPoint struct {
	Date  time.Time `json:"date"`
	Stars int       `json:"stars"`
}

// MultiRepoStarHistory represents star history for multiple repositories
type MultiRepoStarHistory struct {
	Repositories []StarHistory `json:"repositories"`
}

func getStarHistory(owner, repo string, config *Config) (*StarHistory, error) {
	// GitHub's API doesn't provide direct star history, so we'll use stargazers endpoint
	page := 1
	perPage := 100
	history := make([]StarPoint, 0)

	for {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/stargazers?page=%d&per_page=%d",
			owner, repo, page, perPage)

		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}

		// Required header for timestamp information
		req.Header.Add("Accept", "application/vnd.github.v3.star+json")
		if config != nil && config.GithubToken != "" {
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", config.GithubToken))
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error making request: %v", err)
		}

		if resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			return nil, fmt.Errorf("rate limit exceeded. Please use a GitHub token")
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API returned status: %d, body: %s", resp.StatusCode, string(body))
		}

		// Parse response
		var stargazers []struct {
			StarredAt time.Time `json:"starred_at"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&stargazers); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error decoding response: %v", err)
		}
		resp.Body.Close()

		if len(stargazers) == 0 {
			break
		}

		// Accumulate star counts
		starCount := page * perPage
		for i, sg := range stargazers {
			history = append(history, StarPoint{
				Date:  sg.StarredAt,
				Stars: starCount - (len(stargazers) - i - 1),
			})
		}

		if len(stargazers) < perPage {
			break
		}

		page++
	}

	// Sort history by date
	sort.Slice(history, func(i, j int) bool {
		return history[i].Date.Before(history[j].Date)
	})

	return &StarHistory{
		RepoName: fmt.Sprintf("%s/%s", owner, repo),
		History:  history,
	}, nil
}

func handleStarHistory(w http.ResponseWriter, r *http.Request) {
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
	var config *Config
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		config = &Config{GithubToken: token}
	}

	// Fetch star history for all repositories
	result := MultiRepoStarHistory{
		Repositories: make([]StarHistory, 0, len(repos)),
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

func getOrgContributors(org string, config *Config) (*OrganizationStats, error) {
	page := 1
	perPage := 100
	totalContributors := make(map[string]struct{})
	totalRepos := 0

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/%s/repos?page=%d&per_page=%d", org, page, perPage)

		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}

		req.Header.Add("Accept", "application/vnd.github.v3+json")
		if config != nil && config.GithubToken != "" {
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", config.GithubToken))
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error making request: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API returned status: %d, body: %s", resp.StatusCode, string(body))
		}

		var repos []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error decoding response: %v", err)
		}
		resp.Body.Close()

		if len(repos) == 0 {
			break
		}

		totalRepos += len(repos)

		for _, repo := range repos {
			repoName := repo["name"].(string)
			repoContributorsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contributors", org, repoName)

			contribReq, err := http.NewRequest("GET", repoContributorsURL, nil)
			if err != nil {
				return nil, fmt.Errorf("error creating contributors request: %v", err)
			}

			contribReq.Header.Add("Accept", "application/vnd.github.v3+json")
			if config != nil && config.GithubToken != "" {
				contribReq.Header.Add("Authorization", fmt.Sprintf("Bearer %s", config.GithubToken))
			}

			contribResp, err := client.Do(contribReq)
			if err != nil {
				return nil, fmt.Errorf("error making contributors request: %v", err)
			}

			if contribResp.StatusCode == http.StatusNoContent {
				// Repository has no contributors, continue to the next one
				contribResp.Body.Close()
				continue
			}

			if contribResp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(contribResp.Body)
				contribResp.Body.Close()
				return nil, fmt.Errorf("GitHub API returned status for contributors: %d, body: %s", contribResp.StatusCode, string(body))
			}

			var contributors []Contributor
			if err := json.NewDecoder(contribResp.Body).Decode(&contributors); err != nil {
				contribResp.Body.Close()
				return nil, fmt.Errorf("error decoding contributors response: %v", err)
			}
			contribResp.Body.Close()

			for _, contributor := range contributors {
				totalContributors[contributor.Login] = struct{}{}
			}
		}

		page++
	}

	return &OrganizationStats{
		OrgName:           org,
		TotalRepos:        totalRepos,
		TotalContributors: len(totalContributors),
	}, nil
}

func handleOrgContributors(w http.ResponseWriter, r *http.Request) {
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
	var config *Config
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		config = &Config{GithubToken: token}
	}

	stats, err := getOrgContributors(org, config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// extractRepoInfo extracts owner and repo name from GitHub URL
func extractRepoInfo(repoURL string) (string, string, error) {
	patterns := []string{
		`github\.com[:/]([^/]+)/([^/\.]+)(?:\.git)?$`,
		`github\.com/([^/]+)/([^/\.]+)/?$`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(repoURL)
		if len(matches) == 3 {
			return matches[1], matches[2], nil
		}
	}

	return "", "", fmt.Errorf("invalid GitHub repository URL")
}

func getAllReleases(owner, repo string, config *Config) ([]Release, error) {
	var allReleases []Release
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?page=%d&per_page=%d",
			owner, repo, page, perPage)

		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}

		// Add headers conditionally
		req.Header.Add("Accept", "application/vnd.github.v3+json")
		if config != nil && config.GithubToken != "" {
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", config.GithubToken))
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error making request: %v", err)
		}

		// Check rate limit headers
		remaining := resp.Header.Get("X-RateLimit-Remaining")
		limit := resp.Header.Get("X-RateLimit-Limit")

		if resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			return nil, fmt.Errorf("rate limit exceeded. Please use a GitHub token. Limit: %s, Remaining: %s", limit, remaining)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API returned status: %d, body: %s", resp.StatusCode, string(body))
		}

		var releases []Release
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error decoding response: %v", err)
		}
		resp.Body.Close()

		if len(releases) == 0 {
			break
		}

		allReleases = append(allReleases, releases...)

		if len(releases) < perPage {
			break
		}

		page++
	}

	return allReleases, nil
}

func handleRepoStats(w http.ResponseWriter, r *http.Request) {
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
	var config *Config
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		token = strings.TrimSpace(token)
		config = &Config{GithubToken: token}
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

func calculateDownloadStats(releases []Release) *DownloadStats {
	stats := &DownloadStats{
		Releases: make([]ReleaseDownloadStats, 0),
	}

	// Sort releases by creation date (newest first)
	sort.Slice(releases, func(i, j int) bool {
		return releases[i].CreatedAt.After(releases[j].CreatedAt)
	})

	for _, release := range releases {
		releaseStats := ReleaseDownloadStats{
			TagName:   release.TagName,
			CreatedAt: release.CreatedAt,
			Assets:    make([]AssetStats, 0),
		}

		for _, asset := range release.Assets {
			assetStats := AssetStats{
				Name:          asset.Name,
				DownloadCount: asset.DownloadCount,
			}
			releaseStats.TotalDownloads += asset.DownloadCount
			releaseStats.Assets = append(releaseStats.Assets, assetStats)
		}

		stats.TotalDownloads += releaseStats.TotalDownloads
		stats.Releases = append(stats.Releases, releaseStats)
	}

	return stats
}

func main() {
	http.Handle("/images/", http.StripPrefix("/images", http.FileServer(http.Dir("./images"))))
	// Serve static files
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFile(w, r, "./web/index.html")
			return
		}
	})

	http.HandleFunc("/orgs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/orgs" {
			http.ServeFile(w, r, "./web/org.html")
			return
		}
	})

	http.HandleFunc("/starhistory", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/starhistory" {
			http.ServeFile(w, r, "./web/stars.html")
			return
		}
	})

	// API endpoint
	http.HandleFunc("/repo-stats", handleRepoStats)
	http.HandleFunc("/org-contributors", handleOrgContributors)
	http.HandleFunc("/star-history", handleStarHistory)

	// Get port from environment variable or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
