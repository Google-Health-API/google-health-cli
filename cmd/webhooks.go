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
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Google-Health-API/google-health-cli/pkg/client"
	configPkg "github.com/Google-Health-API/google-health-cli/pkg/config"
	"github.com/Google-Health-API/google-health-cli/pkg/output"
	"github.com/spf13/cobra"
)

// Webhooks are delivered through the project-level subscribers/subscriptions
// resource model (discovery revision 20260528). A *subscriber* is an HTTPS
// endpoint (with an auth secret and per-data-type config); a *subscription*
// ties a user to a set of data types under a subscriber. All of these
// endpoints require the cloud-platform OAuth scope and a GCP project ID.
//
//	projects/{project}/subscribers/{subscriber}
//	projects/{project}/subscribers/{subscriber}/subscriptions/{subscription}

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Manage webhook subscribers and subscriptions",
	Long: `Manage push notifications via the Google Health API webhooks model.

A subscriber is an HTTPS endpoint that receives signed notifications; a
subscription binds a user to specific data types under a subscriber.

Requires the 'cloud-platform' OAuth scope and a configured project ID.

IMPORTANT: a token carrying 'cloud-platform' is REJECTED by the data-plane
endpoints ("disallowed OAuth scope"), so keep webhook credentials in a
separate config dir from your health-data credentials. 'auth login' writes
tokens to the active config dir, so point GHEALTH_CONFIG_DIR at a dedicated
one for webhooks:

  # health data (default config dir)
  ghealth auth login --scopes-preset readonly

  # webhooks (dedicated config dir with its own client_secret and project)
  export GHEALTH_CONFIG_DIR=~/.config/ghealth-webhooks
  ghealth setup
  ghealth auth login --scopes cloud-platform
  ghealth webhooks subscribers list

The authenticated identity also needs Health API subscriber IAM permissions
on the project. See https://developers.google.com/health/webhooks`,
}

// ─── subscribers ─────────────────────────────────────────────────

var webhooksSubscribersCmd = &cobra.Command{
	Use:   "subscribers",
	Short: "List, create, update, or delete webhook subscribers (endpoints)",
}

var webhooksSubscribersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List webhook subscribers in the project",
	RunE:  runSubscribersList,
}

var webhooksSubscribersCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a webhook subscriber endpoint",
	RunE:  runSubscribersCreate,
}

var webhooksSubscribersUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a webhook subscriber (raw JSON body)",
	RunE:  runSubscribersUpdate,
}

var webhooksSubscribersDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a webhook subscriber",
	RunE:  runSubscribersDelete,
}

// ─── subscriptions ───────────────────────────────────────────────

var webhooksSubscriptionsCmd = &cobra.Command{
	Use:   "subscriptions",
	Short: "List, create, update, or delete subscriptions under a subscriber",
}

var webhooksSubscriptionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List subscriptions under a subscriber",
	RunE:  runSubscriptionsList,
}

var webhooksSubscriptionsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a subscription under a subscriber",
	RunE:  runSubscriptionsCreate,
}

var webhooksSubscriptionsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a subscription (raw JSON body)",
	RunE:  runSubscriptionsUpdate,
}

var webhooksSubscriptionsDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a subscription",
	RunE:  runSubscriptionsDelete,
}

// ─── verify (local, no API) ──────────────────────────────────────

var webhooksVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Test connectivity to a webhook endpoint (local HTTP check, no API call)",
	RunE:  runWebhooksVerify,
}

var (
	flagWhEndpointURI    string
	flagWhSecret         string
	flagWhTypes          string
	flagWhPolicy         string
	flagWhSubscriberID   string
	flagWhSubscriptionID string
	flagWhUser           string
	flagWhForce          bool
	flagWhJSON           string
	flagWhUpdateMask     string
	flagWhURL            string
)

