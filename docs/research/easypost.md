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

# EasyPost Tracking API — Research Report

Scope: the EasyPost **Tracker** object and supporting endpoints/webhooks only.
Shipment creation, label buying, rates, addresses, customs are out of scope.
Base URL: `https://api.easypost.com/v2`. All source URLs are listed at the end
of the report and referenced inline where load-bearing.

---

## 1. Authentication

**HTTP Basic Authentication** with the API key as the username and an **empty
password**. TLS 1.2+ required. Per
https://docs.easypost.com/docs/authentication: _"Each request must be
authenticated using an API Key, which serves as the Basic Authentication
username (no password is required)."_

```
Authorization: Basic <base64("<api_key>:")>
Content-Type: application/json
```

```bash
# AUTH_HEADER = "Basic " + base64(api_key + ":")
curl -H "Authorization: $AUTH_HEADER" \
     -H "Content-Type: application/json" \
     https://api.easypost.com/v2/trackers
```

**Test vs production**: separate test and production keys are issued from the
Dashboard. Test trackers fire one event then stop (see §14). The published auth
docs page does **not** document a fixed key prefix; third-party sources cite
`EZAK…` for production keys but EasyPost's own pages do not confirm it, so don't
validate on prefix.

---

## 2. Carrier list

EasyPost claims **100+ carriers** (https://www.easypost.com/carriers/). There is
**no machine-readable canonical list inside the Tracker docs**. Authoritative
sources:

- HTML directory: https://www.easypost.com/carriers/
- Carrier guides index: https://docs.easypost.com/carriers
- Programmatic: `GET /v2/metadata` (Carrier Metadata API) returns each carrier's
  `name`, `human_readable`, services, packages.

Headline carriers: USPS, UPS, FedEx, DHL Express, Canada Post, DHL eCommerce,
Maersk, Asendia, Amazon Shipping, Roadie, UniUni, APC, Australia Post, Canpar,
OnTrac, Purolator, Royal Mail, OSM Worldwide, plus dozens of regional/specialty
carriers. Tracking works for the full set without any per-carrier setup —
`CarrierAccount` is only required for shipment creation, not for tracking.

---

## 3. Carrier identification

`carrier` is **optional** on `POST /trackers`. Per the tracking guide: _"If
carrier is omitted, EasyPost attempts auto-detection. If the tracking code
cannot be matched to a supported carrier, an error is returned."_

**Casing gotcha** — EasyPost uses three conventions across its APIs:

- **Tracker endpoint** (request & response on `/trackers`): uppercase short
  codes, e.g. `"carrier": "USPS"`, `"FedEx"`, `"UPSDAP"`. This is what callers
  send.
- **Carrier Metadata** (`/v2/metadata`): lowercase, e.g.
  `?carriers=usps,fedex,ups`. Has a `human_readable` field for display form.
- **CarrierAccount.type**: `"UspsAccount"`, `"UpsAccount"`, `"FedexAccount"`.

For trackage, normalize to the **uppercase Tracker form**; when in doubt, omit
`carrier` and let auto-detection run.

---

## 4. Create a Tracker

**Endpoint**: `POST https://api.easypost.com/v2/trackers`

**Request body** (all fields optional except `tracking_code`):

```json
{
  "tracker": {
    "tracking_code": "EZ1000000001",
    "carrier": "USPS",
    "carrier_account_id": "ca_...",
    "amount": "10.00"
  }
}
```

Documented fields ([trackers ref](https://docs.easypost.com/docs/trackers)):

- `tracking_code` (string, required) — the carrier's tracking number.
- `carrier` (string, optional) — short code (e.g. `USPS`); omit to auto-detect.
- `carrier_account_id` (string, optional) — pin to a specific carrier account.
- `amount` (string, optional) — declared value, only used for return shipments /
  insurance.

**Response** (HTTP 201): a full `Tracker` object. Example shape (abridged from
the docs page):

```json
{
  "id": "trk_9135862729734d96ad2f9ed0f0565a27",
  "object": "Tracker",
  "mode": "test",
  "tracking_code": "EZ1000000001",
  "status": "pre_transit",
  "status_detail": "status_update",
  "signed_by": null,
  "weight": null,
  "est_delivery_date": null,
  "shipment_id": null,
  "carrier": "USPS",
  "public_url": "https://track.easypost.com/djE6dHJrXzkxMzU4NjI3Mjk3MzRkOTZhZDJmOWVkMGYwNTY1YTI3",
  "tracking_details": [
    {
      "object": "TrackingDetail",
      "message": "Pre-Shipment Info Sent to USPS",
      "status": "pre_transit",
      "status_detail": "status_update",
      "datetime": "2025-04-09T20:40:17Z",
      "source": "USPS",
      "tracking_location": {
        "object": "TrackingLocation",
        "city": null,
        "state": null,
        "country": null,
        "zip": null
      }
    }
  ],
  "carrier_detail": {
    "object": "CarrierDetail",
    "service": "First-Class Package Service",
    "container_type": null,
    "est_delivery_date_local": null,
    "est_delivery_time_local": null,
    "origin_location": "HOUSTON TX, 77001",
    "origin_tracking_location": {
      "object": "TrackingLocation",
      "city": "HOUSTON",
      "state": "TX",
      "country": null,
      "zip": "77063"
    },
    "destination_location": "CHARLESTON SC, 29401",
    "destination_tracking_location": null,
    "guaranteed_delivery_date": null,
    "alternate_identifier": null,
    "initial_delivery_attempt": null
  },
  "fees": [],
  "created_at": "2025-05-09T20:40:17Z",
  "updated_at": "2025-05-09T20:40:17Z"
}
```

---

## 5. Retrieve a Tracker

**By ID**: `GET /v2/trackers/{trk_…}` — returns the same `Tracker` shape as §4.

**By tracking code**: there is no dedicated `GET /trackers/by-code/...`
endpoint. Use the list endpoint with a filter:

```
GET /v2/trackers?tracking_codes=EZ1000000001
```

The docs describe `tracking_codes` as: _"Only return Trackers with the given
tracking codes. Useful for retrieving Trackers by tracking_code rather than by
their ids."_ (https://docs.easypost.com/docs/trackers).

This is one of the load-bearing details for the trackage adapter: callers will
frequently want lookup-by-code, and EasyPost makes you go through the list
endpoint to do it.

---

## 6. Status enum

Top-level `status` values (from https://docs.easypost.com/docs/trackers and the
support article "Understanding Trackers"):

| Value                  | Meaning                                                                                                                   |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `unknown`              | EasyPost has not yet received any meaningful update from the carrier (the typical initial state in production — see §14). |
| `pre_transit`          | Carrier has acknowledged the shipment (e.g. label scanned) but the package is not yet moving.                             |
| `in_transit`           | Package is moving through the carrier network.                                                                            |
| `out_for_delivery`     | Out for final-mile delivery.                                                                                              |
| `delivered`            | Delivered (terminal success).                                                                                             |
| `available_for_pickup` | Awaiting recipient pickup at a carrier location or locker.                                                                |
| `return_to_sender`     | Being returned to the shipper.                                                                                            |
| `failure`              | Delivery failure (carrier reported it cannot be delivered).                                                               |
| `cancelled`            | Tracker / shipment was cancelled. The docs use `cancelled` (British spelling).                                            |
| `error`                | EasyPost or the carrier could not process the tracker.                                                                    |

Notes:

- The docs page enumerates the values above. A separate search snippet listed
  `canceled` (US spelling), so be defensive about either spelling, but match the
  docs which spell it `cancelled`.
- The docs explicitly call out the top-level `status` as the field for business
  logic — prefer it over derived signals from `tracking_details`.

---

## 7. `status_detail`

`status_detail` is a finer-grained substatus. The values listed in
https://docs.easypost.com/docs/trackers are:

`address_correction`, `arrived_at_destination`, `arrived_at_facility`,
`arrived_at_pickup_location`, `awaiting_information`, `cancelled`, `damaged`,
`delayed`, `delivery_exception`, `departed_facility`,
`departed_origin_facility`, `expired`, `failure`, `held`, `in_transit`,
`label_created`, `lost`, `missorted`, `out_for_delivery`,
`received_at_destination_facility`, `received_at_origin_facility`, `refused`,
`return`, `status_update`, `transferred_to_destination_carrier`,
`transit_exception`, `unknown`, `weather_delay`.

Caveats:

- EasyPost does not publish a strict mapping from `status_detail` → `status`.
  Treat `status_detail` as informational and never derive coarse status from it.
- `status_detail` is present both at the top of the Tracker and on each
  `TrackingDetail` entry (the per-event substatus may differ from the
  tracker-level one).

---

## 8. Tracking details (events)

`tracker.tracking_details` is an array of `TrackingDetail` objects, ordered
**oldest first**, with new events appended as carriers scan the package.

Per-entry shape (https://docs.easypost.com/docs/trackers):

| Field               | Type              | Notes                                                                                                                                 |
| ------------------- | ----------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `object`            | string            | Always `"TrackingDetail"`                                                                                                             |
| `message`           | string            | Carrier-supplied scan summary, e.g. `"Pre-Shipment Info Sent to USPS"`                                                                |
| `description`       | string            | Sometimes present (long-form). The reference example shows `message`; `description` is documented in the field table but may be null. |
| `status`            | string            | One of the Status enum values from §6 (the status at the time of this scan, not necessarily the current overall status).              |
| `status_detail`     | string            | One of the substatuses from §7.                                                                                                       |
| `datetime`          | string (ISO-8601) | Timezone-aware where the carrier supplies one; otherwise UTC.                                                                         |
| `source`            | string            | Usually the carrier name (e.g. `"USPS"`); identifies which system produced the scan.                                                  |
| `tracking_location` | object            | Nested `TrackingLocation`: `city`, `state`, `country`, `zip` — all nullable.                                                          |

`TrackingLocation`:

```json
{
  "object": "TrackingLocation",
  "city": "HOUSTON",
  "state": "TX",
  "country": null,
  "zip": "77063"
}
```

`carrier_detail` (sibling of `tracking_details`, _not_ per-event) carries the
carrier-supplied metadata that's not tied to a single scan: `service`,
`container_type`, `est_delivery_date_local`, `est_delivery_time_local`,
`origin_location` (formatted string), `origin_tracking_location`
(`TrackingLocation`), `destination_location`, `destination_tracking_location`,
`guaranteed_delivery_date`, `alternate_identifier`, `initial_delivery_attempt`.

---

## 9. Webhooks

EasyPost does **not** offer polling for tracker updates — webhooks are the
supported path.

**Register a webhook**: `POST /v2/webhooks` with at minimum `url`. Optional:
`webhook_secret` (enables HMAC), `custom_headers` (up to 3, no `X-EasyPost-*`
names, ASCII <1000 chars).

**Event types for trackers**:

- `tracker.created` — fired once when the Tracker is first persisted.
- `tracker.updated` — fired on every subsequent change (status transitions, new
  scans).

**Payload**: the webhook POSTs an `Event` object whose `result` is the full
`Tracker`. `previous_attributes` records the prior values of changed fields
(e.g. `"status": "unknown"` when the status moved off `unknown`). Shape:

```json
{
  "object": "Event",
  "id": "evt_ad2365c22d1511f0b5364ffbb3e7a1f1",
  "mode": "production",
  "description": "tracker.updated",
  "status": "pending",
  "previous_attributes": { "status": "unknown" },
  "result": {
    "object": "Tracker",
    "id": "trk_...",
    "status": "in_transit",
    "tracking_details": [
      /* … */
    ]
  },
  "pending_urls": [],
  "completed_urls": [],
  "created_at": "2025-05-09T20:39:17.000Z",
  "updated_at": "2025-05-09T20:39:17.000Z"
}
```

When the same Event is fetched via `GET /events`, the `result` field is
**omitted**; it is only present in the webhook POST body.
(https://docs.easypost.com/docs/events)

**HMAC verification**:

- Sent in header **`X-Hmac-Signature`** (note: the support article also
  references `x-hmac-signature-v2`; the open-source `easypost-go` SDK reads
  `X-Hmac-Signature`). For a multi-version-safe integration, accept either
  header.
- Algorithm: **HMAC-SHA256**.
- Secret handling: the SDK applies **Unicode NFKD normalization** to the webhook
  secret before using it as the HMAC key.
- String-to-sign: the **raw request body bytes** (no canonicalization).
- Encoding: lowercase hex digest, prefixed by `hmac-sha256-hex=` in the header
  value.
- Sources: https://github.com/EasyPost/easypost-go/blob/master/webhook.go and
  https://support.easypost.com/hc/en-us/articles/39826034964237-Webhook-HMAC-Validation
- The support article also describes a timestamp window check to prevent replay;
  the exact tolerance is not published.

If you don't want to roll your own, all official SDKs expose a
`validate_webhook()` helper that takes (secret, headers, body) and either
returns the parsed Event or throws.

**Retries** (https://docs.easypost.com/guides/webhooks-guide):

- A receiver must return a 2XX within **7 seconds** or the delivery is
  considered failed.
- EasyPost retries **6 times** with increasing delay between retries.
- Receivers should be **idempotent**: EasyPost guarantees at-least-once
  delivery.
- Endpoints that repeatedly fail can be auto-disabled and must be re-enabled via
  the Webhook update endpoint.

**Transport**: webhook endpoints must be HTTPS with a publicly trusted
certificate; SSLv2/SSLv3 and export-grade ciphers are rejected.

---

## 10. List Trackers

**Endpoint**: `GET /v2/trackers`

**Query parameters** (https://docs.easypost.com/docs/trackers):

- `page_size` — int, max **100**, default **20**.
- `before_id` — return records created strictly before this id (cursor-style).
- `after_id` — return records created strictly after this id. **Mutually
  exclusive with `before_id`.**
- `start_datetime` / `end_datetime` — ISO timestamp range filter.
- `tracking_code` / `tracking_codes` — filter by tracking number(s).
- `carrier` — filter by carrier short code.

**Response**:

```json
{
  "trackers": [
    {
      "id": "trk_9135862729734d96ad2f9ed0f0565a27",
      "object": "Tracker",
      "status": "pre_transit",
      "carrier": "USPS",
      "tracking_code": "EZ1000000001",
      "created_at": "2025-05-09T20:40:17Z"
    }
  ],
  "has_more": true
}
```

**Pagination model**: cursor-based via `before_id` (and `has_more`). The
Paginating Lists support article
(https://support.easypost.com/hc/en-us/articles/360043967372-Paginating-Lists-via-the-API)
is explicit that any integration assuming offset/`page` semantics will silently
miss records past the first page. To paginate: take the **last** id of the
returned `trackers` array, pass it as `before_id` on the next request, and stop
when `has_more` is `false`.

---

## 11. Rate limits

Per the rate-limiting guide: **"Index endpoints" are limited to 5 requests per
second**. Exceeding returns HTTP 429. "Index" includes `GET /trackers`.
`POST /trackers` and `GET /trackers/{id}` have no published per-second number; a
separate "Load-based Limiter" can also return 429 under burst pressure.

**Headers**:

- No documented `X-RateLimit-*` headers.
- `Retry-After` _may_ be present on 429 — not guaranteed.

Recommended client behavior: exponential backoff with jitter on 429, respect
`Retry-After` if present, request raised limits via support for high-volume
accounts.

---

## 12. Pricing

- **Tracking API is free** when bundled with the Shipping API: _"Once integrated
  with our Shipping API, you have access to our Tracking API and webhooks."_
- Standalone trackers (label bought elsewhere): **$0.01–$0.03 per tracker** per
  the public pricing page.
- "Advanced Tracking" (branded pages, notifications): **$0.03 per shipment**.
- **Dedupe window**: per "Understanding Trackers", creating a tracker with the
  same `carrier`+`tracking_code` again within **3 months** returns the existing
  Tracker and is not double-charged. Integration-relevant: don't bother caching
  for dedupe — the server already does it.

Volume pricing is sales-driven and not on the public page.

---

## 13. Error response shape

From https://docs.easypost.com/docs/errors:

```json
{
  "error": {
    "code": "ADDRESS.VERIFY.FAILURE",
    "message": "Address not found",
    "errors": [
      {
        "field": "street1",
        "message": "House number is missing",
        "suggestion": null
      }
    ]
  }
}
```

Top-level fields:

- `code` — machine-readable dotted namespace (e.g. `TRACKER.CREATE.ERROR`,
  `RATE_LIMIT.EXCEEDED`).
- `message` — human description.
- `errors` — array of strings or `FieldError` objects
  `{ field, message, suggestion }`. Address-verification errors add a per-entry
  `code`.

Status codes used: **400** bad request, **401** auth, **402** payment, **403**
forbidden, **404** not found, **405** wrong method, **422** unprocessable,
**429** rate limited, **500/503** server. Common tracker error cases: 404
(unknown `trk_…`), 422 (no carrier match), 401 (bad key), 429 (burst), 500/503
(carrier-side outage).

---

## 14. Quirks and gotchas

These are the integration footguns — the most valuable part of this report for
the trackage adapter.

1. **`POST /trackers` is "synchronous-ish, async for real data".** The response
   returns immediately with a populated `Tracker`, but in **production** the
   initial `status` is almost always `unknown` with empty `tracking_details`.
   Real data shows up asynchronously via `tracker.updated` webhooks as scans
   arrive.

2. **Test-mode trackers are sticky to a single status.** Test mode emits one
   deterministic event and never progresses. Codes:
   `EZ1000000001`→`pre_transit`, `EZ2000000002`→`in_transit`,
   `EZ3000000003`→`out_for_delivery`, `EZ4000000004`→`delivered`,
   `EZ5000000005`→`return_to_sender`, `EZ6000000006`→`failure`,
   `EZ7000000007`→`unknown`.

3. **No polling — webhooks required for real-time updates.** Re-fetching
   `GET /trackers/{id}` returns EasyPost's cached state; they pull from carriers
   on their own schedule and won't re-poll on demand. Expect stale-by-hours data
   on bare reads.

4. **Server-side dedupe, 3-month window.** Same `carrier`+`tracking_code` within
   3 months returns the existing Tracker, no extra charge. Don't dedupe at your
   layer.

5. **Carrier casing is inconsistent across EasyPost APIs.** Tracker endpoint =
   uppercase (`USPS`, `FedEx`); Carrier Metadata = lowercase (`usps`);
   CarrierAccount.type = `UspsAccount`. Wrong case will silently fall through to
   auto-detection or 422.

6. **Heavy nullability.** `weight`, `est_delivery_date`, `signed_by`,
   `shipment_id`, every field on `carrier_detail`, and most of
   `tracking_location` are nullable. The trackage common model should not
   require any of these.

7. **`tracking_details[*].status` ≠ tracker `status`.** Each event carries its
   own status snapshot. The docs say to use the **top-level** `status` for
   business logic; don't derive it from the last event.

8. **`tracking_details` is appended cumulatively, oldest-first.** Every
   `tracker.updated` event ships the whole history. Some third-party code
   assumes newest-first — it isn't.

9. **`public_url` is opaque** (e.g. `https://track.easypost.com/djE6dHJrXz...`).
   Pass through verbatim if exposing a hosted view.

10. **Pagination is cursor-based.** `before_id` + `has_more`. `before_id` and
    `after_id` are mutually exclusive. Don't assume page numbers.

11. **HMAC header name varies.** SDK reads `X-Hmac-Signature`; support article
    mentions `x-hmac-signature-v2`. Accept either. Signature format:
    `hmac-sha256-hex=<lowercase-hex>` over raw body bytes, key = NFKD-normalized
    webhook secret.

12. **Webhook receivers must ack in 7 seconds.** 6 retries with backoff on
    failure. At-least-once delivery — handlers must be idempotent.

13. **`cancelled` vs `canceled` spelling.** EasyPost's docs spell the status
    `cancelled` (British). Some third-party docs spell it `canceled`. Match
    exactly what the API emits.

14. **No `Retry-After` guarantee on 429.** Use fixed backoff with jitter; treat
    `Retry-After` as a hint when present.

15. **No flat carrier enum.** No `GET /carriers` returning a clean list for
    code-gen. Use a static allow-list and otherwise pass strings through.

16. **`DELETE /v2/trackers/{id}` exists** but is rarely needed in production
    thanks to the dedupe window.

---

## Source URLs (cited throughout)

- https://docs.easypost.com/docs/trackers
- https://docs.easypost.com/docs/authentication
- https://docs.easypost.com/docs/events
- https://docs.easypost.com/docs/webhooks
- https://docs.easypost.com/docs/errors
- https://docs.easypost.com/docs/carrier-metadata
- https://docs.easypost.com/docs/carrier-types
- https://docs.easypost.com/guides/tracking-guide
- https://docs.easypost.com/guides/webhooks-guide
- https://docs.easypost.com/guides/rate-limiting-guide
- https://docs.easypost.com/carriers
- https://www.easypost.com/carriers/
- https://www.easypost.com/tracking-api/
- https://www.easypost.com/pricing/
- https://support.easypost.com/hc/en-us/articles/360044353091-Understanding-Trackers
- https://support.easypost.com/hc/en-us/articles/360043967372-Paginating-Lists-via-the-API
- https://support.easypost.com/hc/en-us/articles/43875047724941-Rate-Limiting-and-API-Reliability
- https://support.easypost.com/hc/en-us/articles/39826034964237-Webhook-HMAC-Validation
- https://github.com/EasyPost/easypost-go/blob/master/webhook.go
