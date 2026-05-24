// Package trackingmore implements the trackage Tracker interface
// against the TrackingMore v4 API.
//
// TrackingMore v4's POST /v4/trackings/create is "create-and-get": the
// response carries any checkpoints the courier has already returned.
// On subsequent calls for the same number, create returns meta code
// 4101 ("Tracking already exists"); Track falls back to
// GET /v4/trackings/get?tracking_numbers=… in that case.
//
// Each create consumes one credit; the fall-back GET does not. As with
// EasyPost, the first call on a new number commonly comes back with
// delivery_status="pending" or "notfound" — TrackingMore polls upstream
// carriers asynchronously (typically every 4–6 hours).
//
// API reference: TrackingMore v4 SDKs at github.com/TrackingMore-API.
package trackingmore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"msrl.dev/trackage"
)

const (
	backendName    = "trackingmore"
	defaultBaseURL = "https://api.trackingmore.com/v4"

	// codeAlreadyExists is TrackingMore's meta code for "tracking already
	// exists" — billed once on first create, then surfaces here for
	// subsequent calls. Track falls back to GET on this code.
	codeAlreadyExists = 4101

	// metaCodeOK is TrackingMore's success meta code.
	metaCodeOK = 200

	// codeNotCreated is TrackingMore's meta code for "tracking does not
	// exist; call create first" — emitted by GET /trackings/get when the
	// number was never registered. Semantically a not-found.
	codeNotCreated = 4102

	// codeQuotaExhausted is the meta code TrackingMore returns when the
	// account's plan credits are used up. Treated as rate-limited so
	// callers can back off.
	codeQuotaExhausted = 4190

	// defaultHTTPTimeout bounds a single Track call when the caller does
	// not supply their own HTTPClient.
	defaultHTTPTimeout = 30 * time.Second
)

// marshalJSON is the package's seam for json.Marshal so tests can
// exercise the otherwise-unreachable encoding error path.
var marshalJSON = json.Marshal

// unknownZone tags a wall-clock value the carrier supplied without a
// timezone. We deliberately do not use time.Local — time.Parse returns
// time.Local whenever a parsed offset happens to match the user's
// machine zone, which would make the sentinel collide with a real
// carrier-supplied offset. A named FixedZone is unambiguous.
var unknownZone = time.FixedZone("local", 0)

// Config configures a TrackingMore tracker.
type Config struct {
	// APIKey is the TrackingMore API key. Required.
	APIKey string

	// BaseURL overrides https://api.trackingmore.com/v4 — primarily for tests.
	BaseURL string

	// HTTPClient overrides http.DefaultClient.
	HTTPClient *http.Client
}

// Tracker is the TrackingMore implementation of trackage.Tracker.
type Tracker struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// New constructs a TrackingMore Tracker.
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

// Name returns "trackingmore".
func (*Tracker) Name() string { return backendName }

// Track creates-or-fetches the latest snapshot. New numbers go through
// POST /v4/trackings/create (consumes one credit); already-known numbers
// fall back to GET /v4/trackings/get.
func (t *Tracker) Track(ctx context.Context, carrier, number string) (*trackage.Tracking, error) {
	code, err := t.resolveCarrier(carrier, number)
	if err != nil {
		return nil, err
	}
	tr, raw, err := t.create(ctx, code, number)
	if err == nil {
		return normalize(carrier, tr, raw), nil
	}

	var apiErr *trackage.APIError
	if errors.As(err, &apiErr) && apiErr.Code == strconv.Itoa(codeAlreadyExists) {
		// Don't filter the fallback GET by courier_code. TrackingMore
		// keys created trackings by (tracking_number, courier_code), and
		// the courier the server bound to this number on first create may
		// differ from what we'd resolve now (different --carrier hint,
		// auto-detect that resolved differently, etc.). Filtering would
		// then return an empty list and surface ErrNotFound for a number
		// that IS registered.
		tr, raw, err = t.get(ctx, "", number)
		if err != nil {
			return nil, err
		}
		return normalize(carrier, tr, raw), nil
	}
	return nil, err
}

// lookupCarrier is a seam for tests; see the matching comment in the
// easypost backend. No real carrier in the table has empty TrackingMore.
var lookupCarrier = trackage.LookupCarrier

