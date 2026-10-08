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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DefaultConfigDir = ".config/ghealth"
	ConfigFileName   = "config.toml"
)

type Config struct {
	Default  ProfileConfig            `toml:"default"`
	Profiles map[string]ProfileConfig `toml:"profiles"`
}

type ProfileConfig struct {
	ProjectID  string   `toml:"project_id,omitempty" json:"project_id,omitempty"`
	Scopes     []string `toml:"scopes,omitempty" json:"scopes,omitempty"`
	Format     string   `toml:"format,omitempty" json:"format,omitempty"`
	Timezone   string   `toml:"timezone,omitempty" json:"timezone,omitempty"`
	APIVersion string   `toml:"api_version,omitempty" json:"api_version,omitempty"`
}

// ProfileOverride is set by the --profile flag. Empty means use default resolution.
var ProfileOverride string

func ConfigDir() string {
	if dir := os.Getenv("GHEALTH_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, DefaultConfigDir)
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), ConfigFileName)
}

func Load() (*Config, error) {
	cfg := &Config{
		Default: ProfileConfig{
			Format: "json",
		},
		Profiles: make(map[string]ProfileConfig),
	}

	path := ConfigPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return cfg, nil
}

func (c *Config) Save() error {
	path := ConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(c)
}

func (c *Config) ActiveProfile() ProfileConfig {
	name := ProfileOverride
	if name == "" {
		name = os.Getenv("GHEALTH_PROFILE")
	}
	if name == "" {
		name = "default"
	}

	if name == "default" {
		return c.Default
	}

	if p, ok := c.Profiles[name]; ok {
		return p
	}

	return c.Default
}

func (c *Config) SetProfile(name string, profile ProfileConfig) {
	if name == "default" {
		c.Default = profile
	} else {
		if c.Profiles == nil {
			c.Profiles = make(map[string]ProfileConfig)
		}
		c.Profiles[name] = profile
	}
}

// GetFormat returns the output format: --format flag, then the GHEALTH_FORMAT
// env var, then the active profile's configured format, then "json".
func GetFormat(flagValue string) string {
	if flagValue != "" {
		return strings.ToLower(flagValue)
	}
	if env := os.Getenv("GHEALTH_FORMAT"); env != "" {
		return strings.ToLower(env)
	}
	if cfg, err := Load(); err == nil {
		if f := cfg.ActiveProfile().Format; f != "" {
			return strings.ToLower(f)
		}
	}
	return "json"
}

// API version sources, reported alongside the resolved version so callers can
// tell an explicit choice from the default.
const (
	APIVersionSourceFlag    = "flag"
	APIVersionSourceEnv     = "env"
	APIVersionSourceProfile = "profile"
	APIVersionSourceDefault = "default"
)

// ResolveAPIVersion returns the API version (channel) to use and where it
// came from: --api-version flag, then the GHEALTH_API_VERSION env var, then
// the active profile's api_version, then "v4". The value is normalized but
// not validated; callers check it with channel.IsSelectable.
func ResolveAPIVersion(flagValue string) (version, source string) {
	if v := strings.TrimSpace(flagValue); v != "" {
		return strings.ToLower(v), APIVersionSourceFlag
	}
	if v := strings.TrimSpace(os.Getenv("GHEALTH_API_VERSION")); v != "" {
		return strings.ToLower(v), APIVersionSourceEnv
	}
	if cfg, err := Load(); err == nil {
		if v := strings.TrimSpace(cfg.ActiveProfile().APIVersion); v != "" {
			return strings.ToLower(v), APIVersionSourceProfile
		}
	}
	return "v4", APIVersionSourceDefault
}

// ClientSecretPath returns the path to the OAuth client secret file.
func ClientSecretPath() string {
	return filepath.Join(ConfigDir(), "client_secret.json")
}

// CredentialsPath returns the path to the encrypted credentials file.
func CredentialsPath() string {
	return filepath.Join(ConfigDir(), "credentials.json")
}

// DiscoveryCachePath returns the path to the cached discovery document for an
// API version, so v4 and v4beta documents never overwrite each other.
func DiscoveryCachePath(apiVersion string) string {
	if apiVersion == "" {
		apiVersion = "v4"
	}
	return filepath.Join(ConfigDir(), "discovery-cache", "health-"+apiVersion+".json")
}
