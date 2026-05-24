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

package trackage

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// Status is the canonical, backend-agnostic shipment status.
//
// trackage intentionally keeps this enum small. Provider-specific detail
// is preserved verbatim in [Tracking.Substatus] (and the per-event
// [Event.Substatus]), and the full upstream response is available in
// [Tracking.Raw] as an escape hatch.
type Status string

const (
	// StatusPending means the carrier has the label / pre-advice but has
	// not yet scanned the parcel into its network (Shippo PRE_TRANSIT,
	// EasyPost pre_transit, 17Track InfoReceived, TrackingMore
	// pending / inforeceived).
	StatusPending Status = "pending"

	// StatusInTransit covers everything between first scan and final
	// delivery, including out-for-delivery and available-for-pickup.
	StatusInTransit Status = "in_transit"

	// StatusDelivered means the carrier reports successful delivery.
	StatusDelivered Status = "delivered"

	// StatusException covers returns, delivery failures, customs holds,
	// lost / damaged parcels, and any other terminal-but-unhappy state.
	StatusException Status = "exception"

	// StatusUnknown is used when the backend has not yet produced data
	// (common immediately after registration on async backends such as
	// 17Track) or reports the parcel as not found.
	StatusUnknown Status = "unknown"
)

// AllStatuses returns the canonical status values in display order.
func AllStatuses() []Status {
	return []Status{StatusPending, StatusInTransit, StatusDelivered, StatusException, StatusUnknown}
}

// Tracking is the normalized snapshot of a parcel's progress.
//
// Field semantics:
//   - Carrier and TrackingNumber identify the parcel using trackage's
//     canonical lowercase carrier id (e.g. "usps", "dhl_express"); see
//     package carriers for the translation tables.
//   - Status is the canonical [Status] mapped from the backend's own enum.
//   - Substatus is the verbatim, backend-specific detail code if any
//     (e.g. Shippo "package_accepted", 17Track "InTransit_PickedUp",
//     TrackingMore "transit003"). Empty if the backend does not provide one.
//   - Description is a single human-readable sentence summarizing the
//     latest event.
//   - LastUpdate is the timestamp of the most recent event. The
//     Location reflects the offset the backend supplied (see the
//     "Timestamps preserve the upstream zone" note in docs/design.md);
//     callers needing UTC can call t.UTC().
//   - EstDelivery is the carrier's estimated delivery time, or nil if not
//     reported.
//   - Events lists checkpoints in chronological order (oldest first).
//   - Raw is the upstream JSON body verbatim, for callers that need a
//     field trackage did not surface.
type Tracking struct {
	Carrier        string          `json:"carrier"`
	TrackingNumber string          `json:"tracking_number"`
	Status         Status          `json:"status"`
	Substatus      string          `json:"substatus,omitempty"`
	Description    string          `json:"description,omitempty"`
	LastUpdate     time.Time       `json:"last_update,omitzero"`
	EstDelivery    *time.Time      `json:"est_delivery,omitempty"`
	Events         []Event         `json:"events,omitempty"`
	Raw            json.RawMessage `json:"raw,omitempty"`
}

// Event is a single carrier checkpoint.
type Event struct {
	Time        time.Time `json:"time"`
	Status      Status    `json:"status"`
	Substatus   string    `json:"substatus,omitempty"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
}

// Tracker is the common interface implemented by every backend.
//
// Track fetches (and, where required by the backend, registers) the
// current state of a parcel. Carrier must be a canonical trackage id;
// pass the empty string to ask backends that support it (EasyPost,
// 17Track, TrackingMore) to auto-detect the carrier from the number.
type Tracker interface {
	Track(ctx context.Context, carrier, number string) (*Tracking, error)

	// Name returns the backend identifier (e.g. "shippo").
	Name() string
}

// Sentinel errors returned by backends. Callers may use errors.Is to
// distinguish them from transport / parsing failures.
var (
	// ErrNotFound is returned when the backend has no record of the
	// tracking number (HTTP 404 or equivalent).
	ErrNotFound = errors.New("trackage: tracking number not found")

	// ErrUnsupportedCarrier is returned when the backend does not
	// recognize the requested canonical carrier id.
	ErrUnsupportedCarrier = errors.New("trackage: carrier not supported by this backend")

	// ErrCarrierRequired is returned when the caller passed an empty
	// carrier id to a backend that cannot auto-detect (Shippo).
	ErrCarrierRequired = errors.New("trackage: this backend requires an explicit carrier")

	// ErrAuth is returned when the backend rejects credentials.
	ErrAuth = errors.New("trackage: authentication failed")

	// ErrRateLimited is returned for HTTP 429 responses; callers may
	// retry after backing off.
	ErrRateLimited = errors.New("trackage: rate limited")
)

// APIError wraps a non-2xx HTTP response from a backend. Backends should
// return APIError (possibly wrapping one of the sentinel errors above
// via Cause) so callers see both the friendly category and the raw
// upstream detail.
type APIError struct {
	Backend    string // e.g. "shippo"
	StatusCode int    // HTTP status code
	Code       string // backend-specific error code, if any
	Message    string // human-readable message from the backend
	Cause      error  // sentinel (ErrAuth, ErrRateLimited, ...), if applicable
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return e.Backend + ": " + e.Code + ": " + e.Message
	case e.Message != "":
		return e.Backend + ": " + e.Message
	case e.Code != "":
		return e.Backend + ": " + e.Code
	default:
		return e.Backend + ": http " + strconv.Itoa(e.StatusCode)
	}
}

func (e *APIError) Unwrap() error { return e.Cause }
