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

package easypost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
)

// Abridged from https://docs.easypost.com/docs/trackers.
//
//nolint:lll // JSON test fixture; readability beats wrapping.
const trackerJSON = `{
  "id": "trk_test123",
  "object": "Tracker",
  "mode": "test",
  "tracking_code": "EZ1000000001",
  "status": "pre_transit",
  "status_detail": "status_update",
  "carrier": "USPS",
  "updated_at": "2025-05-09T20:40:17Z",
  "est_delivery_date": "2025-05-12",
  "tracking_details": [
    {
      "object": "TrackingDetail",
      "message": "Pre-Shipment Info Sent to USPS",
      "description": "",
      "status": "pre_transit",
      "status_detail": "status_update",
      "datetime": "2025-04-09T20:40:17Z",
      "source": "USPS",
      "tracking_location": {"object": "TrackingLocation", "city": null, "state": null, "country": null, "zip": null}
    },
    {
      "object": "TrackingDetail",
      "message": "Picked Up by Shipping Partner, USPS Awaiting Item",
      "description": "",
      "status": "in_transit",
      "status_detail": "received_at_origin_facility",
      "datetime": "2025-04-10T15:22:00Z",
      "source": "USPS",
      "tracking_location": {"object": "TrackingLocation", "city": "HOUSTON", "state": "TX", "country": "US", "zip": "77063"}
    }
  ]
}`

func TestTrackInTransitFixture(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Basic auth: username = key, password empty.
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("EZ_test_xyz:"))
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/trackers" {
			t.Errorf("got %s %s, want POST /trackers", r.Method, r.URL.Path)
		}
		type bodyTracker struct {
			TrackingCode string `json:"tracking_code"`
			Carrier      string `json:"carrier"`
		}
		var body struct {
			Tracker bodyTracker `json:"tracker"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body.Tracker.TrackingCode != "EZ1000000001" {
			t.Errorf("tracking_code = %q, want EZ1000000001", body.Tracker.TrackingCode)
		}
		if body.Tracker.Carrier != "USPS" {
			t.Errorf("carrier = %q, want USPS (translated from canonical usps)", body.Tracker.Carrier)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(trackerJSON))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "EZ_test_xyz", BaseURL: srv.URL})
	got, err := tracker.Track(context.Background(), trackage.CarrierUSPS, "EZ1000000001")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}

	if got.Carrier != trackage.CarrierUSPS {
		t.Errorf("Carrier = %q, want %q", got.Carrier, trackage.CarrierUSPS)
	}
	if got.Status != trackage.StatusPending {
		t.Errorf("Status = %q, want %q (pre_transit→pending)", got.Status, trackage.StatusPending)
	}
	// Substatus reflects the LATEST tracking_details entry (not the
	// possibly-stale tracker-level status_detail), matching how
	// Description and LastUpdate are derived.
	if got.Substatus != "received_at_origin_facility" {
		t.Errorf("Substatus = %q, want received_at_origin_facility (from latest event)", got.Substatus)
	}
	if !strings.Contains(got.Description, "Picked Up") {
		t.Errorf("Description = %q, want it to reflect latest event", got.Description)
	}
	if len(got.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(got.Events))
	}
	if got.Events[1].Status != trackage.StatusInTransit {
		t.Errorf("Events[1].Status = %q, want %q", got.Events[1].Status, trackage.StatusInTransit)
	}
	if got.Events[1].Location != "HOUSTON, TX, 77063, US" {
		t.Errorf("Events[1].Location = %q, want %q", got.Events[1].Location, "HOUSTON, TX, 77063, US")
	}
	if got.EstDelivery == nil || got.EstDelivery.Format("2006-01-02") != "2025-05-12" {
		t.Errorf("EstDelivery = %v, want 2025-05-12", got.EstDelivery)
	}
}

func TestAutoDetect(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tracker map[string]any `json:"tracker"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		// Local detector recognizes 1Z… as UPS and translates to "UPS".
		got, ok := body.Tracker["carrier"].(string)
		if !ok || got != "UPS" {
			t.Errorf("expected carrier=UPS (detected), got %q ok=%v", got, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tracking_code":"1Z999AA10123456784",
			"carrier":"UPS","status":"in_transit","tracking_details":[]
		}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	if _, err := tracker.Track(context.Background(), "", "1Z999AA10123456784"); err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
}

func TestUnknownNumberOmitsCarrier(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		// When detection fails AND no carrier given, we should NOT send
		// a carrier field at all — let EasyPost auto-detect.
		if strings.Contains(string(raw), `"carrier"`) {
			t.Errorf("expected no carrier in body, got %s", string(raw))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tracking_code":"weirdo","status":"unknown","tracking_details":[]}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	if _, err := tracker.Track(context.Background(), "", "weirdo"); err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED","message":"Bad key"}}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tracker.Track(context.Background(), trackage.CarrierUSPS, "EZ1000000001")
	if !errors.Is(err, trackage.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
	var ae *trackage.APIError
	if !errors.As(err, &ae) || ae.Code != "UNAUTHORIZED" {
		t.Errorf("expected APIError code=UNAUTHORIZED, got %+v", ae)
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	//nolint:misspell // EasyPost emits "cancelled"; we test both spellings.
	cases := map[string]trackage.Status{
		"pre_transit":          trackage.StatusPending,
		"in_transit":           trackage.StatusInTransit,
		"out_for_delivery":     trackage.StatusInTransit,
		"available_for_pickup": trackage.StatusInTransit,
		"delivered":            trackage.StatusDelivered,
		"return_to_sender":     trackage.StatusException,
		"failure":              trackage.StatusException,
		"cancelled":            trackage.StatusException,
		"canceled":             trackage.StatusException,
		"error":                trackage.StatusException,
		"unknown":              trackage.StatusUnknown,
		"":                     trackage.StatusUnknown,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
