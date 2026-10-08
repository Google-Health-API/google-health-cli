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

package output

import "encoding/json"

// EnsureEnvelope normalizes any tabular response to one stable shape:
//
//	{"dataPoints": [...], "_hints"?: [...], "nextPageToken"?: "..."}
//
// It is the single source of truth for the output schema of list, get, rollup,
// daily-rollup, and reconcile. The simplify + hint layers can legitimately
// produce four different containers (a bare array for token-less rollups, a
// "data"-wrapped array when hints fire, "rollupDataPoints" when a continuation
// token survives, or "dataPoints" for list/reconcile/sleep) and an empty rollup
// carries no rows key at all. Agents must not have to handle that variety, so
// every tabular path runs its result through here last.
//
// Row bytes are moved, never re-parsed, so value precision and key order inside
// each row are preserved exactly. _hints and nextPageToken (and any other
// top-level metadata) are preserved.
func EnsureEnvelope(data json.RawMessage) json.RawMessage {
	// A bare top-level array (token-less rollup) → wrap under dataPoints. The
	// row bytes are embedded as-is (no re-marshal). A literal `null` decodes to
	// a nil slice; emit `[]` so dataPoints is always an indexable array.
	var asArray []json.RawMessage
	if json.Unmarshal(data, &asArray) == nil {
		rows := data
		if asArray == nil {
			rows = json.RawMessage("[]")
		}
		out, _ := json.MarshalIndent(map[string]json.RawMessage{"dataPoints": rows}, "", "  ")
		return out
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return data // not an object or array (e.g. a scalar) — leave untouched
	}

	// Already canonical.
	if _, ok := obj["dataPoints"]; ok {
		return data
	}

	// Rename the legacy/alternate row containers to dataPoints.
	for _, key := range []string{"rollupDataPoints", "data", "items"} {
		if rows, ok := obj[key]; ok {
			delete(obj, key)
			obj["dataPoints"] = rows
			out, _ := json.MarshalIndent(obj, "", "  ")
			return out
		}
	}

	// Object with no row container at all (empty rollup → {"_hints":...} or {}).
	// Guarantee the key exists so consumers can always index dataPoints.
	obj["dataPoints"] = json.RawMessage("[]")
	out, _ := json.MarshalIndent(obj, "", "  ")
	return out
}

// APIVersionKey is the top-level envelope key recording which API channel
// (e.g. "v4", "v4beta") answered. Like _hints it is CLI metadata, not API
// payload, so it is underscore-prefixed and dropped from table/csv rows.
const APIVersionKey = "_api_version"

// WithAPIVersion adds _api_version to an enveloped (object) response so agents
// can always tell which channel served the data. Non-object payloads are
// returned unchanged. Row bytes are moved, never re-parsed.
func WithAPIVersion(data json.RawMessage, apiVersion string) json.RawMessage {
	if apiVersion == "" {
		return data
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return data
	}
	v, _ := json.Marshal(apiVersion)
	obj[APIVersionKey] = v
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return data
	}
	return out
}
