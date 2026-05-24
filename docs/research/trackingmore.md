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

# TrackingMore Tracking API — Research Report

**Target version:** v4 (current). v3 and v2 are legacy but still operational.
**Base URL:** `https://api.trackingmore.com/v4` **Compiled:** 2026-05-23

Sources are TrackingMore's official SDKs on GitHub (PHP, Ruby, Node.js, .NET,
Go), TrackingMore's docs/support pages, and the Postman collection. The new
`trackingmore.com/docs/trackingmore/*` portal is JavaScript-rendered and was not
reliably scrapeable, so v4 specifics were cross-checked against the SDK source
on GitHub.

---

## 1. Authentication

- **Scheme:** API key in a custom HTTP header. No OAuth, no body signing.
- **Header name (v4 and v3):** `Tracking-Api-Key`
- **Header name (v2 only):** `Trackingmore-Api-Key` — a real difference, not a
  typo.
- **Send on every request**, with `Content-Type: application/json` and
  `Accept: application/json`.

Confirmed in PHP SDK `src/Request.php`:

```php
private $apiBaseUrl = 'api.trackingmore.com';
private $apiVersion = 'v4';
private $headerKey  = 'Tracking-Api-Key';
```

```bash
curl -X POST https://api.trackingmore.com/v4/trackings/create \
  -H "Content-Type: application/json" \
  -H "Tracking-Api-Key: YOUR_API_KEY" \
  -d '{"tracking_number":"9400111899562537624646","courier_code":"usps"}'
```

API keys are generated in the TrackingMore dashboard (`my.trackingmore.com`).
Deleting a key returns meta code `4002` on subsequent calls.

Sources: https://github.com/TrackingMore-API/trackingmore-sdk-php ·
https://support.trackingmore.com/en/article/differences-among-3-versions-of-trackingmore-api-1ip0fmh/

---

## 2. Carrier List

- **Endpoint:** `GET /v4/couriers/all`
- **Body:** none.
- **Total carriers:** TrackingMore currently advertises 1,599–1,602 carriers
  (was 1,300+ earlier).
- **Carrier code format:** lowercase ASCII slugs, hyphen-separated. Examples:
  `usps`, `fedex`, `dhl`, `ups`, `china-post`, `china-ems`, `royal-mail`,
  `phlpost`, `lietuvos-pastas`, `poste-italiane`.

Response (v2 sample; v4 keeps the same envelope and core fields, plus expanded
metadata):

```json
{
  "meta": { "code": 200, "type": "Success", "message": "Success" },
  "data": [
    {
      "name": "DHL",
      "code": "dhl",
      "phone": "1 800 225 5345",
      "homepage": "//www.dhl.com/",
      "type": "express",
      "picture": "//cdn.trackingmore.com/images/icons/express/dhl.png"
    },
    {
      "name": "China post",
      "code": "china-post",
      "phone": "86 20 11185",
      "homepage": "//intmail.183.com.cn/",
      "type": "globalpost",
      "picture": "//cdn.trackingmore.com/images/icons/express/companylogo/3010.jpg"
    }
  ]
}
```

`type` is a category like `express`, `globalpost`, `postal`. v4 carries more
carrier metadata fields than shown here, but TrackingMore doesn't publish a
public field-by-field schema.

Sources: https://www.trackingmore.com/api-carriers-list-all-carriers.html ·
https://github.com/TrackingMore-API/trackingmore-sdk-php

---

## 3. Carrier Identification (Auto-Detect)

- **Endpoint:** `POST /v4/couriers/detect`
- **Body:** `{ "tracking_number": "<string>" }`
- **Cost:** Free in v4 (claimed 97% accuracy). v2/v3 charged 0.2 credits per
  call. Strong reason to use v4.
- Returns 0+ candidate carriers.

```json
// Response
{
  "meta": { "code": 200, "type": "Success", "message": "Success" },
  "data": [{ "name": "China post", "code": "china-post" }]
}
```

