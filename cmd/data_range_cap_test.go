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

	"github.com/Google-Health-API/google-health-cli/pkg/client"
	"github.com/Google-Health-API/google-health-cli/pkg/types"
)

func TestRollupRangeCapDays_Registry(t *testing.T) {
	short := []string{"heart-rate", "total-calories", "active-minutes", "calories-in-heart-rate-zone"}
	for _, id := range short {
		if got := types.Get(id).RollupRangeCapDays(); got != 14 {
			t.Errorf("%s cap = %d, want 14", id, got)
		}
	}
	for _, id := range []string{"steps", "weight", "distance", "floors"} {
		if got := types.Get(id).RollupRangeCapDays(); got != 90 {
			t.Errorf("%s cap = %d, want 90", id, got)
		}
	}
	// Exactly the four short-range types carry the flag.
	var flagged int
	for _, dt := range types.All() {
		if dt.ShortRollupRange {
			flagged++
		}
	}
	if flagged != len(short) {
		t.Errorf("%d types flagged ShortRollupRange, want %d", flagged, len(short))
	}
}

func assertValidationError(t *testing.T, err error, wantSubstrs ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a validation error, got nil")
	}
	cliErr, ok := err.(*client.CLIError)
	if !ok {
		t.Fatalf("got %T (%v), want *client.CLIError", err, err)
	}
	if cliErr.Code != client.ExitValidation {
		t.Errorf("exit code = %d, want %d (validation)", cliErr.Code, client.ExitValidation)
	}
	full := cliErr.Message + " " + cliErr.Hint
	for _, s := range wantSubstrs {
		if !strings.Contains(full, s) {
			t.Errorf("error %q missing %q", full, s)
		}
	}
}

// rollup: 14-day types reject ranges over 14 days, accept exactly 14
// (--to is inclusive, so from Jan 1 to Jan 14 = 14 days).
func TestValidateRollupRange_ShortType(t *testing.T) {
	pinTZ(t, 0)
	hr := types.Get("heart-rate")

	if err := validateRollupRange(hr, "2026-01-01", "2026-01-14"); err != nil {
		t.Errorf("14-day range should pass: %v", err)
	}
	err := validateRollupRange(hr, "2026-01-01", "2026-01-15")
	assertValidationError(t, err, "14", "heart-rate", "chunk")
}

// rollup: other types are capped at 90 days.
func TestValidateRollupRange_StandardType(t *testing.T) {
	pinTZ(t, 0)
	steps := types.Get("steps")

	if err := validateRollupRange(steps, "2026-01-01", "2026-03-31"); err != nil {
		t.Errorf("90-day range should pass: %v", err)
	}
	err := validateRollupRange(steps, "2026-01-01", "2026-04-01")
	assertValidationError(t, err, "90", "steps")
}

// daily-rollup: same caps on the civil range.
func TestValidateDailyRollupRange(t *testing.T) {
	pinTZ(t, 0)
	hr := types.Get("heart-rate")

	if err := validateDailyRollupRange(hr, "2026-01-01", "2026-01-14"); err != nil {
		t.Errorf("14-day civil range should pass: %v", err)
	}
	err := validateDailyRollupRange(hr, "2026-01-01", "2026-01-15")
	assertValidationError(t, err, "14")

	steps := types.Get("steps")
	if err := validateDailyRollupRange(steps, "2026-01-01", "2026-03-31"); err != nil {
		t.Errorf("90-day civil range should pass: %v", err)
	}
	err = validateDailyRollupRange(steps, "2026-01-01", "2026-04-01")
	assertValidationError(t, err, "90")
}
