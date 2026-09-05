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
	"testing"

	"github.com/Google-Health-API/google-health-cli/pkg/types"
)

// schema type <name> must describe the API's real parameters, never the
// fabricated startTime/endTime/bucketDuration names that v4 does not accept.
func TestBuildOperationParameters_ListUsesRealFilterField(t *testing.T) {
	params := buildOperationParameters(types.Get("weight"))

	list, ok := params["list"].(map[string]interface{})
	if !ok {
		t.Fatalf("no list parameters: %v", params)
	}
	wantFilter := `weight.sample_time.physical_time >= "<RFC3339>" AND weight.sample_time.physical_time < "<RFC3339>"`
	if list["filter"] != wantFilter {
		t.Errorf("filter template = %q, want %q", list["filter"], wantFilter)
	}
	for _, k := range []string{"pageSize", "pageToken"} {
		if _, ok := list[k]; !ok {
			t.Errorf("list parameters missing %q", k)
		}
	}
	for _, fabricated := range []string{"startTime", "endTime", "bucketDuration"} {
		if _, ok := list[fabricated]; ok {
			t.Errorf("list parameters contain fabricated %q", fabricated)
		}
	}
}

func TestBuildOperationParameters_RollupBody(t *testing.T) {
	params := buildOperationParameters(types.Get("weight"))

	rollup, ok := params["rollup"].(map[string]interface{})
	if !ok {
		t.Fatalf("no rollup parameters: %v", params)
	}
	body := rollup["body"].(map[string]interface{})
	rng := body["range"].(map[string]string)
	if _, ok := rng["startTime"]; !ok {
		t.Errorf("rollup body range missing startTime: %v", rng)
	}
	if _, ok := rng["endTime"]; !ok {
		t.Errorf("rollup body range missing endTime: %v", rng)
	}
	for _, k := range []string{"windowSize", "pageSize", "pageToken", "dataSourceFamily"} {
		if _, ok := body[k]; !ok {
			t.Errorf("rollup body missing %q", k)
		}
	}
	if _, ok := body["bucketDuration"]; ok {
		t.Errorf("rollup body contains fabricated bucketDuration")
	}
}

func TestBuildOperationParameters_DailyRollupBody(t *testing.T) {
	params := buildOperationParameters(types.Get("steps"))

	dr, ok := params["daily-rollup"].(map[string]interface{})
	if !ok {
		t.Fatalf("no daily-rollup parameters: %v", params)
	}
	body := dr["body"].(map[string]interface{})
	rng := body["range"].(map[string]string)
	if _, ok := rng["start"]; !ok {
		t.Errorf("daily-rollup range missing civil start: %v", rng)
	}
	if _, ok := rng["end"]; !ok {
		t.Errorf("daily-rollup range missing civil end: %v", rng)
	}
	for _, k := range []string{"windowSizeDays", "dataSourceFamily"} {
		if _, ok := body[k]; !ok {
			t.Errorf("daily-rollup body missing %q", k)
		}
	}
}

// Filter templates come from the registry's FilterPath, per time-field kind.
func TestFilterTemplate_PerTimeField(t *testing.T) {
	cases := []struct {
		typeID string
		want   string
	}{
		{"steps", `steps.interval.civil_start_time >= "<ISO8601, no offset>" AND steps.interval.civil_start_time < "<ISO8601, no offset>"`},
		{"sleep", `sleep.interval.civil_end_time >= "<ISO8601, no offset>" AND sleep.interval.civil_end_time < "<ISO8601, no offset>"`},
		{"daily-resting-heart-rate", `daily_resting_heart_rate.date >= "<YYYY-MM-DD>" AND daily_resting_heart_rate.date < "<YYYY-MM-DD>"`},
		// ECG supports only a lower bound.
		{"electrocardiogram", `electrocardiogram.interval.start_time >= "<RFC3339>"`},
		// Catalog types have no time filter.
		{"food", ""},
	}
	for _, c := range cases {
		if got := filterTemplate(types.Get(c.typeID)); got != c.want {
			t.Errorf("filterTemplate(%s) = %q, want %q", c.typeID, got, c.want)
		}
	}
}
