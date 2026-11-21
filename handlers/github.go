package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	cu "github.com/keploy/gitstats/common"
)

// Tunables for the rate-limit-aware GitHub API client. They are vars (not
// consts) so tests can shrink them; production always uses the defaults.
var (
	// maxAPIRetries bounds how many times a transient failure (rate limit,
	// 5xx, transport error) is retried before giving up.
	maxAPIRetries = 3
	// baseBackoff is the delay before the first retry of a transient failure;
	// it doubles on every subsequent attempt up to maxBackoff.
	baseBackoff = 1 * time.Second
	// maxBackoff caps the exponential backoff between retries.
	maxBackoff = 30 * time.Second
	// maxRateLimitWait caps how long a single rate-limit wait may stall a
	// request. When GitHub asks us to wait longer than this (Retry-After or
	// X-RateLimit-Reset further away) we give up immediately and report the
	// limit via a *RateLimitError instead of hanging the request for an hour.
	maxRateLimitWait = 60 * time.Second
)

// Injectable clock/sleep so tests can exercise backoff without real waiting.
var (
	sleepFunc = time.Sleep
	nowFunc   = time.Now
)

// rateLimitMu guards rateLimitedUntil, a process-wide circuit breaker: once a
// rate limit with a long wait is observed, subsequent API calls fail fast
// until the limit resets instead of each paying the full retry cost.
var (
	rateLimitMu      sync.Mutex
	rateLimitedUntil time.Time
)

// RateLimitError is returned when the GitHub API primary rate limit is
// exhausted and the wait GitHub asks for exceeds maxRateLimitWait (or the
// retry budget is spent). Handlers map it to HTTP 429 + Retry-After.
type RateLimitError struct {
	Limit      int           // X-RateLimit-Limit
	Remaining  int           // X-RateLimit-Remaining
	Reset      time.Time     // X-RateLimit-Reset, zero if unknown
	RetryAfter time.Duration // how long the caller should wait before retrying
}

func (e *RateLimitError) Error() string {
	reset := ""
	if !e.Reset.IsZero() {
		reset = fmt.Sprintf(", resets at %s", e.Reset.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("GitHub API rate limit exceeded (limit: %d, remaining: %d%s): retry after %s. "+
		"Supply a GitHub token (Authorization: Bearer <token> header or GITHUB_TOKEN env var) to raise the limit to 5000 requests/hour",
		e.Limit, e.Remaining, reset, e.RetryAfter.Round(time.Second))
}

// rateLimitWait inspects a response and reports whether it is a primary
// rate-limit response and, if so, how long GitHub asks us to wait.
// A 429 is always a rate limit; a 403 is only treated as a rate limit when
// X-RateLimit-Remaining is 0 — a 403 with remaining quota (bad token, abuse
// limit, ...) must fail immediately without retrying.
func rateLimitWait(resp *http.Response) (wait time.Duration, reset time.Time, limited bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, time.Time{}, false
	}
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	if resp.StatusCode == http.StatusForbidden && remaining != "0" {
		return 0, time.Time{}, false
	}

	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Duration(secs) * time.Second, reset, true
		}
		if t, err := http.ParseTime(ra); err == nil {
			return t.Sub(nowFunc()), reset, true
		}
	}
	if rs := resp.Header.Get("X-RateLimit-Reset"); rs != "" {
		if ts, err := strconv.ParseInt(rs, 10, 64); err == nil {
			reset = time.Unix(ts, 0)
			return reset.Sub(nowFunc()), reset, true
		}
	}
	return 0, reset, true
}

func parseRateLimitHeaders(h http.Header) (limit, remaining int) {
	limit, _ = strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, _ = strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	return limit, remaining
}

// noteRateLimited records a rate-limit hit so later calls fail fast until the
// limit resets (circuit breaker).
func noteRateLimited(d time.Duration) {
	if d <= 0 {
		return
	}
	rateLimitMu.Lock()
	until := nowFunc().Add(d)
	if until.After(rateLimitedUntil) {
		rateLimitedUntil = until
	}
	rateLimitMu.Unlock()
}

// rateLimitActive reports how long the circuit breaker still blocks new calls.
func rateLimitActive() time.Duration {
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()
	return rateLimitedUntil.Sub(nowFunc())
}

