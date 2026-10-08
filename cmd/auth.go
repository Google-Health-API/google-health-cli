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

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"ghealth/pkg/auth"
	"ghealth/pkg/client"
	"ghealth/pkg/config"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

var (
	authLoginNonInteractive bool
	authLoginComplete       string
	authLoginScopes         string
	authLoginScopesPreset   string

	authImportFile string

	authStatusValidate bool
)

// defaultScopes are the readonly scopes requested when no scopes are specified
// and no profile is configured.
var defaultScopes = []string{
	"activity_and_fitness.readonly",
	"health_metrics_and_measurements.readonly",
	"sleep.readonly",
	"nutrition.readonly",
	"profile.readonly",
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication with Google Health API",
	Long:  "Manage OAuth credentials for accessing the Google Health API.",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Google Health API via OAuth",
	Long: `Authenticate with Google Health API via OAuth.

Interactive (default): binds an ephemeral loopback port, opens a browser, completes
the OAuth code exchange, and persists tokens to ~/.config/ghealth/credentials.json.

Non-interactive (for headless agents / hosts without a browser):

  ghealth auth login --non-interactive --scopes-preset readonly
    # Prints JSON with an auth_url and a complete_command.
    # Open auth_url on any device with a browser, authorize, then copy the 'code'
    # query parameter from the resulting URL bar.

  ghealth auth login --complete <code>
    # Exchanges the pasted code for tokens using the saved pending-auth state.

Scopes can be specified either with --scopes (a comma-separated list of scope
suffixes — see 'ghealth schema scopes') or with --scopes-preset (readonly | all |
category names like 'sleep,activity_and_fitness').`,
	RunE: runAuthLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove stored credentials",
	RunE:  runAuthLogout,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	Long: `Show current authentication status.

Without --validate, this is a fast local check: it reports what's configured
(stored creds, env token, credentials file) without making any network calls.
The "authenticated" field reflects local state only — for env-token modes it is
omitted, since presence of a token doesn't prove validity.

With --validate, the access token is verified against Google's tokeninfo
endpoint. "authenticated" then reflects actual validity, and the response
includes expires_in and scope from Google.`,
	RunE: runAuthStatus,
}

var authRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Force token refresh",
	RunE:  runAuthRefresh,
}

var authExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Print stored credentials as JSON to stdout",
	Long: `Print stored credentials as JSON to stdout.

Pair with 'ghealth auth import' to move a refresh token between machines:

  # source machine
  ghealth auth export > /tmp/ghealth-creds.json
  scp /tmp/ghealth-creds.json target:

  # target machine
  ghealth auth import --file /tmp/ghealth-creds.json`,
	RunE: runAuthExport,
}

var authImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Read stored credentials JSON from stdin (or --file) and persist it",
	Long: `Read stored credentials JSON from stdin (or --file) and persist it to
~/.config/ghealth/credentials.json so subsequent CLI invocations can use it.

The input must be the JSON produced by 'ghealth auth export' — i.e. contain
access_token, refresh_token, token_type, expiry, scopes. A client_secret must
already be configured locally (typically via 'ghealth setup') so the CLI can
refresh the token; if not, importing only the access_token still works until
that token expires.`,
	RunE: runAuthImport,
}

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd, authRefreshCmd, authExportCmd, authImportCmd)

	authLoginCmd.Flags().BoolVar(&authLoginNonInteractive, "non-interactive", false, "Print an auth URL as JSON and save pending state; complete with --complete <code>")
	authLoginCmd.Flags().StringVar(&authLoginComplete, "complete", "", "Exchange the given authorization code (paired with a prior --non-interactive call)")
	authLoginCmd.Flags().StringVar(&authLoginScopes, "scopes", "", "Comma-separated scope suffixes (see 'ghealth schema scopes')")
	authLoginCmd.Flags().StringVar(&authLoginScopesPreset, "scopes-preset", "", "Scope preset: readonly | all | comma-separated category names")

	authImportCmd.Flags().StringVar(&authImportFile, "file", "", "Read credentials JSON from this file instead of stdin")

	authStatusCmd.Flags().BoolVar(&authStatusValidate, "validate", false, "Verify the token against Google's tokeninfo endpoint (extra HTTP request)")
}

