package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	req, err := http.NewRequest(http.MethodGet, "/active-contributors?repo=https://github.com/keploy/keploy&org=keploy", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	rr := httptest.NewRecorder()
	HandleActiveContributors(rr, req)

	if rr.Body.Len() == 0 {
		t.Errorf("Expected non-empty response body")
	}
}
