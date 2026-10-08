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
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"ghealth/pkg/client"
	configPkg "ghealth/pkg/config"
	"ghealth/pkg/output"
	"ghealth/pkg/types"
	"github.com/spf13/cobra"
)

// withChannelState isolates the package-level channel globals and the client
// base URL for one test.
func withChannelState(t *testing.T) {
	t.Helper()
	t.Setenv("GHEALTH_CONFIG_DIR", t.TempDir())
	os.Unsetenv("GHEALTH_API_VERSION")
	os.Unsetenv("GHEALTH_BASE_URL")
	origFlag, origActive, origSrc, origBase := flagAPIVersion, activeAPIVersion, apiVersionSource, client.BaseURL
	t.Cleanup(func() {
		flagAPIVersion, activeAPIVersion, apiVersionSource, client.BaseURL = origFlag, origActive, origSrc, origBase
	})
	flagAPIVersion = ""
}

func cliErr(t *testing.T, err error) *client.CLIError {
	t.Helper()
	var ce *client.CLIError
	if !errors.As(err, &ce) {
		t.Fatalf("want *client.CLIError, got %T: %v", err, err)
	}
	return ce
}

func TestResolveAPIVersion_DefaultAndFlag(t *testing.T) {
	withChannelState(t)

	if err := resolveAPIVersion(); err != nil {
		t.Fatal(err)
	}
	if activeAPIVersion != "v4" || client.BaseURL != "https://health.googleapis.com/v4" {
		t.Errorf("default: active=%q base=%q", activeAPIVersion, client.BaseURL)
	}

	flagAPIVersion = "v4beta"
	if err := resolveAPIVersion(); err != nil {
		t.Fatal(err)
	}
	if activeAPIVersion != "v4beta" || client.BaseURL != "https://health.googleapis.com/v4beta" {
		t.Errorf("flag: active=%q base=%q", activeAPIVersion, client.BaseURL)
	}
	if apiVersionSource != configPkg.APIVersionSourceFlag {
		t.Errorf("source = %q, want flag", apiVersionSource)
	}
}

func TestResolveAPIVersion_RejectsUnknown(t *testing.T) {
	withChannelState(t)
	for _, v := range []string{"v5", "v3", "beta"} {
		flagAPIVersion = v
		err := resolveAPIVersion()
		if err == nil {
			t.Fatalf("--api-version %s: want error", v)
		}
		if ce := cliErr(t, err); ce.Type != "validation" || !strings.Contains(ce.Hint, "v4beta") {
			t.Errorf("--api-version %s: got %+v", v, ce)
		}
	}
}

// GHEALTH_BASE_URL wins for the URL; its version segment becomes the active
// channel so gating matches what is actually called (pd_healthbench's fake
// API at .../v4 keeps working unchanged).
func TestResolveAPIVersion_BaseURLInference(t *testing.T) {
	withChannelState(t)
	t.Setenv("GHEALTH_BASE_URL", "http://127.0.0.1:8080/v4")

	if err := resolveAPIVersion(); err != nil {
		t.Fatal(err)
	}
	if activeAPIVersion != "v4" || client.BaseURL != "http://127.0.0.1:8080/v4" {
		t.Errorf("active=%q base=%q", activeAPIVersion, client.BaseURL)
	}

	// Explicit, matching choice is fine.
	flagAPIVersion = "v4"
	if err := resolveAPIVersion(); err != nil {
		t.Errorf("matching flag: %v", err)
	}

	// Explicit, conflicting choice is a validation error, not a silent v4 call.
	flagAPIVersion = "v4beta"
	err := resolveAPIVersion()
	if err == nil {
		t.Fatal("conflicting --api-version and GHEALTH_BASE_URL: want error")
	}
	if ce := cliErr(t, err); !strings.Contains(ce.Message, "conflicts with GHEALTH_BASE_URL") {
		t.Errorf("message = %q", ce.Message)
	}
}

// A base URL without a recognizable version segment is used as-is and the
// configured channel still drives gating.
func TestResolveAPIVersion_BaseURLWithoutVersion(t *testing.T) {
	withChannelState(t)
	t.Setenv("GHEALTH_BASE_URL", "http://127.0.0.1:8080/api")
	flagAPIVersion = "v4beta"
	if err := resolveAPIVersion(); err != nil {
		t.Fatal(err)
	}
	if activeAPIVersion != "v4beta" || client.BaseURL != "http://127.0.0.1:8080/api" {
		t.Errorf("active=%q base=%q", activeAPIVersion, client.BaseURL)
	}
}

func findSub(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("%s has no subcommand %q", parent.Name(), name)
	return nil
}

