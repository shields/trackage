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

// Package seventeentrack implements the trackage Tracker interface
// against the 17Track v2.2 tracking API.
//
// 17Track is purely a tracking aggregator and is async-by-default:
// /register subscribes a number and consumes quota, /gettrackinfo
// returns whatever 17Track has scraped so far. The first call on a
// fresh number almost always returns StatusUnknown — real data lands
// later via webhook or repeated polling (polling is free).
//
// Track here always issues register-then-gettrackinfo. Re-registering
// an already-known number returns 17Track error -18019901 ("already
// registered"), which is not billed and which trackage swallows
// silently before proceeding to gettrackinfo.
//
// Carrier codes are opaque integers (e.g. USPS = 21051). Callers may
// pass a canonical trackage id, an integer string, or omit the carrier
// entirely; in the last case trackage's local detector runs first,
// and if it produces nothing, 17Track's own auto-detection takes over.
//
// API reference: https://asset.17track.net/api/document/v2.2_en/index.html
package seventeentrack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"msrl.dev/trackage"
)

const (
	backendName    = "17track"
	defaultBaseURL = "https://api.17track.net"

	// codeAlreadyRegistered is the 17Track per-record error code returned
	// when a tracking number has already been registered. It is not
	// billed and trackage treats it as success.
	codeAlreadyRegistered = -18019901

	// 17Track per-record error codes that mean "the upstream has no
	// useful data for this number." They surface from gettrackinfo's
	// rejected[] when the carrier hasn't returned anything yet, when
	// the number isn't recognized, or when 17Track failed to scrape it.
	// All three map to trackage.ErrNotFound so callers can branch on
	// "this tracking number is not yet trackable" uniformly.
	codeCarrierUndetectable = -18019902
	codeCarrierMismatch     = -18019903
	codeNoTrackingInfo      = -18019909

	// 17Track envelope-level quota codes. They surface when the account's
	// daily request budget or lifetime quota is exhausted; treat both as
	// ErrRateLimited so callers can back off uniformly.
	codeDailyLimit     = -18019907
	codeQuotaExhausted = -18019908

	// defaultHTTPTimeout bounds a single Track call when the caller does
	// not supply their own HTTPClient. 17Track does register+gettrackinfo
	// back-to-back; thirty seconds covers the round-trip with margin.
	defaultHTTPTimeout = 30 * time.Second
)

// marshalJSON is the package's seam for json.Marshal so tests can
// exercise the otherwise-unreachable encoding error path.
var marshalJSON = json.Marshal

// Config configures a 17Track tracker.
type Config struct {
	// APIKey is the 17Track API token (`17token` header). Required.
	APIKey string

	// BaseURL overrides https://api.17track.net — primarily for tests.
	BaseURL string

	// HTTPClient overrides http.DefaultClient.
	HTTPClient *http.Client
}

// Tracker is the 17Track implementation of trackage.Tracker.
type Tracker struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// New constructs a 17Track Tracker.
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

// Name returns "17track".
func (*Tracker) Name() string { return "17track" }

// Track registers (idempotently) and then fetches the latest snapshot.
func (t *Tracker) Track(ctx context.Context, carrier, number string) (*trackage.Tracking, error) {
	code, canon, err := t.resolveCarrier(carrier, number)
	if err != nil {
		return nil, err
	}

	if err = t.register(ctx, code, number); err != nil {
		return nil, err
	}
	info, raw, err := t.getInfo(ctx, code, number)
	if err != nil {
		return nil, err
	}

	return normalize(canon, number, code, info, raw), nil
}

// lookupCarrier is a seam for tests; see the matching comment in
// easypost.go. No real carrier in the table has SeventeenTrack==0.
var lookupCarrier = trackage.LookupCarrier