func init() {
	rootCmd.AddCommand(webhooksCmd)

	// subscribers
	webhooksCmd.AddCommand(webhooksSubscribersCmd)
	webhooksSubscribersCmd.AddCommand(webhooksSubscribersListCmd)

	webhooksSubscribersCmd.AddCommand(webhooksSubscribersCreateCmd)
	webhooksSubscribersCreateCmd.Flags().StringVar(&flagWhEndpointURI, "endpoint-uri", "", "HTTPS URI to receive notifications (required)")
	webhooksSubscribersCreateCmd.Flags().StringVar(&flagWhSecret, "secret", "", "Shared secret signed into each notification (required)")
	webhooksSubscribersCreateCmd.Flags().StringVar(&flagWhTypes, "types", "", "Comma-separated data type IDs for the subscriber config")
	webhooksSubscribersCreateCmd.Flags().StringVar(&flagWhPolicy, "policy", "AUTOMATIC", "Subscription create policy: AUTOMATIC or MANUAL")
	webhooksSubscribersCreateCmd.Flags().StringVar(&flagWhSubscriberID, "id", "", "Optional subscriber ID (final path component)")
	webhooksSubscribersCreateCmd.MarkFlagRequired("endpoint-uri")
	webhooksSubscribersCreateCmd.MarkFlagRequired("secret")

	webhooksSubscribersCmd.AddCommand(webhooksSubscribersUpdateCmd)
	webhooksSubscribersUpdateCmd.Flags().StringVar(&flagWhSubscriberID, "id", "", "Subscriber ID (required)")
	webhooksSubscribersUpdateCmd.Flags().StringVar(&flagWhJSON, "json", "", "JSON body for the Subscriber (required)")
	webhooksSubscribersUpdateCmd.Flags().StringVar(&flagWhUpdateMask, "update-mask", "", "Comma-separated fields to update")
	webhooksSubscribersUpdateCmd.MarkFlagRequired("id")
	webhooksSubscribersUpdateCmd.MarkFlagRequired("json")

	webhooksSubscribersCmd.AddCommand(webhooksSubscribersDeleteCmd)
	webhooksSubscribersDeleteCmd.Flags().StringVar(&flagWhSubscriberID, "id", "", "Subscriber ID (required)")
	webhooksSubscribersDeleteCmd.Flags().BoolVar(&flagWhForce, "force", false, "Also delete child subscriptions")
	webhooksSubscribersDeleteCmd.MarkFlagRequired("id")

	// subscriptions
	webhooksCmd.AddCommand(webhooksSubscriptionsCmd)
	webhooksSubscriptionsCmd.AddCommand(webhooksSubscriptionsListCmd)
	webhooksSubscriptionsListCmd.Flags().StringVar(&flagWhSubscriberID, "subscriber", "", "Parent subscriber ID (required)")
	webhooksSubscriptionsListCmd.MarkFlagRequired("subscriber")

	webhooksSubscriptionsCmd.AddCommand(webhooksSubscriptionsCreateCmd)
	webhooksSubscriptionsCreateCmd.Flags().StringVar(&flagWhSubscriberID, "subscriber", "", "Parent subscriber ID (required)")
	webhooksSubscriptionsCreateCmd.Flags().StringVar(&flagWhTypes, "types", "", "Comma-separated data type IDs to subscribe to")
	webhooksSubscriptionsCreateCmd.Flags().StringVar(&flagWhUser, "user", "users/me", "User resource name the subscription is active for")
	webhooksSubscriptionsCreateCmd.Flags().StringVar(&flagWhSubscriptionID, "id", "", "Optional subscription ID (4-36 chars)")
	webhooksSubscriptionsCreateCmd.MarkFlagRequired("subscriber")

	webhooksSubscriptionsCmd.AddCommand(webhooksSubscriptionsUpdateCmd)
	webhooksSubscriptionsUpdateCmd.Flags().StringVar(&flagWhSubscriberID, "subscriber", "", "Parent subscriber ID (required)")
	webhooksSubscriptionsUpdateCmd.Flags().StringVar(&flagWhSubscriptionID, "id", "", "Subscription ID (required)")
	webhooksSubscriptionsUpdateCmd.Flags().StringVar(&flagWhJSON, "json", "", "JSON body for the Subscription (required)")
	webhooksSubscriptionsUpdateCmd.Flags().StringVar(&flagWhUpdateMask, "update-mask", "", "Comma-separated fields to update")
	webhooksSubscriptionsUpdateCmd.MarkFlagRequired("subscriber")
	webhooksSubscriptionsUpdateCmd.MarkFlagRequired("id")
	webhooksSubscriptionsUpdateCmd.MarkFlagRequired("json")

	webhooksSubscriptionsCmd.AddCommand(webhooksSubscriptionsDeleteCmd)
	webhooksSubscriptionsDeleteCmd.Flags().StringVar(&flagWhSubscriberID, "subscriber", "", "Parent subscriber ID (required)")
	webhooksSubscriptionsDeleteCmd.Flags().StringVar(&flagWhSubscriptionID, "id", "", "Subscription ID (required)")
	webhooksSubscriptionsDeleteCmd.MarkFlagRequired("subscriber")
	webhooksSubscriptionsDeleteCmd.MarkFlagRequired("id")

	// verify
	webhooksCmd.AddCommand(webhooksVerifyCmd)
	webhooksVerifyCmd.Flags().StringVar(&flagWhURL, "url", "", "Webhook URL to verify")
	webhooksVerifyCmd.MarkFlagRequired("url")
}

// projectPath returns "projects/{id}" for the active profile, or a validation
// error if no project ID is configured.
func projectPath() (string, error) {
	cfg, err := configPkg.Load()
	if err != nil {
		return "", client.NewValidationError(err.Error(), "")
	}
	id := cfg.ActiveProfile().ProjectID
	if id == "" {
		return "", client.NewValidationError(
			"no project ID configured",
			"Run 'ghealth setup' or add project_id to your profile; webhooks are project-scoped")
	}
	return "projects/" + id, nil
}

