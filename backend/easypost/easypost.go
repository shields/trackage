// Package easypost implements the trackage Tracker interface against
// the EasyPost tracking API.
//
// EasyPost returns the same Tracker object whether you've never seen
// this tracking number or you saw it last month: POST /v2/trackers is
// server-side-deduped within a 3-month window. Track therefore always
// goes through POST, which both registers the number for ongoing
// webhook updates and returns the latest cached state.
//
// Note: in production the first Track for a new number almost always
// returns status="unknown" with an empty tracking_details array. Real
// data lands via webhooks. Subsequent Track calls return the latest
// cached snapshot but do not force a fresh carrier poll.
//
// API reference: https://docs.easypost.com/docs/trackers
package easypost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"msrl.dev/trackage"
)

const (
	backendName    = "easypost"
	defaultBaseURL = "https://api.easypost.com/v2"

	// defaultHTTPTimeout bounds a single Track call when the caller does
	// not supply their own HTTPClient. EasyPost's POST /v2/trackers is
	// effectively a cache-or-create against their async webhook worker;
	// thirty seconds covers slow upstream polling without wedging the CLI.
	defaultHTTPTimeout = 30 * time.Second
)

// marshalJSON is the package's seam for json.Marshal so tests can
// exercise the otherwise-unreachable encoding error path. Marshal of
// our fixed request struct cannot fail in normal use.
var marshalJSON = json.Marshal

// unknownZone tags a wall-clock value EasyPost surfaced via the
// datetime field without a corresponding datetime_local. We do not use
// time.Local because time.Parse returns time.Local whenever a parsed
// offset matches the user's machine zone, which would make the sentinel
// indistinguishable from a real -07:00 PDT timestamp.
var unknownZone = time.FixedZone("local", 0)

// Config configures an EasyPost tracker.
type Config struct {
	// APIKey is an EasyPost API key (test or production). Required.
	APIKey string

	// BaseURL overrides https://api.easypost.com/v2 — primarily for tests.
	BaseURL string

	// HTTPClient overrides http.DefaultClient.
	HTTPClient *http.Client
}

// Tracker is the EasyPost implementation of trackage.Tracker.
type Tracker struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// New constructs an EasyPost Tracker.
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

// Name returns "easypost".
func (*Tracker) Name() string { return backendName }

// Track creates-or-retrieves an EasyPost Tracker via POST /v2/trackers.
// The carrier hint is passed to EasyPost if we can map it; otherwise
// the call is sent without `carrier` so EasyPost auto-detects.
func (t *Tracker) Track(ctx context.Context, carrier, number string) (*trackage.Tracking, error) {
	code, err := t.resolveCarrier(carrier, number)
	if err != nil {
		return nil, err
	}

	reqBody := struct {
		Tracker createReq `json:"tracker"`
	}{Tracker: createReq{TrackingCode: number, Carrier: code}}
	body, marshalErr := marshalJSON(reqBody)
	if marshalErr != nil {
		return nil, marshalErr
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/trackers", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(t.apiKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // close errors on tracker reads are inactionable

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(resp.StatusCode, rawBody)
	}

	var tr tracker
	if err := json.Unmarshal(rawBody, &tr); err != nil {
		return nil, fmt.Errorf("easypost: parse response: %w", err)
	}
	return normalize(carrier, &tr, rawBody), nil
}

// createReq is the request body for POST /v2/trackers. Carrier is
// omitted from the JSON when empty so EasyPost runs its own auto-detect.
type createReq struct {
	TrackingCode string `json:"tracking_code"`
	Carrier      string `json:"carrier,omitempty"`
}

// lookupCarrier is a seam for tests: it defaults to trackage.LookupCarrier
// but can be swapped so a unit test can drive resolveCarrier with a
// Carrier whose EasyPost field is empty (no real carrier in the table
// has that gap today).
var lookupCarrier = trackage.LookupCarrier