// resolveScopes returns the scopes to request, honouring (in order):
//
//	--scopes (literal list)
//	--scopes-preset (preset name)
//	the active config profile
//	defaultScopes
func resolveScopes() ([]string, error) {
	if authLoginScopes != "" {
		parts := strings.Split(authLoginScopes, ",")
		scopes := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				scopes = append(scopes, s)
			}
		}
		return scopes, nil
	}
	if authLoginScopesPreset != "" {
		return auth.ScopePreset(authLoginScopesPreset)
	}
	cfg, err := config.Load()
	if err == nil {
		profile := cfg.ActiveProfile()
		if len(profile.Scopes) > 0 {
			return profile.Scopes, nil
		}
	}
	return defaultScopes, nil
}

func fetchUserEmail(accessToken string) string {
	req, err := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var info struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(body, &info) == nil {
		return info.Email
	}
	return ""
}

func suggestNotAuthenticated() string {
	if !auth.HasClientSecret() {
		return auth.ClientSecretSetupHint
	}
	return "Run 'ghealth auth login' to authenticate"
}

// noClientSecretError returns the canonical structured error for "no OAuth
// client_secret.json configured", including the recovery checklist. Used at
// every entry point that depends on a client_secret being present.
func noClientSecretError() *client.CLIError {
	return client.NewConfigError(
		auth.ClientSecretSetupMessage,
		auth.ClientSecretSetupHint,
	).WithNextSteps(auth.ClientSecretSetupSteps())
}

