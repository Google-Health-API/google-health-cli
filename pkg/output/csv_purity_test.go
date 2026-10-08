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

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// An empty tabular result in CSV mode must produce an empty stream, never a JSON
// object dumped into the CSV (which breaks pd.read_csv). A non-tabular response
// keeps the JSON fallback.
func TestPrintCSV_EmptyStreamPurity(t *testing.T) {
	cases := []struct {
		name     string
		data     string
		wantList bool
	}{
		{"empty dataPoints + hints", `{"dataPoints":[],"_hints":["no data"]}`, true},
		{"empty bare array", `[]`, true},
		{"empty rollupDataPoints", `{"rollupDataPoints":[]}`, true},
		{"non-tabular object", `{"authenticated":true,"email":"x@y.z"}`, false},
	}
	for _, c := range cases {
		if got := isListShaped(json.RawMessage(c.data)); got != c.wantList {
			t.Errorf("%s: isListShaped=%v want %v", c.name, got, c.wantList)
		}
	}
}

// fwarnDroppedSignals must route _hints and nextPageToken to the given writer
// (stderr in production) so a --limit-capped CSV result is not misread as
// complete — and they must never land in the data stream.
func TestFwarnDroppedSignals(t *testing.T) {
	var buf bytes.Buffer
	fwarnDroppedSignals(&buf, json.RawMessage(`{"dataPoints":[{"a":1}],"_hints":["more data"],"nextPageToken":"ABC"}`))
	out := buf.String()
	if !strings.Contains(out, "hint: more data") {
		t.Errorf("hint not surfaced: %q", out)
	}
	if !strings.Contains(out, "nextPageToken: ABC") {
		t.Errorf("token not surfaced: %q", out)
	}
}

// A pre-GA _api_version must be surfaced on stderr in table/csv mode (rows
// drop _-prefixed keys); the default v4 channel stays quiet.
func TestFwarnDroppedSignals_APIVersion(t *testing.T) {
	var buf bytes.Buffer
	fwarnDroppedSignals(&buf, json.RawMessage(`{"dataPoints":[],"_api_version":"v4beta"}`))
	if !strings.Contains(buf.String(), "api_version: v4beta") {
		t.Errorf("stderr = %q, want api_version: v4beta", buf.String())
	}
	buf.Reset()
	fwarnDroppedSignals(&buf, json.RawMessage(`{"dataPoints":[],"_api_version":"v4"}`))
	if buf.Len() != 0 {
		t.Errorf("v4 must stay quiet, got %q", buf.String())
	}
}
