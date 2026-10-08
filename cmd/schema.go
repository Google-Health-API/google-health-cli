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
	"sort"
	"strings"

	"ghealth/pkg/auth"
	"ghealth/pkg/channel"
	"ghealth/pkg/client"
	"ghealth/pkg/schema"
	"ghealth/pkg/types"
	"github.com/spf13/cobra"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Explore API schema, data types, scopes, and endpoints",
}

var schemaTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List all available data types",
	RunE:  runSchemaTypes,
}

var schemaTypeCmd = &cobra.Command{
	Use:   "type <name>",
	Short: "Show details for a specific data type",
	Args:  cobra.ExactArgs(1),
	RunE:  runSchemaType,
}

var schemaScopesCmd = &cobra.Command{
	Use:   "scopes",
	Short: "List all OAuth scopes with associated data types",
	RunE:  runSchemaScopes,
}

var schemaEndpointsCmd = &cobra.Command{
	Use:   "endpoints",
	Short: "List all API endpoints",
	RunE:  runSchemaEndpoints,
}

func init() {
	rootCmd.AddCommand(schemaCmd)
	schemaCmd.AddCommand(schemaTypesCmd)
	schemaCmd.AddCommand(schemaTypeCmd)
	schemaCmd.AddCommand(schemaScopesCmd)
	schemaCmd.AddCommand(schemaEndpointsCmd)
}

func runSchemaTypes(cmd *cobra.Command, args []string) error {
	source := "registry"
	doc, src, err := schema.FetchDiscovery(activeAPIVersion)
	if err == nil {
		source = src
	}
	_ = doc // types command uses registry, not discovery

	// Build type list from registry (always authoritative for type metadata).
	// Pre-GA types are listed on every channel (so agents can discover them)
	// with "available" telling whether the active --api-version can call them.
	ids := types.IDs()
	typeList := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		dt := types.Get(id)
		entry := map[string]interface{}{
			"id":          dt.ID,
			"category":    dt.Category,
			"description": dt.Description,
			"writable":    dt.Writable,
			"rollupOnly":  dt.RollupOnly,
			"operations":  dt.Operations,
		}
		addChannelInfo(entry, dt)
		typeList = append(typeList, entry)
	}

	result := map[string]interface{}{
		"source":     source,
		"apiVersion": activeAPIVersion,
		"count":      len(typeList),
		"dataTypes":  typeList,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError("failed to encode output", "")
	}

	return printOutput(json.RawMessage(data))
}

// addChannelInfo annotates a schema entry with the type's release channel
// and whether it is callable on the active API version. GA types get
// channel "v4"; per-operation overrides are listed under operationChannels.
func addChannelInfo(entry map[string]interface{}, dt *types.DataType) {
	entry["channel"] = dt.OperationChannel("")
	entry["available"] = channel.Allows(activeAPIVersion, dt.OperationChannel(""))
	if len(dt.OpChannels) > 0 {
		entry["operationChannels"] = dt.OpChannels
	}
}

func runSchemaType(cmd *cobra.Command, args []string) error {
	name := args[0]
	dt := types.Get(name)
	if dt == nil {
		return client.NewValidationError(
			fmt.Sprintf("unknown data type: %s", name),
			fmt.Sprintf("Run 'ghealth schema types' to see available types"),
		)
	}

	source := "registry"
	var discoveryDoc json.RawMessage

	// A pre-GA type is absent from the GA discovery document, so describe it
	// from its own channel's document; otherwise use the active channel's.
	docVersion := activeAPIVersion
	if ch := dt.OperationChannel(""); !channel.Allows(activeAPIVersion, ch) {
		docVersion = ch
	}
	doc, src, err := schema.FetchDiscovery(docVersion)
	if err == nil {
		source = src
		discoveryDoc = doc
	}

	// Extract fields from discovery doc if available.
	fields := extractFieldsFromDiscovery(discoveryDoc, name)

	result := map[string]interface{}{
		"source":      source,
		"apiVersion":  docVersion,
		"id":          dt.ID,
		"filterName":  dt.FilterName,
		"category":    dt.Category,
		"scope":       dt.FullScope(),
		"description": dt.Description,
		"operations":  dt.Operations,
		"writable":    dt.Writable,
		"rollupOnly":  dt.RollupOnly,
		"parameters":  buildOperationParameters(dt),
	}
	addChannelInfo(result, dt)
	if len(fields) > 0 {
		result["fields"] = fields
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError("failed to encode output", "")
	}

	return printOutput(json.RawMessage(data))
}