// resolveCarrier returns the EasyPost-native carrier code to send, or
// "" if we should let EasyPost auto-detect. A canonical id whose
// EasyPost column is empty triggers ErrUnsupportedCarrier so callers
// can branch on "this carrier is recognized but not mapped" without
// silently falling through to EasyPost's guess.
func (*Tracker) resolveCarrier(carrier, number string) (string, error) {
	if carrier != "" {
		if c, ok := lookupCarrier(carrier); ok {
			if c.EasyPost == "" {
				return "", fmt.Errorf("easypost: %w (%s)", trackage.ErrUnsupportedCarrier, carrier)
			}
			return c.EasyPost, nil
		}
		// Pass through unknown carrier strings verbatim.
		return carrier, nil
	}
	if id := trackage.DetectCarrier(number); id != "" {
		if c, ok := lookupCarrier(id); ok && c.EasyPost != "" {
			return c.EasyPost, nil
		}
	}
	return "", nil
}

type tracker struct {
	ID              string           `json:"id"`
	TrackingCode    string           `json:"tracking_code"`
	Carrier         string           `json:"carrier"`
	Status          string           `json:"status"`
	StatusDetail    string           `json:"status_detail"`
	EstDeliveryDate string           `json:"est_delivery_date"`
	UpdatedAt       string           `json:"updated_at"`
	TrackingDetails []trackingDetail `json:"tracking_details"`
}

type trackingDetail struct {
	// Datetime is the upstream's "datetime" field. EasyPost stamps a "Z"
	// onto it even when the underlying carrier value is in local time, so
	// the offset is wrong-by-default. Prefer DatetimeLocal where present.
	Datetime         string            `json:"datetime"`
	DatetimeLocal    string            `json:"datetime_local"`
	Status           string            `json:"status"`
	StatusDetail     string            `json:"status_detail"`
	Message          string            `json:"message"`
	Description      string            `json:"description"`
	Source           string            `json:"source"`
	TrackingLocation *trackingLocation `json:"tracking_location"`
}

type trackingLocation struct {
	City    string `json:"city"`
	State   string `json:"state"`
	Country string `json:"country"`
	Zip     string `json:"zip"`
}