func (*Tracker) resolveCarrier(carrier, number string) (string, error) {
	if carrier != "" {
		if c, ok := lookupCarrier(carrier); ok {
			if c.TrackingMore == "" {
				return "", fmt.Errorf("trackingmore: %w (%s)", trackage.ErrUnsupportedCarrier, carrier)
			}
			return c.TrackingMore, nil
		}
		// Unknown canonical → passthrough; TrackingMore accepts free-form
		// courier_code values and will surface its own validation error.
		return carrier, nil
	}
	if id := trackage.DetectCarrier(number); id != "" {
		if c, ok := lookupCarrier(id); ok && c.TrackingMore != "" {
			return c.TrackingMore, nil
		}
	}
	return "", nil
}

func (t *Tracker) create(ctx context.Context, courier, number string) (*trackingItem, json.RawMessage, error) {
	body := map[string]string{
		"tracking_number": number,
	}
	if courier != "" {
		body["courier_code"] = courier
	}
	enc, err := marshalJSON(body)
	if err != nil {
		return nil, nil, fmt.Errorf("trackingmore: marshal create body: %w", err)
	}
	r, err := t.request(ctx, http.MethodPost, "/trackings/create", bytes.NewReader(enc))
	if err != nil {
		return nil, nil, err
	}
	if r.env.Meta.Code != metaCodeOK {
		return nil, r.raw, envAsError(r.status, r.env)
	}
	var item trackingItem
	if err := json.Unmarshal(r.env.Data, &item); err != nil {
		return nil, r.raw, fmt.Errorf("trackingmore: parse data: %w", err)
	}
	return &item, r.raw, nil
}

func (t *Tracker) get(ctx context.Context, courier, number string) (*trackingItem, json.RawMessage, error) {
	q := url.Values{}
	q.Set("tracking_numbers", number)
	if courier != "" {
		q.Set("courier_code", courier)
	}
	r, err := t.request(ctx, http.MethodGet, "/trackings/get?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	if r.env.Meta.Code != metaCodeOK {
		return nil, r.raw, envAsError(r.status, r.env)
	}
	var items []trackingItem
	if err := json.Unmarshal(r.env.Data, &items); err != nil {
		return nil, r.raw, fmt.Errorf("trackingmore: parse list data: %w", err)
	}
	if len(items) == 0 {
		return nil, r.raw, &trackage.APIError{
			Backend: backendName, Cause: trackage.ErrNotFound, Message: "no tracking found",
		}
	}
	return &items[0], r.raw, nil
}

// request builds a request, sets the standard headers, and dispatches
// it through do(). The single shared helper means both create and get
// go through the same NewRequest error path, which keeps the coverage
// surface small.
func (t *Tracker) request(ctx context.Context, method, path string, body io.Reader) (rawResp, error) {
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, body)
	if err != nil {
		return rawResp{}, err
	}
	req.Header.Set("Tracking-Api-Key", t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return t.do(req)
}

// rawResp bundles the pieces of a parsed TrackingMore response so
// callers don't have to juggle four return values.
type rawResp struct {
	status int
	raw    json.RawMessage
	env    envelope
}

func (t *Tracker) do(req *http.Request) (rawResp, error) {
	resp, err := t.client.Do(req)
	if err != nil {
		return rawResp{}, err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // close errors on tracker reads are inactionable
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return rawResp{}, err
	}
	// Attempt envelope parse, but don't treat failure as fatal yet — a
	// 401/403/429 from a CDN or proxy in front of TrackingMore is
	// frequently non-JSON, and we'd rather surface a typed sentinel than
	// a "parse envelope" wrapper that callers can't branch on.
	var env envelope
	parseErr := json.Unmarshal(raw, &env)
	if cause := httpStatusToSentinel(resp.StatusCode); cause != nil {
		return rawResp{status: resp.StatusCode, raw: raw, env: env}, &trackage.APIError{
			Backend:    backendName,
			StatusCode: resp.StatusCode,
			Code:       metaCodeString(env),
			Message:    firstNonEmpty(env.Meta.Message, http.StatusText(resp.StatusCode)),
			Cause:      cause,
		}
	}
	if parseErr != nil {
		return rawResp{status: resp.StatusCode, raw: raw}, fmt.Errorf("trackingmore: parse envelope: %w", parseErr)
	}
	return rawResp{status: resp.StatusCode, raw: raw, env: env}, nil
}

// httpStatusToSentinel maps an HTTP status to the matching trackage
// sentinel, or nil for codes that don't need one. Used by do() to
// classify proxy/CDN errors that may arrive as non-JSON bodies.
func httpStatusToSentinel(code int) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return trackage.ErrAuth
	case http.StatusNotFound:
		return trackage.ErrNotFound
	case http.StatusTooManyRequests:
		return trackage.ErrRateLimited
	}
	return nil
}