// buildOperationParameters describes the REAL request parameters per
// operation, derived from the type registry (FilterPath) — v4 has no
// startTime/endTime/bucketDuration parameters on these endpoints.
func buildOperationParameters(dt *types.DataType) map[string]interface{} {
	const (
		pageSizeDesc         = "integer (server default 1440, max 10000; exercise/sleep max 25)"
		pageTokenDesc        = "string — continuation token from a previous response"
		dataSourceFamilyDesc = "all-sources | google-wearables | google-sources"
	)

	params := map[string]interface{}{}
	for _, op := range dt.Operations {
		switch op {
		case "list":
			p := map[string]interface{}{
				"pageSize":  pageSizeDesc,
				"pageToken": pageTokenDesc,
			}
			if tmpl := filterTemplate(dt); tmpl != "" {
				p["filter"] = tmpl
			}
			params[op] = p
		case "rollup":
			params[op] = map[string]interface{}{
				"body": map[string]interface{}{
					"range": map[string]string{
						"startTime": "<RFC3339>",
						"endTime":   "<RFC3339, exclusive>",
					},
					"windowSize":       `duration string, e.g. "3600s", "86400s" (required)`,
					"pageSize":         pageSizeDesc,
					"pageToken":        pageTokenDesc,
					"dataSourceFamily": dataSourceFamilyDesc,
				},
			}
		case "daily-rollup":
			params[op] = map[string]interface{}{
				"body": map[string]interface{}{
					"range": map[string]string{
						"start": "civil date {year, month, day}",
						"end":   "civil date {year, month, day}, exclusive",
					},
					"windowSizeDays":   "integer, default 1",
					"dataSourceFamily": dataSourceFamilyDesc,
				},
			}
		}
	}
	return params
}

// filterTemplate builds a concrete list-filter example from the registry's
// real filter field for this type. The placeholder reflects the field's
// timestamp format (RFC-3339 with Z for physical time, ISO 8601 without
// offset for civil time, plain dates for daily-summary types).
func filterTemplate(dt *types.DataType) string {
	path := dt.FilterPath()
	if path == "" {
		return ""
	}
	placeholder := "<ISO8601, no offset>"
	switch dt.TimeField {
	case types.TimeFieldSample, types.TimeFieldPhysicalIntervalStart:
		placeholder = "<RFC3339>"
	case types.TimeFieldDaily:
		placeholder = "<YYYY-MM-DD>"
	}
	if dt.TimeField == types.TimeFieldPhysicalIntervalStart {
		// Only a lower bound is supported (e.g. electrocardiogram).
		return fmt.Sprintf("%s >= %q", path, placeholder)
	}
	return fmt.Sprintf("%s >= %q AND %s < %q", path, placeholder, path, placeholder)
}

func runSchemaScopes(cmd *cobra.Command, args []string) error {
	scopeList := make([]map[string]interface{}, 0, len(auth.AllScopes))
	for _, s := range auth.AllScopes {
		// Find data types associated with this scope's category.
		var associatedTypes []string
		for _, dt := range types.All() {
			if dt.Category == s.Category {
				associatedTypes = append(associatedTypes, dt.ID)
			}
		}
		sort.Strings(associatedTypes)

		scopeList = append(scopeList, map[string]interface{}{
			"scope":     auth.FullScope(s.Suffix),
			"suffix":    s.Suffix,
			"label":     s.Label,
			"category":  s.Category,
			"dataTypes": associatedTypes,
		})
	}

	result := map[string]interface{}{
		"count":  len(scopeList),
		"scopes": scopeList,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError("failed to encode output", "")
	}

	return printOutput(json.RawMessage(data))
}

