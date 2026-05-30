// Copyright © 2026 Michael Shields
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package shippo implements the trackage Tracker interface against
// the Shippo tracking API.
//
// Shippo requires an explicit carrier code on every request — there is
// no auto-detect endpoint — so callers must either supply a canonical
// trackage carrier id (translated below) or pass a Shippo-native
// carrier token directly. If the supplied carrier is empty, trackage
// runs its local format detector first; if that still produces nothing,
// Track returns trackage.ErrCarrierRequired.
//
// API reference: https://docs.goshippo.com/docs/tracking/tracking
package shippo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"msrl.dev/trackage"
)

// defaultHTTPTimeout bounds a single Track call when the caller does
// not supply their own HTTPClient. Shippo's tracking endpoint typically
// answers in well under a second; thirty seconds leaves room for a
// slow upstream without wedging the CLI.
const defaultHTTPTimeout = 30 * time.Second

const (
	backendName    = "shippo"
	defaultBaseURL = "https://api.goshippo.com"
)

// Config configures a Shippo tracker.
type Config struct {
	// APIKey is a Shippo API token (test keys begin "shippo_test_", live
	// keys "shippo_live_"). Required.
	APIKey string

	// BaseURL overrides https://api.goshippo.com — primarily for tests.
	BaseURL string

	// HTTPClient overrides http.DefaultClient.
	HTTPClient *http.Client
}

// Tracker is the Shippo implementation of trackage.Tracker.
type Tracker struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// New constructs a Shippo Tracker.
func New(c Config) *Tracker {
	t := &Tracker{
		apiKey:  c.APIKey,
		baseURL: strings.TrimRight(c.BaseURL, "/"),
		client:  c.HTTPClient,
	}
	if t.baseURL == "" {
		t.baseURL = defaultBaseURL
	}
	if t.client == nil {
		t.client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return t
}

// Name returns "shippo".
func (*Tracker) Name() string { return backendName }

// Track fetches the latest snapshot via GET /tracks/{carrier}/{number}.
//
// If carrier is empty, trackage runs its local detector; Shippo itself
// does not auto-detect, so an undetectable number returns
// trackage.ErrCarrierRequired.
//
// The carrier argument may be either a canonical trackage id (e.g.
// "usps") or a Shippo-native token (e.g. "dhl_benelux"); the former is
// translated, the latter is passed through verbatim.
func (t *Tracker) Track(ctx context.Context, carrier, number string) (*trackage.Tracking, error) {
	code, err := t.resolveCarrier(carrier, number)
	if err != nil {
		return nil, err
	}

	// PathEscape both segments — Shippo's API rejects malformed paths,
	// and an unescaped '?' or '/' in a user-supplied number would split
	// the URL or inject a query string.
	endpoint := fmt.Sprintf("%s/tracks/%s/%s", t.baseURL, url.PathEscape(code), url.PathEscape(number))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "ShippoToken "+t.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // close errors on tracker reads are inactionable

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(resp.StatusCode, body)
	}

	var raw response
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("shippo: parse response: %w", err)
	}

	return normalize(carrier, code, &raw, body), nil
}

// resolveCarrier returns the Shippo-native carrier token to use.
// Order of resolution: canonical lookup, then verbatim pass-through,
// then local detection, then ErrCarrierRequired.
func (*Tracker) resolveCarrier(carrier, number string) (string, error) {
	if carrier != "" {
		if c, ok := trackage.LookupCarrier(carrier); ok {
			if c.Shippo == "" {
				return "", fmt.Errorf("shippo: %w (%s)", trackage.ErrUnsupportedCarrier, carrier)
			}
			return c.Shippo, nil
		}
		// Pass through unknown carrier strings verbatim — Shippo will
		// 400 if it's wrong.
		return carrier, nil
	}
	if id := trackage.DetectCarrier(number); id != "" {
		if c, ok := trackage.LookupCarrier(id); ok && c.Shippo != "" {
			return c.Shippo, nil
		}
	}
	return "", trackage.ErrCarrierRequired
}

// response is the subset of Shippo's tracking object that we surface.
type response struct {
	Carrier         string         `json:"carrier"`
	TrackingNumber  string         `json:"tracking_number"`
	ETA             string         `json:"eta"`
	OriginalETA     string         `json:"original_eta"`
	TrackingStatus  *statusObject  `json:"tracking_status"`
	TrackingHistory []statusObject `json:"tracking_history"`
}

type statusObject struct {
	Status        string     `json:"status"`
	StatusDate    string     `json:"status_date"`
	StatusDetails string     `json:"status_details"`
	ObjectCreated string     `json:"object_created"`
	ObjectUpdated string     `json:"object_updated"`
	Substatus     *substatus `json:"substatus"`
	Location      *location  `json:"location"`
}

type substatus struct {
	Code           string `json:"code"`
	Text           string `json:"text"`
	ActionRequired bool   `json:"action_required"`
}

