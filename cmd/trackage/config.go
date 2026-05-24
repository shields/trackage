package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// configFileName is the on-disk name of trackage's TOML config inside the
// user's XDG config directory.
const configFileName = "config.toml"

// config mirrors the file at $XDG_CONFIG_HOME/trackage/config.toml. All
// fields are optional; absent ones simply don't contribute to the
// resolver's precedence chain.
type config struct {
	DefaultBackend string            `toml:"default_backend"`
	CredsStore     string            `toml:"creds_store"`
	APIKeys        map[string]string `toml:"api_keys"`
}

// configPathFn is the package's seam for locating the config file so
// tests can point trackage at a temp directory.
var configPathFn = configPath

// configPath returns the full path to the config file under
// $XDG_CONFIG_HOME/trackage/. The directory is not created here.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("trackage: locate user config dir: %w", err)
	}
	return filepath.Join(dir, "trackage", configFileName), nil
}

// parseConfig decodes TOML from r. Unknown keys are ignored to keep
// forward compatibility with config files written by newer trackage
// versions.
func parseConfig(r io.Reader) (config, error) {
	var c config
	if _, err := toml.NewDecoder(r).Decode(&c); err != nil {
		return config{}, fmt.Errorf("trackage: parse config: %w", err)
	}
	return c, nil
}

// loadConfig reads and parses the config at path. A missing file returns
// the zero config and a nil error — trackage runs fine without one. Any
// other I/O or parse error is returned to the caller.
func loadConfig(path string) (config, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from os.UserConfigDir() or test-controlled override
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config{}, nil
		}
		return config{}, fmt.Errorf("trackage: open config %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // close on read-only file is inactionable
	return parseConfig(f)
}
