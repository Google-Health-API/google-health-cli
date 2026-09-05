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

// buildFilter for ECG must produce a physical start_time lower-bound filter and
// must NOT include any end-time bound, even when --to is supplied.
func TestBuildFilter_ECG(t *testing.T) {
	pinTZ(t, 0) // UTC host: physical-time anchoring is a no-op
	ecg := types.Get("electrocardiogram")

	// from only
	f, err := buildFilter(ecg, "2026-05-01", "", "")
	if err != nil {
		t.Fatalf("buildFilter error: %v", err)
	}
	want := `electrocardiogram.interval.start_time >= "2026-05-01T00:00:00Z"`
	if f != want {
		t.Errorf("from-only filter = %q, want %q", f, want)
	}

	// from + to: the end bound must be dropped (no "<", no civil_start_time)
	f2, err := buildFilter(ecg, "2026-05-01", "2026-06-08", "")
	if err != nil {
		t.Fatalf("buildFilter error: %v", err)
	}
	if strings.Contains(f2, "<") {
		t.Errorf("ECG filter must not contain an end bound, got %q", f2)
	}
	if strings.Contains(f2, "civil_start_time") {
		t.Errorf("ECG filter must not use civil_start_time, got %q", f2)
	}
	if f2 != want {
		t.Errorf("from+to filter = %q, want just the start bound %q", f2, want)
	}
}
