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

// Canonical trackage carrier identifiers (lowercase snake_case).
//
// The list is deliberately short: only carriers whose tracking numbers
// trackage can locally detect (see detect.go) appear here. Anything
// else is passed through to the backend verbatim — callers are not
// limited to this list, they just lose the cross-backend translation.
const (
	CarrierUSPS           = "usps"
	CarrierUPS            = "ups"
	CarrierFedEx          = "fedex"
	CarrierDHLExpress     = "dhl_express"
	CarrierRoyalMail      = "royal_mail"
	CarrierCanadaPost     = "canada_post"
	CarrierAustraliaPost  = "australia_post"
	CarrierLaPoste        = "la_poste"
	CarrierDeutschePost   = "deutsche_post"
	CarrierChinaPost      = "china_post"
	CarrierJapanPost      = "japan_post"
	CarrierPosteItaliane  = "poste_italiane"
	CarrierCorreiosBrazil = "correios_brazil"
)

// Carrier holds the backend-specific code for each canonical id.
// An empty / zero field means trackage does not know the code for that
// backend; that backend will refuse the lookup and report
// ErrUnsupportedCarrier (callers can still pass the backend-native code
// directly to work around it).
type Carrier struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Shippo         string `json:"shippo,omitempty"`
	EasyPost       string `json:"easypost,omitempty"`
	SeventeenTrack int    `json:"17track,omitempty"`
	TrackingMore   string `json:"trackingmore,omitempty"`
}

var carriers = []Carrier{
	{CarrierUSPS, "USPS", "usps", "USPS", 21051, "usps"},
	{CarrierUPS, "UPS", "ups", "UPS", 100002, "ups"},
	{CarrierFedEx, "FedEx", "fedex", "FedEx", 100003, "fedex"},
	{CarrierDHLExpress, "DHL Express", "dhl_express", "DHLExpress", 100001, "dhl"},
	{CarrierRoyalMail, "Royal Mail", "royal_mail", "RoyalMail", 11031, "royal-mail"},
	{CarrierCanadaPost, "Canada Post", "canada_post", "CanadaPost", 3041, "canada-post"},
	{CarrierAustraliaPost, "Australia Post", "australia_post", "AustraliaPost", 1151, "australia-post"},
	{CarrierLaPoste, "La Poste", "la_poste", "LaPoste", 6051, "la-poste"},
	{CarrierDeutschePost, "Deutsche Post", "deutsche_post", "DeutschePost", 7044, "deutsche-post"},
	{CarrierChinaPost, "China Post", "", "ChinaPost", 3011, "china-post"},
	{CarrierJapanPost, "Japan Post", "", "JapanPost", 10021, "japan-post"},
	{CarrierPosteItaliane, "Poste Italiane", "poste_italiane", "PosteItaliane", 9071, "poste-italiane"},
	{CarrierCorreiosBrazil, "Correios Brazil", "correios_br", "Correios", 2151, "correios-brazil"},
}

var carriersByID = func() map[string]Carrier {
	m := make(map[string]Carrier, len(carriers))
	for _, c := range carriers {
		m[c.ID] = c
	}
	return m
}()

// LookupCarrier returns the master record for a canonical trackage id.
// Returns ok=false for unknown ids.
func LookupCarrier(id string) (Carrier, bool) {
	c, ok := carriersByID[id]
	return c, ok
}

// AllCarriers returns the full known-carrier table in display order.
// The returned slice is a copy; callers may sort/filter it freely.
func AllCarriers() []Carrier {
	out := make([]Carrier, len(carriers))
	copy(out, carriers)
	return out
}