func runSchemaEndpoints(cmd *cobra.Command, args []string) error {
	var endpoints []map[string]interface{}
	// Paths are rooted at the active API version (e.g. /v4 or /v4beta).
	v := "/" + activeAPIVersion

	// User endpoints (static).
	endpoints = append(endpoints,
		map[string]interface{}{"method": "GET", "path": v + "/users/me/identity", "description": "Get user identity"},
		map[string]interface{}{"method": "GET", "path": v + "/users/me/profile", "description": "Get user profile"},
		map[string]interface{}{"method": "PATCH", "path": v + "/users/me/profile", "description": "Update user profile"},
		map[string]interface{}{"method": "GET", "path": v + "/users/me/settings", "description": "Get user settings"},
		map[string]interface{}{"method": "PATCH", "path": v + "/users/me/settings", "description": "Update user settings"},
	)

	// Webhook endpoints — project-level subscriber/subscription model
	// (discovery revision 20260528). These require the cloud-platform scope
	// and a configured project ID; see 'ghealth webhooks --help'.
	endpoints = append(endpoints,
		map[string]interface{}{"method": "GET", "path": v + "/projects/{project}/subscribers", "description": "List webhook subscribers"},
		map[string]interface{}{"method": "POST", "path": v + "/projects/{project}/subscribers", "description": "Create webhook subscriber"},
		map[string]interface{}{"method": "PATCH", "path": v + "/projects/{project}/subscribers/{subscriber}", "description": "Update webhook subscriber"},
		map[string]interface{}{"method": "DELETE", "path": v + "/projects/{project}/subscribers/{subscriber}", "description": "Delete webhook subscriber"},
		map[string]interface{}{"method": "GET", "path": v + "/projects/{project}/subscribers/{subscriber}/subscriptions", "description": "List webhook subscriptions"},
		map[string]interface{}{"method": "POST", "path": v + "/projects/{project}/subscribers/{subscriber}/subscriptions", "description": "Create webhook subscription"},
		map[string]interface{}{"method": "PATCH", "path": v + "/projects/{project}/subscribers/{subscriber}/subscriptions/{subscription}", "description": "Update webhook subscription"},
		map[string]interface{}{"method": "DELETE", "path": v + "/projects/{project}/subscribers/{subscriber}/subscriptions/{subscription}", "description": "Delete webhook subscription"},
	)

	// Data type endpoints (derived from registry).
	ids := types.IDs()
	for _, id := range ids {
		dt := types.Get(id)
		basePath := fmt.Sprintf("%s/users/me/dataTypes/%s/dataPoints", v, dt.ID)

		for _, op := range dt.Operations {
			var method, path, desc string
			switch op {
			case "list":
				method, path, desc = "GET", basePath, fmt.Sprintf("List %s data points", id)
			case "create":
				method, path, desc = "POST", basePath, fmt.Sprintf("Create %s data point", id)
			case "update":
				method, path, desc = "PATCH", basePath+"/{id}", fmt.Sprintf("Update %s data point", id)
			case "delete":
				method, path, desc = "POST", basePath+":batchDelete", fmt.Sprintf("Delete %s data points", id)
			case "rollup":
				method, path, desc = "POST", basePath+":rollUp", fmt.Sprintf("Roll up %s data", id)
			case "daily-rollup":
				method, path, desc = "POST", basePath+":dailyRollUp", fmt.Sprintf("Daily roll up %s data", id)
			case "reconcile":
				method, path, desc = "GET", basePath+":reconcile", fmt.Sprintf("Reconcile %s data", id)
			case "export-tcx":
				method, path, desc = "GET", basePath+"/{id}:exportExerciseTcx", fmt.Sprintf("Export %s as TCX", id)
			default:
				continue
			}
			ep := map[string]interface{}{
				"method":      method,
				"path":        path,
				"description": desc,
				"dataType":    id,
			}
			// Pre-GA operations stay listed, tagged with the channel they need.
			if ch := dt.OperationChannel(op); channel.IsPreview(ch) {
				ep["channel"] = ch
				ep["available"] = channel.Allows(activeAPIVersion, ch)
			}
			endpoints = append(endpoints, ep)
		}
	}

	result := map[string]interface{}{
		"count":     len(endpoints),
		"endpoints": endpoints,
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return client.NewValidationError("failed to encode output", "")
	}

	return printOutput(json.RawMessage(data))
}

