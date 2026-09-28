// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ghealth/internal/version"
	"ghealth/pkg/auth"
)

const MaxRetries = 3

var BaseURL = resolveBaseURL()

const defaultBaseURL = "https://health.googleapis.com/v4"

// allowCustomBaseURLEnv must be set to "1" to permit a GHEALTH_BASE_URL that
// is neither a Google API host nor a loopback address.
const allowCustomBaseURLEnv = "GHEALTH_ALLOW_CUSTOM_BASE_URL"

// resolveBaseURL returns the API base URL. GHEALTH_BASE_URL may override it,
// but because every request carries the user's OAuth access token, overrides
// are restricted to https://*.googleapis.com and loopback hosts (for local
// testing) unless GHEALTH_ALLOW_CUSTOM_BASE_URL=1 is also set. A rejected
// override falls back to the default and prints a warning to stderr.
func resolveBaseURL() string {
	v := os.Getenv("GHEALTH_BASE_URL")
	if v == "" {
		return defaultBaseURL
	}
	if err := validateBaseURL(v, os.Getenv(allowCustomBaseURLEnv) == "1"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring GHEALTH_BASE_URL: %v; using %s\n", err, defaultBaseURL)
		return defaultBaseURL
	}
	if !isTrustedHost(v) {
		fmt.Fprintf(os.Stderr, "warning: GHEALTH_BASE_URL points to non-Google host %q; your OAuth access token will be sent there\n", v)
	}
	return v
}

// validateBaseURL reports whether raw is an acceptable API base URL.
func validateBaseURL(raw string, allowCustom bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not an absolute URL", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if isTrustedHost(raw) || allowCustom {
		return nil
	}
	return fmt.Errorf("host %q is not *.googleapis.com or loopback (set %s=1 to allow)", u.Hostname(), allowCustomBaseURLEnv)
}

// isTrustedHost is true for https://*.googleapis.com and for loopback hosts.
func isTrustedHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "https" && (host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com")) {
		return true
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// sleep is swappable so tests can run the retry loop without real backoff.
var sleep = time.Sleep

type Client struct {
	httpClient  *http.Client
	tokenSource auth.TokenSource
}

func New(ts auth.TokenSource) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		tokenSource: ts,
	}
}

// Request represents an API request to be executed.
// Body is []byte so it can be re-read across retries safely.
type Request struct {
	Method      string
	Path        string // relative to BaseURL, e.g., "/users/me/dataTypes/steps/dataPoints"
	Query       url.Values
	Body        []byte
	ContentType string
}

// Response wraps the raw API response.
type Response struct {
	StatusCode int
	Body       json.RawMessage
	Headers    http.Header
}

