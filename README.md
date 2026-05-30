<!--
Copyright © 2026 Michael Shields

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# trackage

A CLI and Go library that tracks parcels across multiple shipping providers
behind a single interface. Supports Shippo, EasyPost, 17Track, and TrackingMore.

## Install

```
go install msrl.dev/trackage/cmd/trackage@latest
```

## Quick start

```sh
# Save your API key to the OS keychain (no-echo prompt on a TTY).
trackage login shippo

# Track a package.
trackage track --backend=shippo 9400111899223067387543
```

`login` writes the key to your OS keychain via the matching docker credential
helper (`docker-credential-osxkeychain` on macOS, `-secretservice` on Linux,
`-wincred` on Windows). If the helper isn't installed, or you want a different
storage mechanism, see [Credentials](#credentials) — env var, config-file
plaintext, and explicit helper overrides all work.

Write `default_backend = "shippo"` to `$XDG_CONFIG_HOME/trackage/config.toml` to
skip `--backend=` on every call.

Other commands:

```sh
trackage track --json …       # machine-readable output
trackage carriers             # canonical carriers and how each backend names them
trackage backends             # supported backends and their API-key env vars
trackage detect 1Z999…        # try the local carrier detector on a number
```

Set `TRACKAGE_TRACE=1` to dump the raw HTTP request and response (curl-style) to
stderr for any upstream API call. Every credential-bearing header is redacted:
`Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, and each
backend's own auth header (17Track's `17token`, TrackingMore's
`Tracking-Api-Key`). Stdout is unchanged, so `--json | jq …` keeps working with
tracing on.

Pass `--carrier=<id>` to force a carrier hint. Omitted, trackage detects the
carrier from the tracking number's format when it can (UPS `1Z…`, USPS IMpb,
FedEx 12/15-digit, DHL Express 10–11-digit, and any
[UPU S10](<https://en.wikipedia.org/wiki/S10_(UPU_standard)>) national number),
and otherwise lets the backend auto-detect. Shippo requires an explicit carrier
and will error if neither the detector nor the caller supplies one.

## Library

```go
package main

import (
    "context"
    "fmt"

    "msrl.dev/trackage"
    "msrl.dev/trackage/backend/easypost"
)

func main() {
    t := easypost.New(easypost.Config{APIKey: "…"})
    r, err := t.Track(context.Background(), trackage.CarrierUSPS, "EZ1000000001")
    if err != nil {
        panic(err)
    }
    fmt.Println(r.Status, r.Description)
}
```

Every backend implements the same `trackage.Tracker` interface. Adapters live
under `backend/shippo`, `backend/easypost`, `backend/seventeentrack`, and
`backend/trackingmore`.

## Credentials

trackage resolves a backend's API key from the first source that fires, in this
order:

1. `--api-key` flag
2. `<BACKEND>_API_KEY` env var (`SHIPPO_API_KEY`, `EASYPOST_API_KEY`,
   `SEVENTEENTRACK_API_KEY`, `TRACKINGMORE_API_KEY`)
3. `creds_store` in `config.toml` — shells out to
   [`docker-credential-<store>`](https://github.com/docker/docker-credential-helpers)
   with the URL `https://trackage.invalid/<backend>`. The same helpers Docker,
   Podman, and ko use (`osxkeychain`, `secretservice`, `pass`, `wincred`, etc.)
   work verbatim. **If `creds_store` is unset**, trackage auto-picks one by OS —
   `osxkeychain` on macOS, `secretservice` on Linux, `wincred` on Windows — and
   silently falls through to rung 4 when that helper isn't installed.
4. `api_keys.<backend>` in `config.toml` (plaintext)

To stash a key in your OS keychain (set `creds_store` explicitly to override the
per-OS default), run:

```sh
trackage login shippo          # prompts without echo on a TTY
echo "$KEY" | trackage login shippo   # accepts piped input for scripting
trackage logout shippo         # idempotent; no error if not logged in
```

Pass `--creds-store=<name>` to either command to override the config value for a
single invocation. Both commands shell out to the helper named in `creds_store`,
using the synthetic URL `https://trackage.invalid/<backend>`.

## Status model

trackage normalizes every backend's status enum into five canonical values:
`pending`, `in_transit`, `delivered`, `exception`, `unknown`. Backend-specific
detail is preserved on `Tracking.Substatus` (verbatim string) and `Tracking.Raw`
(the upstream JSON, untouched). See [docs/design.md](docs/design.md) for why the
enum is small and where the load-bearing edges are.

## Development

```sh
make build           # go build ./...
make test            # go test -race ./...
make coverage-check  # enforce 100% statement coverage
make lint            # gofumpt + golangci-lint + Prettier on Markdown
make fmt             # apply gofumpt + Prettier
```

Cross-cutting conventions follow
[shields/right-answers](https://github.com/shields/right-answers).
Project-specific design notes are in [docs/design.md](docs/design.md);
per-backend research lives under [`docs/research/`](docs/research/).
[`AGENTS.md`](AGENTS.md) is the short agent-facing pointer.
