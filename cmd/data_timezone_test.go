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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Google-Health-API/google-health-cli/pkg/types"
)

func writeTimezoneConfig(t *testing.T, dir, tz string) {
	t.Helper()
	content := "[default]\ntimezone = \"" + tz + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	resetActiveLocation()
}

// A configured IANA timezone wins over machine-local time when anchoring bare
// dates on physical-time fields. Asia/Kolkata is +05:30 year-round.
func TestBuildFilter_ConfiguredTimezoneAnchorsDay(t *testing.T) {
	dir := pinTZ(t, 0) // machine-local pinned to UTC — config must override it
	writeTimezoneConfig(t, dir, "Asia/Kolkata")

	f, err := buildFilter(types.Get("heart-rate"), "2026-06-08", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-06-08T00:00:00+05:30 == 2026-06-07T18:30:00Z
	want := `heart_rate.sample_time.physical_time >= "2026-06-07T18:30:00Z"`
	if f != want {
		t.Errorf("filter = %q, want %q (configured timezone must anchor the day)", f, want)
	}
}

// The configured timezone also drives the rollUp physical range.
func TestBuildRollupBody_ConfiguredTimezone(t *testing.T) {
	dir := pinTZ(t, 0)
	writeTimezoneConfig(t, dir, "Asia/Kolkata")

	body, err := buildRollupBody("2026-06-08", "2026-06-08", "86400s")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeBody(t, body)
	rng := m["range"].(map[string]interface{})
	if got := rng["startTime"]; got != "2026-06-07T18:30:00Z" {
		t.Errorf("startTime = %v, want 2026-06-07T18:30:00Z", got)
	}
	if got := rng["endTime"]; got != "2026-06-08T18:30:00Z" {
		t.Errorf("endTime = %v, want 2026-06-08T18:30:00Z", got)
	}
}

// An invalid configured timezone falls back to machine-local (with a stderr
// warning) rather than failing or silently using UTC.
func TestBuildFilter_InvalidTimezoneFallsBackToLocal(t *testing.T) {
	dir := pinTZ(t, 2*3600) // +02:00 machine-local
	writeTimezoneConfig(t, dir, "Mars/Olympus_Mons")

	f, err := buildFilter(types.Get("heart-rate"), "2026-06-08", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := `heart_rate.sample_time.physical_time >= "2026-06-07T22:00:00Z"`
	if f != want {
		t.Errorf("filter = %q, want %q (machine-local fallback)", f, want)
	}
}

// 'today' resolves to the current date in the configured timezone, not the
// machine-local one.
func TestParseDate_TodayUsesConfiguredTimezone(t *testing.T) {
	dir := pinTZ(t, 0)
	writeTimezoneConfig(t, dir, "Pacific/Kiritimati") // UTC+14, max skew from UTC

	got, err := parseDate("today")
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Pacific/Kiritimati")
	want := time.Now().In(loc).Format("2006-01-02") + "T00:00:00"
	if got != want {
		t.Errorf("parseDate(today) = %q, want %q (date in configured zone)", got, want)
	}
}
