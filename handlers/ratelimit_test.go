package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- test helpers -----------------------------------------------------------

// fakeClock records sleeps and advances a fake clock so backoff/retry tests
// run instantly and deterministically.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (f *fakeClock) sleep(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleeps = append(f.sleeps, d)
	f.now = f.now.Add(d)
}

func (f *fakeClock) nowFn() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) recorded() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]time.Duration, len(f.sleeps))
	copy(out, f.sleeps)
	return out
}

// installFakeClock swaps sleepFunc/nowFunc for a fake clock and resets the
// circuit breaker + user cache so tests are isolated from each other.
func installFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	// Truncate to whole seconds so X-RateLimit-Reset (unix seconds) arithmetic
	// in tests is exact.
	fc := &fakeClock{now: time.Now().Truncate(time.Second)}
	origSleep, origNow := sleepFunc, nowFunc
	sleepFunc = fc.sleep
	nowFunc = fc.nowFn
	resetGitHubState()
	t.Cleanup(func() {
		sleepFunc, nowFunc = origSleep, origNow
		resetGitHubState()
	})
	return fc
}

func resetGitHubState() {
	rateLimitMu.Lock()
	rateLimitedUntil = time.Time{}
	rateLimitMu.Unlock()
	userCacheMu.Lock()
	userCache = make(map[string]cachedUser)
	userCacheMu.Unlock()
}

// stubGitHub points the GitHub API base URL at an httptest server.
func stubGitHub(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	t.Cleanup(func() { githubAPIBaseURL = orig })
	return srv
}

// writeRateLimit answers like GitHub does when the primary rate limit is hit.
func writeRateLimit(w http.ResponseWriter, status int, retryAfter string, resetUnix int64) {
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.Header().Set("X-RateLimit-Limit", "5000")
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	if resetUnix > 0 {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetUnix, 10))
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
}

func stargazerEntry(login, starredAt string) string {
	return fmt.Sprintf(`{"starred_at":%q,"user":{"login":%q,"avatar_url":"https://avatars/%s.png","html_url":"https://github.com/%s"}}`,
		starredAt, login, login, login)
}

func userJSON(login, name, location string) string {
	return fmt.Sprintf(`{"login":%q,"avatar_url":"https://avatars/%s.png","name":%q,"location":%q,"html_url":"https://github.com/%s"}`,
		login, login, name, location, login)
}

// --- rate-limit detection / retry -------------------------------------------

// TestHandleStargazers_RateLimitExhausted reproduces keploy/keploy#2601: a 403
// with X-RateLimit-Remaining: 0 and a Retry-After further away than the wait
// cap must surface as 429 + Retry-After + a clear message, failing fast
// without hammering the API.
func TestHandleStargazers_RateLimitExhausted(t *testing.T) {
	installFakeClock(t)
	var calls int64
	var mu sync.Mutex
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		writeRateLimit(w, http.StatusForbidden, "3600", 0)
	})

	req := httptest.NewRequest(http.MethodGet, "/github-stargazers?owner=keploy&repo=keploy&page=1", nil)
	rr := httptest.NewRecorder()
	HandleStargazers(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%q", rr.Code, rr.Body.String())
	}
	if ra := rr.Header().Get("Retry-After"); ra != "3600" {
		t.Errorf("Retry-After header = %q, want 3600", ra)
	}
	if !strings.Contains(strings.ToLower(rr.Body.String()), "rate limit") {
		t.Errorf("body should clearly mention the rate limit, got %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "GITHUB_TOKEN") {
		t.Errorf("body should mention the token knob, got %q", rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("upstream calls = %d, want 1 (fail fast when wait exceeds the cap)", calls)
	}
}

// TestFetchStargazers_RateLimitedThenSuccess: a 403 rate limit with a short
// Retry-After must be retried (honouring Retry-After) and then succeed.
func TestFetchStargazers_RateLimitedThenSuccess(t *testing.T) {
	fc := installFakeClock(t)
	var mu sync.Mutex
	repoCalls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			mu.Lock()
			repoCalls++
			n := repoCalls
			mu.Unlock()
			if n == 1 {
				writeRateLimit(w, http.StatusForbidden, "1", 0)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stargazers_count":1}`))
		case r.URL.Path == "/repos/o/r/stargazers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[" + stargazerEntry("alice", "2025-01-02T00:00:00Z") + "]"))
		case r.URL.Path == "/users/alice":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(userJSON("alice", "Alice", "Earth")))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	stargazers, hasMore, total, err := fetchStargazers(context.Background(), "o", "r", "tok", 1)
	if err != nil {
		t.Fatalf("fetchStargazers error = %v, want nil (should retry after Retry-After)", err)
	}
	if total != 1 || len(stargazers) != 1 || stargazers[0].Login != "alice" || stargazers[0].Name != "Alice" {
		t.Errorf("unexpected result: total=%d stargazers=%+v", total, stargazers)
	}
	if hasMore {
		t.Errorf("hasMore = true, want false for a single page")
	}
	sleeps := fc.recorded()
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Errorf("sleeps = %v, want [1s] (Retry-After: 1 honoured)", sleeps)
	}
	mu.Lock()
	defer mu.Unlock()
	if repoCalls != 2 {
		t.Errorf("repo endpoint calls = %d, want 2 (one retry)", repoCalls)
	}
}

