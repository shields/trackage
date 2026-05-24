# Agents

This project follows the cross-cutting conventions documented at
[github.com/shields/right-answers](https://github.com/shields/right-answers).
Design rationale specific to trackage lives in
[`docs/design.md`](docs/design.md); per-backend deep dives are under
[`docs/research/`](docs/research/).

## Scope

trackage is a **library and CLI** for tracking parcels. There is no server, no
webhook receiver, no dashboard. Anything that requires inbound HTTP is out of
scope; reach for polling instead.
