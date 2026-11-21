package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"

	cu "github.com/keploy/gitstats/common"
)

// githubHTTPClientTimeout bounds every outbound GitHub API call. Without it the
// default http.Client waits forever, which can hang request handlers (and CI
// tests) indefinitely when GitHub is slow or unreachable.
const githubHTTPClientTimeout = 15 * time.Second

// githubAPIBaseURL is the GitHub REST API root. It is a var (not a const) so
// tests can point it at an httptest stub; production always uses the real host.
var githubAPIBaseURL = "https://api.github.com"

// tokenOf extracts the token from a config, tolerating a nil config.
func tokenOf(config *cu.Config) string {
	if config == nil {
		return ""
	}
	return config.GithubToken
}

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

func getAllReleases(ctx context.Context, owner, repo string, config *cu.Config) ([]cu.Release, error) {
	var allReleases []cu.Release
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("%s/repos/%s/%s/releases?page=%d&per_page=%d", githubAPIBaseURL,
			owner, repo, page, perPage)

		body, _, err := githubAPIGet(ctx, tokenOf(config), "", url)
		if err != nil {
			return nil, err
		}

		var releases []cu.Release
		if err := json.Unmarshal(body, &releases); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

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
func getStarHistory(ctx context.Context, owner, repo string, config *cu.Config) (*cu.StarHistory, error) {
	// GitHub's API doesn't provide direct star history, so we'll use stargazers endpoint
	page := 1
	perPage := 100
	history := make([]cu.StarPoint, 0)

	for {
		url := fmt.Sprintf("%s/repos/%s/%s/stargazers?page=%d&per_page=%d", githubAPIBaseURL,
			owner, repo, page, perPage)

		// Required header for timestamp information
		body, _, err := githubAPIGet(ctx, tokenOf(config), "application/vnd.github.v3.star+json", url)
		if err != nil {
			return nil, err
		}

		// Parse response
		var stargazers []struct {
			StarredAt time.Time `json:"starred_at"`
		}

		if err := json.Unmarshal(body, &stargazers); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

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

func getOrgContributors(ctx context.Context, org string, config *cu.Config) (*cu.OrganizationStats, error) {
	page := 1
	perPage := 100
	totalContributors := make(map[string]struct{})
	totalRepos := 0

	for {
		url := fmt.Sprintf("%s/orgs/%s/repos?page=%d&per_page=%d", githubAPIBaseURL, org, page, perPage)

		body, _, err := githubAPIGet(ctx, tokenOf(config), "", url)
		if err != nil {
			return nil, err
		}

		var repos []map[string]interface{}
		if err := json.Unmarshal(body, &repos); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

		if len(repos) == 0 {
			break
		}

		totalRepos += len(repos)

		for _, repo := range repos {
			repoName := repo["name"].(string)
			repoContributorsURL := fmt.Sprintf("%s/repos/%s/%s/contributors", githubAPIBaseURL, org, repoName)

			contribBody, _, err := githubAPIGet(ctx, tokenOf(config), "", repoContributorsURL)
			if err != nil {
				return nil, err
			}

			if len(contribBody) == 0 {
				// 204 No Content: repository has no contributors, continue to the next one
				continue
			}

			var contributors []cu.Contributor
			if err := json.Unmarshal(contribBody, &contributors); err != nil {
				return nil, fmt.Errorf("error decoding contributors response: %v", err)
			}

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

func getOrgMembers(ctx context.Context, org string, config *cu.Config) (map[string]struct{}, error) {
	members := make(map[string]struct{})
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("%s/orgs/%s/members?page=%d&per_page=%d", githubAPIBaseURL,
			org, page, perPage)

		body, _, err := githubAPIGet(ctx, tokenOf(config), "", url)
		if err != nil {
			return nil, err
		}

		var orgMembers []struct {
			Login string `json:"login"`
		}

		if err := json.Unmarshal(body, &orgMembers); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

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

func getRecentCommits(ctx context.Context, owner, repo string, since time.Time, config *cu.Config) ([]struct {
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
		url := fmt.Sprintf("%s/repos/%s/%s/commits?since=%s&page=%d&per_page=%d", githubAPIBaseURL,
			owner, repo, since.Format(time.RFC3339), page, perPage)

		body, _, err := githubAPIGet(ctx, tokenOf(config), "", url)
		if err != nil {
			return nil, err
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

		if err := json.Unmarshal(body, &commits); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

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

func handleOrganization(ctx context.Context, w http.ResponseWriter, orgName string, config *cu.Config) {
	// Get organization members to exclude them
	orgMembers, err := getOrgMembers(ctx, orgName, config)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	// Get all repositories in the organization
	repos, err := getOrgRepositories(ctx, orgName, config)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	since := time.Now().AddDate(0, 0, -30)
	contributorStats := make(map[string]*cu.ActiveContributor)

	// Collect commits from all repositories
	for _, repo := range repos {
		commits, err := getRecentCommits(ctx, orgName, repo.Name, since, config)
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

func handleSingleRepo(ctx context.Context, w http.ResponseWriter, owner, repo string, config *cu.Config) {
	orgMembers, err := getOrgMembers(ctx, owner, config)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	since := time.Now().AddDate(0, 0, -30)
	commits, err := getRecentCommits(ctx, owner, repo, since, config)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	contributorStats := make(map[string]*cu.ActiveContributor)
	processCommits(commits, orgMembers, contributorStats)

	responseData := prepareResponse(contributorStats, owner, repo)
	sendJSONResponse(w, responseData)
}

func getOrgRepositories(ctx context.Context, org string, config *cu.Config) ([]struct {
	Name string `json:"name"`
}, error) {
	page := 1
	perPage := 100
	var allRepos []struct {
		Name string `json:"name"`
	}

	for {
		url := fmt.Sprintf("%s/orgs/%s/repos?page=%d&per_page=%d&type=public", githubAPIBaseURL,
			org, page, perPage)

		body, _, err := githubAPIGet(ctx, tokenOf(config), "", url)
		if err != nil {
			return nil, err
		}

		var repos []struct {
			Name string `json:"name"`
		}

		if err := json.Unmarshal(body, &repos); err != nil {
			return nil, fmt.Errorf("error decoding response: %v", err)
		}

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

// fetchStargazers returns one page of stargazers, newest first.
//
// GitHub paginates the stargazers endpoint oldest-first, so "page 1" of the
// UI (the newest stargazers) maps to the last API page: reversePage is
// computed from the total stargazer count and hasMore reports whether older
// pages remain.
//
// Rate limiting: both the repo lookup and the stargazers page go through
// githubAPIGet, which retries rate-limit responses honouring Retry-After /
// X-RateLimit-Reset with bounded backoff and returns a *RateLimitError when
// the limit is exhausted (mapped to HTTP 429 by the handler). Individual
// user-detail lookups are cached for an hour and, if they still fail, the
// stargazer is returned with the basic fields from the stargazers endpoint
// so callers get partial results instead of a hard failure.
func fetchStargazers(ctx context.Context, owner, repo, token string, page int) ([]cu.Stargazer, bool, int, error) {
	perPage := 100

	// First, get total stargazer count
	repoURL := fmt.Sprintf("%s/repos/%s/%s", githubAPIBaseURL, owner, repo)
	repoBody, _, err := githubAPIGet(ctx, token, "", repoURL)
	if err != nil {
		return nil, false, 0, err
	}

	var repoData struct {
		StargazersCount int `json:"stargazers_count"`
	}
	if err := json.Unmarshal(repoBody, &repoData); err != nil {
		return nil, false, 0, fmt.Errorf("error decoding response: %v", err)
	}

	// Calculate the correct page number from the end
	totalPages := int(math.Ceil(float64(repoData.StargazersCount) / float64(perPage)))
	reversePage := totalPages - page + 1
	if reversePage < 1 {
		reversePage = 1
	}

	// Fetch stargazers for the requested reverse page
	url := fmt.Sprintf("%s/repos/%s/%s/stargazers?page=%d&per_page=%d", githubAPIBaseURL,
		owner, repo, reversePage, perPage)

	body, _, err := githubAPIGet(ctx, token, "application/vnd.github.v3.star+json", url)
	if err != nil {
		return nil, false, 0, err
	}

	var starResponses []cu.StargazerResponse
	if err := json.Unmarshal(body, &starResponses); err != nil {
		return nil, false, 0, fmt.Errorf("error decoding response: %v", err)
	}

	// Fetch additional user details for each stargazer
	stargazers := make([]cu.Stargazer, 0, len(starResponses))
	for _, sr := range starResponses {
		user, err := fetchUserDetails(ctx, sr.User.Login, token)
		if err != nil {
			// Partial results: keep the stargazer with the basic fields we
			// already have from the stargazers endpoint instead of dropping
			// the whole page when a single profile lookup fails.
			log.Printf("Error fetching details for user %s: %v", sr.User.Login, err)
			stargazers = append(stargazers, cu.Stargazer{
				Login:     sr.User.Login,
				AvatarURL: sr.User.AvatarURL,
				HTMLURL:   sr.User.HTMLURL,
				StarredAt: sr.StarredAt,
			})
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

// fetchUserDetails returns a GitHub user profile, caching it in memory for
// userCacheTTL. The stargazers endpoint costs one /users/<login> call per
// stargazer per page; the cache removes those repeat calls on every page
// reload, which is what exhausts the 5000 req/h token budget on large repos.
func fetchUserDetails(ctx context.Context, username, token string) (*cu.User, error) {
	if user, ok := cachedUserDetails(username); ok {
		return user, nil
	}

	url := fmt.Sprintf("%s/users/%s", githubAPIBaseURL, username)
	body, _, err := githubAPIGet(ctx, token, "", url)
	if err != nil {
		return nil, err
	}

	var user cu.User
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	storeUserDetails(username, &user)
	return &user, nil
}