// TestFetchStargazers_429WithResetHeader: a 429 without Retry-After falls back
// to X-RateLimit-Reset for the wait duration.
func TestFetchStargazers_429WithResetHeader(t *testing.T) {
	fc := installFakeClock(t)
	var mu sync.Mutex
	calls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r" {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				writeRateLimit(w, http.StatusTooManyRequests, "", fc.nowFn().Unix()+2)
				return
			}
			_, _ = w.Write([]byte(`{"stargazers_count":0}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	_, _, total, err := fetchStargazers(context.Background(), "o", "r", "tok", 1)
	if err != nil {
		t.Fatalf("fetchStargazers error = %v, want nil", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	sleeps := fc.recorded()
	if len(sleeps) != 1 || sleeps[0] != 2*time.Second {
		t.Errorf("sleeps = %v, want [2s] derived from X-RateLimit-Reset", sleeps)
	}
}

// TestFetchStargazers_NonRateLimit403FailsFast: a 403 that is NOT a rate limit
// (remaining quota left, e.g. bad credentials) must fail immediately without
// any retry.
func TestFetchStargazers_NonRateLimit403FailsFast(t *testing.T) {
	fc := installFakeClock(t)
	var mu sync.Mutex
	calls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})

	_, _, _, err := fetchStargazers(context.Background(), "o", "r", "tok", 1)
	if err == nil {
		t.Fatal("expected an error for a non-rate-limit 403")
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		t.Errorf("error should not be a RateLimitError for a 403 with remaining quota: %v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should include the status, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("upstream calls = %d, want 1 (no retry for non-rate-limit 403)", calls)
	}
	if sleeps := fc.recorded(); len(sleeps) != 0 {
		t.Errorf("sleeps = %v, want none", sleeps)
	}
}

// TestFetchStargazers_RetryBudgetExhausted: repeated short Retry-After rate
// limits exhaust the retry budget and surface a *RateLimitError; afterwards
// the circuit breaker makes further calls fail fast without HTTP requests.
func TestFetchStargazers_RetryBudgetExhausted(t *testing.T) {
	fc := installFakeClock(t)
	var mu sync.Mutex
	calls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		writeRateLimit(w, http.StatusForbidden, "1", 0)
	})

	_, _, _, err := fetchStargazers(context.Background(), "o", "r", "tok", 1)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("error = %v, want *RateLimitError", err)
	}
	if rl.RetryAfter != time.Second {
		t.Errorf("RetryAfter = %v, want 1s", rl.RetryAfter)
	}
	if rl.Remaining != 0 || rl.Limit != 5000 {
		t.Errorf("limit/remaining = %d/%d, want 5000/0", rl.Limit, rl.Remaining)
	}
	mu.Lock()
	wantCalls := int64(maxAPIRetries + 1)
	if int64(calls) != wantCalls {
		t.Errorf("upstream calls = %d, want %d (retry budget)", calls, wantCalls)
	}
	mu.Unlock()
	if sleeps := fc.recorded(); len(sleeps) != maxAPIRetries {
		t.Errorf("sleeps = %v, want %d backoff sleeps", sleeps, maxAPIRetries)
	}

	// Circuit breaker: the next call must fail fast without any HTTP request.
	mu.Lock()
	before := calls
	mu.Unlock()
	_, _, _, err = fetchStargazers(context.Background(), "o", "r", "tok", 1)
	if !errors.As(err, &rl) {
		t.Fatalf("after circuit breaker, error = %v, want *RateLimitError", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != before {
		t.Errorf("circuit breaker open but upstream calls went %d -> %d", before, calls)
	}
}

// TestGitHubAPIGet_5xxRetryBackoff: transient 5xx responses are retried with
// bounded exponential backoff (1s, 2s, ...) before succeeding.
func TestGitHubAPIGet_5xxRetryBackoff(t *testing.T) {
	fc := installFakeClock(t)
	var mu sync.Mutex
	calls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"server error"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	body, _, err := githubAPIGet(context.Background(), "tok", "", githubAPIBaseURL+"/repos/o/r")
	if err != nil {
		t.Fatalf("githubAPIGet error = %v, want nil after retries", err)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body = %s, want the 200 response", body)
	}
	sleeps := fc.recorded()
	want := []time.Duration{baseBackoff, 2 * baseBackoff}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Errorf("sleep[%d] = %v, want %v (exponential backoff)", i, sleeps[i], want[i])
		}
	}
}

// --- pagination ---------------------------------------------------------------

// TestFetchStargazers_Pagination: with 250 stargazers (3 API pages of 100),
// UI page 1 must map to API page 3 (newest first) with hasMore=true, and UI
// page 3 maps to API page 1 with hasMore=false.
func TestFetchStargazers_Pagination(t *testing.T) {
	installFakeClock(t)
	var mu sync.Mutex
	var starPages []string
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			_, _ = w.Write([]byte(`{"stargazers_count":250}`))
		case r.URL.Path == "/repos/o/r/stargazers":
			mu.Lock()
			starPages = append(starPages, r.URL.Query().Get("page"))
			mu.Unlock()
			_, _ = w.Write([]byte("[" + stargazerEntry("alice", "2025-01-02T00:00:00Z") + "]"))
		case strings.HasPrefix(r.URL.Path, "/users/"):
			_, _ = w.Write([]byte(userJSON("alice", "Alice", "")))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	_, hasMore, total, err := fetchStargazers(context.Background(), "o", "r", "tok", 1)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 250 {
		t.Errorf("total = %d, want 250", total)
	}
	if !hasMore {
		t.Errorf("page 1: hasMore = false, want true (250 stars = 3 pages)")
	}

	_, hasMore, _, err = fetchStargazers(context.Background(), "o", "r", "tok", 3)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if hasMore {
		t.Errorf("page 3: hasMore = true, want false (oldest page)")
	}

	// Page beyond the end clamps to the oldest API page.
	_, hasMore, _, err = fetchStargazers(context.Background(), "o", "r", "tok", 99)
	if err != nil {
		t.Fatalf("page 99: %v", err)
	}
	if hasMore {
		t.Errorf("page 99: hasMore = true, want false (clamped)")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"3", "1", "1"}
	if len(starPages) != len(want) {
		t.Fatalf("stargazers pages requested = %v, want %v", starPages, want)
	}
	for i := range want {
		if starPages[i] != want[i] {
			t.Errorf("request %d: page = %s, want %s", i, starPages[i], want[i])
		}
	}
}

// --- user-detail cache & partial results --------------------------------------

// TestFetchUserDetails_Cache: repeated profile lookups hit the API once.
func TestFetchUserDetails_Cache(t *testing.T) {
	installFakeClock(t)
	var mu sync.Mutex
	calls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(userJSON("alice", "Alice", "Earth")))
	})

	for i := 0; i < 3; i++ {
		u, err := fetchUserDetails(context.Background(), "alice", "tok")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if u.Name != "Alice" || u.Location != "Earth" {
			t.Errorf("call %d: user = %+v", i, u)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("upstream /users calls = %d, want 1 (cached after first fetch)", calls)
	}
}

// TestFetchUserDetails_CacheExpiry: entries expire after userCacheTTL.
func TestFetchUserDetails_CacheExpiry(t *testing.T) {
	fc := installFakeClock(t)
	origTTL := userCacheTTL
	userCacheTTL = time.Minute
	t.Cleanup(func() { userCacheTTL = origTTL })

	var mu sync.Mutex
	calls := 0
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(userJSON("bob", "Bob", "")))
	})

	if _, err := fetchUserDetails(context.Background(), "bob", "tok"); err != nil {
		t.Fatal(err)
	}
	fc.sleep(2 * time.Minute) // advance past the TTL without a real wait
	if _, err := fetchUserDetails(context.Background(), "bob", "tok"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("upstream calls = %d, want 2 after TTL expiry", calls)
	}
}

// TestFetchStargazers_PartialResults: when one user's profile lookup keeps
// failing, the stargazer is still returned with the basic fields from the
// stargazers endpoint instead of dropping the whole page.
func TestFetchStargazers_PartialResults(t *testing.T) {
	installFakeClock(t)
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			_, _ = w.Write([]byte(`{"stargazers_count":2}`))
		case r.URL.Path == "/repos/o/r/stargazers":
			_, _ = w.Write([]byte("[" +
				stargazerEntry("alice", "2025-01-02T00:00:00Z") + "," +
				stargazerEntry("mallory", "2025-01-01T00:00:00Z") + "]"))
		case r.URL.Path == "/users/alice":
			_, _ = w.Write([]byte(userJSON("alice", "Alice", "Earth")))
		case r.URL.Path == "/users/mallory":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	stargazers, _, total, err := fetchStargazers(context.Background(), "o", "r", "tok", 1)
	if err != nil {
		t.Fatalf("fetchStargazers error = %v, want partial results instead", err)
	}
	if total != 2 || len(stargazers) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2 (partial results)", total, len(stargazers))
	}
	// newest first
	if stargazers[0].Login != "alice" || stargazers[0].Name != "Alice" {
		t.Errorf("stargazers[0] = %+v, want alice with full details", stargazers[0])
	}
	if stargazers[1].Login != "mallory" || stargazers[1].Name != "" || stargazers[1].AvatarURL == "" {
		t.Errorf("stargazers[1] = %+v, want mallory with basic fields only", stargazers[1])
	}
}

// --- token resolution (env knob) ----------------------------------------------

// TestHandleStargazers_EnvTokenFallback: with no Authorization header the
// server falls back to GITHUB_TOKEN, raising the rate limit to 5000 req/h.
func TestHandleStargazers_EnvTokenFallback(t *testing.T) {
	installFakeClock(t)
	t.Setenv("GITHUB_TOKEN", "env-token")
	t.Setenv("GH_TOKEN", "")

	var mu sync.Mutex
	var gotAuth []string
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		switch {
		case r.URL.Path == "/repos/o/r":
			_, _ = w.Write([]byte(`{"stargazers_count":0}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/github-stargazers?owner=o&repo=r&page=1", nil)
	rr := httptest.NewRecorder()
	HandleStargazers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotAuth) == 0 || gotAuth[0] != "Bearer env-token" {
		t.Errorf("Authorization headers = %v, want [Bearer env-token] from GITHUB_TOKEN env", gotAuth)
	}
}

// TestHandleStargazers_HeaderTokenWins: a per-request Authorization header
// takes precedence over the env token.
func TestHandleStargazers_HeaderTokenWins(t *testing.T) {
	installFakeClock(t)
	t.Setenv("GITHUB_TOKEN", "env-token")

	var mu sync.Mutex
	var gotAuth string
	stubGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		if r.URL.Path == "/repos/o/r" {
			_, _ = w.Write([]byte(`{"stargazers_count":0}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})

	req := httptest.NewRequest(http.MethodGet, "/github-stargazers?owner=o&repo=r&page=1", nil)
	req.Header.Set("Authorization", "Bearer header-token")
	rr := httptest.NewRecorder()
	HandleStargazers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer header-token" {
		t.Errorf("Authorization = %q, want Bearer header-token (header wins over env)", gotAuth)
	}
}
