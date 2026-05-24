package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"msrl.dev/trackage"
	"msrl.dev/trackage/backend/easypost"
	"msrl.dev/trackage/backend/seventeentrack"
	"msrl.dev/trackage/backend/shippo"
	"msrl.dev/trackage/backend/trackingmore"
)

// Sentinel errors returned by backend resolution. They wrap the
// human-readable message so callers can errors.Is() against them.
var (
	errNoBackend = errors.New(
		"no backend selected: pass --backend, set TRACKAGE_BACKEND, " +
			"or write default_backend in config.toml",
	)
	errUnknownBackend = errors.New("unknown backend")
	errNoAPIKey       = errors.New("no API key")
)

// backendInfo describes one supported backend for help / discovery.
// Name is the canonical id (used as --backend value); DisplayName is the
// vendor's own casing for human-facing output. Build accepts an
// optional *http.Client; nil means "let the backend pick a default".
type backendInfo struct {
	Name        string
	DisplayName string
	EnvKey      string
	Build       func(apiKey string, httpClient *http.Client) trackage.Tracker
}

var backendRegistry = []backendInfo{
	{
		Name: "shippo", DisplayName: "Shippo", EnvKey: "SHIPPO_API_KEY",
		Build: func(k string, c *http.Client) trackage.Tracker {
			return shippo.New(shippo.Config{APIKey: k, HTTPClient: c})
		},
	},
	{
		Name: "easypost", DisplayName: "EasyPost", EnvKey: "EASYPOST_API_KEY",
		Build: func(k string, c *http.Client) trackage.Tracker {
			return easypost.New(easypost.Config{APIKey: k, HTTPClient: c})
		},
	},
	{
		Name: "17track", DisplayName: "17Track", EnvKey: "SEVENTEENTRACK_API_KEY",
		Build: func(k string, c *http.Client) trackage.Tracker {
			return seventeentrack.New(seventeentrack.Config{APIKey: k, HTTPClient: c})
		},
	},
	{
		Name: "trackingmore", DisplayName: "TrackingMore", EnvKey: "TRACKINGMORE_API_KEY",
		Build: func(k string, c *http.Client) trackage.Tracker {
			return trackingmore.New(trackingmore.Config{APIKey: k, HTTPClient: c})
		},
	},
}

func backendByName(name string) (backendInfo, bool) {
	for _, b := range backendRegistry {
		if b.Name == name {
			return b, true
		}
	}
	return backendInfo{}, false
}

// resolveBackend selects a backend by name (flag › env › cfg.DefaultBackend)
// and resolves its API key via resolveAPIKey.
func resolveBackend(ctx context.Context, flagBackend, flagAPIKey string, cfg config) (trackage.Tracker, string, error) {
	name := flagBackend
	if name == "" {
		name = os.Getenv("TRACKAGE_BACKEND")
	}
	if name == "" {
		name = cfg.DefaultBackend
	}
	if name == "" {
		return nil, "", errNoBackend
	}
	info, ok := backendByName(name)
	if !ok {
		return nil, name, fmt.Errorf("%w %q (run `trackage backends` to list)", errUnknownBackend, name)
	}
	key, err := resolveAPIKey(ctx, info.EnvKey, flagAPIKey, name, cfg)
	if err != nil {
		return nil, name, err
	}
	var httpClient *http.Client
	if traceEnabled() {
		httpClient = newTraceClient()
	}
	return info.Build(key, httpClient), name, nil
}

// resolveAPIKey walks the four-rung precedence chain:
//
//  1. --api-key flag (flagVal)
//  2. <BACKEND>_API_KEY env var (envKey)
//  3. docker-credential-<creds_store> get
//     – cfg.CredsStore set → strict; any helper error bubbles.
//     – cfg.CredsStore unset → try the OS default (defaultCredsStore);
//     PATH-miss is treated as a miss so the chain continues, but
//     non-PATH errors still bubble.
//  4. api_keys.<backendName> in config.toml
//
// On miss it returns an errNoAPIKey enumerating every configured source.
func resolveAPIKey(ctx context.Context, envKey, flagVal, backendName string, cfg config) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if v := os.Getenv(envKey); v != "" {
		return v, nil
	}
	if key, ok, err := tryCredsStore(ctx, cfg.CredsStore, backendName); err != nil {
		return "", err
	} else if ok {
		return key, nil
	}
	if v, ok := cfg.APIKeys[backendName]; ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf(
		"%w for %s: pass --api-key, set %s, configure creds_store, "+
			"or set api_keys.%s in config.toml",
		errNoAPIKey, backendName, envKey, backendName,
	)
}

// tryCredsStore implements the strict-vs-lenient policy for the
// docker-credential-helper rung. A PATH-miss (helper binary not
// installed) is ALWAYS treated as a credstore miss so the chain can
// continue to api_keys.<name> — even when cfg.CredsStore was explicitly
// configured. This keeps `trackage track` consistent with the
// `trackage backends` KEY SOURCE column, which already proceeds past a
// missing helper (after emitting a warning) rather than erroring.
//
// Non-PATH helper errors (binary present but failed: permission denied,
// non-zero exit, malformed output) still bubble: when the helper is
// installed but misbehaving, the user should see the failure rather
// than have it silently masked by an api_keys fallback.
func tryCredsStore(ctx context.Context, configured, backendName string) (string, bool, error) {
	store := configured
	if store == "" {
		store = defaultCredsStore()
	}
	if store == "" {
		return "", false, nil
	}
	key, ok, err := fetchFromCredStore(ctx, store, backendName)
	if err != nil && !isHelperMissing(err) {
		return "", false, err
	}
	return key, ok, nil
}

// keySource names the rung of the credential precedence chain that
// supplied a backend's API key. The empty string means no key is
// available from any stored source.
type keySource string

const (
	sourceNone       keySource = ""
	sourceEnv        keySource = "env"
	sourceCredsStore keySource = "keychain"
	sourceConfig     keySource = "config"
)

// listKeysInCredStore runs `docker-credential-<store> list` once and
// returns the URL→username map, applying the same strict-vs-lenient
// policy as tryCredsStore: explicit cfg.CredsStore propagates errors,
// the OS auto-detect treats a missing helper as "no keys available."
//
// An empty (non-nil) map is returned when no helper is reachable so
// callers can use the result directly without nil-checking.
func listKeysInCredStore(ctx context.Context, cfg config) (map[string]string, error) {
	empty := map[string]string{}
	store := cfg.CredsStore
	if store == "" {
		store = defaultCredsStore()
	}
	if store == "" {
		return empty, nil
	}
	list, err := listFromCredStore(ctx, store)
	if err != nil {
		if cfg.CredsStore == "" && isHelperMissing(err) {
			return empty, nil
		}
		return empty, err
	}
	return list, nil
}

// inspectKeySource reports which rung of the precedence chain (excluding
// the per-call --api-key flag) would supply a key for the named backend,
// given the env and the result of a single credstoreList call. It is
// side-effect-free at call time — the caller is expected to have run
// listKeysInCredStore upfront, so this function never spawns the helper
// itself.
func inspectKeySource(envKey, backendName string, cfg config, credstoreList map[string]string) keySource {
	if os.Getenv(envKey) != "" {
		return sourceEnv
	}
	if _, ok := credstoreList[serviceURLFor(backendName)]; ok {
		return sourceCredsStore
	}
	if v, ok := cfg.APIKeys[backendName]; ok && v != "" {
		return sourceConfig
	}
	return sourceNone
}