On failure: meta `4121` ("Cannot detect courier") inside HTTP 400, or empty
`data: []`. Some carriers also need extra fields (postal code, ship date) — meta
`4122` flags missing ones.

Sources: https://github.com/TrackingMore-API/trackingmore-sdk-php
(`Couriers::detect`) ·
https://www.trackingmore.com/api-carriers-detect-carrier.html ·
https://support.trackingmore.com/en/article/differences-among-3-versions-of-trackingmore-api-1ip0fmh/

---

## 4. Create Tracking

### Single create

- **Endpoint:** `POST /v4/trackings/create`
- **Required:** `tracking_number`, `courier_code`
- **Important:** in v4 this is **create-and-get** ("real-time API"). The
  response includes any available checkpoints immediately; no separate GET
  needed.
- **Per-endpoint rate limit: 3 req/s** (lower than the rest of the API).

```json
{
  "tracking_number": "9400111899562537624646",
  "courier_code": "usps",
  "order_number": "",
  "customer_name": "",
  "customer_email": "",
  "title": "",
  "language": "en",
  "note": "test Order"
}
```

Optional fields: `order_number`, `title`, `customer_name`, `customer_email`,
`note`, `language`. Carriers requiring extras accept additional top-level keys
(meta `4122` flags omissions).

### Batch create

- **Endpoint:** `POST /v4/trackings/batch`
- **Body:** a top-level JSON **array** (not wrapped), **max 40 items** per call
  (meta `4103` otherwise).

```json
[
  { "tracking_number": "92612903029511573030094531", "courier_code": "usps" },
  { "tracking_number": "92612903029511573030094532", "courier_code": "usps" }
]
```

The batch response splits successes and failures so per-item errors (e.g.,
`4101` "already exists") don't fail the whole call.

Sources: https://github.com/TrackingMore-API/trackingmore-sdk-php ·
https://github.com/TrackingMore-API/trackingmore-sdk-nodejs ·
https://support.trackingmore.com/en/article/trackingmore-create-api-122cydg/

---

## 5. Get Tracking Status

### `GET /v4/trackings/get` — list / multi-get

Query parameters:

- `tracking_numbers` — comma-separated list
- `courier_code` — filter
- `created_date_min`, `created_date_max` — ISO-8601 with offset (e.g.,
  `2023-08-23T06:00:00+00:00`)
- `page` (default 1), `limit` (default 100, max 200 in v4)
- `delivery_status` — filter by status enum (section 8)
- `order_number`, `archived`

```
GET /v4/trackings/get?tracking_numbers=92612903029511573030094532&courier_code=usps
```

### By ID

The SDKs do not expose a `GET /v4/trackings/{id}` path. Use
`GET /v4/trackings/get` with filters, or rely on the `id` returned from create.
The internal `id` is a 32-char hex string (e.g.,
`9a035f5cdd0437c55d48e223c705a66c`); it is used as the path segment for
update/delete/retrack.

### Envelope (v2 shape — v4 is a superset, see section 10 for v4 field renames)

```json
{
  "meta": { "code": 200, "type": "Success", "message": "Success" },
  "data": {
    "page": 1, "limit": 25, "total": "56",
    "items": [
      {
        "id": "009e9a8a6450cb5ce4b53ac75674fe78",
        "tracking_number": "RR050349575PH",
        "carrier_code":    "phlpost",
        "status":          "delivered",
        "created_at": "2015-10-30T11:35:16+08:00",
        "updated_at": "2015-11-03T13:47:20+08:00",
        "title": null, "order_id": null,
        "customer_name": null, "customer_email": null,
        "archived": false,
        "original_country":    "Philippines",
        "destination_country": "United States",
        "itemTimeLength": 12,
        "origin_info":      { "weblink": "...", "phone": "...", "carrier_code": "phlpost", "trackinfo": [ ... ] },
        "destination_info": { "weblink": "...", "phone": "...", "carrier_code": "usps",   "trackinfo": [ ... ] },
        "lastEvent":      "Delivered,RENTON, WA 98056,2015-11-02 17:11",
        "lastUpdateTime": "2015-11-02 17:11"
      }
    ]
  }
}
```