type location struct {
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

func (l *location) String() string {
	if l == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if l.City != "" {
		parts = append(parts, l.City)
	}
	if l.State != "" {
		parts = append(parts, l.State)
	}
	if l.Zip != "" {
		parts = append(parts, l.Zip)
	}
	if l.Country != "" {
		parts = append(parts, l.Country)
	}
	return strings.Join(parts, ", ")
}

// normalize maps a Shippo response into trackage's canonical shape.
//
// userCarrier is what the caller passed in (canonical id or backend
// token); shippoCode is what we resolved it to. The returned Tracking
// always carries the canonical id when one is known, so downstream
// code can reason in trackage terms regardless of input form.
func normalize(userCarrier, shippoCode string, r *response, raw []byte) *trackage.Tracking {
	canon := userCarrier
	if c, ok := trackage.LookupCarrier(userCarrier); ok {
		canon = c.ID
	} else {
		// userCarrier was a backend-native token; try to find a canonical
		// id that maps to the same Shippo code. Shippo's own tokens are
		// lowercase snake_case ("usps", "dhl_express"), but compare
		// case-insensitively so a caller who typed an odd casing still
		// recovers the canonical id.
		for _, c := range trackage.AllCarriers() {
			if strings.EqualFold(c.Shippo, shippoCode) {
				canon = c.ID
				break
			}
		}
	}

	out := &trackage.Tracking{
		Carrier:        canon,
		TrackingNumber: r.TrackingNumber,
		Status:         trackage.StatusUnknown,
		Raw:            json.RawMessage(raw),
	}

	if r.TrackingStatus != nil {
		out.Status = mapStatus(r.TrackingStatus.Status)
		out.Description = r.TrackingStatus.StatusDetails
		out.LastUpdate = parseTime(r.TrackingStatus.StatusDate)
		if r.TrackingStatus.Substatus != nil {
			out.Substatus = r.TrackingStatus.Substatus.Code
		}
	}

	if eta := parseTime(r.ETA); !eta.IsZero() {
		out.EstDelivery = &eta
	}

	for _, e := range r.TrackingHistory {
		ev := trackage.Event{
			Time:        parseTime(e.StatusDate),
			Status:      mapStatus(e.Status),
			Description: e.StatusDetails,
			Location:    e.Location.String(),
		}
		if e.Substatus != nil {
			ev.Substatus = e.Substatus.Code
		}
		out.Events = append(out.Events, ev)
	}
	// Shippo does not formally guarantee tracking_history ordering, so
	// sort defensively. Events without a parseable timestamp (zero time)
	// sort first; the stable sort keeps their upstream order.
	slices.SortStableFunc(out.Events, func(a, b trackage.Event) int {
		return a.Time.Compare(b.Time)
	})

	return out
}

// mapStatus translates a Shippo status string to the canonical enum.
//
// Reference values (from docs):
//
//	PRE_TRANSIT → pending
//	TRANSIT     → in_transit
//	DELIVERED   → delivered
//	RETURNED    → exception
//	FAILURE     → exception
//	UNKNOWN     → unknown
func mapStatus(s string) trackage.Status {
	switch strings.ToUpper(s) {
	case "PRE_TRANSIT":
		return trackage.StatusPending
	case "TRANSIT":
		return trackage.StatusInTransit
	case "DELIVERED":
		return trackage.StatusDelivered
	case "RETURNED", "FAILURE":
		return trackage.StatusException
	default:
		return trackage.StatusUnknown
	}
}

// parseTime accepts the two ISO-8601 shapes Shippo emits — millisecond
// precision with Z (object_created/object_updated) and second precision
// with Z (status_date) — and returns the zero time on parse failure or
// empty input.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseError builds a trackage.APIError from a non-2xx Shippo response.
// Shippo doesn't publish a canonical error JSON shape, so we accept
// either a per-field validation map ({field: [msgs]}) or a flat
// {detail: msg} body and fall back to the HTTP status when neither
// parses.
func parseError(code int, body []byte) error {
	apiErr := &trackage.APIError{
		Backend:    backendName,
		StatusCode: code,
		Message:    extractMessage(body),
	}
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		apiErr.Cause = trackage.ErrAuth
	case http.StatusNotFound:
		apiErr.Cause = trackage.ErrNotFound
	case http.StatusTooManyRequests:
		apiErr.Cause = trackage.ErrRateLimited
	default:
		// Other 4xx/5xx codes surface without a sentinel; callers can
		// inspect APIError.StatusCode directly.
	}
	return apiErr
}

func extractMessage(body []byte) string {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return ""
	}

	// Flat detail object.
	var flat struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &flat); err == nil && flat.Detail != "" {
		return flat.Detail
	}

	// Field-keyed validation errors: {"carrier":["Not supported."]}
	// Iterate keys in sorted order so the rendered message is stable
	// across runs — Go map iteration order is randomized, and an
	// unsorted Join produces flaky tests and jittering user-facing text.
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err == nil {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		msgs := make([]string, 0, len(fields))
		for _, k := range keys {
			switch vv := fields[k].(type) {
			case []any:
				for _, item := range vv {
					if s, ok := item.(string); ok {
						msgs = append(msgs, k+": "+s)
					}
				}
			case string:
				msgs = append(msgs, k+": "+vv)
			default:
				// Skip non-string, non-array values.
			}
		}
		if len(msgs) > 0 {
			return strings.Join(msgs, "; ")
		}
	}

	return string(body)
}

// Compile-time guarantee that *Tracker satisfies trackage.Tracker.
var _ trackage.Tracker = (*Tracker)(nil)
