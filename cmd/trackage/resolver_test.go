package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAPIKeyFlagWins(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "from-flag", "shippo", config{
		APIKeys: map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-flag" {
		t.Errorf("got (%q, %v); want from-flag", got, err)
	}
}

func TestResolveAPIKeyEnvWinsOverConfig(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "from-env")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		CredsStore: "fake",
		APIKeys:    map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-env" {
		t.Errorf("got (%q, %v); want from-env", got, err)
	}
}

func TestResolveAPIKeyCredsStoreHitWins(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withFakeHelper(t, "hit")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		CredsStore: "fake",
		APIKeys:    map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "hit-key" {
		t.Errorf("got (%q, %v); want hit-key", got, err)
	}
}

func TestResolveAPIKeyCredsStoreMissFallsThrough(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withFakeHelper(t, "miss-stderr")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		CredsStore: "fake",
		APIKeys:    map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-config" {
		t.Errorf("got (%q, %v); want from-config", got, err)
	}
}

func TestResolveAPIKeyCredsStoreErrorBubbles(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withFakeHelper(t, "stderr-error")
	_, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		CredsStore: "fake",
	})
	if err == nil {
		t.Fatal("expected creds store error to bubble")
	}
}

// TestResolveAPIKeyCredsStoreStrictMissingHelperFallsThrough confirms
// that an explicitly-configured creds_store whose binary isn't on PATH
// falls through to api_keys.<name> rather than erroring — matching what
// `trackage backends` already reports for the same configuration.
func TestResolveAPIKeyCredsStoreStrictMissingHelperFallsThrough(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		CredsStore: "nope-definitely-not-installed",
		APIKeys:    map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-config" {
		t.Errorf("got (%q, %v); want from-config", got, err)
	}
}

func TestResolveAPIKeyAutoDetectHit(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withFakeHelper(t, "hit")
	withDefaultCredsStore(t, "fake")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{})
	if err != nil || got != "hit-key" {
		t.Errorf("got (%q, %v); want hit-key", got, err)
	}
}

func TestResolveAPIKeyAutoDetectMissingHelperFallsThrough(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	// Default points at a binary that genuinely isn't on PATH; the
	// lenient branch should swallow that and fall through to api_keys.
	withDefaultCredsStore(t, "nope-definitely-not-installed")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		APIKeys: map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-config" {
		t.Errorf("got (%q, %v); want from-config", got, err)
	}
}

func TestResolveAPIKeyAutoDetectErrorBubbles(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withFakeHelper(t, "stderr-error")
	withDefaultCredsStore(t, "fake")
	_, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{})
	if err == nil {
		t.Fatal("expected non-PATH helper error to bubble even on auto-detect")
	}
}

func TestResolveAPIKeyNoCredsStoreNoDefault(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withDefaultCredsStore(t, "")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		APIKeys: map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-config" {
		t.Errorf("got (%q, %v); want from-config", got, err)
	}
}

func TestInspectKeySourceEnv(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "from-env")
	got := inspectKeySource("SHIPPO_API_KEY", "shippo", config{}, nil)
	if got != sourceEnv {
		t.Errorf("got %q, want env", got)
	}
}

func TestInspectKeySourceKeychain(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	list := map[string]string{"https://trackage.invalid/shippo": "trackage"}
	got := inspectKeySource("SHIPPO_API_KEY", "shippo", config{}, list)
	if got != sourceCredsStore {
		t.Errorf("got %q, want keychain", got)
	}
}

func TestInspectKeySourceConfig(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	got := inspectKeySource("SHIPPO_API_KEY", "shippo", config{
		APIKeys: map[string]string{"shippo": "from-config"},
	}, nil)
	if got != sourceConfig {
		t.Errorf("got %q, want config", got)
	}
}

func TestInspectKeySourceNone(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	got := inspectKeySource("SHIPPO_API_KEY", "shippo", config{}, nil)
	if got != sourceNone {
		t.Errorf("got %q, want empty", got)
	}
}

