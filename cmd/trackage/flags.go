package main

import "github.com/spf13/cobra"

// stringFlag reads a string flag value, discarding the (always-nil for
// our own flags) error from cobra. Using a helper keeps errcheck happy
// without spraying //nolint comments through every command.
func stringFlag(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name) //nolint:errcheck // our flag, cannot fail
	return v
}

// boolFlag reads a bool flag value, discarding the (always-nil for our
// own flags) error from cobra.
//
//nolint:unparam // currently used only for --json; will pick up more flags later
func boolFlag(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name) //nolint:errcheck // our flag, cannot fail
	return v
}
