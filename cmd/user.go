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
	"encoding/json"

	"github.com/Google-Health-API/google-health-cli/pkg/client"
	"github.com/Google-Health-API/google-health-cli/pkg/output"
	"github.com/spf13/cobra"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "User identity, profile, settings, IRN profile, and paired devices",
}

var userIdentityCmd = &cobra.Command{
	Use:   "identity",
	Short: "Get the authenticated user's identity",
	RunE:  runUserIdentity,
}

var userIrnProfileCmd = &cobra.Command{
	Use:   "irn-profile",
	Short: "Get the user's irregular rhythm notification (IRN) profile",
	RunE:  runUserIrnProfile,
}

var userPairedDevicesCmd = &cobra.Command{
	Use:   "paired-devices",
	Short: "List or get the user's paired devices",
}

var userPairedDevicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the user's paired devices",
	RunE:  runUserPairedDevicesList,
}

var userPairedDevicesGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get a single paired device by ID",
	RunE:  runUserPairedDevicesGet,
}

var flagPairedDeviceID string

var userProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Get or update the user's profile",
}

var userProfileGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the user's profile",
	RunE:  runUserProfileGet,
}

var userProfileUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the user's profile",
	RunE:  runUserProfileUpdate,
}

var userSettingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Get or update the user's settings",
}

var userSettingsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get the user's settings",
	RunE:  runUserSettingsGet,
}

var userSettingsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update the user's settings",
	RunE:  runUserSettingsUpdate,
}

var flagUserJSON string

func init() {
	rootCmd.AddCommand(userCmd)

	userCmd.AddCommand(userIdentityCmd)

	userCmd.AddCommand(userProfileCmd)
	userProfileCmd.AddCommand(userProfileGetCmd)
	userProfileCmd.AddCommand(userProfileUpdateCmd)
	userProfileUpdateCmd.Flags().StringVar(&flagUserJSON, "json", "", "JSON body for update")
	userProfileUpdateCmd.MarkFlagRequired("json")

	userCmd.AddCommand(userSettingsCmd)
	userSettingsCmd.AddCommand(userSettingsGetCmd)
	userSettingsCmd.AddCommand(userSettingsUpdateCmd)
	userSettingsUpdateCmd.Flags().StringVar(&flagUserJSON, "json", "", "JSON body for update")
	userSettingsUpdateCmd.MarkFlagRequired("json")

	userCmd.AddCommand(userIrnProfileCmd)

	userCmd.AddCommand(userPairedDevicesCmd)
	userPairedDevicesCmd.AddCommand(userPairedDevicesListCmd)
	userPairedDevicesCmd.AddCommand(userPairedDevicesGetCmd)
	userPairedDevicesGetCmd.Flags().StringVar(&flagPairedDeviceID, "id", "", "Paired device ID (required)")
	userPairedDevicesGetCmd.MarkFlagRequired("id")
}

func runUserIdentity(cmd *cobra.Command, args []string) error {
	return doUserGet("/users/me/identity")
}

func runUserProfileGet(cmd *cobra.Command, args []string) error {
	return doUserGet("/users/me/profile")
}

func runUserSettingsGet(cmd *cobra.Command, args []string) error {
	return doUserGet("/users/me/settings")
}

func runUserIrnProfile(cmd *cobra.Command, args []string) error {
	return doUserGet("/users/me/irnProfile")
}

func runUserPairedDevicesList(cmd *cobra.Command, args []string) error {
	return doUserGet("/users/me/pairedDevices")
}

func runUserPairedDevicesGet(cmd *cobra.Command, args []string) error {
	return doUserGet("/users/me/pairedDevices/" + flagPairedDeviceID)
}

func doUserGet(path string) error {
	c := newClient()
	req := &client.Request{Method: "GET", Path: path}

	if flagDryRun {
		data, err := c.DryRun(req)
		if err != nil {
			return client.NewValidationError(err.Error(), "")
		}
		return output.PrintJSON(data)
	}

	resp, err := c.Do(req)
	if err != nil {
		if cliErr, ok := err.(*client.CLIError); ok {
			return cliErr
		}
		return client.NewAPIError(0, err.Error(), "")
	}

	return printOutput(resp.Body)
}

func runUserProfileUpdate(cmd *cobra.Command, args []string) error {
	return doUserUpdate("/users/me/profile")
}

func runUserSettingsUpdate(cmd *cobra.Command, args []string) error {
	return doUserUpdate("/users/me/settings")
}

func doUserUpdate(path string) error {
	jsonBody := flagUserJSON
	if !json.Valid([]byte(jsonBody)) {
		return client.NewValidationError("invalid JSON body", "Provide valid JSON via --json")
	}

	c := newClient()
	req := &client.Request{
		Method:      "PATCH",
		Path:        path,
		Body:        []byte(jsonBody),
		ContentType: "application/json",
	}

	if flagDryRun {
		data, err := c.DryRun(req)
		if err != nil {
			return client.NewValidationError(err.Error(), "")
		}
		return output.PrintJSON(data)
	}

	resp, err := c.Do(req)
	if err != nil {
		if cliErr, ok := err.(*client.CLIError); ok {
			return cliErr
		}
		return client.NewAPIError(0, err.Error(), "")
	}

	return printOutput(resp.Body)
}
