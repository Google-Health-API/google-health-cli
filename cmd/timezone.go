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
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Google-Health-API/google-health-cli/pkg/config"
)

var (
	locMu     sync.Mutex
	cachedLoc *time.Location
)

// activeLocation returns the timezone used to resolve 'today'/'yesterday' and
// to anchor bare dates on physical-time fields. When the active profile has a
// valid IANA timezone configured ('ghealth config set timezone <zone>'), that
// zone is used; otherwise the machine-local zone. An invalid configured zone
// warns once on stderr and falls back to machine-local.
func activeLocation() *time.Location {
	locMu.Lock()
	defer locMu.Unlock()
	if cachedLoc != nil {
		return cachedLoc
	}
	cachedLoc = time.Local

	cfg, err := config.Load()
	if err != nil {
		return cachedLoc
	}
	tz := cfg.ActiveProfile().Timezone
	if tz == "" {
		return cachedLoc
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: configured timezone %q is not a valid IANA zone; using machine-local time\n", tz)
		return cachedLoc
	}
	cachedLoc = loc
	return cachedLoc
}

// resetActiveLocation clears the cached location (tests only).
func resetActiveLocation() {
	locMu.Lock()
	cachedLoc = nil
	locMu.Unlock()
}
