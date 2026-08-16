package auth

import "testing"

func TestAllScopesUsesCurrentWriteOnlySuffixes(t *testing.T) {
	want := map[string]bool{
		"activity_and_fitness.writeonly":            false,
		"health_metrics_and_measurements.writeonly": false,
		"sleep.writeonly":                           false,
		"nutrition.writeonly":                       false,
		"profile.writeonly":                         false,
		"settings.writeonly":                        false,
	}
	obsolete := map[string]bool{
		"activity_and_fitness":            true,
		"health_metrics_and_measurements": true,
		"sleep":                           true,
		"nutrition":                       true,
		"profile":                         true,
		"settings":                        true,
	}

	for _, scope := range AllScopes {
		if _, ok := want[scope.Suffix]; ok {
			want[scope.Suffix] = true
		}
		if obsolete[scope.Suffix] {
			t.Errorf("obsolete unsuffixed write scope remains: %s", scope.Suffix)
		}
	}

	for scope, found := range want {
		if !found {
			t.Errorf("current write-only scope missing: %s", scope)
		}
	}
}