// resolveCarrier returns the 17Track integer carrier code (0 means
// "auto-detect / not specified") and the canonical trackage id we'll
// stamp on the resulting Tracking.
//
// 17Track's carrier field is an integer; there is no string-passthrough
// path. A non-canonical, non-numeric carrier string is therefore a user
// mistake (e.g. "usps-priority", "ups-saver") rather than a backend-
// native code, and we surface ErrUnsupportedCarrier rather than
// silently falling through to auto-detect — which would discard the
// caller's intent and obscure the typo.
func (*Tracker) resolveCarrier(carrier, number string) (int, string, error) {
	if carrier != "" {
		if c, ok := lookupCarrier(carrier); ok {
			if c.SeventeenTrack == 0 {
				return 0, "", fmt.Errorf("17track: %w (%s)", trackage.ErrUnsupportedCarrier, carrier)
			}
			return c.SeventeenTrack, c.ID, nil
		}
		// Accept a stringified integer ("21051") as a direct 17Track code.
		if n, err := strconv.Atoi(carrier); err == nil {
			return n, carrier, nil
		}
		return 0, "", fmt.Errorf(
			"17track: %w (%s); pass a canonical id or numeric 17Track code",
			trackage.ErrUnsupportedCarrier, carrier,
		)
	}
	if id := trackage.DetectCarrier(number); id != "" {
		if c, ok := lookupCarrier(id); ok && c.SeventeenTrack != 0 {
			return c.SeventeenTrack, c.ID, nil
		}
	}
	return 0, "", nil
}

type registerItem struct {
	Number        string `json:"number"`
	Carrier       int    `json:"carrier,omitempty"`
	AutoDetection bool   `json:"auto_detection"`
}

type trackInfoItem struct {
	Number  string `json:"number"`
	Carrier int    `json:"carrier,omitempty"`
}

func (t *Tracker) register(ctx context.Context, code int, number string) error {
	body := []registerItem{{
		Number:        number,
		Carrier:       code,
		AutoDetection: code == 0,
	}}
	var resp envelope
	if err := t.postJSON(ctx, "/track/v2.2/register", body, &resp); err != nil {
		return err
	}
	if c := resp.Code; c != 0 {
		return resp.asAPIError()
	}
	// Track sends one number per call, but the API is batch-shaped and
	// could in principle return reordered rejections. Walk the whole
	// list so an "already registered" entry anywhere wins.
	for _, rej := range resp.Data.Rejected {
		if rej.Error.Code == codeAlreadyRegistered {
			return nil
		}
	}
	if len(resp.Data.Rejected) > 0 {
		rej := resp.Data.Rejected[0]
		return &trackage.APIError{
			Backend:    backendName,
			StatusCode: http.StatusOK,
			Code:       strconv.Itoa(rej.Error.Code),
			Message:    rej.Error.Message,
			Cause:      notFoundCauseFor(rej.Error.Code),
		}
	}
	return nil
}

// notFoundCauseFor reports the trackage sentinel for 17Track per-record
// codes that mean "this number cannot be tracked." Returning nil keeps
// other codes opaque.
func notFoundCauseFor(code int) error {
	switch code {
	case codeCarrierUndetectable, codeCarrierMismatch, codeNoTrackingInfo:
		return trackage.ErrNotFound
	}
	return nil
}

func (t *Tracker) getInfo(ctx context.Context, code int, number string) (*trackAccepted, json.RawMessage, error) {
	body := []trackInfoItem{{Number: number, Carrier: code}}
	resp, raw, err := t.postJSONRaw(ctx, "/track/v2.2/gettrackinfo", body)
	if err != nil {
		return nil, nil, err
	}
	if resp.Code != 0 {
		return nil, nil, resp.asAPIError()
	}
	if len(resp.Data.Accepted) == 0 {
		if len(resp.Data.Rejected) > 0 {
			rej := resp.Data.Rejected[0]
			return nil, nil, &trackage.APIError{
				Backend: backendName,
				Code:    strconv.Itoa(rej.Error.Code),
				Message: rej.Error.Message,
				Cause:   notFoundCauseFor(rej.Error.Code),
			}
		}
		return nil, raw, nil
	}
	return &resp.Data.Accepted[0], raw, nil
}

func (t *Tracker) postJSON(ctx context.Context, path string, body any, out *envelope) error {
	env, _, err := t.postJSONRaw(ctx, path, body)
	if err != nil {
		return err
	}
	*out = env
	return nil
}

