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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"msrl.dev/trackage"
)

func newTrackCmd(cfg config) *cobra.Command {
	var (
		carrier string
		apiKey  string
	)
	cmd := &cobra.Command{
		Use:   "track <tracking-number>",
		Short: "Fetch the latest status of a tracking number",
		Long: `Fetch the latest status of one tracking number.

If --carrier is omitted, trackage runs its local detector first. If the
detector can't identify the carrier and the chosen backend supports
auto-detect (EasyPost, 17Track, TrackingMore), the backend will try to
infer it. Shippo requires an explicit carrier and will return an error
otherwise.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number := args[0]
			ctx := cmd.Context()
			tracker, _, err := resolveBackend(ctx, stringFlag(cmd, "backend"), apiKey, cfg)
			if err != nil {
				return err
			}

			result, err := tracker.Track(ctx, carrier, number)
			if err != nil {
				return friendlyBackendError(tracker.Name(), err)
			}

			out := cmd.OutOrStdout()
			if boolFlag(cmd, "json") {
				return writeJSON(out, result)
			}
			return writePretty(out, tracker.Name(), result)
		},
	}
	cmd.Flags().StringVarP(&carrier, "carrier", "c", "", "carrier hint (canonical id or backend-native code)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (defaults to env var, creds_store, or config.toml)")
	return cmd
}

func writeJSON(w io.Writer, t *trackage.Tracking) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}

func writePretty(w io.Writer, backend string, t *trackage.Tracking) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s   %s\n", t.TrackingNumber, statusLabel(t.Status))
	if t.Carrier != "" {
		fmt.Fprintf(&b, "  carrier:  %s\n", t.Carrier)
	}
	fmt.Fprintf(&b, "  backend:  %s\n", backend)
	if t.Substatus != "" {
		fmt.Fprintf(&b, "  detail:   %s\n", t.Substatus)
	}
	if t.Description != "" {
		fmt.Fprintf(&b, "  latest:   %s\n", t.Description)
	}
	if !t.LastUpdate.IsZero() {
		fmt.Fprintf(&b, "  updated:  %s\n", formatTime(t.LastUpdate))
	}
	if t.EstDelivery != nil {
		fmt.Fprintf(&b, "  ETA:      %s\n", t.EstDelivery.Format("2006-01-02"))
	}
	if len(t.Events) > 0 {
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "  events (oldest → newest):")
		for _, e := range t.Events {
			ts := "----------------------"
			if !e.Time.IsZero() {
				ts = formatTime(e.Time)
			}
			line := fmt.Sprintf("    %-22s  %-11s  %s", ts, statusLabel(e.Status), e.Description)
			if e.Location != "" {
				line += " — " + e.Location
			}
			fmt.Fprintln(&b, line)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// formatTime renders a Tracking timestamp for the pretty CLI output.
// Backends tag wall-clock values the carrier emitted without a zone
// with a FixedZone named "local" (TrackingMore v4 checkpoint_date,
// EasyPost datetime when datetime_local is absent). We deliberately do
// not use time.Local — time.Parse aliases that location when a parsed
// offset matches the user's machine zone, which would mask real
// carrier-supplied timestamps as zoneless.
func formatTime(t time.Time) string {
	if t.Location().String() == "local" {
		return t.Format("2006-01-02 15:04") + " local"
	}
	return t.Format("2006-01-02 15:04 MST")
}

// friendlyBackendError converts a backend Track error into a CLI
// message that surfaces the trackage sentinel category, preserving the
// underlying error via %w so callers higher up can still errors.Is
// against it. Unknown errors fall through to the original backend
// wrapping.
func friendlyBackendError(backend string, err error) error {
	switch {
	case errors.Is(err, trackage.ErrNotFound):
		return fmt.Errorf("%s: tracking number not found: %w", backend, err)
	case errors.Is(err, trackage.ErrAuth):
		return fmt.Errorf("%s: authentication failed (check API key): %w", backend, err)
	case errors.Is(err, trackage.ErrRateLimited):
		return fmt.Errorf("%s: rate limited (back off and retry): %w", backend, err)
	case errors.Is(err, trackage.ErrCarrierRequired):
		return fmt.Errorf("%s: carrier required (pass --carrier): %w", backend, err)
	case errors.Is(err, trackage.ErrUnsupportedCarrier):
		return fmt.Errorf("%s: carrier not supported by this backend: %w", backend, err)
	}
	return fmt.Errorf("%s: %w", backend, err)
}

func statusLabel(s trackage.Status) string {
	switch s {
	case trackage.StatusPending:
		return "pending"
	case trackage.StatusInTransit:
		return "in transit"
	case trackage.StatusDelivered:
		return "delivered"
	case trackage.StatusException:
		return "exception"
	case trackage.StatusUnknown:
		return "unknown"
	default:
		return strings.ToLower(string(s))
	}
}