// backoff returns the exponential backoff delay for the given retry attempt.
func backoff(attempt int) time.Duration {
	d := baseBackoff
	for i := 0; i < attempt && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// githubAPIGet performs a GET against the GitHub REST API with rate-limit
// aware retry and bounded exponential backoff.
//
//   - 2xx: returns the response body (nil for 204) and headers.
//   - 403/429 with X-RateLimit-Remaining: 0: honours Retry-After /
//     X-RateLimit-Reset, sleeping up to maxRateLimitWait before retrying.
//     If the required wait is longer, or retries are exhausted, a
//     *RateLimitError is returned immediately instead of stalling.
//   - 5xx / transport errors: retried with bounded exponential backoff.
//   - any other non-2xx: returned as an error including the response body.
//
// An empty token means an unauthenticated request (60 req/h); pass a token to
// raise the limit to 5000 req/h.
func githubAPIGet(ctx context.Context, token, accept, rawURL string) ([]byte, http.Header, error) {
	if accept == "" {
		accept = "application/vnd.github.v3+json"
	}
	if d := rateLimitActive(); d > 0 {
		// Circuit breaker open: don't hammer the API while we know it is
		// rate limited; report immediately so callers can surface a 429.
		return nil, nil, &RateLimitError{RetryAfter: d}
	}

	client := &http.Client{Timeout: githubHTTPClientTimeout}

	var lastErr error
	for attempt := 0; attempt <= maxAPIRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating request: %v", err)
		}
		req.Header.Add("Accept", accept)
		if token != "" {
			req.Header.Add("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("error making request: %v", err)
			if attempt == maxAPIRetries {
				return nil, nil, lastErr
			}
			sleepFunc(backoff(attempt))
			continue
		}

		wait, reset, limited := rateLimitWait(resp)
		limit, remaining := parseRateLimitHeaders(resp.Header)
		if limited {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if wait <= 0 {
				wait = backoff(attempt)
			}
			if wait > maxRateLimitWait || attempt == maxAPIRetries {
				noteRateLimited(wait)
				return nil, nil, &RateLimitError{
					Limit:      limit,
					Remaining:  remaining,
					Reset:      reset,
					RetryAfter: wait,
				}
			}
			sleepFunc(wait)
			continue
		}

		if resp.StatusCode >= 500 {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("GitHub API returned status: %d", resp.StatusCode)
			if attempt == maxAPIRetries {
				return nil, nil, lastErr
			}
			sleepFunc(backoff(attempt))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, nil, fmt.Errorf("GitHub API returned status: %d, body: %s", resp.StatusCode, string(body))
		}

		var body []byte
		if resp.StatusCode != http.StatusNoContent {
			body, err = io.ReadAll(resp.Body)
			if err != nil {
				resp.Body.Close()
				return nil, nil, fmt.Errorf("error reading response: %v", err)
			}
		}
		hdr := resp.Header
		resp.Body.Close()
		return body, hdr, nil
	}
	return nil, nil, lastErr
}

// githubTokenFromEnv returns a server-side GitHub token used to raise the API
// rate limit (60 req/h unauthenticated -> 5000 req/h authenticated). Set
// GITHUB_TOKEN (or GH_TOKEN as a fallback) in the deployment environment.
func githubTokenFromEnv() string {
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

// resolveToken picks the token for an incoming request: a per-request
// Authorization header wins, otherwise the server-wide GITHUB_TOKEN env var
// is used so large repositories don't exhaust the anonymous 60 req/h quota.
func resolveToken(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return githubTokenFromEnv()
}

// configFromRequest builds a *cu.Config from the request (Authorization
// header token, falling back to GITHUB_TOKEN/GH_TOKEN env). It returns nil
// when no token is available so callers keep working unauthenticated.
func configFromRequest(r *http.Request) *cu.Config {
	if t := resolveToken(r); t != "" {
		return &cu.Config{GithubToken: t}
	}
	return nil
}

// writeAPIError maps an upstream GitHub error to an HTTP response: rate-limit
// exhaustion becomes 429 with a Retry-After header and a clear message,
// everything else stays a 500.
func writeAPIError(w http.ResponseWriter, err error) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		retryAfter := int(rl.RetryAfter.Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		http.Error(w, rl.Error(), http.StatusTooManyRequests)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// userCacheTTL is how long fetched user profiles are kept in memory. The
// stargazers endpoint makes one /users/<login> call per stargazer per page;
// names/locations change rarely, so caching them for an hour removes ~100
// API calls per page reload for large repositories.
var userCacheTTL = time.Hour

type cachedUser struct {
	user    *cu.User
	expires time.Time
}

var (
	userCacheMu sync.Mutex
	userCache   = make(map[string]cachedUser)
)

func cachedUserDetails(username string) (*cu.User, bool) {
	userCacheMu.Lock()
	defer userCacheMu.Unlock()
	entry, ok := userCache[username]
	if !ok || nowFunc().After(entry.expires) {
		return nil, false
	}
	return entry.user, true
}

func storeUserDetails(username string, u *cu.User) {
	userCacheMu.Lock()
	defer userCacheMu.Unlock()
	userCache[username] = cachedUser{user: u, expires: nowFunc().Add(userCacheTTL)}
}
