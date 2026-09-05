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
	"strings"
	"testing"

	"github.com/Google-Health-API/google-health-cli/pkg/types"
)

func TestParseEndDate(t *testing.T) {
	cases := []struct {
		in, want string
		desc     string
	}{
		{"2026-03-07", "2026-03-08T00:00:00", "bare date advances one day (inclusive end)"},
		{"2026-02-28", "2026-03-01T00:00:00", "month rollover"},
		{"2026-12-31", "2027-01-01T00:00:00", "year rollover"},
		// A precise time-of-day is an exact bound — passed through unchanged.
		{"2026-03-07T10:30:00", "2026-03-07T10:30:00", "datetime passthrough"},
		{"2026-03-07T10:30:00-05:00", "2026-03-07T10:30:00-05:00", "zoned datetime passthrough"},
	}
	for _, c := range cases {
		got, err := parseEndDate(c.in)
		if err != nil {
			t.Fatalf("parseEndDate(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("%s: parseEndDate(%q) = %q, want %q", c.desc, c.in, got, c.want)
		}
	}
}

func TestBuildFilter_ToIsInclusiveOfNamedDay(t *testing.T) {
	pinTZ(t, 0) // UTC host: physical-time anchoring is a no-op
	cases := []struct {
		typeID  string
		wantSub string
		desc    string
	}{
		{"daily-resting-heart-rate", `.date < "2026-03-08"`, "daily: includes the 7th"},
		{"heart-rate", `.physical_time < "2026-03-08T00:00:00Z"`, "sample: includes all of the 7th"},
		{"steps", `.civil_start_time < "2026-03-08T00:00:00"`, "interval: includes all of the 7th"},
	}
	for _, c := range cases {
		dt := types.Get(c.typeID)
		if dt == nil {
			t.Fatalf("unknown type %q", c.typeID)
		}
		f, err := buildFilter(dt, "", "2026-03-07", "")
		if err != nil {
			t.Fatalf("buildFilter(%s) error: %v", c.typeID, err)
		}
		if !strings.Contains(f, c.wantSub) {
			t.Errorf("%s: buildFilter = %q, want substring %q", c.desc, f, c.wantSub)
		}
	}
}

func TestBuildFilter_FromIsInclusiveStartOfDay(t *testing.T) {
	dt := types.Get("steps")
	f, err := buildFilter(dt, "2026-03-07", "", "")
	if err != nil {
		t.Fatalf("buildFilter error: %v", err)
	}
	want := `steps.interval.civil_start_time >= "2026-03-07T00:00:00"`
	if f != want {
		t.Errorf("buildFilter from = %q, want %q", f, want)
	}
}

func TestBuildFilter_PreciseToNotAdvanced(t *testing.T) {
	pinTZ(t, 0) // UTC host: physical-time anchoring is a no-op
	// A --to with an explicit time must remain an exact exclusive bound.
	dt := types.Get("heart-rate")
	f, err := buildFilter(dt, "", "2026-03-07T12:00:00", "")
	if err != nil {
		t.Fatalf("buildFilter error: %v", err)
	}
	want := `heart_rate.sample_time.physical_time < "2026-03-07T12:00:00Z"`
	if f != want {
		t.Errorf("buildFilter precise to = %q, want %q", f, want)
	}
}

func TestBuildFilter_RawOverrides(t *testing.T) {
	dt := types.Get("steps")
	f, err := buildFilter(dt, "2026-01-01", "2026-02-01", `steps.count > 0`)
	if err != nil {
		t.Fatalf("buildFilter error: %v", err)
	}
	if f != "steps.count > 0" {
		t.Errorf("raw filter should override --from/--to, got %q", f)
	}
}
