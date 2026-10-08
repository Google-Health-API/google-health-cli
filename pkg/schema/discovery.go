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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ghealth/pkg/config"
)

const (
	discoveryURLFormat = "https://health.googleapis.com/$discovery/rest?version=%s"
	cacheTTL           = 24 * time.Hour
)

// DiscoveryURL returns the discovery document URL for an API version.
func DiscoveryURL(apiVersion string) string {
	if apiVersion == "" {
		apiVersion = "v4"
	}
	return fmt.Sprintf(discoveryURLFormat, apiVersion)
}

// FetchDiscovery retrieves the Health API discovery document for an API
// version (e.g. "v4", "v4beta"). It uses a 24-hour file cache at
// DiscoveryCachePath(apiVersion); on a cache miss it fetches from the
// network. The second return value reports the data's origin ("cache" or
// "live"). It returns an error if no fresh cache exists and the network fetch
// fails.
func FetchDiscovery(apiVersion string) (json.RawMessage, string, error) {
	// Check cache first.
	cachePath := config.DiscoveryCachePath(apiVersion)
	if data, err := readCache(cachePath); err == nil {
		return data, "cache", nil
	}

	// Fetch from network.
	data, err := fetchFromNetwork(DiscoveryURL(apiVersion))
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch discovery document: %w", err)
	}

	// Write cache (best-effort).
	_ = writeCache(cachePath, data)

	return data, "live", nil
}

func readCache(path string) (json.RawMessage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > cacheTTL {
		return nil, fmt.Errorf("cache expired")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func writeCache(path string, data json.RawMessage) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func fetchFromNetwork(discoveryURL string) (json.RawMessage, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(discoveryURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery endpoint returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(body), nil
}
