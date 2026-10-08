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
	"fmt"

	"ghealth/internal/version"
	"ghealth/pkg/auth"
	"ghealth/pkg/channel"
	"ghealth/pkg/client"
	configPkg "ghealth/pkg/config"
	"ghealth/pkg/types"
	"github.com/spf13/cobra"
)

var (
	flagFormat     string
	flagProfile    string
	flagDryRun     bool
	flagRaw        bool
	flagOutput     string
	flagAPIVersion string
)

var rootCmd = &cobra.Command{
	Use:     "ghealth",
	Short:   "Google Health API CLI — health data access for agents and developers",
	Version: version.Full(),
	Long: fmt.Sprintf(`ghealth wraps the Google Health API (v4 by default; v4beta with --api-version).
%d verified data types: steps, heart rate, exercise, sleep, weight,
SpO2, HRV, ECG, blood glucose, nutrition, and more.

Responses are simplified JSON by default (flat timestamps, compact source).
Use --raw for the original API response.

Get started:
  ghealth setup                                               # First-time setup
  ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29  # Weekly step totals
  ghealth data heart-rate list --from today --limit 10        # Recent heart rate
  ghealth schema types                                        # Discover all data types`, len(types.Registry)),
	SilenceUsage:  true,
	SilenceErrors: true,
	// Only the root defines a persistent pre-run hook; Cobra runs just the
	// nearest one, so subcommands must not add their own without chaining.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if flagProfile != "" {
			configPkg.ProfileOverride = flagProfile
		}
		if err := resolveAPIVersion(); err != nil {
			// 'ghealth config ...' must keep working with a bad api_version
			// configured, otherwise the user could not fix it.
			if !isChannelExempt(cmd) {
				return err
			}
			activeAPIVersion, apiVersionSource = channel.Default, configPkg.APIVersionSourceDefault
			client.SetAPIVersion(channel.Default)
		}
		return requireChannel(cmd)
	},
}

func init() {
	// Only register flags that actually work and are relevant globally.
	rootCmd.PersistentFlags().StringVarP(&flagFormat, "format", "f", "", "Output format: json, table, csv (default: json)")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "Write response to file (prints summary to stdout)")
	rootCmd.PersistentFlags().StringVarP(&flagProfile, "profile", "p", "", "Named config profile")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Show the HTTP request without executing")
	rootCmd.PersistentFlags().BoolVar(&flagRaw, "raw", false, "Return original API response (skip simplification)")
	rootCmd.PersistentFlags().StringVar(&flagAPIVersion, "api-version", "", "API channel: v4 (GA, default) or v4beta (preview features marked [beta])")
}

// Execute runs the root command and returns the exit code.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		if cliErr, ok := err.(*client.CLIError); ok {
			return client.WriteError(cliErr)
		}
		return client.WriteError(client.NewValidationError(err.Error(), ""))
	}
	return 0
}

func newClient() *client.Client {
	ts := auth.NewCompositeTokenSource()
	return client.New(ts)
}

func getFormat() string {
	return configPkg.GetFormat(flagFormat)
}
