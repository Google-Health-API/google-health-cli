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
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ghealth/pkg/auth"
	"ghealth/pkg/client"
	"ghealth/pkg/config"
	"github.com/spf13/cobra"
)

var (
	setupProjectID          string
	setupClientSecret       string
	setupScopes             string
	setupScopesPreset       string
	setupNoPrompt           bool
	setupSkipEnable         bool
	setupNonInteractiveAuth bool
	setupInstructions       bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Guided first-time setup wizard",
	Long: `Interactive wizard that takes you from zero to authenticated in one command.

Steps:
  1. GCP project ID (create at https://console.cloud.google.com/projectcreate)
  2. OAuth client_secret JSON (Desktop application; download from the GCP Console
     credentials page). Stored at ~/.config/ghealth/client_secret.json.
  3. Enable the Google Health API (via gcloud if available, otherwise a manual link).
  4. Scope selection — defaults to all readonly scopes.
  5. Browser-based OAuth login — binds an ephemeral loopback port; tokens stored
     at ~/.config/ghealth/credentials.json (plaintext JSON, mode 0600).
  6. Save the active profile to ~/.config/ghealth/config.toml.

Flags let agents drive the wizard non-interactively:

  ghealth setup \
    --project-id my-project \
    --client-secret ~/Downloads/client_secret_123.json \
    --scopes-preset readonly \
    --skip-enable-api \
    --no-prompt

With --no-prompt the wizard fails on any missing input rather than asking, so it
can run inside automation. Use --non-interactive-auth to additionally skip the
browser step (you'll then need to run 'ghealth auth login --complete <code>').

For agents that want to fetch the bootstrap checklist deliberately (before any
auth call would fail), use --instructions:

  ghealth setup --instructions
    # exit 0; prints a JSON object with the same next_steps that auth errors emit`,
	RunE: runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
	setupCmd.Flags().StringVar(&setupProjectID, "project-id", "", "GCP project ID (skips the prompt)")
	setupCmd.Flags().StringVar(&setupClientSecret, "client-secret", "", "Path to the downloaded OAuth client_secret JSON (skips the prompt)")
	setupCmd.Flags().StringVar(&setupScopes, "scopes", "", "Comma-separated scope suffixes (overrides --scopes-preset)")
	setupCmd.Flags().StringVar(&setupScopesPreset, "scopes-preset", "", "Scope preset: readonly | all | comma-separated category names")
	setupCmd.Flags().BoolVar(&setupNoPrompt, "no-prompt", false, "Fail if any required input is missing instead of prompting")
	setupCmd.Flags().BoolVar(&setupSkipEnable, "skip-enable-api", false, "Skip the 'Enable Health API' step (assume it's already enabled)")
	setupCmd.Flags().BoolVar(&setupNonInteractiveAuth, "non-interactive-auth", false, "Skip browser-based OAuth login; complete later with 'ghealth auth login --complete <code>'")
	setupCmd.Flags().BoolVar(&setupInstructions, "instructions", false, "Print the OAuth client_secret bootstrap checklist as JSON and exit 0 (no wizard)")
}

// ─── UI helpers ──────────────────────────────────────────────────

