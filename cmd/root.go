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
	"github.com/Google-Health-API/google-health-cli/internal/version"
	"github.com/Google-Health-API/google-health-cli/pkg/auth"
	"github.com/Google-Health-API/google-health-cli/pkg/client"
	configPkg "github.com/Google-Health-API/google-health-cli/pkg/config"
	"github.com/spf13/cobra"
)

var (
	flagFormat  string
	flagProfile string
	flagDryRun  bool
	flagRaw     bool
	flagOutput  string
)

var rootCmd = &cobra.Command{
	Use:     "ghealth",
	Short:   "Google Health API CLI — health data access for agents and developers",
	Version: version.Full(),
	Long: `ghealth wraps the Google Health API v4.
40 verified data types: steps, heart rate, exercise, sleep, weight,
SpO2, HRV, ECG, blood glucose, nutrition, and more.

Responses are simplified JSON by default (flat timestamps, compact source).
Use --raw for the original API response.

Get started:
  ghealth setup                                               # First-time setup
  ghealth data steps daily-rollup --from 2026-03-22 --to 2026-03-29  # Weekly step totals
  ghealth data heart-rate list --from today --limit 10        # Recent heart rate
  ghealth schema types                                        # Discover all data types`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if flagProfile != "" {
			configPkg.ProfileOverride = flagProfile
		}
	},
}

func init() {
	// Only register flags that actually work and are relevant globally.
	rootCmd.PersistentFlags().StringVarP(&flagFormat, "format", "f", "", "Output format: json, table, csv (default: json)")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "", "Write response to file (prints summary to stdout)")
	rootCmd.PersistentFlags().StringVarP(&flagProfile, "profile", "p", "", "Named config profile")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Show the HTTP request without executing")
	rootCmd.PersistentFlags().BoolVar(&flagRaw, "raw", false, "Return original API response (skip simplification)")
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
