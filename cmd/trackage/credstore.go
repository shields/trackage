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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"runtime"
	"strings"
)

// Sentinel errors for the credential-helper actions trackage uses.
// Callers can errors.Is against them.
var (
	errCredStoreGet      = errors.New("docker credential helper get failed")
	errCredStoreStore    = errors.New("docker credential helper store failed")
	errCredStoreErase    = errors.New("docker credential helper erase failed")
	errCredStoreList     = errors.New("docker credential helper list failed")
	errInvalidCredsStore = errors.New("invalid creds_store: must contain only [a-z0-9_-]")
)

// credstoreServiceHost is the synthetic hostname trackage stamps onto
// the URL it stores in the credential helper, with the backend id as
// the URL path.
//
// Two constraints pinned this choice:
//
//  1. The scheme must be https://. The Docker credential helper
//     protocol accepts arbitrary opaque strings, but the actual
//     keychain backends are stricter — macOS Keychain Services and
//     Windows Credential Manager only accept http / https as the
//     stored protocol type and reject anything else with an opaque
//     "exit status 1". A proper https:// URL keeps every helper happy.
//
//  2. The host must not be a real domain. Nothing in the helper
//     protocol fetches the URL, but on macOS the Keychain Access GUI
//     surfaces it as the "Where" field, and Safari / iCloud Keychain
//     uses kSecAttrServer to suggest stored credentials when visiting
//     the matching site. Using a real domain (e.g. msrl.dev) would
//     cause Safari to autofill the trackage API key into login forms
//     on that site — confusing at best, a credential-leak vector at
//     worst. "trackage.invalid" is in RFC 6761's reserved-TLD list,
//     will never resolve, will never match any real site Safari knows
//     about, and visibly signals "synthetic" to anyone inspecting the
//     keychain.
const credstoreServiceHost = "trackage.invalid"

// credstoreMissSentinel is the literal stderr message documented by the
// Docker credential helper protocol when a credential is absent.
//
// See https://github.com/docker/docker-credential-helpers — every
// official helper emits this exact string.
const credstoreMissSentinel = "credentials not found in native keychain"

// credstoreUsername is the constant Username field trackage writes
// when storing a credential. The protocol requires the field to be
// present, but most helpers ignore it for non-registry use; we never
// read it back.
const credstoreUsername = "trackage"

// execCommand is the seam tests use to substitute a fake helper binary
// for the real docker-credential-* lookup. See credstore_test.go for
// the test pattern.
var execCommand = exec.CommandContext

// defaultCredsStore returns the credential helper trackage will try
// when `creds_store` is unset in the config, picked by host OS. An
// empty result means "no default; skip the keychain rung."
//
// Declared as a variable so tests can stub the choice without rebuilding
// under each GOOS.
var defaultCredsStore = func() string { return defaultCredsStoreFor(runtime.GOOS) }

// defaultCredsStoreFor maps a GOOS value to the docker-credential-*
// helper that ships (or is most commonly installed) on that platform.
func defaultCredsStoreFor(goos string) string {
	switch goos {
	case "darwin":
		return "osxkeychain"
	case "linux":
		return "secretservice"
	case "windows":
		return "wincred"
	default:
		return ""
	}
}

// isHelperMissing reports whether err originates from `docker-credential-*`
// not being on PATH — used by the lenient auto-detect path in
// resolveAPIKey to fall through when the OS-default helper isn't
// installed. Permission-denied and other Start failures DO bubble so
// the user sees the real configuration problem.
func isHelperMissing(err error) bool {
	if errors.Is(err, errInvalidCredsStore) {
		return false
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		return false
	}
	return errors.Is(execErr.Err, exec.ErrNotFound) || errors.Is(execErr.Err, fs.ErrNotExist)
}

// marshalJSON is the seam tests use to exercise the otherwise
// unreachable encoding error path inside storeInCredStore.
var marshalJSON = json.Marshal

// credstoreCredential is the JSON shape every Docker credential helper
// reads on `store` input and writes on `get` output.
type credstoreCredential struct {
	ServerURL string `json:"ServerURL"`
	Username  string `json:"Username"`
	Secret    string `json:"Secret"`
}

// fetchFromCredStore asks `docker-credential-<store> get` for the
// credential keyed at the URL serviceURLFor returns
// (e.g. "https://trackage.invalid/<backendName>").
//
// Returns (key, true, nil) on hit, ("", false, nil) when the helper
// reports a documented miss, and ("", false, err) for any other failure
// (binary missing, permission denied, malformed output, etc.).
func fetchFromCredStore(ctx context.Context, store, backendName string) (string, bool, error) {
	binary, err := helperBinary(store)
	if err != nil {
		return "", false, err
	}
	serviceURL := serviceURLFor(backendName)

	cmd := execCommand(ctx, binary, "get")
	cmd.Stdin = strings.NewReader(serviceURL + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if missSentinelInBuffers(&stdout, &stderr) {
			return "", false, nil
		}
		return "", false, wrapHelperRunErr(errCredStoreGet, binary, "get", serviceURL, &stderr, err)
	}

	var cred credstoreCredential
	if err := json.Unmarshal(stdout.Bytes(), &cred); err != nil {
		return "", false, fmt.Errorf("trackage: parse %s output: %w", binary, err)
	}
	if cred.Secret == "" {
		return "", false, nil
	}
	return cred.Secret, true, nil
}

