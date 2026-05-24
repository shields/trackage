package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// withReadSecret swaps the two seams that drive readSecret so tests can
// run both the TTY and non-TTY paths without a real terminal.
func withReadSecret(t *testing.T, isTTY bool, key string, pwErr error) {
	t.Helper()
	origIsTTY, origRead, origReader := isTerminalFn, readPasswordFn, stdinReader
	t.Cleanup(func() {
		isTerminalFn, readPasswordFn, stdinReader = origIsTTY, origRead, origReader
	})
	isTerminalFn = func(int) bool { return isTTY }
	readPasswordFn = func(int) ([]byte, error) {
		if pwErr != nil {
			return nil, pwErr
		}
		return []byte(key), nil
	}
	stdinReader = strings.NewReader(key)
}

//nolint:paralleltest // mutates readSecret seams
func TestReadSecretTTY(t *testing.T) {
	var stderrBuf bytes.Buffer
	origStderr := stderrWriter
	t.Cleanup(func() { stderrWriter = origStderr })
	stderrWriter = &stderrBuf
	withReadSecret(t, true, "tty-key\n", nil)
	got, err := readSecret("prompt> ")
	if err != nil {
		t.Fatalf("readSecret: %v", err)
	}
	if got != "tty-key" {
		t.Errorf("got %q, want tty-key", got)
	}
	if !strings.Contains(stderrBuf.String(), "prompt>") {
		t.Errorf("expected prompt on stderr, got %q", stderrBuf.String())
	}
}

//nolint:paralleltest // mutates readSecret seams
func TestReadSecretTTYError(t *testing.T) {
	withReadSecret(t, true, "", errors.New("term boom"))
	_, err := readSecret("p")
	if err == nil || !strings.Contains(err.Error(), "term boom") {
		t.Errorf("expected term error, got %v", err)
	}
}

//nolint:paralleltest // mutates readSecret seams
func TestReadSecretNonTTY(t *testing.T) {
	withReadSecret(t, false, "piped-key  \n\n", nil)
	got, err := readSecret("p")
	if err != nil {
		t.Fatalf("readSecret: %v", err)
	}
	if got != "piped-key" {
		t.Errorf("got %q, want piped-key", got)
	}
}

//nolint:paralleltest // mutates stdinReader seam
func TestReadSecretNonTTYReadError(t *testing.T) {
	origIsTTY, origReader := isTerminalFn, stdinReader
	t.Cleanup(func() { isTerminalFn, stdinReader = origIsTTY, origReader })
	isTerminalFn = func(int) bool { return false }
	stdinReader = errReader{}
	_, err := readSecret("p")
	if err == nil || !strings.Contains(err.Error(), "read stdin") {
		t.Errorf("expected read error, got %v", err)
	}
}

// errReader satisfies io.Reader and always returns an error so the
// non-TTY ReadAll path is testable.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

// withDefaultCredsStore swaps the auto-detect seam so a single test
// can pretend to run on a different OS (or none).
func withDefaultCredsStore(t *testing.T, store string) {
	t.Helper()
	orig := defaultCredsStore
	t.Cleanup(func() { defaultCredsStore = orig })
	defaultCredsStore = func() string { return store }
}

//nolint:paralleltest // mutates package-level defaultCredsStore
func TestResolveCredsStoreFlagWins(t *testing.T) {
	withDefaultCredsStore(t, "osxkeychain")
	got, err := resolveCredsStore("from-flag", "from-cfg")
	if err != nil || got != "from-flag" {
		t.Errorf("got (%q, %v)", got, err)
	}
}

//nolint:paralleltest // mutates package-level defaultCredsStore
func TestResolveCredsStoreCfgUsedWhenFlagEmpty(t *testing.T) {
	withDefaultCredsStore(t, "osxkeychain")
	got, err := resolveCredsStore("", "from-cfg")
	if err != nil || got != "from-cfg" {
		t.Errorf("got (%q, %v)", got, err)
	}
}

//nolint:paralleltest // mutates package-level defaultCredsStore
func TestResolveCredsStoreAutoDetect(t *testing.T) {
	withDefaultCredsStore(t, "osxkeychain")
	got, err := resolveCredsStore("", "")
	if err != nil || got != "osxkeychain" {
		t.Errorf("got (%q, %v), want osxkeychain", got, err)
	}
}

//nolint:paralleltest // mutates package-level defaultCredsStore
func TestResolveCredsStoreNoDefault(t *testing.T) {
	withDefaultCredsStore(t, "")
	_, err := resolveCredsStore("", "")
	if !errors.Is(err, errNoCredsStore) {
		t.Errorf("expected errNoCredsStore, got %v", err)
	}
}

