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
	"ghealth/pkg/channel"
)

const MaxRetries = 3

// DefaultHost is the Google Health API host. The API version (channel) is
// appended as the first path segment, e.g. https://health.googleapis.com/v4.
const DefaultHost = "https://health.googleapis.com"

// BaseURL is the prefix every request path is appended to. It starts at the
// default-channel URL (honoring GHEALTH_BASE_URL) and is re-resolved by the
// root command once flags are parsed, via SetAPIVersion.
var BaseURL = ResolveBaseURL(channel.Default)

// ResolveBaseURL returns the base URL for an API version. GHEALTH_BASE_URL,
// when set, wins outright: it already carries its own version segment (e.g. a
// local fake API at http://127.0.0.1:8080/v4).
func ResolveBaseURL(apiVersion string) string {
	if v := os.Getenv("GHEALTH_BASE_URL"); v != "" {
		return v
	}
	if apiVersion == "" {
		apiVersion = channel.Default
	}
	return DefaultHost + "/" + apiVersion
}

// SetAPIVersion points the client at the given API version.
func SetAPIVersion(apiVersion string) {
	BaseURL = ResolveBaseURL(apiVersion)
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