func (l *trackingLocation) String() string {
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

func normalize(userCarrier string, tr *tracker, raw []byte) *trackage.Tracking {
	canon := canonicalCarrier(userCarrier, tr.Carrier)

	out := &trackage.Tracking{
		Carrier:        canon,
		TrackingNumber: tr.TrackingCode,
		Status:         mapStatus(tr.Status),
		Substatus:      tr.StatusDetail,
		Raw:            json.RawMessage(raw),
	}

	if t := parseTime(tr.UpdatedAt); !t.IsZero() {
		out.LastUpdate = t
	}
	if t := parseTime(tr.EstDeliveryDate); !t.IsZero() {
		out.EstDelivery = &t
	}

	// Use the latest tracking_details entry as the human-readable
	// description AND the substatus code; EasyPost doesn't carry a
	// tracker-level "latest message" field, and the tracker-level
	// status_detail can lag behind the most recent event's detail (which
	// updates on a different cadence). Surfacing them from the same
	// event keeps Description, Substatus, and LastUpdate aligned.
	if n := len(tr.TrackingDetails); n > 0 {
		last := tr.TrackingDetails[n-1]
		out.Description = firstNonEmpty(last.Description, last.Message)
		if last.StatusDetail != "" {
			out.Substatus = last.StatusDetail
		}
		if t := parseEventTime(last.DatetimeLocal, last.Datetime); !t.IsZero() {
			out.LastUpdate = t
		}
	}

	for _, e := range tr.TrackingDetails {
		out.Events = append(out.Events, trackage.Event{
			Time:        parseEventTime(e.DatetimeLocal, e.Datetime),
			Status:      mapStatus(e.Status),
			Substatus:   e.StatusDetail,
			Description: firstNonEmpty(e.Description, e.Message),
			Location:    e.TrackingLocation.String(),
		})
	}

	return out
}

// parseEventTime applies EasyPost's per-event time rules. When
// datetime_local is present we trust the offset it carries — but a
// minority of upstreams emit a zoneless wall-clock value there (no Z,
// no offset), so the zoneless fallback below tags those with the
// unknownZone sentinel rather than returning zero. When only datetime
// is available, EasyPost stamps a literal "Z" onto values the carrier
// supplied as local time (a known data-quality issue), so for
// "Z"-suffixed values we strip the bogus UTC offset and tag the
// wall-clock with the unknownZone sentinel. Values bearing a real
// offset (e.g. "-07:00") are returned as-is — the upstream got it
// right and recombining would silently shift the absolute instant.
func parseEventTime(datetimeLocal, datetime string) time.Time {
	if datetimeLocal != "" {
		if t := parseTime(datetimeLocal); !t.IsZero() {
			return t
		}
		for _, layout := range []string{
			"2006-01-02T15:04:05.999999999",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
		} {
			if t, err := time.ParseInLocation(layout, datetimeLocal, unknownZone); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	t := parseTime(datetime)
	if t.IsZero() {
		return t
	}
	if !strings.HasSuffix(datetime, "Z") {
		return t
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), unknownZone)
}

// canonicalCarrier returns the lowercase canonical trackage id. The
// userCarrier lookup is lower-cased first because LookupCarrier
// (carriersByID) keys on the canonical lowercase id — a user-supplied
// "USPS" would otherwise miss and fall through to a guess against the
// server-echoed easypost code, which can be a different carrier when
// EasyPost has auto-corrected it. The final fallback also lower-cases
// so the result satisfies the "canonical lowercase id" contract.
func canonicalCarrier(userCarrier, easypostCode string) string {
	if c, ok := trackage.LookupCarrier(strings.ToLower(userCarrier)); ok {
		return c.ID
	}
	for _, c := range trackage.AllCarriers() {
		if strings.EqualFold(c.EasyPost, easypostCode) {
			return c.ID
		}
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

// mapStatus translates EasyPost's status string to the canonical enum.
//
//	pre_transit          → pending
//	in_transit           → in_transit
//	out_for_delivery     → in_transit
//	available_for_pickup → in_transit
//	delivered            → delivered
//	return_to_sender     → exception
//	failure              → exception
//	cancelled            → exception
//	error                → exception
//	unknown / other      → unknown
//
// keep both spellings in the switch so canonicalization is robust.
//
//nolint:misspell // EasyPost emits the British spelling "cancelled"; we
func mapStatus(s string) trackage.Status {
	switch strings.ToLower(s) {
	case "pre_transit":
		return trackage.StatusPending
	case "in_transit", "out_for_delivery", "available_for_pickup":
		return trackage.StatusInTransit
	case "delivered":
		return trackage.StatusDelivered
	case "return_to_sender", "failure", "cancelled", "canceled", "error":
		return trackage.StatusException
	default:
		return trackage.StatusUnknown
	}
}

// parseTime preserves the zone from the input string. Callers display
// in that zone; the UTC instant is recoverable via time.Time.UTC().
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// errorEnvelope is EasyPost's wire format for API errors.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func parseError(code int, body []byte) error {
	apiErr := &trackage.APIError{
		Backend:    backendName,
		StatusCode: code,
	}
	var env errorEnvelope
	if json.Unmarshal(body, &env) == nil {
		apiErr.Code = env.Error.Code
		apiErr.Message = env.Error.Message
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		apiErr.Cause = trackage.ErrAuth
	case http.StatusNotFound:
		apiErr.Cause = trackage.ErrNotFound
	case http.StatusTooManyRequests:
		apiErr.Cause = trackage.ErrRateLimited
	default:
		// Other codes surface without a sentinel.
	}
	return apiErr
}

var _ trackage.Tracker = (*Tracker)(nil)
