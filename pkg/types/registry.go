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

package types

import (
	"sort"
	"strings"
)

// Time field types for filter expression construction.
const (
	// TimeFieldInterval is for data types that span a time range and include
	// civilStartTime/civilEndTime in their response (steps, distance, altitude, etc.).
	// Filter: {type}.interval.civil_start_time
	TimeFieldInterval = "interval"
	// TimeFieldSample is for instantaneous measurements (heart rate, weight).
	// Filter: {type}.sample_time.physical_time
	TimeFieldSample = "sample"
	// TimeFieldDaily is for daily summary types (daily-resting-heart-rate, etc.).
	// Filter: {type}.date (ISO 8601 YYYY-MM-DD)
	TimeFieldDaily = "daily"
	// TimeFieldNone is for catalog/reference types that carry no timestamp
	// (food, food-measurement-unit). These do not support time-range filters.
	TimeFieldNone = "none"
	// TimeFieldPhysicalIntervalStart is for interval types whose API filter uses
	// the physical interval start time (RFC-3339, "Z") and supports ONLY a lower
	// bound (>=). The API rejects civil_start_time and any end-time bound for
	// these — e.g. electrocardiogram ("Filtering by end time is not supported
	// for ECG.").
	TimeFieldPhysicalIntervalStart = "physical_interval_start"
)

// DataType holds metadata about a Health API data type.
type DataType struct {
	ID          string   `json:"id"`          // kebab-case identifier used in CLI and API URLs
	FilterName  string   `json:"filter_name"` // snake_case name used in API filter expressions
	Category    string   `json:"category"`    // scope category
	Description string   `json:"description"`
	Operations  []string `json:"operations"`  // supported operations
	Writable    bool     `json:"writable"`    // supports create/update/delete
	RollupOnly  bool     `json:"rollup_only"` // only available via rollup, not list
	TimeField   string   `json:"time_field"`  // "interval", "sample", or "daily"
	// FilterField overrides the default filter path for --from/--to.
	// Format: "type.path.field" — the full filter expression path.
	// Uses ISO 8601 dates (no Z suffix). Empty means use TimeField default.
	FilterField string `json:"filter_field,omitempty"`
	// ShortRollupRange marks types whose rollup/dailyRollup range the API
	// caps at 14 days (heart-rate, total-calories, active-minutes,
	// calories-in-heart-rate-zone). All other rollup-capable types are
	// capped at 90 days.
	ShortRollupRange bool `json:"short_rollup_range,omitempty"`
	// SmallPageCap marks types whose list pageSize the API caps at 25
	// (exercise, sleep). All other types allow up to 10000 per page.
	SmallPageCap bool `json:"small_page_cap,omitempty"`
	// Channel is the API release channel the type is published on: "" or
	// "v4" for GA, "v4beta" for beta-only types. Pre-GA types stay listed
	// (marked e.g. "[beta]") but their commands refuse to run unless the
	// active --api-version is at least as permissive. Promoting a type to GA
	// is a one-line change: clear this field.
	Channel string `json:"channel,omitempty"`
	// OpChannels overrides Channel for individual operations, for when the
	// API gates a single operation (e.g. a beta-only reconcile on a GA type).
	OpChannels map[string]string `json:"op_channels,omitempty"`
}

// OperationChannel returns the channel an operation of this type requires:
// its OpChannels override if any, else the type's Channel, else "v4".
func (d *DataType) OperationChannel(op string) string {
	if ch, ok := d.OpChannels[op]; ok && ch != "" {
		return ch
	}
	if d.Channel != "" {
		return d.Channel
	}
	return "v4"
}

// RollupRangeCapDays returns the API's maximum rollup/dailyRollup range for
// this type, in days: 14 for the short-range set, 90 for everything else.
func (d *DataType) RollupRangeCapDays() int {
	if d.ShortRollupRange {
		return 14
	}
	return 90
}

// MaxPageSize returns the largest list pageSize the API accepts for this type:
// 25 for the small-page set (exercise, sleep), 10000 for everything else.
func (d *DataType) MaxPageSize() int {
	if d.SmallPageCap {
		return 25
	}
	return 10000
}

// ensureUTC appends "Z" to a timestamp if it doesn't already have a timezone.
// A timezone is either a trailing "Z" or a numeric offset (+HH:MM or -HH:MM) in
// the time portion after the "T". The "-" check is confined to the time portion
// so that the date separators in "2026-03-07" are not mistaken for an offset.
func ensureUTC(date string) string {
	if HasTimezone(date) {
		return date
	}
	return date + "Z"
}

