package common

import "time"

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

// ActiveContributor represents a contributor's activity stats
type ActiveContributor struct {
	Login          string    `json:"login"`
	Contributions  int       `json:"contributions"`
	LastActiveDate time.Time `json:"last_active_date"`
}

// ActiveContributorsResponse represents the response for active contributors
type ActiveContributorsResponse struct {
	RepoName           string              `json:"repo_name"`
	TimeRange          string              `json:"time_range"`
	ActiveContributors []ActiveContributor `json:"active_contributors"`
}

type StargazerResponse struct {
	User      User      `json:"user"`
	StarredAt time.Time `json:"starred_at"`
}

type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Name      string `json:"name"`
	Location  string `json:"location"`
	HTMLURL   string `json:"html_url"`
}

type Stargazer struct {
	Login     string    `json:"login"`
	AvatarURL string    `json:"avatar_url"`
	HTMLURL   string    `json:"html_url"`
	Name      string    `json:"name,omitempty"`
	Location  string    `json:"location,omitempty"`
	StarredAt time.Time `json:"starred_at"`
}

type PageData struct {
	Stargazers []Stargazer
	RepoOwner  string
	RepoName   string
	Error      string
	HasMore    bool
	NextPage   int
	TotalCount int
}
