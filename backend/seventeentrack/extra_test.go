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

package seventeentrack

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
)

func TestName(t *testing.T) {
	t.Parallel()
	if got := (&Tracker{}).Name(); got != "17track" {
		t.Errorf("Name = %q", got)
	}
}

func TestResolveCarrierVariants(t *testing.T) {
	t.Parallel()
	tr := &Tracker{}
	// Canonical
	code, canon, err := tr.resolveCarrier(trackage.CarrierUSPS, "x")
	if err != nil || code != 21051 || canon != "usps" {
		t.Errorf("canonical → code=%d canon=%q err=%v", code, canon, err)
	}
	// Numeric-string passthrough
	code, canon, err = tr.resolveCarrier("100002", "x")
	if err != nil || code != 100002 || canon != "100002" {
		t.Errorf("numeric string → code=%d canon=%q err=%v", code, canon, err)
	}
	// Unknown alpha → ErrUnsupportedCarrier (no silent auto-detect).
	_, _, err = tr.resolveCarrier("weird", "x")
	if !errors.Is(err, trackage.ErrUnsupportedCarrier) {
		t.Errorf("alpha unknown → err=%v, want ErrUnsupportedCarrier", err)
	}
	// Detected
	code, canon, err = tr.resolveCarrier("", "1Z999AA10123456784")
	if err != nil || code != 100002 || canon != "ups" {
		t.Errorf("detected → code=%d canon=%q err=%v", code, canon, err)
	}
	// Undetectable → 0 + empty (auto-detect by 17Track)
	code, canon, err = tr.resolveCarrier("", "12")
	if err != nil || code != 0 || canon != "" {
		t.Errorf("undetectable → code=%d canon=%q err=%v", code, canon, err)
	}
}

func TestRejectedNotAlreadyRegistered(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		//nolint:lll // JSON test fixture
		_, _ = w.Write([]byte(
			`{"code":0,"data":{"accepted":[],"rejected":[{"number":"x","error":{"code":-18019903,"message":"Carrier can not be detected."}}]}}`,
		))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected error from register rejection")
	}
	var ae *trackage.APIError
	if !errors.As(err, &ae) || ae.Code != "-18019903" {
		t.Errorf("expected APIError -18019903, got %+v", ae)
	}
}

func TestRegisterEnvelopeFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":-18010003,"data":{"errors":[{"code":-18010003,"message":"server"}]}}`))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected envelope-level error")
	}
}

func TestGetInfoRejected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			//nolint:lll // JSON test fixture
			_, _ = w.Write([]byte(
				`{"code":0,"data":{"accepted":[],"rejected":[{"number":"x","error":{"code":-18019909,"message":"No tracking info."}}]}}`,
			))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected gettrackinfo rejection error")
	}
}

func TestGetInfoEnvelopeFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			_, _ = w.Write([]byte(`{"code":-18010003,"data":{"errors":[{"code":-18010003,"message":"server"}]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected envelope-level error from gettrackinfo")
	}
}

//nolint:dupl // structure overlaps with TestAlreadyRegisteredIsSwallowed but the assertions differ
func TestGetInfoEmptyAccepted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[],"rejected":[]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	got, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err != nil {
		t.Fatalf("expected normalize-without-trackinfo to succeed, got %v", err)
	}
	if got.Status != trackage.StatusUnknown {
		t.Errorf("status = %q, want unknown", got.Status)
	}
}

func TestPostJSONRawHTTP429(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}

func TestPostJSONRawHTTP401(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestPostJSONRawMalformedBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAsAPIErrorCodes(t *testing.T) {
	t.Parallel()
	// 429 envelope code → ErrRateLimited.
	e := envelope{Code: http.StatusTooManyRequests}
	if !errors.Is(e.asAPIError(), trackage.ErrRateLimited) {
		t.Error("429 should map to ErrRateLimited")
	}
	// 0 fall-through → no sentinel
	e = envelope{Code: -1}
	err := e.asAPIError()
	if errors.Is(err, trackage.ErrAuth) || errors.Is(err, trackage.ErrRateLimited) {
		t.Error("other codes should not map to sentinels")
	}
}

func TestFmtLocation(t *testing.T) {
	t.Parallel()
	// location string wins when present
	if got := fmtLocation("Berlin, DE", address{}); got != "Berlin, DE" {
		t.Errorf("got %q", got)
	}
	// fall through to address fields
	got := fmtLocation("", address{City: "Berlin", State: "BE", PostalCode: "10115", Country: "DE"})
	if got != "Berlin, BE, 10115, DE" {
		t.Errorf("got %q", got)
	}
	// fully empty
	if got := fmtLocation("", address{}); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestLookupCanonicalByCodeZero(t *testing.T) {
	t.Parallel()
	if got := lookupCanonicalByCode(0); got != "" {
		t.Errorf("0 → %q, want empty", got)
	}
	if got := lookupCanonicalByCode(999999); got != "" {
		t.Errorf("unknown → %q, want empty", got)
	}
	if got := lookupCanonicalByCode(21051); got != "usps" {
		t.Errorf("21051 → %q, want usps", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if firstNonEmpty("", "y") != "y" {
		t.Error("should return first non-empty")
	}
	if firstNonEmpty("", "") != "" {
		t.Error("all empty → empty")
	}
}

func TestParseTime(t *testing.T) {
	t.Parallel()
	if got := parseTime("", ""); !got.IsZero() {
		t.Error("empty → zero")
	}
	if got := parseTime("bad", "also bad"); !got.IsZero() {
		t.Error("garbage → zero")
	}
	if got := parseTime("", "2022-05-02T18:34:00Z"); got.IsZero() {
		t.Error("second arg should parse")
	}
}

func TestNetworkErrorBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected network error")
	}
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (errReadCloser) Close() error             { return nil }

type errBodyTransport struct{}

func (errBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}}, nil
}

func TestReadBodyError(t *testing.T) {
	t.Parallel()
	tr := New(Config{APIKey: "k", HTTPClient: &http.Client{Transport: errBodyTransport{}}})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "read boom") {
		t.Fatalf("expected read body error, got %v", err)
	}
}

func TestNewRequestError(t *testing.T) {
	t.Parallel()
	tr := New(Config{APIKey: "k", BaseURL: "http://example.com\x7f"})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected NewRequest error")
	}
}

//nolint:paralleltest // mutates package-level marshalJSON
func TestMarshalError(t *testing.T) {
	orig := marshalJSON
	t.Cleanup(func() { marshalJSON = orig })
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal boom") }
	tr := New(Config{APIKey: "k"})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil || !strings.Contains(err.Error(), "marshal boom") {
		t.Fatalf("expected marshal error, got %v", err)
	}
}

// TestEmptyAcceptedPreservesNumber confirms that when 17Track's
// gettrackinfo returns both accepted=[] and rejected=[] (the documented
// "registered but no scrape yet" state for async backends), the
// resulting Tracking still carries the tracking number the caller
// asked about — losing it leaves downstream code with a blank key.
func TestEmptyAcceptedPreservesNumber(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[],"rejected":[]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	got, err := tr.Track(context.Background(), trackage.CarrierUSPS, "RR987654321US")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if got.TrackingNumber != "RR987654321US" {
		t.Errorf("TrackingNumber = %q, want %q (must be set from the request even when accepted is empty)",
			got.TrackingNumber, "RR987654321US")
	}
	if got.Status != trackage.StatusUnknown {
		t.Errorf("Status = %q, want unknown", got.Status)
	}
}

// TestGetInfoRejectedMapsErrNotFound exercises the rejected-code paths
// that mean "no data" — they must carry the ErrNotFound sentinel so
// callers can branch on it via errors.Is.
func TestGetInfoRejectedMapsErrNotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			//nolint:lll // JSON test fixture
			_, _ = w.Write([]byte(
				`{"code":0,"data":{"accepted":[],"rejected":[{"number":"x","error":{"code":-18019909,"message":"No tracking info."}}]}}`,
			))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrNotFound) {
		t.Errorf("expected ErrNotFound for code -18019909, got %v", err)
	}
}

// TestHTTP404MapsErrNotFound confirms the gateway-level 404 path also
// carries the sentinel — symmetric with how shippo/easypost behave.
func TestHTTP404MapsErrNotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrNotFound) {
		t.Errorf("expected ErrNotFound for HTTP 404, got %v", err)
	}
}

// TestGetInfoNetworkError exercises the second-call-fails branch in
// getInfo: register succeeds, then gettrackinfo's transport breaks.
func TestGetInfoNetworkError(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			calls++
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			calls++
			_, _ = w.Write([]byte("malformed-not-json"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tr.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err == nil {
		t.Fatal("expected parse error from gettrackinfo")
	}
	if calls != 2 {
		t.Errorf("expected register + gettrackinfo (2), got %d", calls)
	}
}

// TestMultiProviderEventsChronological exercises the cross-carrier
// shipment case (e.g. China Post → USPS) where track_info.tracking
// .providers contains more than one provider, each with newest-first
// events. trackage.Tracking.Events must be oldest-first and provider-
// major (all origin scans first, then all destination scans).
// Regression test: reversing the full concatenated slice instead of
// reversing each provider's list flips the inter-provider order.
func TestMultiProviderEventsChronological(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":3011}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			_, _ = w.Write([]byte(`{
				"code":0,
				"data":{"accepted":[{"number":"x","carrier":3011,"track_info":{
					"latest_status":{"status":"Delivered","sub_status":"Delivered_Other"},
					"tracking":{"providers":[
						{"provider":{"key":3011,"name":"China Post"},"events":[
							{"description":"CP_new","time_iso":"2022-04-26T08:00:00Z","stage":"InTransit","sub_status":"InTransit_Other"},
							{"description":"CP_old","time_iso":"2022-04-25T08:00:00Z","stage":"PickedUp","sub_status":"InTransit_PickedUp"}
						]},
						{"provider":{"key":21051,"name":"USPS"},"events":[
							{"description":"US_new","time_iso":"2022-05-02T14:00:00Z","stage":"Delivered","sub_status":"Delivered_Other"},
							{"description":"US_old","time_iso":"2022-05-01T08:00:00Z","stage":"Arrival","sub_status":"InTransit_Arrival"}
						]}
					]}
				}}],"rejected":[]}
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	got, err := tr.Track(context.Background(), trackage.CarrierChinaPost, "x")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	wantDescriptions := []string{"CP_old", "CP_new", "US_old", "US_new"}
	if len(got.Events) != len(wantDescriptions) {
		t.Fatalf("len(Events) = %d, want %d", len(got.Events), len(wantDescriptions))
	}
	for i, want := range wantDescriptions {
		if got.Events[i].Description != want {
			t.Errorf("Events[%d].Description = %q, want %q", i, got.Events[i].Description, want)
		}
	}
	for i := 1; i < len(got.Events); i++ {
		if got.Events[i].Time.Before(got.Events[i-1].Time) {
			t.Errorf("events not chronological at index %d: %v before %v",
				i, got.Events[i].Time, got.Events[i-1].Time)
		}
	}
}

// TestNormalizeFillsCarrierFromCode covers the branch where the caller
// passed no canonical id but the response includes a known integer code
// we can translate back.
func TestNormalizeFillsCarrierFromCode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051}],"rejected":[]}}`))
		case "/track/v2.2/gettrackinfo":
			//nolint:lll // JSON test fixture
			_, _ = w.Write([]byte(
				`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051,"track_info":{"latest_status":{"status":"Delivered"}}}],"rejected":[]}}`,
			))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	tr := New(Config{APIKey: "k", BaseURL: srv.URL})
	// Pass a tracking number with no clear format → resolveCarrier
	// returns canon="" → normalize fills Carrier from response carrier code 21051.
	got, err := tr.Track(context.Background(), "", "non-detectable-number-format")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if got.Carrier != trackage.CarrierUSPS {
		t.Errorf("Carrier = %q, want usps (filled from code 21051)", got.Carrier)
	}
}

//nolint:paralleltest // mutates package-level lookupCarrier
func TestUnsupportedCanonicalCarrierRejected(t *testing.T) {
	// Real carriers all have a non-zero 17Track code, so swap the seam.
	orig := lookupCarrier
	t.Cleanup(func() { lookupCarrier = orig })
	lookupCarrier = func(id string) (trackage.Carrier, bool) {
		return trackage.Carrier{ID: id}, true
	}
	tr := New(Config{APIKey: "k"})
	if _, _, err := tr.resolveCarrier("fake", "x"); !errors.Is(err, trackage.ErrUnsupportedCarrier) {
		t.Errorf("resolveCarrier: got %v, want ErrUnsupportedCarrier", err)
	}
	if _, err := tr.Track(context.Background(), "fake", "x"); !errors.Is(err, trackage.ErrUnsupportedCarrier) {
		t.Errorf("Track: got %v, want ErrUnsupportedCarrier", err)
	}
}

func TestNotFoundCauseForUnrelatedCode(t *testing.T) {
	t.Parallel()
	if got := notFoundCauseFor(-99999); got != nil {
		t.Errorf("unrelated code → %v, want nil", got)
	}
}

func TestNormalizeUnknownInfoCarrierFillsInteger(t *testing.T) {
	t.Parallel()
	// canon="" and code=0 → out.Carrier starts empty. info.Carrier is a
	// non-zero code that's not in our table → the else-if branch fills
	// out.Carrier with the integer string so the caller has something.
	info := &trackAccepted{Number: "x", Carrier: 999999}
	got := normalize("", "x", 0, info, nil)
	if got.Carrier != "999999" {
		t.Errorf("Carrier = %q, want \"999999\"", got.Carrier)
	}
}
