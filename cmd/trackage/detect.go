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
	"fmt"

	"github.com/spf13/cobra"

	"msrl.dev/trackage"
)

func newDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect <tracking-number>",
		Short: "Show which canonical carrier trackage's local detector picks for a number",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			number := args[0]
			id := trackage.DetectCarrier(number)
			out := cmd.OutOrStdout()
			if boolFlag(cmd, "json") {
				payload := map[string]string{
					"tracking_number": number,
					"carrier":         id,
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			if id == "" {
				_, err := fmt.Fprintf(out, "%s: no match (let the backend auto-detect)\n", number)
				return err
			}
			c, _ := trackage.LookupCarrier(id)
			_, err := fmt.Fprintf(out, "%s  →  %s (%s)\n", number, id, c.Name)
			return err
		},
	}
}
