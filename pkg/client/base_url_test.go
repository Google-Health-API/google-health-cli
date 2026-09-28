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

package client

import (
	"os"
	"testing"
)

func TestResolveBaseURLDefault(t *testing.T) {
	os.Unsetenv("GHEALTH_BASE_URL")
	if got := resolveBaseURL(); got != "https://health.googleapis.com/v4" {
		t.Fatalf("default = %q", got)
	}
}

func TestResolveBaseURLOverride(t *testing.T) {
	t.Setenv("GHEALTH_BASE_URL", "http://127.0.0.1:8787/v4")
	if got := resolveBaseURL(); got != "http://127.0.0.1:8787/v4" {
		t.Fatalf("override = %q", got)
	}
}

func TestResolveBaseURLRejectsUntrustedHost(t *testing.T) {
	t.Setenv("GHEALTH_BASE_URL", "https://evil.example.com/v4")
	t.Setenv(allowCustomBaseURLEnv, "")
	if got := resolveBaseURL(); got != defaultBaseURL {
		t.Fatalf("untrusted host should fall back to default, got %q", got)
	}
}

func TestResolveBaseURLCustomHostWithOptIn(t *testing.T) {
	t.Setenv("GHEALTH_BASE_URL", "https://proxy.corp.example/v4")
	t.Setenv(allowCustomBaseURLEnv, "1")
	if got := resolveBaseURL(); got != "https://proxy.corp.example/v4" {
		t.Fatalf("opt-in custom host = %q", got)
	}
}

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		raw         string
		allowCustom bool
		wantErr     bool
	}{
		{"https://health.googleapis.com/v4", false, false},
		{"https://staging-health.sandbox.googleapis.com/v4", false, false},
		{"http://localhost:8080/v4", false, false},
		{"http://[::1]:8080/v4", false, false},
		{"http://health.googleapis.com/v4", false, true},        // plain http to Google host
		{"https://googleapis.com.evil.example/v4", false, true}, // suffix trick
		{"https://evilgoogleapis.com/v4", false, true},          // missing dot boundary
		{"https://evil.example.com/v4", false, true},
		{"https://evil.example.com/v4", true, false},
		{"ftp://health.googleapis.com/v4", true, true},
		{"not a url", true, true},
	}
	for _, c := range cases {
		err := validateBaseURL(c.raw, c.allowCustom)
		if (err != nil) != c.wantErr {
			t.Errorf("validateBaseURL(%q, %v) err = %v, wantErr %v", c.raw, c.allowCustom, err, c.wantErr)
		}
	}
}
