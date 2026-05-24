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
