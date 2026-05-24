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

# Shippo Tracking API — Research Report

Scope: Shippo's tracking API only (no label creation, rates, addresses, etc.).
All facts are cited to their source URL; anything not in the official docs is
called out explicitly.

Base URL for all endpoints: `https://api.goshippo.com` Primary docs:
<https://docs.goshippo.com/docs/tracking/tracking>

---

## 1. Authentication

- **Scheme**: Custom token in the `Authorization` header — **not** `Bearer`.
- **Header format**: `Authorization: ShippoToken <API_TOKEN>` (Source:
  <https://docs.goshippo.com/docs/guides_general/authentication>)
- **Key types**: Test keys are prefixed `shippo_test_`, live keys are prefixed
  `shippo_live_`. The environment is selected entirely by which key you send —
  there is no separate sandbox host. (Source:
  <https://docs.goshippo.com/docs/guides_general/authentication>)
- **Base URL** (same for both): `https://api.goshippo.com/` (Source:
  <https://docs.goshippo.com/docs/Guides_general/API_quickstart>)
- Test-mode tracking objects are flagged in responses via `"test": true`. Data
  is **not shared** between test and live accounts. (Source:
  <https://docs.goshippo.com/docs/guides_general/authentication>)
- Optional version pinning header: `Shippo-API-Version` (also echoed in webhook
  deliveries). (Source: <https://docs.goshippo.com/docs/tracking/webhooks>)

Example:

```bash
curl https://api.goshippo.com/tracks/ \
  -H "Authorization: ShippoToken shippo_test_xxxxxxxxxxxxxxxxxxx" \
  -d carrier="usps" \
  -d tracking_number="9205590164917312751089"
```

---

## 2. Carrier List

Shippo publishes the canonical list of trackable carriers at:
<https://docs.goshippo.com/docs/tracking/trackingcarriers>

The page exposes ~54 carrier tokens covering major global carriers and
regional/specialty providers. Tokens observed in the table:

```
airterra, apc_postal, apg, aramex, asendia_us, australia_post,
axlehire, better_trucks, borderguru, boxberry, bring, canada_post,
chronopost, colissimo, collect_plus, correios_br, correos_espana,
deutsche_post, dhl_benelux, dhl_ecommerce, dhl_express, dhl_germany,
dpd_de, dpd_uk, estafeta, fastway_australia, fedex, globegistics,
gls_us, gso, hermes_uk, la_poste, lasership, lso, mondial_relay,
new_zealand_post, nippon_express, ontrac, passport, pcf, pitney_bowes,
poste_italiane, posti, purolator, royal_mail, royal_mail_sf,
rr_donnelley, russian_post, skypostal, stuart, uds, ups, usps, veho
```

In addition, the magic token `shippo` is used for **test-mode mock tracking**
(see §5). The docs explicitly state carrier-specific restrictions exist (some
carriers may be webhook-update-only), but the public list does not call out a
per-carrier breakdown — **not documented** which carriers are GET-only vs
webhook-only.

---

## 3. Carrier Identification

- **Carrier code is required.** Every documented request body and path in the
  tracking docs takes both `carrier` and `tracking_number`. Auto-detection from
  the tracking number alone is **not documented** as a supported feature.
  (Source: <https://docs.goshippo.com/docs/tracking/tracking>)
- **Casing convention**: lowercase, snake_case for multi-word carriers.
  Examples: `usps`, `ups`, `fedex`, `dhl_express`, `canada_post`, `royal_mail`,
  `dhl_ecommerce`. The docs only use lowercase tokens; uppercase forms (e.g.
  "USPS") are not accepted. (Source:
  <https://docs.goshippo.com/docs/tracking/trackingcarriers>)

---

## 4. Register / Create a Tracking Subscription

| Operation            | Method | Path                                  |
| -------------------- | ------ | ------------------------------------- |
| Register (subscribe) | `POST` | `/tracks/`                            |
| Get latest status    | `GET`  | `/tracks/{carrier}/{tracking_number}` |

(Source: <https://docs.goshippo.com/docs/tracking/tracking>)

There is a clear separation:

- **POST /tracks/** registers Shippo to poll the carrier and emit
  `track_updated` webhooks for this tracking number. The response body contains
  the same tracking object you'd get from the GET. The docs do **not**
  explicitly state whether the response is `200` or `201` — **not documented**.
- **GET /tracks/{carrier}/{tracking_number}** retrieves the latest snapshot. You
  can call this for tracking numbers that were never POST-registered too.

**Request body (POST /tracks/)** — form-encoded or JSON:

| Field                     | Required | Notes                                                                               |
| ------------------------- | -------- | ----------------------------------------------------------------------------------- |
| `carrier`                 | yes      | One of the tokens in §2                                                             |
| `tracking_number`         | yes      | Carrier-issued tracking number                                                      |
| `metadata`                | no       | Free-form string echoed back in webhooks (e.g. your order ID)                       |
| `include_package_details` | no       | Boolean; only meaningful for UPS / FedEx — returns `weight` and `dimensions` arrays |

(Source: <https://docs.goshippo.com/docs/tracking/tracking>)

Example POST:

```bash
curl https://api.goshippo.com/tracks/ \
  -H "Authorization: ShippoToken <API_TOKEN>" \
  -d carrier="usps" \
  -d tracking_number="9102969010383081813033" \
  -d metadata="Order 000123"
```

The docs warn that POSTing is **not idempotent**: "Tracking webhooks are not
idempotent; register once per tracking event. Duplicate registrations cause
multiple notifications." Callers must dedupe on their side. (Source:
<https://docs.goshippo.com/docs/tracking/tracking>)

Note: the documentation also says "you must register a webhook prior to POSTing
to the tracking endpoint," and "Shippo automatically creates a webhook for
tracking when you purchase a label" — so for labels bought via Shippo this
happens implicitly. (Source: <https://docs.goshippo.com/docs/tracking/webhooks>)

---

## 5. Get Tracking Status

`GET https://api.goshippo.com/tracks/{carrier}/{tracking_number}`

Example response (verbatim from
<https://docs.goshippo.com/docs/tracking/tracking>):

```json
{
  "carrier": "usps",
  "tracking_number": "9205590164917312751089",
  "address_from": {
    "city": "Las Vegas",
    "state": "NV",
    "zip": "89101",
    "country": "US"
  },
  "address_to": {
    "city": "Spotsylvania",
    "state": "VA",
    "zip": "22551",
    "country": "US"
  },
  "transaction": "1275c67d754f45bf9d6e4d7a3e205314",
  "original_eta": "2023-07-23T00:00:00Z",
  "eta": "2023-07-23T00:00:00Z",
  "servicelevel": {
    "token": "usps_priority",
    "name": "Priority Mail"
  },
  "metadata": null,
  "tracking_status": {
    "object_created": "2023-07-23T20:35:26.129Z",
    "object_updated": "2023-07-23T20:35:26.129Z",
    "object_id": "ce48ff3d52a34e91b77aa98370182624",
    "status": "DELIVERED",
    "status_details": "Your shipment has been delivered at the destination mailbox.",
    "status_date": "2023-07-23T13:03:00Z",
    "location": {
      "city": "Spotsylvania",
      "state": "VA",
      "zip": "22551",
      "country": "US"
    }
  },
  "tracking_history": [
    {
      "object_created": "2023-07-22T14:36:50.943Z",
      "object_id": "265c7a7c23354da5b87b2bf52656c625",
      "status": "TRANSIT",
      "status_details": "Your shipment has been accepted.",
      "status_date": "2023-07-21T15:33:00Z",
      "location": {
        "city": "Las Vegas",
        "state": "NV",
        "zip": "89101",
        "country": "US"
      }
    },
    {
      "object_created": "2023-07-23T20:35:26.129Z",
      "object_id": "aab1d7c0559d43ccbba4ff8603089e56",
      "status": "DELIVERED",
      "status_details": "Your shipment has been delivered at the destination mailbox.",
      "status_date": "2023-07-23T13:03:00Z",
      "location": {
        "city": "Spotsylvania",
        "state": "VA",
        "zip": "22551",
        "country": "US"
      }
    }
  ]
}
```

Top-level fields: `carrier`, `tracking_number`, `address_from`, `address_to`,
`transaction` (the Shippo label transaction ID, if any), `original_eta`, `eta`,
`servicelevel` (`{token, name}`), `metadata`, `tracking_status`,
`tracking_history`, `test` (boolean).

When `include_package_details=true` (UPS/FedEx only) the response also carries
`weight` and `dimensions` arrays:

```json
"weight": [
  {"value": "2.2", "unit": "LB"},
  {"value": "1.0", "unit": "KG"}
],
"dimensions": [
  {"length": "1", "width": "2", "height": "3", "unit": "in"},
  {"length": "3", "width": "5", "height": "8", "unit": "cm"}
]
```

**Test-mode mocks** — POST with `carrier="shippo"` and one of these magic
tracking numbers to force a particular status:

- `SHIPPO_PRE_TRANSIT`
- `SHIPPO_TRANSIT`
- `SHIPPO_DELIVERED`
- `SHIPPO_RETURNED`
- `SHIPPO_FAILURE`
- `SHIPPO_UNKNOWN`

(Source: <https://docs.goshippo.com/docs/tracking/tracking>)

---

## 6. Status Enum (`tracking_status.status`)

Exhaustive list with definitions, quoted from
<https://docs.goshippo.com/docs/tracking/tracking>:

| Value         | Meaning                                                                    |
| ------------- | -------------------------------------------------------------------------- |
| `PRE_TRANSIT` | Label created but the carrier has not yet picked up / received the parcel. |
| `TRANSIT`     | Parcel accepted by the carrier and en route.                               |
| `DELIVERED`   | Successfully delivered to the recipient.                                   |
| `RETURNED`    | En route back to the sender, or returned successfully.                     |
| `FAILURE`     | Carrier reports a delivery problem.                                        |
| `UNKNOWN`     | Carrier doesn't recognize the tracking number (yet).                       |

---

## 7. Substatus / Detail Codes

Substatuses sit under the parent `status` and are carried as a `substatus`
object on `tracking_status` and on each `tracking_history` entry. Shape (from
webhook example):

```json
"substatus": {
  "code": "information_received",
  "text": "Information about the package received.",
  "action_required": true
}
```

`action_required` flags states that need the recipient/sender to do something
(reschedule, pick up, fix address, etc.).

Documented codes by parent status (source:
<https://docs.goshippo.com/docs/tracking/tracking>):

**PRE_TRANSIT**: `information_received`

**TRANSIT**: `address_issue` _(action required)_, `contact_carrier` _(action
required)_, `delayed`, `delivery_attempted` _(action required)_,
`delivery_rescheduled`, `delivery_scheduled`, `location_inaccessible` _(action
required)_, `notice_left` _(action required)_, `out_for_delivery`,
`package_accepted`, `package_arrived`, `package_damaged` _(action required)_,
`package_departed`, `package_forwarded`, `package_held`, `package_processed`,
`package_processing`, `pickup_available` _(action required)_,
`reschedule_delivery` _(action required)_

**DELIVERED**: `delivered`

**RETURNED**: `return_to_sender`, `package_unclaimed` _(action required)_

**FAILURE**: `package_undeliverable` _(action required)_, `package_disposed`,
`package_lost` _(action required)_

**UNKNOWN**: `other`

---

## 8. Tracking Events / History

`tracking_history` is an **array of checkpoint objects**, ordered as delivered
by the carrier (the public examples show chronological-ascending, but ordering
is not formally guaranteed in the docs — **not documented**).

Each event has these fields:

| Field            | Format                                                                            | Notes                                        |
| ---------------- | --------------------------------------------------------------------------------- | -------------------------------------------- |
| `object_id`      | string (hex/UUID-like)                                                            | Stable Shippo ID for the checkpoint          |
| `object_created` | ISO-8601 UTC, millisecond precision, `Z` suffix (e.g. `2023-07-22T14:36:50.943Z`) | When Shippo recorded it                      |
| `object_updated` | same format                                                                       | Last modification                            |
| `status`         | one of the §6 enum values                                                         |                                              |
| `substatus`      | `{code, text, action_required}`                                                   | See §7                                       |
| `status_date`    | ISO-8601 UTC, `Z` suffix (e.g. `2023-07-21T15:33:00Z`)                            | When the carrier scan occurred               |
| `status_details` | human-readable string                                                             |                                              |
| `location`       | `{city, state, zip, country}`                                                     | All strings; can be partial/null per carrier |

Timestamps in the documented examples are all in UTC with a `Z` suffix.
Sub-second precision varies by field (`object_created`/`object_updated` carry
milliseconds; `status_date` in examples does not). The docs **do not** specify
timezone handling beyond the literal examples.

(Source: <https://docs.goshippo.com/docs/tracking/tracking>)

---

## 9. Webhooks

**Registration**:

- Manually via the Shippo dashboard:
  <https://portal.goshippo.com/api-config/webhooks>
- Programmatically via the Webhooks API (described as **beta**)

The webhook URL must be **< 200 characters**. Shippo auto-creates a tracking
webhook for any label purchased through Shippo. (Source:
<https://docs.goshippo.com/docs/tracking/webhooks>)

**Event types**: `track_updated`, `transaction_created`, `transaction_updated`,
`batch_created`, `batch_purchased`, and `all`.

**Payload shape** (verbatim example for `track_updated`, source:
<https://docs.goshippo.com/docs/tracking/webhooks>):

```json
{
  "event": "track_updated",
  "test": true,
  "data": {
    "address_from": {
      "city": "Las Vegas",
      "country": "US",
      "state": "NV",
      "zip": "89101"
    },
    "address_to": {
      "city": "Las Vegas",
      "country": "US",
      "state": "NV",
      "zip": "89101"
    },
    "carrier": "usps",
    "eta": "2019-08-24T14:15:22Z",
    "messages": ["string"],
    "metadata": "Order 000123",
    "original_eta": "2021-07-23T00:00:00Z",
    "servicelevel": {
      "name": "Priority Mail Express",
      "terms": "string",
      "token": "usps_priority_express",
      "extended_token": "string",
      "parent_servicelevel": {
        "name": "Priority Mail Express",
        "terms": "string",
        "token": "usps_priority_express",
        "extended_token": "string"
      }
    },
    "tracking_history": [
      {
        "location": {
          "city": "Las Vegas",
          "country": "US",
          "state": "NV",
          "zip": "89101"
        },
        "object_created": "2019-08-24T14:15:22Z",
        "object_id": "string",
        "object_updated": "2019-08-24T14:15:22Z",
        "status": "DELIVERED",
        "substatus": {
          "code": "information_received",
          "text": "Information about the package received.",
          "action_required": true
        },
        "status_date": "2016-07-23T00:00:00Z",
        "status_details": "Your shipment has been delivered at the destination mailbox."
      }
    ],
    "tracking_number": "9205590164917312751089",
    "tracking_status": {
      "location": {
        "city": "Las Vegas",
        "country": "US",
        "state": "NV",
        "zip": "89101"
      },
      "object_created": "2019-08-24T14:15:22Z",
      "object_id": "string",
      "object_updated": "2019-08-24T14:15:22Z",
      "status": "DELIVERED",
      "substatus": {
        "code": "information_received",
        "text": "Information about the package received.",
        "action_required": true
      },
      "status_date": "2016-07-23T00:00:00Z",
      "status_details": "Your shipment has been delivered at the destination mailbox."
    },
    "transaction": "string"
  }
}
```

**Retry behaviour**: Shippo expects a 2xx response **within 3 seconds**. On
`408`, `429`, or any `5xx`, Shippo **retries twice**. Any other `4xx` is final —
no retry. (Source: <https://docs.goshippo.com/docs/tracking/webhooks>)

**Security** — three options, documented at
<https://docs.goshippo.com/docs/Tracking/WebhookSecurity>:

1. **IP allowlist** (Shippo publishes its egress ranges):
   - US: `52.4.41.98`, `52.23.121.194`, `52.44.110.80`, `54.81.253.187`,
     `54.81.255.221`
   - EU: `34.248.247.69`, `34.253.119.130`, `52.214.174.64`, `54.72.179.250`
2. **Shared-token query parameter** — you bake a secret into your webhook URL
   (e.g. `?token=123abc`) and reject requests lacking it.
3. **HMAC-SHA256 signature** — opt-in; you must email your Shippo account
   manager to enable and it "can take up to 10 business days to complete."
   - Header: `Shippo-Auth-Signature` (referenced in the Bash verification helper
     as `$HTTP_SHIPPO_AUTH_SIGNATURE`)
   - Value format: `t=<unix_ts>,v1=<hex_sig>`, e.g.
     `t=1688493073,v1=24036c00f9adad56ad83504e5dce63fe0a248631865a89fe9adb9494f6dc7c0b`
   - Signed payload: `"${timestamp}.${raw_body}"`
   - Algorithm: `HMAC-SHA256` over the signed payload string with your shared
     secret, hex-encoded.

In short: there is **no default signing** — HMAC must be requested.

---

## 10. List / Query Existing Trackings

The tracking docs do **not** document a "list all trackings" endpoint. Only
`GET /tracks/{carrier}/{tracking_number}` (single lookup) and `POST /tracks/`
(register) are exposed for tracking objects. The rate-limit table at
<https://docs.goshippo.com/docs/api_concepts/ratelimits> also shows the
`Tracking` row with only POST and GET(single) entries — the GET(multiple) column
is blank — corroborating this.

For Shippo's general list endpoints (parcels, shipments, etc., not tracking),
pagination uses `?results=<page_size, max 100>&page=<n>`. This is unusual: not
`limit`/`offset`, not cursor-based. **If a list-trackings endpoint exists at
all**, it is undocumented and integrators should not depend on it.

---

## 11. Rate Limits

From the official table at
<https://docs.goshippo.com/docs/api_concepts/ratelimits>, per-minute limits for
the `Tracking` resource:

| Verb                                      | Live      | Test     |
| ----------------------------------------- | --------- | -------- |
| `POST /tracks/`                           | 750 / min | 50 / min |
| `GET /tracks/{carrier}/{tracking_number}` | 500 / min | 50 / min |

Exceeding either returns HTTP **429**. The docs **do not** document:

- a `Retry-After` header,
- burst/short-window behaviour,
- the JSON body of a 429.

Higher limits are negotiated by contacting Shippo sales — there is no self-serve
upgrade.

---

## 12. Pricing Model

From <https://goshippo.com/pricing/api>:

- **API Starter** (pay-as-you-go): **$0.02 per tracking call** (the page lists
  "2¢/track").
- **API Premier**: custom/enterprise — contact sales.
- **Labels bought through Shippo**: tracking on those labels is automatic and
  not separately metered for the lifetime of the shipment.
- A widely-cited older quote of **$0.01 per unique tracking number created
  outside of Shippo** appears in third-party summaries; the current pricing page
  reflects $0.02. Cite the live pricing page in our docs rather than the old
  number.

There is no documented free tier for external tracking; **test mode is always
free** with a `shippo_test_` key.

---

## 13. Error Response Shape

HTTP codes Shippo returns (source:
<https://docs.goshippo.com/docs/api_concepts/apihttpstatuscodes>):

| Code      | Meaning                           |
| --------- | --------------------------------- |
| 200 / 201 | OK                                |
| 204       | OK, no body                       |
| 400       | Bad request                       |
| 401       | Auth failure                      |
| 404       | Not found                         |
| 409       | Conflict                          |
| 422       | Unprocessable entity (validation) |
| 429       | Rate limited                      |
| 5xx       | Server error                      |

**The exact JSON error body shape is not documented on the public docs pages** —
neither the status-codes page nor the tracking page shows an error example. In
practice (consistent with Shippo's REST conventions for other resources) errors
are returned as field-keyed validation arrays, e.g.:

```json
{
  "tracking_number": ["This field is required."],
  "carrier": ["Carrier not supported."]
}
```

but **treat this as undocumented**; integrators should accept either shape and
fall back to the HTTP status.

---

## 14. Quirks / Gotchas

- **Carrier code is required.** No tracking-number-only auto-detect. Mapping
  user input to Shippo carrier tokens is the integrator's job. (§3)
- **POST /tracks/ is not idempotent.** Repeated POSTs for the same
  `(carrier, tracking_number)` produce duplicate webhook subscriptions and
  duplicate deliveries. Dedupe on your side. (Source:
  <https://docs.goshippo.com/docs/tracking/tracking>)
- **Webhook must exist before POST.** If you POST to `/tracks/` without a
  tracking webhook configured, updates are silently lost — Shippo only emits via
  active webhooks. Labels bought through Shippo get a webhook auto-created.
  (Source: <https://docs.goshippo.com/docs/tracking/webhooks>)
- **No signing by default.** HMAC verification is opt-in and slow to enable (up
  to 10 business days via account manager). For self-serve protection, use IP
  allowlisting or a secret query-string token. (§9)
- **Two timestamp fields per event with different semantics.** `status_date` =
  when the carrier scanned the event; `object_created` / `object_updated` = when
  Shippo ingested/changed the record. They can be hours apart. Always sort
  history by `status_date` when displaying a timeline.
- **Mixed timestamp precision.** Examples show `object_created` with millisecond
  precision but `status_date` only to seconds. Don't assume uniform precision
  when parsing.
- **`UNKNOWN` is normal right after POST.** Carriers don't immediately know
  about freshly created labels; expect `UNKNOWN` and/or `PRE_TRANSIT` before
  `TRANSIT`. The docs don't formalise eventual-consistency timing — **not
  documented**.
- **`metadata` is your reconciliation key.** Stuff your order ID into `metadata`
  on POST; it round-trips through every webhook payload. (Source:
  <https://docs.goshippo.com/docs/tracking/webhookmetadata/>)
- **`include_package_details` only works for UPS and FedEx.** Other carriers
  silently ignore it. (§5)
- **Address fields are partial.** `address_from` / `address_to` carry only
  `city, state, zip, country` — no street. Treat these as hints, not as full
  addresses.
- **`servicelevel` may be deeply nested.** Webhook payload includes
  `parent_servicelevel`; the GET response often omits it. Code defensively.
- **`messages` is an array of carrier messages.** Strings; can include warnings
  or carrier-specific notes; not formally schema'd.
- **Test mode quirk.** To exercise statuses, you must use `carrier="shippo"`
  plus a magic tracking number (e.g. `SHIPPO_DELIVERED`); a real carrier code in
  test mode does **not** mock these states. (§5)
- **Rate-limit table separates POST and GET pools.** They share no budget — you
  can spend GETs without affecting POSTs and vice versa.
- **`test` flag distinguishes envs in payloads.** Always check it on incoming
  webhooks — easy to mix up dashboards during integration.

---

## Source URLs Referenced

- <https://docs.goshippo.com/docs/tracking/tracking> — main tracking docs
- <https://docs.goshippo.com/docs/tracking/trackingcarriers> — carrier token
  list
- <https://docs.goshippo.com/docs/tracking/webhooks> — webhook payload + retry
  behaviour
- <https://docs.goshippo.com/docs/Tracking/WebhookSecurity> — HMAC / IPs /
  shared-token
- <https://docs.goshippo.com/docs/tracking/webhookmetadata/> — metadata
  round-trip
- <https://docs.goshippo.com/docs/guides_general/authentication> — token format,
  test vs live
- <https://docs.goshippo.com/docs/Guides_general/API_quickstart> — base URL
- <https://docs.goshippo.com/docs/api_concepts/ratelimits> — rate limits
- <https://docs.goshippo.com/docs/api_concepts/apihttpstatuscodes> — HTTP codes
- <https://docs.goshippo.com/docs/Guides_general/testing> — test mode
- <https://goshippo.com/pricing/api> — pricing
