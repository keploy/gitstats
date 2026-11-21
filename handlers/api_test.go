package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cu "github.com/keploy/gitstats/common"
)

// Test generated using Keploy
func TestHandleRepoStats_MethodNotAllowed(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/repo-stats", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleRepoStats(rr, req)

	if status := rr.Code; status != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %v, got %v", http.StatusMethodNotAllowed, status)
	}

	expected := "Method not allowed\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleRepoStats_MissingRepoParameter(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/repo-stats", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleRepoStats(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Expected status code %v, got %v", http.StatusBadRequest, status)
	}

	expected := "Repository URL is required\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleRepoStats_InvalidRepoURL(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/repo-stats?repo=invalid-url", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleRepoStats(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Expected status code %v, got %v", http.StatusBadRequest, status)
	}

	if !strings.Contains(rr.Body.String(), "Invalid repository URL") {
		t.Errorf("Expected error message to contain 'Invalid repository URL', got %q", rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleStarHistory_NoRepos(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/star-history", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleStarHistory(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Expected status code %v, got %v", http.StatusBadRequest, status)
	}

	expected := "At least one repository URL is required\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleOrgContributors_MissingOrg(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/org-contributors", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleOrgContributors(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Expected status code %v, got %v", http.StatusBadRequest, status)
	}

	expected := "Organization name is required\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleStargazers_MissingOwnerOrRepo(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/stargazers", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleStargazers(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Expected status code %v, got %v", http.StatusBadRequest, status)
	}

	expected := "Both owner and repository name are required\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleActiveContributors_MissingOrgAndRepo(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/active-contributors", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	HandleActiveContributors(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Expected status code %v, got %v", http.StatusBadRequest, status)
	}

	expected := "Either organization name or repository URL is required\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleStarHistory_MethodNotAllowed(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/star-history", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	rr := httptest.NewRecorder()
	HandleStarHistory(rr, req)
	if status := rr.Code; status != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %v, got %v", http.StatusMethodNotAllowed, status)
	}
	expected := "Method not allowed\n"
	if rr.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, rr.Body.String())
	}
}

// Test generated using Keploy
func TestHandleActiveContributors_ValidOrgAndRepo(t *testing.T) {
	// Hermetic: stub the GitHub REST API so this never touches the network.
	// (It previously hit live api.github.com with a weak Body.Len()>0 assertion
	// that passed even on rate-limit/error responses.)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orgs/keploy/members":
			// one org member: must be excluded from active contributors
			_, _ = w.Write([]byte(`[{"login":"orgdev"}]`))
		case "/repos/keploy/keploy/commits":
			// an external contributor (2 commits) + an org member (1 commit)
			_, _ = w.Write([]byte(`[
				{"author":{"login":"extuser"},"commit":{"author":{"date":"2026-09-10T10:00:00Z"}}},
				{"author":{"login":"extuser"},"commit":{"author":{"date":"2026-09-12T10:00:00Z"}}},
				{"author":{"login":"orgdev"},"commit":{"author":{"date":"2026-09-11T10:00:00Z"}}}
			]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	orig := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = orig }()

	req := httptest.NewRequest(http.MethodGet, "/active-contributors?repo=https://github.com/keploy/keploy&org=keploy", nil)
	rr := httptest.NewRecorder()
	HandleActiveContributors(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp cu.ActiveContributorsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v; body=%s", err, rr.Body.String())
	}
	if resp.RepoName != "keploy/keploy" {
		t.Errorf("repo_name = %q, want %q", resp.RepoName, "keploy/keploy")
	}
	// orgdev is an org member -> excluded; extuser is external -> the sole active
	// contributor, with both of its commits counted.
	if len(resp.ActiveContributors) != 1 {
		t.Fatalf("active_contributors = %d (%+v), want 1", len(resp.ActiveContributors), resp.ActiveContributors)
	}
	if got := resp.ActiveContributors[0]; got.Login != "extuser" || got.Contributions != 2 {
		t.Errorf("contributor = %+v, want {Login:extuser Contributions:2}", got)
	}
}
