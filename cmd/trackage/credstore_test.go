package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// The fake docker-credential-* helper used by every credstore test
// here is the test binary itself, re-executed with credHelperModeEnv
// set. TestMain checks for that variable on entry; if present, it
// hands control to runFakeCredHelper, which writes the canned
// stdout / stderr the test wants and exits with the chosen status.
//
// Why this trick: a real docker-credential-* binary lives on PATH,
// reads from stdin, writes to stdout/stderr, and exits with a code
// the protocol cares about. We need to exercise all of those paths
// (hit, miss, malformed JSON, transport error) deterministically,
// without depending on which helpers are installed on the host. The
// standard Go pattern — re-exec os.Args[0] with a flag in the
// environment — gives us a stdlib-only "binary" with full control,
// no temp scripts, and no shell-script portability headaches.
const credHelperModeEnv = "TRACKAGE_FAKE_CRED_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(credHelperModeEnv); mode != "" {
		os.Exit(runFakeCredHelper(mode)) //nolint:revive // fake-helper dispatch into the test binary
	}
	m.Run()
}

// runFakeCredHelper mimics the relevant slice of the Docker credential
// helper protocol. The returned int is the exit code the fake binary
// should propagate, mirroring how a real helper signals miss / error.
func runFakeCredHelper(mode string) int {
	// Drain stdin so the writing side doesn't hit EPIPE.
	_, _ = io.Copy(io.Discard, os.Stdin)
	switch mode {
	case "hit":
		_, _ = fmt.Fprintln(os.Stdout, `{"ServerURL":"https://trackage.invalid/shippo","Username":"","Secret":"hit-key"}`)
		return 0
	case "miss-stderr":
		_, _ = fmt.Fprintln(os.Stderr, credstoreMissSentinel)
		return 1
	case "miss-stdout":
		_, _ = fmt.Fprintln(os.Stdout, credstoreMissSentinel)
		return 1
	case "stderr-error":
		_, _ = fmt.Fprintln(os.Stderr, "credential daemon unreachable")
		return 2
	case "bare-error":
		return 3
	case "malformed", "list-malformed":
		_, _ = fmt.Fprintln(os.Stdout, "not json")
		return 0
	case "empty-secret":
		_, _ = fmt.Fprintln(os.Stdout, `{"ServerURL":"https://trackage.invalid/shippo","Username":"","Secret":""}`)
		return 0
	case "store-ok", "erase-ok", "list-empty":
		// "list-empty" intentionally falls through to the empty-stdout path
		// — a helper with no stored credentials emits `{}` per the protocol,
		// which json.Unmarshal handles fine.
		if mode == "list-empty" {
			_, _ = fmt.Fprintln(os.Stdout, `{}`)
		}
		return 0
	case "store-error", "erase-error", "list-error":
		_, _ = fmt.Fprintln(os.Stderr, "helper refused")
		return 4
	case "list-ok":
		//nolint:lll // JSON test fixture; readability beats wrapping.
		_, _ = fmt.Fprintln(os.Stdout,
			`{"https://trackage.invalid/shippo":"trackage","https://trackage.invalid/easypost":"trackage","https://other.example/":"someone"}`)
		return 0
	default:
		_, _ = fmt.Fprintf(os.Stderr, "fake helper: unknown mode %q\n", mode)
		return 99
	}
}