// A beta-only type is listed (with a [beta] marker) but refuses to run on v4
// with a hint naming the flag; on v4beta it runs.
func TestRequireChannel_BetaType(t *testing.T) {
	withChannelState(t)
	dt := &types.DataType{
		ID: "fake-beta", FilterName: "fake_beta", TimeField: types.TimeFieldSample,
		Description: "Fake beta type", Operations: []string{"list", "reconcile"},
		Channel: "v4beta",
	}
	typeCmd := newTypeCommand(dt)
	dataCmd.AddCommand(typeCmd)
	t.Cleanup(func() { dataCmd.RemoveCommand(typeCmd) })

	if !strings.HasPrefix(typeCmd.Short, "[beta] ") {
		t.Errorf("Short = %q, want [beta] prefix", typeCmd.Short)
	}
	list := findSub(t, typeCmd, "list")

	activeAPIVersion = "v4"
	err := requireChannel(list)
	if err == nil {
		t.Fatal("beta type on v4: want error")
	}
	ce := cliErr(t, err)
	if ce.Type != "validation" || !strings.Contains(ce.Message, "requires the v4beta API channel") ||
		!strings.Contains(ce.Hint, "--api-version v4beta") {
		t.Errorf("got %+v", ce)
	}

	activeAPIVersion = "v4beta"
	if err := requireChannel(list); err != nil {
		t.Errorf("beta type on v4beta: %v", err)
	}
}

// When the channel came from GHEALTH_BASE_URL, the hint points at the URL,
// since --api-version cannot override it.
func TestRequireChannel_HintForBaseURL(t *testing.T) {
	withChannelState(t)
	cmd := &cobra.Command{Use: "x", Annotations: map[string]string{channelAnnotation: "v4beta"}}
	activeAPIVersion, apiVersionSource = "v4", "GHEALTH_BASE_URL"
	ce := cliErr(t, requireChannel(cmd))
	if !strings.Contains(ce.Hint, "GHEALTH_BASE_URL") {
		t.Errorf("hint = %q", ce.Hint)
	}
}

// Per-operation overrides: a GA type with one beta-only operation gates only
// that operation.
func TestRequireChannel_OperationOverride(t *testing.T) {
	withChannelState(t)
	dt := &types.DataType{
		ID: "fake-mixed", FilterName: "fake_mixed", TimeField: types.TimeFieldSample,
		Description: "Fake mixed type", Operations: []string{"list", "reconcile"},
		OpChannels: map[string]string{"reconcile": "v4beta"},
	}
	typeCmd := newTypeCommand(dt)
	if strings.HasPrefix(typeCmd.Short, "[beta]") {
		t.Errorf("GA type must not be labelled beta: %q", typeCmd.Short)
	}
	activeAPIVersion = "v4"
	if err := requireChannel(findSub(t, typeCmd, "list")); err != nil {
		t.Errorf("GA list on v4: %v", err)
	}
	rec := findSub(t, typeCmd, "reconcile")
	if !strings.HasPrefix(rec.Short, "[beta] ") {
		t.Errorf("reconcile Short = %q, want [beta] prefix", rec.Short)
	}
	if err := requireChannel(rec); err == nil {
		t.Error("beta reconcile on v4: want error")
	}
}

// Generic gating for non-data commands: an annotated parent gates its children.
func TestRequireChannel_AnnotatedParent(t *testing.T) {
	withChannelState(t)
	parent := &cobra.Command{Use: "statistics"}
	markChannel(parent, "v4beta")
	child := &cobra.Command{Use: "get"}
	parent.AddCommand(child)

	activeAPIVersion = "v4"
	if err := requireChannel(child); err == nil {
		t.Error("child of beta command on v4: want error")
	}
	activeAPIVersion = "v4beta"
	if err := requireChannel(child); err != nil {
		t.Errorf("child of beta command on v4beta: %v", err)
	}
}

// Unknown data types used to print help and exit 0 (or fail with "unknown
// flag" when flags followed); they must now be a structured validation error.
func TestDataCmd_UnknownType(t *testing.T) {
	err := dataCmd.RunE(dataCmd, []string{"skin-temperature", "list"})
	ce := cliErr(t, err)
	if ce.Type != "validation" || !strings.Contains(ce.Message, "unknown data type: skin-temperature") {
		t.Errorf("got %+v", ce)
	}
	if !dataCmd.FParseErrWhitelist.UnknownFlags {
		t.Error("dataCmd must tolerate unknown flags so `data <unknown> list --from x` reaches RunE")
	}
}

func TestWithAPIVersion_Envelope(t *testing.T) {
	out := output.WithAPIVersion(output.EnsureEnvelope(json.RawMessage(`{"dataPoints":[{"a":1}]}`)), "v4beta")
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if string(obj["_api_version"]) != `"v4beta"` {
		t.Errorf("_api_version = %s", obj["_api_version"])
	}
	if _, ok := obj["dataPoints"]; !ok {
		t.Error("dataPoints lost")
	}
}