// loadClientSecretOrGuide is the standard "open the client_secret" call for
// command handlers. On a missing file, it returns the canonical structured
// error with next_steps; on a malformed file, it returns the parse error with
// no next_steps (the user already has *a* file — they need to fix or replace it).
func loadClientSecretOrGuide() (*auth.ClientSecret, *client.CLIError) {
	if !auth.HasClientSecret() {
		return nil, noClientSecretError()
	}
	cs, err := auth.LoadClientSecret()
	if err != nil {
		return nil, client.NewConfigError(err.Error(), "Re-run 'ghealth setup' or repair "+config.ClientSecretPath())
	}
	return cs, nil
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
	cs, cliErr := loadClientSecretOrGuide()
	if cliErr != nil {
		return cliErr
	}

	// --complete: finish a previously-started non-interactive flow.
	if authLoginComplete != "" {
		tok, scopes, err := auth.CompleteNonInteractive(cs, authLoginComplete)
		if err != nil {
			return client.NewAuthError(err.Error(), "Re-run 'ghealth auth login --non-interactive' and try again")
		}
		email := fetchUserEmail(tok.AccessToken)
		creds := &auth.StoredCredentials{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			TokenType:    tok.TokenType,
			Expiry:       tok.Expiry,
			Scopes:       scopes,
			Email:        email,
		}
		if err := auth.SaveCredentials(creds); err != nil {
			return client.NewConfigError(fmt.Sprintf("failed to save credentials: %v", err), "")
		}
		result := map[string]interface{}{
			"status": "authenticated",
			"email":  email,
			"scopes": scopes,
			"expiry": tok.Expiry.Format(time.RFC3339),
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	scopes, err := resolveScopes()
	if err != nil {
		return client.NewValidationError(err.Error(), "Pass --scopes <comma-separated> or --scopes-preset readonly|all|<categories>")
	}

	if authLoginNonInteractive {
		authURL, _, err := auth.NonInteractiveStart(cs, scopes)
		if err != nil {
			return client.NewConfigError(err.Error(), "")
		}
		result := map[string]interface{}{
			"auth_url": authURL,
			"scopes":   scopes,
			"instructions": []string{
				"1. Open auth_url in a browser and authorize ghealth.",
				"2. The browser will redirect to a URL that may fail to load — that's expected.",
				"3. Copy the 'code' query parameter from the redirected URL.",
				"4. Run: ghealth auth login --complete <code>",
			},
			"complete_command": "ghealth auth login --complete <code>",
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	tok, err := auth.InteractiveLogin(cs, scopes)
	if err != nil {
		return client.NewAuthError(err.Error(), "Check your OAuth credentials and try again")
	}

	email := fetchUserEmail(tok.AccessToken)

	creds := &auth.StoredCredentials{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
		Scopes:       scopes,
		Email:        email,
	}

	if err := auth.SaveCredentials(creds); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to save credentials: %v", err), "")
	}

	result := map[string]interface{}{
		"status": "authenticated",
		"email":  email,
		"scopes": scopes,
		"expiry": tok.Expiry.Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runAuthLogout(cmd *cobra.Command, args []string) error {
	// Always clear any in-progress non-interactive auth state, even if no
	// stored credentials.json exists (e.g. after `setup --non-interactive-auth`
	// before the user has called `auth login --complete`). Idempotent.
	_ = auth.ClearPendingAuth()

	path := config.CredentialsPath()
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			result := map[string]string{"status": "not_authenticated", "message": "No stored credentials found; pending auth state cleared if any"}
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(os.Stdout, string(data))
			return nil
		}
		return client.NewConfigError(fmt.Sprintf("failed to remove credentials: %v", err), "")
	}

	result := map[string]string{"status": "logged_out", "message": "Stored credentials removed"}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	// Env-token mode. GHEALTH_ACCESS_TOKEN takes precedence over everything;
	// agents using it shouldn't be told to run 'ghealth setup'. We do NOT
	// claim authenticated:true without --validate, because the token might be
	// expired/revoked and we have no local expiry to check against.
	if envTok := os.Getenv("GHEALTH_ACCESS_TOKEN"); envTok != "" {
		result := map[string]interface{}{
			"auth_method": "env_token",
			"configured":  true,
		}
		if authStatusValidate {
			info, err := auth.ValidateAccessToken(context.Background(), envTok)
			if err != nil {
				result["authenticated"] = false
				result["validation_error"] = err.Error()
			} else {
				result["authenticated"] = true
				result["expires_in"] = info.ExpiresIn
				result["scope"] = info.Scope
				if info.Email != "" {
					result["email"] = info.Email
				}
			}
		} else {
			result["validated"] = false
			result["note"] = "GHEALTH_ACCESS_TOKEN is set; pass --validate to verify it against Google's tokeninfo endpoint"
		}
		addAPIVersionInfo(result)
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	// Credentials-file mode. Same posture as env-token: we know it's pointing
	// somewhere, but we can't prove validity without a network call.
	if envFile := os.Getenv("GHEALTH_CREDENTIALS_FILE"); envFile != "" {
		result := map[string]interface{}{
			"auth_method":      "credentials_file",
			"credentials_path": envFile,
			"configured":       true,
		}
		if authStatusValidate {
			// Best-effort: try to extract a current access token via the same
			// path the runtime would use, then validate it.
			fs, fsErr := auth.NewFileTokenSource()
			var tok string
			if fsErr == nil {
				tok, fsErr = fs.Token()
			}
			if fsErr != nil {
				result["authenticated"] = false
				result["validation_error"] = fsErr.Error()
			} else if info, err := auth.ValidateAccessToken(context.Background(), tok); err != nil {
				result["authenticated"] = false
				result["validation_error"] = err.Error()
			} else {
				result["authenticated"] = true
				result["expires_in"] = info.ExpiresIn
				result["scope"] = info.Scope
			}
		} else {
			result["validated"] = false
			result["note"] = "Credentials file is configured; pass --validate to verify the token"
		}
		addAPIVersionInfo(result)
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	creds, err := auth.LoadCredentials()
	if err != nil {
		// Distinguish "no client_secret at all" (fresh user, needs setup) from
		// "client_secret present but no stored tokens" (run auth login).
		if !auth.HasClientSecret() {
			return noClientSecretError()
		}
		return client.NewAuthError("Not authenticated", suggestNotAuthenticated())
	}

	expired := time.Now().After(creds.Expiry)

	result := map[string]interface{}{
		"authenticated":    !expired,
		"email":            creds.Email,
		"scopes":           creds.Scopes,
		"expiry":           creds.Expiry.Format(time.RFC3339),
		"expired":          expired,
		"auth_method":      "oauth",
		"credentials_path": config.CredentialsPath(),
	}

	if authStatusValidate {
		info, err := auth.ValidateAccessToken(context.Background(), creds.AccessToken)
		if err != nil {
			result["authenticated"] = false
			result["validation_error"] = err.Error()
		} else {
			result["authenticated"] = true
			result["expires_in"] = info.ExpiresIn
			result["scope"] = info.Scope
		}
	}

	addAPIVersionInfo(result)
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runAuthRefresh(cmd *cobra.Command, args []string) error {
	creds, err := auth.LoadCredentials()
	if err != nil {
		if !auth.HasClientSecret() {
			return noClientSecretError()
		}
		return client.NewAuthError("No stored credentials", suggestNotAuthenticated())
	}

	cs, cliErr := loadClientSecretOrGuide()
	if cliErr != nil {
		return cliErr
	}

	oauthCfg := auth.OAuthConfig(cs, creds.Scopes, "")

	tok := &oauth2.Token{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		TokenType:    creds.TokenType,
		Expiry:       time.Now().Add(-time.Hour),
	}

	src := oauthCfg.TokenSource(context.Background(), tok)
	newTok, err := src.Token()
	if err != nil {
		return client.NewAuthError(fmt.Sprintf("failed to refresh token: %v", err), "Try 'ghealth auth login' to re-authenticate")
	}

	creds.AccessToken = newTok.AccessToken
	creds.Expiry = newTok.Expiry
	if newTok.RefreshToken != "" {
		creds.RefreshToken = newTok.RefreshToken
	}

	if err := auth.SaveCredentials(creds); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to save credentials: %v", err), "")
	}

	result := map[string]interface{}{
		"status":     "refreshed",
		"new_expiry": newTok.Expiry.Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runAuthExport(cmd *cobra.Command, args []string) error {
	creds, err := auth.LoadCredentials()
	if err != nil {
		if !auth.HasClientSecret() {
			return noClientSecretError()
		}
		return client.NewAuthError("No stored credentials", suggestNotAuthenticated())
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to marshal credentials: %v", err), "")
	}

	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runAuthImport(cmd *cobra.Command, args []string) error {
	var data []byte
	var err error
	if authImportFile != "" {
		data, err = os.ReadFile(authImportFile)
		if err != nil {
			return client.NewValidationError(fmt.Sprintf("cannot read %s: %v", authImportFile, err), "")
		}
	} else {
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return client.NewValidationError(fmt.Sprintf("failed to read stdin: %v", err), "Pipe JSON to stdin or pass --file <path>")
		}
		if len(data) == 0 {
			return client.NewValidationError("no input on stdin", "Pipe JSON to stdin or pass --file <path>")
		}
	}

	var creds auth.StoredCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return client.NewValidationError(fmt.Sprintf("invalid credentials JSON: %v", err), "Input must match the shape produced by 'ghealth auth export'")
	}
	if err := creds.Validate(); err != nil {
		return client.NewValidationError(err.Error(), "")
	}

	// A refresh_token without a local client_secret.json is a dead end: the
	// CLI can use the access_token until it expires, but cannot refresh, and
	// the next API call past the access-token expiry will fail in an unclear
	// way. Fail loudly now with the bootstrap checklist so the user installs
	// the matching client_secret first.
	if creds.RefreshToken != "" && !auth.HasClientSecret() {
		return client.NewConfigError(
			"Cannot import credentials with a refresh_token: no client_secret.json is configured locally",
			"Install the OAuth client_secret first (so the CLI can refresh tokens), then re-run 'ghealth auth import'",
		).WithNextSteps(append(
			auth.ClientSecretSetupSteps(),
			"Then: ghealth auth import --file "+importInputDescription(),
		))
	}

	if err := auth.SaveCredentials(&creds); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to save credentials: %v", err), "")
	}

	result := map[string]interface{}{
		"status":           "imported",
		"email":            creds.Email,
		"scopes":           creds.Scopes,
		"expiry":           creds.Expiry.Format(time.RFC3339),
		"credentials_path": config.CredentialsPath(),
	}
	if creds.RefreshToken == "" {
		// Access-token-only imports are allowed but degraded: no refresh path,
		// so the credentials die when the token expires. Surface that.
		result["warning"] = "imported credentials have no refresh_token; CLI cannot refresh and will fail once the access token expires"
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(out))
	return nil
}

// importInputDescription returns a human-readable description of where the
// import input came from, suitable for embedding in a re-run hint.
func importInputDescription() string {
	if authImportFile != "" {
		return authImportFile
	}
	return "<path-to-creds.json>"
}
