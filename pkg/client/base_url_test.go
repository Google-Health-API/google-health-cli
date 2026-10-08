package client

import (
	"os"
	"testing"
)

func TestResolveBaseURLDefault(t *testing.T) {
	os.Unsetenv("GHEALTH_BASE_URL")
	if got := ResolveBaseURL(""); got != "https://health.googleapis.com/v4" {
		t.Fatalf("default = %q", got)
	}
	if got := ResolveBaseURL("v4"); got != "https://health.googleapis.com/v4" {
		t.Fatalf("v4 = %q", got)
	}
}

func TestResolveBaseURLVersion(t *testing.T) {
	os.Unsetenv("GHEALTH_BASE_URL")
	if got := ResolveBaseURL("v4beta"); got != "https://health.googleapis.com/v4beta" {
		t.Fatalf("v4beta = %q", got)
	}
}

// GHEALTH_BASE_URL already carries its own version segment and must win
// regardless of the requested API version (fake-API sandboxes rely on this).
func TestResolveBaseURLOverride(t *testing.T) {
	t.Setenv("GHEALTH_BASE_URL", "http://127.0.0.1:8787/v4")
	for _, v := range []string{"", "v4", "v4beta"} {
		if got := ResolveBaseURL(v); got != "http://127.0.0.1:8787/v4" {
			t.Fatalf("override with version %q = %q", v, got)
		}
	}
}

func TestSetAPIVersion(t *testing.T) {
	os.Unsetenv("GHEALTH_BASE_URL")
	orig := BaseURL
	t.Cleanup(func() { BaseURL = orig })

	SetAPIVersion("v4beta")
	if BaseURL != "https://health.googleapis.com/v4beta" {
		t.Fatalf("BaseURL = %q", BaseURL)
	}
	SetAPIVersion("v4")
	if BaseURL != "https://health.googleapis.com/v4" {
		t.Fatalf("BaseURL = %q", BaseURL)
	}
}