// HasTimezone reports whether an ISO 8601 string already carries an explicit
// timezone designator (Z, +HH:MM, or -HH:MM).
func HasTimezone(date string) bool {
	if strings.HasSuffix(date, "Z") {
		return true
	}
	t := strings.IndexByte(date, 'T')
	if t < 0 {
		return false // date-only, no timezone
	}
	timePart := date[t+1:]
	return strings.ContainsAny(timePart, "+-")
}

// FilterPath returns the field path used in filter expressions for this type
// (e.g. "weight.sample_time.physical_time"), or "" when the type carries no
// time filter. This is the single source of truth for filter field names —
// build filter examples and templates from it, never by hand.
func (d *DataType) FilterPath() string {
	if d.TimeField == TimeFieldNone {
		return ""
	}
	if d.FilterField != "" {
		return d.FilterField
	}
	switch d.TimeField {
	case TimeFieldSample:
		return d.FilterName + ".sample_time.physical_time"
	case TimeFieldDaily:
		return d.FilterName + ".date"
	case TimeFieldPhysicalIntervalStart:
		return d.FilterName + ".interval.start_time"
	default:
		return d.FilterName + ".interval.civil_start_time"
	}
}

// FilterFrom returns the filter expression for a --from date constraint.
func (d *DataType) FilterFrom(date string) string {
	path := d.FilterPath()
	if path == "" {
		return ""
	}
	if d.FilterField != "" {
		return path + " >= \"" + date + "\""
	}
	switch d.TimeField {
	case TimeFieldSample, TimeFieldPhysicalIntervalStart:
		return path + " >= \"" + ensureUTC(date) + "\""
	case TimeFieldDaily:
		return path + " >= \"" + dateOnly(date) + "\""
	default:
		return path + " >= \"" + date + "\""
	}
}

// FilterTo returns the filter expression for a --to date constraint.
// The API only supports >= and < (not <= or >).
func (d *DataType) FilterTo(date string) string {
	path := d.FilterPath()
	if path == "" {
		return ""
	}
	if d.FilterField != "" {
		return path + " < \"" + date + "\""
	}
	switch d.TimeField {
	case TimeFieldSample:
		return path + " < \"" + ensureUTC(date) + "\""
	case TimeFieldDaily:
		return path + " < \"" + dateOnly(date) + "\""
	case TimeFieldPhysicalIntervalStart:
		// The API rejects an end-time bound for these types, so emit none.
		return ""
	default:
		return path + " < \"" + date + "\""
	}
}

// dateOnly extracts just the YYYY-MM-DD portion from a date string.
func dateOnly(date string) string {
	if len(date) >= 10 {
		return date[:10]
	}
	return date
}

// Scope returns the readonly scope suffix for this data type.
func (d *DataType) Scope() string {
	return d.Category + ".readonly"
}

// FullScope returns the full OAuth scope URL for readonly access.
func (d *DataType) FullScope() string {
	return "https://www.googleapis.com/auth/googlehealth." + d.Scope()
}

