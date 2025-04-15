package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"

	cu "github.com/keploy/gitstats/common"
)

func calculateDownloadStats(releases []cu.Release) *cu.DownloadStats {
	stats := &cu.DownloadStats{
		Releases: make([]cu.ReleaseDownloadStats, 0),
	}

	// Sort releases by creation date (newest first)
	sort.Slice(releases, func(i, j int) bool {
		return releases[i].CreatedAt.After(releases[j].CreatedAt)
	})

	for _, release := range releases {
		releaseStats := cu.ReleaseDownloadStats{
			TagName:   release.TagName,
			CreatedAt: release.CreatedAt,
			Assets:    make([]cu.AssetStats, 0),
		}

		for _, asset := range release.Assets {
			assetStats := cu.AssetStats{
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

func getAllReleases(owner, repo string, config *cu.Config) ([]cu.Release, error) {
	var allReleases []cu.Release
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

		var releases []cu.Release
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
func getStarHistory(owner, repo string, config *cu.Config) (*cu.StarHistory, error) {
	// GitHub's API doesn't provide direct star history, so we'll use stargazers endpoint
	page := 1
	perPage := 100
	history := make([]cu.StarPoint, 0)

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
			history = append(history, cu.StarPoint{
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

	return &cu.StarHistory{
		RepoName: fmt.Sprintf("%s/%s", owner, repo),
		History:  history,
	}, nil
}

func getOrgContributors(org string, config *cu.Config) (*cu.OrganizationStats, error) {
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

			var contributors []cu.Contributor
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

	return &cu.OrganizationStats{
		OrgName:           org,
		TotalRepos:        totalRepos,
		TotalContributors: len(totalContributors),
	}, nil
}

func getOrgMembers(org string) (map[string]struct{}, error) {
	members := make(map[string]struct{})
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/%s/members?page=%d&per_page=%d",
			org, page, perPage)

		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
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

		var orgMembers []struct {
			Login string `json:"login"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&orgMembers); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error decoding response: %v", err)
		}
		resp.Body.Close()

		if len(orgMembers) == 0 {
			break
		}

		for _, member := range orgMembers {
			members[member.Login] = struct{}{}
		}

		if len(orgMembers) < perPage {
			break
		}

		page++
	}

	return members, nil
}

func getRecentCommits(owner, repo string, since time.Time, config *cu.Config) ([]struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Commit struct {
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}, error) {
	page := 1
	perPage := 100
	var allCommits []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Commit struct {
			Author struct {
				Date time.Time `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}

	for {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?since=%s&page=%d&per_page=%d",
			owner, repo, since.Format(time.RFC3339), page, perPage)

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

		var commits []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Commit struct {
				Author struct {
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error decoding response: %v", err)
		}
		resp.Body.Close()

		if len(commits) == 0 {
			break
		}

		allCommits = append(allCommits, commits...)

		if len(commits) < perPage {
			break
		}

		page++
	}

	return allCommits, nil
}

func handleOrganization(w http.ResponseWriter, orgName string, config *cu.Config) {
	// Get organization members to exclude them
	orgMembers, err := getOrgMembers(orgName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get all repositories in the organization
	repos, err := getOrgRepositories(orgName, config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	since := time.Now().AddDate(0, 0, -30)
	contributorStats := make(map[string]*cu.ActiveContributor)

	// Collect commits from all repositories
	for _, repo := range repos {
		commits, err := getRecentCommits(orgName, repo.Name, since, config)
		if err != nil {
			// Log the error but continue with other repositories
			log.Printf("Error getting commits for %s/%s: %v", orgName, repo.Name, err)
			continue
		}

		processCommits(commits, orgMembers, contributorStats)
	}

	responseData := prepareResponse(contributorStats, orgName, "")
	sendJSONResponse(w, responseData)
}

func handleSingleRepo(w http.ResponseWriter, owner, repo string, config *cu.Config) {
	orgMembers, err := getOrgMembers(owner)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	since := time.Now().AddDate(0, 0, -30)
	commits, err := getRecentCommits(owner, repo, since, config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	contributorStats := make(map[string]*cu.ActiveContributor)
	processCommits(commits, orgMembers, contributorStats)

	responseData := prepareResponse(contributorStats, owner, repo)
	sendJSONResponse(w, responseData)
}

func getOrgRepositories(org string, config *cu.Config) ([]struct {
	Name string `json:"name"`
}, error) {
	page := 1
	perPage := 100
	var allRepos []struct {
		Name string `json:"name"`
	}

	for {
		url := fmt.Sprintf("https://api.github.com/orgs/%s/repos?page=%d&per_page=%d&type=public",
			org, page, perPage)

		client := &http.Client{}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating request: %v", err)
		}

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

		var repos []struct {
			Name string `json:"name"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("error decoding response: %v", err)
		}
		resp.Body.Close()

		if len(repos) == 0 {
			break
		}

		allRepos = append(allRepos, repos...)

		if len(repos) < perPage {
			break
		}

		page++
	}

	return allRepos, nil
}

func processCommits(commits []struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Commit struct {
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}, orgMembers map[string]struct{}, contributorStats map[string]*cu.ActiveContributor) {
	for _, commit := range commits {
		if commit.Author.Login == "" {
			continue
		}

		if _, isOrgMember := orgMembers[commit.Author.Login]; isOrgMember {
			continue
		}

		stats, exists := contributorStats[commit.Author.Login]
		if !exists {
			stats = &cu.ActiveContributor{
				Login:          commit.Author.Login,
				Contributions:  0,
				LastActiveDate: commit.Commit.Author.Date,
			}
			contributorStats[commit.Author.Login] = stats
		}

		stats.Contributions++
		if commit.Commit.Author.Date.After(stats.LastActiveDate) {
			stats.LastActiveDate = commit.Commit.Author.Date
		}
	}
}

func prepareResponse(contributorStats map[string]*cu.ActiveContributor, owner, repo string) cu.ActiveContributorsResponse {
	var activeContributors []cu.ActiveContributor
	for _, stats := range contributorStats {
		activeContributors = append(activeContributors, *stats)
	}

	sort.Slice(activeContributors, func(i, j int) bool {
		if activeContributors[i].Contributions == activeContributors[j].Contributions {
			return activeContributors[i].LastActiveDate.After(activeContributors[j].LastActiveDate)
		}
		return activeContributors[i].Contributions > activeContributors[j].Contributions
	})

	name := owner
	if repo != "" {
		name = fmt.Sprintf("%s/%s", owner, repo)
	}

	return cu.ActiveContributorsResponse{
		RepoName:           name,
		TimeRange:          "Last 30 days",
		ActiveContributors: activeContributors,
	}
}

func sendJSONResponse(w http.ResponseWriter, response cu.ActiveContributorsResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func fetchStargazers(owner, repo, token string, page int) ([]cu.Stargazer, bool, int, error) {
	perPage := 100
	client := &http.Client{}

	// First, get total stargazer count
	repoURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	repoReq, err := http.NewRequest("GET", repoURL, nil)
	if err != nil {
		return nil, false, 0, err
	}
	repoReq.Header.Add("Authorization", "Bearer "+token)
	repoResp, err := client.Do(repoReq)
	if err != nil {
		return nil, false, 0, err
	}
	defer repoResp.Body.Close()

	var repoData struct {
		StargazersCount int `json:"stargazers_count"`
	}
	if err := json.NewDecoder(repoResp.Body).Decode(&repoData); err != nil {
		return nil, false, 0, err
	}

	// Calculate the correct page number from the end
	totalPages := int(math.Ceil(float64(repoData.StargazersCount) / float64(perPage)))
	reversePage := totalPages - page + 1
	if reversePage < 1 {
		reversePage = 1
	}

	// Fetch stargazers for the requested reverse page
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/stargazers?page=%d&per_page=%d",
		owner, repo, reversePage, perPage)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, false, 0, err
	}

	req.Header.Add("Accept", "application/vnd.github.v3.star+json")
	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false, 0, fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}

	var starResponses []cu.StargazerResponse
	if err := json.NewDecoder(resp.Body).Decode(&starResponses); err != nil {
		return nil, false, 0, err
	}

	// Fetch additional user details for each stargazer
	var stargazers []cu.Stargazer
	for _, sr := range starResponses {
		user, err := fetchUserDetails(sr.User.Login, token)
		if err != nil {
			log.Printf("Error fetching details for user %s: %v", sr.User.Login, err)
			continue
		}

		stargazers = append(stargazers, cu.Stargazer{
			Login:     user.Login,
			Name:      user.Name,
			AvatarURL: user.AvatarURL,
			Location:  user.Location,
			HTMLURL:   user.HTMLURL,
			StarredAt: sr.StarredAt,
		})
	}

	// Sort by starred date, newest first
	sort.Slice(stargazers, func(i, j int) bool {
		return stargazers[i].StarredAt.After(stargazers[j].StarredAt)
	})

	// Check if there are more pages (in reverse)
	hasMore := reversePage > 1
	return stargazers, hasMore, repoData.StargazersCount, nil
}

func fetchUserDetails(username, token string) (*cu.User, error) {
	client := &http.Client{}
	url := fmt.Sprintf("https://api.github.com/users/%s", username)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
	}

	var user cu.User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}

	return &user, nil
}