// storeInCredStore writes `key` for the given backend to the helper
// via the `store` action. The helper's response body (if any) is
// discarded — protocol-compliant helpers do not emit one on success.
func storeInCredStore(ctx context.Context, store, backendName, key string) error {
	binary, err := helperBinary(store)
	if err != nil {
		return err
	}
	serviceURL := serviceURLFor(backendName)
	payload, err := marshalJSON(credstoreCredential{
		ServerURL: serviceURL,
		Username:  credstoreUsername,
		Secret:    key,
	})
	if err != nil {
		return fmt.Errorf("trackage: marshal store payload: %w", err)
	}

	cmd := execCommand(ctx, binary, "store")
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return wrapHelperRunErr(errCredStoreStore, binary, "store", serviceURL, &stderr, err)
	}
	return nil
}

// listFromCredStore asks the helper for every stored credential (the
// protocol's `list` action) and returns the URL→username map it emits.
// trackage uses this on `trackage backends` to report key availability
// without issuing per-backend `get` calls — `get` typically requires
// per-credential keychain unlock prompts; `list` is metadata-only and
// usually unguarded.
func listFromCredStore(ctx context.Context, store string) (map[string]string, error) {
	binary, err := helperBinary(store)
	if err != nil {
		return nil, err
	}
	cmd := execCommand(ctx, binary, "list")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapHelperRunErr(errCredStoreList, binary, "list", "", &stderr, err)
	}
	// An empty (or whitespace-only) body is a legitimate "no creds stored"
	// encoding for some helpers; treat it as an empty map rather than
	// surfacing a JSON parse error.
	if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
		return map[string]string{}, nil
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("trackage: parse %s list output: %w", binary, err)
	}
	return result, nil
}

// eraseFromCredStore removes the credential at serviceURLFor(backendName).
// The protocol does not formally specify the response when the entry
// is absent; helpers we know about either exit 0 or exit non-zero with
// the miss sentinel — both are treated as success here so `logout`
// stays idempotent.
func eraseFromCredStore(ctx context.Context, store, backendName string) error {
	binary, err := helperBinary(store)
	if err != nil {
		return err
	}
	serviceURL := serviceURLFor(backendName)

	cmd := execCommand(ctx, binary, "erase")
	cmd.Stdin = strings.NewReader(serviceURL + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if missSentinelInBuffers(&stdout, &stderr) {
			return nil
		}
		return wrapHelperRunErr(errCredStoreErase, binary, "erase", serviceURL, &stderr, err)
	}
	return nil
}

// helperBinary returns the executable name for a docker credential
// helper store (e.g. "osxkeychain" → "docker-credential-osxkeychain").
//
// The store name is validated against [a-z0-9_-] to prevent path
// traversal: exec.Command treats a name containing '/' or '\' as a
// path-relative reference (skipping PATH lookup), so a value like
// "../../bin/sh" coming from a config file would execute an arbitrary
// binary. Validation runs at every entry point that builds a helper
// command — there is no second line of defense.
func helperBinary(store string) (string, error) {
	if !validCredsStoreName(store) {
		return "", fmt.Errorf("%w: %q", errInvalidCredsStore, store)
	}
	return "docker-credential-" + store, nil
}

// validCredsStoreName reports whether s is a syntactically safe value
// for the creds_store field. The known Docker credential helpers all
// match [a-z0-9_-]+ (osxkeychain, secretservice, pass, wincred, file,
// etc.); accepting anything else risks shell or path traversal.
func validCredsStoreName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// serviceURLFor returns the synthetic URL used as the protocol's key
// for a trackage backend (e.g. "https://trackage.invalid/shippo").
func serviceURLFor(backendName string) string {
	return "https://" + credstoreServiceHost + "/" + backendName
}

// missSentinelInBuffers reports whether the protocol's miss-sentinel
// string appears in either captured buffer.
func missSentinelInBuffers(stdout, stderr *bytes.Buffer) bool {
	return strings.Contains(strings.TrimSpace(stderr.String()), credstoreMissSentinel) ||
		strings.Contains(strings.TrimSpace(stdout.String()), credstoreMissSentinel)
}

// wrapHelperRunErr renders a cmd.Run error for a credential helper
// action. PATH-lookup failures keep their *exec.Error chain (for
// errors.Is checks); other failures wrap BOTH the matching sentinel and
// the underlying runErr (typically *exec.ExitError), so callers can use
// errors.Is(err, sentinel) AND errors.As(err, &exec.ExitError{}) on the
// same value.
func wrapHelperRunErr(sentinel error, binary, action, serviceURL string, stderr *bytes.Buffer, runErr error) error {
	var execErr *exec.Error
	if errors.As(runErr, &execErr) {
		return fmt.Errorf("trackage: %s not found on PATH: %w", binary, runErr)
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		return fmt.Errorf("%w: %s %s %s: %w", sentinel, binary, action, serviceURL, runErr)
	}
	return fmt.Errorf("%w: %s %s %s: %s: %w", sentinel, binary, action, serviceURL, msg, runErr)
}
