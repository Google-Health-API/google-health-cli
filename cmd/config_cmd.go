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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/Google-Health-API/google-health-cli/pkg/client"
	"github.com/Google-Health-API/google-health-cli/pkg/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration",
	Long:  "View and modify ghealth configuration settings and profiles.",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE:  runConfigShow,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value (keys: project_id, format, timezone)",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
}

var configProfilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "Manage configuration profiles",
}

var configProfilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all profiles",
	RunE:  runConfigProfilesList,
}

var configProfilesSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Set the active profile",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigProfilesSwitch,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd, configSetCmd, configProfilesCmd)
	configProfilesCmd.AddCommand(configProfilesListCmd, configProfilesSwitchCmd)
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return client.NewConfigError(err.Error(), "")
	}

	format := getFormat()
	if format == "json" {
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return client.NewConfigError(fmt.Sprintf("failed to marshal config: %v", err), "")
		}
		fmt.Fprintln(os.Stdout, string(data))
	} else {
		// Default to TOML for non-JSON formats.
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
			return client.NewConfigError(fmt.Sprintf("failed to encode config: %v", err), "")
		}
		fmt.Fprint(os.Stdout, buf.String())
	}

	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key := strings.ToLower(args[0])
	value := args[1]

	cfg, err := config.Load()
	if err != nil {
		return client.NewConfigError(err.Error(), "")
	}

	profileName := os.Getenv("GHEALTH_PROFILE")
	if flagProfile != "" {
		profileName = flagProfile
	}
	if profileName == "" {
		profileName = "default"
	}

	profile := cfg.ActiveProfile()

	switch key {
	case "project_id":
		profile.ProjectID = value
	case "format":
		valid := map[string]bool{"json": true, "table": true, "csv": true}
		if !valid[strings.ToLower(value)] {
			return client.NewValidationError(fmt.Sprintf("invalid format: %s", value), "Valid formats: json, table, csv")
		}
		profile.Format = strings.ToLower(value)
	case "timezone":
		if value != "" {
			if _, err := time.LoadLocation(value); err != nil {
				return client.NewValidationError(
					fmt.Sprintf("invalid timezone %q: not a valid IANA zone", value),
					"Use an IANA zone like Europe/London or America/New_York, or an empty value for machine-local time")
			}
		}
		profile.Timezone = value
	default:
		return client.NewValidationError(fmt.Sprintf("unknown config key: %s", key), "Valid keys: project_id, format, timezone")
	}

	cfg.SetProfile(profileName, profile)

	if err := cfg.Save(); err != nil {
		return client.NewConfigError(fmt.Sprintf("failed to save config: %v", err), "")
	}

	result := map[string]string{
		"status": "updated",
		"key":    key,
		"value":  value,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runConfigProfilesList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return client.NewConfigError(err.Error(), "")
	}

	activeProfile := os.Getenv("GHEALTH_PROFILE")
	if flagProfile != "" {
		activeProfile = flagProfile
	}
	if activeProfile == "" {
		activeProfile = "default"
	}

	type profileEntry struct {
		Name      string `json:"name"`
		Active    bool   `json:"active"`
		ProjectID string `json:"project_id,omitempty"`
	}

	profiles := []profileEntry{
		{
			Name:      "default",
			Active:    activeProfile == "default",
			ProjectID: cfg.Default.ProjectID,
		},
	}

	for name, p := range cfg.Profiles {
		profiles = append(profiles, profileEntry{
			Name:      name,
			Active:    activeProfile == name,
			ProjectID: p.ProjectID,
		})
	}

	data, _ := json.MarshalIndent(map[string]interface{}{"profiles": profiles}, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}

func runConfigProfilesSwitch(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := config.Load()
	if err != nil {
		return client.NewConfigError(err.Error(), "")
	}

	if name != "default" {
		if _, ok := cfg.Profiles[name]; !ok {
			available := []string{"default"}
			for n := range cfg.Profiles {
				available = append(available, n)
			}
			return client.NewValidationError(
				fmt.Sprintf("profile '%s' not found", name),
				fmt.Sprintf("Available profiles: %s", strings.Join(available, ", ")),
			)
		}
	}

	fmt.Fprintf(os.Stderr, "To use profile '%s', set the environment variable:\n", name)
	fmt.Fprintf(os.Stderr, "  export GHEALTH_PROFILE=%s\n\n", name)
	fmt.Fprintf(os.Stderr, "Or use the --profile flag:\n")
	fmt.Fprintf(os.Stderr, "  ghealth --profile %s <command>\n", name)

	result := map[string]string{
		"status":  "switch",
		"profile": name,
		"hint":    fmt.Sprintf("export GHEALTH_PROFILE=%s", name),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(os.Stdout, string(data))
	return nil
}
