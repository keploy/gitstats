package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cu "github.com/keploy/gitstats/common"
)

// Mock HTTP client and server setup for testing
func setupMockServer(response string, statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(response))
	}))
}

func TestCalculateDownloadStats(t *testing.T) {
	releases := []cu.Release{
		{
			TagName:   "v1.0",
			CreatedAt: time.Now(),
			Assets: []cu.ReleaseAsset{
				{Name: "asset1", DownloadCount: 10},
				{Name: "asset2", DownloadCount: 20},
			},
		},
	}

	stats := calculateDownloadStats(releases)
	if stats.TotalDownloads != 30 {
		t.Errorf("Expected total downloads to be 30, got %d", stats.TotalDownloads)
	}
}

func TestExtractRepoInfo(t *testing.T) {
	tests := []struct {
		url      string
		owner    string
		repo     string
		hasError bool
	}{
		{"https://github.com/owner/repo", "owner", "repo", false},
		{"https://github.com/owner/repo.git", "owner", "repo", false},
		{"invalid-url", "", "", true},
	}

	for _, tt := range tests {
		owner, repo, err := extractRepoInfo(tt.url)
		if (err != nil) != tt.hasError {
			t.Errorf("extractRepoInfo(%s) error = %v, hasError %v", tt.url, err, tt.hasError)
			continue
		}
		if owner != tt.owner || repo != tt.repo {
			t.Errorf("extractRepoInfo(%s) = %s, %s, want %s, %s", tt.url, owner, repo, tt.owner, tt.repo)
		}
	}
}

// Test generated using Keploy
func TestExtractRepoInfo_ValidURL(t *testing.T) {
	owner, repo, err := extractRepoInfo("https://github.com/keploy/gitstats")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if owner != "keploy" || repo != "gitstats" {
		t.Errorf("Expected owner 'keploy' and repo 'gitstats', got owner '%s' and repo '%s'", owner, repo)
	}
}

// Test generated using Keploy
func TestExtractRepoInfo_InvalidURL(t *testing.T) {
	_, _, err := extractRepoInfo("https://invalid-url.com")
	if err == nil || err.Error() != "invalid GitHub repository URL" {
		t.Errorf("Expected error for invalid URL, got %v", err)
	}
}
