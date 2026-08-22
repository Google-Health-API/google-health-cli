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

package auth

import (
	"slices"
	"testing"
)

func allScopeSuffixes() []string {
	out := make([]string, 0, len(AllScopes))
	for _, s := range AllScopes {
		out = append(out, s.Suffix)
	}
	return out
}

func TestScopePresetAllExcludesCloudPlatform(t *testing.T) {
	scopes, err := ScopePreset("all")
	if err != nil {
		t.Fatalf("ScopePreset(all) returned error: %v", err)
	}
	if slices.Contains(scopes, "cloud-platform") {
		t.Errorf("ScopePreset(all) includes cloud-platform; data-plane endpoints reject tokens carrying it")
	}
	// Every non-webhook scope must still be present.
	for _, s := range AllScopes {
		if s.Suffix == "cloud-platform" {
			continue
		}
		if !slices.Contains(scopes, s.Suffix) {
			t.Errorf("ScopePreset(all) is missing health-data scope %q", s.Suffix)
		}
	}
}

func TestScopePresetReadonlyExcludesCloudPlatform(t *testing.T) {
	scopes, err := ScopePreset("readonly")
	if err != nil {
		t.Fatalf("ScopePreset(readonly) returned error: %v", err)
	}
	if slices.Contains(scopes, "cloud-platform") {
		t.Errorf("ScopePreset(readonly) includes cloud-platform")
	}
	if !slices.Contains(scopes, "activity_and_fitness.readonly") {
		t.Errorf("ScopePreset(readonly) is missing activity_and_fitness.readonly")
	}
}

func TestScopePresetCategoryExpansionStillWorks(t *testing.T) {
	scopes, err := ScopePreset("sleep,activity_and_fitness")
	if err != nil {
		t.Fatalf("ScopePreset(sleep,activity_and_fitness) returned error: %v", err)
	}
	want := []string{"sleep.readonly", "activity_and_fitness.readonly"}
	for _, w := range want {
		if !slices.Contains(scopes, w) {
			t.Errorf("category expansion missing %q; got %v", w, scopes)
		}
	}
}

func TestScopePresetUnknownNameErrors(t *testing.T) {
	if _, err := ScopePreset("no-such-preset"); err == nil {
		t.Fatal("expected error for unknown preset name, got nil")
	}
}

func TestScopePresetAllCoversEveryNonWebhookSuffix(t *testing.T) {
	// Guard against a future scope being added to AllScopes but silently
	// dropped from the "all" expansion.
	all, err := ScopePreset("all")
	if err != nil {
		t.Fatalf("ScopePreset(all) returned error: %v", err)
	}
	total := len(allScopeSuffixes()) - 1 // minus cloud-platform
	if len(all) != total {
		t.Errorf("ScopePreset(all) returned %d scopes, want %d", len(all), total)
	}
}
