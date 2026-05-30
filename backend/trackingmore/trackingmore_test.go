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

package trackingmore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
)

//nolint:lll // JSON test fixture; readability beats wrapping.
const itemDeliveredJSON = `{
  "meta": {"code": 200, "type": "Success", "message": "Success"},
  "data": {
    "id": "009e9a8a6450cb5ce4b53ac75674fe78",
    "tracking_number": "RR050349575PH",
    "courier_code": "phlpost",
    "delivery_status": "delivered",
    "substatus": "delivered001",
    "created_at": "2015-10-30T11:35:16+08:00",
    "updated_at": "2015-11-03T13:47:20+08:00",
    "destination_country": "US",
    "latest_event": "Delivered,RENTON, WA 98056,2015-11-02 17:11",
    "latest_checkpoint_time": "2015-11-02T17:11:00",
    "origin_info": {
      "trackinfo": [
        {"checkpoint_date": "2015-10-31T04:00:00", "tracking_detail": "Departed", "location": "Manila PH", "city": "Manila", "country_iso2": "PH", "checkpoint_delivery_status": "transit", "checkpoint_delivery_substatus": "transit001"},
        {"checkpoint_date": "2015-10-30T18:00:00", "tracking_detail": "Picked up", "location": "Manila PH", "city": "Manila", "country_iso2": "PH", "checkpoint_delivery_status": "transit", "checkpoint_delivery_substatus": "transit001"}
      ]
    },
    "destination_info": {
      "trackinfo": [
        {"checkpoint_date": "2015-11-02T17:11:00", "tracking_detail": "Delivered", "location": "RENTON WA US", "city": "RENTON", "state": "WA", "country_iso2": "US", "checkpoint_delivery_status": "delivered", "checkpoint_delivery_substatus": "delivered001"}
      ]
    }
  }
}`

func TestTrackCreateSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Tracking-Api-Key"); got != "tmkey" {
			t.Errorf("Tracking-Api-Key = %q, want tmkey", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/trackings/create" {
			t.Errorf("got %s %s, want POST /trackings/create", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(itemDeliveredJSON))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "tmkey", BaseURL: srv.URL})
	got, err := tracker.Track(context.Background(), "phlpost", "RR050349575PH")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if got.Status != trackage.StatusDelivered {
		t.Errorf("Status = %q, want %q", got.Status, trackage.StatusDelivered)
	}
	if got.Substatus != "delivered001" {
		t.Errorf("Substatus = %q, want delivered001", got.Substatus)
	}
	if !strings.Contains(got.Description, "Delivered") {
		t.Errorf("Description = %q", got.Description)
	}
	if len(got.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3 (origin 2 + dest 1)", len(got.Events))
	}
	if got.Events[2].Status != trackage.StatusDelivered {
		t.Errorf("last event status = %q, want delivered", got.Events[2].Status)
	}
	if got.Events[2].Description != "Delivered" {
		t.Errorf("last event description = %q, want %q (tracking_detail)", got.Events[2].Description, "Delivered")
	}
	if got.Events[2].Substatus != "delivered001" {
		t.Errorf("last event substatus = %q, want delivered001 (checkpoint_delivery_substatus)", got.Events[2].Substatus)
	}
	if got.Events[2].Time.IsZero() {
		t.Error("last event time should not be zero (checkpoint_date should parse)")
	}
	for i := 1; i < len(got.Events); i++ {
		if got.Events[i].Time.Before(got.Events[i-1].Time) {
			t.Errorf("events not chronological: Events[%d].Time %v < Events[%d].Time %v",
				i, got.Events[i].Time, i-1, got.Events[i-1].Time)
		}
	}
	if got.LastUpdate.IsZero() {
		t.Error("LastUpdate should not be zero (latest_checkpoint_time should parse)")
	}
}

func TestTrackFallbackToGet(t *testing.T) {
	t.Parallel()
	createCalls, getCalls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/trackings/create":
			createCalls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"meta":{"code":4101,"type":"BadRequest","message":"Tracking already exists"},"data":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/trackings/get":
			getCalls++
			if got := r.URL.Query().Get("tracking_numbers"); got != "RR050349575PH" {
				t.Errorf("tracking_numbers = %q", got)
			}
			// The fallback GET must NOT filter by courier_code — the server
			// already has the tracking bound to whatever carrier was used
			// on first create, which may differ from the current call's
			// carrier hint. Filtering would return empty for the same number.
			if got := r.URL.Query().Get("courier_code"); got != "" {
				t.Errorf("courier_code = %q, want empty (fallback GET must not filter by courier)", got)
			}
			//nolint:lll // JSON test fixture
			_, _ = w.Write([]byte(`{"meta":{"code":200,"type":"Success","message":"Success"},"data":[` + extractData(itemDeliveredJSON) + `]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	got, err := tracker.Track(context.Background(), "phlpost", "RR050349575PH")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if createCalls != 1 || getCalls != 1 {
		t.Errorf("createCalls=%d getCalls=%d, want 1/1", createCalls, getCalls)
	}
	if got.Status != trackage.StatusDelivered {
		t.Errorf("Status = %q, want delivered", got.Status)
	}
}

// extractData isolates the "data" payload (a single tracking object)
// from the fixture so we can reuse it inside a list-shaped response.
func extractData(envelope string) string {
	start := strings.Index(envelope, `"data": {`)
	if start < 0 {
		return ""
	}
	depth, i := 0, start+len(`"data": `)
	for ; i < len(envelope); i++ {
		switch envelope[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return envelope[start+len(`"data": `) : i+1]
			}
		default:
			// non-brace characters don't affect depth
		}
	}
	return ""
}

func TestAuthErrorMapped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"meta":{"code":4001,"type":"Unauthorized","message":"Invalid API key."},"data":[]}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "bad", BaseURL: srv.URL})
	_, err := tracker.Track(context.Background(), "usps", "x")
	if !errors.Is(err, trackage.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]trackage.Status{
		"pending":      trackage.StatusPending,
		"inforeceived": trackage.StatusPending,
		"notfound":     trackage.StatusUnknown,
		"transit":      trackage.StatusInTransit,
		"pickup":       trackage.StatusInTransit,
		"undelivered":  trackage.StatusException,
		"delivered":    trackage.StatusDelivered,
		"exception":    trackage.StatusException,
		"expired":      trackage.StatusException,
		"":             trackage.StatusUnknown,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCarrierTranslation(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Verify canonical "dhl_express" → TrackingMore "dhl" translation.
		body, _ := readBody(r)
		if !strings.Contains(body, `"courier_code":"dhl"`) {
			t.Errorf("body should contain courier_code:dhl, got %s", body)
		}
		_, _ = w.Write([]byte(itemDeliveredJSON))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	if _, err := tracker.Track(context.Background(), trackage.CarrierDHLExpress, "1234567890"); err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
}

func readBody(r *http.Request) (string, error) {
	defer r.Body.Close()
	buf := make([]byte, 1024)
	n, err := r.Body.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return "", err
	}
	return string(buf[:n]), nil
}
