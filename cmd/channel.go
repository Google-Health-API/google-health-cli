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
	"os"
	"strings"

	"ghealth/pkg/channel"
	"ghealth/pkg/client"
	configPkg "ghealth/pkg/config"
	"github.com/spf13/cobra"
)

// channelAnnotation is the Cobra annotation key that marks a command (and all
// of its subcommands) as requiring a pre-GA API channel, e.g. "v4beta". Data
// type commands get it from the registry's DataType.Channel/OpChannels; any
// other command (a future beta-only webhooks operation, statistics, ...) sets
// it directly. requireChannel enforces it before the command runs.
const channelAnnotation = "channel"

// channelExemptAnnotation marks commands (and their subcommands) that must run
// even when the configured API version is invalid, e.g. 'ghealth config set
// api_version v4' to repair it.
const channelExemptAnnotation = "channel-exempt"

func isChannelExempt(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[channelExemptAnnotation] == "true" {
			return true
		}
	}
	return false
}

// Resolved API channel for this invocation, set by resolveAPIVersion in the
// root PersistentPreRunE.
var (
	activeAPIVersion = channel.Default
	apiVersionSource = configPkg.APIVersionSourceDefault
)

// resolveAPIVersion determines the active API channel and points the HTTP
// client at it. Resolution order: --api-version, GHEALTH_API_VERSION, the
// active profile's api_version, then v4.
//
// GHEALTH_BASE_URL wins outright for the request URL (it already carries a
// version segment, e.g. a local fake API at http://127.0.0.1:8080/v4). When
// its last path segment is a known channel, that channel becomes the active
// one so gating and reporting match what is actually called; an explicitly
// configured channel that disagrees with it is a validation error rather
// than a silent mismatch.
func resolveAPIVersion() error {
	ver, src := configPkg.ResolveAPIVersion(flagAPIVersion)
	if !channel.IsSelectable(ver) {
		return client.NewValidationError(
			fmt.Sprintf("invalid API version %q (from %s)", ver, describeAPIVersionSource(src)),
			"Valid values: "+strings.Join(channel.Selectable, ", ")+". Use v4 (GA) unless you need a beta-only feature.",
		)
	}

	if base := os.Getenv("GHEALTH_BASE_URL"); base != "" {
		if inferred, ok := channel.FromBaseURL(base); ok {
			if src != configPkg.APIVersionSourceDefault && inferred != ver {
				return client.NewValidationError(
					fmt.Sprintf("API version %q (from %s) conflicts with GHEALTH_BASE_URL=%s", ver, describeAPIVersionSource(src), base),
					fmt.Sprintf("GHEALTH_BASE_URL takes precedence for requests. Point it at a /%s endpoint, or unset one of the two.", ver),
				)
			}
			ver, src = inferred, "GHEALTH_BASE_URL"
		}
	}

	activeAPIVersion, apiVersionSource = ver, src
	client.SetAPIVersion(ver)
	return nil
}

func describeAPIVersionSource(src string) string {
	switch src {
	case configPkg.APIVersionSourceFlag:
		return "--api-version"
	case configPkg.APIVersionSourceEnv:
		return "GHEALTH_API_VERSION"
	case configPkg.APIVersionSourceProfile:
		return "config api_version"
	default:
		return src
	}
}

// requiredChannel returns the channel annotation of cmd or its nearest
// annotated ancestor, so an operation-level override beats its type-level
// default. "" means GA.
func requiredChannel(cmd *cobra.Command) string {
	for c := cmd; c != nil; c = c.Parent() {
		if ch, ok := c.Annotations[channelAnnotation]; ok && ch != "" {
			return ch
		}
	}
	return ""
}

// requireChannel rejects a command whose required channel is not reachable
// from the active one, with a hint naming the flag that unlocks it. Runs
// before --dry-run so a dry run never shows a request the real call would
// refuse.
func requireChannel(cmd *cobra.Command) error {
	required := requiredChannel(cmd)
	if channel.Allows(activeAPIVersion, required) {
		return nil
	}
	hint := fmt.Sprintf("Rerun with --api-version %s (or set GHEALTH_API_VERSION=%s). "+
		"Beta features may change before GA; use v4 unless you need this one.", required, required)
	if apiVersionSource == "GHEALTH_BASE_URL" {
		hint = fmt.Sprintf("GHEALTH_BASE_URL points at the %s API; point it at a /%s endpoint to use this command.",
			activeAPIVersion, required)
	}
	return client.NewValidationError(
		fmt.Sprintf("'%s' requires the %s API channel (active: %s)", cmd.CommandPath(), required, activeAPIVersion),
		hint,
	)
}

// markChannel annotates cmd as requiring ch and prefixes its short help with
// the channel label (e.g. "[beta]"). GA channels are a no-op.
func markChannel(cmd *cobra.Command, ch string) {
	if !channel.IsPreview(ch) {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[channelAnnotation] = ch
	if label := channel.Label(ch); label != "" && !strings.HasPrefix(cmd.Short, label) {
		cmd.Short = label + " " + cmd.Short
	}
}

// addAPIVersionInfo reports the effective API channel, where it came from,
// and the base URL requests go to (e.g. in 'ghealth auth status').
func addAPIVersionInfo(result map[string]interface{}) {
	result["api_version"] = activeAPIVersion
	result["api_version_source"] = describeAPIVersionSource(apiVersionSource)
	result["base_url"] = client.BaseURL
}