// metaCodeString returns the envelope's Meta.Code as a string, or empty
// if the envelope did not parse (Code == 0 is also rendered as "").
func metaCodeString(env envelope) string {
	if env.Meta.Code == 0 {
		return ""
	}
	return strconv.Itoa(env.Meta.Code)
}

type envelope struct {
	Meta meta            `json:"meta"`
	Data json.RawMessage `json:"data"`
}

type meta struct {
	Code    int    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func envAsError(httpCode int, env envelope) error {
	apiErr := &trackage.APIError{
		Backend:    backendName,
		StatusCode: httpCode,
		Code:       strconv.Itoa(env.Meta.Code),
		Message:    env.Meta.Message,
	}
	switch env.Meta.Code {
	case 4001, 4002, http.StatusUnauthorized, http.StatusForbidden:
		apiErr.Cause = trackage.ErrAuth
	case http.StatusTooManyRequests, codeQuotaExhausted:
		apiErr.Cause = trackage.ErrRateLimited
	case http.StatusNotFound, codeNotCreated:
		apiErr.Cause = trackage.ErrNotFound
	default:
		// Other meta codes surface without a sentinel.
	}
	return apiErr
}

type trackingItem struct {
	ID                   string       `json:"id"`
	TrackingNumber       string       `json:"tracking_number"`
	CourierCode          string       `json:"courier_code"`
	CarrierCode          string       `json:"carrier_code"`
	DeliveryStatus       string       `json:"delivery_status"`
	Status               string       `json:"status"` // v2/v3 alias
	Substatus            string       `json:"substatus"`
	LatestEvent          string       `json:"latest_event"`
	LastEvent            string       `json:"lastEvent"`
	LatestCheckpointTime string       `json:"latest_checkpoint_time"`
	LastUpdateTime       string       `json:"lastUpdateTime"`
	CreatedAt            string       `json:"created_at"`
	UpdatedAt            string       `json:"updated_at"`
	ExpectedDelivery     string       `json:"expected_delivery_date"`
	ScheduledDelivery    string       `json:"scheduled_delivery_date"`
	OriginInfo           checkpointer `json:"origin_info"`
	DestinationInfo      checkpointer `json:"destination_info"`
}

type checkpointer struct {
	TrackInfo []checkpoint `json:"trackinfo"`
}

// checkpoint mirrors a single entry in `*_info.trackinfo[]`. TrackingMore's
// public v4 docs still describe a PascalCase shape (`Date`,
// `StatusDescription`, `Details`), but the live API actually returns the
// snake_case fields below — keep the legacy PascalCase tags as a fallback
// for older accounts or fixture data.
type checkpoint struct {
	CheckpointDate              string `json:"checkpoint_date"`
	Date                        string `json:"Date"`
	TrackingDetail              string `json:"tracking_detail"`
	StatusDescription           string `json:"StatusDescription"`
	Details                     string `json:"Details"`
	Location                    string `json:"location"`
	City                        string `json:"city"`
	State                       string `json:"state"`
	CountryISO2                 string `json:"country_iso2"`
	Zip                         string `json:"zip"`
	CheckpointDeliveryStatus    string `json:"checkpoint_delivery_status"`
	CheckpointStatus            string `json:"checkpoint_status"`
	CheckpointDeliverySubstatus string `json:"checkpoint_delivery_substatus"`
	Substatus                   string `json:"substatus"`
}

func normalize(userCarrier string, item *trackingItem, raw json.RawMessage) *trackage.Tracking {
	canon := canonicalCarrier(userCarrier, item)

	out := &trackage.Tracking{
		Carrier:        canon,
		TrackingNumber: item.TrackingNumber,
		Status:         mapStatus(firstNonEmpty(item.DeliveryStatus, item.Status)),
		Substatus:      item.Substatus,
		Description:    firstNonEmpty(item.LatestEvent, item.LastEvent),
		LastUpdate:     parseTime(item.LatestCheckpointTime, item.LastUpdateTime, item.UpdatedAt),
		Raw:            raw,
	}

	if eta := parseTime(item.ExpectedDelivery, item.ScheduledDelivery); !eta.IsZero() {
		out.EstDelivery = &eta
	}

	// origin_info has the shipping-country events, destination_info has
	// the last-mile events; v4 returns each list newest-first. Iterate
	// each in reverse and then concatenate origin then destination —
	// origin events all happen before destination events, so the result
	// is the documented oldest-first order without relying on per-event
	// timestamps (which can be zoneless wall-clock values).
	for _, src := range [][]checkpoint{item.OriginInfo.TrackInfo, item.DestinationInfo.TrackInfo} {
		for _, c := range slices.Backward(src) {
			out.Events = append(out.Events, trackage.Event{
				Time:        parseTime(c.CheckpointDate, c.Date),
				Status:      mapStatus(firstNonEmpty(c.CheckpointDeliveryStatus, c.CheckpointStatus)),
				Substatus:   firstNonEmpty(c.CheckpointDeliverySubstatus, c.Substatus),
				Description: firstNonEmpty(c.TrackingDetail, c.StatusDescription),
				Location:    checkpointLocation(c),
			})
		}
	}
	return out
}

// checkpointLocation prefers the free-form `location`/`Details` string
// when present, falling back to a comma-joined city/state/zip/country
// composed from the structured fields. Older v4 fixtures may carry the
// structured fields without the free-form one.
func checkpointLocation(c checkpoint) string {
	if s := firstNonEmpty(c.Location, c.Details); s != "" {
		return s
	}
	parts := make([]string, 0, 4)
	if c.City != "" {
		parts = append(parts, c.City)
	}
	if c.State != "" {
		parts = append(parts, c.State)
	}
	if c.Zip != "" {
		parts = append(parts, c.Zip)
	}
	if c.CountryISO2 != "" {
		parts = append(parts, c.CountryISO2)
	}
	return strings.Join(parts, ", ")
}

// canonicalCarrier returns the lowercase canonical trackage id for a
// tracking response.
//
// The server's response courier_code wins when present: the server
// knows what carrier this tracking number is actually bound to, and
// the user's hint may disagree (the fallback GET path explicitly skips
// filtering by courier_code for the same reason — see Track above).
// A response code that maps to a canonical id is rewritten to that id;
// an unmapped code is returned lower-cased so Tracking.Carrier still
// satisfies its "canonical lowercase id" contract.
//
// With no response code we fall back to the user's hint, mapped to
// canonical if possible. LookupCarrier is case-sensitive (carriersByID
// keys are lowercase), so a user-supplied "USPS" is lower-cased first.
func canonicalCarrier(userCarrier string, item *trackingItem) string {
	if code := firstNonEmpty(item.CourierCode, item.CarrierCode); code != "" {
		for _, c := range trackage.AllCarriers() {
			if strings.EqualFold(c.TrackingMore, code) {
				return c.ID
			}
		}
		return strings.ToLower(code)
	}
	if c, ok := trackage.LookupCarrier(strings.ToLower(userCarrier)); ok {
		return c.ID
	}
	return strings.ToLower(userCarrier)
}

func firstNonEmpty(opts ...string) string {
	for _, s := range opts {
		if s != "" {
			return s
		}
	}
	return ""
}

// mapStatus translates a TrackingMore delivery_status (v4) or status
// (v2/v3) value into the canonical enum.
//
//	pending                       → pending
//	inforeceived                  → pending
//	transit                       → in_transit
//	pickup (their out-for-delivery) → in_transit
//	delivered                     → delivered
//	undelivered, exception, expired → exception
//	notfound (or unrecognized)    → unknown
func mapStatus(s string) trackage.Status {
	switch strings.ToLower(s) {
	case "pending", "inforeceived":
		return trackage.StatusPending
	case "transit", "pickup":
		return trackage.StatusInTransit
	case "delivered":
		return trackage.StatusDelivered
	case "undelivered", "exception", "expired":
		return trackage.StatusException
	default:
		// Includes "notfound" and any value we don't recognize.
		return trackage.StatusUnknown
	}
}

// parseTime accepts the timestamp shapes TrackingMore v4 emits: ISO-8601
// with offset for top-level fields (created_at, latest_checkpoint_time),
// and zoneless ISO ("2026-05-21T10:53:51") or naive space-separated
// ("2015-11-02 17:11") for per-checkpoint fields. Bare dates
// ("2026-05-21") used by expected_delivery_date / scheduled_delivery_date
// are accepted at UTC midnight. Zoneless strings are tagged with
// unknownZone — the wall-clock value is preserved but the actual zone the
// carrier intended is unknown; callers (e.g. the CLI formatter) can
// detect the sentinel and display accordingly.
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
		for _, layout := range []string{
			"2006-01-02T15:04:05.999999999",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02 15:04",
		} {
			if t, err := time.ParseInLocation(layout, s, unknownZone); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

var _ trackage.Tracker = (*Tracker)(nil)