**v4 renames:** `status` → `delivery_status`, `lastEvent` → `latest_event`,
`lastUpdateTime` → `latest_checkpoint_time`. v4 adds `substatus`. v4 carriers
may show `courier_code` and `carrier_code` interchangeably — code defensively.

**v4 envelope:** `data` is the **array of items directly** — the
`{items, page, limit, total}` wrapper from v2 is gone. Pagination/total are not
surfaced on this endpoint in v4. Observed:

```json
{
  "meta": { "code": 200, "message": "Request response is successful" },
  "data": [{ "id": "...", "tracking_number": "...", "...": "..." }]
}
```

Sources: https://github.com/TrackingMore-API/trackingmore-sdk-php
(`Trackings::getTrackingResults`) ·
https://www.trackingmore.com/api-track-list-all-track.html ·
https://support.trackingmore.com/en/article/differences-among-3-versions-of-trackingmore-api-1ip0fmh/

---

## 6. Update / Delete

### Update

- **`PUT /v4/trackings/update/{id}`**
- Updatable fields (from SDK): `customer_name`, `customer_email`, `title`,
  `note`, `order_number`. Partial — only sent fields change.

```json
PUT /v4/trackings/update/9a035f5cdd0437c55d48e223c705a66c
{ "customer_name": "New name", "note": "New tests order note" }
```

### Delete

- **`DELETE /v4/trackings/delete/{id}`**, no body.
- Hard delete — quota is **not** refunded.
- v2 had a separate `archive` endpoint; v4 dropped it in favor of straight
  delete + the `archived` filter on `get`.

Source: https://github.com/TrackingMore-API/trackingmore-sdk-php

---

## 7. Retrack an Expired Tracking

- **`POST /v4/trackings/retrack/{id}`**, no body.
- Only permitted when `delivery_status == "expired"`. Otherwise meta `4113`
  ("Retrack is not allowed. You can only retrack an expired tracking.").
- TrackingMore marks a tracking expired after 30 days without a new checkpoint
  and not yet delivered (substatus `expired001`). Retrack resumes polling.

Sources: https://github.com/TrackingMore-API/trackingmore-sdk-php ·
https://support.trackingmore.com/en/article/9-main-statuses-sub-statuses-of-shipments-in-trackingmore-tlkfjm/

---

## 8. Status Enum (`delivery_status`)

Nine main statuses, stable across v3 and v4:

| Code           | Display name     | Meaning                                                                             |
| -------------- | ---------------- | ----------------------------------------------------------------------------------- |
| `pending`      | Pending          | Newly created. Courier has not returned tracking info yet.                          |
| `notfound`     | Not Found        | Tracking info not available from the courier yet (may resolve later).               |
| `inforeceived` | Info Received    | Courier received the package info and is about to pick up.                          |
| `transit`      | In Transit       | Package is moving normally.                                                         |
| `pickup`       | Out for Delivery | At the local point or on the truck. (Code is `pickup`, **not** `out_for_delivery`.) |
| `undelivered`  | Failed Attempt   | Delivery attempted and failed (recipient absent, bad address, etc.).                |
| `delivered`    | Delivered        | Final successful delivery.                                                          |
| `exception`    | Exception        | Returned, damaged, lost, customs hold, refusal, etc.                                |
| `expired`      | Expired          | No tracking update for 30+ days. Retrack-eligible.                                  |

**The "Info Received" code is `inforeceived` — single word, no underscore.** The
brief asks about a possible "infomation*received" typo: that misspelling does
appear in their \_help text* occasionally, but the actual API enum is
`inforeceived`.

Same nine values appear on each checkpoint as `checkpoint_delivery_status` (v4)
/ `checkpoint_status` (v2/v3).

Source:
https://support.trackingmore.com/en/article/9-main-statuses-sub-statuses-of-shipments-in-trackingmore-tlkfjm/

---

## 9. Substatus

v4 exposes `substatus` with the pattern `<status>NNN` (3-digit numeric suffix).
TrackingMore documents 23 substatuses:

| Main             | Substatus codes                                                                                                                                                                                                                                 |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Info Received    | `inforeceived001` (awaiting pickup)                                                                                                                                                                                                             |
| In Transit       | `transit001` en route · `transit002` arrived at hub · `transit003` arrived at delivery facility · `transit004` arrived at destination country · `transit005` customs cleared · `transit006` dispatched · `transit007` departed airport          |
| Out for Delivery | `pickup001` out for delivery · `pickup002` ready for collection · `pickup003` contact recipient                                                                                                                                                 |
| Delivered        | `delivered001` delivered · `delivered002` picked up by addressee · `delivered003` signed for · `delivered004` left with neighbor / safe place                                                                                                   |
| Failed Attempt   | `undelivered001` address issue · `undelivered002` recipient absent · `undelivered003` unable to locate · `undelivered004` other                                                                                                                 |
| Exception        | `exception004` unclaimed · `exception005` other · `exception006` customs detention · `exception007` lost/damaged · `exception008` cancelled · `exception009` refused · `exception010` returning to sender · `exception011` (additional variant) |
| Not Found        | `notfound002` no tracking data                                                                                                                                                                                                                  |
| Expired          | `expired001` no update 30+ days                                                                                                                                                                                                                 |
| Pending          | (no substatus)                                                                                                                                                                                                                                  |

Source:
https://support.trackingmore.com/en/article/9-main-statuses-sub-statuses-of-shipments-in-trackingmore-tlkfjm/

---

## 10. Tracking Events (`trackinfo[]`)

A tracking has two checkpoint arrays — `origin_info.trackinfo[]` (origin-country
carrier, e.g., China EMS) and `destination_info.trackinfo[]` (last-mile carrier,
e.g., USPS).

### Checkpoint object — casing is mixed in the same object

| Field                        | Casing     | Example                          | Notes                                                                   |
| ---------------------------- | ---------- | -------------------------------- | ----------------------------------------------------------------------- |
| `Date`                       | **Pascal** | `"2018-04-14 17:07:00"`          | Local time per carrier. No timezone.                                    |
| `StatusDescription`          | **Pascal** | `"Arrived at processing center"` | Often in courier's local language.                                      |
| `Details`                    | **Pascal** | `"Paramount, CA, US"`            | Free-form location/extra detail.                                        |
| `checkpoint_delivery_status` | snake      | `"transit"`                      | v4 field — one of the 9 enums.                                          |
| `checkpoint_status`          | snake      | `"transit"`                      | v2/v3 name for the same field (sometimes still present in v4 as alias). |
| `location`                   | snake      | `"Shenzhen"`                     | Sometimes present alongside `Details`.                                  |
| `substatus`                  | snake      | `"transit003"`                   | v4 only, optional per checkpoint.                                       |

### Container example

```json
"origin_info": {
  "weblink":      "http://www.ems.com.cn/",
  "phone":        "11183",
  "carrier_code": "china-ems",
  "trackinfo": [
    {
      "Date": "2018-04-14 17:07:00",
      "StatusDescription": "Arrived at processing center",
      "Details": "United States",
      "checkpoint_delivery_status": "transit"
    },
    {
      "Date": "2018-04-07 21:18:37",
      "StatusDescription": "Left Zhengzhou, sent to San Francisco",
      "Details": "Zhengzhou",
      "checkpoint_delivery_status": "transit"
    }
  ]
}
```

Top-level summary fields: `latest_event` (v4) / `lastEvent` (v2/v3) is a
comma-joined "Description,Location,Date" string; `latest_checkpoint_time` (v4) /
`lastUpdateTime` (v2/v3) is the most-recent checkpoint timestamp.

Sources: https://www.trackingmore.com/api-track-list-all-track.html ·
https://support.trackingmore.com/en/article/9-main-statuses-sub-statuses-of-shipments-in-trackingmore-tlkfjm/

---

## 11. Webhooks

### Registration

Configured in `my.trackingmore.com/webhook_setting.php`: enter the URL, pick
which statuses fire notifications, and choose API version (V2/V3/V4). **Webhook
version must match the API version** used to create trackings — explicit gotcha
in the docs.

