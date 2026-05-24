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

# 17Track Tracking API — Research Report

17Track operates two products: a consumer site (17track.net) and a B2B Tracking
API (api.17track.net). This report covers **only the API**, focused on the
**current v2.2** release. Differences from v2/v1 are called out where they
matter. 17Track is purely a tracking aggregator — no labels, rates, manifests,
or returns.

Docs: v2.2 <https://asset.17track.net/api/document/v2.2_en/index.html> · v2
<https://asset.17track.net/api/document/v2_en/index.html> · v1
<https://asset.17track.net/api/document/v1_en/index.html> · Quick guide
<https://help.17track.net/hc/en-us/articles/30944262120729--Tracking-API-Quick-Guide>
· Carrier JSON <https://res.17track.net/asset/carrier/info/apicarrier.all.json>
· Carrier CSV <https://res.17track.net/asset/carrier/info/apicarrier.all.csv>

---

## 1. Authentication

- **Scheme:** static API key in an HTTP header. No OAuth, no Bearer, no request
  signing.
- **Header:** `17token: <your-secret-key>`.
- **Issuance:** generated in the admin dashboard
  (<https://api.17track.net/admin/settings>); rotation takes effect within ~5
  minutes per the docs.
- **Transport:** HTTPS only; docs recommend TLS 1.2+.
- **IP whitelist (optional):** configured in the dashboard. Calls from
  non-whitelisted IPs return `-18010001`. One key per tenant account; no
  per-user/per-env scoping.
- **Sandbox:** **none.** All testing hits production and consumes real quota.
  `/getquota` is the only free endpoint useful for exploration. This is a major
  gap versus US-carrier APIs.

---

## 2. Carrier list

- Advertised count: ~2,400 (marketing claims 3,300+; docs say 2,100+ — it
  fluctuates).
- **Canonical list** (static CDN assets, fetch and cache): JSON
  <https://res.17track.net/asset/carrier/info/apicarrier.all.json> · CSV
  <https://res.17track.net/asset/carrier/info/apicarrier.all.csv>

JSON record shape:

```json
{
  "key": 21051,
  "_country": 840,
  "_country_iso": "US",
  "_email": null,
  "_tel": "1-800-275-8777",
  "_url": "https://www.usps.com",
  "_name": "USPS",
  "_name_zh-cn": "美国邮政",
  "_name_zh-hk": "美國郵政"
}
```

**Carrier code numbering.** Positive integers, clustered loosely by geography
(not ISO-aligned, not sequential):

- 1xxx/2xxx/3xxx: national posts and regional couriers (China Post `3011`,
  Australia Post `1151`, Royal Mail `11031`).
- 21xxx: large North American postal (USPS `21051`).
- 100000+: global commercial couriers (DHL Express `100001`, UPS `100002`, FedEx
  `100003`).

Treat codes as opaque IDs and resolve by `_name`/`_country_iso`; new carriers
are added frequently but existing codes are stable.

---

## 3. Carrier identification (auto-detect)

**There is no `/getcarrier` endpoint** in any version. Auto-detection is built
into `/register` via `auto_detection: true` (the default). Submit a number
without a `carrier` and 17Track infers one, returning it on `accepted[]`. The
`origin` field carries confidence:

| `origin` | Meaning                                                 |
| -------- | ------------------------------------------------------- |
| `1`      | Auto-detected, or system corrected the code you passed. |
| `2`      | Code you passed was verified correct.                   |
| `3`      | Guess (no code passed, system unsure — may be wrong).   |

If nothing can be guessed, the number lands in `rejected[]` with `-18019903`
"carrier can not be detected". 17Track claims 80%+ hit rate. For corrections
after the fact, use `/changecarrier` (Section 7) — it's free, whereas
delete+re-register burns another quota unit.

---

## 4. Register a tracking number

- **Endpoint:** `POST https://api.17track.net/track/v2.2/register` (v2:
  `/track/v2/register`).
- **Headers:** `17token: <key>`, `Content-Type: application/json`.
- **Body:** JSON array of registration objects, **max 40 per call**.

```json
[
  {
    "number": "RR123456789CN",
    "carrier": 3011,
    "final_carrier": 21051,
    "auto_detection": true,
    "tag": "MyOrderId",
    "lang": "en",
    "param": "postcode123",
    "order_no": "ORD-2026-001",
    "order_time": "2026-05-20T08:00:00Z",
    "email": "customer@example.com"
  }
]
```

Field notes: `number` 5–50 chars (letters/digits/hyphen); `carrier` optional
when `auto_detection: true` (default); `final_carrier` hints last-mile (e.g.
China Post `3011` → USPS `21051`); `tag` is opaque caller metadata; `lang` (one
of `en, ja, fr, da, th, de, es, zh-hans`) enables translated event descriptions
and **costs an extra quota unit**; `param` carries extras some carriers require
(postal code, phone-last-4, formats like `"FR-1000AA"`); `order_no`/`order_time`
(v2.2) feed delivery-performance metrics; `email` subscribes the recipient to
update emails.

**Response:**

```json
{
  "code": 0,
  "data": {
    "accepted": [
      {
        "origin": 1,
        "number": "RR123456789CN",
        "tag": "MyOrderId",
        "carrier": 3011
      }
    ],
    "rejected": []
  }
}
```

**Batch / throughput:** 40 numbers/call, 3 req/s, soft ceiling of ~400 k
registrations/hour. **Registration returns no events** — the number is enrolled
for async tracking; data arrives later via webhook or polling. This
async-by-default model is the biggest behavioural difference from US-carrier
APIs.

---

## 5. Get tracking info

- **Endpoint:** `POST https://api.17track.net/track/v2.2/gettrackinfo`
- **Body:** Array of `{number, carrier}` objects (max 40).

```json
[{ "number": "RR123456789CN", "carrier": 3011 }]
```

**Response** (full shape, abridged inside arrays):

```json
{
  "code": 0,
  "data": {
    "accepted": [
      {
        "number": "RR123456789CN",
        "carrier": 3011,
        "param": "postcode123",
        "tag": "OrderID",
        "track_info": {
          "shipping_info": {
            "shipper_address": {
              "country": "CN",
              "state": "GD",
              "city": "SHENZHEN",
              "postal_code": "518000",
              "coordinates": { "longitude": "114.085947", "latitude": "22.547" }
            },
            "recipient_address": {
              "country": "US",
              "state": "CA",
              "city": "LOS ANGELES",
              "postal_code": "90001",
              "coordinates": { "longitude": "-118.2437", "latitude": "34.0522" }
            }
          },
          "latest_status": {
            "status": "Delivered",
            "sub_status": "Delivered_Other",
            "sub_status_descr": null
          },
          "latest_event": {
            "time_iso": "2022-05-02T14:34:00-04:00",
            "time_utc": "2022-05-02T18:34:00Z",
            "description": "Package delivered",
            "location": "LOS ANGELES, CA",
            "stage": "Delivered",
            "address": {
              "country": "US",
              "state": "CA",
              "city": "LOS ANGELES",
              "postal_code": "90001"
            }
          },
          "time_metrics": {
            "days_after_order": 10,
            "days_of_transit": 8,
            "days_of_transit_done": 8,
            "days_after_last_update": 0,
            "estimated_delivery_date": {
              "source": "Official",
              "from": "2022-05-01T08:00:00-04:00",
              "to": "2022-05-03T20:00:00-04:00"
            }
          },
          "milestone": [
            {
              "key_stage": "InfoReceived",
              "time_iso": "2022-04-25T10:00:00+08:00",
              "time_utc": "2022-04-25T02:00:00Z"
            },
            {
              "key_stage": "PickedUp",
              "time_iso": "2022-04-26T14:30:00+08:00",
              "time_utc": "2022-04-26T06:30:00Z"
            },
            {
              "key_stage": "Delivered",
              "time_iso": "2022-05-02T14:34:00-04:00",
              "time_utc": "2022-05-02T18:34:00Z"
            }
            /* full set: InfoReceived, PickedUp, Departure, Arrival, AvailableForPickup,
             OutForDelivery, Delivered, Returning, Returned (null times when unreached) */
          ],
          "misc_info": {
            "risk_factor": 0,
            "service_type": "Express",
            "weight_kg": "1.5",
            "pieces": "1",
            "dimensions": "20x15x10 CM",
            "local_number": "RR123456789CN",
            "local_provider": "AEON",
            "local_key": 3011
          },
          "tracking": {
            "providers_hash": 182180920,
            "providers": [
              {
                "provider": {
                  "key": 3011,
                  "name": "AEON",
                  "country": "CN",
                  "homepage": "http://aeon.com"
                },
                "service_type": "Express",
                "latest_sync_status": "Success",
                "latest_sync_time": "2022-05-02T18:34:00Z",
                "events_hash": -731027172,
                "events": [
                  {
                    "time_iso": "2022-04-25T10:00:00+08:00",
                    "time_utc": "2022-04-25T02:00:00Z",
                    "stage": "InfoReceived",
                    "description": "Order information received",
                    "location": "SHENZHEN",
                    "address": { "country": "CN", "city": "SHENZHEN" }
                  },
                  {
                    "time_iso": "2022-04-26T14:30:00+08:00",
                    "time_utc": "2022-04-26T06:30:00Z",
                    "stage": "InTransit_PickedUp",
                    "description": "Package picked up",
                    "location": "SHENZHEN",
                    "address": { "country": "CN", "city": "SHENZHEN" }
                  },
                  {
                    "time_iso": "2022-05-02T14:34:00-04:00",
                    "time_utc": "2022-05-02T18:34:00Z",
                    "stage": "Delivered",
                    "description": "Package delivered",
                    "location": "LOS ANGELES, CA",
                    "address": { "country": "US", "city": "LOS ANGELES" }
                  }
                ]
              }
            ]
          }
        }
      }
    ],
    "rejected": []
  }
}
```

**Batch:** 40/call. **Polling is free** — only the initial successful
registration consumes quota; repeated `/gettrackinfo` is unbilled.

---

## 6. Stop tracking / delete

Two operations, both taking arrays of `{number, carrier}`:

- **`POST /track/v2.2/stoptrack`** — halts automatic refresh; record stays
  queryable via `/gettrackinfo` and `/gettracklist` for 90 days, then
  auto-deletes.
- **`POST /track/v2.2/deletetrack`** — immediate, irreversible purge.

Stopped numbers can be revived **exactly once** via `/retrack`; further attempts
return `-18019905`. 17Track also auto-stops a number after 30 days with no
update, or 15 days past the delivered/exception state.

---

## 7. Change carrier / re-track

- **`POST /track/v2.2/changecarrier`** — reassign carrier (or final-mile
  carrier). Body: `number`, `carrier_old`, `carrier_new`, `final_carrier_old`,
  `final_carrier_new`. Hard cap of **5 changes per number**. Blocked on stopped
  numbers (`-18019806`) or before the first tracking result is back
  (`-18019808`).
- **`POST /track/v2.2/changeinfo`** — update only metadata: `tag`, `param`,
  `lang`.
- **`POST /track/v2.2/retrack`** — resume a stopped number; one-shot.
- **`POST /track/v2.2/push`** — manually replay the webhook for a number (useful
  for testing).
- **`POST /track/v2.2/getrealtimetrackinfo`** — synchronous live carrier query,
  cache-bypassed. Carrier subset only, special account authorisation required
  (`-18019818`, `-18019912`).

---

## 8. Status enum

### v2 / v2.2: string `status` + string `sub_status`

`status` is one of 9 values:

| `status`             | Meaning                                                 |
| -------------------- | ------------------------------------------------------- |
| `NotFound`           | Carrier has no record yet.                              |
| `InfoReceived`       | Label created; carrier acknowledged, not in possession. |
| `InTransit`          | Moving through the network.                             |
| `Expired`            | No update for an extended period; lost to tracking.     |
| `AvailableForPickup` | Awaiting recipient pickup.                              |
| `OutForDelivery`     | On the delivery vehicle.                                |
| `DeliveryFailure`    | Attempted but failed.                                   |
| `Delivered`          | Successfully delivered.                                 |
| `Exception`          | Returned/lost/damaged/etc. — abnormal terminal state.   |

`sub_status` is one of ~27–30 strings, pattern `<MainStatus>_<Reason>`:

| Main               | Sub-statuses                                                                                                                                                      |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| NotFound           | `NotFound_Other`, `NotFound_InvalidCode`                                                                                                                          |
| InfoReceived       | `InfoReceived`                                                                                                                                                    |
| InTransit          | `InTransit_PickedUp`, `InTransit_Departure`, `InTransit_Arrival`, `InTransit_Other`                                                                               |
| Expired            | `Expired_Other`                                                                                                                                                   |
| AvailableForPickup | `AvailableForPickup_Other`                                                                                                                                        |
| OutForDelivery     | `OutForDelivery_Other`                                                                                                                                            |
| DeliveryFailure    | `DeliveryFailure_NoBody`, `_Security`, `_Rejected`, `_InvalidAddress`, `_Other`                                                                                   |
| Delivered          | `Delivered_Other`                                                                                                                                                 |
| Exception          | `Exception_Returning`, `_Returned`, `_NoBody`, `_Security`, `_Damage` (sometimes `_Damaged`), `_Rejected`, `_Delayed`, `_Lost`, `_Destroyed`, `_Cancel`, `_Other` |

A `sub_status_descr` free-text field can appear alongside.

### v1: integer `e` field

| `e`  | Meaning                    |
| ---- | -------------------------- |
| `0`  | Not found                  |
| `10` | In transit                 |
| `20` | Expired                    |
| `30` | Pick up (ready for pickup) |
| `35` | Undelivered                |
| `40` | Delivered                  |
| `50` | Alert                      |

**Note:** the prompt mentioned code `60` (Returning), but `60` is **not in the
official v1 docs** — it appears in some community write-ups only. v1 has just
these 7 values; "Returning" is captured in v2/v2.2 via
`sub_status = Exception_Returning`. v1 also has an `f` (delivery time) timestamp
field — not a status. The v2/v2.2 string enums are the canonical model going
forward.

---

## 9. Tracking events

Each event under `track_info.tracking.providers[].events[]`:

```json
{
  "time_iso": "2022-04-26T14:30:00+08:00",
  "time_utc": "2022-04-26T06:30:00Z",
  "description": "Package picked up",
  "description_translation": {
    "lang": "en",
    "description": "Package picked up"
  },
  "location": "SHENZHEN",
  "stage": "InTransit_PickedUp",
  "address": {
    "country": "CN",
    "state": "GD",
    "city": "SHENZHEN",
    "street": null,
    "postal_code": "518000",
    "coordinates": { "longitude": "114.085947", "latitude": "22.547" }
  }
}
```

Notes:

- `stage` uses the same vocabulary as `sub_status` — use it to bucket events on
  the milestone timeline.
- Both timestamps are always present: `time_iso` (carrier-local with offset),
  `time_utc` (UTC `Z`). v2.2 also exposes a `time_raw` triplet
  (date/time/timezone).
- `description_translation` is populated only when `lang` was set on
  registration; otherwise only the raw carrier `description` is filled.
- `providers[]` is plural because a single number can hop carriers (China Post →
  USPS). Each provider carries its own `events_hash` and `latest_sync_*` so you
  can change-detect.
- v1 used a flatter event shape (`z`/`c`/`a` etc.); v2.2 is canonical.

---

## 10. Webhooks (push notifications)

Two events: `TRACKING_UPDATED` and `TRACKING_STOPPED`.

**Configuration:** webhook URL is set in the admin dashboard
(<https://api.17track.net/admin/settings>) — **no API to register webhooks
programmatically**. Setup errors `-18010201..-18010206` cover URL
required/invalid/test-failed/not-set.

**v2.2 payloads:**

```json
{
  "event": "TRACKING_UPDATED",
  "data": {
    "number": "RR123456789CN",
    "carrier": 3011,
    "tag": null,
    "track_info": {
      /* same shape as gettrackinfo */
    }
  }
}
```

```json
{
  "event": "TRACKING_STOPPED",
  "data": { "number": "RR123456789CN", "carrier": 3011, "tag": null }
}
```

**Signature.** SHA-256 **hex**, non-standard scheme:

- v1: `SHA256(event + "/" + JSON(data) + "/" + apiKey)`; delivered as
  `"sign": "<hex>"` inside the JSON body.
- v2/v2.2: `SHA256(<raw body> + "/" + apiKey)`; delivered in an HTTP header. The
  docs reference "the signature header field" without naming it explicitly in
  the public excerpt I could retrieve — implementers typically see
  `sign`/`signature`. The
  [Webhook Development Example Code](https://help.17track.net/hc/en-us/articles/37573146087321-Webhook-Development-Example-Code)
  help article is the source of truth for the exact header.

**Retry:** any non-200 triggers retries at **10 min, 30 min, 60 min**; after 3
failures the event is **dropped** (subsequent events for the same number still
deliver). No outbound IP list is published — pin TLS and verify the signature.

---

## 11. List registered numbers

- **Endpoint:** `POST /track/v2.2/gettracklist` — enumeration/search.
- **Filters:** `number`, `carrier`, `data_origin`, `package_status` (e.g.
  `InTransit`, `Delivered`), `tracking_status` (e.g. `Tracking`, `Stopped`), and
  date ranges `register_time_from/to`, `track_time_from/to`,
  `push_time_from/to`.
- **Pagination:** `page_no` (1-based) and `order_by` ∈ {`RegisterTimeAsc/Desc`,
  `PushTimeAsc/Desc`, `TrackTimeAsc/Desc`}. Page size is fixed at **40**.
  Response carries `page_total`, `page_no`, `data_total`, `page_size`.
- Returns a slimmer projection than `/gettrackinfo` — drive
  reconciliation/backfill jobs from it.

---

## 12. Rate limits & quotas

- **Per-second limit:** `3 req/s` per API key. Exceeding returns HTTP **429**.
  The docs do not specify a `Retry-After` header; implementers should back off
  ~350 ms.
- **Per-call batch limit:** 40 numbers in any of `register`, `gettrackinfo`,
  `changecarrier`, `changeinfo`, `stoptrack`, `retrack`, `deletetrack`, `push`.
- **Per-hour ceiling:** ~400,000 registrations/hour (soft, advertised).
- **Daily limit:** Per-account daily registration cap (`-18019907` "exceeds
  daily tracking limit").
- **Lifetime quota:** Plan-based — see Section 13.

**429 / error envelope shape:**

```json
{
  "code": 429,
  "data": {
    "errors": [{ "code": 429, "message": "Too many requests." }]
  }
}
```

This shape (a top-level `code` plus `data.errors[]`) is also used for top-level
failures like `-18010001` (IP not whitelisted) or `-18010002` (invalid key).
Per-record errors live under `data.rejected[]` instead (Section 14).

---

## 13. Pricing model

- **Unit of charge:** one quota per **successfully registered tracking number**,
  not per call and not per event. Polling, webhook deliveries, carrier
  auto-detect, status changes — all free after registration.
- **Quota lifetime:** Plan quotas remain valid for **12 months** from purchase.
  Unused quota expires.
- **Quota multipliers:** Setting `lang` (translated event descriptions) costs an
  **additional unit per registration**. Calling `/changecarrier` on an existing
  number does **not** consume a fresh unit; deleting and re-registering
  **does**.
- **Real-time channel** (`/getrealtimetrackinfo`) is billed separately and
  requires special account authorisation.
- **Free tier (historical):** 100 free quota refilled monthly.
- **Free tier (effective 2026-01-07 00:00 UTC):** The monthly refill is
  discontinued. New accounts receive a **one-time 200-quota allocation**.
- **Paid plans:** Start around $9/month (per marketing page) with prepaid quota
  bundles. See <https://www.17track.net/en/api>.
- **Quota visibility:** `POST /track/v2.2/getquota` (empty body `[]`) returns
  `quota_total`, `quota_used`, `quota_remain`, `max_track_daily`,
  `free_email_quota`, `free_email_quotaused`.

---

## 14. Error response shape

**Top-level envelope (every call):**

```json
{
  "code":  0,
  "data": { "accepted": [ ... ], "rejected": [ ... ] }
}
```

`code: 0` means "the call itself succeeded". A non-zero `code` indicates a
whole-call failure (auth, rate limit, malformed body):

```json
{
  "code": -18010002,
  "data": {
    "errors": [{ "code": -18010002, "message": "Invalid security key." }]
  }
}
```

**Per-record rejection** (some accepted, some not):

```json
{
  "code": 0,
  "data": {
    "accepted": [{ "number": "RR123456789CN", "carrier": 3011, "origin": 2 }],
    "rejected": [
      {
        "number": "1234",
        "error": {
          "code": -18010012,
          "message": "The format of '1234' is invalid."
        }
      }
    ]
  }
}
```

**Common reject/error codes:**

| Code                                    | Meaning                                                                                          |
| --------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `0`                                     | Success.                                                                                         |
| `-18010001`                             | IP not in whitelist.                                                                             |
| `-18010002`                             | Invalid security key.                                                                            |
| `-18010003`                             | Internal service error.                                                                          |
| `-18010004`                             | Account disabled.                                                                                |
| `-18010010` / `-18010011` / `-18010012` | Missing / invalid value / invalid format for field (`-12` is the most common per-record reject). |
| `-18010013`                             | Invalid submitted data.                                                                          |
| `-18010014`                             | Request exceeds 40-number batch limit.                                                           |
| `-18010015`                             | Invalid value `{0}` for field `{1}`.                                                             |
| `-18010016`, `-18010018..22`            | Last-mile constraints; country/postal/phone/date required.                                       |
| `-18010201..-18010206`                  | Webhook config/test failures.                                                                    |
| `-18019801..-18019811`                  | `/changecarrier` and `/changeinfo` failures (5-change cap, can't change while stopped, etc.).    |
| `-18019815..18019817`                   | Carrier-side timeout, interface error, charge-failed.                                            |
| `-18019818`, `-18019912`                | Real-time endpoint: unsupported carrier / unauthorised access.                                   |
| `-18019901`                             | Tracking number already registered.                                                              |
| `-18019902`                             | Tracking number not registered.                                                                  |
| `-18019903`                             | Carrier cannot be auto-detected.                                                                 |
| `-18019904` / `-18019905`               | Only stopped numbers can be retracked / one retrack per number.                                  |
| `-18019906`                             | Only tracked numbers can be stopped.                                                             |
| `-18019907`                             | Daily tracking limit exceeded.                                                                   |
| `-18019908`                             | Quota exhausted.                                                                                 |
| `-18019909`                             | No tracking info available.                                                                      |
| `-18019910`                             | Incorrect carrier code `{0}`.                                                                    |
| `-18019911`                             | Carrier registration unavailable.                                                                |

**HTTP status mapping:** `200` success (envelope `code` is the business
outcome), `401` auth/whitelist/disabled, `404` wrong URL, `429` rate limit,
`500` server, `503` suspended. Full reference:
<https://help.17track.net/hc/en-us/articles/37570440168473-Error-Codes-and-Troubleshooting>

---

## 15. Quirks & gotchas

1. **Asynchronous by default.** `/register` returns no events. Calling
   `/gettrackinfo` immediately after registration almost always returns
   `NotFound` or empty `events[]`. First real data arrives via webhook
   (preferred) or after a 5+ minute delay — the inverse of FedEx/UPS/USPS, which
   respond synchronously.
2. **Webhooks are the intended primary channel.** Polling wastes the 3 req/s
   budget. Build the webhook receiver first; polling is fallback.
3. **No sandbox.** Production-only; testing burns real quota.
4. **Carriers are opaque integer codes** with non-obvious clustering. Always
   resolve by name from the carrier list — don't hard-code beyond the few you
   actually use.
5. **No `/getcarrier`.** Auto-detect lives inside `/register`. Treat `origin: 3`
   (guess) as untrusted; verify via early events.
6. **Records are scoped by `(number, carrier)`.** The same number registered to
   two carriers is two records; `/gettrackinfo` always requires the carrier — no
   "find any carrier for X" lookup.
7. **Webhook signature is non-standard.** SHA-256 hex of `body + "/" + key`
   (v2/v2.2) or `event/data/key` (v1) — not HMAC. Use the help-centre sample
   code as the source of truth for the header name.
8. **Three retries, then dropped.** 10/30/60 minutes after a non-200; if all
   fail, the event is gone. Your receiver must be near-100% reliable or you must
   reconcile via `/gettracklist` + `/gettrackinfo`.
9. **Quota maths.** Register = 1 unit. `lang` = +1 unit. Delete+re-register = +1
   unit. `/changecarrier` is **free** (up to 5 per number) — always prefer it
   over delete+re-register.
10. **One-shot retrack.** Stopped numbers can be revived exactly once.
11. **Auto-stop at 30/15 days.** Long-tail packages silently fall off tracking —
    refresh status before the window expires.
12. **Time formats:** `time_iso` carries carrier-local offset; `time_utc` is UTC
    `Z`; v2.2 adds a `time_raw` triplet. Sort/compare on `time_utc`.
13. **Free tier shrinks 2026-01-07.** Monthly 100-unit refill ends; new accounts
    get one-shot 200 units. Existing free-tier users must move to a paid plan.
14. **Status enums differ by version.** v1 = integer `e` (0/10/20/30/35/ 40/50);
    v2/v2.2 = string `status` + `sub_status`. No shared code.
15. **Chinese provider, email-only support** (`apisupport@17track.net`), no SLA
    — factor into customer-facing flows.
16. **Doc URLs cache-bust with `?v=…`.** Bookmark the base URL
    `asset.17track.net/api/document/v2.2_en/index.html` without it.

---

### Sources

- v2.2 doc: <https://asset.17track.net/api/document/v2.2_en/index.html>
- v2 doc: <https://asset.17track.net/api/document/v2_en/index.html>
- v1 doc: <https://asset.17track.net/api/document/v1_en/index.html>
- Quick Guide:
  <https://help.17track.net/hc/en-us/articles/30944262120729--Tracking-API-Quick-Guide>
- Supported carriers:
  <https://help.17track.net/hc/en-us/articles/37467353177753-Supported-carriers-and-carrier-codes>
- Webhook example code:
  <https://help.17track.net/hc/en-us/articles/37573146087321-Webhook-Development-Example-Code>
- Webhook config:
  <https://help.17track.net/hc/en-us/articles/37470032453529-What-is-a-webhook-How-to-configure-it>
- Plan Details:
  <https://help.17track.net/hc/en-us/articles/37575217580825-Plan-Details>
- Error codes:
  <https://help.17track.net/hc/en-us/articles/37570440168473-Error-Codes-and-Troubleshooting>
- API landing: <https://www.17track.net/en/api>
- Carrier list JSON/CSV:
  <https://res.17track.net/asset/carrier/info/apicarrier.all.json> ·
  <https://res.17track.net/asset/carrier/info/apicarrier.all.csv>