// Do executes an API request with auth, retry, and error handling.
func (c *Client) Do(req *Request) (*Response, error) {
	var lastErr error
	var nextDelay time.Duration // server-requested delay (Retry-After) for the next attempt
	refreshed := false          // whether we've already force-refreshed the token

	for attempt := 0; attempt <= MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if nextDelay > backoff {
				backoff = nextDelay // honor Retry-After when it's longer than our backoff
			}
			sleep(backoff)
		}
		nextDelay = 0

		resp, err := c.doOnce(req)
		if err != nil {
			// Token acquisition failures are deterministic — retrying with
			// backoff cannot help. Fail fast. A transport-level failure
			// reaching the token endpoint is a network problem (exit 4) —
			// re-login would fail the same way; everything else is an auth
			// problem (exit 2) with the documented recovery hint.
			var authErr *AuthError
			if errors.As(err, &authErr) {
				if isAuthNetworkError(err) {
					return nil, NewNetworkError(fmt.Sprintf("could not reach the OAuth token endpoint: %v", authErr.Err))
				}
				return nil, NewAuthError(authErr.Error(), "Run 'ghealth auth login' to re-authenticate")
			}
			lastErr = err
			// Network errors: retry
			continue
		}

		// Success
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		// 401: force-refresh the token once (in case it was revoked, or expired
		// mid-retry-sequence while the local expiry was still in the future) and
		// retry. Gated on a flag rather than attempt==0 so a 401 arriving after
		// earlier 5xx/429 retries still triggers a refresh.
		if resp.StatusCode == 401 && !refreshed {
			refreshed = true
			if inv, ok := c.tokenSource.(auth.Invalidator); ok {
				inv.Invalidate()
			}
			lastErr = parseAPIError(resp)
			continue
		}

		// 403 "insufficient authentication scopes": an access token can pass the
		// local validity check yet already be expired server-side, because the
		// oauth2 library treats a token as valid until ~10s before its expiry.
		// Within that skew window the API may return this 403 rather than a 401
		// (observed live: the first call after expiry returned it; the next
		// succeeded). A genuine scope gap reproduces identically after a refresh,
		// so one refresh+retry distinguishes a stale token from a real policy
		// denial without masking the latter.
		if resp.StatusCode == 403 && !refreshed && isStaleScopeError(resp) {
			refreshed = true
			if inv, ok := c.tokenSource.(auth.Invalidator); ok {
				inv.Invalidate()
			}
			lastErr = parseAPIError(resp)
			continue
		}

		// 429: honor Retry-After, retry
		if resp.StatusCode == 429 {
			nextDelay = parseRetryAfter(resp.Headers)
			lastErr = parseAPIError(resp)
			continue
		}

		// 5xx: retry (also honor Retry-After if the server sent one)
		if resp.StatusCode >= 500 {
			nextDelay = parseRetryAfter(resp.Headers)
			lastErr = parseAPIError(resp)
			continue
		}

		// 4xx (not 401/429): don't retry
		return resp, parseAPIError(resp)
	}

	// Retries exhausted. An HTTP-status error keeps its API classification
	// (exit 1, e.g. persistent 429/5xx); anything else never got an HTTP
	// response and is a network problem (exit 4).
	var cliErr *CLIError
	if errors.As(lastErr, &cliErr) {
		return nil, cliErr
	}
	return nil, NewNetworkError(fmt.Sprintf("request failed after %d attempts: %v", MaxRetries+1, lastErr))
}

// isStaleScopeError reports whether a 403 body is the "insufficient
// authentication scopes" variant (ACCESS_TOKEN_SCOPE_INSUFFICIENT) — the form a
// stale access token produces — rather than a policy denial. Only this variant
// earns a one-shot refresh+retry.
func isStaleScopeError(resp *Response) bool {
	body := string(resp.Body)
	return strings.Contains(body, "ACCESS_TOKEN_SCOPE_INSUFFICIENT") ||
		strings.Contains(strings.ToLower(body), "insufficient authentication scopes")
}

// parseRetryAfter returns the delay requested by a Retry-After header, in either
// delta-seconds ("120") or HTTP-date form. Returns 0 when absent or unparseable.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) doOnce(req *Request) (*Response, error) {
	fullURL := BaseURL + req.Path
	if len(req.Query) > 0 {
		fullURL += "?" + req.Query.Encode()
	}

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequest(req.Method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add auth header.
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, &AuthError{Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("x-goog-api-client", "ghealth/"+version.Version)

	if req.ContentType != "" {
		httpReq.Header.Set("Content-Type", req.ContentType)
	} else if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       json.RawMessage(body),
		Headers:    resp.Header,
	}, nil
}

// DryRun returns the request details as JSON without executing.
func (c *Client) DryRun(req *Request) (json.RawMessage, error) {
	fullURL := BaseURL + req.Path
	if len(req.Query) > 0 {
		fullURL += "?" + req.Query.Encode()
	}

	params := make(map[string]string)
	for k, v := range req.Query {
		params[k] = strings.Join(v, ",")
	}

	dryRun := map[string]interface{}{
		"method": req.Method,
		"url":    fullURL,
		"headers": map[string]string{
			"Authorization":     "Bearer [REDACTED]",
			"x-goog-api-client": "ghealth/" + version.Version,
			"Content-Type":      "application/json",
		},
	}
	if len(params) > 0 {
		dryRun["params"] = params
	}
	if len(req.Body) > 0 {
		dryRun["body"] = json.RawMessage(req.Body)
	}

	data, err := json.MarshalIndent(dryRun, "", "  ")
	return json.RawMessage(data), err
}
