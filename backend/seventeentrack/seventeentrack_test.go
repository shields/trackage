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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"msrl.dev/trackage"
)

//nolint:lll // JSON test fixture; readability beats wrapping.
const trackInfoJSON = `{
  "code": 0,
  "data": {
    "accepted": [{
      "number": "RR123456789CN",
      "carrier": 3011,
      "tag": "",
      "track_info": {
        "latest_status": {"status": "Delivered", "sub_status": "Delivered_Other", "sub_status_descr": null},
        "latest_event": {
          "time_iso": "2022-05-02T14:34:00-04:00",
          "time_utc": "2022-05-02T18:34:00Z",
          "description": "Package delivered",
          "location": "LOS ANGELES, CA",
          "stage": "Delivered",
          "sub_status": "Delivered_Other",
          "address": {"country": "US", "state": "CA", "city": "LOS ANGELES", "postal_code": "90001"}
        },
        "time_metrics": {
          "estimated_delivery_date": {"source": "Official", "from": "2022-05-01T08:00:00-04:00", "to": "2022-05-03T20:00:00-04:00"}
        },
        "tracking": {
          "providers": [{
            "provider": {"key": 3011, "name": "China Post"},
            "events": [
              {"time_iso": "2022-05-02T14:34:00-04:00", "time_utc": "2022-05-02T18:34:00Z", "description": "Package delivered", "location": "LOS ANGELES, CA", "stage": "Delivered", "sub_status": "Delivered_Other", "address": {"country": "US", "city": "LOS ANGELES"}},
              {"time_iso": "2022-04-28T09:00:00-04:00", "time_utc": "2022-04-28T13:00:00Z", "description": "Departed from facility", "location": "LOS ANGELES, CA", "stage": null, "sub_status": "InTransit_Other", "address": {"country": "US", "city": "LOS ANGELES"}},
              {"time_iso": "2022-04-26T14:30:00+08:00", "time_utc": "2022-04-26T06:30:00Z", "description": "Package picked up", "location": "SHENZHEN", "stage": "PickedUp", "sub_status": "InTransit_PickedUp", "address": {"country": "CN", "city": "SHENZHEN"}},
              {"time_iso": "2022-04-25T10:00:00+08:00", "time_utc": "2022-04-25T02:00:00Z", "description": "Order information received", "location": "SHENZHEN", "stage": "InfoReceived", "sub_status": "InfoReceived", "address": {"country": "CN", "city": "SHENZHEN"}}
            ]
          }]
        }
      }
    }],
    "rejected": []
  }
}`