const (
	dim    = "\033[2m"
	bold   = "\033[1m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	reset  = "\033[0m"
	check  = "\033[32m✓\033[0m"
	arrow  = "\033[36m→\033[0m"
)

func header(step int, total int, title string) {
	fmt.Fprintf(os.Stderr, "\n%s[%d/%d]%s %s%s%s\n", dim, step, total, reset, bold, title, reset)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", dim, strings.Repeat("─", 50), reset)
}

func info(msg string) {
	fmt.Fprintf(os.Stderr, "  %s %s\n", arrow, msg)
}

func success(msg string) {
	fmt.Fprintf(os.Stderr, "  %s %s\n", check, msg)
}

func warn(msg string) {
	fmt.Fprintf(os.Stderr, "  %s!%s %s\n", yellow, reset, msg)
}

func blank() {
	fmt.Fprintln(os.Stderr)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func promptInput(reader *bufio.Reader, prompt, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(os.Stderr, "  %s [%s%s%s]: ", prompt, dim, defaultVal, reset)
	} else {
		fmt.Fprintf(os.Stderr, "  %s: ", prompt)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

func promptConfirm(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "  %s [%s]: ", prompt, hint)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

// ─── Setup wizard ────────────────────────────────────────────────

func runSetup(cmd *cobra.Command, args []string) error {
	// --instructions: deliberate discovery path for agents. Emits the same
	// next_steps that auth errors would emit, on stdout, with exit 0.
	if setupInstructions {
		result := map[string]interface{}{
			"status":             "instructions",
			"message":            auth.ClientSecretSetupMessage + " (or none yet); follow next_steps to obtain one.",
			"next_steps":         auth.ClientSecretSetupSteps(),
			"client_secret_path": config.ClientSecretPath(),
			"docs":               "https://console.cloud.google.com/apis/credentials",
			"oauth_client_type":  "Desktop app",
			"complete_command":   "ghealth setup --client-secret /path/to/client_secret.json",
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(os.Stdout, string(data))
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	totalSteps := 6

	fmt.Fprintf(os.Stderr, "\n  %sghealth Setup%s\n", bold, reset)
	fmt.Fprintf(os.Stderr, "  %s%s%s\n", dim, "Configure GCP project, OAuth credentials, and authenticate", reset)
	blank()

	// ─── Step 1: GCP Project ID ──────────────────────────────────

	header(1, totalSteps, "GCP Project")
	projectID := setupProjectID
	if projectID == "" {
		if setupNoPrompt {
			return client.NewValidationError("--no-prompt set but no --project-id provided", "Pass --project-id <id> or run setup interactively")
		}
		info("You need a Google Cloud project with the Health API enabled.")
		info(fmt.Sprintf("Create one at: %shttps://console.cloud.google.com/projectcreate%s", cyan, reset))
		blank()
		projectID = promptInput(reader, "GCP project ID", "")
		if projectID == "" {
			return client.NewValidationError("project ID is required", "Create a project at https://console.cloud.google.com/projectcreate")
		}
	}
	success(fmt.Sprintf("Project: %s%s%s", bold, projectID, reset))

	// ─── Step 2: OAuth credentials ───────────────────────────────

	header(2, totalSteps, "OAuth Credentials")
	secretPath := setupClientSecret
	useExistingSecret := false
	if secretPath == "" {
		if setupNoPrompt {
			// A previously-installed client_secret satisfies this step; only a
			// truly missing one is the documented bootstrap state (config, exit
			// 5, with next_steps) — not a validation error.
			if !auth.HasClientSecret() {
				return noClientSecretError()
			}
			useExistingSecret = true
		}
	}
	if secretPath == "" && !useExistingSecret {
		info("Create OAuth credentials in your GCP project:")
		blank()
		fmt.Fprintf(os.Stderr, "  %s1.%s Open %s%shttps://console.cloud.google.com/apis/credentials?project=%s%s\n", bold, reset, cyan, dim, projectID, reset)
		fmt.Fprintf(os.Stderr, "  %s2.%s Click %sCreate Credentials%s > %sOAuth client ID%s\n", bold, reset, bold, reset, bold, reset)
		fmt.Fprintf(os.Stderr, "  %s3.%s Application type: %sDesktop app%s\n", bold, reset, bold, reset)
		fmt.Fprintf(os.Stderr, "  %s4.%s Download the JSON file\n", bold, reset)
		blank()
		secretPath = promptInput(reader, "Path to downloaded client_secret JSON", "")
		if secretPath == "" {
			return client.NewValidationError("client secret file path is required", "Download the OAuth client secret JSON from the GCP Console")
		}
	}

	if useExistingSecret {
		success(fmt.Sprintf("Using existing client secret at %s%s%s", dim, config.ClientSecretPath(), reset))
	} else {
		if strings.HasPrefix(secretPath, "~/") {
			home, _ := os.UserHomeDir()
			secretPath = filepath.Join(home, secretPath[2:])
		}

		srcData, err := os.ReadFile(secretPath)
		if err != nil {
			return client.NewValidationError(fmt.Sprintf("cannot read file: %v", err), "Check the file path and try again")
		}

		destPath := config.ClientSecretPath()
		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0700); err != nil {
			return client.NewConfigError(fmt.Sprintf("failed to create config directory: %v", err), "")
		}
		if err := os.WriteFile(destPath, srcData, 0600); err != nil {
			return client.NewConfigError(fmt.Sprintf("failed to copy client secret: %v", err), "")
		}
		success(fmt.Sprintf("Credentials saved to %s%s%s", dim, destPath, reset))
	}

	// ─── Step 3: Enable Health API ───────────────────────────────

	header(3, totalSteps, "Enable Health API")

	switch {
	case setupSkipEnable:
		info("--skip-enable-api set; assuming Health API is already enabled")
	default:
		if _, err := exec.LookPath("gcloud"); err == nil {
			info("Found gcloud, enabling Health API...")
			enableCmd := exec.Command("gcloud", "services", "enable", "health.googleapis.com", "--project", projectID)
			enableCmd.Stderr = os.Stderr
			if err := enableCmd.Run(); err != nil {
				warn(fmt.Sprintf("Could not enable via gcloud: %v", err))
				info(fmt.Sprintf("Enable manually: %shttps://console.cloud.google.com/apis/api/health.googleapis.com?project=%s%s", cyan, projectID, reset))
				if !setupNoPrompt {
					blank()
					promptInput(reader, "Press Enter when done", "")
				}
			} else {
				success("Health API enabled")
			}
		} else {
			info("gcloud not found. Enable the Health API manually:")
			info(fmt.Sprintf("%shttps://console.cloud.google.com/apis/api/health.googleapis.com?project=%s%s", cyan, projectID, reset))
			if setupNoPrompt {
				warn("--no-prompt set; continuing without confirmation (re-run with --skip-enable-api to silence this)")
			} else {
				blank()
				promptInput(reader, "Press Enter when done", "")
			}
			success("Continuing")
		}
	}

	// ─── Step 4: Scope selection ─────────────────────────────────

	header(4, totalSteps, "Select Scopes")

	var selectedScopes []string
	switch {
	case setupScopes != "":
		for _, part := range strings.Split(setupScopes, ",") {
			if s := strings.TrimSpace(part); s != "" {
				selectedScopes = append(selectedScopes, s)
			}
		}
		info(fmt.Sprintf("Using --scopes (%d scope%s)", len(selectedScopes), plural(len(selectedScopes))))
	case setupScopesPreset != "":
		preset, err := auth.ScopePreset(setupScopesPreset)
		if err != nil {
			return client.NewValidationError(err.Error(), "Try --scopes-preset readonly | all | <category,...>")
		}
		selectedScopes = preset
		info(fmt.Sprintf("Using --scopes-preset %s (%d scope%s)", setupScopesPreset, len(selectedScopes), plural(len(selectedScopes))))
	case setupNoPrompt:
		preset, _ := auth.ScopePreset("readonly")
		selectedScopes = preset
		info(fmt.Sprintf("--no-prompt set; defaulting to readonly preset (%d scope%s)", len(selectedScopes), plural(len(selectedScopes))))
	default:
		info("Choose which health data categories to authorize.")
		info(fmt.Sprintf("Default: %sall readonly scopes%s (recommended)", bold, reset))
		blank()

		type scopeOption struct {
			scope auth.ScopeInfo
		}
		var readOnlyScopes []scopeOption
		var readWriteScopes []scopeOption

		for _, s := range auth.AllScopes {
			if strings.HasSuffix(s.Suffix, ".readonly") {
				readOnlyScopes = append(readOnlyScopes, scopeOption{scope: s})
			} else {
				readWriteScopes = append(readWriteScopes, scopeOption{scope: s})
			}
		}

		fmt.Fprintf(os.Stderr, "  %sRead-only scopes:%s\n", bold, reset)
		for j, opt := range readOnlyScopes {
			fmt.Fprintf(os.Stderr, "    %s%2d.%s %s\n", green, j+1, reset, opt.scope.Label)
		}
		blank()
		fmt.Fprintf(os.Stderr, "  %sRead/write scopes:%s\n", bold, reset)
		for j, opt := range readWriteScopes {
			fmt.Fprintf(os.Stderr, "    %s%2d.%s %s\n", yellow, j+len(readOnlyScopes)+1, reset, opt.scope.Label)
		}
		blank()

		allOptions := append(readOnlyScopes, readWriteScopes...)

		fmt.Fprintf(os.Stderr, "  %sOptions:%s\n", dim, reset)
		fmt.Fprintf(os.Stderr, "    %sEnter%s     = All readonly scopes (recommended)\n", bold, reset)
		fmt.Fprintf(os.Stderr, "    %s*%s         = All scopes (read + write)\n", bold, reset)
		fmt.Fprintf(os.Stderr, "    %s1,2,5%s     = Specific scope numbers\n", bold, reset)
		blank()

		scopeInput := promptInput(reader, "Select scopes", "")

		switch strings.TrimSpace(scopeInput) {
		case "", "default":
			for _, opt := range readOnlyScopes {
				selectedScopes = append(selectedScopes, opt.scope.Suffix)
			}
		case "*", "all":
			for _, s := range auth.AllScopes {
				selectedScopes = append(selectedScopes, s.Suffix)
			}
		default:
			for _, part := range strings.Split(scopeInput, ",") {
				part = strings.TrimSpace(part)
				var idx int
				if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx >= 1 && idx <= len(allOptions) {
					selectedScopes = append(selectedScopes, allOptions[idx-1].scope.Suffix)
				}
			}
			if len(selectedScopes) == 0 {
				for _, opt := range readOnlyScopes {
					selectedScopes = append(selectedScopes, opt.scope.Suffix)
				}
			}
		}
	}

	blank()
	fmt.Fprintf(os.Stderr, "  %sSelected:%s\n", bold, reset)
	for _, s := range selectedScopes {
		label := s
		for _, si := range auth.AllScopes {
			if si.Suffix == s {
				label = si.Label
				break
			}
		}
		fmt.Fprintf(os.Stderr, "    %s %s\n", check, label)
	}

	// ─── Step 5: OAuth login ─────────────────────────────────────

	header(5, totalSteps, "Authenticate")

	cs, err := auth.LoadClientSecret()
	if err != nil {
		return client.NewConfigError(err.Error(), "Check the client secret file")
	}

	var email string
	var pendingAuthURL string // populated when --non-interactive-auth; surfaced on stdout below
	if setupNonInteractiveAuth {
		authURL, _, err := auth.NonInteractiveStart(cs, selectedScopes)
		if err != nil {
			return client.NewConfigError(err.Error(), "")
		}
		pendingAuthURL = authURL
		info("Skipping browser login (--non-interactive-auth).")
		info("Open this URL on any browser, authorize, then paste the redirected URL (or just the 'code' query parameter):")
		fmt.Fprintf(os.Stderr, "\n  %s\n\n", authURL)
		info("Then run: ghealth auth login --complete <code-or-url>")
	} else {
		info("Opening browser for Google OAuth consent...")
		blank()

		tok, err := auth.InteractiveLogin(cs, selectedScopes)
		if err != nil {
			return client.NewAuthError(fmt.Sprintf("login failed: %v", err), "Check your OAuth credentials and try again")
		}

		email = fetchUserEmail(tok.AccessToken)

		creds := &auth.StoredCredentials{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			TokenType:    tok.TokenType,
			Expiry:       tok.Expiry,
			Scopes:       selectedScopes,
			Email:        email,
		}

		if err := auth.SaveCredentials(creds); err != nil {
			return client.NewConfigError(fmt.Sprintf("failed to save credentials: %v", err), "")
		}

		if email != "" {
			success(fmt.Sprintf("Authenticated as %s%s%s", bold, email, reset))
		} else {
			success("Authenticated successfully")
		}
	}

	// ─── Step 6: Save config ─────────────────────────────────────

	header(6, totalSteps, "Save Configuration")

	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{
			Default:  config.ProfileConfig{},
			Profiles: make(map[string]config.ProfileConfig),
		}
	}
	cfg.Default.ProjectID = projectID
	cfg.Default.Scopes = selectedScopes

	if err := cfg.Save(); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to save config: %v", err), "")
	}
	success(fmt.Sprintf("Config saved to %s%s%s", dim, config.ConfigPath(), reset))
	if !setupNonInteractiveAuth {
		success(fmt.Sprintf("Credentials saved to %s%s%s", dim, config.CredentialsPath(), reset))
	}

	// ─── Done ────────────────────────────────────────────────────

	blank()
	fmt.Fprintf(os.Stderr, "  %s%s Setup Complete %s\n", bold, check, reset)
	blank()
	if !setupNonInteractiveAuth {
		fmt.Fprintf(os.Stderr, "  %sTry it out:%s\n", dim, reset)
		fmt.Fprintf(os.Stderr, "    %s$ ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29%s\n", cyan, reset)
		fmt.Fprintf(os.Stderr, "    %s$ ghealth data sleep list --from 2026-03-22%s\n", cyan, reset)
		fmt.Fprintf(os.Stderr, "    %s$ ghealth schema types%s\n", cyan, reset)
		blank()
	}

	status := "setup_complete"
	if setupNonInteractiveAuth {
		status = "setup_pending_auth"
	}
	result := map[string]interface{}{
		"status":             status,
		"project_id":         projectID,
		"email":              email,
		"scopes":             selectedScopes,
		"config_dir":         config.ConfigDir(),
		"client_secret_path": config.ClientSecretPath(),
		"credentials_path":   config.CredentialsPath(),
	}
	if setupNonInteractiveAuth {
		// Surface everything an agent needs to finish the flow on stdout — the
		// browser URL on stderr is for humans only and is not machine-readable.
		result["auth_url"] = pendingAuthURL
		result["complete_command"] = "ghealth auth login --complete <code-or-url>"
		result["pending_auth_path"] = auth.PendingAuthPath()
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}
