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

// At a +02:00 host, a bare date on a physical-time (sample) field must anchor at
// LOCAL midnight, i.e. 2 hours before UTC midnight, so it lines up with the
// user's local day rather than UTC midnight.
func TestBuildFilter_SampleAnchorsLocalDay(t *testing.T) {
	pinTZ(t, 2*3600) // +02:00

	hr := types.Get("heart-rate")
	f, err := buildFilter(hr, "2026-06-08", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// local 2026-06-08T00:00:00+02:00 == 2026-06-07T22:00:00Z
	want := `heart_rate.sample_time.physical_time >= "2026-06-07T22:00:00Z"`
	if f != want {
		t.Errorf("from filter = %q, want %q", f, want)
	}

	// --to is inclusive of the named local day: exclusive bound is local
	// midnight of the *next* day == 2026-06-08T22:00:00Z.
	f2, err := buildFilter(hr, "", "2026-06-08", "")
	if err != nil {
		t.Fatal(err)
	}
	wantTo := `heart_rate.sample_time.physical_time < "2026-06-08T22:00:00Z"`
	if f2 != wantTo {
		t.Errorf("to filter = %q, want %q", f2, wantTo)
	}
}

// The SAME bare date must select the SAME local instant for a sample type and
// an interval type — interval uses civil (local wall-clock) directly, sample is
// anchored to the same local midnight then expressed in UTC.
func TestBuildFilter_SampleAndIntervalAgreeOnLocalDay(t *testing.T) {
	pinTZ(t, 1*3600) // +01:00

	sample, _ := buildFilter(types.Get("heart-rate"), "2026-06-08", "", "")
	interval, _ := buildFilter(types.Get("steps"), "2026-06-08", "", "")

	// sample: local 2026-06-08T00:00:00+01:00 -> 2026-06-07T23:00:00Z
	if sample != `heart_rate.sample_time.physical_time >= "2026-06-07T23:00:00Z"` {
		t.Errorf("sample filter = %q", sample)
	}
	// interval: civil time is the local wall clock, unchanged
	if interval != `steps.interval.civil_start_time >= "2026-06-08T00:00:00"` {
		t.Errorf("interval filter = %q", interval)
	}
	// Both denote the same instant (2026-06-08 00:00 local), expressed in their
	// native field conventions.
}

// On a UTC host the anchoring is a no-op (back-compatible).
func TestBuildFilter_UTCHostUnchanged(t *testing.T) {
	pinTZ(t, 0)
	f, _ := buildFilter(types.Get("heart-rate"), "2026-06-08", "", "")
	if f != `heart_rate.sample_time.physical_time >= "2026-06-08T00:00:00Z"` {
		t.Errorf("UTC host filter = %q, want UTC midnight", f)
	}
}

// parsePhysicalTime (rollUp range) anchors bare dates at the local day too.
func TestParsePhysicalTime_LocalDay(t *testing.T) {
	pinTZ(t, 2*3600) // +02:00
	got, err := parsePhysicalTime("2026-06-08")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-06-07T22:00:00Z" {
		t.Errorf("parsePhysicalTime = %q, want 2026-06-07T22:00:00Z (local midnight in UTC)", got)
	}
	// An explicit zoned value is converted as given.
	got2, _ := parsePhysicalTime("2026-06-08T12:00:00-05:00")
	if got2 != "2026-06-08T17:00:00Z" {
		t.Errorf("parsePhysicalTime zoned = %q, want 2026-06-08T17:00:00Z", got2)
	}
}
