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

package channel

import "testing"

func TestAllows_SupersetRule(t *testing.T) {
	cases := []struct {
		active, required string
		want             bool
	}{
		{"v4", "", true},
		{"v4", "v4", true},
		{"v4", "v4beta", false},
		{"v4beta", "v4", true},
		{"v4beta", "", true},
		{"v4beta", "v4beta", true},
		{"V4BETA", "v4beta", true}, // case-insensitive
		{"bogus", "", true},        // unknown active: GA only
		{"bogus", "v4beta", false},
		{"v4beta", "bogus", false}, // unknown required: never
	}
	for _, c := range cases {
		if got := Allows(c.active, c.required); got != c.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", c.active, c.required, got, c.want)
		}
	}
}

func TestIsSelectable(t *testing.T) {
	for _, v := range []string{"v4", "v4beta", " V4Beta "} {
		if !IsSelectable(v) {
			t.Errorf("IsSelectable(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "v5", "beta"} {
		if IsSelectable(v) {
			t.Errorf("IsSelectable(%q) = true, want false", v)
		}
	}
}

func TestFromBaseURL(t *testing.T) {
	cases := []struct {
		url    string
		want   string
		wantOK bool
	}{
		{"https://health.googleapis.com/v4", "v4", true},
		{"https://health.googleapis.com/v4beta", "v4beta", true},
		{"https://health.googleapis.com/v4beta/", "v4beta", true},
		{"http://127.0.0.1:8080/v4", "v4", true},
		{"http://127.0.0.1:8080/v5", "", false},
		{"http://127.0.0.1:8080/api", "", false},
		{"http://127.0.0.1:8080", "", false},
	}
	for _, c := range cases {
		got, ok := FromBaseURL(c.url)
		if got != c.want || ok != c.wantOK {
			t.Errorf("FromBaseURL(%q) = (%q, %v), want (%q, %v)", c.url, got, ok, c.want, c.wantOK)
		}
	}
}

func TestLabel(t *testing.T) {
	if Label("v4beta") != "[beta]" || Label("v4") != "" || Label("") != "" {
		t.Errorf("unexpected labels: %q %q %q", Label("v4beta"), Label("v4"), Label(""))
	}
}
