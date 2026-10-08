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

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// GetFormat resolution order: flag > GHEALTH_FORMAT env > configured profile
// format > "json". The profile step is what 'ghealth config set format'
// writes, so it must actually be honored.
func TestGetFormat_UsesConfiguredProfileFormat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	t.Setenv("GHEALTH_FORMAT", "")
	os.Unsetenv("GHEALTH_FORMAT")

	if err := os.WriteFile(filepath.Join(dir, ConfigFileName),
		[]byte("[default]\nformat = \"table\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := GetFormat(""); got != "table" {
		t.Errorf("GetFormat(\"\") = %q, want %q from config.toml", got, "table")
	}
	if got := GetFormat("csv"); got != "csv" {
		t.Errorf("flag must override config: got %q, want csv", got)
	}
	t.Setenv("GHEALTH_FORMAT", "json")
	if got := GetFormat(""); got != "json" {
		t.Errorf("env must override config: got %q, want json", got)
	}
}

func TestGetFormat_DefaultsToJSON(t *testing.T) {
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	os.Unsetenv("GHEALTH_FORMAT")
	if got := GetFormat(""); got != "json" {
		t.Errorf("GetFormat(\"\") = %q, want json", got)
	}
}

// ResolveAPIVersion order: flag > GHEALTH_API_VERSION > profile api_version >
// "v4", mirroring GetFormat.
func TestResolveAPIVersion_Order(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	os.Unsetenv("GHEALTH_API_VERSION")

	if v, src := ResolveAPIVersion(""); v != "v4" || src != APIVersionSourceDefault {
		t.Errorf("default = (%q, %q), want (v4, default)", v, src)
	}

	if err := os.WriteFile(filepath.Join(dir, ConfigFileName),
		[]byte("[default]\napi_version = \"v4beta\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if v, src := ResolveAPIVersion(""); v != "v4beta" || src != APIVersionSourceProfile {
		t.Errorf("profile = (%q, %q), want (v4beta, profile)", v, src)
	}

	t.Setenv("GHEALTH_API_VERSION", "v4")
	if v, src := ResolveAPIVersion(""); v != "v4" || src != APIVersionSourceEnv {
		t.Errorf("env = (%q, %q), want (v4, env)", v, src)
	}

	if v, src := ResolveAPIVersion("V4Beta"); v != "v4beta" || src != APIVersionSourceFlag {
		t.Errorf("flag = (%q, %q), want (v4beta, flag)", v, src)
	}
}

// Each API version gets its own discovery cache file so a cached v4 document
// is never served for v4beta (or vice versa).
func TestDiscoveryCachePath_PerVersion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)
	v4 := DiscoveryCachePath("v4")
	beta := DiscoveryCachePath("v4beta")
	if v4 == beta {
		t.Fatalf("v4 and v4beta share a cache path: %s", v4)
	}
	if want := filepath.Join(dir, "discovery-cache", "health-v4beta.json"); beta != want {
		t.Errorf("v4beta cache = %q, want %q", beta, want)
	}
	if DiscoveryCachePath("") != v4 {
		t.Errorf("empty version must default to the v4 cache path")
	}
}
