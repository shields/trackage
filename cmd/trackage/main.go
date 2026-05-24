// Command trackage tracks parcels across multiple shipping providers.
//
// Usage examples:
//
//	trackage track 1Z999AA10123456784
//	trackage track --backend=easypost EZ1000000001
//	trackage track --json --backend=shippo --carrier=usps 9400111899223067387543
//	trackage carriers
//	trackage backends
//	trackage detect 1Z999AA10123456784
//
// Output is pretty by default. Pass --json for machine output. The CLI
// never auto-switches based on whether stdout is a TTY.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.1.0-dev"

// Indirection points so main()'s body is reachable from tests.
var (
	osExit = os.Exit
	osArgs = os.Args
)

func main() {
	osExit(realMain(osArgs[1:], os.Stdout, os.Stderr))
}

// realMain is the testable entry point. It builds a fresh root command,
// wires args/stdout/stderr, and returns the exit code main should use.
// Config-file load failures surface as a non-zero exit; a missing file
// is fine and yields the zero config.
func realMain(args []string, stdout, stderr io.Writer) int {
	cfg, err := loadConfigFromDefaultPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "trackage:", err) //nolint:errcheck // stderr write errors are inactionable
		return 1
	}
	root := newRoot(cfg)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, "trackage:", err) //nolint:errcheck // stderr write errors are inactionable
		return 1
	}
	return 0
}

// loadConfigFromDefaultPath looks up the config path via the injectable
// configPathFn seam, then delegates to loadConfig.
func loadConfigFromDefaultPath() (config, error) {
	path, err := configPathFn()
	if err != nil {
		return config{}, err
	}
	return loadConfig(path)
}

func newRoot(cfg config) *cobra.Command {
	root := &cobra.Command{
		Use:           "trackage",
		Short:         "Track parcels across multiple shipping providers",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringP("backend", "b", "",
		"backend to use: shippo, easypost, 17track, trackingmore (env: TRACKAGE_BACKEND)")
	root.PersistentFlags().Bool("json", false, "emit JSON instead of pretty output")

	root.AddCommand(newTrackCmd(cfg))
	root.AddCommand(newCarriersCmd())
	root.AddCommand(newBackendsCmd(cfg))
	root.AddCommand(newDetectCmd())
	root.AddCommand(newLoginCmd(cfg))
	root.AddCommand(newLogoutCmd(cfg))
	return root
}