func TestTrackFullFixture(t *testing.T) {
	t.Parallel()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("17token"); got != "secret-key" {
			t.Errorf("17token header = %q, want secret-key", got)
		}
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			calls++
			// Expect carrier=3011 (china_post) — translated from canonical.
			var body []map[string]any
			_ = json.Unmarshal(raw, &body)
			carrier, ok := body[0]["carrier"].(float64)
			if len(body) != 1 || !ok || carrier != 3011 {
				t.Errorf("register body = %s", raw)
			}
			_, _ = w.Write([]byte(
				`{"code":0,"data":{"accepted":[{"number":"RR123456789CN","carrier":3011,"origin":2}],"rejected":[]}}`,
			))
		case "/track/v2.2/gettrackinfo":
			calls++
			_, _ = w.Write([]byte(trackInfoJSON))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "secret-key", BaseURL: srv.URL})
	got, err := tracker.Track(context.Background(), trackage.CarrierChinaPost, "RR123456789CN")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected register + gettrackinfo (2 calls), got %d", calls)
	}

	if got.Carrier != trackage.CarrierChinaPost {
		t.Errorf("Carrier = %q, want %q", got.Carrier, trackage.CarrierChinaPost)
	}
	if got.Status != trackage.StatusDelivered {
		t.Errorf("Status = %q, want %q", got.Status, trackage.StatusDelivered)
	}
	if got.Substatus != "Delivered_Other" {
		t.Errorf("Substatus = %q, want Delivered_Other", got.Substatus)
	}
	if got.Description != "Package delivered" {
		t.Errorf("Description = %q, want %q", got.Description, "Package delivered")
	}
	if len(got.Events) != 4 {
		t.Fatalf("len(Events) = %d, want 4", len(got.Events))
	}
	if got.Events[0].Status != trackage.StatusPending {
		t.Errorf("Events[0].Status = %q, want %q (InfoReceived)", got.Events[0].Status, trackage.StatusPending)
	}
	if got.Events[1].Status != trackage.StatusInTransit || got.Events[1].Substatus != "InTransit_PickedUp" {
		t.Errorf("Events[1] = %q/%q, want in_transit/InTransit_PickedUp (stage=PickedUp + sub_status=InTransit_PickedUp)",
			got.Events[1].Status, got.Events[1].Substatus)
	}
	if got.Events[2].Status != trackage.StatusInTransit || got.Events[2].Substatus != "InTransit_Other" {
		t.Errorf("Events[2] = %q/%q, want in_transit/InTransit_Other (stage=null + sub_status=InTransit_Other)",
			got.Events[2].Status, got.Events[2].Substatus)
	}
	if got.Events[3].Status != trackage.StatusDelivered {
		t.Errorf("Events[3].Status = %q, want %q", got.Events[3].Status, trackage.StatusDelivered)
	}
	for i := 1; i < len(got.Events); i++ {
		if got.Events[i].Time.Before(got.Events[i-1].Time) {
			t.Errorf("events not in chronological order: Events[%d].Time %v is before Events[%d].Time %v",
				i, got.Events[i].Time, i-1, got.Events[i-1].Time)
		}
	}
}

//nolint:lll,dupl // JSON test fixtures; structure overlaps with TestGetInfoEmptyAccepted but the assertions differ
func TestAlreadyRegisteredIsSwallowed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/track/v2.2/register":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[],"rejected":[{"number":"x","error":{"code":-18019901,"message":"Tracking number already registered."}}]}}`))
		case "/track/v2.2/gettrackinfo":
			_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"x","carrier":21051,"track_info":{"latest_status":{"status":"InTransit","sub_status":"InTransit_Other"}}}],"rejected":[]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "k", BaseURL: srv.URL})
	got, err := tracker.Track(context.Background(), trackage.CarrierUSPS, "x")
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if got.Status != trackage.StatusInTransit {
		t.Errorf("Status = %q, want in_transit", got.Status)
	}
}

func TestAuthErrorMapped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-18010002,"data":{"errors":[{"code":-18010002,"message":"Invalid security key."}]}}`))
	}))
	defer srv.Close()

	tracker := New(Config{APIKey: "wrong", BaseURL: srv.URL})
	_, err := tracker.Track(context.Background(), trackage.CarrierUSPS, "x")
	if !errors.Is(err, trackage.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]trackage.Status{
		"NotFound":           trackage.StatusUnknown,
		"InfoReceived":       trackage.StatusPending,
		"InTransit":          trackage.StatusInTransit,
		"PickedUp":           trackage.StatusInTransit,
		"Departure":          trackage.StatusInTransit,
		"Arrival":            trackage.StatusInTransit,
		"OutForDelivery":     trackage.StatusInTransit,
		"AvailableForPickup": trackage.StatusInTransit,
		"Delivered":          trackage.StatusDelivered,
		"Expired":            trackage.StatusException,
		"DeliveryFailure":    trackage.StatusException,
		"Exception":          trackage.StatusException,
		"Returning":          trackage.StatusException,
		"Returned":           trackage.StatusException,
		"":                   trackage.StatusUnknown,
		"WhatEver":           trackage.StatusUnknown,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStageMapping(t *testing.T) {
	t.Parallel()
	if got := mapStatusFromStage("InTransit_PickedUp"); got != trackage.StatusInTransit {
		t.Errorf("mapStatusFromStage(InTransit_PickedUp) = %q, want in_transit", got)
	}
	if got := mapStatusFromStage("Exception_Returning"); got != trackage.StatusException {
		t.Errorf("mapStatusFromStage(Exception_Returning) = %q, want exception", got)
	}
}
