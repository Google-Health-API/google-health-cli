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

// Civil filter fields (interval civil_start_time, sleep civil_end_time) do
// not accept timezone designators — reject zoned input at parse time with a
// clear validation error instead of passing it through to an API 400.
func TestBuildFilter_RejectsZonedTimestampOnCivilField(t *testing.T) {
	pinTZ(t, 0)

	_, err := buildFilter(types.Get("steps"), "2026-03-28T10:00:00+05:00", "", "")
	assertValidationError(t, err, "civil", "offset")

	_, err = buildFilter(types.Get("steps"), "", "2026-03-28T10:00:00+05:00", "")
	assertValidationError(t, err, "civil", "offset")

	// A trailing Z is also a timezone designator the civil fields reject.
	_, err = buildFilter(types.Get("steps"), "2026-03-28T10:00:00Z", "", "")
	assertValidationError(t, err, "civil")

	// sleep filters on a civil field via FilterField.
	_, err = buildFilter(types.Get("sleep"), "", "2026-03-28T10:00:00+05:00", "")
	assertValidationError(t, err, "civil")
}

// Physical-time fields still accept zoned values (converted to UTC), and
// zone-less civil datetimes still pass through.
func TestBuildFilter_ZonedTimestampStillValidElsewhere(t *testing.T) {
	pinTZ(t, 0)

	f, err := buildFilter(types.Get("heart-rate"), "2026-03-28T10:00:00+05:00", "", "")
	if err != nil {
		t.Fatalf("sample type should accept zoned input: %v", err)
	}
	want := `heart_rate.sample_time.physical_time >= "2026-03-28T05:00:00Z"`
	if f != want {
		t.Errorf("filter = %q, want %q", f, want)
	}

	f, err = buildFilter(types.Get("steps"), "2026-03-28T10:00:00", "", "")
	if err != nil {
		t.Fatalf("zone-less civil datetime should pass: %v", err)
	}
	if f != `steps.interval.civil_start_time >= "2026-03-28T10:00:00"` {
		t.Errorf("filter = %q", f)
	}
}
