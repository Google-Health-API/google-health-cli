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

package schema

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCacheReturnsFreshCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health-v4.json")
	want := []byte(`{"kind":"discovery#restDescription"}`)

	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := readCache(path)
	if err != nil {
		t.Fatalf("readCache() error = %v, want nil", err)
	}
	if string(got) != string(want) {
		t.Errorf("readCache() = %s, want %s", got, want)
	}
}

func TestReadCacheRejectsExpiredCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health-v4.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	expired := time.Now().Add(-(cacheTTL + time.Hour))
	if err := os.Chtimes(path, expired, expired); err != nil {
		t.Fatal(err)
	}

	if _, err := readCache(path); err == nil {
		t.Fatal("readCache() error = nil, want expired cache error")
	}
}

func TestWriteCacheCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "discovery-cache", "health-v4.json")
	want := []byte(`{"rootUrl":"https://health.googleapis.com/"}`)

	if err := writeCache(path, want); err != nil {
		t.Fatalf("writeCache() error = %v, want nil", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("cached file = %s, want %s", got, want)
	}
}

func TestFetchDiscoveryUsesFreshCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHEALTH_CONFIG_DIR", dir)

	path := filepath.Join(dir, "discovery-cache", "health-v4.json")
	want := []byte(`{"revision":"cached"}`)
	if err := writeCache(path, want); err != nil {
		t.Fatal(err)
	}

	got, origin, err := FetchDiscovery()
	if err != nil {
		t.Fatalf("FetchDiscovery() error = %v, want nil", err)
	}
	if origin != "cache" {
		t.Errorf("FetchDiscovery() origin = %q, want cache", origin)
	}
	if string(got) != string(want) {
		t.Errorf("FetchDiscovery() = %s, want %s", got, want)
	}
}
