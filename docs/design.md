# Design notes

The choices specific to trackage that a competent developer would not infer from
the code, the linters, or the
[shields/right-answers](https://github.com/shields/right-answers) conventions.

## Canonical status enum is intentionally small

The library exposes only five statuses: `pending`, `in_transit`, `delivered`,
`exception`, `unknown`. Backend-specific detail belongs in `Tracking.Substatus`
(a free-form string carrying the upstream's own code verbatim) and
`Tracking.Raw` (the full upstream JSON). Do not grow the canonical enum without
checking back — every new value has to be mapped from four upstream
vocabularies, and the small set is the contract callers depend on.

## Carrier identification

Carriers are identified by lowercase snake_case IDs (`usps`, `dhl_express`,
`royal_mail`). The list is intentionally small — only carriers we can locally
detect from tracking-number format (`detect.go`). Detection covers UPS (`1Z…`),
USPS IMpb (22 digits beginning 91–95), FedEx (12 / 15 digits), DHL Express
(10–11 digits), and any UPU S10 international number whose origin country we
recognize.

When a caller passes an ID we do not have in the translation table, the string
passes through verbatim to the backend — they can use a backend-native code
(`DHLeCommerceSolutions`, `21051`, `china-ems`) for carriers we have not
curated. The four-column table in `carriers.go` is load-bearing: changes need
cross-backend research. Prefer adding a new detection rule over expanding the
table opportunistically.

## Async-by-default backends

`17track` and `easypost` are async: a first `Track()` call almost always returns
`StatusUnknown` with no events. Real data arrives later via the upstream's own
polling. Do not paper over this asymmetry — surface the unknown state and let
callers re-poll.

For 17Track specifically, `Track()` always does `register + gettrackinfo`. The
"already registered" error (`-18019901`) is not billed and is swallowed silently
before the get. For TrackingMore, `Track()` POSTs `/trackings/create`; on meta
`4101` ("tracking already exists") it falls back to `GET /trackings/get`.

## CLI output format is explicit, never inferred

The CLI prints human-readable output by default and JSON when `--json` is
passed. It does not auto-switch based on whether stdout is a TTY. Scripts and
interactive use behave identically.

## Timestamps preserve the upstream zone

Adapters return `time.Time` values whose `Location()` reflects the offset the
backend supplied — they do **not** normalize to UTC. The CLI's `track` output
prints each timestamp in that zone with the zone designator (`UTC`, `-0700`,
etc.), so the same scan rendered through different backends looks
self-consistent rather than getting flattened to UTC and then compared
apples-to-oranges.

Each backend ships its own timestamp shape:

- **Shippo** — single field `status_date`, ISO-8601 with `Z`. Always real UTC.
- **17Track** — `time_iso` (offset-aware, scan-local zone) and `time_utc` (`Z`).
  Both refer to the same instant. The adapter prefers `time_iso` so the rendered
  zone matches where the scan happened.
- **TrackingMore** — single field `c.Date`; sometimes naive
  (`"2015-11-02 17:11"`) with no zone information. Naive strings are tagged with
  a `time.FixedZone("local", 0)` sentinel so the CLI formatter can surface them
  as `local` rather than mislabel them as UTC. We deliberately do not use
  `time.UTC` (would silently imply a confirmed offset) or `time.Local`
  (`time.Parse` aliases that whenever a parsed offset happens to match the
  machine zone, which would mask real carrier-supplied timestamps as zoneless).
- **EasyPost** — ships **both** `datetime` and `datetime_local`. They look
  almost identical but mean different things:
  ```json
  "datetime":       "2026-05-21T09:23:47Z",
  "datetime_local": "2026-05-21T09:23:47-07:00"
  ```
  Same wall clock, different zone designators — i.e., not the same instant.
  Empirically `datetime_local` is the correct value: its offset matches the
  scan's `tracking_location`, and the resulting UTC time matches what Shippo
  reports for the same UPS event. `datetime` is the carrier-local wall clock
  with a literal `Z` tacked on (carrier-local mislabeled as UTC). The adapter
  prefers `datetime_local` and only falls back to `datetime` when the former is
  empty (some test-mode events have no geocode and so no localized value).

## Credential resolution

trackage composes two established conventions rather than inventing a key store
of its own:

- Config file location follows the
  [XDG Base Directory Spec](https://specifications.freedesktop.org/basedir-spec/),
  via `os.UserConfigDir()`, so the file lives at
  `$XDG_CONFIG_HOME/trackage/config.toml` (or the OS equivalent).
- OS-keychain access reuses the
  [Docker credential helper protocol](https://github.com/docker/docker-credential-helpers).
  When `creds_store = "osxkeychain"` (or `secretservice`, `pass`, `wincred`,
  etc.) is set in the config, trackage shells out to
  `docker-credential-<store> get` with the synthetic URL
  `https://trackage.invalid/<backend>` on stdin. Helpers ship pre-built for
  every major OS and store; we don't have to write OS-specific keychain code. If
  `creds_store` is unset, trackage auto-picks one by `runtime.GOOS`
  (`osxkeychain` / `secretservice` / `wincred`); when that helper isn't on PATH
  during `track`, the rung is treated as a miss and the chain continues. For
  `login` / `logout` the user is explicitly asking to interact with a keychain,
  so a missing helper bubbles up as an error instead of being silently skipped.

### Why `https://trackage.invalid/<backend>` for the key URL

The scheme is forced to `https://` because macOS Keychain Services and Windows
Credential Manager only accept `http` / `https` as the stored protocol type —
custom schemes like `trackage://` parse fine for the helper-protocol dispatch
but get rejected at the keychain backend with an opaque "exit status 1".

The host is `trackage.invalid` rather than a real domain. Nothing in the helper
protocol fetches the URL, but on macOS the Keychain Access GUI surfaces it as
the "Where" field, and Safari / iCloud Keychain use it to suggest stored
credentials when visiting the matching site. Picking a real domain (`msrl.dev`,
say) would cause Safari to autofill the trackage API key into login forms on
that site — confusing at best, a credential-leak vector at worst.
`trackage.invalid` is from RFC 6761's reserved-TLD list, will never resolve, and
will never match a real site Safari knows about.

Precedence, highest wins:

1. `--api-key` flag
2. `<BACKEND>_API_KEY` env var
3. `creds_store` in config (looked up via the Docker helper)
4. `api_keys.<backend>` in `config.toml`

Backend selection mirrors this: `--backend` › `TRACKAGE_BACKEND` ›
`default_backend` in the config file.

`*_FILE` env vars (the Docker / k8s file-mounted-secret pattern) were considered
and intentionally skipped — users who want file-mounted secrets can point the
docker credential helper at the same file (`docker-credential-pass` already does
this) or hand-load the key into their shell.

## Testing notes

### Faking external binaries via the test binary itself

`cmd/trackage/credstore_test.go` shells out to a fake `docker-credential-*`
helper. Rather than ship a shell script or stage a real binary in a
`t.TempDir()` (cross-platform pain), the fake is the test binary re-executed
with `TRACKAGE_FAKE_CRED_HELPER` set. `TestMain` sees the env var, dispatches
into `runFakeCredHelper`, writes canned stdout / stderr, and exits with the
chosen status. The `withFakeHelper(t, mode)` helper overrides the `execCommand`
seam in `credstore.go` to spawn this fake. The pattern is stdlib-only and
exercises the real `os/exec` + stdin/stdout/stderr path. Reuse it whenever we
need to mock an external binary.

## Per-backend research

Each of the four supported backends (Shippo, EasyPost, 17Track, TrackingMore)
has a notes file under [`docs/research/`](research/). Re-read the relevant file
before changing a backend's status mapping, carrier translation, auth flow, or
error handling — the upstream APIs disagree on essentially every dimension and
the differences are documented there, not in the upstream docs alone.