### Outgoing IP

`47.90.206.205` (use for firewall allow-lists). Documented in TrackingMore's
"Webhook IP Addresses" support article.

### Payload (POST, `Content-Type: application/json`)

```json
{
  "meta": { "code": 200, "type": "Success", "message": "Success" },
  "data": {
    "id": "...",
    "tracking_number": "...",
    "carrier_code": "...",
    "status": "delivered",
    "created_at": "...",
    "updated_at": "...",
    "title": "...",
    "order_id": null,
    "customer_name": null,
    "customer_email": "...",
    "original_country": "...",
    "destination_country": "...",
    "itemTimeLength": 12,
    "origin_info": { "...": "..." },
    "destination_info": { "...": "..." },
    "lastEvent": "..."
  },
  "verifyInfo": {
    "timeStr": 1488249109,
    "signature": "4b279021f5c041f6e3344e7a0636cc26201ab24b91adcea6d38331cb89221d45"
  }
}
```

`data` mirrors the tracking object returned by `GET /v4/trackings/get`. In v4
the status field is `delivery_status` and summary fields are renamed
(`latest_event`, `latest_checkpoint_time`).

### Signature

- **No header signature** — the signature lives **in the body** under
  `verifyInfo`.
- Algorithm: **HMAC-SHA256**.
- Secret: the **registered account email** (no separate webhook secret like
  Stripe).
- Message: the `verifyInfo.timeStr` value as a string.
- Verify by recomputing `HMAC-SHA256(message=timeStr_as_string, key=email)` and
  comparing hex-digest equality to `verifyInfo.signature`:

```php
function verify($timeStr, $useremail, $signature) {
    return hash_equals(hash_hmac("sha256", $timeStr, $useremail), $signature);
}
```

### Retry policy

- Up to **14 retries** with exponential backoff, formula
  `delay = 2^retry_number * 30s` (retry 1 ≈ 60s, retry 14 ≈ 5.7 days).
- Triggered by any non-`2xx` response. Return 200 fast and queue work.
- Webhooks fire when TrackingMore detects a change on its own polling cycle —
  every **4–6 hours** per tracking. No real-time push.

Sources: https://www.trackingmore.com/webhook.html ·
https://support.trackingmore.com/en/category/api-webhook-oop8bq/ ·
https://support.trackingmore.com/en/article/something-you-need-to-know-about-trackingmore-api-webhook-1w51egp/

---

## 12. List Trackings

- **Endpoint:** `GET /v4/trackings/get`
- **Pagination:** offset/limit, **not** cursor. `page` (1-based, default 1),
  `limit` (default 100, **max 200** in v4).
- **Filters:** `delivery_status`, `courier_code`, `created_date_min`/`max`,
  `tracking_numbers`, `order_number`, `archived`.
- **Response includes a `total`** (returned as a _string_) alongside
  `page`/`limit`.

For large datasets, walk time windows with `created_date_min/max` — there is no
documented stable iteration order to rely on.

---

## 13. Rate Limits

| Endpoint                    | v4 limit                 | v3/v2   |
| --------------------------- | ------------------------ | ------- |
| `POST /v4/trackings/create` | **3 req/s** per account  | 1 req/s |
| All other endpoints         | **10 req/s** per account | 1 req/s |

- **429 behavior:** HTTP 429 + `meta.code: 429`, `meta.type: "TooManyRequests"`.
  **Docs say wait 120 seconds.**
- **No documented `Retry-After`, `X-RateLimit-*`, or `RateLimit-*` headers.**
  Treat 429 as a fixed 120-s cooldown unless your Enterprise contract exposes
  them.
- Higher limits negotiable on Enterprise.

Source:
https://support.trackingmore.com/en/article/trackingmore-api-request-rate-limit-c0ye70/

---

## 14. Pricing Model

Credit-based, **1 credit = 1 tracking number created**. Plans (monthly USD,
annual saves up to 20%):

