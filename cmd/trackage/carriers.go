package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"msrl.dev/trackage"
)

func newCarriersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "carriers",
		Short: "List the canonical carriers trackage can normalize across backends",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if boolFlag(cmd, "json") {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(trackage.AllCarriers())
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			//nolint:errcheck // tabwriter writes are inactionable
			_, _ = fmt.Fprintln(w, "NAME\tID\tSHIPPO\tEASYPOST\t17TRACK\tTRACKINGMORE")
			for _, c := range trackage.AllCarriers() {
				//nolint:errcheck // tabwriter writes are inactionable
				_, _ = fmt.Fprintf(
					w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					c.Name, c.ID,
					orDash(c.Shippo),
					orDash(c.EasyPost),
					orDashInt(c.SeventeenTrack),
					orDash(c.TrackingMore),
				)
			}
			return w.Flush()
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orDashInt(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}
