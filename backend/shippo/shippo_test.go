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

package shippo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"msrl.dev/trackage"
)

// Verbatim example from https://docs.goshippo.com/docs/tracking/tracking
const trackedDeliveredJSON = `{
  "carrier": "usps",
  "tracking_number": "9205590164917312751089",
  "address_from": {"city": "Las Vegas", "state": "NV", "zip": "89101", "country": "US"},
  "address_to":   {"city": "Spotsylvania", "state": "VA", "zip": "22551", "country": "US"},
  "transaction": "1275c67d754f45bf9d6e4d7a3e205314",
  "original_eta": "2023-07-23T00:00:00Z",
  "eta": "2023-07-23T00:00:00Z",
  "servicelevel": {"token": "usps_priority", "name": "Priority Mail"},
  "metadata": null,
  "tracking_status": {
    "object_created": "2023-07-23T20:35:26.129Z",
    "object_updated": "2023-07-23T20:35:26.129Z",
    "object_id": "ce48ff3d52a34e91b77aa98370182624",
    "status": "DELIVERED",
    "substatus": {"code": "delivered", "text": "Delivered.", "action_required": false},
    "status_details": "Your shipment has been delivered at the destination mailbox.",
    "status_date": "2023-07-23T13:03:00Z",
    "location": {"city": "Spotsylvania", "state": "VA", "zip": "22551", "country": "US"}
  },
  "tracking_history": [
    {
      "object_created": "2023-07-22T14:36:50.943Z",
      "object_id": "265c7a7c23354da5b87b2bf52656c625",
      "status": "TRANSIT",
      "substatus": {"code": "package_accepted", "text": "Accepted.", "action_required": false},
      "status_details": "Your shipment has been accepted.",
      "status_date": "2023-07-21T15:33:00Z",
      "location": {"city": "Las Vegas", "state": "NV", "zip": "89101", "country": "US"}
    },
    {
      "object_created": "2023-07-23T20:35:26.129Z",
      "object_id": "aab1d7c0559d43ccbba4ff8603089e56",
      "status": "DELIVERED",
      "substatus": {"code": "delivered", "text": "Delivered.", "action_required": false},
      "status_details": "Your shipment has been delivered at the destination mailbox.",
      "status_date": "2023-07-23T13:03:00Z",
      "location": {"city": "Spotsylvania", "state": "VA", "zip": "22551", "country": "US"}
    }
  ]
}`

func TestTrackDeliveredFixture(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "ShippoToken shippo_test_xyz" {
			t.Errorf("Authorization header = %q, want ShippoToken shippo_test_xyz", got)
		}
		if r.URL.Path != "/tracks/usps/9205590164917312751089" {
			t.Errorf("path = %q, want /tracks/usps/9205590164917312751089", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(trackedDeliveredJSON))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "shippo_test_xyz", BaseURL: srv.URL})
	got, err := tracker.Track(context.Background(), trackage.CarrierUSPS, "9205590164917312751089")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}

	if got.Carrier != trackage.CarrierUSPS {
		t.Errorf("Carrier = %q, want %q", got.Carrier, trackage.CarrierUSPS)
	}
	if got.Status != trackage.StatusDelivered {
		t.Errorf("Status = %q, want %q", got.Status, trackage.StatusDelivered)
	}
	if got.Substatus != "delivered" {
		t.Errorf("Substatus = %q, want %q", got.Substatus, "delivered")
	}
	if !strings.Contains(got.Description, "delivered") {
		t.Errorf("Description = %q, want it to mention delivered", got.Description)
	}
	if got.LastUpdate.IsZero() {
		t.Error("LastUpdate is zero, want 2023-07-23T13:03:00Z")
	}
	if got.EstDelivery == nil {
		t.Error("EstDelivery is nil, want 2023-07-23T00:00:00Z")
	}
	if len(got.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(got.Events))
	}
	if got.Events[0].Status != trackage.StatusInTransit {
		t.Errorf("Events[0].Status = %q, want %q", got.Events[0].Status, trackage.StatusInTransit)
	}
	if got.Events[0].Location != "Las Vegas, NV, 89101, US" {
		t.Errorf("Events[0].Location = %q, want %q", got.Events[0].Location, "Las Vegas, NV, 89101, US")
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]trackage.Status{
		"PRE_TRANSIT": trackage.StatusPending,
		"TRANSIT":     trackage.StatusInTransit,
		"DELIVERED":   trackage.StatusDelivered,
		"RETURNED":    trackage.StatusException,
		"FAILURE":     trackage.StatusException,
		"UNKNOWN":     trackage.StatusUnknown,
		"":            trackage.StatusUnknown,
		"weird":       trackage.StatusUnknown,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCarrierRequiredWhenNoDetection(t *testing.T) {
	t.Parallel()
	tracker := New(Config{APIKey: "k"})
	_, err := tracker.Track(context.Background(), "", "definitely-not-a-tracking-number")
	if !errors.Is(err, trackage.ErrCarrierRequired) {
		t.Errorf("expected ErrCarrierRequired, got %v", err)
	}
}

func TestCarrierAutoDetected(t *testing.T) {
	t.Parallel()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// "1Z..." UPS pattern should be resolved to canonical "ups" → Shippo "ups".
		if r.URL.Path != "/tracks/ups/1Z999AA10123456784" {
			t.Errorf("path = %q, want /tracks/ups/1Z999AA10123456784", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		//nolint:lll // JSON test fixture
		_, _ = w.Write([]byte(`{"carrier":"ups","tracking_number":"1Z999AA10123456784","tracking_status":{"status":"UNKNOWN"}}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tracker.Track(context.Background(), "", "1Z999AA10123456784")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if !called {
		t.Error("server was not called")
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Not found"}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	_, err := tracker.Track(context.Background(), trackage.CarrierUSPS, "1234")
	if !errors.Is(err, trackage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	var ae *trackage.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *trackage.APIError, got %T", err)
	}
	if ae.Message != "Not found" {
		t.Errorf("APIError.Message = %q, want %q", ae.Message, "Not found")
	}
}