| Plan       | Price        | Included shipments / month | Overage    |
| ---------- | ------------ | -------------------------- | ---------- |
| Free       | $0           | small (evaluation)         | n/a        |
| Basic      | from ~$11/mo | 200                        | $0.04 each |
| Pro        | from ~$74/mo | larger (e.g., 2,000)       | $0.04 each |
| Enterprise | custom       | custom                     | custom     |

Key billing rules:

- **Credits are consumed when a tracking number is created**
  (`POST /v4/trackings/create` / `/batch`). `GET`, webhooks, and updates are
  free.
- **Unused credits do not roll over.** Quota resets each cycle.
- **v4 auto-detect is free**; v2/v3 charged 0.2 credit per detect.
- API access is generally a **Pro-plan-and-up** feature — Basic is mainly
  dashboard/CSV import. Verify the current pricing page before assuming.

Source: https://www.trackingmore.com/pricing

---

## 15. Error Response Shape

Universal envelope:

```json
{
  "meta": {
    "code": 4001,
    "type": "Unauthorized",
    "message": "Invalid API key."
  },
  "data": []
}
```

- `meta.code` is TrackingMore-specific (frequently differs from the HTTP
  status).
- `meta.type` is a CamelCase classification.
- `meta.message` is human-readable.
- `data` is `[]` on most errors; on success it's an object (single
  create/update) or `{ items: [...], page, limit, total }` (list).

### v4 meta-code reference (from the official SDK READMEs)

| HTTP | meta.code   | meta.type         | Meaning                                     |
| ---- | ----------- | ----------------- | ------------------------------------------- |
| 200  | 200         | `Success`         | OK                                          |
| 400  | 400         | `BadRequest`      | Request shape error                         |
| 400  | 4101        | `BadRequest`      | Tracking already exists                     |
| 400  | 4102        | `BadRequest`      | Tracking does not exist (call create first) |
| 400  | 4103        | `BadRequest`      | Over 40-per-call batch limit                |
| 400  | 4110        | `BadRequest`      | `tracking_number` value invalid             |
| 400  | 4111        | `BadRequest`      | `tracking_number` required                  |
| 400  | 4112        | `BadRequest`      | Invalid tracking ID (32-char internal)      |
| 400  | 4113        | `BadRequest`      | Retrack disallowed — only `expired`         |
| 400  | 4120        | `BadRequest`      | `courier_code` invalid                      |
| 400  | 4121        | `BadRequest`      | Auto-detect found no match                  |
| 400  | 4122        | `BadRequest`      | Missing carrier-specific required field     |
| 400  | 4130        | `BadRequest`      | Malformed field name                        |
| 400  | 4160        | `BadRequest`      | `awb_number` required / invalid             |
| 400  | 4161        | `BadRequest`      | Air waybill airline not supported           |
| 400  | 4190        | `BadRequest`      | Account quota reached — upgrade             |
| 401  | 401         | `Unauthorized`    | Auth failed                                 |
| 401  | 4001        | `Unauthorized`    | Invalid API key                             |
| 401  | 4002        | `Unauthorized`    | API key deleted                             |
| 403  | 403         | `Forbidden`       | Access denied                               |
| 404  | 404         | `NotFound`        | Bad path                                    |
| 429  | 429         | `TooManyRequests` | Rate-limited — wait 120s                    |
| 500  | 511/512/513 | `ServerError`     | Email `service@trackingmore.org`            |

Source: SDK READMEs (PHP, Ruby, Node.js, .NET) on github.com/TrackingMore-API.

---

## 16. Quirks / Gotchas

1. **Mixed field casing in the same object.** Checkpoints have PascalCase
   `Date`, `StatusDescription`, `Details` next to snake_case
   `checkpoint_delivery_status`, `tracking_number`, `courier_code`.
   Deserializers must map both styles explicitly.

