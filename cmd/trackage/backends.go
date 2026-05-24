package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type backendRow struct {
	DisplayName string `json:"display_name"`
	Name        string `json:"name"`
	EnvKey      string `json:"env_key"`
	KeySource   string `json:"key_source"`
}

func newBackendsCmd(cfg config) *cobra.Command {
	return &cobra.Command{
		Use:   "backends",
		Short: "List the supported tracking backends and where each one's API key would come from",
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, listErr := listKeysInCredStore(cmd.Context(), cfg)
			if listErr != nil {
				//nolint:errcheck // stderr write errors are inactionable
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "trackage: warning: %v\n", listErr)
			}

			rows := make([]backendRow, 0, len(backendRegistry))
			for _, b := range backendRegistry {
				rows = append(rows, backendRow{
					DisplayName: b.DisplayName,
					Name:        b.Name,
					EnvKey:      b.EnvKey,
					KeySource:   string(inspectKeySource(b.EnvKey, b.Name, cfg, list)),
				})
			}

			out := cmd.OutOrStdout()
			if boolFlag(cmd, "json") {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			//nolint:errcheck // tabwriter writes are inactionable
			_, _ = fmt.Fprintln(w, "NAME\tID\tKEY SOURCE\tAPI KEY ENV VAR")
			for _, r := range rows {
				//nolint:errcheck // tabwriter writes are inactionable
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.DisplayName, r.Name, orDash(r.KeySource), r.EnvKey)
			}
			return w.Flush()
		},
	}
}
