package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for the keploy/keploy#2601 fix: the stargazers endpoint used to burn
// ~102 GitHub API calls per page (1 repo + 1 page + 1 user-detail call per
// stargazer) and failed at once on a rate limit. User details are now cached
// in memory and GitHub calls retry rate limits / 5xx with backoff, so a big
// repo no longer exhausts the token on repeat loads.

func overrideGitHubAPI(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	t.Cleanup(func() { githubAPIBaseURL = orig })
}

// stubSleep replaces githubSleep with a recorder so tests never really wait.
func stubSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	orig := githubSleep
	githubSleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { githubSleep = orig })
	return &slept
}

func TestFetchStargazers_RetriesAfterRateLimit(t *testing.T) {
	var stargazerCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/stargazers"):
			if stargazerCalls.Add(1) == 1 {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(2*time.Second).Unix()))
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`[{"user":{"login":"alice","avatar_url":"https://example.com/a.png","html_url":"https://github.com/alice"},"starred_at":"2026-01-02T03:04:05Z"}]`))
		case r.URL.Path == "/repos/o/r":
			_, _ = w.Write([]byte(`{"stargazers_count":1}`))
		case strings.HasPrefix(r.URL.Path, "/users/"):
			_, _ = w.Write([]byte(`{"login":"alice","name":"Alice","avatar_url":"https://example.com/a.png","location":"Wonderland","html_url":"https://github.com/alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	slept := stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	stargazers, hasMore, total, err := fetchStargazers("o", "r", "tok", 1)
	if err != nil {
		t.Fatalf("fetchStargazers returned error: %v", err)
	}
	if total != 1 || len(stargazers) != 1 || stargazers[0].Login != "alice" || stargazers[0].Name != "Alice" {
		t.Fatalf("unexpected result: total=%d stargazers=%+v hasMore=%v", total, stargazers, hasMore)
	}
	if got := stargazerCalls.Load(); got != 2 {
		t.Errorf("stargazers endpoint called %d times, want 2 (1 rate-limited + 1 retry)", got)
	}
	if len(*slept) == 0 {
		t.Errorf("expected a backoff sleep before retrying, got none")
	}
}

func TestFetchStargazers_HappyPath(t *testing.T) {
	var userCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			_, _ = w.Write([]byte(`{"stargazers_count":2}`))
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/stargazers"):
			// alice listed first, bob starred later: result must be newest first.
			_, _ = w.Write([]byte(`[
				{"user":{"login":"alice","avatar_url":"https://example.com/a.png","html_url":"https://github.com/alice"},"starred_at":"2026-01-02T03:04:05Z"},
				{"user":{"login":"bob","avatar_url":"https://example.com/b.png","html_url":"https://github.com/bob"},"starred_at":"2026-01-03T03:04:05Z"}
			]`))
		case r.URL.Path == "/users/alice":
			userCalls.Add(1)
			_, _ = w.Write([]byte(`{"login":"alice","name":"Alice","avatar_url":"https://example.com/a.png","location":"Wonderland","html_url":"https://github.com/alice"}`))
		case r.URL.Path == "/users/bob":
			userCalls.Add(1)
			_, _ = w.Write([]byte(`{"login":"bob","name":"Bob","avatar_url":"https://example.com/b.png","location":"Builderland","html_url":"https://github.com/bob"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	stargazers, hasMore, total, err := fetchStargazers("o", "r", "tok", 1)
	if err != nil {
		t.Fatalf("fetchStargazers returned error: %v", err)
	}
	if total != 2 || len(stargazers) != 2 {
		t.Fatalf("total=%d stargazers=%d, want 2 and 2", total, len(stargazers))
	}
	if hasMore {
		t.Errorf("hasMore = true, want false for a 2-star repo on page 1")
	}
	if stargazers[0].Login != "bob" || stargazers[1].Login != "alice" {
		t.Errorf("order = %s, %s; want bob (newest) first, then alice", stargazers[0].Login, stargazers[1].Login)
	}
	if stargazers[0].Location != "Builderland" || stargazers[1].Name != "Alice" {
		t.Errorf("user details missing: %+v", stargazers)
	}
	if got := userCalls.Load(); got != 2 {
		t.Errorf("user-detail endpoint called %d times, want 2 (one per stargazer)", got)
	}
}

func TestFetchUserDetails_CachedWithinTTL(t *testing.T) {
	var userCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userCalls.Add(1)
		login := strings.TrimPrefix(r.URL.Path, "/users/")
		_, _ = fmt.Fprintf(w, `{"login":%q,"name":"Name %s","avatar_url":"https://example.com/a.png","html_url":"https://github.com/%s"}`, login, login, login)
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	u1, err := fetchUserDetails("alice", "tok")
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	u2, err := fetchUserDetails("alice", "tok")
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if u1.Name != u2.Name || u1.Login != "alice" {
		t.Fatalf("cached result differs: %+v vs %+v", u1, u2)
	}
	if _, err := fetchUserDetails("bob", "tok"); err != nil {
		t.Fatalf("fetch for another user failed: %v", err)
	}
	// alice twice + bob once must cost 2 API calls, not 3.
	if got := userCalls.Load(); got != 2 {
		t.Errorf("user-detail endpoint called %d times, want 2 (alice cached)", got)
	}
}

func TestFetchUserDetails_CacheExpires(t *testing.T) {
	var userCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userCalls.Add(1)
		_, _ = w.Write([]byte(`{"login":"alice","name":"Alice","avatar_url":"https://example.com/a.png","html_url":"https://github.com/alice"}`))
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	stubSleep(t)
	userCache = newUserDetailsCache(time.Millisecond)

	if _, err := fetchUserDetails("alice", "tok"); err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := fetchUserDetails("alice", "tok"); err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if got := userCalls.Load(); got != 2 {
		t.Errorf("user-detail endpoint called %d times, want 2 (entry expired)", got)
	}
}

func TestFetchUserDetails_RateLimitFarFuture(t *testing.T) {
	var userCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userCalls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	slept := stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	_, err := fetchUserDetails("alice", "tok")
	if err == nil {
		t.Fatal("expected a rate-limit error, got nil")
	}
	var rlErr *rateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("error = %v (%T), want *rateLimitError", err, err)
	}
	if rlErr.RetryAfter < 30*time.Minute {
		t.Errorf("RetryAfter = %s, want about an hour", rlErr.RetryAfter)
	}
	if got := userCalls.Load(); got != 1 {
		t.Errorf("endpoint called %d times, want 1 (no point retrying a far-off reset)", got)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v, want no sleep when the reset is far away", *slept)
	}
}

func TestFetchUserDetails_NonRateLimit403NoRetry(t *testing.T) {
	var userCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userCalls.Add(1)
		// Quota remains, so this 403 is e.g. bad credentials, not a limit.
		w.Header().Set("X-RateLimit-Remaining", "60")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	slept := stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	_, err := fetchUserDetails("alice", "tok")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var rlErr *rateLimitError
	if errors.As(err, &rlErr) {
		t.Errorf("error = %v, want a plain error, not a rate-limit error", err)
	}
	if got := userCalls.Load(); got != 1 {
		t.Errorf("endpoint called %d times, want 1 (no retry for a non-limit 403)", got)
	}
	if len(*slept) != 0 {
		t.Errorf("slept %v, want no sleep for a non-retryable error", *slept)
	}
}

func TestFetchUserDetails_RetriesOnServerError(t *testing.T) {
	var userCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"login":"alice","name":"Alice","avatar_url":"https://example.com/a.png","html_url":"https://github.com/alice"}`))
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	slept := stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	user, err := fetchUserDetails("alice", "tok")
	if err != nil {
		t.Fatalf("fetch returned error: %v", err)
	}
	if user.Login != "alice" {
		t.Fatalf("user = %+v, want alice", user)
	}
	if got := userCalls.Load(); got != 2 {
		t.Errorf("endpoint called %d times, want 2 (1 failure + 1 retry)", got)
	}
	if len(*slept) == 0 {
		t.Errorf("expected a backoff sleep before retrying, got none")
	}
}

func TestHandleStargazers_RateLimitReturns429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			_, _ = w.Write([]byte(`{"stargazers_count":1}`))
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/stargazers"):
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	overrideGitHubAPI(t, srv)
	stubSleep(t)
	userCache = newUserDetailsCache(time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/github-stargazers?owner=o&repo=r", nil)
	rr := httptest.NewRecorder()
	HandleStargazers(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusTooManyRequests, rr.Body.String())
	}
	after := rr.Header().Get("Retry-After")
	secs, err := strconv.Atoi(after)
	if err != nil || secs < 3500 {
		t.Errorf("Retry-After = %q, want about 3600 seconds", after)
	}
	if !strings.Contains(rr.Body.String(), "rate limit") {
		t.Errorf("body = %q, want it to mention the rate limit", rr.Body.String())
	}
}
