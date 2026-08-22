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

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ghealth/pkg/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	AuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL = "https://oauth2.googleapis.com/token"
)

// ScopePrefix is the common prefix for all Health API scopes.
const ScopePrefix = "https://www.googleapis.com/auth/googlehealth."

// AllScopes lists all available Health API OAuth scope suffixes.
var AllScopes = []ScopeInfo{
	{Suffix: "activity_and_fitness.readonly", Label: "Activity & Fitness (read)", Category: "activity_and_fitness"},
	{Suffix: "activity_and_fitness", Label: "Activity & Fitness (read/write)", Category: "activity_and_fitness"},
	{Suffix: "health_metrics_and_measurements.readonly", Label: "Health Metrics (read)", Category: "health_metrics_and_measurements"},
	{Suffix: "health_metrics_and_measurements", Label: "Health Metrics (read/write)", Category: "health_metrics_and_measurements"},
	{Suffix: "sleep.readonly", Label: "Sleep (read)", Category: "sleep"},
	{Suffix: "sleep", Label: "Sleep (read/write)", Category: "sleep"},
	{Suffix: "nutrition.readonly", Label: "Nutrition (read)", Category: "nutrition"},
	{Suffix: "nutrition", Label: "Nutrition (read/write)", Category: "nutrition"},
	{Suffix: "profile.readonly", Label: "Profile (read)", Category: "profile"},
	{Suffix: "profile", Label: "Profile (read/write)", Category: "profile"},
	{Suffix: "settings.readonly", Label: "Settings (read)", Category: "settings"},
	{Suffix: "settings", Label: "Settings (read/write)", Category: "settings"},
	{Suffix: "location.readonly", Label: "Location (read)", Category: "location"},
	{Suffix: "ecg.readonly", Label: "Electrocardiogram (read)", Category: "ecg"},
	{Suffix: "irn.readonly", Label: "Irregular Rhythm Notifications (read)", Category: "irn"},
	{Suffix: "cloud-platform", Label: "Cloud Platform (manage webhook subscribers/subscriptions)", Category: "webhooks"},
}

// CloudPlatformScope is the full OAuth scope required for the project-level
// webhook subscriber/subscription endpoints. It is NOT under the googlehealth.
// prefix, so FullScope special-cases it.
const CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

type ScopeInfo struct {
	Suffix   string
	Label    string
	Category string
}

func FullScope(suffix string) string {
	// Already-qualified scope URLs pass through unchanged.
	if strings.HasPrefix(suffix, "https://") {
		return suffix
	}
	// cloud-platform is a Google-wide scope, not under the googlehealth. prefix.
	if suffix == "cloud-platform" {
		return CloudPlatformScope
	}
	return ScopePrefix + suffix
}

// ClientSecretSetupMessage is the headline used wherever no OAuth
// client_secret.json is configured. It's intentionally identical across every
// entry point that can hit this state so agents can match on it.
const ClientSecretSetupMessage = "No OAuth client_secret.json configured"

// ClientSecretSetupHint is the one-liner hint paired with ClientSecretSetupMessage.
const ClientSecretSetupHint = "Run 'ghealth setup' to create or import OAuth credentials"

// ClientSecretSetupSteps returns the copy-pastable recovery checklist for a
// missing OAuth client_secret.json. Returned as a fresh slice each time so
// callers can extend it without affecting the canonical version.
//
// This is the single source of truth: every error path that can hit "no
// client_secret" — auth login, auth login --non-interactive, auth login
// --complete, setup --no-prompt without --client-secret, auth status with no
// stored or env credentials — embeds the same steps. The setup --instructions
// command surfaces them on the success path for deliberate discovery.
func ClientSecretSetupSteps() []string {
	return []string{
		"Open https://console.cloud.google.com/apis/credentials",
		"Create or select a Google Cloud project",
		"Enable the Google Health API (https://console.cloud.google.com/apis/api/health.googleapis.com)",
		"Create OAuth client ID with Application type: Desktop app",
		"Download the client_secret JSON",
		"Run: ghealth setup --client-secret /path/to/client_secret.json",
	}
}

// HasClientSecret reports whether an OAuth client_secret.json exists at the
// configured config-dir path. Cheap stat; safe to call from error builders.
func HasClientSecret() bool {
	_, err := os.Stat(config.ClientSecretPath())
	return err == nil
}

