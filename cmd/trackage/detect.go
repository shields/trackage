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