// withFakeHelper installs an execCommand override that runs this test
// binary in fake-helper mode. The mode value selects which response the
// helper emits.
func withFakeHelper(t *testing.T, mode string) {
	t.Helper()
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = append(os.Environ(), credHelperModeEnv+"="+mode)
		return cmd
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreHit(t *testing.T) {
	withFakeHelper(t, "hit")
	secret, ok, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok || secret != "hit-key" {
		t.Errorf("got (%q, %v); want (\"hit-key\", true)", secret, ok)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreMissStderr(t *testing.T) {
	withFakeHelper(t, "miss-stderr")
	_, ok, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err != nil || ok {
		t.Errorf("expected (false, nil), got ok=%v err=%v", ok, err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreMissStdout(t *testing.T) {
	withFakeHelper(t, "miss-stdout")
	_, ok, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err != nil || ok {
		t.Errorf("expected (false, nil), got ok=%v err=%v", ok, err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreStderrError(t *testing.T) {
	withFakeHelper(t, "stderr-error")
	_, _, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err == nil || !strings.Contains(err.Error(), "credential daemon unreachable") {
		t.Errorf("expected stderr message in error, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreBareError(t *testing.T) {
	withFakeHelper(t, "bare-error")
	_, _, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("expected exec error text, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreMalformedJSON(t *testing.T) {
	withFakeHelper(t, "malformed")
	_, _, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreEmptySecret(t *testing.T) {
	withFakeHelper(t, "empty-secret")
	_, ok, err := fetchFromCredStore(context.Background(), "fake", "shippo")
	if err != nil || ok {
		t.Errorf("expected (false, nil) for empty Secret, got ok=%v err=%v", ok, err)
	}
}

func TestFetchFromCredStoreBinaryNotFound(t *testing.T) {
	t.Parallel()
	// No override: real execCommand looks for `docker-credential-nope-…`
	// on PATH and fails. Returns *exec.Error.
	_, _, err := fetchFromCredStore(context.Background(), "nope-definitely-not-installed", "shippo")
	if err == nil {
		t.Fatal("expected PATH lookup error")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error should mention PATH, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestStoreInCredStoreSuccess(t *testing.T) {
	withFakeHelper(t, "store-ok")
	if err := storeInCredStore(context.Background(), "fake", "shippo", "the-key"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestStoreInCredStoreError(t *testing.T) {
	withFakeHelper(t, "store-error")
	err := storeInCredStore(context.Background(), "fake", "shippo", "k")
	if !errors.Is(err, errCredStoreStore) {
		t.Errorf("expected errCredStoreStore, got %v", err)
	}
	if !strings.Contains(err.Error(), "helper refused") {
		t.Errorf("expected stderr in message, got %v", err)
	}
}

func TestStoreInCredStoreBinaryNotFound(t *testing.T) {
	t.Parallel()
	err := storeInCredStore(context.Background(), "nope-definitely-not-installed", "shippo", "k")
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("expected PATH error, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level marshalJSON
func TestStoreInCredStoreMarshalError(t *testing.T) {
	orig := marshalJSON
	t.Cleanup(func() { marshalJSON = orig })
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal boom") }
	err := storeInCredStore(context.Background(), "fake", "shippo", "k")
	if err == nil || !strings.Contains(err.Error(), "marshal boom") {
		t.Errorf("expected marshal error, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestEraseFromCredStoreSuccess(t *testing.T) {
	withFakeHelper(t, "erase-ok")
	if err := eraseFromCredStore(context.Background(), "fake", "shippo"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestEraseFromCredStoreMissIsSuccess(t *testing.T) {
	withFakeHelper(t, "miss-stderr")
	if err := eraseFromCredStore(context.Background(), "fake", "shippo"); err != nil {
		t.Errorf("erase miss should be idempotent, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestEraseFromCredStoreError(t *testing.T) {
	withFakeHelper(t, "erase-error")
	err := eraseFromCredStore(context.Background(), "fake", "shippo")
	if !errors.Is(err, errCredStoreErase) {
		t.Errorf("expected errCredStoreErase, got %v", err)
	}
}

func TestEraseFromCredStoreBinaryNotFound(t *testing.T) {
	t.Parallel()
	err := eraseFromCredStore(context.Background(), "nope-definitely-not-installed", "shippo")
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("expected PATH error, got %v", err)
	}
}

func TestDefaultCredsStoreFor(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"darwin":      "osxkeychain",
		"linux":       "secretservice",
		"windows":     "wincred",
		"freebsd":     "",
		"openbsd":     "",
		"netbsd":      "",
		"plan9":       "",
		"unsupported": "",
	}
	for goos, want := range cases {
		if got := defaultCredsStoreFor(goos); got != want {
			t.Errorf("defaultCredsStoreFor(%q) = %q, want %q", goos, got, want)
		}
	}
}

//nolint:paralleltest // mutates package-level defaultCredsStore
func TestDefaultCredsStoreReturnsAValue(t *testing.T) {
	// The default-store seam should return something derived from the
	// host GOOS. Cover the closure itself by invoking it without an
	// override.
	got := defaultCredsStore()
	want := defaultCredsStoreFor(runtime.GOOS)
	if got != want {
		t.Errorf("defaultCredsStore() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestListFromCredStoreSuccess(t *testing.T) {
	withFakeHelper(t, "list-ok")
	got, err := listFromCredStore(context.Background(), "fake")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["https://trackage.invalid/shippo"] != "trackage" {
		t.Errorf("missing trackage entry: %v", got)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestListFromCredStoreError(t *testing.T) {
	withFakeHelper(t, "list-error")
	_, err := listFromCredStore(context.Background(), "fake")
	if !errors.Is(err, errCredStoreList) {
		t.Errorf("expected errCredStoreList, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestListFromCredStoreMalformedJSON(t *testing.T) {
	withFakeHelper(t, "list-malformed")
	_, err := listFromCredStore(context.Background(), "fake")
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

func TestListFromCredStoreBinaryNotFound(t *testing.T) {
	t.Parallel()
	_, err := listFromCredStore(context.Background(), "nope-definitely-not-installed")
	if err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("expected PATH error, got %v", err)
	}
}

func TestIsHelperMissing(t *testing.T) {
	t.Parallel()
	if isHelperMissing(nil) {
		t.Error("nil should not be a helper-missing error")
	}
	if isHelperMissing(errors.New("plain error")) {
		t.Error("plain errors should not be helper-missing")
	}
	// Build an exec.Error chain by trying a binary that doesn't exist.
	cmd := exec.Command("trackage-test-definitely-not-a-binary")
	err := cmd.Run()
	if !isHelperMissing(err) {
		t.Errorf("expected helper-missing for %v", err)
	}
	// errInvalidCredsStore must NOT count as missing — a malformed
	// creds_store is a hard configuration error the user should see.
	wrapped := fmt.Errorf("%w: %q", errInvalidCredsStore, "../evil")
	if isHelperMissing(wrapped) {
		t.Error("errInvalidCredsStore must not be classified as helper-missing")
	}
}

func TestHelperBinaryRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	bad := []string{
		"../../bin/sh",
		"osxkeychain/../sh",
		"foo bar",
		"foo;rm -rf /",
		"foo\nbar",
		"",
		"Osx", // uppercase rejected too
		".hidden",
	}
	for _, store := range bad {
		if _, err := helperBinary(store); !errors.Is(err, errInvalidCredsStore) {
			t.Errorf("helperBinary(%q) → err=%v, want errInvalidCredsStore", store, err)
		}
	}
	good := []string{"osxkeychain", "secretservice", "pass", "wincred", "file"}
	for _, store := range good {
		if got, err := helperBinary(store); err != nil || got != "docker-credential-"+store {
			t.Errorf("helperBinary(%q) → %q, err=%v", store, got, err)
		}
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestFetchFromCredStoreInvalidStore(t *testing.T) {
	// No execCommand override needed — validation must short-circuit
	// before any exec.Command call.
	_, _, err := fetchFromCredStore(context.Background(), "../../bin/sh", "shippo")
	if !errors.Is(err, errInvalidCredsStore) {
		t.Errorf("expected errInvalidCredsStore, got %v", err)
	}
}

//nolint:paralleltest // mutates package-level execCommand
func TestListFromCredStoreEmptyStdout(t *testing.T) {
	// list-empty mode exits 0 with no stdout — must surface as empty
	// map rather than "unexpected end of JSON input."
	withFakeHelper(t, "store-ok") // store-ok writes nothing and exits 0
	got, err := listFromCredStore(context.Background(), "fake")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}