func (t *Tracker) postJSONRaw(ctx context.Context, path string, body any) (envelope, json.RawMessage, error) {
	enc, err := marshalJSON(body)
	if err != nil {
		return envelope{}, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+path, bytes.NewReader(enc))
	if err != nil {
		return envelope{}, nil, err
	}
	req.Header.Set("17token", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return envelope{}, nil, err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // close errors on tracker reads are inactionable
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return envelope{}, nil, err
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return envelope{}, raw, &trackage.APIError{
			Backend:    backendName,
			StatusCode: resp.StatusCode,
			Cause:      trackage.ErrRateLimited,
			Message:    "rate limited",
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return envelope{}, raw, &trackage.APIError{
			Backend:    backendName,
			StatusCode: resp.StatusCode,
			Cause:      trackage.ErrAuth,
			Message:    "auth failed",
		}
	case http.StatusNotFound:
		return envelope{}, raw, &trackage.APIError{
			Backend:    backendName,
			StatusCode: resp.StatusCode,
			Cause:      trackage.ErrNotFound,
			Message:    "not found",
		}
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return envelope{}, raw, fmt.Errorf("17track: parse response: %w", err)
	}
	return env, raw, nil
}

type envelope struct {
	Code int          `json:"code"`
	Data envelopeData `json:"data"`
}

type envelopeData struct {
	Accepted []trackAccepted `json:"accepted"`
	Rejected []rejectEntry   `json:"rejected"`
	Errors   []apiSubError   `json:"errors"`
}

func (e envelope) asAPIError() error {
	msg := ""
	if len(e.Data.Errors) > 0 {
		msg = e.Data.Errors[0].Message
	}
	apiErr := &trackage.APIError{
		Backend: backendName,
		Code:    strconv.Itoa(e.Code),
		Message: msg,
	}
	switch e.Code {
	case -18010001, -18010002, -18010004:
		apiErr.Cause = trackage.ErrAuth
	case codeDailyLimit, codeQuotaExhausted, http.StatusTooManyRequests:
		apiErr.Cause = trackage.ErrRateLimited
	default:
		// Other codes surface without a sentinel.
	}
	return apiErr
}

type rejectEntry struct {
	Number string      `json:"number"`
	Error  apiSubError `json:"error"`
}

type apiSubError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type trackAccepted struct {
	Number    string    `json:"number"`
	Carrier   int       `json:"carrier"`
	Tag       string    `json:"tag"`
	TrackInfo trackInfo `json:"track_info"`
}

type trackInfo struct {
	LatestStatus latestStatus `json:"latest_status"`
	LatestEvent  trackEvent   `json:"latest_event"`
	TimeMetrics  timeMetrics  `json:"time_metrics"`
	Tracking     trackingSub  `json:"tracking"`
}

type latestStatus struct {
	Status         string `json:"status"`
	SubStatus      string `json:"sub_status"`
	SubStatusDescr string `json:"sub_status_descr"`
}

type trackEvent struct {
	TimeISO     string  `json:"time_iso"`
	TimeUTC     string  `json:"time_utc"`
	Description string  `json:"description"`
	Location    string  `json:"location"`
	Stage       string  `json:"stage"`
	SubStatus   string  `json:"sub_status"`
	Address     address `json:"address"`
}

type address struct {
	Country    string `json:"country"`
	State      string `json:"state"`
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
}

type timeMetrics struct {
	EstimatedDeliveryDate estimatedDeliveryDate `json:"estimated_delivery_date"`
}

type estimatedDeliveryDate struct {
	Source string `json:"source"`
	From   string `json:"from"`
	To     string `json:"to"`
}

type trackingSub struct {
	Providers []provider `json:"providers"`
}

type provider struct {
	Provider providerInfo `json:"provider"`
	Events   []trackEvent `json:"events"`
}

type providerInfo struct {
	Key  int    `json:"key"`
	Name string `json:"name"`
}

func normalize(canon, number string, code int, info *trackAccepted, raw json.RawMessage) *trackage.Tracking {
	// Prefer the canonical-table translation of the resolved code over
	// the raw user input — so a caller who passed a numeric string like
	// "21051" still gets Tracking.Carrier="usps" rather than the integer.
	out := &trackage.Tracking{
		Carrier:        firstNonEmpty(lookupCanonicalByCode(code), canon),
		TrackingNumber: number,
		Status:         trackage.StatusUnknown,
		Raw:            raw,
	}
	if info == nil {
		return out
	}
	if info.Number != "" {
		out.TrackingNumber = info.Number
	}
	// If the upstream's auto-detect (or manual correction) settled on a
	// different carrier than the caller asked about, prefer the
	// authoritative answer. When the response code is one we don't have
	// in our carriers table, fall back to surfacing the raw integer (only
	// if we still have nothing better) so the caller has *something* to
	// display — mirroring how resolveCarrier accepts a numeric carrier
	// string on input.
	if info.Carrier != 0 {
		if corrected := lookupCanonicalByCode(info.Carrier); corrected != "" {
			out.Carrier = corrected
		} else if out.Carrier == "" {
			out.Carrier = strconv.Itoa(info.Carrier)
		}
	}

	ti := info.TrackInfo
	out.Status = mapStatus(ti.LatestStatus.Status)
	out.Substatus = ti.LatestStatus.SubStatus
	out.Description = firstNonEmpty(ti.LatestEvent.Description, ti.LatestStatus.SubStatusDescr)
	out.LastUpdate = parseTime(ti.LatestEvent.TimeISO, ti.LatestEvent.TimeUTC)
	if to := parseTime(ti.TimeMetrics.EstimatedDeliveryDate.To); !to.IsZero() {
		out.EstDelivery = &to
	}

	// providers is typically ordered origin → destination for a hop
	// (e.g. [ChinaPost, USPS]); per-provider events are newest-first.
	// Reverse each provider list individually before concatenating so the
	// final slice is provider-major and chronological within each provider.
	// Reversing the whole concatenated slice instead would flip the
	// inter-provider order, putting destination scans before origin scans.
	for _, p := range ti.Tracking.Providers {
		for _, e := range slices.Backward(p.Events) {
			// sub_status is the canonical full vocabulary (e.g.
			// "InTransit_PickedUp"); stage is often null or a milestone
			// shorthand ("PickedUp"). Prefer sub_status when present.
			key := firstNonEmpty(e.SubStatus, e.Stage)
			out.Events = append(out.Events, trackage.Event{
				Time:        parseTime(e.TimeISO, e.TimeUTC),
				Status:      mapStatusFromStage(key),
				Substatus:   key,
				Description: e.Description,
				Location:    fmtLocation(e.Location, e.Address),
			})
		}
	}
	return out
}

func fmtLocation(s string, a address) string {
	if s != "" {
		return s
	}
	parts := make([]string, 0, 4)
	if a.City != "" {
		parts = append(parts, a.City)
	}
	if a.State != "" {
		parts = append(parts, a.State)
	}
	if a.PostalCode != "" {
		parts = append(parts, a.PostalCode)
	}
	if a.Country != "" {
		parts = append(parts, a.Country)
	}
	return strings.Join(parts, ", ")
}

func lookupCanonicalByCode(code int) string {
	if code == 0 {
		return ""
	}
	for _, c := range trackage.AllCarriers() {
		if c.SeventeenTrack == code {
			return c.ID
		}
	}
	return ""
}

func firstNonEmpty(opts ...string) string {
	for _, s := range opts {
		if s != "" {
			return s
		}
	}
	return ""
}

// mapStatus translates a v2/v2.2 latest_status.status value, and also
// the milestone-vocabulary key_stage values (PickedUp, Departure, ...)
// that show up bare in per-event `stage` when `sub_status` is missing.
//
//	NotFound (or unrecognized)                  → unknown
//	InfoReceived                                → pending
//	InTransit, PickedUp, Departure, Arrival     → in_transit
//	AvailableForPickup, OutForDelivery          → in_transit
//	Delivered                                   → delivered
//	Expired, DeliveryFailure, Exception         → exception
//	Returning, Returned                         → exception
func mapStatus(s string) trackage.Status {
	switch s {
	case "InfoReceived":
		return trackage.StatusPending
	case "InTransit", "PickedUp", "Departure", "Arrival",
		"AvailableForPickup", "OutForDelivery":
		return trackage.StatusInTransit
	case "Delivered":
		return trackage.StatusDelivered
	case "Expired", "DeliveryFailure", "Exception", "Returning", "Returned":
		return trackage.StatusException
	default:
		// Includes "NotFound" and anything we don't recognize.
		return trackage.StatusUnknown
	}
}

// mapStatusFromStage looks at a per-event "stage" (which uses the
// substatus vocabulary like "InTransit_PickedUp") and maps the prefix
// to the canonical status.
func mapStatusFromStage(stage string) trackage.Status {
	prefix, _, _ := strings.Cut(stage, "_")
	return mapStatus(prefix)
}

// parseTime tries each opts value in order, returning the first that
// parses. The location is preserved so callers can display in the
// upstream's zone — pass time_iso (with offset) before time_utc when
// you want the scan-local zone. Date-only values (the shape 17Track
// uses for estimated_delivery_date.from/to) are accepted with their
// implicit UTC midnight.
func parseTime(opts ...string) time.Time {
	for _, s := range opts {
		if s == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

var _ trackage.Tracker = (*Tracker)(nil)