// extractFieldsFromDiscovery attempts to pull field info from the discovery JSON,
// resolving $ref references to show nested object properties.
func extractFieldsFromDiscovery(doc json.RawMessage, dataTypeID string) []map[string]interface{} {
	if doc == nil {
		return nil
	}

	var parsed struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil || len(parsed.Schemas) == 0 {
		return nil
	}

	// Try to find a schema matching the data type name.
	candidates := []string{
		dataTypeID,
		types.KebabToSnake(dataTypeID),
		strings.ReplaceAll(dataTypeID, "-", ""),
		toPascalCase(dataTypeID),
	}

	for _, candidate := range candidates {
		if schemaRaw, found := parsed.Schemas[candidate]; found {
			return parseSchemaFields(schemaRaw, parsed.Schemas)
		}
	}

	return nil
}

// schemaProperty represents a single property from a discovery schema.
type schemaProperty struct {
	Type        string   `json:"type"`
	Ref         string   `json:"$ref"`
	Description string   `json:"description"`
	Format      string   `json:"format"`
	Enum        []string `json:"enum"`
	Items       *struct {
		Ref string `json:"$ref"`
	} `json:"items"`
}

func parseSchemaFields(schemaRaw json.RawMessage, allSchemas map[string]json.RawMessage) []map[string]interface{} {
	var s struct {
		Properties map[string]schemaProperty `json:"properties"`
	}
	if err := json.Unmarshal(schemaRaw, &s); err != nil || len(s.Properties) == 0 {
		return nil
	}

	// Sort field names for consistent output.
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		prop := s.Properties[name]
		f := map[string]interface{}{
			"name": name,
		}

		// Determine type and resolve $ref.
		if prop.Ref != "" {
			f["type"] = "object"
			if subProps := resolveRefProperties(prop.Ref, allSchemas); len(subProps) > 0 {
				f["properties"] = subProps
			}
		} else if prop.Type == "array" && prop.Items != nil && prop.Items.Ref != "" {
			f["type"] = "array"
			if subProps := resolveRefProperties(prop.Items.Ref, allSchemas); len(subProps) > 0 {
				f["itemProperties"] = subProps
			}
		} else if prop.Type != "" {
			f["type"] = prop.Type
		}

		if prop.Format != "" {
			f["format"] = prop.Format
		}
		if prop.Description != "" {
			f["description"] = prop.Description
			// Extract required/optional from description prefix.
			if strings.HasPrefix(prop.Description, "Required.") {
				f["required"] = true
			}
		}
		if len(prop.Enum) > 0 {
			f["enum"] = prop.Enum
		}

		fields = append(fields, f)
	}
	return fields
}

// resolveRefProperties looks up a $ref schema and returns its property names.
func resolveRefProperties(ref string, allSchemas map[string]json.RawMessage) []string {
	schemaRaw, ok := allSchemas[ref]
	if !ok {
		return nil
	}
	var s struct {
		Properties map[string]interface{} `json:"properties"`
	}
	if err := json.Unmarshal(schemaRaw, &s); err != nil {
		return nil
	}
	props := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		props = append(props, name)
	}
	sort.Strings(props)
	return props
}

func toPascalCase(kebab string) string {
	parts := strings.Split(kebab, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
