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

// Package channel models the Google Health API release channels (API
// versions) and the rule that decides whether a feature published on one
// channel is reachable from another.
//
// Channels form a superset chain: everything in v4 (GA) is also in v4beta.
// A feature tagged with channel C is therefore available whenever the active
// channel is C or a more permissive one.
package channel

import (
	"net/url"
	"strings"
)

const (
	// V4 is the generally available channel and the default.
	V4 = "v4"
	// V4Beta exposes features that are baking before GA promotion.
	V4Beta = "v4beta"

	// Default is the channel used when nothing else is configured.
	Default = V4
)

// Selectable lists the channels a user may choose with --api-version,
// GHEALTH_API_VERSION, or the api_version config key.
var Selectable = []string{V4, V4Beta}

// rank orders channels from most restrictive (GA) to most permissive.
var rank = map[string]int{V4: 0, V4Beta: 1}

// Normalize lower-cases and trims a channel name.
func Normalize(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// IsSelectable reports whether v is a channel users may select explicitly.
func IsSelectable(v string) bool {
	v = Normalize(v)
	for _, s := range Selectable {
		if v == s {
			return true
		}
	}
	return false
}

// IsKnown reports whether v is a recognized channel.
func IsKnown(v string) bool {
	_, ok := rank[Normalize(v)]
	return ok
}

// Allows reports whether a feature published on channel required is
// reachable when the active channel is active. An empty required channel
// means GA (v4). Unknown active channels allow only GA features.
func Allows(active, required string) bool {
	required = Normalize(required)
	if required == "" {
		required = V4
	}
	r, ok := rank[required]
	if !ok {
		return false
	}
	a, ok := rank[Normalize(active)]
	if !ok {
		return r == 0
	}
	return a >= r
}

// IsPreview reports whether a feature channel is pre-GA (anything other than
// empty or v4).
func IsPreview(ch string) bool {
	ch = Normalize(ch)
	return ch != "" && ch != V4
}

// Label returns the short marker shown next to pre-GA features in help and
// schema output, e.g. "[beta]". It returns "" for GA features.
func Label(ch string) string {
	if Normalize(ch) == V4Beta {
		return "[beta]"
	}
	return ""
}

// FromBaseURL infers the channel from the final path segment of a base URL
// such as "https://health.googleapis.com/v4beta" or
// "http://127.0.0.1:8080/v4". It returns ok=false when the last segment is
// not a recognized channel.
func FromBaseURL(base string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", false
	}
	path := strings.TrimRight(u.Path, "/")
	seg := path[strings.LastIndex(path, "/")+1:]
	seg = Normalize(seg)
	if IsKnown(seg) {
		return seg, true
	}
	return "", false
}