// runRootCfg runs realMain-equivalent flow against a constructed root
// with the supplied config, returning captured stdout/stderr + exit code.
// We construct the root directly so each test can pass its own cfg.
func runRootCfg(t *testing.T, cfg config, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	root := newRoot(cfg)
	var sout, serr bytes.Buffer
	root.SetArgs(args)
	root.SetOut(&sout)
	root.SetErr(&serr)
	if err := root.Execute(); err != nil {
		_, _ = io.WriteString(&serr, "trackage: "+err.Error()+"\n")
		return sout.String(), serr.String(), 1
	}
	return sout.String(), serr.String(), 0
}

//nolint:paralleltest // mutates readSecret + execCommand seams
func TestLoginCmdSuccess(t *testing.T) {
	withReadSecret(t, false, "the-key\n", nil)
	withFakeHelper(t, "store-ok")
	stdout, _, code := runRootCfg(t, config{CredsStore: "fake"}, "login", "shippo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "stored API key for shippo") {
		t.Errorf("stdout = %q", stdout)
	}
}

//nolint:paralleltest // mutates readSecret seams
func TestLoginCmdUnknownBackend(t *testing.T) {
	withReadSecret(t, false, "key", nil)
	_, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "login", "bogus")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "unknown backend") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates readSecret + defaultCredsStore seams
func TestLoginCmdNoCredsStore(t *testing.T) {
	withReadSecret(t, false, "k", nil)
	withDefaultCredsStore(t, "")
	_, stderr, code := runRootCfg(t, config{}, "login", "shippo")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "creds_store") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates readSecret seams
func TestLoginCmdReadError(t *testing.T) {
	origIsTTY, origReader := isTerminalFn, stdinReader
	t.Cleanup(func() { isTerminalFn, stdinReader = origIsTTY, origReader })
	isTerminalFn = func(int) bool { return false }
	stdinReader = errReader{}
	_, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "login", "shippo")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "read stdin") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates readSecret seams
func TestLoginCmdEmptyKey(t *testing.T) {
	withReadSecret(t, false, "   \n", nil)
	_, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "login", "shippo")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "empty API key") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates readSecret + execCommand seams
func TestLoginCmdStoreError(t *testing.T) {
	withReadSecret(t, false, "k\n", nil)
	withFakeHelper(t, "store-error")
	_, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "login", "shippo")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "helper refused") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates readSecret + execCommand seams
func TestLoginCmdFlagOverridesConfig(t *testing.T) {
	withReadSecret(t, false, "k\n", nil)
	withFakeHelper(t, "store-ok")
	// cfg lacks creds_store, but --creds-store=fake supplies it.
	stdout, _, code := runRootCfg(t, config{}, "login", "--creds-store=fake", "shippo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "docker-credential-fake") {
		t.Errorf("stdout = %q", stdout)
	}
}

//nolint:paralleltest // mutates execCommand seam
func TestLogoutCmdSuccess(t *testing.T) {
	withFakeHelper(t, "erase-ok")
	stdout, _, code := runRootCfg(t, config{CredsStore: "fake"}, "logout", "shippo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "removed API key for shippo") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestLogoutCmdUnknownBackend(t *testing.T) {
	t.Parallel()
	_, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "logout", "bogus")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "unknown backend") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates defaultCredsStore seam
func TestLogoutCmdNoCredsStore(t *testing.T) {
	withDefaultCredsStore(t, "")
	_, stderr, code := runRootCfg(t, config{}, "logout", "shippo")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "creds_store") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates execCommand seam
func TestLogoutCmdEraseError(t *testing.T) {
	withFakeHelper(t, "erase-error")
	_, stderr, code := runRootCfg(t, config{CredsStore: "fake"}, "logout", "shippo")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr, "helper refused") {
		t.Errorf("stderr = %q", stderr)
	}
}

//nolint:paralleltest // mutates execCommand seam
func TestLogoutCmdFlagOverridesConfig(t *testing.T) {
	withFakeHelper(t, "erase-ok")
	stdout, _, code := runRootCfg(t, config{}, "logout", "--creds-store=fake", "shippo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "docker-credential-fake") {
		t.Errorf("stdout = %q", stdout)
	}
}

//nolint:paralleltest // mutates readSecret + defaultCredsStore + execCommand seams
func TestLoginCmdAutoDetectsCredsStore(t *testing.T) {
	withReadSecret(t, false, "k\n", nil)
	withDefaultCredsStore(t, "fake")
	withFakeHelper(t, "store-ok")
	stdout, _, code := runRootCfg(t, config{}, "login", "shippo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "docker-credential-fake") {
		t.Errorf("stdout = %q", stdout)
	}
}

//nolint:paralleltest // mutates defaultCredsStore + execCommand seams
func TestLogoutCmdAutoDetectsCredsStore(t *testing.T) {
	withDefaultCredsStore(t, "fake")
	withFakeHelper(t, "erase-ok")
	stdout, _, code := runRootCfg(t, config{}, "logout", "shippo")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "docker-credential-fake") {
		t.Errorf("stdout = %q", stdout)
	}
}
