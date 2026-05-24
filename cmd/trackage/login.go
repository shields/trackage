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
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Sentinel errors for the login / logout commands.
var (
	errNoCredsStore = errors.New(
		"no creds_store configured: set creds_store in config.toml or pass --creds-store",
	)
	errEmptyKey = errors.New("empty API key")
)

// Seams used by readSecret so tests can exercise both the TTY and
// non-TTY paths without needing a real terminal. They point at the
// real x/term implementations by default; tests reassign them.
var (
	isTerminalFn   func(fd int) bool            = term.IsTerminal
	readPasswordFn func(fd int) ([]byte, error) = term.ReadPassword
	stdinReader    io.Reader                    = os.Stdin
	stderrWriter   io.Writer                    = os.Stderr
)

// readSecret returns an API key from stdin. If stdin is a terminal,
// the user is prompted on stderr and the input is read without echo.
// Otherwise the entire stdin is read and trailing whitespace trimmed,
// so callers can pipe a key in (`echo … | trackage login …`).
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if isTerminalFn(fd) {
		_, _ = fmt.Fprint(stderrWriter, prompt) //nolint:errcheck // stderr write errors are inactionable
		b, err := readPasswordFn(fd)
		_, _ = fmt.Fprintln(stderrWriter) //nolint:errcheck // stderr write errors are inactionable
		if err != nil {
			return "", fmt.Errorf("trackage: read password: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	data, err := io.ReadAll(stdinReader)
	if err != nil {
		return "", fmt.Errorf("trackage: read stdin: %w", err)
	}
	return strings.TrimRight(string(data), "\r\n \t"), nil
}

// resolveCredsStore picks the helper login / logout will use. Flag
// wins, then config, then the OS default from defaultCredsStore. If
// none of those produce a value (e.g. unsupported OS), errNoCredsStore
// surfaces.
func resolveCredsStore(flagVal, cfgVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if cfgVal != "" {
		return cfgVal, nil
	}
	if def := defaultCredsStore(); def != "" {
		return def, nil
	}
	return "", errNoCredsStore
}

func newLoginCmd(cfg config) *cobra.Command {
	var credsStore string
	cmd := &cobra.Command{
		Use:   "login <backend>",
		Short: "Store an API key in the OS keychain via the configured docker credential helper",
		Long: `Save a backend's API key to the OS keychain through the credential
helper named in config.toml's creds_store (or passed with --creds-store).

On an interactive terminal, the key is read without echo. When stdin is
a pipe or redirect, the key is read verbatim from stdin so the command
can be scripted, e.g. ` + "`echo $KEY | trackage login shippo`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := args[0]
			if _, ok := backendByName(backend); !ok {
				return fmt.Errorf("%w %q (run `trackage backends` to list)", errUnknownBackend, backend)
			}
			store, err := resolveCredsStore(credsStore, cfg.CredsStore)
			if err != nil {
				return err
			}
			key, err := readSecret(fmt.Sprintf("API key for %s: ", backend))
			if err != nil {
				return err
			}
			if key == "" {
				return errEmptyKey
			}
			if err := storeInCredStore(cmd.Context(), store, backend, key); err != nil {
				return err
			}
			_, _ = fmt.Fprintf( //nolint:errcheck // stdout write errors are inactionable
				cmd.OutOrStdout(),
				"stored API key for %s via docker-credential-%s\n", backend, store,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&credsStore, "creds-store", "",
		"credential helper name (overrides config.creds_store)")
	return cmd
}

func newLogoutCmd(cfg config) *cobra.Command {
	var credsStore string
	cmd := &cobra.Command{
		Use:   "logout <backend>",
		Short: "Remove the stored API key from the configured docker credential helper",
		Long: `Erase a backend's stored API key via docker-credential-<creds_store>.

The operation is idempotent — logging out when no key is stored is a
no-op, not an error.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := args[0]
			if _, ok := backendByName(backend); !ok {
				return fmt.Errorf("%w %q (run `trackage backends` to list)", errUnknownBackend, backend)
			}
			store, err := resolveCredsStore(credsStore, cfg.CredsStore)
			if err != nil {
				return err
			}
			if err := eraseFromCredStore(cmd.Context(), store, backend); err != nil {
				return err
			}
			_, _ = fmt.Fprintf( //nolint:errcheck // stdout write errors are inactionable
				cmd.OutOrStdout(),
				"removed API key for %s via docker-credential-%s\n", backend, store,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&credsStore, "creds-store", "",
		"credential helper name (overrides config.creds_store)")
	return cmd
}