// KebabToSnake converts a kebab-case string to snake_case for filter expressions.
func KebabToSnake(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// Registry is the global data type registry.
var Registry = map[string]*DataType{}

// All returns all registered data types.
func All() []*DataType {
	result := make([]*DataType, 0, len(Registry))
	for _, dt := range Registry {
		result = append(result, dt)
	}
	return result
}

// Get returns a data type by its kebab-case ID, or nil if not found.
func Get(id string) *DataType {
	return Registry[id]
}

// IDs returns all data type IDs (sorted).
func IDs() []string {
	ids := make([]string, 0, len(Registry))
	for id := range Registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func register(dt *DataType) {
	Registry[dt.ID] = dt
}

func init() {
	// Data types verified against the live Health API v4 (2026-03-30).
	// Only types the API accepts are registered. Operations are confirmed
	// by probing each endpoint.

	interval := TimeFieldInterval
	sample := TimeFieldSample
	daily := TimeFieldDaily

	// --- Activity & Fitness (verified via live API) ---

	register(&DataType{
		ID: "steps", FilterName: "steps", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Step count data (use daily-rollup for totals)",
		Operations:  []string{"list", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "heart-rate", FilterName: "heart_rate", TimeField: sample,
		Category:         "activity_and_fitness",
		Description:      "Heart rate in beats per minute",
		Operations:       []string{"list", "rollup", "daily-rollup", "reconcile"},
		ShortRollupRange: true,
	})
	register(&DataType{
		ID: "exercise", FilterName: "exercise", TimeField: interval,
		Category:     "activity_and_fitness",
		Description:  "Exercise and workout sessions",
		Operations:   []string{"list", "get", "create", "update", "delete", "reconcile", "export-tcx"},
		Writable:     true,
		SmallPageCap: true, // API caps exercise list at 25 rows/page
	})
	register(&DataType{
		ID: "distance", FilterName: "distance", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Distance traveled (use daily-rollup for totals in mm)",
		Operations:  []string{"list", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "active-zone-minutes", FilterName: "active_zone_minutes", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Active zone minutes with heart rate zone breakdown",
		Operations:  []string{"list", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "altitude", FilterName: "altitude", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Altitude data",
		Operations:  []string{"list", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "basal-energy-burned", FilterName: "basal_energy_burned", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Basal energy burned from BMR (kcal per interval)",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "active-energy-burned", FilterName: "active_energy_burned", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Active energy burned from activity (kcal per interval)",
		Operations:  []string{"list", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "vo2-max", FilterName: "vo2_max", TimeField: sample,
		Category:    "activity_and_fitness",
		Description: "VO2 max estimation",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "heart-rate-variability", FilterName: "heart_rate_variability", TimeField: sample,
		Category:    "activity_and_fitness",
		Description: "Heart rate variability (HRV)",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "activity-level", FilterName: "activity_level", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Activity level breakdown (sedentary, light, moderate, vigorous)",
		Operations:  []string{"list", "reconcile"},
	})

	// --- Rollup-only activity types ---

	register(&DataType{
		ID: "floors", FilterName: "floors", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Floors climbed",
		Operations:  []string{"rollup", "daily-rollup", "reconcile"},
		RollupOnly:  true,
	})
	register(&DataType{
		ID: "active-minutes", FilterName: "active_minutes", TimeField: interval,
		Category:         "activity_and_fitness",
		Description:      "Active minutes",
		Operations:       []string{"rollup", "daily-rollup", "reconcile"},
		RollupOnly:       true,
		ShortRollupRange: true,
	})

	// --- Health Metrics & Measurements ---

	register(&DataType{
		ID: "weight", FilterName: "weight", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Body weight (weightGrams)",
		Operations:  []string{"list", "get", "create", "update", "delete", "rollup", "daily-rollup", "reconcile"},
		Writable:    true,
	})
	register(&DataType{
		ID: "body-fat", FilterName: "body_fat", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Body fat percentage",
		Operations:  []string{"list", "get", "create", "update", "delete", "rollup", "daily-rollup", "reconcile"},
		Writable:    true,
	})
	register(&DataType{
		ID: "height", FilterName: "height", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Body height (heightMillimeters)",
		Operations:  []string{"list", "get", "create", "update", "delete", "reconcile"},
		Writable:    true,
	})
	register(&DataType{
		ID: "oxygen-saturation", FilterName: "oxygen_saturation", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Blood oxygen saturation (SpO2)",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "blood-glucose", FilterName: "blood_glucose", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Blood glucose (mg/dL) with meal and measurement context",
		Operations:  []string{"list", "get", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "core-body-temperature", FilterName: "core_body_temperature", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Core body temperature (Celsius)",
		Operations:  []string{"list", "get", "rollup", "daily-rollup", "reconcile"},
	})

	// --- Sleep ---

	register(&DataType{
		ID: "sleep", FilterName: "sleep", TimeField: interval,
		Category:     "sleep",
		Description:  "Sleep sessions with stages, summary, and duration",
		Operations:   []string{"list", "get", "create", "update", "delete", "reconcile"},
		Writable:     true,
		FilterField:  "sleep.interval.civil_end_time", // sleep only supports end_time filtering
		SmallPageCap: true,                            // API caps sleep list at 25 rows/page
	})

	// --- Daily summaries (date-based, no interval or sample time) ---

	register(&DataType{
		ID: "daily-resting-heart-rate", FilterName: "daily_resting_heart_rate", TimeField: daily,
		Category:    "health_metrics_and_measurements",
		Description: "Daily resting heart rate",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "daily-heart-rate-variability", FilterName: "daily_heart_rate_variability", TimeField: daily,
		Category:    "health_metrics_and_measurements",
		Description: "Daily heart rate variability summary",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "daily-oxygen-saturation", FilterName: "daily_oxygen_saturation", TimeField: daily,
		Category:    "health_metrics_and_measurements",
		Description: "Daily oxygen saturation summary",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "daily-respiratory-rate", FilterName: "daily_respiratory_rate", TimeField: daily,
		Category:    "health_metrics_and_measurements",
		Description: "Daily respiratory rate summary",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "daily-vo2-max", FilterName: "daily_vo2_max", TimeField: daily,
		Category:    "activity_and_fitness",
		Description: "Daily VO2 max summary",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "daily-sleep-temperature-derivations", FilterName: "daily_sleep_temperature_derivations", TimeField: daily,
		Category:    "health_metrics_and_measurements",
		Description: "Daily sleep temperature deviation from baseline",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "daily-heart-rate-zones", FilterName: "daily_heart_rate_zones", TimeField: daily,
		Category:    "health_metrics_and_measurements",
		Description: "Daily heart rate zone breakdown",
		Operations:  []string{"reconcile"},
		RollupOnly:  true,
	})

	// --- Additional sample/interval types ---

	register(&DataType{
		ID: "respiratory-rate-sleep-summary", FilterName: "respiratory_rate_sleep_summary", TimeField: sample,
		Category:    "health_metrics_and_measurements",
		Description: "Respiratory rate during sleep (per-stage breakdown)",
		Operations:  []string{"list", "reconcile"},
	})
	register(&DataType{
		ID: "run-vo2-max", FilterName: "run_vo2_max", TimeField: sample,
		Category:    "activity_and_fitness",
		Description: "VO2 max estimated from running activities",
		Operations:  []string{"list", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "sedentary-period", FilterName: "sedentary_period", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Sedentary periods detected by the device",
		Operations:  []string{"list", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "swim-lengths-data", FilterName: "swim_lengths_data", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Swim lengths with stroke type and count (use daily-rollup for strokeCountSum)",
		Operations:  []string{"list", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "hydration-log", FilterName: "hydration_log", TimeField: interval,
		Category:    "nutrition",
		Description: "Hydration log entries",
		Operations:  []string{"list", "get", "daily-rollup", "reconcile"},
	})

	// --- Rollup-only types (no list) ---

	register(&DataType{
		ID: "total-calories", FilterName: "total_calories", TimeField: interval,
		Category:         "activity_and_fitness",
		Description:      "Total calories burned (use daily-rollup for kcalSum)",
		Operations:       []string{"daily-rollup"},
		RollupOnly:       true,
		ShortRollupRange: true,
	})
	register(&DataType{
		ID: "time-in-heart-rate-zone", FilterName: "time_in_heart_rate_zone", TimeField: interval,
		Category:    "activity_and_fitness",
		Description: "Time spent in each heart rate zone",
		Operations:  []string{"daily-rollup", "reconcile"},
		RollupOnly:  true,
	})
	register(&DataType{
		ID: "calories-in-heart-rate-zone", FilterName: "calories_in_heart_rate_zone", TimeField: interval,
		Category:         "activity_and_fitness",
		Description:      "Calories burned per heart rate zone (rollup-only)",
		Operations:       []string{"rollup", "daily-rollup", "reconcile"},
		RollupOnly:       true,
		ShortRollupRange: true,
	})

	// --- Cardiac (dedicated ecg / irn scopes) ---

	register(&DataType{
		// ECG filters on physical interval.start_time and supports only a lower
		// bound (>=); civil_start_time and end-time bounds are rejected by the API.
		ID: "electrocardiogram", FilterName: "electrocardiogram", TimeField: TimeFieldPhysicalIntervalStart,
		Category:    "ecg",
		Description: "ECG recordings with waveform samples and rhythm classification (requires ecg.readonly)",
		Operations:  []string{"list"},
	})
	register(&DataType{
		ID: "irregular-rhythm-notification", FilterName: "irregular_rhythm_notification", TimeField: interval,
		Category:    "irn",
		Description: "Irregular rhythm notifications with alert windows (requires irn.readonly)",
		Operations:  []string{"list"},
	})

	// --- Nutrition ---

	register(&DataType{
		ID: "nutrition-log", FilterName: "nutrition_log", TimeField: interval,
		Category:    "nutrition",
		Description: "Logged food/nutrition entries with nutrient and energy breakdown",
		Operations:  []string{"list", "get", "rollup", "daily-rollup", "reconcile"},
	})
	register(&DataType{
		ID: "food", FilterName: "food", TimeField: TimeFieldNone,
		Category:    "nutrition",
		Description: "Food catalog entries with nutrient profiles (reference data, no time filter)",
		Operations:  []string{"list", "get"},
	})
	register(&DataType{
		ID: "food-measurement-unit", FilterName: "food_measurement_unit", TimeField: TimeFieldNone,
		Category:    "nutrition",
		Description: "Food measurement units (reference data, no time filter)",
		Operations:  []string{"list", "get"},
	})
}