// TokenInfo is the subset of Google's tokeninfo endpoint response that
// ValidateAccessToken returns. Returned only when the token is valid.
type TokenInfo struct {
	Audience  string `json:"audience"`
	ExpiresIn int    `json:"expires_in"`
	Scope     string `json:"scope"`
	Email     string `json:"email,omitempty"`
}

// ValidateAccessToken hits Google's tokeninfo endpoint with the given access
// token. Returns nil + non-nil error if the token is invalid/expired/revoked.
// Scope-agnostic — works with any Google OAuth access token, including
// health-only tokens that don't have userinfo scopes.
func ValidateAccessToken(ctx context.Context, accessToken string) (*TokenInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://oauth2.googleapis.com/tokeninfo?access_token="+neturl.QueryEscape(accessToken), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tokeninfo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 400 with {"error": "invalid_token"} is the normal "bad token" response.
		var apiErr struct {
			Err  string `json:"error"`
			Desc string `json:"error_description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Err != "" {
			return nil, fmt.Errorf("tokeninfo: %s (%s)", apiErr.Err, apiErr.Desc)
		}
		return nil, fmt.Errorf("tokeninfo returned HTTP %d", resp.StatusCode)
	}

	var info struct {
		Audience  string `json:"aud"`
		ExpiresIn string `json:"expires_in"` // returned as a string
		Scope     string `json:"scope"`
		Email     string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("malformed tokeninfo response: %w", err)
	}
	expires := 0
	fmt.Sscanf(info.ExpiresIn, "%d", &expires)
	return &TokenInfo{
		Audience:  info.Audience,
		ExpiresIn: expires,
		Scope:     info.Scope,
		Email:     info.Email,
	}, nil
}

// ScopePreset expands a preset name ("readonly", "all") or a comma-separated
// list of category names into scope suffixes. Returns an error for unknown
// presets/categories.
func ScopePreset(name string) ([]string, error) {
	switch strings.TrimSpace(name) {
	case "":
		return nil, fmt.Errorf("empty scope preset")
	case "readonly":
		var out []string
		for _, s := range AllScopes {
			if strings.HasSuffix(s.Suffix, ".readonly") {
				out = append(out, s.Suffix)
			}
		}
		return out, nil
	case "all":
		// cloud-platform is a webhook-admin scope that the data-plane
		// endpoints reject ("Request contains disallowed OAuth scope(s)"),
		// so "all" covers only health-data scopes. Webhook credentials need
		// their own login with --scopes cloud-platform (see 'ghealth webhooks').
		out := make([]string, 0, len(AllScopes))
		for _, s := range AllScopes {
			if s.Suffix == "cloud-platform" {
				continue
			}
			out = append(out, s.Suffix)
		}
		return out, nil
	}

	known := make(map[string][]string)
	for _, s := range AllScopes {
		known[s.Category] = append(known[s.Category], s.Suffix)
	}
	var out []string
	for _, raw := range strings.Split(name, ",") {
		c := strings.TrimSpace(raw)
		if c == "" {
			continue
		}
		matches, ok := known[c]
		if !ok {
			return nil, fmt.Errorf("unknown scope preset or category %q (valid: readonly, all, or category names like 'sleep,activity_and_fitness')", c)
		}
		readonlyFound := false
		for _, suffix := range matches {
			if strings.HasSuffix(suffix, ".readonly") {
				out = append(out, suffix)
				readonlyFound = true
			}
		}
		if !readonlyFound {
			out = append(out, matches...)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("scope preset %q resolved to no scopes", name)
	}
	return out, nil
}

// TokenSource provides access tokens for API requests.
type TokenSource interface {
	// Token returns a valid access token, refreshing if necessary.
	Token() (string, error)
}

// StoredCredentials holds the OAuth tokens persisted to disk.
type StoredCredentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
	Scopes       []string  `json:"scopes,omitempty"`
	Email        string    `json:"email,omitempty"`
}

// Validate returns an error if required fields are missing, and defaults
// TokenType to "Bearer" when omitted.
func (s *StoredCredentials) Validate() error {
	if s == nil {
		return fmt.Errorf("credentials are nil")
	}
	if s.AccessToken == "" && s.RefreshToken == "" {
		return fmt.Errorf("credentials must contain access_token or refresh_token")
	}
	if s.TokenType == "" {
		s.TokenType = "Bearer"
	}
	return nil
}

// ClientSecret holds the OAuth client configuration.
type ClientSecret struct {
	Installed *ClientConfig `json:"installed,omitempty"`
	Web       *ClientConfig `json:"web,omitempty"`
}

type ClientConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
	AuthURI      string   `json:"auth_uri,omitempty"`
	TokenURI     string   `json:"token_uri,omitempty"`
}

func (cs *ClientSecret) Config() *ClientConfig {
	if cs.Installed != nil {
		return cs.Installed
	}
	return cs.Web
}

// LoadClientSecret reads the OAuth client secret file.
func LoadClientSecret() (*ClientSecret, error) {
	path := config.ClientSecretPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read client secret: %w\nRun 'ghealth setup' to configure credentials", err)
	}

	var cs ClientSecret
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("failed to parse client secret: %w", err)
	}

	if cs.Config() == nil {
		return nil, fmt.Errorf("invalid client secret file: must contain 'installed' or 'web' key")
	}

	return &cs, nil
}

// OAuthConfig builds an oauth2.Config from the client secret.
// redirectURL overrides the URI from the client_secret JSON; pass "" to use the JSON's value.
func OAuthConfig(cs *ClientSecret, scopes []string, redirectURL string) *oauth2.Config {
	cc := cs.Config()
	if redirectURL == "" {
		redirectURL = "http://127.0.0.1"
		if len(cc.RedirectURIs) > 0 {
			redirectURL = cc.RedirectURIs[0]
		}
	}

	fullScopes := make([]string, len(scopes))
	for i, s := range scopes {
		fullScopes[i] = FullScope(s)
	}

	return &oauth2.Config{
		ClientID:     cc.ClientID,
		ClientSecret: cc.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  AuthURL,
			TokenURL: TokenURL,
		},
		RedirectURL: redirectURL,
		Scopes:      fullScopes,
	}
}

// SaveCredentials persists tokens to the credentials file.
func SaveCredentials(creds *StoredCredentials) error {
	path := config.CredentialsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// LoadCredentials reads stored tokens from the credentials file.
func LoadCredentials() (*StoredCredentials, error) {
	path := config.CredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var creds StoredCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}

	return &creds, nil
}

// KeyringTokenSource retrieves tokens from stored credentials and refreshes as needed.
type KeyringTokenSource struct {
	oauthConfig *oauth2.Config
	mu          sync.Mutex
	forced      bool
}

func NewKeyringTokenSource() (*KeyringTokenSource, error) {
	creds, err := LoadCredentials()
	if err != nil {
		return nil, err
	}

	// A client_secret is only needed to refresh. Stored credentials with a
	// still-valid access token (e.g. an access-token-only 'auth import') are
	// usable without one, so its absence must not disable this source.
	ks := &KeyringTokenSource{}
	if cs, err := LoadClientSecret(); err == nil {
		ks.oauthConfig = OAuthConfig(cs, creds.Scopes, "")
	}
	return ks, nil
}

// Invalidate marks the cached token stale so the next Token() call refreshes.
// Used by the HTTP client to recover from a 401 caused by server-side revocation
// or out-of-band expiry where local expiry is still in the future.
func (k *KeyringTokenSource) Invalidate() {
	k.mu.Lock()
	k.forced = true
	k.mu.Unlock()
}

func (k *KeyringTokenSource) Token() (string, error) {
	creds, err := LoadCredentials()
	if err != nil {
		return "", fmt.Errorf("no stored credentials: %w", err)
	}

	k.mu.Lock()
	forced := k.forced
	k.forced = false
	k.mu.Unlock()

	tok := &oauth2.Token{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		TokenType:    creds.TokenType,
		Expiry:       creds.Expiry,
	}

	if !forced && tok.Valid() {
		return tok.AccessToken, nil
	}
	if k.oauthConfig == nil {
		return "", fmt.Errorf("stored access token is expired or rejected and no client_secret.json is configured to refresh it; run 'ghealth setup' or import fresh credentials")
	}
	if forced {
		tok.Expiry = time.Now().Add(-time.Hour)
	}

	src := k.oauthConfig.TokenSource(context.Background(), tok)
	newTok, err := src.Token()
	if err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	creds.AccessToken = newTok.AccessToken
	creds.Expiry = newTok.Expiry
	rotatedRefresh := newTok.RefreshToken != "" && newTok.RefreshToken != creds.RefreshToken
	if newTok.RefreshToken != "" {
		creds.RefreshToken = newTok.RefreshToken
	}
	if err := SaveCredentials(creds); err != nil {
		// The refreshed access token is still usable for this call, so don't
		// fail the request. But warn — especially if the refresh token rotated,
		// since losing the new one breaks auth on the next invocation.
		if rotatedRefresh {
			fmt.Fprintf(os.Stderr, "warning: refresh token rotated but could not be saved (%v); "+
				"you may need to re-authenticate with 'ghealth auth login'\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "warning: failed to persist refreshed credentials: %v\n", err)
		}
	}

	return newTok.AccessToken, nil
}

// EnvTokenSource returns tokens from the GHEALTH_ACCESS_TOKEN env var.
type EnvTokenSource struct{}

func (e *EnvTokenSource) Token() (string, error) {
	tok := os.Getenv("GHEALTH_ACCESS_TOKEN")
	if tok == "" {
		return "", fmt.Errorf("GHEALTH_ACCESS_TOKEN not set")
	}
	return tok, nil
}

// ADCTokenSource uses Application Default Credentials.
type ADCTokenSource struct {
	ts oauth2.TokenSource
}

func NewADCTokenSource() (*ADCTokenSource, error) {
	ctx := context.Background()
	creds, err := google.FindDefaultCredentials(ctx,
		"https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly",
	)
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials available: %w", err)
	}
	return &ADCTokenSource{ts: creds.TokenSource}, nil
}

func (a *ADCTokenSource) Token() (string, error) {
	tok, err := a.ts.Token()
	if err != nil {
		return "", fmt.Errorf("ADC token error: %w", err)
	}
	return tok.AccessToken, nil
}

// FileTokenSource reads credentials from a JSON file (GHEALTH_CREDENTIALS_FILE).
type FileTokenSource struct {
	path string
}

func NewFileTokenSource() (*FileTokenSource, error) {
	path := os.Getenv("GHEALTH_CREDENTIALS_FILE")
	if path == "" {
		return nil, fmt.Errorf("GHEALTH_CREDENTIALS_FILE not set")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("credentials file not found: %s", path)
	}
	return &FileTokenSource{path: path}, nil
}

func (f *FileTokenSource) Token() (string, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return "", err
	}

	// Try as a service account or OAuth token file.
	ctx := context.Background()
	creds, err := google.CredentialsFromJSON(ctx, data,
		"https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly",
		"https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly",
		"https://www.googleapis.com/auth/googlehealth.sleep.readonly",
		"https://www.googleapis.com/auth/googlehealth.nutrition.readonly",
	)
	if err != nil {
		return "", fmt.Errorf("failed to parse credentials file: %w", err)
	}

	tok, err := creds.TokenSource.Token()
	if err != nil {
		return "", err
	}

	return tok.AccessToken, nil
}

// CompositeTokenSource tries multiple token sources in precedence order.
type CompositeTokenSource struct {
	sources []namedSource
}

// Invalidator is implemented by token sources that can be forced to refresh
// the cached token on the next Token() call.
type Invalidator interface {
	Invalidate()
}

type namedSource struct {
	name   string
	source TokenSource
}

// NewCompositeTokenSource creates a token source that checks in order:
// 1. GHEALTH_ACCESS_TOKEN env var
// 2. GHEALTH_CREDENTIALS_FILE env var
// 3. Stored credentials (from ghealth auth login)
// 4. Application Default Credentials
func NewCompositeTokenSource() *CompositeTokenSource {
	cts := &CompositeTokenSource{}

	// 1. Env token
	cts.sources = append(cts.sources, namedSource{
		name:   "GHEALTH_ACCESS_TOKEN",
		source: &EnvTokenSource{},
	})

	// 2. Credentials file
	if fs, err := NewFileTokenSource(); err == nil {
		cts.sources = append(cts.sources, namedSource{
			name:   "GHEALTH_CREDENTIALS_FILE",
			source: fs,
		})
	}

	// 3. Stored credentials
	if ks, err := NewKeyringTokenSource(); err == nil {
		cts.sources = append(cts.sources, namedSource{
			name:   "stored credentials",
			source: ks,
		})
	}

	// 4. ADC
	if adc, err := NewADCTokenSource(); err == nil {
		cts.sources = append(cts.sources, namedSource{
			name:   "Application Default Credentials",
			source: adc,
		})
	}

	return cts
}

func (c *CompositeTokenSource) Token() (string, error) {
	// Collect the real failure from each configured source so the eventual
	// error reports WHY auth failed (e.g. a refresh-token rejection or a
	// network failure reaching the token endpoint) instead of a generic
	// "not authenticated". The env source's only error is "var not set" —
	// that is the normal case, not a failure, so it is excluded.
	var srcErrs []error
	for _, ns := range c.sources {
		tok, err := ns.source.Token()
		if err == nil && tok != "" {
			return tok, nil
		}
		if err != nil && ns.name != "GHEALTH_ACCESS_TOKEN" {
			srcErrs = append(srcErrs, fmt.Errorf("%s: %w", ns.name, err))
		}
	}
	if len(srcErrs) > 0 {
		return "", fmt.Errorf("not authenticated: %w", errors.Join(srcErrs...))
	}
	if _, err := os.Stat(config.ClientSecretPath()); os.IsNotExist(err) {
		return "", fmt.Errorf("not authenticated. Run 'ghealth setup' to configure OAuth credentials, or set GHEALTH_ACCESS_TOKEN")
	}
	return "", fmt.Errorf("not authenticated. Run 'ghealth auth login' or set GHEALTH_ACCESS_TOKEN")
}

// Invalidate forwards Invalidate() to any underlying source that supports it,
// so a 401 retry can force a token refresh.
func (c *CompositeTokenSource) Invalidate() {
	for _, ns := range c.sources {
		if inv, ok := ns.source.(Invalidator); ok {
			inv.Invalidate()
		}
	}
}

// InteractiveLogin performs the OAuth 2.0 authorization code flow with a local server.
// It binds an ephemeral loopback port and overrides the OAuth redirect URI to match,
// so the GCP OAuth client doesn't need a pre-registered port. Works for Desktop OAuth
// clients (Google permits arbitrary loopback redirects).
func InteractiveLogin(cs *ClientSecret, scopes []string) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind loopback port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/", port)

	cfg := OAuthConfig(cs, scopes, redirectURL)

	state, err := randomState()
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("state"); got != state {
			errCh <- fmt.Errorf("state mismatch")
			fmt.Fprint(w, "<html><body><h2>Error: state mismatch</h2></body></html>")
			return
		}
		if e := r.URL.Query().Get("error"); e != "" {
			errCh <- fmt.Errorf("OAuth error: %s", e)
			fmt.Fprintf(w, "<html><body><h2>Authorization failed: %s</h2></body></html>", e)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code received")
			fmt.Fprint(w, "<html><body><h2>Error: No authorization code received</h2></body></html>")
			return
		}
		codeCh <- code
		fmt.Fprint(w, "<html><body><h2>Authentication successful</h2><p>You can close this window.</p></body></html>")
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Fprintf(os.Stderr, "\nOpen this URL in your browser to authenticate:\n\n  %s\n\nWaiting for authorization on %s ...\n", authURL, redirectURL)
	openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		_ = srv.Close()
		return nil, err
	}

	_ = srv.Close()

	tok, err := cfg.Exchange(context.Background(), code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	return tok, nil
}

// NonInteractiveStart generates an auth URL and persists the state + PKCE
// verifier needed for CompleteNonInteractive. The redirect URL comes from the
// client_secret JSON — after the user authorizes in their browser, the
// redirect typically fails to load on the user's machine (no server
// listening), but the authorization code is visible in the resulting URL bar.
// The user pastes either the raw code OR the full redirected URL to
// `ghealth auth login --complete <code-or-url>`.
//
// The flow is hardened with:
//   - PKCE (S256) — code_challenge sent in the auth URL, code_verifier kept
//     locally and sent on token exchange. Protects against authorization-code
//     interception even if the code leaks (e.g. shoulder-surfing the URL bar).
//   - state validation — random URL-safe token in the auth URL, must match on
//     the redirected URL when CompleteNonInteractive parses one.
func NonInteractiveStart(cs *ClientSecret, scopes []string) (authURL string, pending *PendingAuth, err error) {
	cfg := OAuthConfig(cs, scopes, "")

	state, err := randomState()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate state: %w", err)
	}
	verifier, err := generateCodeVerifier()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	challenge := codeChallengeS256(verifier)

	pending = &PendingAuth{
		State:        state,
		CodeVerifier: verifier,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       scopes,
		CreatedAt:    time.Now(),
	}
	if err := SavePendingAuth(pending); err != nil {
		return "", nil, fmt.Errorf("failed to save pending auth state: %w", err)
	}

	authURL = cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return authURL, pending, nil
}

// CompleteNonInteractive exchanges the user-pasted code (or full redirect URL)
// for a token, validating state and using the persisted PKCE verifier. The
// state file is removed on success — and on a state-mismatch failure, since
// the pending flow is no longer recoverable.
//
// Accepts either of:
//   - a bare authorization code: "4/0AX4XfWh..."
//   - the full redirect URL with code+state query params, e.g.
//     "http://127.0.0.1:43215/?code=4/...&state=abc&scope=...". Useful because
//     users often have the whole URL in their browser bar and don't know which
//     part to copy.
func CompleteNonInteractive(cs *ClientSecret, codeOrURL string) (*oauth2.Token, []string, error) {
	pending, err := LoadPendingAuth()
	if err != nil {
		return nil, nil, fmt.Errorf("no pending auth state: %w (run 'ghealth auth login --non-interactive' first)", err)
	}

	code, gotState, err := parseCodeInput(codeOrURL)
	if err != nil {
		return nil, nil, err
	}

	// State validation. Only enforced when the user pasted a URL that contains
	// a state param; a bare code paste skips this check (the pending file's
	// state is still consumed so a stale pending flow can't be reused).
	if gotState != "" && gotState != pending.State {
		_ = ClearPendingAuth()
		return nil, nil, fmt.Errorf("OAuth state mismatch (got %q, expected %q) — pending auth cleared; re-run 'ghealth auth login --non-interactive'", gotState, pending.State)
	}

	cfg := OAuthConfig(cs, pending.Scopes, pending.RedirectURL)
	tok, err := cfg.Exchange(context.Background(), code,
		oauth2.SetAuthURLParam("code_verifier", pending.CodeVerifier),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to exchange code: %w", err)
	}

	_ = ClearPendingAuth()
	return tok, pending.Scopes, nil
}

// parseCodeInput extracts the authorization code (and state, if present) from
// either a bare code string or a full redirect URL. The OAuth `code` param can
// contain `/` and `=` characters but no whitespace; a leading `http` is the
// signal that the input is a URL.
func parseCodeInput(input string) (code, state string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty authorization code")
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		u, err := neturl.Parse(input)
		if err != nil {
			return "", "", fmt.Errorf("could not parse redirect URL: %w", err)
		}
		q := u.Query()
		if e := q.Get("error"); e != "" {
			return "", "", fmt.Errorf("OAuth provider returned error: %s", e)
		}
		code = q.Get("code")
		if code == "" {
			return "", "", fmt.Errorf("redirect URL has no 'code' query parameter")
		}
		state = q.Get("state")
		return code, state, nil
	}
	return input, "", nil
}

// PendingAuth holds the state of an in-progress non-interactive auth flow.
type PendingAuth struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	RedirectURL  string    `json:"redirect_url"`
	Scopes       []string  `json:"scopes"`
	CreatedAt    time.Time `json:"created_at"`
}

// PendingAuthPath is where the in-progress auth state is stored.
func PendingAuthPath() string {
	return filepath.Join(config.ConfigDir(), "pending_auth.json")
}

func SavePendingAuth(p *PendingAuth) error {
	path := PendingAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func LoadPendingAuth() (*PendingAuth, error) {
	data, err := os.ReadFile(PendingAuthPath())
	if err != nil {
		return nil, err
	}
	var p PendingAuth
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func ClearPendingAuth() error {
	if err := os.Remove(PendingAuthPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// openBrowser launches the platform's default browser at url, best-effort.
// Callers must still print the URL: on headless hosts (or unknown platforms)
// this fails silently and the user falls back to copy-paste.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateCodeVerifier returns a high-entropy URL-safe PKCE code verifier per
// RFC 7636 §4.1 (43-128 chars from [A-Z][a-z][0-9]-._~). 32 random bytes
// base64url-encoded produces a 43-character verifier.
func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallengeS256 returns the S256 code challenge for a verifier per
// RFC 7636 §4.2: base64url(sha256(verifier)), no padding.
func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