// splitTypes turns a comma-separated flag into a trimmed, non-empty slice.
func splitTypes(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// runWebhookReq executes (or dry-runs) a request and prints the response.
func runWebhookReq(req *client.Request) error {
	c := newClient()
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

// ─── subscriber handlers ─────────────────────────────────────────

func runSubscribersList(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	return runWebhookReq(&client.Request{Method: "GET", Path: "/" + parent + "/subscribers"})
}

func runSubscribersCreate(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}

	body := map[string]interface{}{
		"endpointUri": flagWhEndpointURI,
		"endpointAuthorization": map[string]interface{}{
			"secret": flagWhSecret,
		},
	}
	if types := splitTypes(flagWhTypes); len(types) > 0 {
		policy := strings.ToUpper(strings.TrimSpace(flagWhPolicy))
		if policy != "AUTOMATIC" && policy != "MANUAL" {
			return client.NewValidationError(
				fmt.Sprintf("invalid --policy %q", flagWhPolicy),
				"Use AUTOMATIC or MANUAL")
		}
		body["subscriberConfigs"] = []map[string]interface{}{{
			"dataTypes":                types,
			"subscriptionCreatePolicy": policy,
		}}
	}

	bodyJSON, _ := json.Marshal(body)
	req := &client.Request{
		Method:      "POST",
		Path:        "/" + parent + "/subscribers",
		Body:        bodyJSON,
		ContentType: "application/json",
	}
	if flagWhSubscriberID != "" {
		req.Query = url.Values{"subscriberId": {flagWhSubscriberID}}
	}
	return runWebhookReq(req)
}

func runSubscribersUpdate(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	if !json.Valid([]byte(flagWhJSON)) {
		return client.NewValidationError("invalid JSON body", "Provide valid JSON via --json")
	}
	req := &client.Request{
		Method:      "PATCH",
		Path:        "/" + parent + "/subscribers/" + flagWhSubscriberID,
		Body:        []byte(flagWhJSON),
		ContentType: "application/json",
	}
	if flagWhUpdateMask != "" {
		req.Query = url.Values{"updateMask": {flagWhUpdateMask}}
	}
	return runWebhookReq(req)
}

func runSubscribersDelete(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	req := &client.Request{
		Method: "DELETE",
		Path:   "/" + parent + "/subscribers/" + flagWhSubscriberID,
	}
	if flagWhForce {
		req.Query = url.Values{"force": {"true"}}
	}
	return runWebhookReq(req)
}

// ─── subscription handlers ───────────────────────────────────────

func runSubscriptionsList(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	path := "/" + parent + "/subscribers/" + flagWhSubscriberID + "/subscriptions"
	return runWebhookReq(&client.Request{Method: "GET", Path: path})
}

func runSubscriptionsCreate(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	body := map[string]interface{}{
		"user": flagWhUser,
	}
	if types := splitTypes(flagWhTypes); len(types) > 0 {
		body["dataTypes"] = types
	}
	bodyJSON, _ := json.Marshal(body)
	req := &client.Request{
		Method:      "POST",
		Path:        "/" + parent + "/subscribers/" + flagWhSubscriberID + "/subscriptions",
		Body:        bodyJSON,
		ContentType: "application/json",
	}
	if flagWhSubscriptionID != "" {
		req.Query = url.Values{"subscriptionId": {flagWhSubscriptionID}}
	}
	return runWebhookReq(req)
}

func runSubscriptionsUpdate(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	if !json.Valid([]byte(flagWhJSON)) {
		return client.NewValidationError("invalid JSON body", "Provide valid JSON via --json")
	}
	req := &client.Request{
		Method:      "PATCH",
		Path:        "/" + parent + "/subscribers/" + flagWhSubscriberID + "/subscriptions/" + flagWhSubscriptionID,
		Body:        []byte(flagWhJSON),
		ContentType: "application/json",
	}
	if flagWhUpdateMask != "" {
		req.Query = url.Values{"updateMask": {flagWhUpdateMask}}
	}
	return runWebhookReq(req)
}

func runSubscriptionsDelete(cmd *cobra.Command, args []string) error {
	parent, err := projectPath()
	if err != nil {
		return err
	}
	path := "/" + parent + "/subscribers/" + flagWhSubscriberID + "/subscriptions/" + flagWhSubscriptionID
	return runWebhookReq(&client.Request{Method: "DELETE", Path: path})
}

// ─── verify (local connectivity check) ───────────────────────────

func runWebhooksVerify(cmd *cobra.Command, args []string) error {
	if flagDryRun {
		result := map[string]interface{}{
			"action": "verify_webhook",
			"url":    flagWhURL,
			"note":   "dry-run: would send HTTP GET to the webhook URL",
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return output.PrintJSON(json.RawMessage(data))
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Get(flagWhURL)
	if err != nil {
		return client.NewNetworkError(fmt.Sprintf("failed to reach webhook URL: %v", err))
	}
	defer resp.Body.Close()

	result := map[string]interface{}{
		"url":        flagWhURL,
		"statusCode": resp.StatusCode,
		"reachable":  resp.StatusCode >= 200 && resp.StatusCode < 500,
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return printOutput(json.RawMessage(data))
}
