// Copyright © 2026 Michael Shields
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

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigSuccess(t *testing.T) {
	t.Parallel()
	in := `
default_backend = "easypost"
creds_store = "osxkeychain"

[api_keys]
shippo = "shippo_live_x"
easypost = "EZ_y"
`
	got, err := parseConfig(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got.DefaultBackend != "easypost" {
		t.Errorf("DefaultBackend = %q", got.DefaultBackend)
	}
	if got.CredsStore != "osxkeychain" {
		t.Errorf("CredsStore = %q", got.CredsStore)
	}
	if got.APIKeys["shippo"] != "shippo_live_x" || got.APIKeys["easypost"] != "EZ_y" {
		t.Errorf("APIKeys = %v", got.APIKeys)
	}
}

func TestParseConfigEmpty(t *testing.T) {
	t.Parallel()
	got, err := parseConfig(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got.DefaultBackend != "" || got.CredsStore != "" || len(got.APIKeys) != 0 {
		t.Errorf("expected zero config, got %+v", got)
	}
}

func TestParseConfigMalformed(t *testing.T) {
	t.Parallel()
	_, err := parseConfig(strings.NewReader("not = valid = toml"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseConfigUnknownKeysIgnored(t *testing.T) {
	t.Parallel()
	// TOML's decoder ignores unknown fields by default — important for
	// forward compat with newer trackage versions writing extra keys.
	got, err := parseConfig(strings.NewReader(`future_setting = "value"`))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got.DefaultBackend != "" {
		t.Errorf("DefaultBackend = %q", got.DefaultBackend)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := loadConfig(filepath.Join(dir, "nope.toml"))
	if err != nil {
		t.Fatalf("loadConfig should swallow ENOENT, got %v", err)
	}
	if got.DefaultBackend != "" {
		t.Errorf("expected zero config, got %+v", got)
	}
}

func TestLoadConfigMalformedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("garbage = = ="), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadConfigOpenError(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("test relies on permission denial; running as root defeats it")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("default_backend = \"shippo\""), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected permission error")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("permission error should not be classified as ErrNotExist: %v", err)
	}
}

func TestLoadConfigSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "default_backend = \"shippo\"\n[api_keys]\nshippo = \"k\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got.DefaultBackend != "shippo" || got.APIKeys["shippo"] != "k" {
		t.Errorf("got %+v", got)
	}
}

func TestConfigPathReturnsTrackageDirectory(t *testing.T) {
	t.Parallel()
	got, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("trackage", "config.toml")) {
		t.Errorf("configPath = %q, want it to end in trackage/config.toml", got)
	}
}

// TestConfigPathError forces os.UserConfigDir to fail by clearing both
// $XDG_CONFIG_HOME and $HOME (the Linux fallbacks). The test is skipped
// where the OS picks the path up from other sources.
func TestConfigPathError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "") // Windows fallback, harmless on others
	t.Setenv("AppData", "")     // ditto
	t.Setenv("APPDATA", "")     // ditto
	_, err := configPath()
	if err == nil {
		t.Skip("os.UserConfigDir still succeeded on this platform; nothing to assert")
	}
}