2. **Field renames across versions.**
   - v2/v3: `status`, `lastEvent`, `lastUpdateTime`, `carrier_code`,
     `checkpoint_status`.
   - v4: `delivery_status`, `latest_event`, `latest_checkpoint_time`,
     `courier_code` (sometimes alongside `carrier_code`),
     `checkpoint_delivery_status`.
   - **Webhook version must equal API version** — the dashboard lets them drift,
     which then produces shape mismatches the API silently ships.

3. **`inforeceived` is one word.** Not `info_received`, not
   `information_received`. The "infomation" misspelling appears in their help
   text but is not the enum.

4. **`pickup`, not `out_for_delivery`.** Out-for-delivery status is the string
   `pickup`.

5. **Eventual consistency on create.** v4's create returns immediately, but the
   first response often has `delivery_status: "pending"` or `"notfound"` until
   TrackingMore's first carrier poll completes. Use webhooks; poll no faster
   than every 4–6 hours.

6. **Auth header varies by version.** v2 = `Trackingmore-Api-Key`; v3/v4 =
   `Tracking-Api-Key`.

7. **v4 detect is free, v2/v3 detect costs credits.** Always go through v4 if
   you expose auto-detect.

8. **Batch body is a top-level array.** `POST /v4/trackings/batch` body is
   `[{...},{...}]`, not `{"trackings":[...]}`. Code generators that wrap arrays
   in objects will break.

9. **`id` vs `tracking_number`.** Internal `id` is 32-char hex; required for
   update/delete/retrack. `tracking_number` is the carrier-issued value;
   required for create. Don't conflate them.

10. **`429` returns no `Retry-After`.** Bake in the 120-second wait yourself.

11. **Delete does not refund credits**, and credits do not roll over.

12. **Webhook signature is in the body, not a header.** Most webhook libraries
    assume a header — TrackingMore puts it in `verifyInfo`. Also, the secret is
    your **account email**, not a generated webhook secret.

13. **Retrack only works on `expired` trackings.** Otherwise meta `4113`. Don't
    expose retrack as a generic refresh.

14. **Mixed time formats.** Top-level timestamps are ISO-8601 with offset
    (`2015-10-30T11:35:16+08:00`); checkpoint `Date` is naive local
    (`2015-11-02 17:11`) with no timezone. Parsers must accept both.

15. **`total` in the list response is a _string_**, not an integer
    (`"total": "56"`). Coerce.

16. **The new `trackingmore.com/docs/trackingmore/*` portal is JS-rendered**;
    for v4 the most reliable references are the official SDK READMEs on GitHub
    plus the Postman collection. The legacy `trackingmore.com/api-*.html` pages
    still serve HTML but document v2.

---

### Primary sources

- PHP SDK (v4): https://github.com/TrackingMore-API/trackingmore-sdk-php
- Node.js SDK (v4): https://github.com/TrackingMore-API/trackingmore-sdk-nodejs
- Ruby SDK (v4): https://github.com/TrackingMore-API/trackingmore-sdk-ruby
- .NET SDK (v4): https://github.com/TrackingMore-API/trackingmore-sdk-net
- Go SDK (v3): https://github.com/trackingmore100/tracking-sdk-go
- Statuses & substatuses:
  https://support.trackingmore.com/en/article/9-main-statuses-sub-statuses-of-shipments-in-trackingmore-tlkfjm/
- Version differences:
  https://support.trackingmore.com/en/article/differences-among-3-versions-of-trackingmore-api-1ip0fmh/
- Rate limit:
  https://support.trackingmore.com/en/article/trackingmore-api-request-rate-limit-c0ye70/
- Webhook: https://www.trackingmore.com/webhook.html ·
  https://support.trackingmore.com/en/category/api-webhook-oop8bq/
- Status code reference: https://www.trackingmore.com/api-status_code.html
- Pricing: https://www.trackingmore.com/pricing
- Postman:
  https://www.postman.com/trackingmore/trackingmore-api/documentation/e1s0538/trackingmore-api
- Legacy v2 endpoint pages:
  https://www.trackingmore.com/api-track-list-all-track.html ·
  https://www.trackingmore.com/api-carriers-list-all-carriers.html ·
  https://www.trackingmore.com/api-carriers-detect-carrier.html