//nolint:paralleltest // mutates defaultCredsStore + execCommand
func TestListKeysInCredStoreAutoDetectMissing(t *testing.T) {
	withDefaultCredsStore(t, "nope-definitely-not-installed")
	list, err := listKeysInCredStore(context.Background(), config{})
	if err != nil {
		t.Errorf("auto-detect PATH miss should fall through silently, got %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got list=%v, want empty", list)
	}
}

//nolint:paralleltest // mutates defaultCredsStore
func TestListKeysInCredStoreNoDefault(t *testing.T) {
	withDefaultCredsStore(t, "")
	list, err := listKeysInCredStore(context.Background(), config{})
	if err != nil || len(list) != 0 {
		t.Errorf("got (%v, %v), want (empty, nil)", list, err)
	}
}

//nolint:paralleltest // mutates execCommand
func TestListKeysInCredStoreExplicitError(t *testing.T) {
	withFakeHelper(t, "list-error")
	_, err := listKeysInCredStore(context.Background(), config{CredsStore: "fake"})
	if err == nil {
		t.Fatal("explicit cfg.CredsStore errors should bubble")
	}
}

//nolint:paralleltest // mutates execCommand + defaultCredsStore
func TestListKeysInCredStoreAutoDetectFromFake(t *testing.T) {
	withDefaultCredsStore(t, "fake")
	withFakeHelper(t, "list-ok")
	list, err := listKeysInCredStore(context.Background(), config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, ok := list["https://trackage.invalid/shippo"]; !ok {
		t.Errorf("expected shippo entry in list, got %v", list)
	}
}

func TestResolveAPIKeyConfigOnly(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withDefaultCredsStore(t, "")
	got, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{
		APIKeys: map[string]string{"shippo": "from-config"},
	})
	if err != nil || got != "from-config" {
		t.Errorf("got (%q, %v); want from-config", got, err)
	}
}

func TestResolveAPIKeyNoneError(t *testing.T) {
	t.Setenv("SHIPPO_API_KEY", "")
	withDefaultCredsStore(t, "")
	_, err := resolveAPIKey(context.Background(), "SHIPPO_API_KEY", "", "shippo", config{})
	if !errors.Is(err, errNoAPIKey) {
		t.Fatalf("expected errNoAPIKey, got %v", err)
	}
	for _, want := range []string{"--api-key", "SHIPPO_API_KEY", "creds_store", "api_keys.shippo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got %v", want, err)
		}
	}
}

func TestResolveBackendDefaultBackendFromConfig(t *testing.T) {
	t.Setenv("TRACKAGE_BACKEND", "")
	t.Setenv("SHIPPO_API_KEY", "from-env")
	tr, name, err := resolveBackend(context.Background(), "", "", config{DefaultBackend: "shippo"})
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if name != "shippo" || tr.Name() != "shippo" {
		t.Errorf("name=%q tr.Name=%q", name, tr.Name())
	}
}

//nolint:paralleltest // mutates package-level configPathFn
func TestConfigPathErrorBubbles(t *testing.T) {
	origFn := configPathFn
	t.Cleanup(func() { configPathFn = origFn })
	configPathFn = func() (string, error) { return "", errors.New("nope") }

	_, err := loadConfigFromDefaultPath()
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("expected the configPathFn error to bubble, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level configPathFn
func TestRealMainConfigErrorExits(t *testing.T) {
	origFn := configPathFn
	t.Cleanup(func() { configPathFn = origFn })
	configPathFn = func() (string, error) { return "", errors.New("config-path-broken") }

	var sout, serr bytes.Buffer
	code := realMain([]string{"backends"}, &sout, &serr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(serr.String(), "config-path-broken") {
		t.Errorf("stderr should mention the error, got %q", serr.String())
	}
}

//nolint:paralleltest // mutates package-level configPathFn
func TestRealMainLoadsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "default_backend = \"shippo\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	origFn := configPathFn
	t.Cleanup(func() { configPathFn = origFn })
	configPathFn = func() (string, error) { return path, nil }

	var sout, serr bytes.Buffer
	code := realMain([]string{"backends"}, &sout, &serr)
	if code != 0 {
		t.Errorf("exit code = %d, stderr=%q", code, serr.String())
	}
}
