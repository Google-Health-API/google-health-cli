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

import "testing"

func has(ops []string, op string) bool {
	for _, o := range ops {
		if o == op {
			return true
		}
	}
	return false
}

// nutrition-log and hydration-log accept create and batchDelete (verified
// against the live v4 API), but PATCH on either returns 500 INTERNAL — with a
// field mask and without one. They are therefore create/delete-only: "update"
// must stay absent so the CLI does not expose a command that always fails.
func TestNutritionLogsAreCreateDeleteOnly(t *testing.T) {
	for _, id := range []string{"nutrition-log", "hydration-log"} {
		dt := Get(id)
		if dt == nil {
			t.Fatalf("%s: not registered", id)
		}
		if !dt.Writable {
			t.Errorf("%s: Writable = false, want true", id)
		}
		for _, op := range []string{"create", "delete"} {
			if !has(dt.Operations, op) {
				t.Errorf("%s: missing %q in Operations %v", id, op, dt.Operations)
			}
		}
		if has(dt.Operations, "update") {
			t.Errorf("%s: has \"update\", but PATCH returns 500 INTERNAL on this type", id)
		}
	}
}

// The food catalog is reference data: read-only for API clients. A custom food
// is expressed as an anonymous nutrition-log entry (inline nutrients), not as a
// new catalog entry.
func TestFoodCatalogStaysReadOnly(t *testing.T) {
	for _, id := range []string{"food", "food-measurement-unit"} {
		dt := Get(id)
		if dt == nil {
			t.Fatalf("%s: not registered", id)
		}
		if dt.Writable {
			t.Errorf("%s: Writable = true, want false (catalog is read-only)", id)
		}
		for _, op := range []string{"create", "update", "delete"} {
			if has(dt.Operations, op) {
				t.Errorf("%s: unexpected %q in Operations %v", id, op, dt.Operations)
			}
		}
	}
}
